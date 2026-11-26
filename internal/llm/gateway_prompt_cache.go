// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"slices"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/maximhq/bifrost/core/schemas"
)

func claudeBehindOpenAIGateway(cfg BifrostConfig, azureGateway azureGatewayRoute) bool {
	if schemas.ModelProvider(cfg.Provider) != schemas.OpenAI || azureGateway.active || cfg.GrantID != "" {
		return false
	}
	host := baseURLHost(cfg.BaseURL)
	return host != "" && !isOpenAIHost(host)
}

func isOpenAIHost(host string) bool {
	return host == "openai.com" || strings.HasSuffix(host, ".openai.com")
}

func (c *BifrostClient) wireProviderFor(model string) schemas.ModelProvider {
	if c.claudeBehindGateway && IsClaudeModel(model) {
		return schemas.OpenRouter
	}
	return schemas.ModelProvider(c.cfg.Provider)
}

func IsClaudeModel(model string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(model), isModelIDSeparator) {
		if token == "claude" || slices.Contains(modelfamily.ClaudeTiers, token) {
			return true
		}
	}
	return false
}

func isModelIDSeparator(r rune) bool {
	return r == '-' || r == '_' || r == '/' || r == '.' || r == ':' || r == '@'
}

func requestsPromptCache(req *ChatRequest) bool {
	for i := range req.Messages {
		if req.Messages[i].CacheControl != nil {
			return true
		}
	}
	for i := range req.Tools {
		if req.Tools[i].CacheControl != nil {
			return true
		}
	}
	return false
}

func expectsCacheRead(req *ChatRequest) bool {
	if !requestsPromptCache(req) {
		return false
	}
	for i := range req.Messages {
		if req.Messages[i].Role == "assistant" {
			return true
		}
	}
	return false
}

const (
	promptCacheMinPromptTokens  = 4096
	promptCacheMissesBeforeWarn = 20
)

type promptCacheEvent int

const (
	promptCacheQuiet promptCacheEvent = iota
	promptCacheNeverServed
	promptCacheServing
)

type promptCacheWatch struct {
	mu     sync.Mutex
	misses int
	warned bool
	served bool
}

func (w *promptCacheWatch) observe(usage *Usage) (misses int, event promptCacheEvent) {
	if usage == nil || usage.PromptTokens < promptCacheMinPromptTokens {
		return 0, promptCacheQuiet
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if usage.CachedTokens-usage.CacheWriteTokens > 0 {
		w.misses = 0
		announce := !w.served || w.warned
		w.served = true
		w.warned = false
		if announce {
			return 0, promptCacheServing
		}
		return 0, promptCacheQuiet
	}
	w.misses++
	if w.warned || w.misses < promptCacheMissesBeforeWarn {
		return w.misses, promptCacheQuiet
	}
	w.warned = true
	return w.misses, promptCacheNeverServed
}

const promptCacheHint = "check that the gateway forwards cache_control for this model"

func (c *BifrostClient) notePromptCacheOutcome(cacheReadExpected bool, model string, usage *Usage) {
	if !cacheReadExpected || c.wireProvider(model) != schemas.OpenRouter {
		return
	}
	misses, event := c.promptCache.observe(usage)
	switch event {
	case promptCacheNeverServed:
		c.logger.Warn("prompt caching requested on every follow-up call but the provider never reported a cached token",
			"provider", c.cfg.Provider,
			"host", baseURLHost(c.cfg.BaseURL),
			"model", model,
			"consecutiveMisses", misses,
			"hint", promptCacheHint)
	case promptCacheServing:
		c.logger.Info("prompt cache serving",
			"provider", c.cfg.Provider,
			"host", baseURLHost(c.cfg.BaseURL),
			"model", model)
	}
}
