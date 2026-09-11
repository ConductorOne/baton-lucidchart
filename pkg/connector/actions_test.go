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
	lc, err := client.NewLucidchartClient(context.Background(), "api-key", ts, "http://localhost", "", "", "")
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

// scimActionConnector wires a Connector to a SCIM mock that answers every PATCH
// with the given body.
func scimActionConnector(t *testing.T, respond func(w http.ResponseWriter)) *Connector {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		respond(w)
	}))
	t.Cleanup(srv.Close)

	return &Connector{client: testLucidClient(t, srv.URL, srv.URL)}
}

func jsonBody(body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

// update_user reports which of the requested changes Lucid's SCIM response
// actually echoed back, instead of discarding that response entirely.
func TestUpdateUserHandler_ReportsScimConfirmedFields(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{
		"id": "lucid-7",
		"userName": "ada.lovelace",
		"name": {"givenName": "Ada", "familyName": "Stale"},
		"emails": [{"value": "ada@example.com", "primary": true}]
	}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"firstName":"Ada","lastName":"Lovelace","email":"ada@example.com","username":"ada.lovelace"}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.NoError(t, err)

	fields := res.AsMap()
	require.Equal(t, true, fields["success"])
	require.Equal(t, "firstName, lastName, email, username", fields["updated_fields"])
	// lastName is absent: Lucid answered "Stale", not the requested "Lovelace".
	require.Equal(t, "firstName, email, username", fields["confirmed_fields"])
}

// A bodyless PATCH response confirms nothing, and must not be reported as
// confirming everything — nor as an empty confirmation, which is the distinct
// and far more alarming "Lucid answered and matched none of them".
func TestUpdateUserHandler_NoResponseBodyOmitsConfirmedFields(t *testing.T) {
	c := scimActionConnector(t, func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) })

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"firstName":"Ada"}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.NoError(t, err)

	fields := res.AsMap()
	require.Equal(t, true, fields["success"])
	require.Equal(t, "firstName", fields["updated_fields"])
	require.NotContains(t, fields, "confirmed_fields")
}

// Lucid returned a resource body but echoed none of the requested attributes
// back. SCIM permits returning a subset of the resource, so that confirms
// nothing either way and must not fail an update that may well have landed — it
// reports an empty confirmed_fields, distinct from the omitted-entirely case.
func TestUpdateUserHandler_ResponseBodyEchoingNothing_ReportsEmptyConfirmedFields(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{"id":"lucid-7"}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"firstName":"Ada","lastName":"Lovelace"}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.NoError(t, err)

	fields := res.AsMap()
	require.Equal(t, true, fields["success"])
	require.Equal(t, "firstName, lastName", fields["updated_fields"])
	require.Contains(t, fields, "confirmed_fields")
	require.Equal(t, "", fields["confirmed_fields"])
}

// Lucid echoed the requested attributes back and contradicted every one of
// them. Nothing landed, so the action must fail rather than report success.
func TestUpdateUserHandler_ResponseBodyContradictingEverything_ReturnsFailedPrecondition(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{
		"id": "lucid-7",
		"userName": "someone.else",
		"name": {"givenName": "Stale", "familyName": "Stale"}
	}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"firstName":"Ada","lastName":"Lovelace"}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.Error(t, err)
	require.Nil(t, res)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// A "replace" on roles that landed can still echo back an effective set that
// carries an implicit or default role. Extra roles are not a rejection of the
// request, so a roles-only update must not fail on them.
func TestUpdateUserHandler_ExtraRolesInResponseAreNotContradiction(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{"id":"lucid-7","roles":[{"value":"Developer"},{"value":"DocumentAdmin"}]}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"roles":["Developer"]}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.NoError(t, err)

	fields := res.AsMap()
	require.Equal(t, true, fields["success"])
	// Not merely "not contradicted": the requested role is present, so it must
	// read as confirmed. An empty confirmed_fields here would report the change
	// as unacknowledged when it plainly landed.
	require.Equal(t, "roles", fields["confirmed_fields"])
}

// A role Lucid did not apply is absent from the response, and that is the only
// thing that counts as a contradiction for roles.
func TestUpdateUserHandler_MissingRequestedRoleIsContradiction(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{"id":"lucid-7","roles":[{"value":"Developer"}]}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"roles":["DocumentAdmin"]}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.Error(t, err)
	require.Nil(t, res)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// Lucid normalizing the case of an identifier is not a contradiction. Both
// halves compare case-insensitively, so the address reads as confirmed rather
// than falling between the two and reporting an empty confirmed_fields.
func TestUpdateUserHandler_CaseNormalizedEmailIsNotContradiction(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{"id":"lucid-7","emails":[{"value":"ada@example.com","primary":true}]}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"email":"Ada@Example.com"}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.NoError(t, err)

	fields := res.AsMap()
	require.Equal(t, true, fields["success"])
	// The address matches bar its case, so it is confirmed, not merely
	// uncontradicted.
	require.Equal(t, "email", fields["confirmed_fields"])
}

