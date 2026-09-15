package main

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	"github.com/stretchr/testify/require"
)

// newCollaboratorTestServer serves this mock's routes over httptest and returns a
// real connector client pointed at it, so the assertions below exercise the same
// code path a sync or a Grant pre-check takes — not a stand-in for it.
func newCollaboratorTestServer(t *testing.T) *client.LucidchartClient {
	t.Helper()

	// No scimToken: these routes are on the REST surface, and leaving it empty
	// keeps requireRest's "wrong surface" check from matching the API key.
	srv := httptest.NewServer(newMux(newStore(), config{pageSize: lucidPageSize}))
	t.Cleanup(srv.Close)

	c, err := client.NewLucidchartClient(context.Background(), client.LucidchartConfig{
		APIKey:  "test-api-key",
		BaseURL: srv.URL,
	})
	require.NoError(t, err)

	return c
}

// GetDocumentUserCollaborator is documented in the client as direct-only: a role
// it returns is always a direct document share, never one inherited from a parent
// folder. Grant() leans on exactly that — it treats a matching role as proof the
// grant already exists and skips the upsert. If inherited access ever surfaced
// here, an inherited-only user would be reported as already granted while holding
// no direct share, and the grant would silently never be created.
//
// The claim was verified against live Lucid under CXH-2285 but had no mock
// coverage, so nothing would catch a regression in that assumption. These tests
// pin it as a contract the mock enforces.
func TestDocumentUserCollaboratorIsDirectOnly(t *testing.T) {
	ctx := context.Background()

	t.Run("direct collaborator returns their role", func(t *testing.T) {
		c := newCollaboratorTestServer(t)

		got, err := c.GetDocumentUserCollaborator(ctx, seedDocumentID, strconv.Itoa(seedDirectDocUser))
		require.NoError(t, err)
		require.Equal(t, seedDocumentID, got.DocumentId)
		require.Equal(t, seedDirectDocUser, got.UserId)
		require.Equal(t, "view", got.Role)
		require.False(t, got.Created.IsZero(), "created must decode into a real timestamp")
	})

	t.Run("inherited-only user 404s on the document", func(t *testing.T) {
		c := newCollaboratorTestServer(t)

		// Precondition: the fixture really does give this user inherited access —
		// they hold a share on the folder that contains the document. Without this
		// the 404 below would prove nothing, since a user with no access anywhere
		// would 404 too.
		parent, ok := newStore().documentParent(seedDocumentID)
		require.True(t, ok, "fixture must place the document inside a folder")
		require.Equal(t, seedFolderID, parent)

		inherited, err := c.GetFolderUserCollaborator(ctx, parent, strconv.Itoa(seedInheritedUser))
		require.NoError(t, err, "the user must be a direct collaborator on the parent folder")
		require.Equal(t, "edit", inherited.Role)

		// The contract: that folder share does not surface on the document.
		got, err := c.GetDocumentUserCollaborator(ctx, seedDocumentID, strconv.Itoa(seedInheritedUser))
		require.Error(t, err)
		require.Nil(t, got)
		require.True(t, client.IsNotFoundError(err),
			"inherited-only access must be reported as NotFound, got %v", err)
	})

	t.Run("user with no access at all 404s", func(t *testing.T) {
		c := newCollaboratorTestServer(t)

		got, err := c.GetDocumentUserCollaborator(ctx, seedDocumentID, "999999")
		require.Error(t, err)
		require.Nil(t, got)
		require.True(t, client.IsNotFoundError(err), "expected NotFound, got %v", err)
	})
}

// The folder endpoint's direct-only behaviour is published, but the connector
// relies on it identically, so it is pinned the same way.
func TestFolderUserCollaboratorIsDirectOnly(t *testing.T) {
	ctx := context.Background()

	t.Run("direct collaborator returns their role", func(t *testing.T) {
		c := newCollaboratorTestServer(t)

		got, err := c.GetFolderUserCollaborator(ctx, seedFolderID, strconv.Itoa(seedInheritedUser))
		require.NoError(t, err)
		require.Equal(t, strconv.Itoa(got.FolderId), seedFolderID)
		require.Equal(t, seedInheritedUser, got.UserId)
		require.Equal(t, "edit", got.Role)
		require.False(t, got.Created.IsZero(), "created must decode into a real timestamp")
	})

	t.Run("user with no share on the folder 404s", func(t *testing.T) {
		c := newCollaboratorTestServer(t)

		// Direct on the document, nothing on the folder: the mirror image of the
		// inherited case, and equally a 404.
		got, err := c.GetFolderUserCollaborator(ctx, seedFolderID, strconv.Itoa(seedDirectDocUser))
		require.Error(t, err)
		require.Nil(t, got)
		require.True(t, client.IsNotFoundError(err), "expected NotFound, got %v", err)
	})
}
