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

const (
	actionUpdateUser  = "update_user"
	actionDisableUser = "disable_user"
	actionEnableUser  = "enable_user"

	argUserID         = "user_id"
	retSuccess        = "success"
	retSuccessDisplay = "Success"
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
			Description: "A JSON object of attributes to update (firstName, lastName, email, username, roles).",
			Field:       &config.Field_StringField{},
			IsRequired:  true,
		},
	},
	ReturnTypes: []*config.Field{
		{Name: retSuccess, DisplayName: retSuccessDisplay, Field: &config.Field_BoolField{}},
		{Name: "updated_fields", DisplayName: "Updated Fields", Field: &config.Field_StringField{}},
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
		for _, role := range roles {
			if !isKnownScimRole(role) {
				return nil, nil, status.Errorf(codes.InvalidArgument, "baton-lucidchart: update_user: invalid role %q (valid SCIM roles: %v)", role, knownScimRoles)
			}
		}
		payload.Roles = roles
		if len(roles) > 0 {
			updated = append(updated, "roles")
		}
	}

	if len(updated) == 0 {
		return nil, nil, status.Errorf(codes.InvalidArgument, "baton-lucidchart: update_user: no updatable fields provided")
	}

	if _, _, err := c.client.UpdateUser(ctx, userID, payload); err != nil {
		return nil, nil, fmt.Errorf("baton-lucidchart: update_user %s: %w", userID, err)
	}

	result := actions.NewReturnValues(true, actions.NewStringReturnField("updated_fields", strings.Join(updated, ", ")))
	return result, nil, nil
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
	annos, err := c.client.SetUserActive(ctx, userID, active)
	if err != nil {
		return nil, annos, fmt.Errorf("baton-lucidchart: %s %s: %w", op, userID, err)
	}

	result := actions.NewReturnValues(true)
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
