// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type ModelConfigService struct {
	repo     *store.ModelConfigRepo
	logger   *slog.Logger
	features *features.Registry
	grants   OAuthGrantReader
}

func (s *ModelConfigService) SetOAuthGrants(g OAuthGrantReader) {
	s.grants = g
}

func (s *ModelConfigService) validateRuntimeConfig(ctx context.Context, mc *store.ModelConfiguration) error {
	raw := ToRawLiteLLMConfig(mc)
	if err := ValidateRuntimeModelConfig(raw); err != nil {
		return err
	}
	return ValidateModelGrant(ctx, s.grants, raw)
}

func (s *ModelConfigService) SetFeatureRegistry(r *features.Registry) {
	s.features = r
}

func NewModelConfigService(repo *store.ModelConfigRepo, logger *slog.Logger) *ModelConfigService {
	return &ModelConfigService{repo: repo, logger: logger}
}

func (s *ModelConfigService) GetByAlias(ctx context.Context, alias string) (*ModelConfigResponse, error) {
	mc, err := s.repo.GetByAlias(ctx, alias)
	if err != nil {
		return nil, fmt.Errorf("get model config: %w", err)
	}
	if mc == nil {
		return nil, &NotFoundError{Resource: "model configuration", ID: alias}
	}
	resp := toModelConfigResponse(mc)
	return &resp, nil
}

func (s *ModelConfigService) GetRawByID(ctx context.Context, id string) (*store.ModelConfiguration, error) {
	mc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get raw model config: %w", err)
	}
	if mc == nil {
		return nil, &NotFoundError{Resource: "model configuration", ID: id}
	}
	return mc, nil
}

func (s *ModelConfigService) GetRawByAlias(ctx context.Context, alias string) (*store.ModelConfiguration, error) {
	mc, err := s.repo.GetByAlias(ctx, alias)
	if err != nil {
		return nil, fmt.Errorf("get raw model config: %w", err)
	}
	if mc == nil {
		return nil, &NotFoundError{Resource: "model configuration", ID: alias}
	}
	return mc, nil
}

const modelConfigPlaceholderValue = store.ModelConfigPlaceholder

func validateExtraHeaders(modelParams map[string]any) error {
	if k, bad := samapp.ExtraHeadersCRLFKey(modelParams); bad {
		return &ValidationError{Message: fmt.Sprintf("extra_headers %q contains CR/LF — header injection blocked", k)}
	}
	if _, named := modelParams[llm.GrantIDKey]; named {
		return &ValidationError{Message: GrantIDInModelParamsMessage}
	}
	return nil
}

func (s *ModelConfigService) Create(ctx context.Context, req CreateModelConfigRequest, createdBy string) (*ModelConfigResponse, error) {
	req.Alias = strings.ToLower(strings.TrimSpace(req.Alias))
	if req.Alias == "" {
		return nil, &ValidationError{Message: "alias is required"}
	}
	if req.Provider == "" {
		return nil, &ValidationError{Message: "provider is required"}
	}
	req.ModelName = strings.TrimSpace(req.ModelName)
	if req.ModelName == "" {
		return nil, &ValidationError{Message: "modelName is required"}
	}
	modelType, typeErr := ResolveModelType(req.ModelType, req.Provider)
	if typeErr != nil {
		return nil, &ValidationError{Message: typeErr.Error()}
	}
	if flagErr := RequireDecisionModelsEnabled(modelType, s.features); flagErr != nil {
		return nil, flagErr
	}
	if err := RequireDecisionAPIKey(modelType, req.AuthConfig); err != nil {
		return nil, authConfigValidationError(err)
	}
	if strings.TrimSpace(req.APIBase) == "" {
		req.APIBase = DefaultAPIBase(req.Provider, req.AuthConfig)
	}
	if err := RequireAPIBase(req.APIBase); err != nil {
		return nil, err
	}
	if err := validateExtraHeaders(req.ModelParams); err != nil {
		return nil, err
	}

	exists, err := s.repo.ExistsByAlias(ctx, req.Alias)
	if err != nil {
		return nil, fmt.Errorf("check alias exists: %w", err)
	}
	if exists {
		return nil, &ConflictError{Message: fmt.Sprintf("model configuration with alias %q already exists", req.Alias)}
	}

	apiBase := req.APIBase

	authType := "none"
	if t, ok := req.AuthConfig["type"].(string); ok && t != "" {
		authType = t
	}

	authConfigJSON, err := json.Marshal(req.AuthConfig)
	if err != nil {
		return nil, &ValidationError{Message: "invalid authConfig"}
	}

	modelParamsJSON := []byte("{}")
	if req.ModelParams != nil {
		modelParamsJSON, err = json.Marshal(req.ModelParams)
		if err != nil {
			return nil, &ValidationError{Message: "invalid modelParams"}
		}
	}

	mc := &store.ModelConfiguration{
		Alias:           req.Alias,
		Provider:        req.Provider,
		ModelName:       req.ModelName,
		ModelAuthType:   authType,
		ModelAuthConfig: string(authConfigJSON),
		ModelParams:     string(modelParamsJSON),
		MaxInputTokens:  req.MaxInputTokens,
		ModelType:       modelType,
		CreatedBy:       createdBy,
		UpdatedBy:       createdBy,
	}
	if apiBase != "" {
		mc.APIBase = &apiBase
	}
	if req.Description != "" {
		mc.Description = &req.Description
	}
	if err := s.validateRuntimeConfig(ctx, mc); err != nil {
		return nil, err
	}

	if err := s.repo.Create(ctx, mc); err != nil {
		return nil, fmt.Errorf("create model config: %w", err)
	}

	resp := toModelConfigResponse(mc)
	return &resp, nil
}

