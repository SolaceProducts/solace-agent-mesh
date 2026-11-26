// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const placeholderValue = store.ModelConfigPlaceholder

var defaultModelAliases = []string{"general", "planning"}

type seedModelConfig struct {
	Alias       string
	ModelEnv    string
	EndpointEnv string
	APIKeyEnv   string
	Description string
}

var defaultSeedConfigs = []seedModelConfig{
	{Alias: "planning", ModelEnv: "LLM_SERVICE_PLANNING_MODEL_NAME", EndpointEnv: "LLM_SERVICE_ENDPOINT", APIKeyEnv: "LLM_SERVICE_API_KEY", Description: "Default planning model"},
	{Alias: "general", ModelEnv: "LLM_SERVICE_GENERAL_MODEL_NAME", EndpointEnv: "LLM_SERVICE_ENDPOINT", APIKeyEnv: "LLM_SERVICE_API_KEY", Description: "Default general-purpose model"},
	{Alias: "image_gen", ModelEnv: "IMAGE_MODEL_NAME", EndpointEnv: "IMAGE_SERVICE_ENDPOINT", APIKeyEnv: "IMAGE_SERVICE_API_KEY", Description: "Default image generation model"},
	{Alias: "report_gen", ModelEnv: "LLM_REPORT_MODEL_NAME", EndpointEnv: "LLM_SERVICE_ENDPOINT", APIKeyEnv: "LLM_SERVICE_API_KEY", Description: "Default report generation model"},
}

func seedModelEnv(name string) (string, []string) {
	return config.ExpandVarsCollect(os.Getenv(name), os.LookupEnv)
}

func SeedModelConfigs(ctx context.Context, repo *store.ModelConfigRepo, analytics *platpkg.ResourceReporter, logger *slog.Logger) error {
	warnedMalformedEndpoint := make(map[string]bool, len(defaultSeedConfigs))
	for _, sc := range defaultSeedConfigs {
		modelName, unresolved := seedModelEnv(sc.ModelEnv)
		modelName = strings.TrimSpace(modelName)
		if modelName == "" && len(unresolved) == 0 {
			continue
		}
		if len(unresolved) > 0 || looksLikeUnresolvedRef(modelName) {
			logger.Warn("model config env holds unresolved variable reference; skipping seed", "alias", sc.Alias, "env", sc.ModelEnv, "unresolvedVars", unresolved, "value", modelName)
			continue
		}

		existing, err := repo.GetByAlias(ctx, sc.Alias)
		if err != nil {
			return fmt.Errorf("seed: get %q: %w", sc.Alias, err)
		}
		if existing != nil && isConfigured(existing) {
			continue
		}

		endpoint := strings.TrimSpace(os.Getenv(sc.EndpointEnv))
		if !platpkg.ValidAPIBaseStructure(endpoint) {
			if !warnedMalformedEndpoint[sc.EndpointEnv] {
				logger.Warn("model config endpoint env is malformed; skipping seed for every alias that uses it", "env", sc.EndpointEnv, "valueLen", len(endpoint))
				warnedMalformedEndpoint[sc.EndpointEnv] = true
			}
			continue
		}
		apiKey := os.Getenv(sc.APIKeyEnv)
		provider := inferProvider(endpoint, modelName)

		authType := "none"
		authConfigJSON := "{}"
		if apiKey != "" {
			authType = "apikey"
			authConfigJSON = `{"type":"apikey","api_key":"` + escapeJSONString(apiKey) + `"}`
		}

		description := sc.Description

		if existing == nil {
			mc := &store.ModelConfiguration{
				Alias:           sc.Alias,
				Provider:        provider,
				ModelName:       modelName,
				ModelAuthType:   authType,
				ModelAuthConfig: authConfigJSON,
				ModelParams:     "{}",
				Description:     &description,
				CreatedBy:       "system",
				UpdatedBy:       "system",
			}
			if endpoint != "" {
				mc.APIBase = &endpoint
			}
			if err := repo.Create(ctx, mc); err != nil {
				logger.Warn("failed to seed model config", "alias", sc.Alias, "error", err)
				continue
			}
			logger.Info("seeded model configuration", "alias", sc.Alias, "provider", provider, "model", modelName)
			analytics.ModelConfigured(ctx, provider, modelName, endpoint, taxonomy.ModelKindChat, httputil.SurfaceSystem)
		} else {
			existing.Provider = provider
			existing.ModelName = modelName
			existing.ModelAuthType = authType
			existing.ModelAuthConfig = authConfigJSON
			if endpoint != "" {
				existing.APIBase = &endpoint
			} else {
				existing.APIBase = nil
			}
			existing.Description = &description
			existing.UpdatedBy = "system"
			if err := repo.Update(ctx, existing); err != nil {
				logger.Warn("failed to upgrade placeholder model config", "alias", sc.Alias, "error", err)
				continue
			}
			logger.Info("upgraded placeholder model configuration from env", "alias", sc.Alias, "provider", provider, "model", modelName)
			analytics.ModelConfigured(ctx, provider, modelName, endpoint, taxonomy.ModelKindChat, httputil.SurfaceSystem)
		}
	}

	return ensureDefaultAliases(ctx, repo, logger)
}

