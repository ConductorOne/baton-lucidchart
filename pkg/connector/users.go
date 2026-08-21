package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/crypto"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// knownRestRoles is the set of role strings accepted by Lucid's REST
// POST /v1/users (Create User) endpoint (kebab-case).
// Source: https://lucid.readme.io/reference/createuser — 11 enum values total;
// 10 are confirmed (the 11th is behind an interactive expand the page fetcher
// cannot reach). List sourced from reviewer citation on PR #54.
var knownRestRoles = []string{
	"billing-admin", "team-admin", "document-admin", "template-admin",
	"account-owner", "developer", "enterprise-shield-admin", "group-admin",
	"organizational-group-admin", "team-manager",
}

func isKnownRestRole(role string) bool {
	for _, r := range knownRestRoles {
		if r == role {
			return true
		}
	}
	return false
}

type userBuilder struct {
	client *client.LucidchartClient
	// contentTransferUserEmail, when set, receives a deleted user's documents
	// before the user is removed, so owned content is retained. Must be an
	// email address: the Lucid transferUserContent API requires email, not ID.
	contentTransferUserEmail string
}

func (o *userBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return userResourceType
}

// List returns all the users from the database as resource objects.
// Users include a UserTrait because they are the 'shape' of a standard user.
func (o *userBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, opts rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	l := ctxzap.Extract(ctx)
	pToken := &opts.PageToken

	users, nextToken, err := o.client.ListUser(ctx, pToken.Token)
	if err != nil {
		l.Error("Error getting users", zap.Error(err))
		return nil, nil, err
	}

	var resources []*v2.Resource
	for _, u := range users {
		user, err := userResource(u)
		if err != nil {
			return nil, nil, err
		}

		resources = append(resources, user)
	}

	return resources, &rs.SyncOpResults{NextPageToken: nextToken}, nil
}

