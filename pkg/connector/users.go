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
// Lucid's GET /v1/users/{id} documents only 200 and 403; 403 is explicitly the
// "does not belong to the authenticated account or does not exist" response, and
// 404 is not a documented response at all. So neither a 403 nor an
// (undocumented) 404 proves the user is actually gone — both are ambiguous as to
// absence, and treating either as "gone" outright risks hard-deleting a user
// whose content we were told to retain. In both cases we ask SCIM (which 404s
// specifically for absence) before deciding, and refuse the delete only when
// SCIM affirmatively reports the user still present.
//
// The two paths differ only in how they treat a SCIM probe error. On the
// undocumented 404 a probe outage is treated as "proceed" so it can't block a
// delete (preserving the original rule that a probe outage must not block an
// otherwise-valid delete). On the documented-but-overloaded 403 an unresolved
// probe blocks the delete, but the returned gRPC code is classified so callers
// can react: a cancelled/timed-out probe preserves the context error (so
// errors.Is still matches), a transient failure (429/5xx surfaces as Unavailable,
// a 408 as DeadlineExceeded) keeps that retryable code, and only a genuinely
// indeterminate probe falls through to a deliberate codes.Unknown.
func (o *userBuilder) transferContentBeforeDelete(ctx context.Context, userID string) error {
	fromUser, err := o.client.GetUser(ctx, userID)
	switch {
	case err == nil:
		if _, err := o.client.TransferContent(ctx, fromUser.Email, o.contentTransferUserEmail); err != nil {
			return fmt.Errorf("baton-lucidchart: transfer content from user %s: %w", userID, err)
		}
		return nil

	case client.IsNotFoundError(err):
		// REST returned a 404, which Lucid does not document for GET
		// /v1/users/{id} — that endpoint only documents 200 and 403, and 403 is
		// its "does not exist" response. A 404 is therefore of unknown meaning,
		// so we can't assume the user is gone: if it can ever occur for a user
		// who still exists, proceeding straight to the hard SCIM delete would
		// destroy content the operator asked to retain. Probe SCIM (which 404s
		// specifically for absence) and refuse only when it affirmatively
		// reports the user still present. Any probe error is treated as
		// "proceed" so a probe outage still cannot block an otherwise-valid
		// delete.
		exists, existsErr := o.client.ScimUserExists(ctx, userID)
		if existsErr == nil && exists {
			// Present per SCIM but unreadable over REST: deleting would destroy
			// content we could not transfer.
			return status.Errorf(codes.FailedPrecondition,
				"baton-lucidchart: user %s could not be read for content transfer (undocumented REST 404: %v) "+
					"but SCIM reports they still exist; refusing to delete and lose their documents — "+
					"check that the OAuth token carries account.user:readonly and that the user is on this account",
				userID, err)
		}
		// Either SCIM confirms the user is gone, or the probe itself failed; in
		// both cases proceed to the SCIM delete (which treats its own 404 as
		// success), so a probe outage cannot abort a valid delete.
		return nil

	case client.IsPermissionDeniedError(err):
		// REST 403 is ambiguous ("gone" or "not permitted"), so confirm with SCIM.
		exists, existsErr := o.client.ScimUserExists(ctx, userID)
		if existsErr != nil {
			// Neither source could tell us whether the user still exists, so we
			// cannot yet decide if deleting is safe. But the *kind* of probe
			// failure drives what we report: collapsing every failure into
			// codes.Unknown would hide cancellation from errors.Is downstream and
			// discourage the platform from retrying a transient outage that would
			// likely succeed. Classify in priority order — cancellation, then
			// retryable, then the deliberate indeterminate fallback.
			switch {
			case ctx.Err() != nil ||
				errors.Is(existsErr, context.Canceled) ||
				errors.Is(existsErr, context.DeadlineExceeded):
				// The sync was cancelled or timed out while the probe was in
				// flight. Preserve the context error via %w so errors.Is keeps
				// matching context.Canceled / context.DeadlineExceeded downstream,
				// instead of masking cancellation as a generic Unknown.
				ctxErr := ctx.Err()
				if ctxErr == nil {
					ctxErr = existsErr
				}
				return fmt.Errorf(
					"baton-lucidchart: content-transfer existence probe for user %s was cancelled before it could confirm the user (REST said: %s): %w",
					userID, err.Error(), ctxErr)
			case status.Code(existsErr) == codes.Unavailable ||
				status.Code(existsErr) == codes.DeadlineExceeded:
				// A transient probe failure. The SDK's GrpcCodeFromHTTPStatus maps
				// HTTP 429 and 5xx to codes.Unavailable and HTTP 408 to
				// codes.DeadlineExceeded, and its retry layer treats exactly those
				// two codes as retryable. Preserve the retryable code so the platform
				// re-attempts the deprovision (which would likely succeed once SCIM
				// is reachable again) rather than parking it behind a non-retryable
				// Unknown.
				return status.Errorf(status.Code(existsErr),
					"baton-lucidchart: could not resolve user %s for content transfer (%v); the SCIM existence probe failed transiently and should be retried: %v",
					userID, err, existsErr)
			default:
				// Genuinely indeterminate/non-retryable: return codes.Unknown
				// deliberately as a chosen "we cannot decide" signal, not whatever
				// errors.As happens to surface first from two %w-wrapped chains.
				// Both underlying errors are kept verbatim in the detail text so no
				// diagnostic information is lost.
				return status.Errorf(codes.Unknown,
					"baton-lucidchart: could not resolve user %s for content transfer (%v) and could not confirm whether they still exist: %v",
					userID, err, existsErr)
			}
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
