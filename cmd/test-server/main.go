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
//	--lucid-scim-token test-scim-token
//
// The OAuth2 token endpoint is derived from --base-url by the connector, so the
// refresh_token grant is served here too — no real (rotating) token is involved.
//
// # Fidelity
//
// This mock is written from Lucid's PUBLISHED API contract, not from the
// connector's client code. That distinction is the whole point: a mock that
// mirrors the connector reproduces its bugs and turns the test suite into a
// fiction. Where the connector and the documented contract disagree, this server
// follows the documentation and the connector fails — which is the correct
// outcome, because the live API would fail the same way.
//
// Behaviours deliberately modelled from the docs (with the doc citation):
//
//   - GET /v1/users/{id} returns 403 for a user that does not exist, never 404
//     ("if the user does not belong to the authenticated account or if the user
//     does not exist" — reference/getuser). Use -legacy-user-404 to restore the
//     permissive 404 for an A/B comparison.
//   - SCIM PATCH actually applies and persists the requested `active` value and
//     profile operations, and echoes the real state back (reference/modifyuserpatch).
//   - SCIM DELETE returns 404 for an unknown user and 409 for a protected one
//     ("if the user cannot be deleted (e.g., account owner or default document
//     owner)" — reference/deleteuser).
//   - The REST user model exposes `username` (singular) and `enabled`, matching
//     Lucid's documented shape (reference/getuser).
//   - The SCIM Content-Type, PatchOp `schemas` URN, and PATCH operation paths
//     accept both the RFC 7644 form and the form Lucid's spec documents, logging
//     a DIVERGENCE for the RFC form. Use -strict-scim-doc to reject it instead.
//   - The single-collaborator GETs (/folders|documents/{id}/shares/users/{uid})
//     are direct-only: a user who reaches the container only through an ancestor
//     404s there. Published for folders (reference/getfolderusercollaborators);
//     for documents it is the one behaviour here taken from observation rather
//     than documentation, verified live under CXH-2285.
//   - Errors use Lucid's envelope {code, message, requestId} (reference-rest).
//   - GET /users paginates via an opaque pageToken carried in the Link header,
//     200 records per page (reference-rest).
//   - REST and SCIM require DIFFERENT bearer tokens, as they do in production.
//
// Run with -h to see the scenario flags.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// scimIDPrefix is the prefix Lucid puts on SCIM resource IDs: the SCIM id for
	// REST user 1234 is "lucid-1234" (reference/overview-scim, User.id).
	scimIDPrefix = "lucid-"

	// maxSeedUsers caps POST /_test/users?count= so a mistyped count returns a
	// 400 instead of OOMing the mock in makeslice. Well above any real
	// pagination-boundary run (PAG-03 exercises a few thousand).
	maxSeedUsers = 100_000

	// lucidPageSize is Lucid's page size for paginated REST endpoints. The docs
	// state a 200-record default and that a larger requested pageSize is clamped
	// to 200 (reference-rest).
	lucidPageSize = 200

	// docContentType is what Lucid's OpenAPI declares for SCIM operations.
	// scimContentType is the RFC 7644 form, still accepted unless -strict-scim-doc.
	scimContentType = "application/scim+json"
	docContentType  = "application/json"

	// docPatchSchema is the `schemas` value Lucid's PATCH example uses.
	// scimPatchOpSchema is the RFC 7644 PatchOp URN, still accepted unless -strict-scim-doc.
	scimPatchOpSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	docPatchSchema    = "urn:ietf:params:scim:schemas:core:2.0:User"

	scimListSchema = "urn:ietf:params:scim:api:messages:2.0:ListResponse"

	// maxOAuthFormBytes bounds the /oauth2/token request body read by ParseForm,
	// which otherwise buffers the whole body in memory regardless of size.
	maxOAuthFormBytes = 1 << 20

	// The collaborator fixture: document seedDocumentID lives inside folder
	// seedFolderID. Folder IDs are numeric in Lucid's model (folderId is a number
	// in the collaborator record) while document IDs are opaque strings.
	seedFolderID     = "9001"
	seedDocumentID   = "doc-200"
	seedShareCreated = "2024-01-01T00:00:00Z"

	// seedDirectDocUser holds a DIRECT share on the document; seedInheritedUser
	// is shared only on the parent folder, so its document access is inherited.
	seedDirectDocUser = 102
	seedInheritedUser = 103
)

// config carries the scenario switches. Defaults are the documented behaviour;
// every flag exists to reproduce a specific test case from the CXH-1488 plan.
type config struct {
	legacyUser404  bool
	protectedUsers map[int]bool
	transferLimit  bool
	pageSize       int
	scimToken      string
	// strictSCIMDoc rejects SCIM requests that follow RFC 7644 where Lucid's own
	// spec documents something different (Content-Type, PatchOp schemas URN,
	// value-filtered operation paths). Off by default: the docs and the RFC
	// genuinely disagree and only a live Enterprise tenant can settle it, so
	// failing by default would over-claim.
	strictSCIMDoc bool
}

