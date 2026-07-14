package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
)

const scimContentType = "application/scim+json"

const scimOpReplace = "replace"

var (
	// ScimUserPath is the SCIM 2.0 single-user resource path: /Users/{id}.
	ScimUserPath = "/Users/%s"

	scimPatchOpSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
)

// scimResourceID converts a bare REST userId (e.g. "101") to the SCIM resource
// ID that Lucid expects (e.g. "lucid-101"). The Lucid SCIM surface uses this
// prefix for all /Users/{id} operations; the REST API uses the bare numeric ID.
func scimResourceID(restUserID string) string {
	return "lucid-" + restUserID
}

// ScimPatchOp is a SCIM 2.0 PatchOp request body.
type ScimPatchOp struct {
	Schemas    []string             `json:"schemas"`
	Operations []ScimPatchOperation `json:"Operations"`
}

// ScimPatchOperation is a single operation within a SCIM PatchOp.
type ScimPatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

// errScimNotConfigured is returned when a SCIM operation is attempted without a
// SCIM bearer token. Deprovisioning requires Lucid Enterprise tier.
var errScimNotConfigured = errors.New("SCIM is not configured: a SCIM bearer token (Enterprise tier) is required for user deprovisioning")

// newScimRequest builds a request against the SCIM base URL using the separate
// SCIM bearer token and SCIM 2.0 content negotiation.
func (c *LucidchartClient) newScimRequest(
	ctx context.Context,
	method string,
	path string,
	body interface{},
) (*http.Request, error) {
	urlAddress, err := url.Parse(c.scimBaseURL)
	if err != nil {
		return nil, err
	}

	urlAddress = urlAddress.JoinPath(path)

	options := []uhttp.RequestOption{
		uhttp.WithBearerToken(c.scimToken),
		uhttp.WithAccept(scimContentType),
	}

	if body != nil {
		// WithJSONBody marshals the body and sets application/json; override the
		// content type afterwards so it wins (SCIM expects application/scim+json).
		options = append(options, uhttp.WithJSONBody(body), uhttp.WithContentType(scimContentType))
	}

	return c.client.NewRequest(ctx, method, urlAddress, options...)
}

// SetUserActive toggles a user's active state via SCIM PATCH. active=false is a
// soft, reversible deactivation; active=true reactivates a deactivated user.
func (c *LucidchartClient) SetUserActive(ctx context.Context, userID string, active bool) (annotations.Annotations, error) {
	if !c.ScimConfigured() {
		return nil, errScimNotConfigured
	}

	body := &ScimPatchOp{
		Schemas: []string{scimPatchOpSchema},
		Operations: []ScimPatchOperation{
			{Op: scimOpReplace, Path: "active", Value: active},
		},
	}

	req, err := c.newScimRequest(ctx, http.MethodPatch, fmt.Sprintf(ScimUserPath, scimResourceID(userID)), body)
	if err != nil {
		return nil, err
	}

	if _, err := c.doRequest(ctx, req, nil); err != nil {
		return nil, err
	}

	return nil, nil
}

// ScimDeleteUser permanently deletes a user via SCIM DELETE /Users/{id}. This
// is a hard delete; callers should transfer owned content first when it must be
// retained (see TransferContent).
func (c *LucidchartClient) ScimDeleteUser(ctx context.Context, userID string) (annotations.Annotations, error) {
	if !c.ScimConfigured() {
		return nil, errScimNotConfigured
	}

	req, err := c.newScimRequest(ctx, http.MethodDelete, fmt.Sprintf(ScimUserPath, scimResourceID(userID)), nil)
	if err != nil {
		return nil, err
	}

	if _, err := c.doRequest(ctx, req, nil); err != nil {
		return nil, err
	}

	return nil, nil
}
