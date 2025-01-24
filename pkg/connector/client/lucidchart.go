package client

import (
	"context"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"net/http"
	"net/url"
)

var LucidchartApiFedRampUrl = "https://api.lucidgov.app"
var LucidchartApiUrl = "https://api.lucid.app"

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

func (c *LucidchartClient) doRequest(ctx context.Context, method string, urlAddress *url.URL, res interface{}, body interface{}) error {
	var (
		resp *http.Response
		err  error
	)

	req, err := c.client.NewRequest(
		ctx,
		method,
		urlAddress,
		uhttp.WithBearerToken(c.apiKey),
		uhttp.WithJSONBody(body),
	)
	if err != nil {
		return err
	}

	var options []uhttp.DoOption

	if res != nil {
		options = append(options, uhttp.WithResponse(&res))
	}

	resp, err = c.client.Do(req, options...)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
