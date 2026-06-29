package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	LucidApiKeyField = field.StringField(
		"lucid-api-key",
		field.WithDisplayName("Lucidchart API Key"),
		field.WithDescription("The API key for the Lucidchart API."),
		field.WithRequired(true),
		field.WithIsSecret(true),
	)

	LucidOAuthField = field.Oauth2Field(
		"oauth2",
		field.WithDisplayName("Lucidchart OAuth2 Token"),
		field.WithDescription("The OAuth2 token for the Lucidchart API."),
	)

	LucidClientIdField = field.StringField(
		"lucid-client-id",
		field.WithDisplayName("Lucidchart Client ID"),
		field.WithDescription("The OAuth2 client ID for the Lucidchart API."),
	)

	LucidClientSecretField = field.StringField(
		"lucid-client-secret",
		field.WithDisplayName("Lucidchart Client Secret"),
		field.WithDescription("The OAuth2 client secret for the Lucidchart API."),
		field.WithIsSecret(true),
	)

	LucidRefreshTokenField = field.StringField(
		"lucid-refresh-token",
		field.WithDisplayName("Lucidchart Refresh Token"),
		field.WithDescription("The OAuth2 refresh token for the Lucidchart API."),
		field.WithExportTarget(field.ExportTargetCLIOnly),
		field.WithHidden(true),
		field.WithIsSecret(true),
	)

	ExcludeShortcutsField = field.BoolField(
		"exclude-shortcuts",
		field.WithDisplayName("Exclude Shortcuts"),
		field.WithDescription("Exclude shortcut documents and folders"),
		field.WithDefaultValue(false),
	)

	BaseURLField = field.StringField(
		"base-url",
		field.WithDescription("Override the Lucidchart API URL (for testing)"),
		field.WithHidden(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	LucidScimTokenField = field.StringField(
		"lucid-scim-token",
		field.WithDisplayName("Lucidchart SCIM Token"),
		field.WithDescription("The SCIM 2.0 bearer token for user deprovisioning (deactivate/delete). Requires Lucid Enterprise tier. Optional: sync and account creation work without it."),
		field.WithIsSecret(true),
	)

	ScimBaseURLField = field.StringField(
		"scim-base-url",
		field.WithDescription("Override the Lucid SCIM base URL (for testing)"),
		field.WithHidden(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	LucidContentTransferUserIdField = field.StringField(
		"lucid-content-transfer-user-id",
		field.WithDisplayName("Content Transfer User ID"),
		field.WithDescription("User ID to transfer owned documents to before deleting a user. When set, a user delete first transfers their content to this user so it is retained."),
	)

	ConfigurationFields = []field.SchemaField{
		LucidApiKeyField,
		LucidOAuthField,
		LucidClientIdField,
		LucidClientSecretField,
		LucidRefreshTokenField,
		ExcludeShortcutsField,
		BaseURLField,
		LucidScimTokenField,
		ScimBaseURLField,
		LucidContentTransferUserIdField,
	}
)

//go:generate go run ./gen
var Config = field.NewConfiguration(
	ConfigurationFields,
	field.WithConnectorDisplayName("Lucidchart"),
	field.WithIconUrl("/static/app-icons/lucidchart.svg"),
	field.WithHelpUrl("/docs/baton/lucidchart"),
)
