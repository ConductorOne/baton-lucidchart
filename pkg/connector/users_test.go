package connector

import (
	"context"
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

func TestDelete_GetUserNotFoundWithContentTransfer_SkipsTransferButRunsScimDelete(t *testing.T) {
	routes := &deleteRoutes{}
	srv := newDeleteServer(t, routes, http.StatusNotFound, http.StatusNotFound, http.StatusNoContent)
	defer srv.Close()

	err := deleteUser(t, srv, "recipient@example.com")
	require.NoError(t, err)
	require.False(t, routes.transfer, "transfer must be skipped when the user is not found")
	require.True(t, routes.scimDelete)
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

func TestUserResource_MissingEnabledIsUnspecified(t *testing.T) {
	res, err := userResource(client.User{Email: "a@example.com", UserId: 1})
	require.NoError(t, err)
	require.Equal(t, v2.Status_RESOURCE_STATUS_UNSPECIFIED, res.GetStatus().GetStatus())
}

func boolPtr(b bool) *bool { return &b }
