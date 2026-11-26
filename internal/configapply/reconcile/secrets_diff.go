// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

type SecretDiffer interface {
	DiffSecrets(ctx context.Context, kind, id string, values any) (allMatch bool, err error)
	DiffModelSecrets(ctx context.Context, id string, authConfig map[string]any) (allMatch bool, err error)
}

type secretsDiffResponse struct {
	Secrets map[string]secretFieldMatch `json:"secrets"`
}

type secretFieldMatch struct {
	Matches bool `json:"matches"`
}

const secretsDiffPath = "/secretsDiff"

var secretsDiffResources = map[string]platformResource{
	"agents":      agentsResource,
	"entrypoints": entrypointsResource,
	"linkAgents":  linkAgentsResource,
	"connectors":  connectorsResource,
	"toolsets":    toolsetsResource,
}

func (c *Client) DiffSecrets(ctx context.Context, kind, id string, values any) (bool, error) {
	r, known := secretsDiffResources[kind]
	if !known {
		return false, fmt.Errorf("no secrets diff for kind %q", kind)
	}
	var resp secretsDiffResponse
	if err := c.callResource(ctx, r, http.MethodPut, "/"+url.PathEscape(id)+secretsDiffPath, SecretsDiffRequestBody{Values: values}, &resp); err != nil {
		return false, err
	}
	return allSecretsMatch(resp), nil
}

func (c *Client) DiffModelSecrets(ctx context.Context, id string, authConfig map[string]any) (bool, error) {
	var resp secretsDiffResponse
	if err := c.callResource(ctx, modelsResource, http.MethodPut, "/"+url.PathEscape(id)+secretsDiffPath, ModelSecretsDiffRequestBody{AuthConfig: authConfig}, &resp); err != nil {
		return false, err
	}
	return allSecretsMatch(resp), nil
}

func allSecretsMatch(resp secretsDiffResponse) bool {
	for _, f := range resp.Secrets {
		if !f.Matches {
			return false
		}
	}
	return true
}

type SecretsDiffRequestBody struct {
	Values any `json:"values"`
}

type ModelSecretsDiffRequestBody struct {
	AuthConfig map[string]any `json:"authConfig"`
}
