package connector

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"

	"go.uber.org/zap"
)

const (
	rootId = "root"

	documentHasUserAccessEntitlement = "user/"
)

type documentBuilder struct {
	client *client.LucidchartClient
}

func (o *documentBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return documentResourceType
}

func (o *documentBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	if parentResourceID == nil && pToken.Token == "" {
		l.Info("baton-lucidchart: ignoring first List call for root folder, only uses parentResourceID")
		return nil, "", nil, nil
	}

	if parentResourceID != nil {
		var folderContent []client.FolderContent
		var nextToken string
		var err error

		if parentResourceID.Resource == rootId {
			folderContent, nextToken, err = o.client.RootFolderContent(ctx, pToken.Token)
			if err != nil {
				return nil, "", nil, err
			}
		} else {
			folderContent, nextToken, err = o.client.FolderContent(ctx, parentResourceID.Resource, pToken.Token)
			if err != nil {
				return nil, "", nil, err
			}
		}

		innerDocuments, err := documentResources(folderContent, parentResourceID)
		if err != nil {
			return nil, "", nil, err
		}

		return innerDocuments, nextToken, nil, nil
	}

	l.Error("invalid parentResourceID", zap.Any("parentResourceID", parentResourceID))

	return nil, "", nil, nil
}
func (o *documentBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	for _, role := range client.UserFolderRoles {
		assigmentOptions := []entitlement.EntitlementOption{
			entitlement.WithGrantableTo(userResourceType),
			entitlement.WithDescription(fmt.Sprintf("%s can %s on %s", userResourceType.DisplayName, role, resource.DisplayName)),
			entitlement.WithDisplayName(fmt.Sprintf("%s is %s of %s", userResourceType.DisplayName, role, resource.DisplayName)),
		}
		rv = append(rv, entitlement.NewPermissionEntitlement(resource, documentHasUserAccessEntitlement+role, assigmentOptions...))
	}

	return rv, "", nil, nil
}

func (o *documentBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	if resource.Id.Resource == "root" {
		return nil, "", nil, nil
	}

	collaborators, nextToken, err := o.client.ListDocumentUserCollaborators(ctx, resource.Id.Resource, pToken.Token)
	if err != nil {
		return nil, "", nil, err
	}

	var grants []*v2.Grant

	for _, collaborator := range collaborators {
		userID, err := rs.NewResourceID(userResourceType, collaborator.UserId)
		if err != nil {
			return nil, "", nil, err
		}

		metadata := map[string]interface{}{
			"role":    collaborator.Role,
			"created": collaborator.Created.String(),
		}

		newGrant := grant.NewGrant(resource, documentHasUserAccessEntitlement+collaborator.Role, userID, grant.WithGrantMetadata(metadata))

		grants = append(grants, newGrant)
	}

	return grants, nextToken, nil, nil
}

func (o *documentBuilder) Grant(ctx context.Context, resource *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	if resource.Id.ResourceType == userResourceType.Id {
		userId := resource.Id.Resource
		documentId := entitlement.Resource.Id.Resource

		splitted := strings.Split(entitlement.Slug, "/")
		if len(splitted) != 2 {
			return nil, nil, fmt.Errorf("invalid entitlement slug %s", entitlement.Slug)
		}

		role := splitted[1]

		response, err := o.client.UpsertDocumentUserCollaborator(ctx, documentId, userId, role)
		if err != nil {
			return nil, nil, err
		}

		userID, err := rs.NewResourceID(userResourceType, response.UserId)
		if err != nil {
			return nil, nil, err
		}

		metadata := map[string]interface{}{
			"role":    response.Role,
			"created": response.Created.String(),
		}

		newGrant := grant.NewGrant(resource, documentHasUserAccessEntitlement+response.Role, userID, grant.WithGrantMetadata(metadata))

		return []*v2.Grant{newGrant}, nil, nil
	}

	return nil, nil, fmt.Errorf("invalid resource type %s", resource.Id.ResourceType)
}

func (o *documentBuilder) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	if grant.Principal.Id.ResourceType == userResourceType.Id {
		userId := grant.Principal.Id.Resource
		documentId := grant.Entitlement.Resource.Id.Resource

		err := o.client.DeleteDocumentUserCollaborator(ctx, documentId, userId)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return annotations.New(&v2.GrantAlreadyRevoked{}), nil
			}

			return nil, err
		}

		return nil, nil
	}

	return nil, fmt.Errorf("resource type %s is not supported", grant.Principal.Id.ResourceType)
}

func documentResources(folderContent []client.FolderContent, parentResourceID *v2.ResourceId) ([]*v2.Resource, error) {
	var resources []*v2.Resource

	for _, folder := range folderContent {
		if folder.Type != "document" {
			continue
		}

		newResource, err := documentResource(folder.ID(), folder.Name, parentResourceID)
		if err != nil {
			return nil, err
		}

		resources = append(resources, newResource)
	}

	return resources, nil
}

func documentResource(id, name string, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	resourceOptions := []rs.ResourceOption{
		rs.WithParentResourceID(parentResourceID),
	}

	return rs.NewResource(
		name,
		documentResourceType,
		id,
		resourceOptions...,
	)
}

func newDocumentBuilder(client *client.LucidchartClient) *documentBuilder {
	return &documentBuilder{
		client: client,
	}
}