// user mirrors Lucid's REST User model (reference/getuser).
//
// NOTE the field names. Lucid documents `username` (singular) and `enabled`
// (reference/getuser), so this mock serves exactly that shape. Keep them as-is:
// the mock follows Lucid's published contract, and renaming a field to match a
// consumer would let the test suite drift from the documented API.
type user struct {
	AccountId int      `json:"accountId"`
	Email     string   `json:"email"`
	Name      string   `json:"name"`
	UserId    int      `json:"userId"`
	Username  string   `json:"username"`
	Enabled   bool     `json:"enabled"`
	Roles     []string `json:"roles"`
}

// scimName is the SCIM 2.0 complex name attribute.
type scimName struct {
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

// scimEmail is a SCIM 2.0 multi-valued email entry.
type scimEmail struct {
	Value   string `json:"value"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary"`
}

// scimUser is the SCIM representation returned by PATCH and GET /Users.
type scimUser struct {
	Schemas  []string    `json:"schemas"`
	ID       string      `json:"id"`
	UserName string      `json:"userName"`
	Name     scimName    `json:"name"`
	Emails   []scimEmail `json:"emails"`
	Active   bool        `json:"active"`
	Roles    []scimRole  `json:"roles,omitempty"`
}

type scimRole struct {
	Value string `json:"value"`
}

// scimPatchOp is the request body for PATCH /Users/{id}.
type scimPatchOp struct {
	Schemas    []string           `json:"schemas"`
	Operations []scimPatchOpEntry `json:"Operations"`
}

type scimPatchOpEntry struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

// lucidError is Lucid's documented error envelope (reference-rest).
type lucidError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestId string `json:"requestId"`
}

// share is one DIRECT collaborator record on a folder or a document. The two
// endpoints wrap it in slightly different envelopes (folderId vs documentId), so
// the handlers assemble the body rather than marshalling this type directly.
type share struct {
	userID  int
	role    string
	created string
}

type store struct {
	mu     sync.Mutex
	users  []user
	nextID int

	// folderShares and documentShares hold only DIRECT shares, keyed by container
	// ID then user ID. Access a user has by inheritance is deliberately absent
	// from documentShares: that is what makes the direct-only contract testable
	// (see the single-collaborator routes in newMux).
	folderShares   map[string]map[int]share
	documentShares map[string]map[int]share
	// documentParents records which folder contains a document, so the fixture
	// can express "this user's document access is inherited from that folder"
	// without the mock having to model a whole folder tree.
	documentParents map[string]string
}

var requestCounter atomic.Int64

func nextRequestID() string {
	return fmt.Sprintf("req-%06d", requestCounter.Add(1))
}

func newStore() *store {
	return &store{
		users: []user{
			{AccountId: 1, Email: "owner@example.com", Name: "Olivia Owner", UserId: 101, Username: "owner@example.com", Enabled: true, Roles: []string{"account-owner"}},
			{AccountId: 1, Email: "editor@example.com", Name: "Eddie Editor", UserId: 102, Username: "editor@example.com", Enabled: true, Roles: []string{"document-admin"}},
			// Deliberate diversification beyond the dev's happy path: a user with
			// no roles, a unicode display name, and a user that starts disabled so
			// a sync can be checked for whether it reports state at all.
			{AccountId: 1, Email: "no-roles@example.com", Name: "Nora NoRoles", UserId: 103, Username: "no-roles@example.com", Enabled: true, Roles: nil},
			{AccountId: 1, Email: "unicode@example.com", Name: "Zoë Ünicode-Ñame 🎨", UserId: 104, Username: "unicode@example.com", Enabled: true, Roles: []string{"team-admin"}},
			{AccountId: 1, Email: "disabled@example.com", Name: "Dana Disabled", UserId: 105, Username: "disabled@example.com", Enabled: false, Roles: []string{"developer"}},
		},
		nextID: 1000,

		// Eddie (102) is shared directly on the document. Nora (103) is shared on
		// the parent folder only, so she can open the document but holds no direct
		// share on it — the inherited-access case the direct-only contract is about.
		folderShares: map[string]map[int]share{
			seedFolderID: {
				seedInheritedUser: {userID: seedInheritedUser, role: "edit", created: seedShareCreated},
			},
		},
		documentShares: map[string]map[int]share{
			seedDocumentID: {
				seedDirectDocUser: {userID: seedDirectDocUser, role: "view", created: seedShareCreated},
			},
		},
		documentParents: map[string]string{seedDocumentID: seedFolderID},
	}
}

// folderShare returns the user's DIRECT share on a folder. containerKnown is
// false when the folder itself is not in the fixture.
func (s *store) folderShare(folderID string, userID int) (share, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	shares, containerKnown := s.folderShares[folderID]
	if !containerKnown {
		return share{}, false, false
	}
	sh, found := shares[userID]
	return sh, found, true
}

// documentParent returns the folder that contains a document, which is where a
// document collaborator's access comes from when they hold no direct share.
func (s *store) documentParent(documentID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, ok := s.documentParents[documentID]
	return parent, ok
}

// documentShare returns the user's DIRECT share on a document. A user whose only
// access is inherited from the parent folder is not found here — by design.
func (s *store) documentShare(documentID string, userID int) (share, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	shares, containerKnown := s.documentShares[documentID]
	if !containerKnown {
		return share{}, false, false
	}
	sh, found := shares[userID]
	return sh, found, true
}

