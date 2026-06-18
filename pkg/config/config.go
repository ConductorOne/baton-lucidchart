package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	LucidApiKeyField = field.StringField(
		"lucid-api-key",
		field.WithDisplayName("API Key"),
		field.WithDescription("The API key for the Lucidchart API."),
		field.WithRequired(true),
		field.WithIsSecret(true),
	)

	LucidClientIdField = field.StringField(
		"lucid-client-id",
		field.WithDisplayName("Client ID"),
		field.WithDescription("The OAuth2 client ID for the Lucidchart API."),
		field.WithRequired(true),
	)

	LucidClientSecretField = field.StringField(
		"lucid-client-secret",
		field.WithDisplayName("Client Secret"),
		field.WithDescription("The OAuth2 client secret for the Lucidchart API."),
		field.WithRequired(true),
		field.WithIsSecret(true),
	)

	LucidRefreshTokenField = field.StringField(
		"lucid-refresh-token",
		field.WithDisplayName("Refresh Token"),
		field.WithDescription("The OAuth2 refresh token for the Lucidchart API."),
		field.WithRequired(true),
		field.WithIsSecret(true),
	)

	ExcludeShortcutsField = field.BoolField(
		"exclude-shortcuts",
		field.WithDescription("Exclude shortcut documents and folders"),
		field.WithDefaultValue(false),
	)

	BaseURLField = field.StringField(
		"base-url",
		field.WithDescription("Override the Lucidchart API URL (for testing)"),
		field.WithHidden(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	ConfigurationFields = []field.SchemaField{
		LucidApiKeyField,
		LucidClientIdField,
		LucidClientSecretField,
		LucidRefreshTokenField,
		ExcludeShortcutsField,
		BaseURLField,
	}

	FieldRelationships = []field.SchemaFieldRelationship{}
)

//go:generate go run ./gen
var Config = field.NewConfiguration(
	ConfigurationFields,
	field.WithConstraints(FieldRelationships...),
	field.WithConnectorDisplayName("Lucidchart"),
	field.WithIconUrl("/static/app-icons/lucidchart.svg"),
	field.WithHelpUrl("/docs/baton/lucidchart"),
)
