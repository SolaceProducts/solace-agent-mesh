// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func (a *Agent) agentTurnRequest(ctx context.Context, state *TaskExecutionState, messages []llm.ChatMessage, tools []llm.ToolDefinition) *llm.ChatRequest {
	req := &llm.ChatRequest{
		Model:            a.cfg.Model,
		Messages:         messages,
		Tools:            tools,
		MaxTokens:        a.cfg.MaxTokens,
		ModelOverride:    state.ModelOverride,
		Spend:            a.agentTurnSpend(ctx, state, messages),
		WithStoredParams: true,
	}
	a.applyReasoningAndCache(ctx, req, state)
	return req
}

func (a *Agent) applyReasoningAndCache(ctx context.Context, req *llm.ChatRequest, state *TaskExecutionState) {
	var bindingRaw map[string]any
	if a.cfg.ModelAlias != "" {
		if reg := a.svc.Bindings(); reg != nil {
			if raw, err := reg.ResolveRawOrFetch(ctx, a.cfg.ModelAlias); err != nil {
				a.logger.Debug("bound model_params unavailable for cache/reasoning enrichment; using defaults",
					"alias", a.cfg.ModelAlias, "error", err)
			} else {
				bindingRaw = raw
			}
		}
	}

	reasoning, answering := resolveReasoningTier(
		reasoningTier{source: "taskOverride",
			effort: state.OverrideReasoningEffort,
			budget: state.OverrideReasoningBudgetTokens},
		reasoningTier{source: "agentConfig",
			effort: a.cfg.ReasoningEffort, budget: a.cfg.ReasoningBudgetTokens},
		a.bindingReasoningTier(bindingRaw),
	)
	req.Reasoning = reasoning

	var resolvedEffort string
	var resolvedBudget int
	if reasoning != nil {
		resolvedEffort, resolvedBudget = reasoning.Effort, reasoning.BudgetTokens
	}
	a.logger.Debug("resolved reasoning config",
		"reasoningEffort", resolvedEffort,
		"reasoningBudgetTokens", resolvedBudget,
		"reasoningSource", answering.source)

	cacheStrategy := a.cfg.CacheStrategy
	switch {
	case state.OverrideCacheStrategy != "":
		cacheStrategy = state.OverrideCacheStrategy
	case cacheStrategy == "" && bindingRaw != nil:
		cacheStrategy = samapp.ExtractCacheStrategy(bindingRaw)
		if cacheStrategy == "" {
			cacheStrategy = defaultBoundCacheStrategy
		}
	}
	if cacheStrategy != "" && cacheStrategy != "none" {
		applyCacheControl(req, cacheStrategy)
	}
}

func (a *Agent) taskStubBatchBytes(ctx context.Context, state *TaskExecutionState) int {
	if state == nil {
		return a.stubBatchBytes(ctx, nil, "")
	}
	return a.stubBatchBytes(ctx, state.ModelOverride, state.ModelOverrideReportModel)
}

func (a *Agent) overrideStubBatchBytes(ctx context.Context, override *ResolvedModelOverride) int {
	if override == nil {
		return a.stubBatchBytes(ctx, nil, "")
	}
	return a.stubBatchBytes(ctx, override.Config, override.ReportModel)
}

func (a *Agent) stubBatchBytes(ctx context.Context, override *llm.BifrostConfig, overrideReportModel string) int {
	if override != nil {
		return llm.ToolResultStubBatchBytes(override.Provider, cmp.Or(overrideReportModel, override.Model))
	}
	model := a.reportModelCtx(ctx)
	provider, _ := samapp.ParseModelProvider(model)
	return llm.ToolResultStubBatchBytes(provider, model)
}

const reasoningSourceUnset = "unset"

const defaultBoundCacheStrategy = "5m"

type reasoningTier struct {
	source string
	effort string
	budget int
}

func (t reasoningTier) silent() bool { return t.effort == "" && t.budget <= 0 }

func (a *Agent) bindingReasoningTier(bindingRaw map[string]any) reasoningTier {
	if bindingRaw == nil {
		return reasoningTier{source: "modelBinding"}
	}
	effort, offending, rejected := samapp.ExtractReasoningEffort(bindingRaw)
	a.warnOnNewRejectedBindingEffort(offending, rejected)
	return reasoningTier{
		source: "modelBinding",
		effort: effort,
		budget: samapp.ExtractReasoningBudgetTokens(bindingRaw),
	}
}

func (a *Agent) warnOnNewRejectedBindingEffort(offending string, rejected bool) {
	a.bindingEffortMu.Lock()
	defer a.bindingEffortMu.Unlock()
	if !rejected {
		a.haveRejectedBindingEffort = false
		a.lastRejectedBindingEffort = ""
		return
	}
	if a.haveRejectedBindingEffort && offending == a.lastRejectedBindingEffort {
		return
	}
	a.haveRejectedBindingEffort = true
	a.lastRejectedBindingEffort = offending
	a.logger.Warn("invalid thinking.effort, ignoring",
		"source", "modelBinding", "alias", a.cfg.ModelAlias, "value", offending)
}

