// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"log/slog"

	"github.com/maximhq/bifrost/core/schemas"
)

var nativeProviderForOpenAICompatibleHost = map[string]schemas.ModelProvider{
	"api.deepseek.com": schemas.DeepSeek,
}

var providersReplayingToolCallReasoning = map[schemas.ModelProvider]bool{
	schemas.DeepSeek: true,
}

func routeOpenAICompatibleHostToNativeProvider(cfg BifrostConfig, logger *slog.Logger) BifrostConfig {
	if schemas.ModelProvider(cfg.Provider) != schemas.OpenAI || cfg.OAuth2 != nil || cfg.GrantID != "" {
		return cfg
	}
	host := baseURLHost(cfg.BaseURL)
	native, known := nativeProviderForOpenAICompatibleHost[host]
	if !known {
		return cfg
	}
	if cfg.APIKeyEnvVar == "" {
		cfg.APIKeyEnvVar = defaultAPIKeyEnv(cfg.Provider)
	}
	if root, isEndpoint := ChatCompletionsRoot(cfg.BaseURL); isEndpoint {
		cfg.BaseURL = root
	}
	cfg.Provider = string(native)
	logger.Info("OpenAI-compatible endpoint is a provider's own API: using that provider's native route so thinking-mode tool calls replay their reasoning",
		"host", host, "provider", cfg.Provider)
	return cfg
}

func replaysToolCallReasoning(provider schemas.ModelProvider) bool {
	return providersReplayingToolCallReasoning[provider]
}

func dropToolCallReasoning(msgs []schemas.ChatMessage) {
	for i := range msgs {
		if msgs[i].ChatAssistantMessage != nil {
			msgs[i].Reasoning = nil
		}
	}
}
