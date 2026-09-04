package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/conductorone/baton-sdk/pkg/annotations"
)

// UserCreatePayload represents the JSON body accepted by the POST /users endpoint of Lucid.
// https://lucid.readme.io/reference/createuser
type UserCreatePayload struct {
	FirstName string   `json:"firstName"`
	LastName  string   `json:"lastName"`
	Email     string   `json:"email"`
	Username  string   `json:"username,omitempty"`
	Password  string   `json:"password,omitempty"`
	Roles     []string `json:"roles,omitempty"`
}

// UserUpdatePayload carries the profile attributes that callers wish to change.
// UpdateUser routes this through the SCIM PATCH surface; only non-empty fields
// are sent as SCIM replace operations.
type UserUpdatePayload struct {
	FirstName string   `json:"firstName,omitempty"`
	LastName  string   `json:"lastName,omitempty"`
	Email     string   `json:"email,omitempty"`
	Username  string   `json:"username,omitempty"`
	Roles     []string `json:"roles,omitempty"`
}

// TransferContentPayload is the body for POST /v1/transferUserContent. It moves
// documents owned by fromUser to toUser, the precondition for deleting a user
// whose content must be retained.
type TransferContentPayload struct {
	FromUser string `json:"fromUser"`
	ToUser   string `json:"toUser"`
}

// CreateUser creates a new user in the authenticated account.
func (c *LucidchartClient) CreateUser(ctx context.Context, payload *UserCreatePayload) (*User, annotations.Annotations, error) {
	if payload == nil {
		return nil, nil, fmt.Errorf("nil payload")
	}

	// Lucid recommends using the normal host for account operations.
	req, err := c.newRequest(ctx, http.MethodPost, "/users", payload, LucidAuthTypeOAuth2)
	if err != nil {
		return nil, nil, err
	}

	var created User
	_, err = c.doRequest(ctx, req, &created)
	if err != nil {
		return nil, nil, err
	}

	return &created, nil, nil
}

// UpdateUser updates an existing user's profile via SCIM PATCH /Users/{id}.
// Lucid has no modify-user REST endpoint; all profile changes go through the
// SCIM 2.0 surface (https://users.lucid.app/scim/v2/Users/{id}). Only fields
// with non-empty values in payload are sent. userID is the bare REST user ID
// (e.g. "101"); the lucid- SCIM prefix is applied internally.
func (c *LucidchartClient) UpdateUser(ctx context.Context, userID string, payload *UserUpdatePayload) (*User, annotations.Annotations, error) {
	if !c.ScimConfigured() {
		return nil, nil, errScimNotConfigured
	}

	var ops []ScimPatchOperation
	if payload.FirstName != "" {
		ops = append(ops, ScimPatchOperation{Op: scimOpReplace, Path: "name.givenName", Value: payload.FirstName})
	}
	if payload.LastName != "" {
		ops = append(ops, ScimPatchOperation{Op: scimOpReplace, Path: "name.familyName", Value: payload.LastName})
	}
	if payload.Email != "" {
		// Bare attribute path with the full multi-valued replacement, the shape
		// Lucid documents: its UserOperation.path example is the bare attribute
		// "roles", and the only documented use of the eq operator is the `filter`
		// query parameter on GET /Users — no filtered path such as
		// "emails[primary eq true].value" appears anywhere in Lucid's reference
		// (CXH-2282).
		//
		// A replace on a bare multi-valued attribute replaces the whole collection
		// rather than just the primary entry's value. That is safe here because
		// Lucid's user model holds exactly one address — the REST User exposes a
		// single `email`, and GET /Users/{id} returns one emails entry. The entry
		// mirrors that response, primary and type included, so the record Lucid
		// echoes back keeps the shape it had.
		ops = append(ops, ScimPatchOperation{
			Op:   scimOpReplace,
			Path: "emails",
			Value: []map[string]interface{}{
				{"value": payload.Email, "primary": true, "type": "work"},
			},
		})
	}
	if payload.Username != "" {
		ops = append(ops, ScimPatchOperation{Op: scimOpReplace, Path: "userName", Value: payload.Username})
	}
	if len(payload.Roles) > 0 {
		roleValues := make([]map[string]string, len(payload.Roles))
		for i, r := range payload.Roles {
			roleValues[i] = map[string]string{"value": r}
		}
		ops = append(ops, ScimPatchOperation{Op: scimOpReplace, Path: "roles", Value: roleValues})
	}

	if len(ops) == 0 {
		return nil, nil, fmt.Errorf("baton-lucidchart: update user: no updatable fields provided")
	}

	body := &ScimPatchOp{
		Schemas:    []string{scimPatchSchema},
		Operations: ops,
	}

	req, err := c.newScimRequest(ctx, http.MethodPatch, fmt.Sprintf(ScimUserPath, scimResourceID(userID)), body)
	if err != nil {
		return nil, nil, err
	}

	if _, err := c.doRequest(ctx, req, nil); err != nil {
		return nil, nil, err
	}

	return nil, nil, nil
}

// TransferContent moves all documents owned by fromUserEmail to toUserEmail via
// POST /v1/transferUserContent. The Lucid API requires email addresses for both
// fields ("Email of the user whose content will be transferred"). Call before
// deleting a user when their content must be retained.
func (c *LucidchartClient) TransferContent(ctx context.Context, fromUserEmail, toUserEmail string) (annotations.Annotations, error) {
	payload := &TransferContentPayload{
		FromUser: fromUserEmail,
		ToUser:   toUserEmail,
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/v1/transferUserContent", payload, LucidAuthTypeOAuth2)
	if err != nil {
		return nil, err
	}

	if _, err := c.doRequest(ctx, req, nil); err != nil {
		return nil, err
	}

	return nil, nil
}
