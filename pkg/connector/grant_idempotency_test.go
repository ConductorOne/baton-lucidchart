package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/stretchr/testify/require"
)

// collaboratorTestServer is a stateful mock of the Lucid folder/document
// collaborator surface. It serves the single-collaborator GET (404 when the user
// is not a collaborator, 200 + role when it is) and the PUT upsert, and records
// how many times the upsert was called so tests can prove a no-op re-grant does
// not touch upstream state.
type collaboratorTestServer struct {
	server   *httptest.Server
	roles    map[string]string // userId -> current role
	putCalls int64

	// Fault-injection overrides (0 = disabled). The handler goroutine reads them
	// via atomic.LoadInt64; the tests set them with a plain assignment during
	// setup, before any request is issued, so there is no concurrent access.
	getStatus int64 // when non-zero, the single-collaborator GET returns this status
	putStatus int64 // when non-zero, the upsert PUT returns this status
}

// putCallCount returns the number of times the upsert PUT was invoked. Reads go
// through atomic.LoadInt64 to pair with the atomic.AddInt64 in the PUT handler
// goroutine.
func (cts *collaboratorTestServer) putCallCount() int64 {
	return atomic.LoadInt64(&cts.putCalls)
}

func newCollaboratorTestServer(t *testing.T, kind string) *collaboratorTestServer {
	t.Helper()

	cts := &collaboratorTestServer{roles: map[string]string{}}

	mux := http.NewServeMux()

	getPattern := "GET /" + kind + "/{id}/shares/users/{uid}"
	putPattern := "PUT /" + kind + "/{id}/shares/users/{uid}"

	mux.HandleFunc(getPattern, func(w http.ResponseWriter, r *http.Request) {
		if s := atomic.LoadInt64(&cts.getStatus); s != 0 {
			// Fault injection: simulate a non-404 read failure (e.g. 403/500).
			w.WriteHeader(int(s))
			_, _ = w.Write([]byte(`{"code":` + strconv.FormatInt(s, 10) + `,"message":"injected"}`))
			return
		}
		uid := r.PathValue("uid")
		role, ok := cts.roles[uid]
		if !ok {
			// Lucid returns 404 when the user is not a direct collaborator.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
			return
		}
		cts.writeCollaborator(w, kind, r.PathValue("id"), uid, role)
	})

	mux.HandleFunc(putPattern, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&cts.putCalls, 1)
		uid := r.PathValue("uid")

		var body struct {
			Role string `json:"role"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		if s := atomic.LoadInt64(&cts.putStatus); s != 0 {
			// Fault injection: simulate the upsert itself failing (e.g. 409).
			w.WriteHeader(int(s))
			_, _ = w.Write([]byte(`{"code":` + strconv.FormatInt(s, 10) + `,"message":"injected"}`))
			return
		}

		cts.roles[uid] = body.Role
		cts.writeCollaborator(w, kind, r.PathValue("id"), uid, body.Role)
	})

	cts.server = httptest.NewServer(mux)
	t.Cleanup(cts.server.Close)

	return cts
}

func (cts *collaboratorTestServer) writeCollaborator(w http.ResponseWriter, kind, objID, uid, role string) {
	uidInt, _ := strconv.Atoi(uid)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if kind == "documents" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"documentId": objID,
			"userId":     uidInt,
			"role":       role,
			"created":    "2024-01-01T00:00:00Z",
		})
		return
	}

	folderIDInt, _ := strconv.Atoi(objID)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"folderId": folderIDInt,
		"userId":   uidInt,
		"role":     role,
		"created":  "2024-01-01T00:00:00Z",
	})
}

func newTestClient(t *testing.T, baseURL string) *client.LucidchartClient {
	t.Helper()
	c, err := client.NewLucidchartClient(context.Background(), "test-api-key", nil, baseURL, "", "")
	require.NoError(t, err)
	return c
}

func userPrincipal(userID string) *v2.Resource {
	return &v2.Resource{
		Id: &v2.ResourceId{ResourceType: userResourceType.Id, Resource: userID},
	}
}

func objectEntitlement(resourceType, objectID, role string) *v2.Entitlement {
	return &v2.Entitlement{
		Slug: "user/" + role,
		Resource: &v2.Resource{
			Id: &v2.ResourceId{ResourceType: resourceType, Resource: objectID},
		},
	}
}

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
	})

	t.Run("re-grant of same role returns already-exists and skips upsert", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "folders")
		cts.roles["100"] = "edit" // user already holds exactly this role
		b := &folderBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("100"), objectEntitlement(folderResourceType.Id, "9001", "edit"))
		require.NoError(t, err)
		require.Nil(t, grants)
		require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(0), cts.putCallCount(), "no-op re-grant must not touch upstream state")
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

	t.Run("upsert 409 conflict is treated as already-exists", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "folders")
		cts.putStatus = http.StatusConflict
		b := &folderBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("100"), objectEntitlement(folderResourceType.Id, "9001", "edit"))
		require.NoError(t, err)
		require.Nil(t, grants)
		require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(1), cts.putCallCount())
	})
}

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
	})

	t.Run("re-grant of same role returns already-exists and skips upsert", func(t *testing.T) {
		cts := newCollaboratorTestServer(t, "documents")
		cts.roles["200"] = "comment"
		b := &documentBuilder{client: newTestClient(t, cts.server.URL)}

		grants, annos, err := b.Grant(ctx, userPrincipal("200"), objectEntitlement(documentResourceType.Id, "doc-abc", "comment"))
		require.NoError(t, err)
		require.Nil(t, grants)
		require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		require.Equal(t, int64(0), cts.putCallCount())
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

	t.Run("upsert 409 conflict is treated as already-exists", func(t *testing.T) {
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
