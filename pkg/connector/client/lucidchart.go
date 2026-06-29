package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"golang.org/x/oauth2"
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

// LucidScimUrl is the default SCIM 2.0 base URL. SCIM is a separate surface
// from the REST API: a different host, a separate (Enterprise-tier) bearer
// token, and SCIM 2.0 JSON bodies. It is the official user-deprovisioning path.
var LucidScimUrl ClientUrl = "https://users.lucid.app/scim/v2"

type LucidchartClient struct {
	client      *uhttp.BaseHttpClient
	tokenSource oauth2.TokenSource
	apiKey      string
	baseURL     string
	// scimToken is the separate Enterprise SCIM bearer token. When empty the
	// SCIM deprovisioning operations (deactivate/delete) are unavailable.
	scimToken string
	// scimBaseURL is the SCIM 2.0 base URL. Defaults to LucidScimUrl.
	scimBaseURL string
}

func NewLucidchartClient(ctx context.Context, apiKey string, tokenSource oauth2.TokenSource, baseURL, scimToken, scimBaseURL string) (*LucidchartClient, error) {
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return nil, err
	}

	uhttpClient, err := uhttp.NewBaseHttpClientWithContext(ctx, httpClient)
	if err != nil {
		return nil, err
	}

	if baseURL == "" {
		baseURL = string(LucidchartApiUrl)
	}

	if scimBaseURL == "" {
		scimBaseURL = string(LucidScimUrl)
	}

	return &LucidchartClient{
		client:      uhttpClient,
		tokenSource: tokenSource,
		apiKey:      apiKey,
		baseURL:     baseURL,
		scimToken:   scimToken,
		scimBaseURL: scimBaseURL,
	}, nil
}

// ScimConfigured reports whether a SCIM bearer token was supplied. The SCIM
// deprovisioning operations require Lucid Enterprise tier and a separate token.
func (c *LucidchartClient) ScimConfigured() bool {
	return c.scimToken != ""
}

func (c *LucidchartClient) newRequest(
	ctx context.Context,
	method string,
	path string,
	body interface{},
	authType LucidAuthType,
) (*http.Request, error) {
	urlAddress, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}

	urlAddress = urlAddress.JoinPath(path)

	var accessToken string

	switch authType {
	case LucidAuthTypeOAuth2:
		token, err := c.tokenSource.Token()
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
) (string, error) {
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