// Entitlements always returns an empty slice for users.
func (o *userBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// Grants always returns an empty slice for users since they don't have any entitlements.
func (o *userBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func (b *userBuilder) CreateAccountCapabilityDetails(_ context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_RANDOM_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_RANDOM_PASSWORD,
	}, nil, nil
}

func (o *userBuilder) CreateAccount(
	ctx context.Context,
	accountInfo *v2.AccountInfo,
	credentialOptions *v2.LocalCredentialOptions,
) (
	connectorbuilder.CreateAccountResponse,
	[]*v2.PlaintextData,
	annotations.Annotations,
	error,
) {
	// Extract fields from the profile
	profile := accountInfo.GetProfile().AsMap()

	firstName, ok := profile["firstName"].(string)
	if !ok {
		return nil, nil, nil, errors.New("missing or invalid firstName")
	}
	lastName, ok := profile["lastName"].(string)
	if !ok {
		return nil, nil, nil, errors.New("missing or invalid lastName")
	}
	email, ok := profile["email"].(string)
	if !ok {
		return nil, nil, nil, errors.New("missing or invalid email")
	}
	username, _ := profile["username"].(string)

	var roles []string
	if r, ok := profile["roles"].([]interface{}); ok {
		for _, v := range r {
			if s, ok := v.(string); ok {
				roles = append(roles, s)
			}
		}
	}

	// Validate that all roles are valid for the REST Create User endpoint.
	for _, role := range roles {
		if !isKnownRestRole(role) {
			return nil, nil, nil, fmt.Errorf("invalid role '%s'. Valid roles are: %v", role, knownRestRoles)
		}
	}

	password, err := generateCredentials(credentialOptions)
	if err != nil {
		return nil, nil, nil, err
	}

	payload := &client.UserCreatePayload{
		FirstName: firstName,
		LastName:  lastName,
		Email:     email,
		Username:  username,
		Password:  password,
		Roles:     roles,
	}

	// Create user via REST client
	created, annos, err := o.client.CreateUser(ctx, payload)
	if err != nil {
		return nil, nil, nil, err
	}

	// Convert to resource
	res, err := userResource(*created)
	if err != nil {
		return nil, nil, nil, err
	}

	resp := &v2.CreateAccountResponse_SuccessResult{Resource: res, IsCreateAccountResult: true}

	plaintext := []*v2.PlaintextData{{
		Name:        "password",
		Description: "Generated password for the new user",
		Bytes:       []byte(password),
	}}

	return resp, plaintext, annos, nil
}

func generateCredentials(credentialOptions *v2.LocalCredentialOptions) (string, error) {
	if credentialOptions.GetRandomPassword() == nil {
		return "", errors.New("unsupported credential option")
	}

	password, err := crypto.GenerateRandomPassword(
		&v2.LocalCredentialOptions_RandomPassword{
			Length: min(12, credentialOptions.GetRandomPassword().GetLength()),
		},
	)
	if err != nil {
		return "", err
	}
	return password, nil
}

func userResource(user client.User) (*v2.Resource, error) {
	status := v2.Status_RESOURCE_STATUS_ENABLED
	if user.Enabled != nil && !*user.Enabled {
		status = v2.Status_RESOURCE_STATUS_DISABLED
	}

	// structpb has no []string case, so roles have to be widened.
	roles := make([]interface{}, 0, len(user.Roles))
	for _, r := range user.Roles {
		roles = append(roles, r)
	}

	profile := map[string]interface{}{
		"account_id": user.AccountId,
		"email":      user.Email,
		"name":       user.Name,
		argUserID:    user.UserId,
		"usernames":  user.Usernames,
		"username":   user.Username,
		"roles":      roles,
	}

	userTraitOptions := []rs.UserTraitOption{
		rs.WithEmail(user.Email, true),
		rs.WithUserLogin(user.Email),
		// Keep the (deprecated) trait status in sync with the resource status.
		// NewUserTrait defaults an unset trait status to ENABLED, so without this
		// a disabled user would report Resource.Status=DISABLED alongside
		// UserTrait.Status=ENABLED — the exact "disabled user reports enabled"
		// divergence this fix targets, just moved to trait-status readers.
		//nolint:staticcheck // SA1019: WithStatus is deprecated, but consumers still read the trait status; syncing it prevents the divergence.
		rs.WithStatus(status),
	}

	newUserResource, err := rs.NewUserResource(
		user.Email,
		userResourceType,
		user.UserId,
		userTraitOptions,
		rs.WithResourceProfile(profile),
		rs.WithResourceStatus(status, ""),
	)
	if err != nil {
		return nil, err
	}

	return newUserResource, nil
}

// Delete permanently removes a user via the SCIM DELETE path (the official
// deprovisioning surface). When a content-transfer user is configured, the
// deleted user's documents are transferred first so they are retained.
//
// Not-found is treated as success: the platform retries failed deletes, and a
// connector that errored on an already-deleted user would fail every retry.
func (o *userBuilder) Delete(ctx context.Context, resourceID *v2.ResourceId, parentResourceID *v2.ResourceId) (annotations.Annotations, error) {
	if !o.client.ScimConfigured() {
		return nil, status.Error(codes.Unimplemented, "baton-lucidchart: delete user: SCIM not configured (a SCIM bearer token, Enterprise tier, is required for deprovisioning)")
	}

	userID := resourceID.Resource

	if o.contentTransferUserEmail != "" {
		if err := o.transferContentBeforeDelete(ctx, userID); err != nil {
			return nil, err
		}
		// A nil error with no email resolved means the user is already gone;
		// fall through to the SCIM delete, which treats its own 404 as success.
	}

	annos, err := o.client.ScimDeleteUser(ctx, userID)
	switch {
	case err == nil:
		return annos, nil
	case client.IsNotFoundError(err):
		// Already deleted is success.
		return annos, nil
	case client.IsConflictError(err):
		// Raw 409 surfaces as AlreadyExists, which reads as an idempotent
		// success and would let a failed offboarding look complete.
		return annos, status.Errorf(codes.FailedPrecondition,
			"baton-lucidchart: delete user %s: Lucid refused the delete (409) — the user is an account owner "+
				"or a default document owner and cannot be deleted via SCIM; reassign that role in Lucid first (%v)",
			userID, err)
	default:
		return annos, fmt.Errorf("baton-lucidchart: delete user %s: %w", userID, err)
	}
}

// transferContentBeforeDelete moves the leaving user's documents to the
// configured recipient, resolving their email from the REST record first.
//
// A definite REST 404 means the user is already gone: skip the transfer and let
// the SCIM delete (which treats its own 404 as success) run — no SCIM probe, so
// a probe outage can't block an otherwise-valid delete. REST answers 403 both
// for "gone" and for "not permitted", so only there is absence ambiguous; we ask
// SCIM (which 404s specifically for absence) before deciding, because guessing
// would mean either deleting content we failed to move or breaking retry
// idempotency.
func (o *userBuilder) transferContentBeforeDelete(ctx context.Context, userID string) error {
	fromUser, err := o.client.GetUser(ctx, userID)
	switch {
	case err == nil:
		if _, err := o.client.TransferContent(ctx, fromUser.Email, o.contentTransferUserEmail); err != nil {
			return fmt.Errorf("baton-lucidchart: transfer content from user %s: %w", userID, err)
		}
		return nil

	case client.IsNotFoundError(err):
		// REST gave a definite 404: the user is gone as far as content transfer
		// is concerned. Proceed to the SCIM delete without probing SCIM first, so
		// a probe failure (403/405/501 on a tenant without SCIM user GET, or a
		// transient 5xx) cannot abort a delete REST already told us is safe.
		return nil

	case client.IsPermissionDeniedError(err):
		// REST 403 is ambiguous ("gone" or "not permitted"), so confirm with SCIM.
		exists, existsErr := o.client.ScimUserExists(ctx, userID)
		if existsErr != nil {
			// Neither source could tell us whether the user still exists, so we
			// genuinely cannot decide if deleting is safe. Return codes.Unknown
			// deliberately: the gRPC code here is a chosen "indeterminate"
			// signal, not whatever errors.As happens to surface first from two
			// %w-wrapped chains. Both underlying errors are kept verbatim in the
			// detail text so no diagnostic information is lost.
			return status.Errorf(codes.Unknown,
				"baton-lucidchart: could not resolve user %s for content transfer (%v) and could not confirm whether they still exist: %v",
				userID, err, existsErr)
		}
		if exists {
			// Present but unreadable over REST: deleting would destroy content
			// the operator asked to retain.
			return status.Errorf(codes.FailedPrecondition,
				"baton-lucidchart: user %s exists but their email could not be read for content transfer (%v); "+
					"refusing to delete and lose their documents — check that the OAuth token carries "+
					"account.user:readonly and that the user is on this account",
				userID, err)
		}
		// Genuinely gone; proceed so the delete stays idempotent under retry.
		return nil

	default:
		return fmt.Errorf("baton-lucidchart: resolve email for content transfer (user %s): %w", userID, err)
	}
}

func newUserBuilder(client *client.LucidchartClient, contentTransferUserEmail string) *userBuilder {
	return &userBuilder{
		client:                   client,
		contentTransferUserEmail: contentTransferUserEmail,
	}
}
