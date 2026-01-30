package config

//go:generate go run ./gen

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	LucidApiKeyField = field.StringField(
		"lucid-api-key",
		field.WithDisplayName("Lucidchart API Key"),
		field.WithRequired(true),
		field.WithDescription("The API key for the Lucidchart API."),
		field.WithIsSecret(true),
	)

	LucidClientIdField = field.StringField(
		"lucid-client-id",
		field.WithDisplayName("Lucidchart Client ID"),
		field.WithRequired(true),
		field.WithDescription("The OAuth2 client ID for the Lucidchart API."),
	)

	LucidClientSecretField = field.StringField(
		"lucid-client-secret",
		field.WithDisplayName("Lucidchart Client Secret"),
		field.WithRequired(true),
		field.WithDescription("The OAuth2 client secret for the Lucidchart API."),
		field.WithIsSecret(true),
	)

	LucidRefreshTokenField = field.StringField(
		"lucid-refresh-token",
		field.WithDisplayName("Lucidchart Refresh Token"),
		field.WithRequired(true),
		field.WithDescription("The OAuth2 refresh token for the Lucidchart API."),
		field.WithIsSecret(true),
	)

	ExcludeShortcutsField = field.BoolField(
		"exclude-shortcuts",
		field.WithDisplayName("Exclude Shortcuts"),
		field.WithDefaultValue(false),
		field.WithDescription("Exclude shortcut documents and folders"),
	)

	// ConfigurationFields defines the external configuration required for the
	// connector to run.
	ConfigurationFields = []field.SchemaField{
		LucidApiKeyField,
		LucidClientIdField,
		LucidClientSecretField,
		LucidRefreshTokenField,
		ExcludeShortcutsField,
	}

	// FieldRelationships defines relationships between the fields.
	FieldRelationships = []field.SchemaFieldRelationship{}

	// Config is the configuration schema for the connector.
	Config = field.NewConfiguration(
		ConfigurationFields,
		field.WithConstraints(FieldRelationships...),
		field.WithConnectorDisplayName("Lucidchart"),
		field.WithHelpUrl("/docs/baton/lucidchart"),
		field.WithIconUrl("/static/app-icons/lucidchart.svg"),
	)
)

// ValidateConfig is run after the configuration is loaded, and should return an
// error if it isn't valid. Implementing this function is optional, it only
// needs to perform extra validations that cannot be encoded with configuration
// parameters.
func ValidateConfig(c *Lucidchart) error {
	return nil
}
