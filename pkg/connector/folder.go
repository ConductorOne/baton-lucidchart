package connector

import (
	"context"
	"fmt"

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
	folderHasUserAccessEntitlement = "has-user"
)

type folderBuilder struct {
	client *client.LucidchartClient
}

func (o *folderBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return folderResourceType
}

func (o *folderBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	// Root folder
	if parentResourceID == nil && pToken.Token == "" {
		root, err := folderResource("root", "root", nil)
		if err != nil {
			return nil, "", nil, err
		}

		resources := []*v2.Resource{root}

		return resources, "", nil, err
	}

	// Child folders
	if parentResourceID != nil {
		folderContent, nextToken, err := o.client.FolderContent(ctx, parentResourceID.Resource, pToken.Token)
		if err != nil {
			return nil, "", nil, err
		}

		innerFolders, err := folderResources(folderContent, parentResourceID)
		if err != nil {
			return nil, "", nil, err
		}

		return innerFolders, nextToken, nil, nil
	}

	l.Error("invalid parentResourceID", zap.Any("parentResourceID", parentResourceID))

	return nil, "", nil, nil
}
func (o *folderBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	assigmentOptions := []entitlement.EntitlementOption{
		entitlement.WithGrantableTo(userResourceType),
		entitlement.WithDescription(fmt.Sprintf("%s can acess %s", userResourceType.DisplayName, resource.DisplayName)),
		entitlement.WithDisplayName(fmt.Sprintf("%s acess %s", userResourceType.DisplayName, resource.DisplayName)),
	}
	rv = append(rv, entitlement.NewPermissionEntitlement(resource, folderHasUserAccessEntitlement, assigmentOptions...))

	return rv, "", nil, nil
}

func (o *folderBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	if resource.Id.Resource == "root" {
		return nil, "", nil, nil
	}

	collaborators, nextToken, err := o.client.ListFolderUserCollaborators(ctx, resource.Id.Resource, pToken.Token)
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

		newGrant := grant.NewGrant(resource, folderHasUserAccessEntitlement, userID, grant.WithGrantMetadata(metadata))

		grants = append(grants, newGrant)
	}

	return grants, nextToken, nil, nil
}

func folderResources(folderContent []client.FolderContent, parentResourceID *v2.ResourceId) ([]*v2.Resource, error) {
	var resources []*v2.Resource

	for _, folder := range folderContent {
		if folder.Type != "folder" {
			continue
		}

		newResource, err := folderResource(folder.ID(), folder.Name, parentResourceID)
		if err != nil {
			return nil, err
		}

		resources = append(resources, newResource)
	}

	return resources, nil
}

func folderResource(id, name string, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	resourceOptions := []rs.ResourceOption{
		rs.WithParentResourceID(parentResourceID),
		rs.WithAnnotation(
			&v2.ChildResourceType{
				ResourceTypeId: folderResourceType.Id,
			},
			&v2.ChildResourceType{
				ResourceTypeId: documentResourceType.Id,
			},
		),
	}

	return rs.NewResource(
		name,
		folderResourceType,
		id,
		resourceOptions...,
	)
}

func newFolderBuilder(client *client.LucidchartClient) *folderBuilder {
	return &folderBuilder{
		client: client,
	}
}
