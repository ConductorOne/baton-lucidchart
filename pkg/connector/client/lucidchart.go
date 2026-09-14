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
	"go.uber.org/zap"
	"golang.org/x/oauth2"
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
	// scimBaseURLErr records why a caller-supplied scim-base-url was rejected.
	// It disables the SCIM surface instead of failing construction: sync runs
	// entirely on the REST API and has no business dying because the separate
	// SCIM URL is wrong. Every SCIM request returns this instead.
	scimBaseURLErr error
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

	// A rejected scim-base-url disables SCIM rather than failing construction.
	// connector.New turns a constructor error into a dead connector, and sync
	// talks only to the REST API under base-url — so failing hard here would take
	// down read-only syncing over a setting it never reads. The tokens are still
	// protected: no SCIM request is built at all while this is set.
	var scimBaseURLErr error
	if scimBaseURL == "" {
		scimBaseURL = string(LucidScimUrl)
	} else if err := validateScimBaseURL(scimBaseURL); err != nil {
		// FailedPrecondition, not a bare error: this is a configuration problem,
		// and retrying it cannot help until scim-base-url is corrected. An
		// unclassified error reaches the platform as codes.Unknown and gets
		// retried forever — the same trap the content-access 401/403 branch in
		// users.go avoids.
		scimBaseURLErr = status.Error(codes.FailedPrecondition, err.Error())
		ctxzap.Extract(ctx).Warn(
			"baton-lucidchart: scim-base-url rejected; SCIM actions and deprovisioning are disabled, sync is unaffected",
			zap.Error(err),
		)
	}

	return &LucidchartClient{
		client:           uhttpClient,
		tokenSource:      tokenSource,
		apiKey:           apiKey,
		baseURL:          baseURL,
		scimToken:        scimToken,
		contentScimToken: contentScimToken,
		scimBaseURL:      scimBaseURL,
		scimBaseURLErr:   scimBaseURLErr,
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
// typo into an error naming the flag rather than a mid-sync surprise.
//
// A rejection disables SCIM rather than failing construction — see the
// scimBaseURLErr field. The protection is unchanged either way, because no SCIM
// request is built at all while it is set; what changes is that a wrong SCIM URL
// can no longer take down a sync that never reads it.
//
// The host itself is deliberately not constrained. Lucid generates a GovSuite
// tenant's SCIM hostname per account and publishes no list of them, so any
// allowlist would be a guess — and a guess that is wrong for one real customer
// rejects the exact FedRAMP URL this field exists to accept.
func validateScimBaseURL(scimBaseURL string) error {
	parsed, err := url.Parse(scimBaseURL)
	if err != nil {
		return fmt.Errorf("baton-lucidchart: scim-base-url %q is not a valid URL: %w", scimBaseURL, err)
	}

	// Loopback is tolerated, cleartext included: it never leaves the machine and
	// no observer — or impostor — can reach it. That keeps the constructor, and
	// therefore its validation, exercisable against a local test server.
	if isLoopbackHost(parsed.Hostname()) && (parsed.Scheme == "http" || parsed.Scheme == "https") {
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

// ScimBaseURLErr returns why the SCIM surface is disabled, or nil when it is
// usable. Callers that cause side effects before their first SCIM call must
// check this up front: ScimConfigured only reports that a token was supplied,
// and stays true when the SCIM URL was rejected.
func (c *LucidchartClient) ScimBaseURLErr() error {
	return c.scimBaseURLErr
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
