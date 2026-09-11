package client

import (
	"context"
	"fmt"
	"net/http"
)

func (c *LucidchartClient) UpsertFolderUserCollaborator(ctx context.Context, folderId, userId string, role string) (*FolderUserCollaboration, error) {
	var response FolderUserCollaboration

	path := fmt.Sprintf(FolderUserCollaboratorPath, folderId, userId)

	body := struct {
		Role string `json:"role"`
	}{
		Role: role,
	}

	req, err := c.newRequest(ctx, http.MethodPut, path, body, LucidAuthTypeApiKey)
	if err != nil {
		return &response, err
	}
	// Return response even on error: doRequest decodes the body into it before
	// checking the HTTP status, so on an error status (e.g. 409) it may already
	// hold the upstream record. It is zero-valued whenever nothing was decoded
	// (network error, non-JSON body), so callers must treat it as best-effort.
	_, err = c.doRequest(ctx, req, &response)
	if err != nil {
		return &response, err
	}

	return &response, nil
}

func (c *LucidchartClient) DeleteFolderUserCollaborator(ctx context.Context, folderId, userId string) error {
	path := fmt.Sprintf(FolderUserCollaboratorPath, folderId, userId)

	req, err := c.newRequest(ctx, http.MethodDelete, path, nil, LucidAuthTypeApiKey)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, req, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *LucidchartClient) UpsertDocumentUserCollaborator(ctx context.Context, documentId, userId string, role string) (*DocumentUserCollaboration, error) {
	var response DocumentUserCollaboration

	path := fmt.Sprintf(DocumentUserCollaboratorPath, documentId, userId)

	body := struct {
		Role string `json:"role"`
	}{
		Role: role,
	}

	req, err := c.newRequest(ctx, http.MethodPut, path, body, LucidAuthTypeApiKey)
	if err != nil {
		return &response, err
	}
	// Return response even on error, for the same reason as the folder upsert above.
	_, err = c.doRequest(ctx, req, &response)
	if err != nil {
		return &response, err
	}

	return &response, nil
}

func (c *LucidchartClient) DeleteDocumentUserCollaborator(ctx context.Context, documentId, userId string) error {
	path := fmt.Sprintf(DocumentUserCollaboratorPath, documentId, userId)

	req, err := c.newRequest(ctx, http.MethodDelete, path, nil, LucidAuthTypeApiKey)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, req, nil)
	if err != nil {
		return err
	}

	return nil
}
