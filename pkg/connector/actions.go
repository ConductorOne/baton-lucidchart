package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"

	config "github.com/conductorone/baton-sdk/pb/c1/config/v1"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/actions"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// Compile-time interface assertion. Mis-wiring fails the build, not the platform.
var _ connectorbuilder.GlobalActionProvider = (*Connector)(nil)

// knownScimRoles is the complete authoritative set of role strings accepted by
// Lucid's SCIM PATCH /Users/{id} (Modify User) endpoint (PascalCase).
// Source: https://lucid.readme.io/reference/modifyuserput
var knownScimRoles = []string{
	"AccountAdmin", "BillingAdmin", "Developer", "DocumentAdmin",
	"EnterpriseShieldAdmin", "TemplateAdmin",
}

func isKnownScimRole(role string) bool {
	for _, r := range knownScimRoles {
		if r == role {
			return true
		}
	}
	return false
}

// resolveScimRole returns the SCIM role name to send for a caller-supplied
// role. Native SCIM names pass through unchanged; REST names (kebab-case, as
// account creation takes) are translated via restRoleToScim. A REST role with
// no SCIM counterpart gets its own error, because reporting it as merely
// "invalid" alongside the SCIM enum hides the real problem: the role exists,
// Lucid's SCIM surface just cannot express it.
func resolveScimRole(role string) (string, error) {
	if isKnownScimRole(role) {
		return role, nil
	}
	if scimRole, ok := restRoleToScimRole(role); ok {
		return scimRole, nil
	}
	if isKnownRestRole(role) {
		return "", fmt.Errorf(
			"role %q has no SCIM equivalent, so it cannot be set by update_user: Lucid's SCIM roles are %v. "+
				"This role can only be assigned when the account is created",
			role, knownScimRoles)
	}
	return "", fmt.Errorf("invalid role %q (valid SCIM roles: %v)", role, knownScimRoles)
}

const (
	actionUpdateUser  = "update_user"
	actionDisableUser = "disable_user"
	actionEnableUser  = "enable_user"

	argUserID          = "user_id"
	retSuccess         = "success"
	retSuccessDisplay  = "Success"
	retConfirmedFields = "confirmed_fields"
	retActive          = "active"
)

var updateUserSchema = &v2.BatonActionSchema{
	Name:        actionUpdateUser,
	DisplayName: "Update User",
	Description: "Updates a user's profile attributes (firstName, lastName, email, username, roles) via SCIM PATCH /Users/{id}.",
	Arguments: []*config.Field{
		{Name: argUserID, DisplayName: "User Resource ID", Description: "The ID of the user to update.", Field: &config.Field_StringField{}, IsRequired: true},
		{
			Name:        "user_profile",
			DisplayName: "User Profile Data",
			Description: "A JSON object of attributes to update (firstName, lastName, email, username, roles). " +
				"roles accepts either SCIM names (AccountAdmin) or the kebab-case names account creation takes (team-admin); " +
				"account-owner, group-admin, organizational-group-admin and team-manager have no SCIM equivalent and are rejected.",
			Field:      &config.Field_StringField{},
			IsRequired: true,
		},
	},
	ReturnTypes: []*config.Field{
		{Name: retSuccess, DisplayName: retSuccessDisplay, Field: &config.Field_BoolField{}},
		{Name: "updated_fields", DisplayName: "Updated Fields", Field: &config.Field_StringField{}},
		{
			Name:        retConfirmedFields,
			DisplayName: "SCIM-Confirmed Fields",
			Description: "The subset of updated_fields that Lucid's SCIM response echoed back with the requested value. " +
				"Text is matched ignoring case, and roles count as confirmed when the requested ones are all present, since " +
				"Lucid may return an effective role set carrying extras. Omitted entirely when Lucid answered without a " +
				"usable resource body — either no body at all, or one the connector could not decode — and so confirmed " +
				"nothing. Present but empty when Lucid returned a resource body that echoed " +
				"none of the requested attributes back — including when Lucid echoed attributes back and " +
				"contradicted every one of them, which is logged but does not fail the action, since Lucid's " +
				"PATCH response is not documented as the authoritative post-update state.",
			Field: &config.Field_StringField{},
		},
	},
	ActionType: []v2.ActionType{
		v2.ActionType_ACTION_TYPE_ACCOUNT,
		v2.ActionType_ACTION_TYPE_ACCOUNT_UPDATE_PROFILE,
	},
}

