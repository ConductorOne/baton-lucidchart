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
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"

	"go.uber.org/zap"
)

const (
	folderHasUserAccessEntitlement = "user/"
)

type folderBuilder struct {
	client           *client.LucidchartClient
	excludeShortcuts bool
}

func (o *folderBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return folderResourceType
}

func (o *folderBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, opts rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	l := ctxzap.Extract(ctx)
	pToken := &opts.PageToken

	// Root folder
	if parentResourceID == nil && pToken.Token == "" {
		root, err := folderResource("root", "root", nil)
		if err != nil {
			return nil, nil, err
		}

		resources := []*v2.Resource{root}

		return resources, nil, nil
	}

	// Child folders
	if parentResourceID != nil {
		folderContent, nextToken, err := o.client.FolderContent(ctx, parentResourceID.Resource, pToken.Token)
		if err != nil {
			return nil, nil, err
		}

		var innerFolders []*v2.Resource
		for _, item := range folderContent {
			if item.Type != "folder" {
				continue
			}

			if item.IsShortcut && o.excludeShortcuts {
				l.Info("baton-lucidchart: skipping shortcut folder", zap.String("folder_id", item.ID()))
				continue
			}

			newResource, err := folderResource(item.ID(), item.Name, parentResourceID)
			if err != nil {
				return nil, nil, err
			}
			innerFolders = append(innerFolders, newResource)
		}

		return innerFolders, &rs.SyncOpResults{NextPageToken: nextToken}, nil
	}

	l.Error("invalid parentResourceID", zap.Any("parentResourceID", parentResourceID))

	return nil, nil, nil
}

func (o *folderBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	var rv []*v2.Entitlement

	for _, role := range client.UserFolderRoles {
		assigmentOptions := []entitlement.EntitlementOption{
			entitlement.WithGrantableTo(userResourceType),
			entitlement.WithDescription(fmt.Sprintf("%s can %s on %s", userResourceType.DisplayName, role, resource.DisplayName)),
			entitlement.WithDisplayName(fmt.Sprintf("%s is %s of %s", userResourceType.DisplayName, role, resource.DisplayName)),
		}
		rv = append(rv, entitlement.NewPermissionEntitlement(resource, folderHasUserAccessEntitlement+role, assigmentOptions...))
	}

	return rv, nil, nil
}

func (o *folderBuilder) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	if resource.Id.Resource == "root" {
		return nil, nil, nil
	}

	pToken := &opts.PageToken

	collaborators, nextToken, err := o.client.ListFolderUserCollaborators(ctx, resource.Id.Resource, pToken.Token)
	if err != nil {
		return nil, nil, err
	}

	var grants []*v2.Grant

	for _, collaborator := range collaborators {
		userID, err := rs.NewResourceID(userResourceType, collaborator.UserId)
		if err != nil {
			return nil, nil, err
		}

		metadata := map[string]interface{}{
			metaRole:    collaborator.Role,
			metaCreated: collaborator.Created.String(),
		}

		newGrant := grant.NewGrant(resource, folderHasUserAccessEntitlement+collaborator.Role, userID, grant.WithGrantMetadata(metadata))

		grants = append(grants, newGrant)
	}

	return grants, &rs.SyncOpResults{NextPageToken: nextToken}, nil
}

func (o *folderBuilder) Grant(ctx context.Context, resource *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	if resource.Id.ResourceType == userResourceType.Id {
		userId := resource.Id.Resource
		folderId := entitlement.Resource.Id.Resource

		splitted := strings.Split(entitlement.Slug, "/")
		if len(splitted) != 2 {
			return nil, nil, fmt.Errorf("invalid entitlement slug %s", entitlement.Slug)
		}

		role := splitted[1]

		response, err := o.client.UpsertFolderUserCollaborator(ctx, folderId, userId, role)
		if err != nil {
			return nil, nil, err
		}

		userID, err := rs.NewResourceID(userResourceType, response.UserId)
		if err != nil {
			return nil, nil, err
		}

		metadata := map[string]interface{}{
			metaRole:    response.Role,
			metaCreated: response.Created.String(),
		}

		newGrant := grant.NewGrant(resource, folderHasUserAccessEntitlement+response.Role, userID, grant.WithGrantMetadata(metadata))

		return []*v2.Grant{newGrant}, nil, nil
	}

	return nil, nil, fmt.Errorf("resource type %s is not supported", resource.Id.ResourceType)
}

func (o *folderBuilder) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	if grant.Principal.Id.ResourceType == userResourceType.Id {
		userId := grant.Principal.Id.Resource
		folderId := grant.Entitlement.Resource.Id.Resource

		err := o.client.DeleteFolderUserCollaborator(ctx, folderId, userId)
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

func newFolderBuilder(client *client.LucidchartClient, excludeShortcuts bool) *folderBuilder {
	return &folderBuilder{
		client:           client,
		excludeShortcuts: excludeShortcuts,
	}
}
