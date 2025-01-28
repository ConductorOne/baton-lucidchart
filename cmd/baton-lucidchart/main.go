package main

import (
	"context"
	"fmt"
	config2 "github.com/conductorone/baton-lucidchart/cmd/baton-lucidchart/config"
	"os"

	"github.com/conductorone/baton-lucidchart/pkg/connector"
	"github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/conductorone/baton-sdk/pkg/types"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var version = "dev"

func main() {
	ctx := context.Background()

	_, cmd, err := config.DefineConfiguration(
		ctx,
		"baton-lucidchart",
		getConnector,
		field.Configuration{
			Fields: config2.ConfigurationFields,
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
	if err := config2.ValidateConfig(v); err != nil {
		return nil, err
	}

	apiKey := v.GetString(config2.LucidApiKeyField.FieldName)
	code := v.GetString(config2.LucidCodeKeyField.FieldName)
	clientID := v.GetString(config2.LucidClientIdField.FieldName)
	clientSecret := v.GetString(config2.LucidClientSecretField.FieldName)
	redirectURL := v.GetString(config2.LucidRedirectUrlField.FieldName)
	refreshToken := v.GetString(config2.LucidRefreshTokenField.FieldName)

	cb, err := connector.New(ctx, apiKey, code, clientID, clientSecret, redirectURL, refreshToken)
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
