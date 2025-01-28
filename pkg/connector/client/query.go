package client

import (
	"context"
	"fmt"
	"net/http"
)

var (
	GetUsersPath          = "/users"
	RootFolderContentPath = "/folders/root/contents"
	FolderContentPath     = "/folders/%s/contents"
)

func (c *LucidchartClient) ListUser(ctx context.Context, pageToken string) ([]User, string, error) {
	var response []User

	req, err := c.newRequest(ctx, LucidchartApiUrl, http.MethodGet, GetUsersPath, nil, LucidAuthTypeOAuth2)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response, false)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}

func (c *LucidchartClient) RootFolderContent(ctx context.Context, pageToken string) ([]FolderContent, string, error) {
	var response []FolderContent

	req, err := c.newRequest(ctx, LucidchartApiUrl, http.MethodGet, RootFolderContentPath, nil, LucidAuthTypeApiKey)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response, false)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}

func (c *LucidchartClient) FolderContent(ctx context.Context, folderId string, pageToken string) ([]FolderContent, string, error) {
	var response []FolderContent

	path := fmt.Sprintf(FolderContentPath, folderId)

	req, err := c.newRequest(ctx, LucidchartApiUrl, http.MethodGet, path, nil, LucidAuthTypeApiKey)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response, false)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}
