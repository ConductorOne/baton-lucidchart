package connector

import (
	"context"
	"fmt"
	"strconv"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const assignedEntitlement = "assigned"

type licenseBuilder struct {
	client *client.LucidchartClient
}

func (l *licenseBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return licenseResourceType
}

func licenseResource(sub client.Subscription) (*v2.Resource, error) {
	name := "Lucid Subscription " + sub.Id

	assignedEntitlementID := ent.NewEntitlementID(
		&v2.Resource{Id: &v2.ResourceId{ResourceType: licenseResourceType.Id, Resource: sub.Id}},
		assignedEntitlement,
	)

	licenseOpts := []rs.LicenseProfileTraitOption{
		rs.WithLicenseName(name),
		rs.WithLicenseEntitlementIDs(assignedEntitlementID),
	}

	if sub.LicenseTotal != nil {
		licenseOpts = append(licenseOpts, rs.WithLicenseSeats(*sub.LicenseTotal, sub.LicensesUsed))
	}

	return rs.NewResource(
		name,
		licenseResourceType,
		sub.Id,
		rs.WithLicenseProfileTrait(licenseOpts...),
	)
}

func (l *licenseBuilder) List(ctx context.Context, _ *v2.ResourceId, opts rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	logger := ctxzap.Extract(ctx)
	pToken := &opts.PageToken

	subscriptions, nextToken, err := l.client.ListSubscriptions(ctx, pToken.Token)
	if err != nil {
		code := status.Code(err)
		if code == codes.NotFound || code == codes.PermissionDenied {
			logger.Warn("baton-lucidchart: failed to fetch subscriptions; skipping license sync", zap.Error(err))
			return nil, &rs.SyncOpResults{}, nil
		}
		return nil, nil, fmt.Errorf("baton-lucidchart: failed to fetch subscriptions: %w", err)
	}

	var resources []*v2.Resource
	for _, sub := range subscriptions {
		lr, err := licenseResource(sub)
		if err != nil {
			return nil, nil, fmt.Errorf("baton-lucidchart: failed to build license resource %s: %w", sub.Id, err)
		}
		resources = append(resources, lr)
	}

	return resources, &rs.SyncOpResults{NextPageToken: nextToken}, nil
}

func (l *licenseBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	entitlementOpts := []ent.EntitlementOption{
		ent.WithGrantableTo(userResourceType),
		ent.WithDisplayName(fmt.Sprintf("%s license %s", resource.DisplayName, assignedEntitlement)),
		ent.WithDescription(fmt.Sprintf("Holds a %s license seat in Lucid", resource.DisplayName)),
	}

	en := ent.NewAssignmentEntitlement(resource, assignedEntitlement, entitlementOpts...)
	return []*v2.Entitlement{en}, &rs.SyncOpResults{}, nil
}

// Grants fetches user-license assignments from the Lucid Licensing API
// (GET /v1/subscriptions/{id}/licenses) and emits one grant per assignment.
func (l *licenseBuilder) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	logger := ctxzap.Extract(ctx)
	pToken := &opts.PageToken

	subscriptionId := resource.Id.Resource
	licenses, nextToken, err := l.client.ListLicenses(ctx, subscriptionId, pToken.Token)
	if err != nil {
		code := status.Code(err)
		if code == codes.NotFound || code == codes.PermissionDenied {
			logger.Warn("baton-lucidchart: failed to fetch licenses; skipping license grants", zap.Error(err))
			return nil, &rs.SyncOpResults{}, nil
		}
		return nil, nil, fmt.Errorf("baton-lucidchart: failed to fetch licenses for subscription %s: %w", subscriptionId, err)
	}

	var grants []*v2.Grant
	for _, lic := range licenses {
		userID, err := rs.NewResourceID(userResourceType, lic.UserId)
		if err != nil {
			return nil, nil, err
		}

		metadata := map[string]interface{}{
			"subscription_id": lic.SubscriptionId,
			"user_id":         strconv.Itoa(lic.UserId),
			"created":         lic.Created,
		}

		g := grant.NewGrant(resource, assignedEntitlement, userID, grant.WithGrantMetadata(metadata))
		grants = append(grants, g)
	}

	return grants, &rs.SyncOpResults{NextPageToken: nextToken}, nil
}

func newLicenseBuilder(client *client.LucidchartClient) *licenseBuilder {
	return &licenseBuilder{
		client: client,
	}
}
