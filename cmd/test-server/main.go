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
	"fmt"
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

// subscription mirrors client.Subscription (pkg/connector/client/models.go).
type subscription struct {
	Id           string `json:"id"`
	LicenseTotal *int64 `json:"licenseTotal"`
	LicensesUsed int64  `json:"licensesUsed"`
	Trial        bool   `json:"trial"`
	Start        string `json:"start"`
	End          string `json:"end"`
	Renewal      string `json:"renewal"`
}

// license mirrors client.LicenseAssignment (pkg/connector/client/models.go).
type license struct {
	UserId         int    `json:"userId"`
	SubscriptionId string `json:"subscriptionId"`
	Created        string `json:"created"`
}

type store struct {
	mu            sync.Mutex
	users         []user
	subscriptions []subscription
	licenses      []license
	nextID        int
}

func newStore() *store {
	licenseTotal := int64(10)
	licenseTotal2 := int64(5)
	return &store{
		users: []user{
			{AccountId: 1, Email: "owner@example.com", Name: "Olivia Owner", UserId: 101, Usernames: "owner@example.com", Roles: []string{"admin"}},
			{AccountId: 1, Email: "editor@example.com", Name: "Eddie Editor", UserId: 102, Usernames: "editor@example.com", Roles: []string{"member"}},
		},
		subscriptions: []subscription{
			{Id: "sub-1", LicenseTotal: &licenseTotal, LicensesUsed: 2, Trial: false, Start: "2026-01-01T00:00:00Z", End: "2027-01-01T00:00:00Z", Renewal: "2027-01-01T00:00:00Z"},
			{Id: "sub-2", LicenseTotal: &licenseTotal2, LicensesUsed: 1, Trial: false, Start: "2026-01-01T00:00:00Z", End: "2027-01-01T00:00:00Z", Renewal: "2027-01-01T00:00:00Z"},
		},
		licenses: []license{
			{UserId: 101, SubscriptionId: "sub-1", Created: "2026-01-01T00:00:00Z"},
			{UserId: 102, SubscriptionId: "sub-1", Created: "2026-01-02T00:00:00Z"},
			{UserId: 102, SubscriptionId: "sub-2", Created: "2026-01-03T00:00:00Z"},
		},
		nextID: 1000,
	}
}

func (s *store) listUsers() []user {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]user, len(s.users))
	copy(out, s.users)
	return out
}

// addUser appends u to the store (allocating a UserId if zero) and returns it.
func (s *store) addUser(u user) user {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u.UserId == 0 {
		s.nextID++
		u.UserId = s.nextID
	}
	s.users = append(s.users, u)
	return u
}

// getUserByID returns the user with the given numeric UserId string, or false
// if not found. Used by GET /v1/users/{id}.
func (s *store) getUserByID(id string) (user, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if fmt.Sprintf("%d", u.UserId) == id {
			return u, true
		}
	}
	return user{}, false
}

// listSubscriptions returns all subscriptions. Used by GET /v1/subscriptions.
func (s *store) listSubscriptions() []subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]subscription, len(s.subscriptions))
	copy(out, s.subscriptions)
	return out
}

// subscriptionExists reports whether a subscription with the given id exists.
// Used by GET /v1/subscriptions/{id}/licenses to 404 on an unknown subscription.
func (s *store) subscriptionExists(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subscriptions {
		if sub.Id == id {
			return true
		}
	}
	return false
}

// listLicensesForSubscription returns the licenses assigned under the given
// subscription id. Used by GET /v1/subscriptions/{id}/licenses.
func (s *store) listLicensesForSubscription(subscriptionId string) []license {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []license
	for _, l := range s.licenses {
		if l.SubscriptionId == subscriptionId {
			out = append(out, l)
		}
	}
	return out
}

