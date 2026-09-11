package connector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func testLucidClient(t *testing.T, restURL, scimURL string) *client.LucidchartClient {
	t.Helper()
	return testLucidClientWithContentToken(t, restURL, scimURL, "")
}

func testLucidClientWithContentToken(t *testing.T, restURL, scimURL, contentScimToken string) *client.LucidchartClient {
	t.Helper()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauth-test-token"}) //nolint:gosec // G101: test token literal
	c, err := client.NewLucidchartClient(context.Background(), "api-key", ts, restURL, "scim-test-token", scimURL, contentScimToken)
	require.NoError(t, err)
	return c
}

// deleteRoutes records which paths a Delete touched, so tests can assert both
// what was called and what was not.
type deleteRoutes struct {
	getUser      bool
	transfer     bool
	scimGet      bool
	scimDelete   bool
	scimDeleteID string
}

// newDeleteServer serves the three endpoints Delete touches. restUserStatus is
// the code GET /v1/users/{id} answers; scimGetStatus is what SCIM GET answers;
// scimDeleteStatus is what SCIM DELETE answers.
func newDeleteServer(t *testing.T, routes *deleteRoutes, restUserStatus, scimGetStatus, scimDeleteStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/42":
			routes.getUser = true
			if restUserStatus != http.StatusOK {
				w.WriteHeader(restUserStatus)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"userId":42,"email":"leaver@example.com","enabled":true}`))

		case r.Method == http.MethodPost && r.URL.Path == "/v1/transferUserContent":
			routes.transfer = true
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodGet && r.URL.Path == "/Users/lucid-42":
			routes.scimGet = true
			w.WriteHeader(scimGetStatus)

		case r.Method == http.MethodDelete && r.URL.Path == "/Users/lucid-42":
			routes.scimDelete = true
			routes.scimDeleteID = r.URL.Path
			w.WriteHeader(scimDeleteStatus)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
}

// deleteUser runs Delete against srv. Returns the Delete error only; the
// annotations are not asserted by these tests.
func deleteUser(t *testing.T, srv *httptest.Server, transferEmail string) error {
	t.Helper()
	c := testLucidClient(t, srv.URL, srv.URL)
	b := newUserBuilder(c, transferEmail)
	_, err := b.Delete(context.Background(), &v2.ResourceId{Resource: "42"}, nil)
	return err
}

// Lucid's GET /v1/users/{id} answers 403 — never 404 — for a user that does not
// exist. Delete must still complete, or every retry of an already-processed
// deprovision fails forever.
func TestDelete_RestForbiddenAndUserGone_ProceedsToScimDelete(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusForbidden, http.StatusNotFound, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "recipient@example.com")
	require.NoError(t, err)

	require.True(t, routes.getUser, "REST lookup should be attempted")
	require.True(t, routes.scimGet, "SCIM must be consulted to disambiguate the 403")
	require.False(t, routes.transfer, "no transfer is possible for a user that is gone")
	require.True(t, routes.scimDelete, "delete must still run so retries converge")
}

// A 403 with the user still present is a scope problem, not an absence. Deleting
// would destroy the content the operator asked to retain.
func TestDelete_RestForbiddenButUserExists_RefusesToDelete(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusForbidden, http.StatusOK, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "recipient@example.com")
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.False(t, routes.transfer)
	require.False(t, routes.scimDelete, "must not delete when content could not be transferred")
}

// A transient SCIM probe failure after an ambiguous 403 must abort the delete
// with a retryable code, not run SCIM DELETE.
func TestDelete_RestForbiddenAndScimProbeTransient_ReturnsRetryableCode(t *testing.T) {
	cases := []struct {
		probeStatus int
		wantCode    codes.Code
	}{
		{http.StatusTooManyRequests, codes.Unavailable},
		{http.StatusServiceUnavailable, codes.Unavailable},
		{http.StatusInternalServerError, codes.Unavailable},
		{http.StatusRequestTimeout, codes.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.probeStatus), func(t *testing.T) {
			routes := &deleteRoutes{}
			srv := newDeleteServer(t, routes, http.StatusForbidden, tc.probeStatus, http.StatusNoContent)
			defer srv.Close()

			err := deleteUser(t, srv, "recipient@example.com")
			require.Error(t, err)
			require.Equal(t, tc.wantCode, status.Code(err),
				"a transient probe failure must surface as a retryable code")
			require.True(t, routes.getUser, "REST lookup should be attempted")
			require.True(t, routes.scimGet, "SCIM must be probed to disambiguate the 403")
			require.False(t, routes.transfer, "no transfer when the email could not be resolved")
			require.False(t, routes.scimDelete, "delete must not run when existence could not be confirmed")
		})
	}
}

// A non-retryable, indeterminate SCIM probe failure after an ambiguous 403
// must abort with codes.Unknown, not run SCIM DELETE.
func TestDelete_RestForbiddenAndScimProbeIndeterminate_ReturnsUnknown(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusForbidden, http.StatusBadRequest, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "recipient@example.com")
	require.Error(t, err)
	require.Equal(t, codes.Unknown, status.Code(err))
	require.True(t, routes.getUser, "REST lookup should be attempted")
	require.True(t, routes.scimGet, "SCIM must be probed to disambiguate the 403")
	require.False(t, routes.transfer, "no transfer when the email could not be resolved")
	require.False(t, routes.scimDelete, "delete must not run when existence could not be confirmed")
}

// Cancelling the sync while the SCIM probe is in flight must abort the delete
// with an error still matchable via errors.Is(err, context.Canceled).
func TestDelete_RestForbiddenAndProbeCancelled_PreservesContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	routes := &deleteRoutes{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/42":
			routes.getUser = true
			w.WriteHeader(http.StatusForbidden)
		case r.Method == http.MethodGet && r.URL.Path == "/Users/lucid-42":
			routes.scimGet = true
			cancel() // cancels mid-probe so ScimUserExists returns a context error
			<-r.Context().Done()
		case r.Method == http.MethodDelete && r.URL.Path == "/Users/lucid-42":
			routes.scimDelete = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := testLucidClient(t, srv.URL, srv.URL)
	b := newUserBuilder(c, "recipient@example.com")
	_, err := b.Delete(ctx, &v2.ResourceId{Resource: "42"}, nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled),
		"cancellation must remain detectable via errors.Is, got %v", err)
	require.True(t, routes.scimGet, "SCIM must be probed to disambiguate the 403")
	require.False(t, routes.transfer, "no transfer when the probe was cancelled")
	require.False(t, routes.scimDelete, "delete must not run when the probe was cancelled")
}

// An undocumented REST 404 must be confirmed via SCIM before the delete
// proceeds.
func TestDelete_GetUserNotFoundAndScimUserGone_ProbesThenRunsScimDelete(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusNotFound, http.StatusNotFound, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "recipient@example.com")
	require.NoError(t, err)
	require.True(t, routes.getUser, "REST lookup should be attempted")
	require.True(t, routes.scimGet, "an undocumented 404 must be disambiguated via SCIM")
	require.False(t, routes.transfer, "transfer must be skipped when the user is not found")
	require.True(t, routes.scimDelete, "delete must run once SCIM confirms the user is gone")
}

// An undocumented REST 404 with the user still present per SCIM must refuse
// the delete rather than destroy their content.
func TestDelete_GetUserNotFoundButScimUserExists_RefusesToDelete(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusNotFound, http.StatusOK, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "recipient@example.com")
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.True(t, routes.scimGet, "an undocumented 404 must be disambiguated via SCIM")
	require.False(t, routes.transfer)
	require.False(t, routes.scimDelete, "must not delete a user SCIM says still exists")
}

// A transient SCIM probe failure on the undocumented-404 path must abort with
// a retryable code, not run SCIM DELETE.
func TestDelete_GetUserNotFoundAndScimProbeTransient_AbortsWithRetryableCode(t *testing.T) {
	cases := []struct {
		probeStatus int
		wantCode    codes.Code
	}{
		{http.StatusTooManyRequests, codes.Unavailable},
		{http.StatusServiceUnavailable, codes.Unavailable},
		{http.StatusInternalServerError, codes.Unavailable},
		{http.StatusRequestTimeout, codes.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.probeStatus), func(t *testing.T) {
			routes := &deleteRoutes{}
			srv := newDeleteServer(t, routes, http.StatusNotFound, tc.probeStatus, http.StatusNoContent)
			defer srv.Close()

			err := deleteUser(t, srv, "recipient@example.com")
			require.Error(t, err)
			require.Equal(t, tc.wantCode, status.Code(err),
				"a transient probe failure must surface as a retryable code")
			require.True(t, routes.scimGet, "an undocumented 404 must attempt the SCIM probe")
			require.False(t, routes.transfer, "no transfer when the email could not be resolved")
			require.False(t, routes.scimDelete,
				"must not hard-delete an unconfirmed user on the strength of a SCIM outage")
		})
	}
}

// Cancellation during the probe on the undocumented-404 path must abort with
// an error still matchable via errors.Is(err, context.Canceled).
func TestDelete_GetUserNotFoundAndProbeCancelled_PreservesContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	routes := &deleteRoutes{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/42":
			routes.getUser = true
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/Users/lucid-42":
			routes.scimGet = true
			cancel()
			<-r.Context().Done()
		case r.Method == http.MethodDelete && r.URL.Path == "/Users/lucid-42":
			routes.scimDelete = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := testLucidClient(t, srv.URL, srv.URL)
	b := newUserBuilder(c, "recipient@example.com")
	_, err := b.Delete(ctx, &v2.ResourceId{Resource: "42"}, nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled),
		"cancellation must remain detectable via errors.Is, got %v", err)
	require.False(t, routes.scimDelete, "delete must not run when the probe was cancelled")
}

// A non-retryable, indeterminate probe failure on the undocumented-404 path
// must refuse the delete, mirroring the ambiguous-403 case.
func TestDelete_GetUserNotFoundAndScimProbeIndeterminate_RefusesToDelete(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusNotFound, http.StatusBadRequest, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "recipient@example.com")
	require.Error(t, err)
	require.Equal(t, codes.Unknown, status.Code(err))
	require.True(t, routes.scimGet, "an undocumented 404 must attempt the SCIM probe")
	require.False(t, routes.transfer)
	require.False(t, routes.scimDelete,
		"an indeterminate probe never confirmed absence, so the hard delete must not run")
}

func TestDelete_HappyPath_TransfersThenDeletes(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusOK, http.StatusOK, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "recipient@example.com")
	require.NoError(t, err)

	require.True(t, routes.transfer, "content must be transferred before delete")
	require.True(t, routes.scimDelete)
	require.Equal(t, "/Users/lucid-42", routes.scimDeleteID)
	require.False(t, routes.scimGet, "no SCIM probe needed when REST answered")
}

// Without a transfer email there is nothing to resolve, so Delete must not call
// REST at all.
func TestDelete_NoTransferEmail_SkipsRestLookup(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusOK, http.StatusOK, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "")
	require.NoError(t, err)

	require.False(t, routes.getUser)
	require.False(t, routes.transfer)
	require.True(t, routes.scimDelete)
}

// Lucid answers 409 for a user that can never be deleted. Surfacing that as
// AlreadyExists would read as an idempotent success and hide a failed
// offboarding.
func TestDelete_ScimConflict_IsTerminalNotAlreadyExists(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusOK, http.StatusOK, http.StatusConflict)
	defer srv.Close()

	err := deleteUser(t, srv, "")
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.NotEqual(t, codes.AlreadyExists, status.Code(err))
	require.Contains(t, err.Error(), "account owner")
}

func TestUserResource_ReportsDisabledAndUsername(t *testing.T) {
	res, err := userResource(client.User{
		AccountId: 1,
		Email:     "disabled@example.com",
		Name:      "Dana Disabled",
		UserId:    105,
		Username:  "disabled@example.com",
		Enabled:   boolPtr(false),
		Roles:     []string{"developer"},
	})
	require.NoError(t, err)

	require.Equal(t, v2.Status_RESOURCE_STATUS_DISABLED, res.GetStatus().GetStatus())

	profile := res.GetProfile().AsMap()
	require.Equal(t, "disabled@example.com", profile["username"])
}

func TestUserResource_EnabledUserIsEnabled(t *testing.T) {
	res, err := userResource(client.User{Email: "a@example.com", UserId: 1, Enabled: boolPtr(true)})
	require.NoError(t, err)
	require.Equal(t, v2.Status_RESOURCE_STATUS_ENABLED, res.GetStatus().GetStatus())
}

func TestUserResource_MissingEnabledDefaultsToEnabled(t *testing.T) {
	res, err := userResource(client.User{Email: "a@example.com", UserId: 1})
	require.NoError(t, err)
	require.Equal(t, v2.Status_RESOURCE_STATUS_ENABLED, res.GetStatus().GetStatus())
}

func boolPtr(b bool) *bool { return &b }

// --- Second SCIM integration ("SCIM for content access") ---------------------
//
// Both integrations are served from the same base URL and the same
// /Users/{id} path; only the bearer token differs. These tests therefore route
// on the Authorization header, which is exactly what Lucid does.

const (
	adminScimAuth   = "Bearer scim-test-token"
	contentScimAuth = "Bearer content-scim-test-token"
	contentScimTok  = "content-scim-test-token"
)

// dualScimRoutes records the SCIM deletes seen per integration.
type dualScimRoutes struct {
	adminDeletes   int
	contentDeletes int
	unknownAuth    []string
}

// newDualScimServer answers DELETE /Users/lucid-42 differently depending on
// which SCIM bearer token the request carries.
func newDualScimServer(t *testing.T, routes *dualScimRoutes, adminStatus, contentStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/Users/lucid-42" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		switch r.Header.Get("Authorization") {
		case adminScimAuth:
			routes.adminDeletes++
			w.WriteHeader(adminStatus)
		case contentScimAuth:
			routes.contentDeletes++
			w.WriteHeader(contentStatus)
		default:
			routes.unknownAuth = append(routes.unknownAuth, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
}

func deleteUserWithContentToken(t *testing.T, srv *httptest.Server, contentScimToken string) error {
	t.Helper()
	c := testLucidClientWithContentToken(t, srv.URL, srv.URL, contentScimToken)
	b := newUserBuilder(c, "")
	_, err := b.Delete(context.Background(), &v2.ResourceId{Resource: "42"}, nil)
	return err
}

// With no content-access token configured, Delete must behave exactly as before:
// one SCIM delete, against the admin-management integration only.
func TestDelete_NoContentScimToken_DeletesFromAdminManagementOnly(t *testing.T) {
	routes := &dualScimRoutes{}
	srv := newDualScimServer(t, routes, http.StatusNoContent, http.StatusNoContent)
	defer srv.Close()

	err := deleteUserWithContentToken(t, srv, "")
	require.NoError(t, err)
	require.Equal(t, 1, routes.adminDeletes)
	require.Zero(t, routes.contentDeletes, "content-access delete must not be attempted when no token is configured")
	require.Empty(t, routes.unknownAuth)
}

func TestDelete_ContentScimToken_DeletesFromBothIntegrations(t *testing.T) {
	routes := &dualScimRoutes{}
	srv := newDualScimServer(t, routes, http.StatusNoContent, http.StatusNoContent)
	defer srv.Close()

	err := deleteUserWithContentToken(t, srv, contentScimTok)
	require.NoError(t, err)
	require.Equal(t, 1, routes.adminDeletes)
	require.Equal(t, 1, routes.contentDeletes)
	require.Empty(t, routes.unknownAuth)
}

// The user being absent from the content-access integration is not a failure —
// delete stays idempotent under the platform's retries.
func TestDelete_ContentScimToken_ContentNotFoundIsSuccess(t *testing.T) {
	routes := &dualScimRoutes{}
	srv := newDualScimServer(t, routes, http.StatusNoContent, http.StatusNotFound)
	defer srv.Close()

	err := deleteUserWithContentToken(t, srv, contentScimTok)
	require.NoError(t, err)
	require.Equal(t, 1, routes.contentDeletes)
}

// The admin delete succeeded but the content-access delete did not. That is a
// partial deprovisioning and must surface as an error, not a clean success.
func TestDelete_ContentScimToken_ContentDeleteFails_SurfacesPartialDeprovisioning(t *testing.T) {
	routes := &dualScimRoutes{}
	srv := newDualScimServer(t, routes, http.StatusNoContent, http.StatusInternalServerError)
	defer srv.Close()

	err := deleteUserWithContentToken(t, srv, contentScimTok)
	require.Error(t, err)
	require.Equal(t, 1, routes.adminDeletes, "the admin-management delete must still have been attempted")
	require.Equal(t, 1, routes.contentDeletes)
	require.Contains(t, err.Error(), "PARTIAL DEPROVISIONING")
	require.Contains(t, err.Error(), "content-access")
}

// A 409 from the content-access integration is a precondition failure, not a
// transient one, and must not read as an idempotent AlreadyExists success.
func TestDelete_ContentScimToken_ContentConflictIsFailedPrecondition(t *testing.T) {
	routes := &dualScimRoutes{}
	srv := newDualScimServer(t, routes, http.StatusNoContent, http.StatusConflict)
	defer srv.Close()

	err := deleteUserWithContentToken(t, srv, contentScimTok)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "error must be a gRPC status error")
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, err.Error(), "PARTIAL DEPROVISIONING")
}

// When the admin-management delete fails, the content-access delete must not
// run: the user is still fully provisioned, so there is no partial state yet.
func TestDelete_ContentScimToken_AdminDeleteFails_SkipsContentDelete(t *testing.T) {
	routes := &dualScimRoutes{}
	srv := newDualScimServer(t, routes, http.StatusInternalServerError, http.StatusNoContent)
	defer srv.Close()

	err := deleteUserWithContentToken(t, srv, contentScimTok)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "PARTIAL DEPROVISIONING")
	require.Equal(t, 1, routes.adminDeletes)
	require.Zero(t, routes.contentDeletes)
}
