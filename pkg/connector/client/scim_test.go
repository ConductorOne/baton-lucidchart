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
	return testClientWithContentToken(t, restURL, scimURL, scimToken, "")
}

func testClientWithContentToken(t *testing.T, restURL, scimURL, scimToken, contentScimToken string) *LucidchartClient {
	t.Helper()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauth-test-token"}) //nolint:gosec // G101: static token literal for tests, not a real credential
	c, err := NewLucidchartClient(context.Background(), "api-key", ts, restURL, scimToken, scimURL, contentScimToken)
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"lucid-123","userName":"ada@example.com","active":false,"externalId":"ext-1"}`))
	}))
	defer srv.Close()

	c := testClient(t, srv.URL, srv.URL, "scim-test-token")

	updated, _, err := c.SetUserActive(context.Background(), "123", false)
	require.NoError(t, err)

	// The PATCH response is Lucid's confirmed post-write state, not a discarded body.
	require.NotNil(t, updated)
	require.False(t, updated.IsZero())
	require.Equal(t, "lucid-123", updated.ID)
	require.Equal(t, "ada@example.com", updated.UserName)
	require.Equal(t, "ext-1", updated.ExternalID)
	require.NotNil(t, updated.GetActive())
	require.False(t, *updated.GetActive())

	require.Equal(t, http.MethodPatch, gotMethod)
	require.Equal(t, "/Users/lucid-123", gotPath)
	// Literal values, not the constants, so a wire-shape change must be deliberate.
	require.Equal(t, "application/json", gotContentType)
	require.Equal(t, "Bearer scim-test-token", gotAuth)
	require.Equal(t, []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, body.Schemas)
	require.Len(t, body.Operations, 1)
	require.Equal(t, "replace", body.Operations[0].Op)
	require.Equal(t, "active", body.Operations[0].Path)
	require.Equal(t, false, body.Operations[0].Value)
}

// TestUpdateUserSendsDocumentedScimShape pins the documented Content-Type,
// Accept, `schemas` URN, and bare (non-filtered) attribute paths.
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

// UpdateUser used to return (nil, nil, nil) unconditionally, throwing away the
// SCIM User resource Lucid answers a PATCH with. It must now return that state.
func TestUpdateUserReturnsConfirmedScimUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "lucid-123",
			"userName": "ada.lovelace",
			"active": true,
			"externalId": "ext-42",
			"name": {"givenName": "Ada", "familyName": "Lovelace"},
			"emails": [{"value": "ada@example.com", "type": "work", "primary": true}],
			"roles": [{"value": "DocumentAdmin"}, {"value": "Developer"}]
		}`))
	}))
	defer srv.Close()

	c := testClient(t, srv.URL, srv.URL, "scim-test-token")

	updated, _, err := c.UpdateUser(context.Background(), "123", &UserUpdatePayload{
		FirstName: "Ada",
		LastName:  "Lovelace",
		Email:     "ada@example.com",
		Username:  "ada.lovelace",
		Roles:     []string{"DocumentAdmin", "Developer"},
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.False(t, updated.IsZero())

	require.Equal(t, "lucid-123", updated.ID)
	require.Equal(t, "ada.lovelace", updated.UserName)
	require.Equal(t, "ext-42", updated.ExternalID)
	require.NotNil(t, updated.GetActive())
	require.True(t, *updated.GetActive())
	require.Equal(t, "Ada", updated.Name.GivenName)
	require.Equal(t, "Lovelace", updated.Name.FamilyName)
	require.Equal(t, "ada@example.com", updated.PrimaryEmail())
	require.ElementsMatch(t, []string{"DocumentAdmin", "Developer"}, updated.RoleValues())
}

// A SCIM server may answer a PATCH with 204 No Content. That is a successful
// write with nothing to confirm, not a failure — and it must not be turned into
// one by asking for a body.
func TestScimWriteWithNoResponseBodyStillSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := testClient(t, srv.URL, srv.URL, "scim-test-token")

	updated, _, err := c.UpdateUser(context.Background(), "123", &UserUpdatePayload{FirstName: "Ada"})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.True(t, updated.IsZero(), "a bodyless response confirms nothing")

	active, _, err := c.SetUserActive(context.Background(), "123", false)
	require.NoError(t, err)
	require.True(t, active.IsZero())
	require.Nil(t, active.GetActive())
}

// PrimaryEmail falls back to the first address when SCIM marks none primary,
// which is what Lucid's single-address user model means in practice.
func TestScimUserPrimaryEmailFallback(t *testing.T) {
	u := &ScimUser{Emails: []ScimUserEmail{{Value: "first@example.com"}, {Value: "second@example.com"}}}
	require.Equal(t, "first@example.com", u.PrimaryEmail())

	u.Emails[1].Primary = true
	require.Equal(t, "second@example.com", u.PrimaryEmail())

	require.Empty(t, (*ScimUser)(nil).PrimaryEmail())
	require.Nil(t, (*ScimUser)(nil).RoleValues())
	require.True(t, (*ScimUser)(nil).IsZero())
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

	_, _, err := c.SetUserActive(context.Background(), "123", false)
	require.ErrorIs(t, err, errScimNotConfigured)

	_, err = c.ScimDeleteUser(context.Background(), "123")
	require.ErrorIs(t, err, errScimNotConfigured)
}

// Lucid's two SCIM integrations share one base URL and one /Users/{id} path;
// only the bearer token distinguishes them. ScimDeleteUserContentAccess must
// therefore differ from ScimDeleteUser in exactly one respect: the token.
func TestScimDeleteUserContentAccess(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := testClientWithContentToken(t, srv.URL, srv.URL, "scim-test-token", "content-scim-test-token")
	require.True(t, c.ScimConfigured())
	require.True(t, c.ContentScimConfigured())

	_, err := c.ScimDeleteUserContentAccess(context.Background(), "abc")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/Users/lucid-abc", gotPath)
	require.Equal(t, "Bearer content-scim-test-token", gotAuth)
}

func TestScimDeleteUserUsesAdminTokenNotContentToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := testClientWithContentToken(t, srv.URL, srv.URL, "scim-test-token", "content-scim-test-token")

	_, err := c.ScimDeleteUser(context.Background(), "abc")
	require.NoError(t, err)
	require.Equal(t, "Bearer scim-test-token", gotAuth)
}

func TestContentScimNotConfigured(t *testing.T) {
	c := testClient(t, "https://api.lucid.co", "", "scim-test-token")
	require.True(t, c.ScimConfigured(), "the admin token alone must still configure SCIM")
	require.False(t, c.ContentScimConfigured())

	_, err := c.ScimDeleteUserContentAccess(context.Background(), "123")
	require.ErrorIs(t, err, errContentScimNotConfigured)
}

func TestScimDefaultBaseURL(t *testing.T) {
	c := testClient(t, "", "", "scim-test-token")
	require.Equal(t, string(LucidScimUrl), c.scimBaseURL)
	require.Equal(t, string(LucidchartApiUrl), c.baseURL)
}