// seedUsers replaces the user set with exactly n generated users. Used by
// -users N and POST /_test/users to hit pagination boundaries (PAG-03).
func (s *store) seedUsers(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = make([]user, 0, n)
	for i := 0; i < n; i++ {
		id := 1 + i
		s.users = append(s.users, user{
			AccountId: 1,
			Email:     fmt.Sprintf("user%d@example.com", id),
			Name:      fmt.Sprintf("User %d", id),
			UserId:    id,
			Username:  fmt.Sprintf("user%d@example.com", id),
			Enabled:   true,
		})
	}
	// Seeded IDs run 1..n. Advance the allocator past them so a subsequent
	// POST /users can't mint a colliding userId (e.g. -users 2000 then a create).
	if n > s.nextID {
		s.nextID = n
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

// getUserByID returns the user with the given numeric UserId string.
func (s *store) getUserByID(id string) (user, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if strconv.Itoa(u.UserId) == id {
			return u, true
		}
	}
	return user{}, false
}

// updateUser applies mutate to the stored user with the given numeric id.
func (s *store) updateUser(id int, mutate func(*user)) (user, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.users {
		if s.users[i].UserId == id {
			mutate(&s.users[i])
			return s.users[i], true
		}
	}
	return user{}, false
}

func (s *store) userExistsByEmail(email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if strings.EqualFold(u.Email, email) {
			return true
		}
	}
	return false
}

// deleteUserByID removes the user with the given numeric id.
func (s *store) deleteUserByID(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.users {
		if u.UserId == id {
			s.users = append(s.users[:i], s.users[i+1:]...)
			return true
		}
	}
	return false
}

// parseScimID converts a SCIM resource ID ("lucid-101") to its numeric REST id.
// A SCIM ID without the documented prefix is not a valid Lucid SCIM ID.
func parseScimID(scimID string) (int, bool) {
	if !strings.HasPrefix(scimID, scimIDPrefix) {
		return 0, false
	}
	id, err := strconv.Atoi(strings.TrimPrefix(scimID, scimIDPrefix))
	if err != nil {
		return 0, false
	}
	return id, true
}

// primaryEmailFromPatchValue extracts the address from a PATCH `emails` value:
// the primary entry, else the first usable one, or a bare string. ok is false
// if no usable address is found.
func primaryEmailFromPatchValue(v interface{}) (string, bool) {
	if s, ok := v.(string); ok && s != "" {
		return s, true
	}
	list, ok := v.([]interface{})
	if !ok {
		return "", false
	}
	chosen := ""
	for _, entry := range list {
		m, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		val, _ := m["value"].(string)
		if val == "" {
			continue
		}
		if primary, _ := m["primary"].(bool); primary {
			return val, true
		}
		if chosen == "" {
			chosen = val
		}
	}
	return chosen, chosen != ""
}

func toScimUser(u user) scimUser {
	roles := make([]scimRole, 0, len(u.Roles))
	for _, r := range u.Roles {
		roles = append(roles, scimRole{Value: r})
	}
	parts := strings.SplitN(u.Name, " ", 2)
	given := parts[0]
	family := ""
	if len(parts) > 1 {
		family = parts[1]
	}
	return scimUser{
		Schemas:  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		ID:       scimIDPrefix + strconv.Itoa(u.UserId),
		UserName: u.Username,
		Name:     scimName{GivenName: given, FamilyName: family},
		Emails:   []scimEmail{{Value: u.Email, Type: "work", Primary: true}},
		Active:   u.Enabled,
		Roles:    roles,
	}
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeSCIMJSON emits a SCIM response with the Content-Type Lucid documents.
func writeSCIMJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", docContentType)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeOAuthError emits the RFC 6749 token-endpoint error shape.
func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	log.Printf("-> %d oauth %s: %s", status, code, description) //nolint:gosec // test-server: message is diagnostic only
	writeJSON(w, status, map[string]string{
		"error":             code,
		"error_description": description,
	})
}

// writeLucidError emits Lucid's documented error envelope.
func writeLucidError(w http.ResponseWriter, status int, code, message string) {
	rid := nextRequestID()
	log.Printf("-> %d %s: %s (requestId=%s)", status, code, message, rid) //nolint:gosec // test-server: message is diagnostic only
	writeJSON(w, status, lucidError{Code: code, Message: message, RequestId: rid})
}

