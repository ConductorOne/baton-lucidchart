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

	// Single-collaborator paths are shared across the GET (read current role),
	// PUT (upsert role), and DELETE (revoke) operations — the HTTP method at
	// each call site conveys the verb, so one constant per resource suffices.
	FolderUserCollaboratorPath   = "/folders/%s/shares/users/%s"
	DocumentUserCollaboratorPath = "/documents/%s/shares/users/%s"
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

// GetFolderUserCollaborator fetches a single user's current collaborator role on
// a folder via GET /folders/{id}/shares/users/{userId}. Lucid returns 404 (mapped
// to codes.NotFound) when the user is not a direct collaborator. Callers treat
// this read as best-effort and fall through to the upsert on any error.
func (c *LucidchartClient) GetFolderUserCollaborator(ctx context.Context, folderId, userId string) (*FolderUserCollaboration, error) {
	var response FolderUserCollaboration

	path := fmt.Sprintf(FolderUserCollaboratorPath, folderId, userId)

	req, err := c.newRequest(ctx, http.MethodGet, path, nil, LucidAuthTypeApiKey)
	if err != nil {
		return nil, err
	}

	_, err = c.doRequest(ctx, req, &response)
	if err != nil {
		return nil, err
	}

	return &response, nil
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

// GetDocumentUserCollaborator fetches a single user's current collaborator role
// on a document via GET /documents/{id}/shares/users/{userId}. Lucid returns 404
// (mapped to codes.NotFound) when the user is not a direct collaborator. Callers
// treat this read as best-effort and fall through to the upsert on any error.
func (c *LucidchartClient) GetDocumentUserCollaborator(ctx context.Context, documentId, userId string) (*DocumentUserCollaboration, error) {
	var response DocumentUserCollaboration

	path := fmt.Sprintf(DocumentUserCollaboratorPath, documentId, userId)

	req, err := c.newRequest(ctx, http.MethodGet, path, nil, LucidAuthTypeApiKey)
	if err != nil {
		return nil, err
	}

	_, err = c.doRequest(ctx, req, &response)
	if err != nil {
		return nil, err
	}

	return &response, nil
}
