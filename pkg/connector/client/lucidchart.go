package client

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"net/http"
	"net/url"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ClientUrl string

var LucidchartApiFedRampUrl ClientUrl = "https://api.lucidgov.app"
var LucidchartApiUrl ClientUrl = "https://api.lucid.app"

type LucidchartClient struct {
	client         *uhttp.BaseHttpClient
	lucidCharToken *LucidChartOAuth2
}

func NewLucidchartClient(ctx context.Context, opts *LucidChartOAuth2Options) (*LucidchartClient, error) {
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return nil, err
	}

	uhttpClient, err := uhttp.NewBaseHttpClientWithContext(ctx, httpClient)
	if err != nil {
		return nil, err
	}

	lucidCharToken, err := NewLucidChartOAuth2(ctx, opts)
	if err != nil {
		return nil, err
	}

	return &LucidchartClient{
		client:         uhttpClient,
		lucidCharToken: lucidCharToken,
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

	token, err := c.lucidCharToken.GetToken(ctx)
	if err != nil {
		return nil, err
	}

	options := []uhttp.RequestOption{
		uhttp.WithBearerToken(token.AccessToken),
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
	isRetryToken bool,
) error {
	l := ctxzap.Extract(ctx)

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
		if !isRetryToken && status.Code(err) == codes.Unauthenticated {
			token, errToken := c.lucidCharToken.GetToken(ctx)
			if errToken != nil {
				return errors.Join(err, errToken)
			}

			l.Debug("Retrying request with new token", zap.String("token", token.AccessToken))

			req.Header.Set("Authorization", token.AccessToken)

			return c.doRequest(ctx, req, res, true)
		}

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
