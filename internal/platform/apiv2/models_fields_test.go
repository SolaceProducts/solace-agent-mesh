// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestModels_UpdateLeavesTheFieldsItDoesNotNameAlone(t *testing.T) {
	api, _ := newModelAPI(t)
	created := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": "keep-fields", "provider": "openai", "modelName": "gpt-4",
		"apiBase": "https://llm.example.com/v1", "authConfig": apiKeyAuth(),
		"description": "keep me", "maxInputTokens": 128000,
	}), "models")

	rec := api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{"modelName": "gpt-4o"})

	expectStatus(t, rec, http.StatusOK)
	updated := decodeData[Model](t, rec)
	if updated.Description == nil || *updated.Description != "keep me" {
		t.Errorf("description became %v, want it untouched", updated.Description)
	}
	if updated.MaxInputTokens == nil || *updated.MaxInputTokens != 128000 {
		t.Errorf("maxInputTokens became %v, want it untouched", updated.MaxInputTokens)
	}
	if updated.ModelName == nil || *updated.ModelName != "gpt-4o" {
		t.Errorf("modelName became %v, want gpt-4o", updated.ModelName)
	}
}

func TestModels_PricingIsWithheldWithoutModelConfigRead(t *testing.T) {
	api, _ := newModelAPI(t)
	created := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": "priced-read", "provider": "openai", "modelName": "gpt-4o", "authConfig": apiKeyAuth(),
		"pricing": map[string]any{"input": "2.5", "output": "10"},
	}), "models")

	for name, tc := range map[string]struct {
		scope       string
		wantPricing bool
	}{
		"with model-config read":    {rbac.ScopeModelConfigRead, true},
		"without model-config read": {rbac.ScopeAgentBuilderRead, false},
	} {
		t.Run(name, func(t *testing.T) {
			single := api.call(http.MethodGet, "/models/"+created.ID, nil, tc.scope)
			expectStatus(t, single, http.StatusOK)
			if got := decodeData[Model](t, single).Pricing != nil; got != tc.wantPricing {
				t.Errorf("GET /models/{id}: pricing present = %v, want %v", got, tc.wantPricing)
			}

			list := api.call(http.MethodGet, "/models?pageSize=100", nil, tc.scope)
			expectStatus(t, list, http.StatusOK)
			for _, m := range decodeList[Model](t, list).Data {
				if m.ID == created.ID && (m.Pricing != nil) != tc.wantPricing {
					t.Errorf("GET /models: pricing present = %v, want %v", m.Pricing != nil, tc.wantPricing)
				}
			}
		})
	}
}

func TestModels_DecisionModelWithoutAnAPIKeyNamesOnlyAuthConfig(t *testing.T) {
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "jev", "provider": "typesafe", "modelName": "jev-1.13.0", "modelType": "decision",
		"apiBase": "https://api.typesafe.ai", "authConfig": map[string]any{"type": "none"},
	})

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if keys := slices.Sorted(maps.Keys(decodeError(t, rec).ValidationDetails)); !slices.Equal(keys, []string{"authConfig"}) {
		t.Errorf("validationDetails keys = %v, want only authConfig: %s", keys, rec.Body.String())
	}
}

func TestServerCapabilities_NoDecisionModelsWhileBothDecisionFlagsAreOff(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeyWorkflowDecisionNode), "false")
	t.Setenv(features.EnvVarName(features.KeyEvalDecisionEvaluator), "false")
	core, _ := newTestCore(t)

	rec := newAPIClient(t, core).call(http.MethodGet, "/serverCapabilities", nil)

	expectStatus(t, rec, http.StatusOK)
	if decodeData[ServerCapabilities](t, rec).CreateDecisionModels {
		t.Error("createDecisionModels = true with both decision flags off")
	}
}
