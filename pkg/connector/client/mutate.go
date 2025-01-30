package client

import (
	"context"
	"fmt"
	"net/http"
)

func (c *LucidchartClient) UpsertFolderUserCollaborator(ctx context.Context, folderId, userId string, role string) (*FolderUserCollaboration, error) {
	var response FolderUserCollaboration

	path := fmt.Sprintf(UpsertFolderUserCollaboratorPath, folderId, userId)

	body := struct {
		Role string `json:"role"`
	}{
		Role: role,
	}

	req, err := c.newRequest(ctx, LucidchartApiUrl, http.MethodPut, path, body, LucidAuthTypeApiKey)
	if err != nil {
		return nil, err
	}
	_, err = c.doRequest(ctx, req, &response, false)
	if err != nil {
		return nil, err
	}

	return &response, nil
}

func (c *LucidchartClient) DeleteFolderUserCollaborator(ctx context.Context, folderId, userId string) error {
	path := fmt.Sprintf(DeleteFolderUserCollaboratorPath, folderId, userId)

	req, err := c.newRequest(ctx, LucidchartApiUrl, http.MethodDelete, path, nil, LucidAuthTypeApiKey)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, req, nil, false)
	if err != nil {
		return err
	}

	return nil
}