func (s *ModelConfigService) Update(ctx context.Context, modelID string, req UpdateModelConfigRequest, updatedBy string) (*ModelConfigResponse, error) {
	mc, err := s.repo.GetByID(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model config for update: %w", err)
	}
	if mc == nil {
		return nil, &NotFoundError{Resource: "model configuration", ID: modelID}
	}

	if req.Alias != nil {
		trimmed := strings.ToLower(strings.TrimSpace(*req.Alias))
		if trimmed == "" {
			return nil, &ValidationError{Message: "alias cannot be empty or contain only whitespace"}
		}
		req.Alias = &trimmed
	}
	if req.Alias != nil && *req.Alias != mc.Alias {
		exists, err := s.repo.ExistsByAlias(ctx, *req.Alias)
		if err != nil {
			return nil, fmt.Errorf("check new alias: %w", err)
		}
		if exists {
			return nil, &ConflictError{Message: fmt.Sprintf("model configuration with alias %q already exists", *req.Alias)}
		}
		mc.Alias = *req.Alias
	}

	storedProvider := mc.Provider
	if req.Provider != nil {
		mc.Provider = *req.Provider
	}
	if err := ValidateModelTypeUpdate(mc.ModelType, storedProvider, mc.Provider); err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}
	if req.ModelName != nil {
		modelName := strings.TrimSpace(*req.ModelName)
		if modelName == "" {
			return nil, &ValidationError{Message: "modelName cannot be empty or contain only whitespace"}
		}
		mc.ModelName = modelName
	}
	if req.APIBase != nil {
		apiBase := strings.TrimSpace(*req.APIBase)
		if apiBase == "" {
			provider := mc.Provider
			if req.Provider != nil {
				provider = *req.Provider
			}
			authConfig := req.AuthConfig
			if authConfig == nil {
				_ = json.Unmarshal([]byte(mc.ModelAuthConfig), &authConfig)
			}
			apiBase = DefaultAPIBase(provider, authConfig)
		}
		if err := RequireAPIBase(apiBase); err != nil {
			return nil, err
		}
		mc.APIBase = &apiBase
	}
	if req.Description != nil || req.DescriptionSet {
		mc.Description = req.Description
	}
	if req.MaxInputTokens != nil || req.MaxInputTokensSet {
		mc.MaxInputTokens = req.MaxInputTokens
	}

	if req.AuthConfig != nil {
		mergedJSON, resolvedType, err := mergeAuthConfigForUpdate(mc.ModelAuthConfig, req.AuthConfig)
		if err != nil {
			return nil, err
		}
		mc.ModelAuthConfig = mergedJSON
		if resolvedType != "" {
			mc.ModelAuthType = resolvedType
		}
		if err := requireMergedDecisionAPIKey(mc); err != nil {
			return nil, err
		}
	}

	if err := validateExtraHeaders(req.ModelParams); err != nil {
		return nil, err
	}
	if req.ModelParams != nil {
		paramsJSON, err := json.Marshal(req.ModelParams)
		if err != nil {
			return nil, &ValidationError{Message: "invalid modelParams"}
		}
		mc.ModelParams = string(paramsJSON)
	}

	if err := s.validateRuntimeConfig(ctx, mc); err != nil {
		return nil, err
	}

	mc.UpdatedBy = updatedBy
	mc.UpdatedTime = time.Now().UnixMilli()

	if err := s.repo.Update(ctx, mc); err != nil {
		return nil, fmt.Errorf("update model config: %w", err)
	}

	resp := toModelConfigResponse(mc)
	return &resp, nil
}

func requireMergedDecisionAPIKey(mc *store.ModelConfiguration) error {
	var merged map[string]any
	if err := json.Unmarshal([]byte(mc.ModelAuthConfig), &merged); err != nil {
		return &ValidationError{Message: "invalid authConfig"}
	}
	if err := RequireDecisionAPIKey(mc.ModelType, merged); err != nil {
		return authConfigValidationError(err)
	}
	return nil
}

func authConfigValidationError(err error) *ValidationError {
	return &ValidationError{
		Message: err.Error(),
		Field:   "authConfig",
		Details: map[string][]string{"authConfig": {err.Error()}},
	}
}

