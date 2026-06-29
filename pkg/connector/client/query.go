package client

import (
	"context"
	"fmt"
	"net/http"
)

var (
	GetUsersPath                      = "/users"
	GetUserPath                       = "/v1/users/%s"
	RootFolderContentPath             = "/folders/root/contents"
	FolderContentPath                 = "/folders/%s/contents"
	ListFolderUserCollaboratorsPath   = "/folders/%s/shares/users"
	ListDocumentUserCollaboratorsPath = "/documents/%s/shares/users"
	UpsertFolderUserCollaboratorPath  = "/folders/%s/shares/users/%s"
	DeleteFolderUserCollaboratorPath  = "/folders/%s/shares/users/%s"

	UpsertDocumentUserCollaboratorPath = "/documents/%s/shares/users/%s"
	DeleteDocumentUserCollaboratorPath = "/documents/%s/shares/users/%s"

	GetSubscriptionsPath = "/subscriptions"
	GetLicensesPath      = "/licenses"
)

// GetUser fetches a single user by their numeric REST user ID via
// GET /v1/users/{id}. Called before TransferContent to resolve the user's
// email address (the Lucid transferUserContent API requires email, not ID).
func (c *LucidchartClient) GetUser(ctx context.Context, userID string) (*User, error) {
	path := fmt.Sprintf(GetUserPath, userID)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, LucidAuthTypeOAuth2)
	if err != nil {
		return nil, fmt.Errorf("baton-lucidchart: get user %s: %w", userID, err)
	}
	var u User
	if _, err := c.doRequest(ctx, req, &u); err != nil {
		return nil, fmt.Errorf("baton-lucidchart: get user %s: %w", userID, err)
	}
	return &u, nil
}

func (c *LucidchartClient) ListUser(ctx context.Context, pageToken string) ([]User, string, error) {
	var response []User

	req, err := c.newRequest(ctx, http.MethodGet, GetUsersPath, nil, LucidAuthTypeOAuth2)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}

func (c *LucidchartClient) RootFolderContent(ctx context.Context, pageToken string) ([]FolderContent, string, error) {
	var response []FolderContent

	req, err := c.newRequest(ctx, http.MethodGet, RootFolderContentPath, nil, LucidAuthTypeApiKey)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}

func (c *LucidchartClient) FolderContent(ctx context.Context, folderId string, pageToken string) ([]FolderContent, string, error) {
	var response []FolderContent

	path := fmt.Sprintf(FolderContentPath, folderId)

	req, err := c.newRequest(ctx, http.MethodGet, path, nil, LucidAuthTypeApiKey)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}

func (c *LucidchartClient) ListFolderUserCollaborators(ctx context.Context, folderId string, pageToken string) ([]FolderUserCollaboration, string, error) {
	var response []FolderUserCollaboration

	path := fmt.Sprintf(ListFolderUserCollaboratorsPath, folderId)

	req, err := c.newRequest(ctx, http.MethodGet, path, nil, LucidAuthTypeApiKey)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}

func (c *LucidchartClient) ListDocumentUserCollaborators(ctx context.Context, documentId string, pageToken string) ([]DocumentUserCollaboration, string, error) {
	var response []DocumentUserCollaboration

	path := fmt.Sprintf(ListDocumentUserCollaboratorsPath, documentId)

	req, err := c.newRequest(ctx, http.MethodGet, path, nil, LucidAuthTypeApiKey)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}

func (c *LucidchartClient) ListSubscriptions(ctx context.Context, pageToken string) ([]Subscription, string, error) {
	var response []Subscription

	req, err := c.newRequest(ctx, http.MethodGet, GetSubscriptionsPath, nil, LucidAuthTypeOAuth2)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}

func (c *LucidchartClient) ListLicenses(ctx context.Context, pageToken string) ([]License, string, error) {
	var response []License

	req, err := c.newRequest(ctx, http.MethodGet, GetLicensesPath, nil, LucidAuthTypeOAuth2)
	if err != nil {
		return nil, "", err
	}

	addPageToken(req, pageToken)

	nextToken, err := c.doRequest(ctx, req, &response)
	if err != nil {
		return nil, "", err
	}

	return response, nextToken, nil
}
