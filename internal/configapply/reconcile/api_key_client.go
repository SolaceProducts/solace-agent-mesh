// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"net/http"
	"net/url"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

var apiKeysResource = platformResource{path: "/apiKeys"}

type APIKeyFilter struct {
	UserID string
	Status string
}

func (c *Client) CreateAPIKey(ctx context.Context, req wire.CreateAPIKeyRequest) (wire.CreatedAPIKey, error) {
	var resp wire.CreatedAPIKey
	if err := c.callResource(ctx, apiKeysResource, http.MethodPost, "", req, &resp); err != nil {
		return wire.CreatedAPIKey{}, err
	}
	return resp, nil
}

func (c *Client) GetAPIKey(ctx context.Context, id string) (wire.APIKeyResponse, error) {
	var resp wire.APIKeyResponse
	if err := c.callResource(ctx, apiKeysResource, http.MethodGet, "/"+url.PathEscape(id), nil, &resp); err != nil {
		return wire.APIKeyResponse{}, err
	}
	return resp, nil
}

func (c *Client) ListAPIKeys(ctx context.Context, filter APIKeyFilter) ([]wire.APIKeyResponse, error) {
	query := url.Values{}
	if filter.UserID != "" {
		query.Set("userId", filter.UserID)
	}
	if filter.Status != "" {
		query.Set("status", filter.Status)
	}
	var suffix string
	if len(query) > 0 {
		suffix = "?" + query.Encode()
	}
	return listAllPages[wire.APIKeyResponse](ctx, c, apiKeysResource.apiPath()+suffix)
}

func (c *Client) RenameAPIKey(ctx context.Context, id, label string) (wire.APIKeyResponse, error) {
	var resp wire.APIKeyResponse
	req := wire.UpdateAPIKeyRequest{Label: label}
	if err := c.callResource(ctx, apiKeysResource, http.MethodPut, "/"+url.PathEscape(id), req, &resp); err != nil {
		return wire.APIKeyResponse{}, err
	}
	return resp, nil
}

func (c *Client) DeleteAPIKey(ctx context.Context, id string) error {
	return c.callResource(ctx, apiKeysResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}
