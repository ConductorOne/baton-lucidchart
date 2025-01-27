package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

type LucidChartOAuth2Options struct {
	Code string

	// ClientID is the application's ID.
	ClientID string

	// ClientSecret is the application's secret.
	ClientSecret string

	RedirectUrl string
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
		RedirectURI:  "http://localhost:8080",
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
		return nil, err
	}

	resp.Body.Close()

	return &respVar, nil
}

func (c *LucidChartOAuth2) refreshToken(ctx context.Context) (*GetTokenResponse, error) {
	if c.token == nil {
		return nil, errors.New("baton-lucidchart: no refresh token found")
	}

	type Body struct {
		RefreshToken string `json:"refresh_token"`
		ClientId     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		GrantType    string `json:"grant_type"`
	}

	body := Body{
		RefreshToken: c.token.RefreshToken,
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
		return nil, err
	}

	resp.Body.Close()

	return &respVar, nil
}
