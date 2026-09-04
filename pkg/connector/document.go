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
	rootId = "root"

	documentHasUserAccessEntitlement = "user/"
)

type documentBuilder struct {
	client           *client.LucidchartClient
	excludeShortcuts bool
}

func (o *documentBuilder) ResourceType(ctx context.Context) *v2.ResourceType {
	return documentResourceType
}

func (o *documentBuilder) List(ctx context.Context, parentResourceID *v2.ResourceId, opts rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	l := ctxzap.Extract(ctx)
	pToken := &opts.PageToken

	if parentResourceID == nil && pToken.Token == "" {
		l.Debug("baton-lucidchart: ignoring first List call for root folder, only uses parentResourceID")
		return nil, nil, nil
	}

	if parentResourceID != nil {
		l.Debug("baton-lucidchart: listing documents for parent", zap.String("parent_id", parentResourceID.Resource))
		var folderContent []client.FolderContent
		var nextToken string
		var err error

		if parentResourceID.Resource == rootId {
			folderContent, nextToken, err = o.client.RootFolderContent(ctx, pToken.Token)
			if err != nil {
				return nil, nil, err
			}
		} else {
			folderContent, nextToken, err = o.client.FolderContent(ctx, parentResourceID.Resource, pToken.Token)
			if err != nil {
				return nil, nil, err
			}
		}

		var resources []*v2.Resource
		for _, item := range folderContent {
			if item.Type != "document" {
				continue
			}

			if item.IsShortcut && o.excludeShortcuts {
				l.Debug("baton-lucidchart: skipping shortcut document", zap.String("document_id", item.ID()))
				continue
			}

			newRes, err := documentResource(item.ID(), item.Name, parentResourceID)
			if err != nil {
				return nil, nil, err
			}
			resources = append(resources, newRes)
		}

		return resources, &rs.SyncOpResults{NextPageToken: nextToken}, nil
	}

	l.Error("invalid parentResourceID", zap.Any("parentResourceID", parentResourceID))

	return nil, nil, nil
}

func (o *documentBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	var rv []*v2.Entitlement

	for _, role := range client.UserFolderRoles {
		assigmentOptions := []entitlement.EntitlementOption{
			entitlement.WithGrantableTo(userResourceType),
			entitlement.WithDescription(fmt.Sprintf("%s can %s on %s", userResourceType.DisplayName, role, resource.DisplayName)),
			entitlement.WithDisplayName(fmt.Sprintf("%s is %s of %s", userResourceType.DisplayName, role, resource.DisplayName)),
		}
		rv = append(rv, entitlement.NewPermissionEntitlement(resource, documentHasUserAccessEntitlement+role, assigmentOptions...))
	}

	return rv, nil, nil
}

func (o *documentBuilder) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	l := ctxzap.Extract(ctx)
	if resource.Id.Resource == "root" {
		return nil, nil, nil
	}

	pToken := &opts.PageToken

	collaborators, nextToken, err := o.client.ListDocumentUserCollaborators(ctx, resource.Id.Resource, pToken.Token)
	if err != nil {
		// Ignore permission denied errors as they indicate no access to collaborators for this document
		if status.Code(err) == codes.PermissionDenied {
			l.Debug("baton-lucidchart: permission denied when listing document collaborators", zap.String("document_id", resource.Id.Resource))
			return nil, nil, nil
		}
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

		newGrant := grant.NewGrant(resource, documentHasUserAccessEntitlement+collaborator.Role, userID, grant.WithGrantMetadata(metadata))

		grants = append(grants, newGrant)
	}

	return grants, &rs.SyncOpResults{NextPageToken: nextToken}, nil
}

func (o *documentBuilder) Grant(ctx context.Context, resource *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)
	if resource.Id.ResourceType == userResourceType.Id {
		userId := resource.Id.Resource
		documentId := entitlement.Resource.Id.Resource

		splitted := strings.Split(entitlement.Slug, "/")
		if len(splitted) != 2 {
			return nil, nil, fmt.Errorf("invalid entitlement slug %s", entitlement.Slug)
		}

		role := splitted[1]

		// Pre-check the current role so a no-op re-grant reports GrantAlreadyExists.
		// Best-effort: any read error falls through to the authoritative upsert.
		current, err := o.client.GetDocumentUserCollaborator(ctx, documentId, userId)
		if err != nil {
			// Debug, not Warn: this can recur on every grant for tenants without the GET.
			l.Debug("baton-lucidchart: document collaborator pre-check GET failed; falling through to upsert",
				zap.String("document_id", documentId),
				zap.String("user_id", userId),
				zap.Error(err),
			)
		} else if current.Role == role {
			// Return the grant alongside GrantAlreadyExists so C1 materializes the
			// membership now instead of waiting for the next sync; the annotation
			// alone carries no grant data. The ID and metadata match what Grants() emits.
			metadata := map[string]interface{}{
				metaRole:    current.Role,
				metaCreated: current.Created.String(),
			}
			newGrant := grant.NewGrant(entitlement.Resource, entitlement.Slug, resource.Id, grant.WithGrantMetadata(metadata))
			return []*v2.Grant{newGrant}, annotations.New(&v2.GrantAlreadyExists{}), nil
		}

		response, err := o.client.UpsertDocumentUserCollaborator(ctx, documentId, userId, role)
		if err != nil {
			// Lucid's upsert is documented as never returning 409 today, but if it
			// ever does, treat it as an idempotent success rather than a failure.
			if client.IsAlreadyExistsError(err) {
				return nil, annotations.New(&v2.GrantAlreadyExists{}), nil
			}
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

		newGrant := grant.NewGrant(resource, documentHasUserAccessEntitlement+response.Role, userID, grant.WithGrantMetadata(metadata))

		return []*v2.Grant{newGrant}, nil, nil
	}

	return nil, nil, fmt.Errorf("invalid resource type %s", resource.Id.ResourceType)
}

func (o *documentBuilder) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	if grant.Principal.Id.ResourceType == userResourceType.Id {
		userId := grant.Principal.Id.Resource
		documentId := grant.Entitlement.Resource.Id.Resource

		// Remove the user's collaborator record entirely. A 404 (already gone) is
		// an idempotent success (GrantAlreadyRevoked).
		err := o.client.DeleteDocumentUserCollaborator(ctx, documentId, userId)
		if err != nil {
			if client.IsNotFoundError(err) {
				return annotations.New(&v2.GrantAlreadyRevoked{}), nil
			}

			return nil, err
		}

		return nil, nil
	}

	return nil, fmt.Errorf("resource type %s is not supported", grant.Principal.Id.ResourceType)
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

func newDocumentBuilder(client *client.LucidchartClient, excludeShortcuts bool) *documentBuilder {
	return &documentBuilder{
		client:           client,
		excludeShortcuts: excludeShortcuts,
	}
}
