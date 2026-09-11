package client

import (
	"context"
	"encoding/json"
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

// ScimUser is the SCIM 2.0 User resource Lucid returns from a successful
// PATCH /Users/{id} (and GET /Users/{id}). It is Lucid's confirmed
// post-write state, which is not necessarily what the caller asked for.
//
// Only the core attributes the connector acts on are modelled; SCIM responses
// carry more (meta, groups, enterprise extensions) and unknown fields are
// ignored. Every field is optional — a server that answers 204 No Content
// leaves the whole struct zero-valued, so callers must nil-check before use.
type ScimUser struct {
	ID         string          `json:"id"`
	UserName   string          `json:"userName"`
	Active     *bool           `json:"active"`
	ExternalID string          `json:"externalId"`
	Name       *ScimUserName   `json:"name"`
	Emails     []ScimUserEmail `json:"emails"`
	Roles      []ScimUserRole  `json:"roles"`
}

// ScimUserName is the SCIM complex "name" attribute.
type ScimUserName struct {
	GivenName  string `json:"givenName"`
	FamilyName string `json:"familyName"`
}

// ScimUserEmail is one entry of the SCIM multi-valued "emails" attribute.
type ScimUserEmail struct {
	Value   string `json:"value"`
	Type    string `json:"type"`
	Primary bool   `json:"primary"`
}

// ScimUserRole is one entry of the SCIM multi-valued "roles" attribute.
type ScimUserRole struct {
	Value string `json:"value"`
}

// GetActive returns the confirmed active flag, or nil when the response did not
// carry one. nil-safe so callers need not guard the receiver.
func (u *ScimUser) GetActive() *bool {
	if u == nil {
		return nil
	}
	return u.Active
}

// PrimaryEmail returns the address SCIM marked primary, falling back to the
// first entry. Lucid's user model holds a single address, so the fallback is
// what a response without an explicit primary flag means.
func (u *ScimUser) PrimaryEmail() string {
	if u == nil {
		return ""
	}
	for _, e := range u.Emails {
		if e.Primary {
			return e.Value
		}
	}
	if len(u.Emails) > 0 {
		return u.Emails[0].Value
	}
	return ""
}

// RoleValues flattens the multi-valued roles attribute to the bare role names.
func (u *ScimUser) RoleValues() []string {
	if u == nil || len(u.Roles) == 0 {
		return nil
	}
	out := make([]string, 0, len(u.Roles))
	for _, r := range u.Roles {
		out = append(out, r.Value)
	}
	return out
}

// IsZero reports whether the response carried no usable SCIM resource — the
// case when Lucid answers a PATCH with 204 No Content instead of the updated
// user. Callers use it to tell "no confirmation available" apart from a
// confirmation that happens to be empty.
func (u *ScimUser) IsZero() bool {
	return u == nil || (u.ID == "" && u.UserName == "" && u.Active == nil &&
		u.ExternalID == "" && u.Name == nil && len(u.Emails) == 0 && len(u.Roles) == 0)
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

// scimUserResponse decodes the SCIM User resource Lucid returns from a
// successful PATCH /Users/{id} into out.
//
// uhttp.WithResponse cannot be used here: it rejects any response whose
// Content-Type is neither JSON nor XML, which includes the bodyless 204 a SCIM
// server is entitled to answer a PATCH with. A write that Lucid accepted must
// not be reported as a failure just because it came back without a body, so an
// absent or non-JSON body leaves out zero-valued (ScimUser.IsZero) rather than
// erroring. A body that claims to be JSON and is not still errors — that is a
// broken response, not an empty one.
func scimUserResponse(out *ScimUser) uhttp.DoOption {
	return func(resp *uhttp.WrapperResponse) error {
		if out == nil || resp.StatusCode == http.StatusNoContent || len(resp.Body) == 0 {
			return nil
		}
		if !uhttp.IsJSONContentType(resp.Header.Get(uhttp.ContentType)) {
			return nil
		}
		if err := json.Unmarshal(resp.Body, out); err != nil {
			return fmt.Errorf("failed to decode SCIM user response: %w", err)
		}
		return nil
	}
}

// SetUserActive toggles a user's active state via SCIM PATCH. active=false is a
// soft, reversible deactivation; active=true reactivates a deactivated user.
//
// The returned *ScimUser is Lucid's confirmed post-write state. It is never nil
// on success, but may be IsZero when Lucid answered without a body.
func (c *LucidchartClient) SetUserActive(ctx context.Context, userID string, active bool) (*ScimUser, annotations.Annotations, error) {
	if !c.ScimConfigured() {
		return nil, nil, errScimNotConfigured
	}

	body := &ScimPatchOp{
		Schemas: []string{scimPatchSchema},
		Operations: []ScimPatchOperation{
			{Op: scimOpReplace, Path: "active", Value: active},
		},
	}

	req, err := c.newScimRequest(ctx, http.MethodPatch, fmt.Sprintf(ScimUserPath, scimResourceID(userID)), body)
	if err != nil {
		return nil, nil, err
	}

	updated := &ScimUser{}
	if _, err := c.doRequestWithOptions(ctx, req, scimUserResponse(updated)); err != nil {
		return nil, nil, err
	}

	return updated, nil, nil
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
