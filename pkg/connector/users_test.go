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
)

func testLucidClient(t *testing.T, restURL, scimURL string) *client.LucidchartClient {
	t.Helper()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauth-test-token"}) //nolint:gosec // G101: test token literal
	c, err := client.NewLucidchartClient(context.Background(), "api-key", ts, restURL, "scim-test-token", scimURL)
	require.NoError(t, err)
	return c
}

// When content transfer is configured and GetUser returns 404, Delete must skip
// the transfer (no email to resolve) but still attempt the SCIM delete —
// REST 404 does not prove the SCIM record is gone.
func TestDelete_GetUserNotFoundWithContentTransfer_SkipsTransferButRunsScimDelete(t *testing.T) {
	var transferCalled, scimDeleteCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/already-deleted":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/transferUserContent":
			transferCalled = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && r.URL.Path == "/Users/lucid-already-deleted":
			scimDeleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := testLucidClient(t, srv.URL, srv.URL)
	b := newUserBuilder(c, "recipient@example.com")

	annos, err := b.Delete(context.Background(), &v2.ResourceId{Resource: "already-deleted"}, nil)
	require.NoError(t, err)
	require.Nil(t, annos)
	require.False(t, transferCalled, "transfer must be skipped when GetUser returns not-found")
	require.True(t, scimDeleteCalled, "SCIM delete must still be attempted even when GetUser returns not-found")
}
