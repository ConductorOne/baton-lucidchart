package main

import (
	"context"
	"fmt"
	"os"

	"github.com/conductorone/baton-lucidchart/pkg/connector"
	"github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/conductorone/baton-sdk/pkg/types"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

var version = "dev"

func main() {
	ctx := context.Background()

	_, cmd, err := config.DefineConfiguration(
		ctx,
		"baton-lucidchart",
		getConnector,
		field.Configuration{
			Fields: ConfigurationFields,
		},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	cmd.Version = version

	err = cmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func getConnector(ctx context.Context, v *viper.Viper) (types.ConnectorServer, error) {
	l := ctxzap.Extract(ctx)
	if err := ValidateConfig(v); err != nil {
		return nil, err
	}

	apiKey := v.GetString(LucidApiKeyField.FieldName)
	clientID := v.GetString(LucidClientIdField.FieldName)
	clientSecret := v.GetString(LucidClientSecretField.FieldName)
	refreshToken := v.GetString(LucidRefreshTokenField.FieldName)
	excludeShortcuts := v.GetBool(ExcludeShortcutsField.FieldName)
	baseURL := v.GetString(BaseURLField.FieldName)

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

	cb, err := connector.New(ctx, apiKey, tokenSource, excludeShortcuts, baseURL)
	if err != nil {
		l.Error("error creating connector", zap.Error(err))
		return nil, err
	}
	connector, err := connectorbuilder.NewConnector(ctx, cb)
	if err != nil {
		l.Error("error creating connector", zap.Error(err))
		return nil, err
	}
	return connector, nil
}
