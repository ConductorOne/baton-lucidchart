package connector

import (
	"context"
	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
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
		folderContent, nextToken, err := o.client.RootFolderContent(ctx, pToken.Token)
		if err != nil {
			return nil, "", nil, err
		}

		l.Info("folderContent", zap.Any("folderContent", folderContent))

		root, err := folderResource("root", "root", nil)
		if err != nil {
			return nil, "", nil, err
		}

		resources := []*v2.Resource{
			root,
		}

		return resources, nextToken, nil, err
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
	return nil, "", nil, nil
}

func (o *folderBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
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
	resourceOptions := []resource.ResourceOption{
		resource.WithParentResourceID(parentResourceID),
		resource.WithAnnotation(
			&v2.ChildResourceType{
				ResourceTypeId: folderResourceType.Id,
			},
		),
	}

	return resource.NewResource(
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
