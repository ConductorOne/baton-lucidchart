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
)

const assignedEntitlement = "assigned"

// Grant metadata keys for license assignments.
const (
	metadataKeySubscriptionID = "subscription_id"
	metadataKeyUserID         = "user_id"
	metadataKeyCreated        = "created"
)

type licenseClient interface {
	ListSubscriptions(ctx context.Context, pageToken string) ([]client.Subscription, string, error)
	ListLicenses(ctx context.Context, subscriptionId string, pageToken string) ([]client.License, string, error)
}

type licenseBuilder struct {
	client licenseClient
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
	pToken := &opts.PageToken
	subscriptions, nextToken, err := l.client.ListSubscriptions(ctx, pToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-lucidchart: failed to fetch subscriptions: %w", err)
	}

	resources := make([]*v2.Resource, 0, len(subscriptions))
	for _, sub := range subscriptions {
		lr, err := licenseResource(sub)
		if err != nil {
			return nil, nil, fmt.Errorf("baton-lucidchart: failed to build license resource %s: %w", sub.Id, err)
		}
		resources = append(resources, lr)
	}

	return resources, &rs.SyncOpResults{NextPageToken: nextToken}, nil
}

func (l *licenseBuilder) StaticEntitlements(_ context.Context, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	en := ent.NewAssignmentEntitlement(nil, assignedEntitlement,
		ent.WithGrantableTo(userResourceType),
		ent.WithDisplayName("Assigned"),
		ent.WithDescription("Holds a license seat in this Lucid subscription"),
	)
	return []*v2.Entitlement{en}, nil, nil
}

func (l *licenseBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// Grants fetches user-license assignments from the Lucid Licensing API
// (GET /v1/subscriptions/{id}/licenses) and emits one grant per assignment.
func (l *licenseBuilder) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	pToken := &opts.PageToken

	subscriptionId := resource.Id.Resource
	licenses, nextToken, err := l.client.ListLicenses(ctx, subscriptionId, pToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-lucidchart: failed to fetch licenses for subscription %s: %w", subscriptionId, err)
	}

	grants := make([]*v2.Grant, 0, len(licenses))
	for _, lic := range licenses {
		userID, err := rs.NewResourceID(userResourceType, lic.UserId)
		if err != nil {
			return nil, nil, fmt.Errorf("baton-lucidchart: build user resource id for user %d: %w", lic.UserId, err)
		}

		metadata := map[string]interface{}{
			metadataKeySubscriptionID: lic.SubscriptionId,
			metadataKeyUserID:         strconv.Itoa(lic.UserId),
			metadataKeyCreated:        lic.Created,
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
