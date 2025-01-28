package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/spf13/viper"
)

var (
	LucidApiKeyField = field.StringField(
		"lucid-api-key",
		field.WithDescription("The API key for the Lucidchart API."),
		field.WithRequired(true),
	)

	LucidCodeKeyField = field.StringField(
		"lucid-code",
		field.WithDescription("The code key for the Lucidchart API."),
		field.WithRequired(false),
	)

	LucidClientIdField = field.StringField(
		"lucid-client-id",
		field.WithDescription("The client ID for the Lucidchart API."),
		field.WithRequired(true),
	)

	LucidClientSecretField = field.StringField(
		"lucid-client-secret",
		field.WithDescription("The client secret for the Lucidchart API."),
		field.WithRequired(true),
	)

	LucidRedirectUrlField = field.StringField(
		"lucid-redirect-url",
		field.WithDescription("The redirect URL for the Lucidchart API."),
		field.WithRequired(true),
	)

	LucidRefreshTokenField = field.StringField(
		"lucid-refresh-token",
		field.WithDescription("The refresh token for the Lucidchart API."),
		field.WithRequired(false),
	)

	// ConfigurationFields defines the external configuration required for the
	// connector to run. Note: these fields can be marked as optional or
	// required.
	ConfigurationFields = []field.SchemaField{
		LucidApiKeyField,
		LucidCodeKeyField,
		LucidClientIdField,
		LucidClientSecretField,
		LucidRedirectUrlField,
		LucidRefreshTokenField,
	}

	// FieldRelationships defines relationships between the fields listed in
	// ConfigurationFields that can be automatically validated. For example, a
	// username and password can be required together, or an access token can be
	// marked as mutually exclusive from the username password pair.
	FieldRelationships = []field.SchemaFieldRelationship{}
)

// ValidateConfig is run after the configuration is loaded, and should return an
// error if it isn't valid. Implementing this function is optional, it only
// needs to perform extra validations that cannot be encoded with configuration
// parameters.
func ValidateConfig(v *viper.Viper) error {
	return nil
}
