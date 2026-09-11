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

// scimContentType is the media type Lucid's SCIM surface documents.
// RFC 7644's "application/scim+json" appears nowhere in Lucid's reference.
const scimContentType = "application/json"

const scimOpReplace = "replace"

var (
	// ScimUserPath is the SCIM 2.0 single-user resource path: /Users/{id}.
	ScimUserPath = "/Users/%s"

	// scimPatchSchema is the `schemas` value Lucid documents for a PATCH
	// /Users/{id} body, in place of RFC 7644's PatchOp URN.
	scimPatchSchema = "urn:ietf:params:scim:schemas:core:2.0:User"
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

// errContentScimNotConfigured is returned when a content-access SCIM operation
// is attempted without the second (content-access) bearer token.
var errContentScimNotConfigured = errors.New("SCIM for content access is not configured: the lucid-content-scim-token bearer token is required for content-access deprovisioning")

// newScimRequest builds a request against the SCIM base URL using the
// admin-management SCIM bearer token and the content negotiation Lucid's SCIM
// surface documents.
func (c *LucidchartClient) newScimRequest(
	ctx context.Context,
	method string,
	path string,
	body interface{},
) (*http.Request, error) {
	return c.newScimRequestWithToken(ctx, c.scimToken, method, path, body)
}

// newScimRequestWithToken is the shared SCIM request builder. Lucid's two SCIM
// integrations ("admin management" and "content access") share one base URL and
// are distinguished only by the bearer token, so everything except the token is
// identical between them.
func (c *LucidchartClient) newScimRequestWithToken(
	ctx context.Context,
	token string,
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
		uhttp.WithBearerToken(token),
		uhttp.WithAccept(scimContentType),
	}

	if body != nil {
		// WithJSONBody already sets application/json; pin it explicitly too.
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
		Schemas: []string{scimPatchSchema},
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

// ScimUserExists reports whether the user still exists, via SCIM GET /Users/{id}.
// SCIM 404s specifically for absence, which disambiguates REST's overloaded 403.
// A non-nil error means "unknown" — never treat it as "gone".
func (c *LucidchartClient) ScimUserExists(ctx context.Context, userID string) (bool, error) {
	if !c.ScimConfigured() {
		return false, errScimNotConfigured
	}

	req, err := c.newScimRequest(ctx, http.MethodGet, fmt.Sprintf(ScimUserPath, scimResourceID(userID)), nil)
	if err != nil {
		return false, err
	}

	if _, err := c.doRequest(ctx, req, nil); err != nil {
		if IsNotFoundError(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// ScimDeleteUser permanently deletes a user via SCIM DELETE /Users/{id} on the
// admin-management integration. This is a hard delete; callers should transfer
// owned content first when it must be retained (see TransferContent).
func (c *LucidchartClient) ScimDeleteUser(ctx context.Context, userID string) (annotations.Annotations, error) {
	if !c.ScimConfigured() {
		return nil, errScimNotConfigured
	}

	return c.scimDeleteUserWithToken(ctx, c.scimToken, userID)
}

// ScimDeleteUserContentAccess deletes a user from Lucid's second SCIM
// integration, "SCIM for content access" (teams). Same base URL and same
// /Users/{id} path as ScimDeleteUser — only the bearer token differs, which is
// what routes the call to the other integration.
func (c *LucidchartClient) ScimDeleteUserContentAccess(ctx context.Context, userID string) (annotations.Annotations, error) {
	if !c.ContentScimConfigured() {
		return nil, errContentScimNotConfigured
	}

	return c.scimDeleteUserWithToken(ctx, c.contentScimToken, userID)
}

// scimDeleteUserWithToken issues DELETE /Users/{id} with the given bearer
// token, shared by both SCIM integrations.
func (c *LucidchartClient) scimDeleteUserWithToken(ctx context.Context, token, userID string) (annotations.Annotations, error) {
	req, err := c.newScimRequestWithToken(ctx, token, http.MethodDelete, fmt.Sprintf(ScimUserPath, scimResourceID(userID)), nil)
	if err != nil {
		return nil, err
	}

	if _, err := c.doRequest(ctx, req, nil); err != nil {
		return nil, err
	}

	return nil, nil
}
