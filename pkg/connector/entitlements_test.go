package connector

import (
	"context"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/stretchr/testify/require"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
)

func TestWritableRoles_StaysInSyncWithReadCatalog(t *testing.T) {
	readable := make(map[string]bool, len(client.UserFolderRoles))
	for _, role := range client.UserFolderRoles {
		readable[role] = true
	}

	// (a) Every writable role must be readable, and the difference between the
	// two catalogs must be exactly the owner role.
	for _, role := range client.UserFolderWritableRoles {
		require.True(t, readable[role], "%q is writable but not readable", role)
	}
	writable := make(map[string]bool, len(client.UserFolderWritableRoles))
	for _, role := range client.UserFolderWritableRoles {
		writable[role] = true
	}
	var onlyReadable []string
	for _, role := range client.UserFolderRoles {
		if !writable[role] {
			onlyReadable = append(onlyReadable, role)
		}
	}
	require.Equal(t, []string{"owner"}, onlyReadable)
	// (d) Grants-role closure: every role literal the collaborator fixtures
	// exercise (document_test.go/folder_test.go seed cts.roles with these) is a
	// member of the read catalog, and owner stays in it so owner collaborator
	// grants keep syncing.
	for _, role := range []string{"edit", "view", "comment"} {
		require.True(t, readable[role], "fixture role %q missing from read catalog", role)
	}
	require.True(t, readable["owner"], "owner must stay in the read catalog: Grants() emits owner collaborator grants")
}

func TestFolderAndDocumentEntitlements_GrantabilitySplit(t *testing.T) {
	t.Parallel()

	b := &folderBuilder{client: newTestClient(t, newCollaboratorTestServer(t, "folders").server.URL)}
	d := &documentBuilder{client: newTestClient(t, newCollaboratorTestServer(t, "documents").server.URL)}

	tests := []struct {
		name string
		ents func() []*v2.Entitlement
	}{
		{"folder", func() []*v2.Entitlement {
			resource := &v2.Resource{
				Id:          &v2.ResourceId{ResourceType: folderResourceType.Id, Resource: "fld-1"},
				DisplayName: "Folder One",
			}
			ents, _, err := b.Entitlements(context.Background(), resource, rs.SyncOpAttrs{})
			require.NoError(t, err)
			return ents
		}},
		{"document", func() []*v2.Entitlement {
			resource := &v2.Resource{
				Id:          &v2.ResourceId{ResourceType: documentResourceType.Id, Resource: "doc-1"},
				DisplayName: "Document One",
			}
			ents, _, err := d.Entitlements(context.Background(), resource, rs.SyncOpAttrs{})
			require.NoError(t, err)
			return ents
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ents := tt.ents()
			// (b) The emitted slug set equals the full read catalog — all five,
			// none extra, none renamed.
			require.Len(t, ents, len(client.UserFolderRoles))
			seen := make(map[string]bool, len(ents))
			for _, ent := range ents {
				seen[ent.GetSlug()] = true
			}
			for _, role := range client.UserFolderRoles {
				require.Contains(t, seen, folderHasUserAccessEntitlement+role, "slug %q missing", role)
			}
			// (c) Exactly the four writable roles are grantable to user; owner
			// is emitted but carries no WithGrantableTo option.
			for _, ent := range ents {
				grantableTo := ent.GetGrantableTo()
				if ent.GetSlug() == folderHasUserAccessEntitlement+"owner" {
					require.Empty(t, grantableTo, "user/owner must not be grantable")
					continue
				}
				require.Len(t, grantableTo, 1)
				require.Equal(t, userResourceType.Id, grantableTo[0].GetId())
			}
		})
	}
}
