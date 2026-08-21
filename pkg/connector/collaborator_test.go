package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/stretchr/testify/require"
)

// collaboratorTestServer is a stateful mock of the Lucid folder/document
// collaborator surface. It serves the single-collaborator GET (404 when the user
// is not a collaborator, 200 + role when it is), the PUT upsert, and the DELETE
// (404 when the user is not a collaborator), and records how many times the
// upsert was called so tests can prove a no-op re-grant does not touch upstream
// state.
type collaboratorTestServer struct {
	server   *httptest.Server
	mu       sync.Mutex        // guards roles
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

// getRole, setRole, and deleteRole guard the roles map behind mu so concurrent
// handler goroutines are safe under -race.
func (cts *collaboratorTestServer) getRole(uid string) (string, bool) {
	cts.mu.Lock()
	defer cts.mu.Unlock()
	role, ok := cts.roles[uid]
	return role, ok
}

func (cts *collaboratorTestServer) setRole(uid, role string) {
	cts.mu.Lock()
	defer cts.mu.Unlock()
	cts.roles[uid] = role
}

func (cts *collaboratorTestServer) deleteRole(uid string) bool {
	cts.mu.Lock()
	defer cts.mu.Unlock()
	_, ok := cts.roles[uid]
	delete(cts.roles, uid)
	return ok
}

func newCollaboratorTestServer(t *testing.T, kind string) *collaboratorTestServer {
	t.Helper()

	cts := &collaboratorTestServer{roles: map[string]string{}}

	mux := http.NewServeMux()

	getPattern := "GET /" + kind + "/{id}/shares/users/{uid}"
	putPattern := "PUT /" + kind + "/{id}/shares/users/{uid}"
	deletePattern := "DELETE /" + kind + "/{id}/shares/users/{uid}"

	mux.HandleFunc(getPattern, func(w http.ResponseWriter, r *http.Request) {
		if s := atomic.LoadInt64(&cts.getStatus); s != 0 {
			// Fault injection: simulate a non-404 read failure (e.g. 403/500).
			w.WriteHeader(int(s))
			_, _ = w.Write([]byte(`{"code":` + strconv.FormatInt(s, 10) + `,"message":"injected"}`))
			return
		}
		uid := r.PathValue("uid")
		role, ok := cts.getRole(uid)
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

		cts.setRole(uid, body.Role)
		cts.writeCollaborator(w, kind, r.PathValue("id"), uid, body.Role)
	})

	mux.HandleFunc(deletePattern, func(w http.ResponseWriter, r *http.Request) {
		uid := r.PathValue("uid")
		if !cts.deleteRole(uid) {
			// Lucid returns 404 when the user is not a direct collaborator.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
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

func userGrant(userID, resourceType, objectID, role string) *v2.Grant {
	return &v2.Grant{
		Principal:   userPrincipal(userID),
		Entitlement: objectEntitlement(resourceType, objectID, role),
	}
}