var disableUserSchema = &v2.BatonActionSchema{
	Name:        actionDisableUser,
	DisplayName: "Disable User",
	Description: "Deactivates a user via SCIM (sets active=false). Soft, reversible. Requires a SCIM token (Enterprise tier).",
	Arguments: []*config.Field{
		{Name: argUserID, DisplayName: "User ID", Description: "The ID of the user to deactivate.", Field: &config.Field_StringField{}, IsRequired: true},
	},
	ReturnTypes: []*config.Field{
		{Name: retSuccess, DisplayName: retSuccessDisplay, Field: &config.Field_BoolField{}},
		{
			Name:        retActive,
			DisplayName: "Active",
			Description: "The active state Lucid's SCIM response confirmed. Omitted if Lucid answered without a usable resource " +
				"body (no body at all, or one the connector could not decode), or with a body that omitted the active " +
				"attribute — RFC 7644 lets a SCIM server echo back only a subset of the resource.",
			Field: &config.Field_BoolField{},
		},
	},
	ActionType: []v2.ActionType{
		v2.ActionType_ACTION_TYPE_ACCOUNT,
		v2.ActionType_ACTION_TYPE_ACCOUNT_DISABLE,
	},
}

var enableUserSchema = &v2.BatonActionSchema{
	Name:        actionEnableUser,
	DisplayName: "Enable User",
	Description: "Reactivates a user via SCIM (sets active=true). Requires a SCIM token (Enterprise tier).",
	Arguments: []*config.Field{
		{Name: argUserID, DisplayName: "User ID", Description: "The ID of the user to reactivate.", Field: &config.Field_StringField{}, IsRequired: true},
	},
	ReturnTypes: []*config.Field{
		{Name: retSuccess, DisplayName: retSuccessDisplay, Field: &config.Field_BoolField{}},
		{
			Name:        retActive,
			DisplayName: "Active",
			Description: "The active state Lucid's SCIM response confirmed. Omitted if Lucid answered without a usable resource " +
				"body (no body at all, or one the connector could not decode), or with a body that omitted the active " +
				"attribute — RFC 7644 lets a SCIM server echo back only a subset of the resource.",
			Field: &config.Field_BoolField{},
		},
	},
	ActionType: []v2.ActionType{
		v2.ActionType_ACTION_TYPE_ACCOUNT,
		v2.ActionType_ACTION_TYPE_ACCOUNT_ENABLE,
	},
}

// GlobalActions registers the connector's custom lifecycle actions.
func (c *Connector) GlobalActions(ctx context.Context, registry actions.ActionRegistry) error {
	if err := registry.Register(ctx, updateUserSchema, c.updateUserHandler); err != nil {
		return fmt.Errorf("baton-lucidchart: register update_user: %w", err)
	}
	if err := registry.Register(ctx, disableUserSchema, c.disableUserHandler); err != nil {
		return fmt.Errorf("baton-lucidchart: register disable_user: %w", err)
	}
	if err := registry.Register(ctx, enableUserSchema, c.enableUserHandler); err != nil {
		return fmt.Errorf("baton-lucidchart: register enable_user: %w", err)
	}
	return nil
}

