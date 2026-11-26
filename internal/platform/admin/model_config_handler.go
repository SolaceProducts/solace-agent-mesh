// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const errProviderRequired = "provider is required"

type modelConfigHandler struct {
	svc            *ModelConfigService
	logger         *slog.Logger
	spendPublisher *spend.Publisher
	budgetGate     *budget.Gate
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func probeAuthConfig(req *ModelConfigTestRequest) map[string]any {
	authConfig := req.AuthConfig
	if req.AuthType == "" {
		return authConfig
	}
	if authConfig == nil {
		authConfig = map[string]any{}
	}
	if t, ok := authConfig["type"].(string); !ok || t == "" {
		authConfig["type"] = req.AuthType
	}
	return authConfig
}

type probeConfig struct {
	raw         map[string]any
	modelParams map[string]any
	provider    string
	modelType   string
	configID    string
}

func (h *modelConfigHandler) buildProbeConfig(ctx context.Context, req *ModelConfigTestRequest, authConfig map[string]any) (*probeConfig, error) {
	modelName := strings.TrimSpace(req.ModelName)
	if req.ModelID != "" {
		mc, err := h.svc.repo.GetByID(ctx, req.ModelID)
		if err != nil {
			return nil, fmt.Errorf("load model config for connection test: %w", err)
		}
		if mc == nil {
			return nil, &NotFoundError{Resource: resourceModelConfig, ID: req.ModelID}
		}
		apiBase, auth := probeConnection(mc, req, authConfig)
		params := req.ModelParams
		if params == nil {
			params = parseJSONMap(mc.ModelParams)
		}
		raw := buildRawConfigForProbe(mc, req.Provider, modelName, apiBase, auth, params)
		samapp.WithModelParams(raw, params)
		provider, modelType, err := platpkg.StoredTestTarget(mc.ModelType, mc.Provider, req.Provider)
		if err != nil {
			return nil, err
		}
		probe := &probeConfig{raw: raw, modelParams: params, provider: provider, modelType: modelType}
		if (req.Provider == "" || req.Provider == mc.Provider) && (req.ModelName == "" || req.ModelName == mc.ModelName) {
			probe.configID = mc.ID
		}
		return probe, nil
	}

	if req.Provider == "" {
		return nil, &ValidationError{Message: errProviderRequired, Field: "provider"}
	}
	if authType, _ := authConfig["type"].(string); authType == "" {
		return nil, &ValidationError{Message: "authConfig.type is required", Field: "authConfig"}
	}
	if modelName == "" {
		return nil, &ValidationError{Message: "modelName is required", Field: "modelName"}
	}
	modelType, err := platpkg.NewTestTarget(req.ModelType, req.Provider)
	if err != nil {
		return nil, err
	}

	raw := map[string]any{
		"model": platpkg.ResolveLiteLLMModelName(req.Provider, modelType, modelName),
	}
	if req.APIBase != "" {
		raw["api_base"] = req.APIBase
	}
	platpkg.WriteAuthConfigToRaw(raw, authConfig)
	maps.Copy(raw, req.ModelParams)
	samapp.WithModelParams(raw, req.ModelParams)
	return &probeConfig{raw: raw, modelParams: req.ModelParams, provider: req.Provider, modelType: modelType}, nil
}

func probeConnection(mc *store.ModelConfiguration, req *ModelConfigTestRequest, authConfig map[string]any) (string, map[string]any) {
	if switchesProvider(mc.Provider, req.Provider) {
		return req.APIBase, authConfig
	}
	auth := parseJSONMap(mc.ModelAuthConfig)
	if len(authConfig) > 0 {
		auth = mergeModelAuthConfig(auth, authConfig)
	}
	return cmp.Or(req.APIBase, derefOrEmpty(mc.APIBase)), auth
}

func buildRawConfigForProbe(mc *store.ModelConfiguration, providerOverride, modelNameOverride, apiBase string, auth, params map[string]any) map[string]any {
	provider := cmp.Or(providerOverride, mc.Provider)
	modelName := cmp.Or(modelNameOverride, mc.ModelName)
	raw := map[string]any{
		"model": platpkg.ResolveLiteLLMModelName(provider, mc.ModelType, modelName),
	}
	if apiBase != "" {
		raw["api_base"] = apiBase
	}
	platpkg.WriteAuthConfigToRaw(raw, auth)
	maps.Copy(raw, params)
	if mc.MaxInputTokens != nil {
		raw["max_input_tokens"] = *mc.MaxInputTokens
	}
	return raw
}
