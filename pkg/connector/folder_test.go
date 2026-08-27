package connector

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/stretchr/testify/require"
)

func TestFolderGrantIdempotency(t *testing.T) {
	ctx := context.Background()

	t.Run("new grant returns no already-exists annotation and calls upsert", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "folders")
		b := &folderBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("100"), objectEntitlement(folderResourceType.Id, "9001", "edit"))
		require.NoError(t, err)
		require.Len(t, grants, 1)
		require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(1), cts.putCallCount())
		// The GET pre-check and PUT upsert must target the folder from the
		// entitlement, not some other object.
		require.Equal(t, []string{"9001"}, cts.recordedGetObjIDs())
		require.Equal(t, []string{"9001"}, cts.recordedPutObjIDs())
	})

	t.Run("re-grant of same role returns already-exists and skips upsert", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "folders")
		cts.roles["100"] = "edit" // user already holds exactly this role
		b := &folderBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("100"), objectEntitlement(folderResourceType.Id, "9001", "edit"))
		require.NoError(t, err)
		require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(0), cts.putCallCount(), "no-op re-grant must not touch upstream state")
		// The no-op path still returns the grant so C1 can materialize the
		// membership immediately; it must target the folder (from the
		// entitlement), not the user principal.
		require.Len(t, grants, 1)
		require.Equal(t, "9001", grants[0].Entitlement.Resource.Id.Resource)
		require.Equal(t, "100", grants[0].Principal.Id.Resource)
	})

	t.Run("role change is not treated as already-exists", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "folders")
		cts.roles["100"] = "view" // user holds a different role
		b := &folderBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("100"), objectEntitlement(folderResourceType.Id, "9001", "edit"))
		require.NoError(t, err)
		require.Len(t, grants, 1)
		require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(1), cts.putCallCount())
	})

	// A non-404 read failure on the pre-check GET (403 PermissionDenied observed
	// on this surface, or 500) must not abort the grant: the upsert is
	// authoritative, so the grant should still succeed.
	for _, getStatus := range []int64{http.StatusForbidden, http.StatusInternalServerError} {
		t.Run("pre-check GET "+strconv.FormatInt(getStatus, 10)+" falls through to successful upsert", func(t *testing.T) {
			cts := newCollaboratorTestServer(t, "folders")
			cts.getStatus = getStatus
			b := &folderBuilder{client: newTestClient(t, cts.server.URL)}

			grants, annos, err := b.Grant(ctx, userPrincipal("100"), objectEntitlement(folderResourceType.Id, "9001", "edit"))
			require.NoError(t, err, "GET %d must not abort the grant", getStatus)
			require.Len(t, grants, 1)
			require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
			require.Equal(t, int64(1), cts.putCallCount(), "upsert must still run after a failed pre-check")
		})
	}

	t.Run("upsert failure propagates as an error", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "folders")
		cts.putStatus = http.StatusInternalServerError
		b := &folderBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("100"), objectEntitlement(folderResourceType.Id, "9001", "edit"))
		require.Error(t, err)
		require.Nil(t, grants)
		require.Nil(t, annos)
		require.Equal(t, int64(1), cts.putCallCount())
	})
}

func TestFolderRevoke(t *testing.T) {
	ctx := context.Background()

	t.Run("revoke of existing collaborator deletes upstream and returns no annotation", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "folders")
		cts.roles["100"] = "edit"
		b := &folderBuilder{client: newTestClient(t, cts.server.URL)}

		annos, err := b.Revoke(ctx, userGrant("100", folderResourceType.Id, "9001", "edit"))
		require.NoError(t, err)
		require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
		_, ok := cts.getRole("100")
		require.False(t, ok, "collaborator must be removed after revoke")
		// The DELETE must target the folder from the grant's entitlement.
		require.Equal(t, []string{"9001"}, cts.recordedDeleteObjIDs())
	})

	t.Run("revoke of missing collaborator returns already-revoked", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "folders")
		b := &folderBuilder{client: newTestClient(t, cts.server.URL)}

		annos, err := b.Revoke(ctx, userGrant("100", folderResourceType.Id, "9001", "edit"))
		require.NoError(t, err)
		require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
	})
}
