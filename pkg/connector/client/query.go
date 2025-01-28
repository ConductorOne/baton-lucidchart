package client

import (
	"context"
	"net/http"
)

var (
	GetUsersPath = "/users"
)

func (c *LucidchartClient) ListUser(ctx context.Context, pageToken string) ([]User, error) {
	var response []User

	req, err := c.newRequest(ctx, LucidchartApiUrl, http.MethodGet, GetUsersPath, nil)
	if err != nil {
		return nil, err
	}

	addPageToken(req, pageToken)

	err = c.doRequest(ctx, req, &response, false)
	if err != nil {
		return nil, err
	}

	return response, nil
}