func ensureDefaultAliases(ctx context.Context, repo *store.ModelConfigRepo, logger *slog.Logger) error {
	for _, alias := range defaultModelAliases {
		existing, err := repo.GetByAlias(ctx, alias)
		if err != nil {
			return fmt.Errorf("ensure default alias %q: %w", alias, err)
		}
		if existing != nil {
			continue
		}
		mc := &store.ModelConfiguration{Alias: alias, CreatedBy: "system"}
		setPlaceholder(mc)
		if err := repo.Create(ctx, mc); err != nil {
			logger.Warn("failed to create placeholder for default model alias", "alias", alias, "error", err)
			continue
		}
		logger.Info("created placeholder for default model alias", "alias", alias)
	}
	return nil
}

func setPlaceholder(mc *store.ModelConfiguration) {
	description := "Default model configuration created by the system"
	mc.Provider = placeholderValue
	mc.ModelName = placeholderValue
	mc.APIBase = nil
	mc.ModelAuthType = "none"
	mc.ModelAuthConfig = `{"type":"none"}`
	mc.ModelParams = "{}"
	mc.Description = &description
	mc.MaxInputTokens = nil
	mc.PriceInput, mc.PriceOutput, mc.PriceCacheRead, mc.PriceCacheWrite = nil, nil, nil, nil
	mc.UpdatedBy = "system"
}

func inferProvider(endpoint, modelName string) string {
	if endpoint != "" {
		if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
			host := strings.ToLower(u.Host)
			switch {
			case strings.Contains(host, "api.openai.com"):
				return "openai"
			case strings.Contains(host, "api.anthropic.com"):
				return "anthropic"
			case isAzureHost(host):
				return "azure_openai"
			case strings.Contains(host, "bedrock"):
				return "bedrock"
			case strings.Contains(host, "aiplatform.googleapis.com"):
				return "vertex_ai"
			case strings.Contains(host, "generativelanguage.googleapis.com"):
				return "google_ai_studio"
			}
		}
		return "custom"
	}

	lower := strings.ToLower(modelName)
	switch {
	case strings.HasPrefix(lower, "claude"):
		return "anthropic"
	case strings.HasPrefix(lower, "gpt"):
		return "openai"
	case strings.HasPrefix(lower, "gemini"):
		return "google_ai_studio"
	}

	return "custom"
}

var azureHostSuffixes = []string{
	".openai.azure.com",
	".openai.azure.us",
	".openai.azure.cn",
	".cognitiveservices.azure.com",
	".cognitiveservices.azure.us",
	".cognitiveservices.azure.cn",
	".api.cognitive.microsoft.com",
	".services.ai.azure.com",
}

func isAzureHost(host string) bool {
	for _, suffix := range azureHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func escapeJSONString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
