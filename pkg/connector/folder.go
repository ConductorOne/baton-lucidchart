package connector

import (
	"context"
	"fmt"
	"strings"

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

		// Omit metaCreated rather than serialize a zero time: a record whose
		// created is missing or undecodable would otherwise publish a fabricated
		// "0001-01-01 00:00:00 +0000 UTC". Matches every Grant() path below.
		metadata := map[string]interface{}{metaRole: collaborator.Role}
		if !collaborator.Created.IsZero() {
			metadata[metaCreated] = collaborator.Created.String()
		}

		newGrant := grant.NewGrant(resource, folderHasUserAccessEntitlement+collaborator.Role, userID, grant.WithGrantMetadata(metadata))

		grants = append(grants, newGrant)
	}

	return grants, &rs.SyncOpResults{NextPageToken: nextToken}, nil
}

func (o *folderBuilder) Grant(ctx context.Context, resource *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)
	if resource.Id.ResourceType == userResourceType.Id {
		userId := resource.Id.Resource
		folderId := entitlement.Resource.Id.Resource

		splitted := strings.Split(entitlement.Slug, "/")
		if len(splitted) != 2 {
			return nil, nil, fmt.Errorf("invalid entitlement slug %s", entitlement.Slug)
		}

		role := splitted[1]

		// Pre-check the current role so a no-op re-grant reports GrantAlreadyExists.
		// Best-effort: any read error falls through to the authoritative upsert.
		//
		// preCheckSaysAbsent separates the two things a nil `current` can mean: a
		// 404 (Lucid answering "this user holds no direct share" — though a tenant
		// without the GET route returns the same status, so it is not unconditional
		// proof) versus an ambiguous failure — 403, 5xx, a timeout — after which
		// nothing at all is known. The 409 guard below is stricter in the first
		// case. The route caveat needs no handling today: Lucid does not 409 on the
		// upsert, and if it ever did, the strict path fails safe — it surfaces the
		// error instead of fabricating a grant.
		var preCheckSaysAbsent bool
		current, err := o.client.GetFolderUserCollaborator(ctx, folderId, userId)
		if err != nil {
			preCheckSaysAbsent = client.IsNotFoundError(err)

			// Warn only for 403, the one failure the client can act on (grant the
			// share-read scope). Everything else — the expected 404, 5xx, timeouts,
			// tenants without the GET — stays at Debug: nobody can act on it, and it
			// can recur on every grant.
			if client.IsPermissionDeniedError(err) {
				l.Warn("baton-lucidchart: folder collaborator pre-check GET denied — check OAuth scope; falling through to upsert",
					zap.String("folder_id", folderId),
					zap.String("user_id", userId),
					zap.Error(err),
				)
			} else {
				l.Debug("baton-lucidchart: folder collaborator pre-check GET failed; falling through to upsert",
					zap.String("folder_id", folderId),
					zap.String("user_id", userId),
					zap.Error(err),
				)
			}
		} else if current.Role == role {
			// Return the grant alongside GrantAlreadyExists so C1 materializes the
			// membership now instead of waiting for the next sync; the annotation
			// alone carries no grant data. The ID and metadata match what Grants() emits.
			metadata := map[string]interface{}{metaRole: current.Role}
			if !current.Created.IsZero() {
				metadata[metaCreated] = current.Created.String()
			}
			newGrant := grant.NewGrant(entitlement.Resource, entitlement.Slug, resource.Id, grant.WithGrantMetadata(metadata))
			return []*v2.Grant{newGrant}, annotations.New(&v2.GrantAlreadyExists{}), nil
		}

		response, err := o.client.UpsertFolderUserCollaborator(ctx, folderId, userId, role)
		if err != nil {
			// Lucid's upsert is documented as never returning 409 today, but if it
			// ever does, treat it as an idempotent success rather than a failure —
			// but only when the conflict really is about the role we asked for.
			// When Lucid returns the conflicting record in the 409 body (the upsert
			// decodes it before checking the status), that record is authoritative.
			// If it names a *different* role, the requested role genuinely was not
			// granted: the emitted grant is keyed on entitlement.Slug, so reporting
			// GrantAlreadyExists would make C1 materialize an entitlement the user
			// does not hold until the next sync corrects it. Fall through to the
			// real error in that case, and metaCreated is omitted rather than
			// fabricated from a zero time.
			//
			// A successful pre-check outranks an absent role in the 409 body: if
			// current != nil the GET returned a record, and the equal-role case
			// already returned above, so current.Role != role is known. Treating an
			// undecodable 409 as idempotent there would claim a role the user
			// demonstrably does not hold.
			if client.IsConflictError(err) && current == nil {
				// How much the 409 has to prove depends on what the pre-check
				// established. After a 404 the user provably held no direct share, so
				// only a decoded, matching role is enough to overturn that and call
				// the conflict idempotent — an undecodable body is likelier a plain
				// failed PUT than a share that materialized between the two calls.
				// After an ambiguous failure (403/5xx/timeout) or no pre-check at all,
				// nothing is known either way, so a silent 409 stays idempotent: that
				// is the conservative reading when the alternative is failing a grant
				// whose target state may already be correct.
				if response.Role == role || (!preCheckSaysAbsent && response.Role == "") {
					metadata := map[string]interface{}{metaRole: role}
					if !response.Created.IsZero() {
						metadata[metaCreated] = response.Created.String()
					}
					newGrant := grant.NewGrant(entitlement.Resource, entitlement.Slug, resource.Id, grant.WithGrantMetadata(metadata))
					return []*v2.Grant{newGrant}, annotations.New(&v2.GrantAlreadyExists{}), nil
				}
			}
			return nil, nil, err
		}

		userID, err := rs.NewResourceID(userResourceType, response.UserId)
		if err != nil {
			return nil, nil, err
		}

		// Same omit-when-zero rule as the branches above and Grants(): if the
		// upsert response carried no usable created timestamp, leave the key out
		// instead of publishing a zero time as if it were real.
		metadata := map[string]interface{}{metaRole: response.Role}
		if !response.Created.IsZero() {
			metadata[metaCreated] = response.Created.String()
		}

		// The entitlement's resource (the folder) is the first argument, not the
		// principal: NewGrant keys NewEntitlementID on it, so passing the user here
		// collides across every folder the same user holds the same role on.
		// Matches the pre-check and 409 branches above and what Grants() emits.
		//
		// Keyed on entitlement.Slug — the entitlement C1 actually asked for —
		// rather than rebuilt from the role Lucid echoed back, so a normalized or
		// substituted role in the response can never emit a grant for an
		// entitlement that was never requested.
		newGrant := grant.NewGrant(entitlement.Resource, entitlement.Slug, userID, grant.WithGrantMetadata(metadata))

		return []*v2.Grant{newGrant}, nil, nil
	}

	return nil, nil, fmt.Errorf("resource type %s is not supported", resource.Id.ResourceType)
}

func (o *folderBuilder) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	if grant.Principal.Id.ResourceType == userResourceType.Id {
		userId := grant.Principal.Id.Resource
		folderId := grant.Entitlement.Resource.Id.Resource

		// Remove the user's collaborator record entirely. A 404 (already gone) is
		// an idempotent success (GrantAlreadyRevoked).
		err := o.client.DeleteFolderUserCollaborator(ctx, folderId, userId)
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
