package client

import (
	"context"
	"encoding/json"
	"errors"
	"go.uber.org/zap"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

type ValidationError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	ErrorUri         string `json:"error_uri"`
}

type LucidChartOAuth2Options struct {
	Code string

	// ClientID is the application's ID.
	ClientID string

	// ClientSecret is the application's secret.
	ClientSecret string

	// RedirectUrl is the URL to redirect to after the user has authenticated.
	RedirectUrl string

	// RefreshToken is the last refresh token to use to get a new access token.
	RefreshToken string
}

type LucidChartOAuth2 struct {
	client *uhttp.BaseHttpClient
	opts   *LucidChartOAuth2Options

	tokenMutex sync.RWMutex
	token      *GetTokenResponse
}

func NewLucidChartOAuth2(ctx context.Context, opts *LucidChartOAuth2Options) (*LucidChartOAuth2, error) {
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return nil, err
	}

	uhttpClient, err := uhttp.NewBaseHttpClientWithContext(ctx, httpClient)
	if err != nil {
		return nil, err
	}

	return &LucidChartOAuth2{
		client:     uhttpClient,
		opts:       opts,
		tokenMutex: sync.RWMutex{},
		token:      nil,
	}, nil
}

type GetTokenResponse struct {
	AccessToken  string   `json:"access_token"`
	ClientId     string   `json:"client_id"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int      `json:"expires_in"`
	Expires      int64    `json:"expires"`
	Scope        string   `json:"scope"`
	Scopes       []string `json:"scopes"`
	TokenType    string   `json:"token_type"`
	AccountId    int      `json:"accountId"`
}

func (t *GetTokenResponse) Expired() bool {
	data := time.UnixMilli(t.Expires).UTC()

	return time.Now().UTC().After(data)
}

func (c *LucidChartOAuth2) GetToken(ctx context.Context) (*GetTokenResponse, error) {
	c.tokenMutex.Lock()
	defer c.tokenMutex.Unlock()
	l := ctxzap.Extract(ctx)

	l.Info("Getting token")

	if c.token != nil {
		if c.token.Expired() {
			token, err := c.refreshToken(ctx)
			if err != nil {
				return nil, err
			}

			c.token = token
		}
		return c.token, nil
	}

	if c.token == nil && c.opts.RefreshToken != "" {
		token, err := c.refreshToken(ctx)
		if err != nil {
			return nil, err
		}

		c.token = token
		return c.token, nil
	}

	if c.opts.Code == "" {
		return nil, errors.New("baton-lucidchart: no code found to generate token")
	}

	type Body struct {
		Code         string `json:"code"`
		ClientId     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		GrantType    string `json:"grant_type"`
		RedirectURI  string `json:"redirect_uri"`
	}

	body := Body{
		Code:         c.opts.Code,
		ClientId:     c.opts.ClientID,
		ClientSecret: c.opts.ClientSecret,
		GrantType:    "authorization_code",
		RedirectURI:  c.opts.RedirectUrl,
	}

	endPoint, err := url.Parse("https://api.lucid.co/oauth2/token")
	if err != nil {
		return nil, err
	}

	var respVar GetTokenResponse
	req, err := c.client.NewRequest(
		ctx,
		http.MethodPost,
		endPoint,
		uhttp.WithJSONBody(body),
		uhttp.WithAcceptJSONHeader(),
	)

	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req, uhttp.WithResponse(&respVar))
	if err != nil {
		return nil, parseLucidChartResponseError(resp, err)
	}

	defer resp.Body.Close()

	c.token = &respVar

	l.Debug("Token received", zap.Any("token", c.token))

	return &respVar, nil
}

func (c *LucidChartOAuth2) refreshToken(ctx context.Context) (*GetTokenResponse, error) {
	l := ctxzap.Extract(ctx)

	l.Info("Getting refresh token")

	var resfreshToken string

	if c.token != nil {
		resfreshToken = c.token.AccessToken
	} else {
		resfreshToken = c.opts.RefreshToken
	}

	if resfreshToken == "" {
		return nil, errors.New("baton-lucidchart: no refresh token found")
	}

	type Body struct {
		RefreshToken string `json:"refresh_token"`
		ClientId     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		GrantType    string `json:"grant_type"`
	}

	body := Body{
		RefreshToken: resfreshToken,
		ClientId:     c.opts.ClientID,
		ClientSecret: c.opts.ClientSecret,
		GrantType:    "refresh_token",
	}

	endPoint, err := url.Parse("https://api.lucid.co/oauth2/token")
	if err != nil {
		return nil, err
	}

	var respVar GetTokenResponse
	req, err := c.client.NewRequest(
		ctx,
		http.MethodPost,
		endPoint,
		uhttp.WithJSONBody(body),
		uhttp.WithAcceptJSONHeader(),
	)

	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req, uhttp.WithResponse(&respVar))
	if err != nil {
		return nil, parseLucidChartResponseError(resp, err)
	}

	defer resp.Body.Close()

	l.Debug("Refresh token received", zap.Any("token", respVar))

	return &respVar, nil
}

func parseLucidChartResponseError(resp *http.Response, err error) error {
	if resp != nil && resp.StatusCode == http.StatusBadRequest {
		defer resp.Body.Close()
		var validationError ValidationError
		errJson := json.NewDecoder(resp.Body).Decode(&validationError)
		if errJson != nil {
			return errors.Join(err, errJson)
		}

		return errors.Join(err, errors.New(validationError.ErrorDescription))

	}

	return err
}
