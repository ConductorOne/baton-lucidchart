package connector

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/stretchr/testify/require"
)

func TestDocumentGrantIdempotency(t *testing.T) {
	ctx := context.Background()

	t.Run("new grant returns no already-exists annotation and calls upsert", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.Len(t, grants, 1)
		require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(1), cts.putCallCount())
		// The GET pre-check and PUT upsert must target the document from the
		// entitlement, not some other object.
		require.Equal(t, []string{"doc-abc"}, cts.recordedGetObjIDs())
		require.Equal(t, []string{"doc-abc"}, cts.recordedPutObjIDs())
	})

	t.Run("re-grant of same role returns already-exists and skips upsert", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		cts.roles["200"] = "comment"
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(0), cts.putCallCount())
		// The no-op path still returns the grant so C1 can materialize the
		// membership immediately; it must target the document (from the
		// entitlement), not the user principal.
		require.Len(t, grants, 1)
		require.Equal(t, "doc-abc", grants[0].Entitlement.Resource.Id.Resource)
		require.Equal(t, "200", grants[0].Principal.Id.Resource)

		// The no-op grant must carry the same metaRole/metaCreated metadata a
		// normal sync produces, so C1 does not see a metadata-diff churn.
		noopMeta := grantMetadata(t, grants[0])
		require.Equal(t, "comment", noopMeta[metaRole])
		require.NotEmpty(t, noopMeta[metaCreated])

		// Prove the no-op metadata is identical to what the upsert-success path
		// emits for the same collaborator record.
		upCts := newCollaboratorTestServer(t, "documents")
		upB := &documentBuilder{client: newTestClient(t, upCts.server.URL)}
		upGrants, _, err := upB.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.Len(t, upGrants, 1)
		require.Equal(t, grantMetadata(t, upGrants[0]), noopMeta)
	})

	t.Run("role change is not treated as already-exists", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		cts.roles["200"] = "view"
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "edit"))
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
			cts := newCollaboratorTestServer(t, "documents")
			cts.getStatus = getStatus
			b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

			grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
			require.NoError(t, err, "GET %d must not abort the grant", getStatus)
			require.Len(t, grants, 1)
			require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
			require.Equal(t, int64(1), cts.putCallCount(), "upsert must still run after a failed pre-check")
		})
	}

	t.Run("upsert failure propagates as an error", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		cts.putStatus = http.StatusInternalServerError
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.Error(t, err)
		require.Nil(t, grants)
		require.Nil(t, annos)
		require.Equal(t, int64(1), cts.putCallCount())
	})

	// Lucid's upsert is documented as never returning 409 today, but the error
	// path defensively treats one as an idempotent success rather than a
	// failure, in case that ever changes upstream.
	t.Run("upsert 409 is treated as already-exists, not an error", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		cts.putStatus = http.StatusConflict
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.Nil(t, grants)
		require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(1), cts.putCallCount())
	})
}

func TestDocumentRevoke(t *testing.T) {
	ctx := context.Background()

	t.Run("revoke of existing collaborator deletes upstream and returns no annotation", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		cts.roles["200"] = "comment"
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		annos, err := b.Revoke(ctx, userGrant("200", documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
		_, ok := cts.getRole("200")
		require.False(t, ok, "collaborator must be removed after revoke")
		// The DELETE must target the document from the grant's entitlement.
		require.Equal(t, []string{"doc-abc"}, cts.recordedDeleteObjIDs())
	})

	t.Run("revoke of missing collaborator returns already-revoked", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		annos, err := b.Revoke(ctx, userGrant("200", documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
	})
}
