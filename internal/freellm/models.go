// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package freellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const modelsPath = "/v1/models"

type ModelDisplay struct {
	ModelName   string
	DisplayName string
}

func (a *Allowances) KnownModels() []ModelDisplay {
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	return a.models
}

func (a *Allowances) Models(ctx context.Context) ([]ModelDisplay, error) {
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	if a.models != nil {
		return a.models, nil
	}
	answer, err := a.fetch(ctx, modelsPath)
	if err != nil {
		return nil, fmt.Errorf("read the free models: %w", err)
	}
	switch answer.status {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil
	default:
		return nil, fmt.Errorf("read the free models: the free LLM answered %d", answer.status)
	}
	var list struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(answer.body, &list); err != nil {
		return nil, fmt.Errorf("decode the free models: %w", err)
	}
	models := make([]ModelDisplay, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID == "" || m.DisplayName == "" {
			continue
		}
		models = append(models, ModelDisplay{ModelName: m.ID, DisplayName: m.DisplayName})
	}
	if len(models) == 0 {
		return nil, errors.New("read the free models: the free LLM named none")
	}
	a.models = models
	return models, nil
}
