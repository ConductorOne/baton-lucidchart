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
	case err == nil, client.IsNotFoundError(err):
		// nil = just deleted; not-found = already deleted. Both are success.
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
// GET /v1/users/{id} documents only 200 and 403, where 403 covers both "not
// permitted" and "does not exist"; an undocumented 404 also occurs. Neither
// response proves the user is gone, so both ask SCIM (which 404s specifically
// for absence) and refuse the delete when SCIM reports the user still present.
//
// A probe that fails without answering that question is gated by
// probeFailureBlocksDelete: cancelled and transient failures abort the delete
// via classifyProbeFailure. The paths diverge only on a non-retryable,
// indeterminate probe failure — 403 returns codes.Unknown, 404 proceeds to the
// SCIM delete, which treats its own 404 as success so retries converge.
func (o *userBuilder) transferContentBeforeDelete(ctx context.Context, userID string) error {
	fromUser, err := o.client.GetUser(ctx, userID)
	switch {
	case err == nil:
		if _, err := o.client.TransferContent(ctx, fromUser.Email, o.contentTransferUserEmail); err != nil {
			return fmt.Errorf("baton-lucidchart: transfer content from user %s: %w", userID, err)
		}
		return nil

	case client.IsNotFoundError(err):
		// A 404 is undocumented for this endpoint and so does not prove absence;
		// confirm with SCIM before any hard delete.
		exists, existsErr := o.client.ScimUserExists(ctx, userID)
		if existsErr != nil && probeFailureBlocksDelete(ctx, existsErr) {
			return classifyProbeFailure(ctx, userID, err, existsErr)
		}
		if existsErr == nil && exists {
			// Present per SCIM but unreadable over REST: deleting would destroy
			// content we could not transfer.
			return status.Errorf(codes.FailedPrecondition,
				"baton-lucidchart: user %s could not be read for content transfer (undocumented REST 404: %v) "+
					"but SCIM reports they still exist; refusing to delete and lose their documents — "+
					"check that the OAuth token carries account.user:readonly and that the user is on this account",
				userID, err)
		}
		// SCIM confirms the user is gone, or the probe failed in a non-retryable,
		// indeterminate way; proceed to the SCIM delete, which treats its own 404
		// as success.
		return nil

	case client.IsPermissionDeniedError(err):
		// REST 403 is ambiguous ("gone" or "not permitted"), so confirm with SCIM.
		exists, existsErr := o.client.ScimUserExists(ctx, userID)
		if existsErr != nil {
			// Neither source could tell us whether the user still exists, so we
			// cannot yet decide if deleting is safe. Classify the probe failure so
			// callers can react rather than seeing every failure as codes.Unknown.
			return classifyProbeFailure(ctx, userID, err, existsErr)
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

// probeFailureBlocksDelete reports whether a failed SCIM existence probe is one
// that must abort the delete outright. A cancelled or transient probe carries no
// information about whether the user still exists, so deleting on the strength
// of it risks destroying content the operator asked to retain. Callers that see
// true should hand existsErr to classifyProbeFailure for the error to return.
func probeFailureBlocksDelete(ctx context.Context, existsErr error) bool {
	return isProbeCancellation(ctx, existsErr) || client.IsRetryableError(existsErr)
}

// isProbeCancellation reports whether a failed SCIM existence probe failed
// because the surrounding sync was cancelled or timed out rather than because
// of anything the probe learned about the user. Shared by the gate
// (probeFailureBlocksDelete) and the classifier (classifyProbeFailure) so the
// two cannot drift out of agreement about what counts as cancellation.
func isProbeCancellation(ctx context.Context, existsErr error) bool {
	return ctx.Err() != nil ||
		errors.Is(existsErr, context.Canceled) ||
		errors.Is(existsErr, context.DeadlineExceeded)
}

// classifyProbeFailure turns a failed SCIM existence probe into the gRPC error
// to return when the REST lookup (an overloaded 403, or an undocumented 404)
// left the user's existence undecided. The *kind* of probe failure drives what
// we report: collapsing every failure into codes.Unknown would hide cancellation
// from errors.Is downstream and discourage the platform from retrying a
// transient outage that would likely succeed. Classification is in priority
// order — cancellation, then retryable, then the deliberate indeterminate
// fallback, which only the 403 caller reaches (the 404 caller gates on
// probeFailureBlocksDelete and proceeds instead). restErr is the original REST
// error.
func classifyProbeFailure(ctx context.Context, userID string, restErr, existsErr error) error {
	switch {
	case isProbeCancellation(ctx, existsErr):
		// The sync was cancelled or timed out while the probe was in flight.
		// Preserve the context error via %w so errors.Is keeps matching
		// context.Canceled / context.DeadlineExceeded downstream, instead of
		// masking cancellation as a generic Unknown.
		ctxErr := ctx.Err()
		if ctxErr == nil {
			ctxErr = existsErr
		}
		return fmt.Errorf(
			"baton-lucidchart: content-transfer existence probe for user %s was cancelled before it could confirm the user (REST said: %s): %w",
			userID, restErr.Error(), ctxErr)
	case client.IsRetryableError(existsErr):
		// A transient probe failure. GrpcCodeFromHTTPStatus maps HTTP 429 and 5xx
		// to codes.Unavailable — except 501, which it special-cases to
		// codes.Unimplemented ahead of its 500..599 fallback, so a 501 is not
		// retryable and lands in the Unknown arm below — and HTTP 408 to
		// codes.DeadlineExceeded.
		// Preserve the retryable code so the platform re-attempts the deprovision
		// (which would likely succeed once SCIM is reachable again) rather than
		// parking it behind a non-retryable Unknown.
		return status.Errorf(status.Code(existsErr),
			"baton-lucidchart: could not resolve user %s for content transfer (%v); the SCIM existence probe failed transiently and should be retried: %v",
			userID, restErr, existsErr)
	default:
		// Genuinely indeterminate/non-retryable: return codes.Unknown deliberately
		// as a chosen "we cannot decide" signal, not whatever errors.As happens to
		// surface first from two %w-wrapped chains. Both underlying errors are kept
		// verbatim in the detail text so no diagnostic information is lost.
		return status.Errorf(codes.Unknown,
			"baton-lucidchart: could not resolve user %s for content transfer (%v) and could not confirm whether they still exist: %v",
			userID, restErr, existsErr)
	}
}

func newUserBuilder(client *client.LucidchartClient, contentTransferUserEmail string) *userBuilder {
	return &userBuilder{
		client:                   client,
		contentTransferUserEmail: contentTransferUserEmail,
	}
}
