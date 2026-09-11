package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestUpdateUserHandler_ScimNotConfigured_ReturnsUnimplemented(t *testing.T) {
	t.Parallel()

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauth-test-token"}) //nolint:gosec // G101: test token literal
	lc, err := client.NewLucidchartClient(context.Background(), "api-key", ts, "http://localhost", "", "")
	require.NoError(t, err)

	c := &Connector{client: lc}

	args, err := structpb.NewStruct(map[string]any{"user_id": "user-123"})
	require.NoError(t, err)

	_, _, handlerErr := c.updateUserHandler(context.Background(), args)
	require.Error(t, handlerErr)
	st, ok := status.FromError(handlerErr)
	require.True(t, ok, "error must be a gRPC status error")
	require.Equal(t, codes.Unimplemented, st.Code())
}

// newScimPatchServer serves SCIM PATCH /Users/lucid-42 and records the roles
// actually put on the wire, so tests can assert the translated value is sent
// rather than whatever the caller typed.
func newScimPatchServer(t *testing.T, sentRoles *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/Users/lucid-42" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var body struct {
			Operations []struct {
				Op    string `json:"op"`
				Path  string `json:"path"`
				Value any    `json:"value"`
			} `json:"Operations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode request body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		for _, op := range body.Operations {
			if op.Path != "roles" {
				continue
			}
			values, ok := op.Value.([]any)
			if !ok {
				t.Errorf("roles value must be a list, got %T", op.Value)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			for _, v := range values {
				entry, ok := v.(map[string]any)
				if !ok {
					t.Errorf("role entry must be an object, got %T", v)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				role, ok := entry["value"].(string)
				if !ok {
					t.Errorf("role entry must carry a string value, got %T", entry["value"])
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				*sentRoles = append(*sentRoles, role)
			}
		}

		w.Header().Set("Content-Type", "application/scim+json")
		_, _ = w.Write([]byte(`{"id":"lucid-42","userName":"user@example.com"}`))
	}))
}

// updateUserRoles invokes update_user with roles against srv and returns the
// handler error. What reached the wire is recorded by srv itself.
func updateUserRoles(t *testing.T, srv *httptest.Server, roles []any) error {
	t.Helper()

	c := &Connector{client: testLucidClient(t, srv.URL, srv.URL)}

	profile, err := structpb.NewStruct(map[string]any{"roles": roles})
	require.NoError(t, err)

	args, err := structpb.NewStruct(map[string]any{"user_id": "42"})
	require.NoError(t, err)
	args.Fields["user_profile"] = structpb.NewStructValue(profile)

	_, _, handlerErr := c.updateUserHandler(context.Background(), args)
	return handlerErr
}

func TestUpdateUserHandler_TranslatesRestRolesToScim(t *testing.T) {
	t.Parallel()

	// Every REST role with a SCIM counterpart, and the SCIM name it must reach
	// the wire as. team-admin -> AccountAdmin is the one non-transliterated
	// pair; see restRoleToScim in users.go.
	for restRole, wantScimRole := range map[string]string{
		"billing-admin":           "BillingAdmin",
		"developer":               "Developer",
		"document-admin":          "DocumentAdmin",
		"enterprise-shield-admin": "EnterpriseShieldAdmin",
		"team-admin":              "AccountAdmin",
		"template-admin":          "TemplateAdmin",
	} {
		t.Run(restRole, func(t *testing.T) {
			t.Parallel()

			var sent []string
			srv := newScimPatchServer(t, &sent)
			defer srv.Close()

			require.NoError(t, updateUserRoles(t, srv, []any{restRole}))
			require.Equal(t, []string{wantScimRole}, sent)
		})
	}
}

func TestUpdateUserHandler_NativeScimRolesStillPassThrough(t *testing.T) {
	t.Parallel()

	// Backward compatibility: callers already sending SCIM names must keep
	// working, and must reach the wire untranslated.
	for _, scimRole := range knownScimRoles {
		t.Run(scimRole, func(t *testing.T) {
			t.Parallel()

			var sent []string
			srv := newScimPatchServer(t, &sent)
			defer srv.Close()

			require.NoError(t, updateUserRoles(t, srv, []any{scimRole}))
			require.Equal(t, []string{scimRole}, sent)
		})
	}
}

func TestUpdateUserHandler_RestRoleWithoutScimEquivalentIsRejected(t *testing.T) {
	t.Parallel()

	// These are valid REST roles, so the error must say the role has no SCIM
	// equivalent rather than calling it invalid.
	for _, restRole := range []string{"account-owner", "group-admin", "organizational-group-admin", "team-manager"} {
		t.Run(restRole, func(t *testing.T) {
			t.Parallel()

			var sent []string
			srv := newScimPatchServer(t, &sent)
			defer srv.Close()

			err := updateUserRoles(t, srv, []any{restRole})
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok, "error must be a gRPC status error")
			require.Equal(t, codes.InvalidArgument, st.Code())
			require.Contains(t, st.Message(), restRole)
			require.Contains(t, st.Message(), "no SCIM equivalent")
			require.Empty(t, sent, "no SCIM PATCH should be sent for an untranslatable role")
		})
	}
}

func TestUpdateUserHandler_UnknownRoleIsRejectedAsInvalid(t *testing.T) {
	t.Parallel()

	var sent []string
	srv := newScimPatchServer(t, &sent)
	defer srv.Close()

	err := updateUserRoles(t, srv, []any{"not-a-role"})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok, "error must be a gRPC status error")
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), `invalid role "not-a-role"`)
	// A role Lucid does not define at all is invalid, not merely unmappable.
	require.NotContains(t, st.Message(), "no SCIM equivalent")
	require.Empty(t, sent)
}

func TestRestRoleToScim_StaysInSyncWithBothEnums(t *testing.T) {
	t.Parallel()

	// Guards the two enums against drift: a role added to either list without
	// a decision about the other side fails here rather than at runtime.
	for restRole, scimRole := range restRoleToScim {
		require.True(t, isKnownRestRole(restRole), "%q is not a known REST role", restRole)
		require.True(t, isKnownScimRole(scimRole), "%q is not a known SCIM role", scimRole)
	}

	// The mapping is onto: every SCIM role is reachable from some REST role.
	reachable := make(map[string]bool, len(restRoleToScim))
	for _, scimRole := range restRoleToScim {
		require.False(t, reachable[scimRole], "%q is the target of more than one REST role", scimRole)
		reachable[scimRole] = true
	}
	for _, scimRole := range knownScimRoles {
		require.True(t, reachable[scimRole], "no REST role maps to %q", scimRole)
	}
}
