// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"log/slog"

	"github.com/maximhq/bifrost/core/schemas"
)

const freeModelCacheMarkerRoute = schemas.OpenRouter

func isFreeModelConfig(cfg BifrostConfig) bool {
	return cfg.GrantID == FreeModelGrantID && schemas.ModelProvider(cfg.Provider) == schemas.OpenAI
}

func (c *BifrostClient) wireProvider(model string) schemas.ModelProvider {
	if isFreeModelConfig(c.cfg) {
		return freeModelCacheMarkerRoute
	}
	return c.wireProviderFor(model)
}

func logFreeModelCacheRoute(cfg BifrostConfig, logger *slog.Logger) {
	if isFreeModelConfig(cfg) {
		logger.Info("free model: the free LLM serves only Claude, so requests take Bifrost's OpenRouter route to keep prompt-cache markers",
			"host", baseURLHost(cfg.BaseURL))
	}
}
