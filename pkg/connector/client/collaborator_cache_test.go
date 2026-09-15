package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// singleCollaboratorServer mocks the single-collaborator GET and records the
// Cache-Control header of every request it receives, plus the role it served, so
// tests can prove the read-before-write pre-check is never answered from
// uhttp's GET response cache.
type singleCollaboratorServer struct {
	server *httptest.Server

	mu            sync.Mutex
	cacheControls []string // Cache-Control header per request, in call order
	role          string   // role the next response reports
	calls         int
}

func (s *singleCollaboratorServer) recordedCacheControls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cacheControls...)
}

func (s *singleCollaboratorServer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *singleCollaboratorServer) setRole(role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.role = role
}

// newSingleCollaboratorServer serves GET /{kind}/{id}/shares/users/{uid} with a
// collaborator record. kind is "folders" or "documents".
func newSingleCollaboratorServer(t *testing.T, kind string) *singleCollaboratorServer {
	t.Helper()

	s := &singleCollaboratorServer{role: "view"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+kind+"/{id}/shares/users/{uid}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls++
		s.cacheControls = append(s.cacheControls, r.Header.Get("Cache-Control"))
		role := s.role
		s.mu.Unlock()

		body := map[string]interface{}{
			"userId":  100,
			"role":    role,
			"created": "2024-01-01T00:00:00Z",
		}
		if kind == "documents" {
			body["documentId"] = r.PathValue("id")
		} else {
			body["folderId"] = 9001
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})

	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)

	return s
}

func newCacheTestClient(t *testing.T, baseURL string) *LucidchartClient {
	t.Helper()
	c, err := NewLucidchartClient(context.Background(), LucidchartConfig{ //nolint:gosec // G101: static token literal for tests, not a real credential
		APIKey:  "test-api-key",
		BaseURL: baseURL,
	})
	require.NoError(t, err)
	return c
}

// The collaborator pre-check GETs are read-before-write: Grant() compares the
// role they return against the requested one to decide whether to skip the
// authoritative upsert. uhttp's BaseHttpClient caches GET responses by default
// (memory backend, 1h TTL) for any request that does not set
// `Cache-Control: no-cache`, and never invalidates that entry on the PUT/DELETE
// issued against the same path. Without the header, a role changed by Revoke or
// in the Lucid UI would leave the pre-check reading a stale role, wrongly
// matching, skipping the upsert, and reporting GrantAlreadyExists — a silent
// no-op recorded by C1 as success.
func TestSingleCollaboratorGetBypassesResponseCache(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		kind string
		get  func(c *LucidchartClient) (string, error)
	}{
		{
			name: "GetFolderUserCollaborator",
			kind: "folders",
			get: func(c *LucidchartClient) (string, error) {
				resp, err := c.GetFolderUserCollaborator(ctx, "9001", "100")
				if err != nil {
					return "", err
				}
				return resp.Role, nil
			},
		},
		{
			name: "GetDocumentUserCollaborator",
			kind: "documents",
			get: func(c *LucidchartClient) (string, error) {
				resp, err := c.GetDocumentUserCollaborator(ctx, "doc-1", "100")
				if err != nil {
					return "", err
				}
				return resp.Role, nil
			},
		},
	} {
		t.Run(tc.name+" sends Cache-Control: no-cache", func(t *testing.T) {
			s := newSingleCollaboratorServer(t, tc.kind)
			c := newCacheTestClient(t, s.server.URL)

			role, err := tc.get(c)
			require.NoError(t, err)
			require.Equal(t, "view", role)
			require.Equal(t, []string{"no-cache"}, s.recordedCacheControls(),
				"the pre-check GET must opt out of uhttp's response cache")
		})

		// The header is only a means to an end; what matters is that a role
		// changed between two reads is actually observed rather than served from
		// the cache entry the first read populated.
		t.Run(tc.name+" re-reads a role changed upstream", func(t *testing.T) {
			s := newSingleCollaboratorServer(t, tc.kind)
			c := newCacheTestClient(t, s.server.URL)

			role, err := tc.get(c)
			require.NoError(t, err)
			require.Equal(t, "view", role)

			// Simulate a Revoke or an out-of-band role change in Lucid.
			s.setRole("edit")

			role, err = tc.get(c)
			require.NoError(t, err)
			require.Equal(t, "edit", role, "the second read must not be served from the response cache")
			require.Equal(t, 2, s.callCount(), "both reads must reach the API")
		})
	}
}
