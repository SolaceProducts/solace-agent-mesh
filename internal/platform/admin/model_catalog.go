// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

type ModelCatalogEntry struct {
	Name        string  `json:"name"`
	ModelType   string  `json:"modelType"`
	Provider    *string `json:"provider"`
	ModelName   *string `json:"modelName"`
	Description *string `json:"description"`
}

type ModelCatalog struct {
	Data                 []ModelCatalogEntry `json:"data"`
	DecisionNodesEnabled bool                `json:"decisionNodesEnabled"`
}

func (m *ManageablePlatform) handleModelCatalog(ctx context.Context, method string, subPath []string) (any, error) {
	if method != "get" {
		return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
	}
	if len(subPath) > 0 {
		return nil, fmt.Errorf("models lists the catalog only; it has no per-model lookup")
	}
	if m.deps.ModelConfigSvc == nil {
		return nil, fmt.Errorf("model config service not available")
	}
	models, err := m.deps.ModelConfigSvc.List(ctx)
	if err != nil {
		return nil, err
	}
	catalog := ModelCatalog{
		Data:                 make([]ModelCatalogEntry, len(models)),
		DecisionNodesEnabled: m.deps.FeatureRegistry != nil && m.deps.FeatureRegistry.IsEnabled(features.KeyWorkflowDecisionNode),
	}
	for i, model := range models {
		catalog.Data[i] = ModelCatalogEntry{
			Name:        model.Alias,
			ModelType:   model.ModelType,
			Provider:    model.Provider,
			ModelName:   model.ModelName,
			Description: model.Description,
		}
	}
	return catalog, nil
}