func (c *Connector) updateUserHandler(
	ctx context.Context,
	args *structpb.Struct,
) (*structpb.Struct, annotations.Annotations, error) {
	userID, ok := actions.GetStringArg(args, argUserID)
	if !ok || userID == "" {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-lucidchart: update_user: user_id is required")
	}

	if !c.client.ScimConfigured() {
		return nil, nil, status.Error(codes.Unimplemented, "baton-lucidchart: update_user: SCIM not configured (a SCIM bearer token, Enterprise tier, is required)")
	}

	profile, err := profileArgAsMap(args, "user_profile")
	if err != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-lucidchart: update_user: %v", err)
	}

	payload := &client.UserUpdatePayload{}
	var updated []string

	if v, ok := profile["firstName"].(string); ok && v != "" {
		payload.FirstName = v
		updated = append(updated, "firstName")
	}
	if v, ok := profile["lastName"].(string); ok && v != "" {
		payload.LastName = v
		updated = append(updated, "lastName")
	}
	if v, ok := profile["email"].(string); ok && v != "" {
		payload.Email = v
		updated = append(updated, "email")
	}
	if v, ok := profile["username"].(string); ok && v != "" {
		payload.Username = v
		updated = append(updated, "username")
	}
	if rawRoles, ok := profile["roles"]; ok {
		roles, err := parseRoles(rawRoles)
		if err != nil {
			return nil, nil, status.Errorf(codes.InvalidArgument, "baton-lucidchart: update_user: %v", err)
		}
		scimRoles := make([]string, 0, len(roles))
		for _, role := range roles {
			scimRole, err := resolveScimRole(role)
			if err != nil {
				return nil, nil, status.Errorf(codes.InvalidArgument, "baton-lucidchart: update_user: %v", err)
			}
			scimRoles = append(scimRoles, scimRole)
		}
		payload.Roles = scimRoles
		if len(scimRoles) > 0 {
			updated = append(updated, "roles")
		}
	}

	if len(updated) == 0 {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-lucidchart: update_user: no updatable fields provided")
	}

	confirmed, _, err := c.client.UpdateUser(ctx, userID, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-lucidchart: update_user %s: %w", userID, err)
	}

	fields := []actions.ReturnField{
		actions.NewStringReturnField("updated_fields", strings.Join(updated, ", ")),
	}
	// Report which of the requested changes Lucid's PATCH response echoed back,
	// rather than implying all of updated_fields landed. This is reporting, not
	// adjudication: the echo is the best signal we have about the post-update
	// state, but it is not documented as authoritative, so it never fails the
	// action. Omitted entirely when Lucid answered without a resource body, so an
	// empty confirmed_fields only ever means "Lucid told us the post-update state
	// and echoed none of the requested attributes back" and never "Lucid stayed
	// silent".
	if !confirmed.IsZero() {
		matched := confirmedFields(payload, confirmed)
		// Every requested field came back disagreeing. That is logged and nothing
		// more: failing here would rest on the unverified assumption that Lucid's
		// PATCH response is always the authoritative, immediate post-update state,
		// and Lucid's published spec documents 200 as unconditional success with no
		// "applied nothing" case, so a wholesale contradiction inside a 200 is an
		// undocumented vendor edge case the caller cannot act on. Debug, not Warn,
		// for exactly that reason. The 2xx is what we report on, and confirmed_fields
		// still comes back empty, which says the same thing to whoever reads it.
		//
		// The test is against all of updated_fields, not just the ones Lucid spoke
		// about, because an omitted attribute carries no information — SCIM permits
		// returning a subset of the resource — and must not count against the update.
		if contradicted := contradictedFields(payload, confirmed); len(contradicted) == len(updated) {
			ctxzap.Extract(ctx).Debug("baton-lucidchart: Lucid's post-update user contradicts the requested value for every field",
				zap.String("action", actionUpdateUser),
				zap.String("user_id", userID),
				zap.Strings("contradicted_fields", contradicted),
			)
		}
		fields = append(fields, actions.NewStringReturnField(retConfirmedFields, strings.Join(matched, ", ")))
	}

	result := actions.NewReturnValues(true, fields...)
	return result, nil, nil
}

