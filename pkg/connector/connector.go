package connector

import (
	"context"
	"fmt"
	"io"
	"strings"

	cfg "github.com/conductorone/baton-lucidchart/pkg/config"
	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/cli"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"golang.org/x/oauth2"
)

const (
	// metaRole and metaCreated are grant-metadata keys shared by document and
	// folder collaborator grants.
	metaRole    = "role"
	metaCreated = "created"
)

type Connector struct {
	client                *client.LucidchartClient
	excludeShortcuts      bool
	contentTransferUserID string
}

// ResourceSyncers returns a ResourceSyncerV2 for each resource type that should be synced from the upstream service.
func (d *Connector) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncerV2 {
	return []connectorbuilder.ResourceSyncerV2{
		newUserBuilder(d.client, d.contentTransferUserID),
		newFolderBuilder(d.client, d.excludeShortcuts),
		newDocumentBuilder(d.client, d.excludeShortcuts),
	}
}

// Asset takes an input AssetRef and attempts to fetch it using the connector's authenticated http client
// It streams a response, always starting with a metadata object, following by chunked payloads for the asset.
func (d *Connector) Asset(ctx context.Context, asset *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

// Metadata returns metadata about the connector.
func (d *Connector) Metadata(ctx context.Context) (*v2.ConnectorMetadata, error) {
	return &v2.ConnectorMetadata{
		DisplayName: "Lucidchart",
		Description: "Lucidchart connector",
		AccountCreationSchema: &v2.ConnectorAccountCreationSchema{
			FieldMap: map[string]*v2.ConnectorAccountCreationSchema_Field{
				"firstName": {
					DisplayName: "First Name",
					Required:    true,
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Order: 1,
				},
				"lastName": {
					DisplayName: "Last Name",
					Required:    true,
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Order: 2,
				},
				"email": {
					DisplayName: "Email",
					Required:    true,
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Order: 3,
				},
				"username": {
					DisplayName: "Username",
					Required:    false,
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Order: 4,
				},
				"roles": {
					DisplayName: "Roles",
					Required:    false,
					Field: &v2.ConnectorAccountCreationSchema_Field_StringListField{
						StringListField: &v2.ConnectorAccountCreationSchema_StringListField{},
					},
					Order: 5,
				},
			},
		},
	}, nil
}

// Validate is called to ensure that the connector is properly configured. It should exercise any API credentials
// to be sure that they are valid.
func (d *Connector) Validate(ctx context.Context) (annotations.Annotations, error) {
	return nil, nil
}

const lucidTokenURL = "https://api.lucid.co/oauth2/token" //nolint:gosec // not a credential, it's a URL

// New returns a new instance of the connector.
func New(ctx context.Context, connectorConfig *cfg.Lucidchart, opts *cli.ConnectorOpts) (connectorbuilder.ConnectorBuilderV2, []connectorbuilder.Opt, error) {
	if connectorConfig.LucidApiKey == "" {
		return nil, nil, fmt.Errorf("baton-lucidchart: lucid-api-key is required")
	}

	var tokenSource oauth2.TokenSource
	if connectorConfig.LucidRefreshToken == "" {
		if opts == nil || opts.TokenSource == nil {
			return nil, nil, fmt.Errorf("baton-lucidchart: lucid-refresh-token is required when no OAuth token source is provided")
		}
		tokenSource = opts.TokenSource
	} else {
		// Derive the token URL from base-url so that a test mock that overrides
		// base-url also receives token requests locally.
		tokenURL := lucidTokenURL
		if connectorConfig.BaseUrl != "" {
			tokenURL = strings.TrimRight(connectorConfig.BaseUrl, "/") + "/oauth2/token"
		}
		oauthConfig := &oauth2.Config{
			ClientID:     connectorConfig.LucidClientId,
			ClientSecret: connectorConfig.LucidClientSecret,
			Endpoint: oauth2.Endpoint{
				TokenURL: tokenURL,
			},
		}
		tokenSource = oauthConfig.TokenSource(ctx, &oauth2.Token{
			RefreshToken: connectorConfig.LucidRefreshToken,
		})
	}

	lucidClient, err := client.NewLucidchartClient(
		ctx,
		connectorConfig.LucidApiKey,
		tokenSource,
		connectorConfig.BaseUrl,
		connectorConfig.LucidScimToken,
		connectorConfig.ScimBaseUrl,
	)
	if err != nil {
		return nil, nil, err
	}

	return &Connector{
		client:                lucidClient,
		excludeShortcuts:      connectorConfig.ExcludeShortcuts,
		contentTransferUserID: connectorConfig.LucidContentTransferUserId,
	}, nil, nil
}
