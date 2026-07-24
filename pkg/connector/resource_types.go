package connector

import (
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
)

// The user resource type is for all user objects from the database.
var userResourceType = &v2.ResourceType{
	Id:          "user",
	DisplayName: "User",
	Traits:      []v2.ResourceType_Trait{v2.ResourceType_TRAIT_USER},
}

var folderResourceType = &v2.ResourceType{
	Id:          "folder",
	DisplayName: "Folder",
}

var documentResourceType = &v2.ResourceType{
	Id:          "document",
	DisplayName: "Document",
}

var licenseResourceType = &v2.ResourceType{
	Id:          "license",
	DisplayName: "License",
	Traits: []v2.ResourceType_Trait{
		v2.ResourceType_TRAIT_LICENSE_PROFILE,
	},
	Annotations: annotations.New(
		&v2.OptInRequired{},
		&v2.SkipEntitlements{},
		capabilityPermissions("licenses:admin.readonly"),
	),
}

// capabilityPermissions builds the CapabilityPermissions annotation declaring
// the OAuth scopes a resource type's API calls require.
func capabilityPermissions(perms ...string) *v2.CapabilityPermissions {
	var permissions []*v2.CapabilityPermission
	for _, p := range perms {
		permissions = append(permissions, v2.CapabilityPermission_builder{Permission: p}.Build())
	}
	return v2.CapabilityPermissions_builder{Permissions: permissions}.Build()
}
