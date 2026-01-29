package connector

import (
	"context"
	"errors"
	"io"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"golang.org/x/oauth2"
)

type Connector struct {
	client           *client.LucidchartClient
	excludeShortcuts bool
}

// ResourceSyncers returns a ResourceSyncer for each resource type that should be synced from the upstream service.
func (d *Connector) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncer {
	return []connectorbuilder.ResourceSyncer{
		newUserBuilder(d.client),
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

// New returns a new instance of the connector.
func New(ctx context.Context, apiKey string, clientID string, clientSecret string, refreshToken string, excludeShortcuts bool) (*Connector, error) {
	if apiKey == "" {
		return nil, errors.New("apiKey is required")
	}

	if clientID == "" {
		return nil, errors.New("clientID is required")
	}

	if clientSecret == "" {
		return nil, errors.New("clientSecret is required")
	}

	if refreshToken == "" {
		return nil, errors.New("refreshToken is required")
	}

	// Set up OAuth2 config
	oauthConfig := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: "https://api.lucid.co/oauth2/token",
		},
	}

	// Create token source from refresh token
	token := &oauth2.Token{
		RefreshToken: refreshToken,
	}
	tokenSource := oauthConfig.TokenSource(ctx, token)

	lucidClient, err := client.NewLucidchartClient(ctx, apiKey, tokenSource)
	if err != nil {
		return nil, err
	}

	return &Connector{
		client:           lucidClient,
		excludeShortcuts: excludeShortcuts,
	}, nil
}
