// Command test-server is an in-process mock of the Lucid REST + SCIM APIs and the
// OAuth2 token endpoint, covering the surface baton-lucidchart uses. It lets CI
// (and local runs) exercise sync and the user update / SCIM deprovisioning paths
// without a real Lucid tenant, real OAuth credentials, or a rotating refresh
// token.
//
// Point the connector at it with:
//
//	--base-url http://localhost:8080
//	--scim-base-url http://localhost:8080/scim/v2
//	--lucid-scim-token test-scim-token   (any non-empty value enables SCIM)
//
// The OAuth2 token endpoint is derived from --base-url by the connector, so the
// refresh_token grant is served here too — no real (rotating) token is involved.
// Any non-empty bearer is accepted on authenticated routes.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// user mirrors client.User (pkg/connector/client/models.go).
type user struct {
	AccountId int      `json:"accountId"`
	Email     string   `json:"email"`
	Name      string   `json:"name"`
	UserId    int      `json:"userId"`
	Usernames string   `json:"usernames"`
	Roles     []string `json:"roles"`
}

type store struct {
	mu    sync.Mutex
	users []user
}

func newStore() *store {
	return &store{
		users: []user{
			{AccountId: 1, Email: "owner@example.com", Name: "Olivia Owner", UserId: 101, Usernames: "owner@example.com", Roles: []string{"admin"}},
			{AccountId: 1, Email: "editor@example.com", Name: "Eddie Editor", UserId: 102, Usernames: "editor@example.com", Roles: []string{"member"}},
		},
	}
}

func (s *store) listUsers() []user {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]user, len(s.users))
	copy(out, s.users)
	return out
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// requireBearer accepts any non-empty bearer token. The mock does not validate
// credentials — it only checks the connector wired auth through.
func requireBearer(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return false
	}
	return true
}

func newMux(s *store) *http.ServeMux {
	mux := http.NewServeMux()

	// Health check (unauthenticated) — the CI start-test-server action polls this.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// OAuth2 token endpoint (unauthenticated). Serves the refresh_token grant the
	// connector performs at startup; returns a static access token so no real
	// (rotating) refresh token is needed.
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"access_token":  "mock-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "mock-refresh-token",
		})
	})

	// REST: list users (OAuth2). Single page (no Link header).
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, s.listUsers())
	})

	// REST: create user (POST /users) — stubbed echo so account creation, if
	// exercised, succeeds. Not used by the sync job.
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, user{AccountId: 1, Email: "new@example.com", Name: "New User", UserId: 999, Usernames: "new@example.com"})
	})

	// REST: transfer content before delete — accept and succeed.
	mux.HandleFunc("POST /users/transferContent", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// REST: update user (PUT /users/{id}) — echo a user back.
	mux.HandleFunc("PUT /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, user{AccountId: 1, Email: "updated@example.com", Name: "Updated User", UserId: 101, Usernames: "updated@example.com"})
	})

	// REST: folder content — empty so the sync completes with no folders/documents
	// (and therefore no collaborator grants to enumerate). API-key auth.
	emptyContents := func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, []interface{}{})
	}
	mux.HandleFunc("GET /folders/root/contents", emptyContents)
	mux.HandleFunc("GET /folders/{id}/contents", emptyContents)

	// SCIM: deactivate/reactivate (PATCH) and delete (DELETE). Separate base URL
	// (/scim/v2) and bearer token in the real API; here any bearer is accepted.
	mux.HandleFunc("PATCH /scim/v2/Users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":     r.PathValue("id"),
			"active": true,
		})
	})
	mux.HandleFunc("DELETE /scim/v2/Users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	return mux
}

func run() error {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newMux(newStore()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("lucidchart test-server listening on http://%s", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Printf("test-server error: %v", err)
		os.Exit(1)
	}
}
