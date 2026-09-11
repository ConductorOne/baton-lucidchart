package client

import (
	"context"
	"errors"
	"fmt"
	"net"
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
//
// Lucid runs two SCIM integrations — "SCIM for admin management" (organizational
// groups) and "SCIM for content access" (teams) — behind this single base URL.
// The bearer token alone decides which integration a request reaches.
// https://developer.lucid.co/reference/overview-scim
var LucidScimUrl ClientUrl = "https://users.lucid.app/scim/v2"

type LucidchartClient struct {
	client      *uhttp.BaseHttpClient
	tokenSource oauth2.TokenSource
	apiKey      string
	baseURL     string
	// scimToken is the separate Enterprise SCIM bearer token for the "SCIM for
	// admin management" integration. When empty the SCIM deprovisioning
	// operations (deactivate/delete) are unavailable.
	scimToken string
	// contentScimToken is the bearer token for the second SCIM integration,
	// "SCIM for content access" (teams). Optional and independent of scimToken:
	// same base URL, different integration. When empty, content-access
	// deprovisioning is skipped.
	contentScimToken string
	// scimBaseURL is the SCIM 2.0 base URL, shared by both integrations.
	// Defaults to LucidScimUrl.
	scimBaseURL string
}

func NewLucidchartClient(
	ctx context.Context,
	apiKey string,
	tokenSource oauth2.TokenSource,
	baseURL, scimToken, scimBaseURL, contentScimToken string,
) (*LucidchartClient, error) {
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
	} else if err := validateScimBaseURL(scimBaseURL); err != nil {
		return nil, err
	}

	return &LucidchartClient{
		client:           uhttpClient,
		tokenSource:      tokenSource,
		apiKey:           apiKey,
		baseURL:          baseURL,
		scimToken:        scimToken,
		contentScimToken: contentScimToken,
		scimBaseURL:      scimBaseURL,
	}, nil
}

// validateScimBaseURL checks a caller-supplied SCIM base URL at construction
// time. scim-base-url is customer-settable — a FedRAMP/GovSuite tenant must
// enter the account-specific URL by hand — and the SCIM surface is where the
// Enterprise bearer tokens travel, so a cleartext value would put them on the
// wire in the clear.
//
// url.Parse is far too permissive to serve as the check on its own: it accepts
// "http://users.lucid.app/scim/v2" without comment, and it accepts a schemeless
// "users.lucid.app/scim/v2" as a relative path, which only fails much later and
// far less legibly when a request is built from it. Rejecting both here turns a
// typo into a config error naming the flag rather than a mid-sync surprise.
func validateScimBaseURL(scimBaseURL string) error {
	parsed, err := url.Parse(scimBaseURL)
	if err != nil {
		return fmt.Errorf("baton-lucidchart: scim-base-url %q is not a valid URL: %w", scimBaseURL, err)
	}

	// http is tolerated for loopback only, where the cleartext never leaves the
	// machine and no observer can reach it. That keeps the constructor — and
	// therefore its validation — exercisable against a local test server.
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}

	if parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf(
			"baton-lucidchart: scim-base-url must be an absolute https:// URL (for example %s), got %q",
			LucidScimUrl,
			scimBaseURL,
		)
	}

	return nil
}

// isLoopbackHost reports whether host names the local machine.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// ScimConfigured reports whether the admin-management SCIM bearer token was
// supplied. The SCIM deprovisioning operations require Lucid Enterprise tier
// and a separate token.
//
// The content-access integration is an optional add-on, not a substitute: every
// SCIM operation the connector performs still goes through the admin-management
// token first, so this remains the precondition for all of them.
func (c *LucidchartClient) ScimConfigured() bool {
	return c.scimToken != ""
}

// ContentScimConfigured reports whether the second SCIM integration ("SCIM for
// content access", which syncs to teams) has a bearer token. When true, a user
// delete is also propagated to that integration.
func (c *LucidchartClient) ContentScimConfigured() bool {
	return c.contentScimToken != ""
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
	var options []uhttp.DoOption

	if res != nil {
		options = append(options, uhttp.WithResponse(&res))
	}

	return c.doRequestWithOptions(ctx, req, options...)
}

// doRequestWithOptions is doRequest with the response handling left to the
// caller, for surfaces whose bodies need decoding rules uhttp.WithResponse does
// not cover (see scimUserResponse).
func (c *LucidchartClient) doRequestWithOptions(
	ctx context.Context,
	req *http.Request,
	options ...uhttp.DoOption,
) (string, error) {
	var (
		resp *http.Response
		err  error
	)

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