// SCIM "emails" is multi-valued. A response carrying both the old and the new
// address with neither flagged primary makes PrimaryEmail() return the old one,
// so comparing against that single pick would fail an update that landed.
func TestUpdateUserHandler_EmailMatchedAcrossAllEntries(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{"id":"lucid-7","emails":[{"value":"old@example.com"},{"value":"new@example.com"}]}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"email":"new@example.com"}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.NoError(t, err)

	fields := res.AsMap()
	require.Equal(t, true, fields["success"])
	require.Equal(t, "email", fields["confirmed_fields"])
}

// The requested address appearing in no entry at all is the only thing that
// makes email a contradiction.
func TestUpdateUserHandler_EmailAbsentFromAllEntriesIsContradiction(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{"id":"lucid-7","emails":[{"value":"old@example.com"},{"value":"other@example.com"}]}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"email":"new@example.com"}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.Error(t, err)
	require.Nil(t, res)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// An attribute Lucid omitted must never help condemn the update. Here firstName
// is contradicted and roles is simply absent, so the roles half may well have
// landed — failing terminally would strand it on every retry.
func TestUpdateUserHandler_OmittedFieldDoesNotTriggerFailure(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{"id":"lucid-7","name":{"givenName":"Stale"}}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"firstName":"Ada","roles":["Developer"]}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.NoError(t, err)

	fields := res.AsMap()
	require.Equal(t, true, fields["success"])
	require.Equal(t, "", fields["confirmed_fields"])
}

// A contradiction on one attribute while others landed is a partial update, not
// a no-op: confirmed_fields already reports exactly which changes took, and
// failing the whole action would discard that.
func TestUpdateUserHandler_PartialContradictionStillReportsConfirmedFields(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{
		"id": "lucid-7",
		"name": {"givenName": "Ada", "familyName": "Stale"}
	}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"firstName":"Ada","lastName":"Lovelace"}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.NoError(t, err)

	fields := res.AsMap()
	require.Equal(t, true, fields["success"])
	require.Equal(t, "firstName", fields["confirmed_fields"])
}

func TestUpdateUserHandler_ConfirmsRolesRegardlessOfOrder(t *testing.T) {
	c := scimActionConnector(t, jsonBody(`{"id":"lucid-7","roles":[{"value":"Developer"},{"value":"DocumentAdmin"}]}`))

	args, err := structpb.NewStruct(map[string]any{
		"user_id":      "7",
		"user_profile": `{"roles":["DocumentAdmin","Developer"]}`,
	})
	require.NoError(t, err)

	res, _, err := c.updateUserHandler(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "roles", res.AsMap()["confirmed_fields"])
}

// disable_user / enable_user report the active state Lucid confirmed, not the
// one that was requested.
func TestSetUserActiveHandlers_ReturnConfirmedActiveState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		disable  bool
		wantBool bool
	}{
		{name: "disable", body: `{"id":"lucid-7","active":false}`, disable: true, wantBool: false},
		{name: "enable", body: `{"id":"lucid-7","active":true}`, disable: false, wantBool: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := scimActionConnector(t, jsonBody(tc.body))

			args, err := structpb.NewStruct(map[string]any{"user_id": "7"})
			require.NoError(t, err)

			handler := c.enableUserHandler
			if tc.disable {
				handler = c.disableUserHandler
			}

			res, _, err := handler(context.Background(), args)
			require.NoError(t, err)

			fields := res.AsMap()
			require.Equal(t, true, fields["success"])
			require.Equal(t, tc.wantBool, fields["active"])
		})
	}
}

// Lucid answered without an HTTP error but its body contradicts the requested
// state. A disable that plainly did not happen must not close out as a success.
func TestSetUserActiveHandlers_ConfirmedStateContradictsRequest_ReturnsFailedPrecondition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		disable bool
	}{
		{name: "disable confirmed still active", body: `{"id":"lucid-7","active":true}`, disable: true},
		{name: "enable confirmed still inactive", body: `{"id":"lucid-7","active":false}`, disable: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := scimActionConnector(t, jsonBody(tc.body))

			args, err := structpb.NewStruct(map[string]any{"user_id": "7"})
			require.NoError(t, err)

			handler := c.enableUserHandler
			if tc.disable {
				handler = c.disableUserHandler
			}

			res, _, err := handler(context.Background(), args)
			require.Error(t, err)
			require.Nil(t, res)
			require.Equal(t, codes.FailedPrecondition, status.Code(err))
		})
	}
}

// An absent active field must read as "unconfirmed", never as "confirmed false".
func TestSetUserActiveHandler_NoResponseBodyOmitsActive(t *testing.T) {
	c := scimActionConnector(t, func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) })

	args, err := structpb.NewStruct(map[string]any{"user_id": "7"})
	require.NoError(t, err)

	res, _, err := c.disableUserHandler(context.Background(), args)
	require.NoError(t, err)

	fields := res.AsMap()
	require.Equal(t, true, fields["success"])
	require.NotContains(t, fields, "active")
}