func newMux(s *store, cfg config) *http.ServeMux {
	mux := http.NewServeMux()

	// requireToken checks for the specific bearer the surface expects. REST and
	// SCIM use different tokens in production; accepting either here would hide a
	// connector that sends the wrong one.
	requireToken := func(want, surface string) func(http.ResponseWriter, *http.Request) bool {
		return func(w http.ResponseWriter, r *http.Request) bool {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				writeLucidError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return false
			}
			got := strings.TrimPrefix(auth, "Bearer ")
			if want != "" && got != want {
				writeLucidError(w, http.StatusUnauthorized, "unauthorized",
					fmt.Sprintf("%s surface received the wrong bearer token", surface))
				return false
			}
			return true
		}
	}
	// The REST surface accepts both the OAuth access token this server mints and
	// the configured API key, because the connector legitimately uses each for a
	// different route family.
	requireRest := func(w http.ResponseWriter, r *http.Request) bool {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeLucidError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return false
		}
		got := strings.TrimPrefix(auth, "Bearer ")
		if got == cfg.scimToken {
			writeLucidError(w, http.StatusUnauthorized, "unauthorized",
				"REST surface received the SCIM bearer token")
			return false
		}
		return true
	}
	requireScim := requireToken(cfg.scimToken, "SCIM")

	// Health check (unauthenticated) — the CI start-test-server action polls this.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Test-control endpoints (unauthenticated, prefixed /_test/). Not part of the
	// Lucid contract — they exist so a test case can reshape the dataset between
	// runs without restarting the process.
	mux.HandleFunc("POST /_test/users", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.URL.Query().Get("count"))
		// Cap count so a fat-fingered request fails loudly with a 400 rather than
		// OOMing the mock in makeslice — the value reaches seedUsers unvalidated.
		if err != nil || n < 0 || n > maxSeedUsers {
			http.Error(w, fmt.Sprintf("count must be an integer in [0, %d]", maxSeedUsers), http.StatusBadRequest)
			return
		}
		s.seedUsers(n)
		log.Printf("[_test] reseeded with %d users", n)
		writeJSON(w, http.StatusOK, map[string]int{"users": n})
	})
	mux.HandleFunc("POST /_test/reset", func(w http.ResponseWriter, _ *http.Request) {
		fresh := newStore()
		s.mu.Lock()
		s.users, s.nextID = fresh.users, fresh.nextID
		s.folderShares, s.documentShares = fresh.folderShares, fresh.documentShares
		s.documentParents = fresh.documentParents
		s.mu.Unlock()
		log.Printf("[_test] store reset to seed fixtures")
		writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	})

	// POST /oauth2/token — https://lucid.readme.io/reference/createorrefreshaccesstoken
	// Serves the refresh_token grant the connector performs at startup, and
	// rejects what the real endpoint rejects: a permissive token endpoint has
	// shipped broken grant_type params to production before.
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxOAuthFormBytes)
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
			return
		}
		grantType := r.Form.Get("grant_type")
		clientID := r.Form.Get("client_id")
		clientSecret := r.Form.Get("client_secret")
		if clientID == "" || clientSecret == "" {
			// The Go OAuth2 library may send credentials via Basic auth instead.
			if u, p, ok := r.BasicAuth(); ok {
				clientID, clientSecret = u, p
			}
		}

		switch grantType {
		case "refresh_token":
			if r.Form.Get("refresh_token") == "" {
				writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
				return
			}
		case "authorization_code":
			if r.Form.Get("code") == "" || r.Form.Get("redirect_uri") == "" {
				writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code and redirect_uri are required")
				return
			}
		case "":
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "grant_type is required")
			return
		default:
			writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
				fmt.Sprintf("unsupported grant_type %q", grantType))
			return
		}

		if clientID == "" || clientSecret == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id and client_secret are required")
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"access_token":  "mock-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "mock-refresh-token",
		})
	})

	// GET /users — https://lucid.readme.io/reference/listusers
	// Paginated exactly as Lucid documents: an opaque
	// pageToken carried ONLY in the Link header, default/max 200 per page.
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
		if !requireRest(w, r) {
			return
		}
		all := s.listUsers()

		offset := 0
		if tok := r.URL.Query().Get("pageToken"); tok != "" {
			parsed, err := strconv.Atoi(tok)
			if err != nil || parsed < 0 || parsed > len(all) {
				writeLucidError(w, http.StatusBadRequest, "badRequest", "invalid pageToken")
				return
			}
			offset = parsed
		}

		size := cfg.pageSize
		if requested := r.URL.Query().Get("pageSize"); requested != "" {
			if n, err := strconv.Atoi(requested); err == nil && n > 0 && n < size {
				size = n
			}
		}

		end := offset + size
		if end > len(all) {
			end = len(all)
		}
		page := all[offset:end]

		if end < len(all) {
			next := fmt.Sprintf("%s://%s/users?pageSize=%d&pageToken=%d", schemeOf(r), r.Host, size, end)
			w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"next\"", next))
		}
		log.Printf("GET /users offset=%d returned=%d total=%d hasNext=%v", offset, len(page), len(all), end < len(all))
		writeJSON(w, http.StatusOK, page)
	})

	// GET /v1/users/{id} — https://lucid.readme.io/reference/getuser
	//
	// Lucid documents 403 — NOT 404 — for a user that does not exist:
	// "if the user does not belong to the authenticated account or if the user
	// does not exist" (reference/getuser). This mock returns 403 for unknown
	// users so the connector's delete path is exercised against the documented
	// behaviour. -legacy-user-404 restores the permissive 404 so a test can A/B
	// the two.
	mux.HandleFunc("GET /v1/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireRest(w, r) {
			return
		}
		id := r.PathValue("id")
		u, ok := s.getUserByID(id)
		if !ok {
			if cfg.legacyUser404 {
				log.Printf("GET /v1/users/%s — not found (LEGACY 404 mode)", id) //nolint:gosec // test-server: path value is diagnostic only
				writeLucidError(w, http.StatusNotFound, "notFound", "user not found")
				return
			}
			log.Printf("GET /v1/users/%s — absent; returning documented 403", id) //nolint:gosec // test-server: path value is diagnostic only
			writeLucidError(w, http.StatusForbidden, "accessForbidden",
				"the user does not belong to the authenticated account or does not exist")
			return
		}
		log.Printf("GET /v1/users/%s → email=%s", id, u.Email) //nolint:gosec // test-server: path value is diagnostic only
		writeJSON(w, http.StatusOK, u)
	})

	// POST /users — https://lucid.readme.io/reference/createuser
	// Lucid documents 201 (not 200), 409 on a duplicate email,
	// and 400 when a required field is missing (reference/createuser).
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		if !requireRest(w, r) {
			return
		}
		var payload struct {
			FirstName string   `json:"firstName"`
			LastName  string   `json:"lastName"`
			Email     string   `json:"email"`
			Username  string   `json:"username"`
			Roles     []string `json:"roles"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeLucidError(w, http.StatusBadRequest, "badRequest", "malformed request body")
			return
		}
		if payload.Email == "" || payload.FirstName == "" || payload.LastName == "" {
			writeLucidError(w, http.StatusBadRequest, "badRequest",
				"email, firstName and lastName are required")
			return
		}
		if s.userExistsByEmail(payload.Email) {
			writeLucidError(w, http.StatusConflict, "alreadyExists",
				"a user with the same email or username already exists")
			return
		}
		username := payload.Username
		if username == "" {
			username = payload.Email
		}
		u := s.addUser(user{
			AccountId: 1,
			Email:     payload.Email,
			Name:      payload.FirstName + " " + payload.LastName,
			Username:  username,
			Enabled:   true,
			Roles:     payload.Roles,
		})
		log.Printf("POST /users created userId=%d email=%s roles=%v", u.UserId, u.Email, u.Roles)
		writeJSON(w, http.StatusCreated, u)
	})

	// POST /v1/transferUserContent — https://lucid.readme.io/reference/transferusercontent
	// Lucid requires EMAIL addresses for
	// both fields, documents 400 when they are the same user, 403 when either
	// user does not exist, and a 30-request/5-second per-account rate limit
	// (reference/transferusercontent).
	var transferTimes []time.Time
	var transferMu sync.Mutex
	mux.HandleFunc("POST /v1/transferUserContent", func(w http.ResponseWriter, r *http.Request) {
		if !requireRest(w, r) {
			return
		}
		var body struct {
			FromUser string `json:"fromUser"`
			ToUser   string `json:"toUser"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeLucidError(w, http.StatusBadRequest, "badRequest", "malformed request body")
			return
		}
		log.Printf("POST /v1/transferUserContent fromUser=%s toUser=%s", body.FromUser, body.ToUser)

		if cfg.transferLimit {
			transferMu.Lock()
			now := time.Now()
			kept := transferTimes[:0]
			for _, t := range transferTimes {
				if now.Sub(t) < 5*time.Second {
					kept = append(kept, t)
				}
			}
			transferTimes = kept
			over := len(transferTimes) >= 30
			if !over {
				transferTimes = append(transferTimes, now)
			}
			transferMu.Unlock()
			if over {
				w.Header().Set("Retry-After", "5")
				writeLucidError(w, http.StatusTooManyRequests, "tooManyRequests",
					"You have sent too many requests in a given amount of time.")
				return
			}
		}

		// The API requires emails. A numeric ID here is the pre-fix connector bug.
		if !strings.Contains(body.FromUser, "@") || !strings.Contains(body.ToUser, "@") {
			writeLucidError(w, http.StatusBadRequest, "badRequest",
				fmt.Sprintf("fromUser and toUser must be email addresses, got fromUser=%q toUser=%q", body.FromUser, body.ToUser))
			return
		}
		if strings.EqualFold(body.FromUser, body.ToUser) {
			writeLucidError(w, http.StatusBadRequest, "badRequest", "fromUser and toUser must differ")
			return
		}
		if !s.userExistsByEmail(body.FromUser) || !s.userExistsByEmail(body.ToUser) {
			writeLucidError(w, http.StatusForbidden, "accessForbidden",
				"the users do not exist or are not on the authenticated account")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// GET /folders/root/contents — https://lucid.readme.io/reference/listrootfoldercontents
	// GET /folders/{id}/contents — https://lucid.readme.io/reference/listfoldercontents
	// Empty so the sync completes with no folders/documents.
	emptyContents := func(w http.ResponseWriter, r *http.Request) {
		if !requireRest(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, []interface{}{})
	}
	mux.HandleFunc("GET /folders/root/contents", emptyContents)
	mux.HandleFunc("GET /folders/{id}/contents", emptyContents)

	// The single-collaborator GETs the Grant pre-check uses. Both are DIRECT-ONLY:
	// a user who can reach the container only through an ancestor is not reported
	// here, and the endpoint 404s for them.
	//
	// For folders this is published:
	//   "A user having access to a folder through one of the folder's ancestors
	//    will not be shown through this API" (reference/getfolderusercollaborators).
	//
	// For documents Lucid publishes no such sentence. The behaviour was verified
	// empirically against a live tenant under CXH-2285 — a user shared only on the
	// parent folder stays 404 here — and is modelled below so the connector's
	// Grant short-circuit is tested against the contract it actually relies on,
	// rather than against nothing. This is the one place this mock encodes
	// observed rather than documented behaviour; if Lucid ever starts reporting
	// inherited access here, these routes are where that shows up as a failure.
	parseCollaboratorUser := func(w http.ResponseWriter, r *http.Request) (int, bool) {
		uid, err := strconv.Atoi(r.PathValue("uid"))
		if err != nil {
			writeLucidError(w, http.StatusBadRequest, "badRequest", "user id must be numeric")
			return 0, false
		}
		return uid, true
	}

	// GET /folders/{id}/shares/users/{uid} — reference/getfolderusercollaborators
	mux.HandleFunc("GET /folders/{id}/shares/users/{uid}", func(w http.ResponseWriter, r *http.Request) {
		if !requireRest(w, r) {
			return
		}
		uid, ok := parseCollaboratorUser(w, r)
		if !ok {
			return
		}
		folderID := r.PathValue("id")
		sh, found, containerKnown := s.folderShare(folderID, uid)
		if !containerKnown {
			writeLucidError(w, http.StatusNotFound, "itemNotFound", "no such folder")
			return
		}
		if !found {
			writeLucidError(w, http.StatusNotFound, "itemNotFound", "user is not a direct collaborator on this folder")
			return
		}
		// folderId is a number in Lucid's folder collaborator record.
		folderIDNum, err := strconv.Atoi(folderID)
		if err != nil {
			writeLucidError(w, http.StatusNotFound, "itemNotFound", "no such folder")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"folderId": folderIDNum,
			"userId":   sh.userID,
			"role":     sh.role,
			"created":  sh.created,
		})
	})

	// GET /documents/{id}/shares/users/{uid} — reference/getdocumentusercollaborator
	mux.HandleFunc("GET /documents/{id}/shares/users/{uid}", func(w http.ResponseWriter, r *http.Request) {
		if !requireRest(w, r) {
			return
		}
		uid, ok := parseCollaboratorUser(w, r)
		if !ok {
			return
		}
		documentID := r.PathValue("id")
		sh, found, containerKnown := s.documentShare(documentID, uid)
		if !containerKnown {
			writeLucidError(w, http.StatusNotFound, "itemNotFound", "no such document")
			return
		}
		if !found {
			// Deliberately 404 even when the user reaches this document through the
			// parent folder: inherited access is not a collaborator record.
			writeLucidError(w, http.StatusNotFound, "itemNotFound", "user is not a direct collaborator on this document")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"documentId": documentID,
			"userId":     sh.userID,
			"role":       sh.role,
			"created":    sh.created,
		})
	})

	// GET /scim/v2/Users — https://lucid.readme.io/reference/getallusers
	// Lucid documents that "Deactivated users will not be
	// included in the totalResults or the JSON payload of users returned"
	// (reference/getallusers) — which is exactly why reading users over REST and
	// writing over SCIM, as the connector does, is the correct split.
	mux.HandleFunc("GET /scim/v2/Users", func(w http.ResponseWriter, r *http.Request) {
		if !requireScim(w, r) {
			return
		}
		var active []scimUser
		for _, u := range s.listUsers() {
			if u.Enabled {
				active = append(active, toScimUser(u))
			}
		}
		writeSCIMJSON(w, http.StatusOK, map[string]interface{}{
			"schemas":      []string{scimListSchema},
			"totalResults": len(active),
			"startIndex":   1,
			"itemsPerPage": len(active),
			"Resources":    active,
		})
	})

	// GET /scim/v2/Users/{id} — https://lucid.readme.io/reference/getuser-1
	// 404s specifically for absence, which is what lets the connector tell a
	// deleted user apart from REST's ambiguous 403.
	mux.HandleFunc("GET /scim/v2/Users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireScim(w, r) {
			return
		}
		id := r.PathValue("id")
		numericID, ok := parseScimID(id)
		if !ok {
			writeLucidError(w, http.StatusNotFound, "notFound",
				fmt.Sprintf("SCIM resource id must be of the form %s<userId>", scimIDPrefix))
			return
		}
		u, found := s.getUserByID(strconv.Itoa(numericID))
		if !found {
			log.Printf("GET /scim/v2/Users/%s — not found", id) //nolint:gosec // test-server: path value is diagnostic only
			writeLucidError(w, http.StatusNotFound, "notFound", "user not found")
			return
		}
		writeSCIMJSON(w, http.StatusOK, toScimUser(u))
	})

	// PATCH /scim/v2/Users/{id} — https://lucid.readme.io/reference/modifyuserpatch
	// Applies and PERSISTS the requested operations, then
	// echoes real state back. The previous version of this mock always replied
	// active:true and stored nothing, which made a deactivation impossible to
	// verify and could never have caught a wrong value on the wire.
	mux.HandleFunc("PATCH /scim/v2/Users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireScim(w, r) {
			return
		}
		id := r.PathValue("id")

		// Accept both Content-Type forms and log which arrived; -strict-scim-doc
		// enforces Lucid's documented application/json.
		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(ct, docContentType):
			// matches Lucid's published spec
		case strings.HasPrefix(ct, scimContentType):
			log.Printf("DIVERGENCE: Content-Type %q is RFC 7644-correct but Lucid's spec declares %q for SCIM", scimContentType, docContentType)
			if cfg.strictSCIMDoc {
				writeLucidError(w, http.StatusUnsupportedMediaType, "badRequest",
					fmt.Sprintf("Lucid's SCIM spec declares Content-Type %s, got %q", docContentType, ct))
				return
			}
		default:
			writeLucidError(w, http.StatusUnsupportedMediaType, "badRequest",
				fmt.Sprintf("Content-Type must be %s (Lucid spec) or %s (RFC 7644), got %q", docContentType, scimContentType, ct))
			return
		}

		numericID, ok := parseScimID(id)
		if !ok {
			log.Printf("PATCH /scim/v2/Users/%s — id is missing the %q prefix Lucid documents", id, scimIDPrefix) //nolint:gosec // test-server: path value is diagnostic only
			writeLucidError(w, http.StatusNotFound, "notFound",
				fmt.Sprintf("SCIM resource id must be of the form %s<userId>", scimIDPrefix))
			return
		}

		var body scimPatchOp
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeLucidError(w, http.StatusBadRequest, "badRequest", "malformed PatchOp body")
			return
		}
		// Same as Content-Type: accept both `schemas` forms, reject the RFC form
		// only under -strict-scim-doc.
		if len(body.Schemas) == 0 {
			writeLucidError(w, http.StatusBadRequest, "badRequest", "schemas is required")
			return
		}
		switch body.Schemas[0] {
		case docPatchSchema:
			// matches Lucid's published example
		case scimPatchOpSchema:
			log.Printf("DIVERGENCE: schemas[0]=%q is RFC 7644-correct but Lucid's spec example is %q", scimPatchOpSchema, docPatchSchema)
			if cfg.strictSCIMDoc {
				writeLucidError(w, http.StatusBadRequest, "badRequest",
					fmt.Sprintf("Lucid's SCIM spec example for schemas is [%s], got %q", docPatchSchema, body.Schemas[0]))
				return
			}
		default:
			writeLucidError(w, http.StatusBadRequest, "badRequest",
				fmt.Sprintf("schemas[0] must be %s (Lucid spec) or %s (RFC 7644), got %q", docPatchSchema, scimPatchOpSchema, body.Schemas[0]))
			return
		}
		if len(body.Operations) == 0 {
			writeLucidError(w, http.StatusBadRequest, "badRequest", "Operations must not be empty")
			return
		}
		// Same again for operation paths: Lucid documents only bare attribute paths
		// (e.g. "roles"), not SCIM value filters like "emails[primary eq true].value".
		for _, op := range body.Operations {
			if strings.ContainsAny(op.Path, "[]") {
				log.Printf("DIVERGENCE: PATCH path %q uses a SCIM value filter; Lucid documents "+
					"no filtered-path support (its path example is the bare attribute %q)",
					op.Path, "roles")
				if cfg.strictSCIMDoc {
					writeLucidError(w, http.StatusBadRequest, "badRequest",
						fmt.Sprintf("Lucid documents no filtered-path support for PATCH; path must be a bare attribute, got %q", op.Path))
					return
				}
				continue
			}
			// Reject a malformed `emails` value here instead of letting it silently
			// no-op with a 200.
			isWrite := strings.EqualFold(op.Op, "replace") || strings.EqualFold(op.Op, "add")
			if op.Path == "emails" && isWrite {
				if _, ok := primaryEmailFromPatchValue(op.Value); !ok {
					writeLucidError(w, http.StatusBadRequest, "badRequest",
						"emails operation carries no usable address; expected [{\"value\":\"…\",\"primary\":true}]")
					return
				}
			}
		}

		var applied []string
		updated, found := s.updateUser(numericID, func(u *user) {
			for _, op := range body.Operations {
				if !strings.EqualFold(op.Op, "replace") && !strings.EqualFold(op.Op, "add") {
					continue
				}
				switch op.Path {
				case "active":
					if b, ok := op.Value.(bool); ok {
						u.Enabled = b
						applied = append(applied, fmt.Sprintf("active=%v", b))
					}
				case "name.givenName":
					if v, ok := op.Value.(string); ok {
						_, family, _ := strings.Cut(u.Name, " ")
						u.Name = strings.TrimSpace(v + " " + family)
						applied = append(applied, "name.givenName")
					}
				case "name.familyName":
					if v, ok := op.Value.(string); ok {
						given, _, _ := strings.Cut(u.Name, " ")
						u.Name = strings.TrimSpace(given + " " + v)
						applied = append(applied, "name.familyName")
					}
				case "emails":
					// Bare attribute path, the shape Lucid documents.
					if v, ok := primaryEmailFromPatchValue(op.Value); ok {
						u.Email = v
						applied = append(applied, "emails")
					}
				case "emails[primary eq true].value":
					// RFC 7644's filtered form; only reachable with -strict-scim-doc off.
					if v, ok := op.Value.(string); ok {
						u.Email = v
						applied = append(applied, "emails")
					}
				case "userName":
					if v, ok := op.Value.(string); ok {
						u.Username = v
						applied = append(applied, "userName")
					}
				case "roles":
					if list, ok := op.Value.([]interface{}); ok {
						roles := make([]string, 0, len(list))
						for _, entry := range list {
							if m, ok := entry.(map[string]interface{}); ok {
								if v, ok := m["value"].(string); ok {
									roles = append(roles, v)
								}
							}
						}
						u.Roles = roles
						applied = append(applied, "roles")
					}
				}
			}
		})
		if !found {
			log.Printf("PATCH /scim/v2/Users/%s — no such user", id) //nolint:gosec // test-server: path value is diagnostic only
			writeLucidError(w, http.StatusNotFound, "notFound", "user not found")
			return
		}

		log.Printf("PATCH /scim/v2/Users/%s applied=[%s] → active=%v", id, strings.Join(applied, " "), updated.Enabled) //nolint:gosec // test-server: path value is diagnostic only
		writeSCIMJSON(w, http.StatusOK, toScimUser(updated))
	})

	// DELETE /scim/v2/Users/{id} — https://lucid.readme.io/reference/deleteuser
	// Lucid documents 204 on success, 404 for an unknown user,
	// and 409 "if the user cannot be deleted (e.g., account owner or default
	// document owner)" (reference/deleteuser). The stock mock always returned 204,
	// so neither error path was reachable.
	mux.HandleFunc("DELETE /scim/v2/Users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireScim(w, r) {
			return
		}
		id := r.PathValue("id")

		numericID, ok := parseScimID(id)
		if !ok {
			log.Printf("DELETE /scim/v2/Users/%s — id is missing the %q prefix Lucid documents", id, scimIDPrefix) //nolint:gosec // test-server: path value is diagnostic only
			writeLucidError(w, http.StatusNotFound, "notFound",
				fmt.Sprintf("SCIM resource id must be of the form %s<userId>", scimIDPrefix))
			return
		}

		if cfg.protectedUsers[numericID] {
			log.Printf("DELETE /scim/v2/Users/%s — protected user, returning 409", id) //nolint:gosec // test-server: path value is diagnostic only
			writeLucidError(w, http.StatusConflict, "alreadyExists",
				"the user cannot be deleted (account owner or default document owner)")
			return
		}

		if !s.deleteUserByID(numericID) {
			log.Printf("DELETE /scim/v2/Users/%s — no such user, returning 404", id) //nolint:gosec // test-server: path value is diagnostic only
			writeLucidError(w, http.StatusNotFound, "notFound", "user not found")
			return
		}

		log.Printf("DELETE /scim/v2/Users/%s removed=true", id) //nolint:gosec // test-server: path value is diagnostic only
		w.WriteHeader(http.StatusNoContent)
	})

	// Catch-all: any route not registered above returns 404 so a wrong connector
	// path is caught immediately rather than silently swallowed.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("UNMATCHED ROUTE: %s %s — returning 404", r.Method, r.URL.Path) //nolint:gosec // test-server: path value is diagnostic only
		writeLucidError(w, http.StatusNotFound, "unknownOperation", "no such endpoint")
	})

	return mux
}

