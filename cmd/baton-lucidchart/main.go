package main

import (
	"context"

	"github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorrunner"
	cfg "github.com/conductorone/baton-lucidchart/pkg/config"
	"github.com/conductorone/baton-lucidchart/pkg/connector"
)

var version = "dev"

func main() {
	ctx := context.Background()
	config.RunConnector(ctx,
		"baton-lucidchart",
		version,
		cfg.Config,
		connector.New,
		connectorrunner.WithDefaultCapabilitiesConnectorBuilder(&connector.Connector{}),
	)
}
