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

	// LucidContentScimTokenField is the bearer token for Lucid's *second* SCIM
	// integration. Lucid ships two: "SCIM for admin management" (organizational
	// groups) and "SCIM for content access" (teams). Both are served from the
	// same base URL — the token alone selects which integration a call hits.
	LucidContentScimTokenField = field.StringField(
		"lucid-content-scim-token",
		field.WithDisplayName("Lucidchart Content Access SCIM Token"),
		field.WithDescription("The SCIM 2.0 bearer token for Lucid's \"SCIM for content access\" integration, which syncs to teams. "+
			"This is a second, separate token from lucid-scim-token (the \"SCIM for admin management\" integration, which syncs to "+
			"organizational groups); both integrations share the same SCIM base URL and are distinguished only by the token. "+
			"Optional: when set, deleting a user also deprovisions them from the content-access integration."),
		field.WithIsSecret(true),
	)

	ScimBaseURLField = field.StringField(
		"scim-base-url",
		field.WithDescription("Override the Lucid SCIM base URL (for testing)"),
		field.WithHidden(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	LucidContentTransferUserEmailField = field.StringField(
		"lucid-content-transfer-user-email",
		field.WithDisplayName("Content Transfer User Email"),
		field.WithDescription("Email address of the user to transfer owned documents to before "+
			"deleting a user. When set, a user delete first transfers their content to this user "+
			"so it is retained. Must be an email address — the Lucid transferUserContent API "+
			"requires email, not a numeric user ID."),
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
		LucidContentScimTokenField,
		ScimBaseURLField,
		LucidContentTransferUserEmailField,
	}
)

//go:generate go run ./gen
var Config = field.NewConfiguration(
	ConfigurationFields,
	field.WithConnectorDisplayName("Lucidchart"),
	field.WithIconUrl("/static/app-icons/lucidchart.svg"),
	field.WithHelpUrl("/docs/baton/lucidchart"),
)
