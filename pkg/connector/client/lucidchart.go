package client

import (
	"context"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"net/http"
	"net/url"
)

type ClientUrl string

var LucidchartApiFedRampUrl ClientUrl = "https://api.lucidgov.app"
var LucidchartApiUrl ClientUrl = "https://api.lucid.app"

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

func (c *LucidchartClient) newRequest(
	ctx context.Context,
	clientUrl ClientUrl,
	method string,
	path string,
	body interface{},
) (*http.Request, error) {
	urlAddress, err := url.Parse(string(clientUrl))
	if err != nil {
		return nil, err
	}

	urlAddress = urlAddress.JoinPath(path)

	options := []uhttp.RequestOption{
		uhttp.WithBearerToken(c.apiKey),
		uhttp.WithHeader("Lucid-Api-Version", "1"),
		uhttp.WithAcceptJSONHeader(),
	}

	if body != nil {
		options = append(options, uhttp.WithJSONBody(body))
	}

	req, err := c.client.NewRequest(
		ctx,
		method,
		urlAddress,
		options...,
	)
	if err != nil {
		return nil, err
	}

	return req, nil
}

func (c *LucidchartClient) doRequest(
	ctx context.Context,
	req *http.Request,
	res interface{},
) error {
	var (
		resp *http.Response
		err  error
	)

	var options []uhttp.DoOption

	if res != nil {
		options = append(options, uhttp.WithResponse(&res))
	}

	resp, err = c.client.Do(req.WithContext(ctx), options...)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func addPageToken(req *http.Request, pageToken string) {
	if pageToken != "" {
		query := req.URL.Query()
		query.Add("pageToken", pageToken)

		req.URL.RawQuery = query.Encode()
	}
}
