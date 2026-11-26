// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type toolLLMChatFunc func(ctx context.Context, choice toolModelChoice, componentName string, inv *remotetool.InvokeParams, req *llm.ChatRequest) (*llm.ChatResponse, error)

type toolLLMTarget struct {
	client        llm.Client
	alias         string
	spend         *llm.SpendAttribution
	observability llm.ObservabilityContext
}

func (w *Worker) resolveToolLLMTarget(ctx context.Context, envLLM llm.Client, choice toolModelChoice, componentName string, inv *remotetool.InvokeParams) (*toolLLMTarget, error) {
	alias, tier := w.resolveToolAlias(choice.alias)
	if choice.requested {
		if err := w.checkRequestedChatModel(ctx, alias); err != nil {
			return nil, err
		}
	}
	attribution := invocationSpend(inv)
	publisher := w.spendPublisher()
	recording := publisher != nil && attribution != nil
	client, configID, resolveErr := w.resolveToolLLMClient(ctx, alias, envLLM, recording)
	if choice.requested && resolveErr != nil {
		return nil, fmt.Errorf("%w: model alias %q is not resolvable: %w", errChatModelNotPermitted, alias, resolveErr)
	}
	if tier.requiresResolution() && resolveErr != nil {
		return nil, fmt.Errorf("%s %q is not resolvable: %w", tier, alias, resolveErr)
	}
	if resolveErr != nil {
		w.logger.Debug("tool LLM alias unresolved; using the default env client",
			"alias", alias, "tier", tier.String(), "tool", componentName, "error", resolveErr)
	}
	if client == nil {
		return nil, fmt.Errorf("LLM calls not available: no %q model binding and no default LLM client", alias)
	}
	modelKey, boundModel := alias, ""
	if resolveErr != nil {
		modelKey, boundModel = "", w.cfg.EnvModelName
	}
	if publisher != nil && attribution == nil {
		w.noteMissingSpend(componentName, inv)
	}
	return &toolLLMTarget{
		client:        spend.NewMeteredClient(client, boundModel, publisher, w.svc.BudgetGate()),
		alias:         alias,
		spend:         toolCallSpend(attribution, componentName, modelKey, configID),
		observability: llm.ObservabilityContext{ComponentName: "tool-" + componentName},
	}, nil
}

func (w *Worker) newToolChatCallback(envLLM llm.Client) toolLLMChatFunc {
	if envLLM == nil && w.svc.Bindings() == nil {
		return nil
	}
	return func(ctx context.Context, choice toolModelChoice, componentName string, inv *remotetool.InvokeParams, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		target, err := w.resolveToolLLMTarget(ctx, envLLM, choice, componentName, inv)
		if err != nil {
			return nil, err
		}
		req.Observability = target.observability
		req.Spend = target.spend
		markChatCacheBreakpoints(req)
		return llm.ChatCompletionWithTemperatureFallback(
			ctx, target.client, req, &w.tempRejects, target.alias, w.logger.With("tool", componentName))
	}
}

func (w *Worker) checkRequestedChatModel(ctx context.Context, alias string) error {
	b := w.svc.Bindings()
	if b == nil {
		return fmt.Errorf("%w: model alias %q is not resolvable: this STR has no model bindings registry", errChatModelNotPermitted, alias)
	}
	raw, err := b.ResolveRawOrFetch(ctx, alias)
	if err != nil {
		return fmt.Errorf("%w: model alias %q is not resolvable: %w", errChatModelNotPermitted, alias, err)
	}
	if len(raw) == 0 {
		return fmt.Errorf("%w: model alias %q is not resolvable", errChatModelNotPermitted, alias)
	}
	if samapp.IsDecisionModel(raw) {
		return fmt.Errorf("%w: model alias %q is a decision model, not a chat model", errChatModelNotPermitted, alias)
	}
	return nil
}

func markChatCacheBreakpoints(req *llm.ChatRequest) {
	breakpoint := &llm.CacheControl{Type: "ephemeral"}
	if n := len(req.Tools); n > 0 {
		req.Tools[n-1].CacheControl = breakpoint
	}
	if n := len(req.Messages); n > 0 {
		req.Messages[n-1].CacheControl = breakpoint
	}
}
