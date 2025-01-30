package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var UserFolderRoles = []string{
	"owner",
	"editandshare",
	"edit",
	"comment",
	"view",
}

type LucidAuthType string

const (
	LucidAuthTypeOAuth2 LucidAuthType = "OAUTH2"
	LucidAuthTypeApiKey LucidAuthType = "API_KEY"
)

type ClientUrl string

var LucidchartApiFedRampUrl ClientUrl = "https://api.lucidgov.app"
var LucidchartApiUrl ClientUrl = "https://api.lucid.co"

type LucidchartClient struct {
	client         *uhttp.BaseHttpClient
	lucidCharToken *LucidChartOAuth2
	apiKey         string
}

func NewLucidchartClient(ctx context.Context, apiKey string, opts *LucidChartOAuth2Options) (*LucidchartClient, error) {
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
		apiKey:         apiKey,
	}, nil
}

func (c *LucidchartClient) newRequest(
	ctx context.Context,
	clientUrl ClientUrl,
	method string,
	path string,
	body interface{},
	authType LucidAuthType,
) (*http.Request, error) {
	urlAddress, err := url.Parse(string(clientUrl))
	if err != nil {
		return nil, err
	}

	urlAddress = urlAddress.JoinPath(path)

	var accessToken string

	switch authType {
	case LucidAuthTypeOAuth2:
		token, err := c.lucidCharToken.GetToken(ctx)
		if err != nil {
			return nil, err
		}
		accessToken = token.AccessToken

	case LucidAuthTypeApiKey:
		accessToken = c.apiKey
	}

	options := []uhttp.RequestOption{
		uhttp.WithBearerToken(accessToken),
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
) (string, error) {
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
				return "", errors.Join(err, errToken)
			}

			l.Debug("Retrying request with new token", zap.String("token", token.AccessToken))

			req.Header.Set("Authorization", token.AccessToken)

			return c.doRequest(ctx, req, res, true)
		}

		return "", err
	}
	defer resp.Body.Close()

	nextToken := resp.Header.Get("Link")

	if nextToken != "" {
		nextToken, err = extractPageToken(nextToken)
		if err != nil {
			return "", errors.Join(err, errors.New("failed to extract page token"))
		}

		return nextToken, nil
	}

	return "", nil
}

func extractPageToken(token string) (string, error) {
	splitResult := strings.Split(token, ";")

	if len(splitResult) < 2 {
		return "", errors.New("expected two parts in the token")
	}

	value := strings.Trim(strings.TrimSpace(splitResult[0]), "<> ")

	valueUrl, err := url.Parse(value)
	if err != nil {
		return "", err
	}

	query := valueUrl.Query()
	pageToken := query.Get("pageToken")

	return pageToken, nil
}

func addPageToken(req *http.Request, pageToken string) {
	if pageToken != "" {
		query := req.URL.Query()
		query.Add("pageToken", pageToken)

		req.URL.RawQuery = query.Encode()
	}
}
