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
	Password  string   `json:"password,omitempty"` //nolint:gosec // G117: Lucidchart API request field, not a hardcoded credential
	Roles     []string `json:"roles,omitempty"`
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
