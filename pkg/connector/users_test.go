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
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauth-test-token"}) //nolint:gosec // G101: test token literal
	c, err := client.NewLucidchartClient(context.Background(), "api-key", ts, restURL, "scim-test-token", scimURL)
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

// When REST answers an ambiguous 403 and the SCIM probe fails *transiently*, the
// connector cannot yet tell whether the user is gone, so it must abort without
// deleting — but it must surface the retryable code the SDK's retry layer honors
// so the platform re-attempts the deprovision instead of parking it behind a
// non-retryable Unknown. The SDK maps HTTP 429/502/503/504 to codes.Unavailable
// and HTTP 408 to codes.DeadlineExceeded, and treats exactly those two codes as
// retryable. SCIM DELETE must never run.
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

// When REST answers an ambiguous 403 and the SCIM probe fails in a way that is
// neither cancellation nor transient (here a 400, standing in for any
// non-retryable, indeterminate probe error), the connector cannot decide whether
// the user is gone. It must abort with a deliberate, indeterminate gRPC code
// (codes.Unknown) — not one that depends on errors.As DFS order across two
// wrapped chains — and SCIM DELETE must never run.
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

// When the sync is cancelled while the SCIM probe is in flight on the ambiguous
// 403 path, the delete must abort with an error that still matches
// context.Canceled via errors.Is — collapsing it to codes.Unknown would hide the
// cancellation from downstream retry/backoff logic. SCIM DELETE must never run.
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
			// Cancel the caller's context mid-probe, then wait for the client to
			// abort the request so ScimUserExists returns a context error.
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
	require.True(t, routes.scimGet, "SCIM must be probed to disambiguate the 403")
	require.False(t, routes.transfer, "no transfer when the probe was cancelled")
	require.False(t, routes.scimDelete, "delete must not run when the probe was cancelled")
}

// Lucid does not document a 404 for GET /v1/users/{id} (only 200 and 403), so a
// 404 is of unknown meaning and must not be trusted as "gone" on its own. When
// SCIM confirms the user really is absent, the delete proceeds so retries of an
// already-processed deprovision converge.
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

// An undocumented REST 404 with the user still present per SCIM is not an
// absence: deleting would destroy the content the operator asked to retain, so
// Delete must refuse and never call SCIM DELETE.
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

// On the undocumented 404 path a SCIM probe outage must NOT block the delete:
// the original rule is that a probe failure cannot abort an otherwise-valid
// delete. Unlike the ambiguous-403 path, a failed probe here means "proceed".
func TestDelete_GetUserNotFoundAndScimProbeFails_ProceedsToScimDelete(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusNotFound, http.StatusInternalServerError, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "recipient@example.com")
	require.NoError(t, err)
	require.True(t, routes.scimGet, "an undocumented 404 must attempt the SCIM probe")
	require.False(t, routes.transfer)
	require.True(t, routes.scimDelete, "a probe outage must not block an otherwise-valid delete")
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
