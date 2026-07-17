package connector

import (
	"context"
	"errors"
	"testing"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockLicenseClient struct {
	subscriptionsErr error
	subscriptions    []client.Subscription
	nextSubToken     string

	licensesErr  error
	licenses     []client.License
	nextLicToken string
}

func (m *mockLicenseClient) ListSubscriptions(_ context.Context, _ string) ([]client.Subscription, string, error) {
	return m.subscriptions, m.nextSubToken, m.subscriptionsErr
}

func (m *mockLicenseClient) ListLicenses(_ context.Context, _ string, _ string) ([]client.License, string, error) {
	return m.licenses, m.nextLicToken, m.licensesErr
}

func TestLicenseList_PropagatesNotFound(t *testing.T) {
	notFoundErr := status.Error(codes.NotFound, "not found")
	lb := &licenseBuilder{client: &mockLicenseClient{subscriptionsErr: notFoundErr}}

	_, _, err := lb.List(context.Background(), nil, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected error from List when client returns NotFound, got nil")
	}
	if !errors.Is(err, notFoundErr) {
		t.Fatalf("expected wrapped NotFound error, got: %v", err)
	}
}

func TestLicenseList_PropagatesPermissionDenied(t *testing.T) {
	permErr := status.Error(codes.PermissionDenied, "forbidden")
	lb := &licenseBuilder{client: &mockLicenseClient{subscriptionsErr: permErr}}

	_, _, err := lb.List(context.Background(), nil, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected error from List when client returns PermissionDenied, got nil")
	}
	if !errors.Is(err, permErr) {
		t.Fatalf("expected wrapped PermissionDenied error, got: %v", err)
	}
}

func TestLicenseGrants_PropagatesNotFound(t *testing.T) {
	notFoundErr := status.Error(codes.NotFound, "not found")
	lb := &licenseBuilder{client: &mockLicenseClient{licensesErr: notFoundErr}}

	resource := &v2.Resource{
		Id: &v2.ResourceId{ResourceType: licenseResourceType.Id, Resource: "sub-123"},
	}
	_, _, err := lb.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected error from Grants when client returns NotFound, got nil")
	}
	if !errors.Is(err, notFoundErr) {
		t.Fatalf("expected wrapped NotFound error, got: %v", err)
	}
}

func TestLicenseGrants_PropagatesPermissionDenied(t *testing.T) {
	permErr := status.Error(codes.PermissionDenied, "forbidden")
	lb := &licenseBuilder{client: &mockLicenseClient{licensesErr: permErr}}

	resource := &v2.Resource{
		Id: &v2.ResourceId{ResourceType: licenseResourceType.Id, Resource: "sub-123"},
	}
	_, _, err := lb.Grants(context.Background(), resource, rs.SyncOpAttrs{})
	if err == nil {
		t.Fatal("expected error from Grants when client returns PermissionDenied, got nil")
	}
	if !errors.Is(err, permErr) {
		t.Fatalf("expected wrapped PermissionDenied error, got: %v", err)
	}
}
