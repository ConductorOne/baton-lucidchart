package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/crypto"
	"github.com/conductorone/baton-sdk/pkg/pagination"
)

type userBuilder struct {
	client *client.LucidchartClient
}

func (o *userBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return userResourceType
}

// List returns all the users from the database as resource objects.
// Users include a UserTrait because they are the 'shape' of a standard user.
func (o *userBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	user, nextToken, err := o.client.ListUser(ctx, pToken.Token)
	if err != nil {
		l.Error("Error getting users", zap.Error(err))
		return nil, "", nil, err
	}

	var resources []*v2.Resource
	for _, u := range user {
		user, err := userResource(u)
		if err != nil {
			return nil, "", nil, err
		}

		resources = append(resources, user)
	}

	return resources, nextToken, nil, nil
}

// Entitlements always returns an empty slice for users.
func (o *userBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

// Grants always returns an empty slice for users since they don't have any entitlements.
func (o *userBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
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

	// Validate that all roles are valid
	validRoles := map[string]bool{
		"billing-admin":  true,
		"team-admin":     true,
		"document-admin": true,
		"template-admin": true,
	}

	for _, role := range roles {
		if !validRoles[role] {
			return nil, nil, nil, fmt.Errorf("invalid role '%s'. Valid roles are: billing-admin, team-admin, document-admin, template-admin", role)
		}
	}

	password, err := generateCredentials(credentialOptions)
	if err != nil {
		return nil, nil, nil, err
	}

	if len(roles) == 0 {
		roles = []string{"editor"}
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
	created, annotations, err := o.client.CreateUser(ctx, payload)
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

	return resp, plaintext, annotations, nil
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
	status := v2.UserTrait_Status_STATUS_ENABLED

	profile := map[string]interface{}{
		"account_id": user.AccountId,
		"email":      user.Email,
		"name":       user.Name,
		"user_id":    user.UserId,
		"usernames":  user.Usernames,
	}

	userTraitOptions := []resource.UserTraitOption{
		resource.WithUserProfile(profile),
		resource.WithEmail(user.Email, true),
		resource.WithStatus(status),
		resource.WithUserLogin(user.Email),
	}

	newUserResource, err := resource.NewUserResource(
		user.Email,
		userResourceType,
		user.UserId,
		userTraitOptions,
	)
	if err != nil {
		return nil, err
	}

	return newUserResource, nil
}

func newUserBuilder(client *client.LucidchartClient) *userBuilder {
	return &userBuilder{
		client: client,
	}
}