func resolveReasoningTier(tiers ...reasoningTier) (*llm.ReasoningConfig, reasoningTier) {
	for _, tier := range tiers {
		if tier.silent() {
			continue
		}
		if tier.effort != "" {
			return &llm.ReasoningConfig{Effort: tier.effort}, tier
		}
		return &llm.ReasoningConfig{BudgetTokens: tier.budget}, tier
	}
	return nil, reasoningTier{source: reasoningSourceUnset}
}

type ResolvedModelOverride struct {
	Alias                 string
	ConfigID              string
	Config                *llm.BifrostConfig
	ReportModel           string
	MaxInputTokens        int
	CacheStrategy         string
	ReasoningBudgetTokens int
	ReasoningEffort       string
}

func (a *Agent) resolveTaskModelOverride(ctx context.Context, metadata map[string]any, logger *slog.Logger) (*ResolvedModelOverride, string) {
	if metadata == nil {
		return nil, ""
	}
	resolved, alias, errReason := resolveModelOverrideMetadata(
		ctx, metadata, a.svc.Bindings(), a.svc.ModelResolver(), logger,
	)
	if errReason != "" || resolved == nil {
		return nil, errReason
	}
	cfg, err := samapp.ParseModelConfig(resolved, logger)
	if err != nil || cfg == nil || cfg.Model == "" {
		if err != nil {
			logger.Error("model_override config could not be parsed", "error", err)
		}
		return nil, ""
	}
	if b := a.svc.Bindings(); b != nil {
		cfg.Grants = b.GrantTokens()
	}
	effort, offending, effortRejected := samapp.ExtractReasoningEffort(resolved)
	if effortRejected {
		logger.Warn("invalid thinking.effort, ignoring",
			"source", "taskOverride", "model", cfg.Model, "value", offending)
	}
	reportModel, _ := resolved["model"].(string)
	maxInput, _ := readIntValue(resolved["max_input_tokens"])
	override := &ResolvedModelOverride{
		Alias:                 alias,
		ConfigID:              samapp.ModelConfigID(resolved),
		Config:                cfg,
		ReportModel:           reportModel,
		MaxInputTokens:        maxInput,
		CacheStrategy:         samapp.ExtractCacheStrategy(resolved),
		ReasoningBudgetTokens: samapp.ExtractReasoningBudgetTokens(resolved),
		ReasoningEffort:       effort,
	}
	logger.Info("applied per-request model override",
		"overrideModel", cfg.Model,
		"defaultModel", a.cfg.Model,
		"overrideCacheStrategy", override.CacheStrategy,
		"overrideReasoningBudget", override.ReasoningBudgetTokens,
		"overrideReasoningEffort", override.ReasoningEffort,
	)
	return override, ""
}

func resolveModelOverrideMetadata(
	ctx context.Context,
	metadata map[string]any,
	bindings runtime.ModelBindings,
	resolver runtime.ModelAliasResolver,
	logger interface {
		Debug(msg string, args ...any)
		Warn(msg string, args ...any)
		Error(msg string, args ...any)
	},
) (resolved map[string]any, alias, errReason string) {
	raw, ok := metadata["model_override"]
	if !ok || raw == nil {
		return nil, "", ""
	}

	asMap, isMap := raw.(map[string]any)
	if !isMap {
		logger.Warn("model_override has unrecognized format — ignoring",
			"type", fmt.Sprintf("%T", raw))
		delete(metadata, "model_override")
		return nil, "", ""
	}
	alias, aliasOK := asMap["model_id"].(string)
	if !aliasOK || alias == "" {
		logger.Warn("model_override has unrecognized format — ignoring (missing model_id)")
		delete(metadata, "model_override")
		return nil, "", ""
	}

	var err error
	switch {
	case bindings != nil:
		resolved, err = bindings.ResolveRawOrFetch(ctx, alias)
	case resolver != nil:
		resolved, err = resolver.Resolve(ctx, alias)
	default:
		logger.Error("model_override supplied but no bindings registry or resolver is configured",
			"alias", alias)
		return nil, alias, "model config not available"
	}
	if err != nil || len(resolved) == 0 {
		logger.Warn("model override resolution failed", "alias", alias, "error", err)
		return nil, alias, "alias '" + alias + "' not found or resolution timed out"
	}
	if samapp.IsDecisionModel(resolved) {
		logger.Warn("model override names a decision model; a chat model is required", "alias", alias)
		return nil, alias, "alias '" + alias + "' is a decision model, not a chat model"
	}

	metadata["model_override"] = resolved
	return resolved, alias, ""
}