func (s *ModelConfigService) Delete(ctx context.Context, modelID string) (*store.ModelConfiguration, error) {
	mc, err := s.repo.GetByID(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model config for delete: %w", err)
	}
	if mc == nil {
		return nil, &NotFoundError{Resource: "model configuration", ID: modelID}
	}

	if err := s.repo.Delete(ctx, modelID); err != nil {
		return nil, fmt.Errorf("delete model config: %w", err)
	}
	return mc, nil
}

func IsModelConfigured(mc *store.ModelConfiguration) bool {
	return mc != nil && strings.TrimSpace(mc.ModelName) != "" && mc.ModelName != modelConfigPlaceholderValue
}

func (s *ModelConfigService) CheckStatus(ctx context.Context) (*ModelConfigStatusResponse, error) {
	general, err := s.repo.GetByAlias(ctx, "general")
	if err != nil {
		return nil, fmt.Errorf("check general model: %w", err)
	}
	planning, err := s.repo.GetByAlias(ctx, "planning")
	if err != nil {
		return nil, fmt.Errorf("check planning model: %w", err)
	}
	configured := IsModelConfigured(general) && IsModelConfigured(planning)
	return &ModelConfigStatusResponse{Configured: configured}, nil
}

var OAuthKeyMap = map[string]string{
	"client_id":                    "oauth_client_id",
	"client_secret":                "oauth_client_secret",
	"token_url":                    "oauth_token_url",
	"scope":                        "oauth_scope",
	"audience":                     "oauth_audience",
	"ca_cert":                      "oauth_ca_cert",
	"token_refresh_buffer_seconds": "oauth_token_refresh_buffer_seconds",
	"max_retries":                  "oauth_max_retries",
}

func WriteAuthConfigToRaw(raw map[string]any, authConfig map[string]any) {
	authType, _ := authConfig["type"].(string)
	switch authType {
	case AuthTypeAzureWorkloadIdentity:
		raw[llm.AzureWorkloadIdentityKey] = azureWorkloadIdentityBlock(authConfig)
	case AuthTypeOAuth2Delegated:
		raw[llm.GrantIDKey] = ""
	}
	for k, v := range authConfig {
		if k == "type" {
			continue
		}
		if authType == AuthTypeAzureWorkloadIdentity && slices.Contains(azureWorkloadIdentityFields, k) {
			continue
		}
		if k == llm.GrantIDKey && authType != AuthTypeOAuth2Delegated {
			continue
		}
		if authType == "oauth2" {
			if mapped, ok := OAuthKeyMap[k]; ok {
				raw[mapped] = v
				continue
			}
		}
		raw[k] = v
	}
}

func ToRawLiteLLMConfig(mc *store.ModelConfiguration) map[string]any {
	result := map[string]any{
		"model": ResolveLiteLLMModelName(mc.Provider, mc.ModelType, mc.ModelName),
	}
	if mc.APIBase != nil {
		result["api_base"] = *mc.APIBase
	}

	var authConfig map[string]any
	if err := json.Unmarshal([]byte(mc.ModelAuthConfig), &authConfig); err == nil {
		WriteAuthConfigToRaw(result, authConfig)
	}

	var modelParams map[string]any
	if err := json.Unmarshal([]byte(mc.ModelParams), &modelParams); err == nil {
		for k, v := range modelParams {
			result[k] = v
		}
		samapp.WithModelParams(result, modelParams)
	}

	if mc.MaxInputTokens != nil {
		result["max_input_tokens"] = *mc.MaxInputTokens
	}
	AddModelTypeToRaw(result, mc.ModelType, mc.Provider)
	result[samapp.ModelConfigIDKey] = mc.ID

	return result
}

func toModelConfigResponse(mc *store.ModelConfiguration) ModelConfigResponse {
	var authConfig map[string]any
	if err := json.Unmarshal([]byte(mc.ModelAuthConfig), &authConfig); err != nil {
		authConfig = map[string]any{}
	}

	var modelParams map[string]any
	if err := json.Unmarshal([]byte(mc.ModelParams), &modelParams); err != nil {
		modelParams = map[string]any{}
	}

	return ModelConfigResponse{
		ID:             mc.ID,
		Alias:          mc.Alias,
		Provider:       mc.Provider,
		ModelName:      mc.ModelName,
		APIBase:        mc.APIBase,
		AuthType:       mc.ModelAuthType,
		AuthConfig:     RedactAuthConfig(mc.ModelAuthType, authConfig),
		ModelParams:    modelParams,
		Description:    mc.Description,
		MaxInputTokens: mc.MaxInputTokens,
		ModelType:      mc.ModelType,
		CreatedBy:      mc.CreatedBy,
		UpdatedBy:      mc.UpdatedBy,
		CreatedAt:      epochMsToISO8601(mc.CreatedTime),
		UpdatedAt:      epochMsToISO8601(mc.UpdatedTime),
	}
}

func epochMsToISO8601(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z07:00")
}
