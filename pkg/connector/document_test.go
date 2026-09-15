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

	// Known, accepted limitation (not a bug to fix): the pre-check is best-effort,
	// so when the GET fails for a reason other than "not a collaborator" — e.g. a
	// persistent 403 from a mis-scoped API key — a genuine no-op re-grant is
	// indistinguishable from a new grant. Unlike the fallthrough cases above, this
	// one seeds a matching role first, so a real GrantAlreadyExists condition does
	// exist upstream and is nonetheless not reported. The grant still succeeds;
	// only the annotation is lost.
	t.Run("pre-check GET 403 hides an existing matching role (known gap)", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		cts.roles["200"] = "comment" // user already holds exactly the role being granted
		cts.getStatus = http.StatusForbidden
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.Len(t, grants, 1)
		require.False(t, annos.Contains(&v2.GrantAlreadyExists{}),
			"documents the known gap: a blind pre-check cannot report an existing role")
		require.Equal(t, int64(1), cts.putCallCount(),
			"the upsert runs anyway, so upstream state still ends up correct")
		role, ok := cts.getRole("200")
		require.True(t, ok)
		require.Equal(t, "comment", role)
	})

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
		require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(1), cts.putCallCount())

		// Like the pre-check no-op path, the 409 branch returns the grant so C1
		// materializes the membership immediately instead of waiting for the next
		// sync. It targets the document (from the entitlement), not the principal.
		require.Len(t, grants, 1)
		require.Equal(t, "doc-abc", grants[0].Entitlement.Resource.Id.Resource)
		require.Equal(t, "200", grants[0].Principal.Id.Resource)

		// metaRole is knowable from the entitlement. This 409 body is a bare error
		// envelope with no record in it, so metaCreated is omitted rather than
		// fabricated from a zero time.
		meta := grantMetadata(t, grants[0])
		require.Equal(t, "comment", meta[metaRole])
		require.NotContains(t, meta, metaCreated)
	})

	// When the 409 body does carry the conflicting record, the upsert decodes it
	// before checking the status, so the no-op grant can carry the same
	// metaRole/metaCreated pair Grants() and the pre-check path emit.
	t.Run("upsert 409 carrying the record attaches metaCreated", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		cts.putStatus = http.StatusConflict
		cts.putErrorReturnsRecord = 1
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Len(t, grants, 1)

		meta := grantMetadata(t, grants[0])
		require.Equal(t, "comment", meta[metaRole])
		require.Equal(t, "2024-01-01 00:00:00 +0000 UTC", meta[metaCreated],
			"metaCreated must come from the 409 body, not a zero time")

		// The 409 metadata must match what the pre-check no-op path emits for the
		// same collaborator record, so C1 sees no metadata churn between them.
		noopCts := newCollaboratorTestServer(t, "documents")
		noopCts.roles["200"] = "comment"
		noopB := &documentBuilder{client: newTestClient(t, noopCts.server.URL)}
		noopGrants, _, err := noopB.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.Len(t, noopGrants, 1)
		require.Equal(t, grantMetadata(t, noopGrants[0]), meta)
	})

	// The likeliest reading of a real 409 on an upsert is "the user already holds
	// a *different* role". That is not an idempotent re-grant: the requested role
	// genuinely was not applied. Since the emitted grant is keyed on
	// entitlement.Slug (user/comment), reporting GrantAlreadyExists would make C1
	// materialize an entitlement the user does not hold until the next sync
	// corrects it. The upsert error must surface instead.
	t.Run("upsert 409 carrying a different role returns the error", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		cts.roles["200"] = "view" // upstream role, different from the one granted
		cts.putStatus = http.StatusConflict
		cts.putErrorReturnsRecord = 1
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		// The pre-check GET sees "view" != "comment", so it falls through to the
		// upsert, which 409s with the conflicting "view" record.
		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.Error(t, err, "a 409 naming a different role is a real conflict, not a no-op")
		require.Nil(t, grants, "no grant may be emitted for a role that was not applied")
		require.Nil(t, annos)
		require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(1), cts.putCallCount())
	})
}

// CXH-1919 regression: the upsert-success path used to pass the user principal
// as NewGrant's first argument, which is what NewEntitlementID is keyed on. That
// made the same user+role on two different documents produce one identical
// entitlement ID (user:200:user/comment), so the second grant overwrote the
// first in C1 instead of being a distinct membership.
func TestDocumentGrantEntitlementIDIsPerDocument(t *testing.T) {
	ctx := context.Background()

	// A separate server per document: the mock keys collaborators by user only,
	// so reusing one would make the second grant hit the no-op pre-check path
	// instead of the upsert-success path this test needs to exercise.
	grantOn := func(docID string) *v2.Grant {
		t.Helper()
		cts := newCollaboratorTestServer(t, "documents")
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, docID, "comment"))
		require.NoError(t, err)
		require.Len(t, grants, 1)
		// Guard that this really is the upsert-success path, not the no-op or 409 branch.
		require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(1), cts.putCallCount())
		return grants[0]
	}

	first := grantOn("doc-abc")
	second := grantOn("doc-xyz")

	require.NotEqual(t, first.Entitlement.Id, second.Entitlement.Id,
		"same user+role on different documents must not collide on one entitlement ID")

	// The entitlement must be keyed on the document, with the user as principal.
	require.Equal(t, documentResourceType.Id+":doc-abc:user/comment", first.Entitlement.Id)
	require.Equal(t, documentResourceType.Id+":doc-xyz:user/comment", second.Entitlement.Id)
	require.Equal(t, "doc-abc", first.Entitlement.Resource.Id.Resource)
	require.Equal(t, "doc-xyz", second.Entitlement.Resource.Id.Resource)
	require.Equal(t, "200", first.Principal.Id.Resource)
	require.Equal(t, "200", second.Principal.Id.Resource)
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