// confirmedFields returns the requested fields whose values Lucid's SCIM
// response echoes back unchanged, in the same order and spelling as
// updated_fields. It returns nil both when Lucid answered without a resource
// body — a legitimate SCIM response and not a failure — and when the body it did
// return confirmed nothing, so callers that need to distinguish those two must
// check confirmed.IsZero() themselves.
//
// It matches on the same terms contradictedFields disagrees on — case-insensitive
// strings, roles by subset — so the two can never both decline the same
// attribute. Were they to disagree, a value Lucid case-normalized (or a role set
// it returned with an extra entry) would count as neither confirmed nor
// contradicted and report an empty confirmed_fields, which is the alarming
// "Lucid echoed nothing back" signal, for a change that plainly landed.
func confirmedFields(payload *client.UserUpdatePayload, confirmed *client.ScimUser) []string {
	if confirmed.IsZero() {
		return nil
	}

	matches := func(requested, got string) bool {
		return requested != "" && strings.EqualFold(requested, got)
	}

	var out []string
	if confirmed.Name != nil && matches(payload.FirstName, confirmed.Name.GivenName) {
		out = append(out, "firstName")
	}
	if confirmed.Name != nil && matches(payload.LastName, confirmed.Name.FamilyName) {
		out = append(out, "lastName")
	}
	if payload.Email != "" && hasEmail(confirmed, payload.Email) {
		out = append(out, "email")
	}
	if matches(payload.Username, confirmed.UserName) {
		out = append(out, "username")
	}
	if len(payload.Roles) > 0 && containsAllRoles(confirmed.RoleValues(), payload.Roles) {
		out = append(out, "roles")
	}
	return out
}

// contradictedFields returns the requested fields Lucid's SCIM response echoes
// back with a value other than the one that was asked for — the post-update
// state disagreeing with the request, which means the change did not take
// effect.
//
// It deliberately does not report a field Lucid simply left out. RFC 7644 lets a
// server return a subset of the resource, so an absent attribute carries no
// information either way; treating that as a contradiction would fail updates
// that did land. Absence is reported by leaving the field out of
// confirmedFields instead.
//
// Its comparisons are deliberately laxer than confirmedFields'. Failing to
// confirm a change costs an empty entry in confirmed_fields; wrongly declaring
// one contradicted fails the whole action, so anything short of demonstrable
// disagreement is left alone: strings compare case-insensitively (SCIM servers
// normalize case on identifiers), and roles count as contradicted only when a
// requested role is missing from the response, never when Lucid returns extra
// ones — a "replace" that lands can still echo an effective set carrying an
// implicit or default role.
func contradictedFields(payload *client.UserUpdatePayload, confirmed *client.ScimUser) []string {
	if confirmed.IsZero() {
		return nil
	}

	differs := func(requested, got string) bool {
		return requested != "" && got != "" && !strings.EqualFold(requested, got)
	}

	var out []string
	if confirmed.Name != nil && differs(payload.FirstName, confirmed.Name.GivenName) {
		out = append(out, "firstName")
	}
	if confirmed.Name != nil && differs(payload.LastName, confirmed.Name.FamilyName) {
		out = append(out, "lastName")
	}
	if payload.Email != "" && len(confirmed.Emails) > 0 && !hasEmail(confirmed, payload.Email) {
		out = append(out, "email")
	}
	if differs(payload.Username, confirmed.UserName) {
		out = append(out, "username")
	}
	if got := confirmed.RoleValues(); len(payload.Roles) > 0 && len(got) > 0 && !containsAllRoles(got, payload.Roles) {
		out = append(out, "roles")
	}
	return out
}

// hasEmail reports whether want appears anywhere in Lucid's echoed emails,
// ignoring case. SCIM "emails" is multi-valued, so the confirm/contradict
// comparison must look at every entry rather than at PrimaryEmail()'s single
// pick: a response carrying both the old and new address with neither flagged
// primary makes that pick the *old* one, which would read as a contradiction of
// an update that landed. PrimaryEmail() stays the right choice for display.
func hasEmail(confirmed *client.ScimUser, want string) bool {
	for _, e := range confirmed.Emails {
		if strings.EqualFold(e.Value, want) {
			return true
		}
	}
	return false
}

// containsAllRoles reports whether every requested role is present in got. It is
// a subset test, not set equality: Lucid echoing back roles beyond the ones that
// were requested is an effective role set, not a rejection of the request. Order
// is irrelevant — SCIM does not guarantee a multi-valued attribute comes back in
// the order it was sent.
func containsAllRoles(got, requested []string) bool {
	have := make(map[string]struct{}, len(got))
	for _, r := range got {
		have[strings.ToLower(r)] = struct{}{}
	}
	for _, r := range requested {
		if _, ok := have[strings.ToLower(r)]; !ok {
			return false
		}
	}
	return true
}