// deleteUserByScimID removes the user whose UserId matches the numeric suffix
// of a SCIM resource ID (e.g. "lucid-101" → removes UserId=101).
// Returns true if a user was found and removed.
func (s *store) deleteUserByScimID(scimID string) bool {
	const prefix = "lucid-"
	if !strings.HasPrefix(scimID, prefix) {
		return false
	}
	rawID := scimID[len(prefix):]
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.users {
		if fmt.Sprintf("%d", u.UserId) == rawID {
			s.users = append(s.users[:i], s.users[i+1:]...)
			return true
		}
	}
	return false
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

	// REST: get single user by ID (GET /v1/users/{id}). Used by the connector
	// to resolve a user's email address before calling transferUserContent.
	mux.HandleFunc("GET /v1/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		id := r.PathValue("id")
		u, ok := s.getUserByID(id)
		if !ok {
			log.Printf("GET /v1/users/%s — not found", id) //nolint:gosec // test-server: path value is diagnostic only
			http.NotFound(w, r)
			return
		}
		log.Printf("GET /v1/users/%s → email=%s", id, u.Email) //nolint:gosec // test-server: path value is diagnostic only
		writeJSON(w, http.StatusOK, u)
	})

	// REST: list subscriptions (GET /v1/subscriptions, OAuth2). Single page
	// (no Link header) — seeded with two subscriptions so the license resource
	// type's List produces multiple resources to sync, and StaticEntitlements
	// per-instance stamping can be verified across them.
	mux.HandleFunc("GET /v1/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, s.listSubscriptions())
	})

	// REST: list per-subscription license assignments
	// (GET /v1/subscriptions/{id}/licenses, OAuth2). Used by the license
	// resource type's Grants to emit one grant per assignment.
	mux.HandleFunc("GET /v1/subscriptions/{id}/licenses", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		id := r.PathValue("id")
		if !s.subscriptionExists(id) {
			log.Printf("GET /v1/subscriptions/%s/licenses — not found", id) //nolint:gosec // test-server: path value is diagnostic only
			http.NotFound(w, r)
			return
		}
		licenses := s.listLicensesForSubscription(id)
		log.Printf("GET /v1/subscriptions/%s/licenses → %d licenses", id, len(licenses)) //nolint:gosec // test-server: path value is diagnostic only
		writeJSON(w, http.StatusOK, licenses)
	})

	// REST: create user (POST /users) — parses request body and persists the
	// new user so the next GET /users sync can find it. This is required for
	// baton-test's provisioning flow, which creates then re-syncs to verify.
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var payload struct {
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
			Email     string `json:"email"`
			Username  string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		name := payload.FirstName + " " + payload.LastName
		username := payload.Username
		if username == "" {
			username = payload.Email
		}
		u := s.addUser(user{AccountId: 1, Email: payload.Email, Name: name, Usernames: username})
		log.Printf("POST /users created userId=%d email=%s", u.UserId, u.Email)
		writeJSON(w, http.StatusOK, u)
	})

	// REST: content transfer before delete. The Lucid API requires email
	// addresses for fromUser and toUser ("Email of the user whose content will
	// be transferred"). Returns 400 if either field is missing '@' — this
	// catches the pre-fix bug where numeric IDs were passed instead of emails.
	mux.HandleFunc("POST /v1/transferUserContent", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body struct {
			FromUser string `json:"fromUser"`
			ToUser   string `json:"toUser"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		log.Printf("POST /v1/transferUserContent fromUser=%s toUser=%s", body.FromUser, body.ToUser)
		if !strings.Contains(body.FromUser, "@") || !strings.Contains(body.ToUser, "@") {
			msg := fmt.Sprintf("fromUser and toUser must be email addresses, got fromUser=%q toUser=%q", body.FromUser, body.ToUser)
			log.Printf("POST /v1/transferUserContent 400: %s", msg)
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
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
	// Logs the {id} path value so we can confirm the connector sends lucid-<userId>.
	mux.HandleFunc("PATCH /scim/v2/Users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		id := r.PathValue("id")
		log.Printf("PATCH /scim/v2/Users/%s", id) //nolint:gosec // test-server: path value is diagnostic only
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":     id,
			"active": true,
		})
	})
	mux.HandleFunc("DELETE /scim/v2/Users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		id := r.PathValue("id")
		removed := s.deleteUserByScimID(id)
		log.Printf("DELETE /scim/v2/Users/%s removed=%v", id, removed) //nolint:gosec // test-server: path value is diagnostic only
		w.WriteHeader(http.StatusNoContent)
	})

	// Catch-all: any route not registered above returns 404 so a wrong connector
	// path is caught immediately rather than silently swallowed.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("UNMATCHED ROUTE: %s %s — returning 404", r.Method, r.URL.Path) //nolint:gosec // test-server: path value is diagnostic only
		http.NotFound(w, r)
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