func schemeOf(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func parseProtected(raw string) map[int]bool {
	out := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.Atoi(part); err == nil {
			out[id] = true
		}
	}
	return out
}

func run() error {
	addr := flag.String("addr", ":8080", "address to listen on")
	users := flag.Int("users", 0, "replace the seed fixtures with exactly N generated users (0 = keep fixtures); use to hit pagination boundaries")
	legacy404 := flag.Bool("legacy-user-404", false, "GET /v1/users/{id} returns 404 for an unknown user instead of the documented 403 (A/B for the delete-retry path)")
	protected := flag.String("protected-users", "", "comma-separated numeric user IDs that SCIM DELETE rejects with 409, as Lucid does for account/document owners")
	transferLimit := flag.Bool("transfer-rate-limit", false, "enforce Lucid's documented 30 requests / 5 seconds limit on transferUserContent, returning 429 + Retry-After")
	pageSize := flag.Int("page-size", lucidPageSize, "records per page for GET /users (Lucid's documented default and maximum is 200)")
	scimToken := flag.String("scim-token", "test-scim-token", "bearer token the SCIM surface requires; must match --lucid-scim-token")
	strictSCIMDoc := flag.Bool("strict-scim-doc", false,
		"reject SCIM requests that follow RFC 7644 where Lucid's spec documents otherwise "+
			"(Content-Type, schemas URN, attribute paths)")
	flag.Parse()

	s := newStore()
	// Same cap POST /_test/users enforces — otherwise the raw flag value reaches
	// seedUsers unvalidated and a mistyped -users OOMs the mock in makeslice.
	if *users < 0 || *users > maxSeedUsers {
		return fmt.Errorf("users flag must be in [0, %d]", maxSeedUsers)
	}
	if *users > 0 {
		s.seedUsers(*users)
	}

	cfg := config{
		legacyUser404:  *legacy404,
		protectedUsers: parseProtected(*protected),
		transferLimit:  *transferLimit,
		pageSize:       *pageSize,
		scimToken:      *scimToken,
		strictSCIMDoc:  *strictSCIMDoc,
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newMux(s, cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("lucidchart test-server listening on http://%s", *addr)
	log.Printf("  users=%d  pageSize=%d  legacyUser404=%v  protected=%v  transferRateLimit=%v",
		len(s.listUsers()), cfg.pageSize, cfg.legacyUser404, *protected, cfg.transferLimit)
	if !cfg.legacyUser404 {
		log.Printf("  NOTE: GET /v1/users/{id} returns 403 for unknown users, per Lucid's docs.")
		log.Printf("        Use -legacy-user-404 to serve the permissive 404 instead.")
	}
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