func (c *Connector) disableUserHandler(
	ctx context.Context,
	args *structpb.Struct,
) (*structpb.Struct, annotations.Annotations, error) {
	return c.setUserActive(ctx, args, false)
}

func (c *Connector) enableUserHandler(
	ctx context.Context,
	args *structpb.Struct,
) (*structpb.Struct, annotations.Annotations, error) {
	return c.setUserActive(ctx, args, true)
}

func (c *Connector) setUserActive(
	ctx context.Context,
	args *structpb.Struct,
	active bool,
) (*structpb.Struct, annotations.Annotations, error) {
	op := "disable_user"
	if active {
		op = "enable_user"
	}

	userID, ok := actions.GetStringArg(args, argUserID)
	if !ok || userID == "" {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-lucidchart: %s: user_id is required", op)
	}

	if !c.client.ScimConfigured() {
		return nil, nil, status.Error(codes.Unimplemented, fmt.Sprintf("baton-lucidchart: %s: SCIM not configured (a SCIM bearer token, Enterprise tier, is required)", op))
	}

	// SCIM PATCH replace of the active flag is idempotent: re-disabling an
	// already-inactive user (or re-enabling an active one) returns success from
	// the API, so no special "already in state" handling is required here.
	confirmed, annos, err := c.client.SetUserActive(ctx, userID, active)
	if err != nil {
		return nil, annos, fmt.Errorf("baton-lucidchart: %s %s: %w", op, userID, err)
	}

	// Report the state Lucid confirmed rather than the one that was requested.
	// Omitted entirely when Lucid answered without a resource body, so an absent
	// field reads as "unconfirmed" and never as "confirmed false".
	fields := []actions.ReturnField{}
	if got := confirmed.GetActive(); got != nil {
		// A body that disagrees with what was asked for is logged and nothing more.
		// Failing here would rest on the unverified assumption that Lucid's PATCH
		// response is always the authoritative, immediate post-update state; Lucid's
		// published spec documents 200 as unconditional success with no "applied
		// nothing" case, so a contradiction inside a 200 is an undocumented vendor
		// edge case the caller cannot act on. Debug, not Warn, for exactly that
		// reason. The 2xx is what we report on, and retActive still carries the state
		// Lucid actually named, so the disagreement stays visible to whoever reads it.
		if *got != active {
			ctxzap.Extract(ctx).Debug("baton-lucidchart: Lucid's post-update user contradicts the requested active state",
				zap.String("action", op),
				zap.String("user_id", userID),
				zap.Bool("requested_active", active),
				zap.Bool("confirmed_active", *got),
			)
		}
		fields = append(fields, actions.NewBoolReturnField(retActive, *got))
	}

	result := actions.NewReturnValues(true, fields...)
	return result, annos, nil
}

// profileArgAsMap accepts the user_profile arg as either a JSON string (how C1
// push rules send it) or a nested struct (manual invocation).
func profileArgAsMap(args *structpb.Struct, key string) (map[string]any, error) {
	v, ok := args.GetFields()[key]
	if !ok || v == nil {
		return nil, fmt.Errorf("%s is required", key)
	}
	switch k := v.GetKind().(type) {
	case *structpb.Value_StringValue:
		var m map[string]any
		if err := json.Unmarshal([]byte(k.StringValue), &m); err != nil {
			return nil, fmt.Errorf("invalid %s JSON: %w", key, err)
		}
		return m, nil
	case *structpb.Value_StructValue:
		return k.StructValue.AsMap(), nil
	default:
		return nil, fmt.Errorf("invalid %s format", key)
	}
}

// parseRoles normalizes a roles value that may arrive as a JSON array, a single
// string, or a comma-separated string.
func parseRoles(raw any) ([]string, error) {
	switch v := raw.(type) {
	case []interface{}:
		roles := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("roles must be strings")
			}
			roles = append(roles, s)
		}
		return roles, nil
	case []string:
		return v, nil
	case string:
		if v == "" {
			return nil, nil
		}
		parts := strings.Split(v, ",")
		roles := make([]string, 0, len(parts))
		for _, p := range parts {
			roles = append(roles, strings.TrimSpace(p))
		}
		return roles, nil
	default:
		return nil, fmt.Errorf("roles must be a list of strings")
	}
}
