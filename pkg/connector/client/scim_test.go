package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func testClient(t *testing.T, restURL, scimURL, scimToken string) *LucidchartClient {
	t.Helper()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauth-test-token"}) //nolint:gosec // G101: static token literal for tests, not a real credential
	c, err := NewLucidchartClient(context.Background(), "api-key", ts, restURL, scimToken, scimURL)
	require.NoError(t, err)
	return c
}

func TestSetUserActive(t *testing.T) {
	var gotMethod, gotPath, gotContentType, gotAuth string
	var body ScimPatchOp

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"123","active":false}`))
	}))
	defer srv.Close()

	c := testClient(t, srv.URL, srv.URL, "scim-test-token")

	_, err := c.SetUserActive(context.Background(), "123", false)
	require.NoError(t, err)

	require.Equal(t, http.MethodPatch, gotMethod)
	require.Equal(t, "/Users/lucid-123", gotPath)
	// Literal values, not the constants, so a change to the wire shape has to be
	// made deliberately in two places. These are the values Lucid documents; see
	// CXH-2282 for why they win over the RFC 7644 forms.
	require.Equal(t, "application/json", gotContentType)
	require.Equal(t, "Bearer scim-test-token", gotAuth)
	require.Equal(t, []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, body.Schemas)
	require.Len(t, body.Operations, 1)
	require.Equal(t, "replace", body.Operations[0].Op)
	require.Equal(t, "active", body.Operations[0].Path)
	require.Equal(t, false, body.Operations[0].Value)
}

// TestUpdateUserSendsDocumentedScimShape pins every construct CXH-2282 covers:
// Lucid's documented Content-Type and Accept, its documented `schemas` URN, and
// bare attribute paths with no SCIM value filter anywhere in the body.
func TestUpdateUserSendsDocumentedScimShape(t *testing.T) {
	var gotContentType, gotAccept string
	var body ScimPatchOp

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotAccept = r.Header.Get("Accept")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL, srv.URL, "scim-test-token")

	_, _, err := c.UpdateUser(context.Background(), "123", &UserUpdatePayload{
		FirstName: "Ada",
		Email:     "ada@example.com",
		Roles:     []string{"admin"},
	})
	require.NoError(t, err)

	require.Equal(t, "application/json", gotContentType)
	require.Equal(t, "application/json", gotAccept)
	require.Equal(t, []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, body.Schemas)

	paths := make([]string, 0, len(body.Operations))
	for _, op := range body.Operations {
		require.NotContains(t, op.Path, "[", "PATCH path %q must be a bare attribute; Lucid documents no filtered-path support", op.Path)
		paths = append(paths, op.Path)
	}
	require.ElementsMatch(t, []string{"name.givenName", "emails", "roles"}, paths)

	for _, op := range body.Operations {
		if op.Path != "emails" {
			continue
		}
		// Bare path carries the full multi-valued replacement, mirroring the single
		// entry Lucid returns from GET /Users/{id}.
		require.Equal(t, []interface{}{
			map[string]interface{}{"value": "ada@example.com", "primary": true, "type": "work"},
		}, op.Value)
	}
}

func TestScimDeleteUser(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL, srv.URL, "scim-test-token")

	_, err := c.ScimDeleteUser(context.Background(), "abc")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/Users/lucid-abc", gotPath)
}

func TestScimDeleteUserNotFoundIsClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL, srv.URL, "scim-test-token")

	_, err := c.ScimDeleteUser(context.Background(), "missing")
	require.Error(t, err)
	require.True(t, IsNotFoundError(err), "404 should classify as not-found")
}

func TestScimNotConfigured(t *testing.T) {
	c := testClient(t, "https://api.lucid.co", "", "")
	require.False(t, c.ScimConfigured())

	_, err := c.SetUserActive(context.Background(), "123", false)
	require.ErrorIs(t, err, errScimNotConfigured)

	_, err = c.ScimDeleteUser(context.Background(), "123")
	require.ErrorIs(t, err, errScimNotConfigured)
}

func TestScimDefaultBaseURL(t *testing.T) {
	c := testClient(t, "", "", "scim-test-token")
	require.Equal(t, string(LucidScimUrl), c.scimBaseURL)
	require.Equal(t, string(LucidchartApiUrl), c.baseURL)
}
