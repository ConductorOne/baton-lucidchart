package client

import (
	"context"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

type LucidchartClient struct {
	client *uhttp.BaseHttpClient
	apiKey string
}

func NewLucidchartClient(ctx context.Context, apiKey string) (*LucidchartClient, error) {
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return nil, err
	}

	uhttpClient, err := uhttp.NewBaseHttpClientWithContext(ctx, httpClient)
	if err != nil {
		return nil, err
	}

	return &LucidchartClient{
		client: uhttpClient,
		apiKey: apiKey,
	}, nil
}
