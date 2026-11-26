// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"cmp"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/aliased"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/providers"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func (w *WorkflowInstance) initDecisions(svc runtime.Services) error {
	if !hasDecisionNodes(w.cfg.Nodes) {
		return nil
	}
	if !decisionNodesEnabled(svc) {
		w.logger.Warn("workflow has decision nodes but the feature is disabled; they will fail when reached",
			"feature", features.KeyWorkflowDecisionNode)
		return nil
	}
	provider := w.cfg.Decision
	if provider == nil {
		return fmt.Errorf("workflow %q: %w", w.cfg.Name, errDecisionProviderMissing)
	}
	if err := checkDecisionMediaSupported(provider, w.cfg.Nodes); err != nil {
		return fmt.Errorf("workflow %q: %w", w.cfg.Name, err)
	}
	w.warnDecisionMediaDisabled(svc)
	if provider.ModelAlias != "" {
		bindings := svc.Bindings()
		if bindings == nil {
			return fmt.Errorf("workflow %q: decision model_alias %q needs the platform model registry, which this process does not have; use an inline api_base/api_key block instead",
				w.cfg.Name, provider.ModelAlias)
		}
		evaluator, err := aliased.New(aliased.Config{
			Alias:         provider.ModelAlias,
			Bindings:      bindings,
			HTTPClient:    svc.HTTPClient(),
			Logger:        w.logger,
			Timeout:       time.Duration(provider.TimeoutSeconds) * time.Second,
			MaxStateBytes: provider.MaxStateBytes,
			Instrument: func(client decision.Evaluator) decision.Evaluator {
				return w.instrumentedDecisions(client, provider.ModelAlias, svc)
			},
		})
		if err != nil {
			return fmt.Errorf("workflow %q: %w", w.cfg.Name, err)
		}
		w.decisions = evaluator
		return nil
	}
	client, err := providers.NewEvaluator(providers.Config{Provider: provider.Provider, ClientConfig: decision.ClientConfig{
		BaseURL:       provider.APIBase,
		APIKey:        provider.APIKey,
		Timeout:       time.Duration(provider.TimeoutSeconds) * time.Second,
		MaxStateBytes: provider.MaxStateBytes,
		HTTPClient:    svc.HTTPClient(),
		Logger:        w.logger,
	}})
	if err != nil {
		if provider.ReplacedWorkflowBlock {
			return fmt.Errorf("workflow %q: decision provider: %s: %w", w.cfg.Name, replacedDecisionBlockHint, err)
		}
		return fmt.Errorf("workflow %q: decision provider: %w", w.cfg.Name, err)
	}
	if defaultModel := providers.DefaultModel(provider.Provider); cmp.Or(provider.Model, defaultModel) == defaultModel {
		w.logger.Warn("decision provider model is not pinned; routing can change when the provider moves the alias",
			"provider", provider.Provider, "model", defaultModel)
	}
	w.decisions = w.instrumentedDecisions(client, cmp.Or(provider.Model, providers.DefaultModel(provider.Provider)), svc)
	return nil
}

func (w *WorkflowInstance) instrumentedDecisions(evaluator decision.Evaluator, model string, svc runtime.Services) decision.Evaluator {
	return observability.NewInstrumentedEvaluator(evaluator, model, "workflow-"+cmp.Or(w.cfg.DisplayName, w.cfg.Name), svc.Metrics())
}

const replacedDecisionBlockHint = "the app_config decision block sets api_base, api_key or model_alias, so it replaced the workflow's decision block whole; " +
	"complete its endpoint, or keep only timeout_seconds, max_state_bytes, max_images, max_image_bytes, max_media, max_media_bytes and model there to tune the workflow's block"

func decisionNodesEnabled(svc runtime.Services) bool {
	return svc != nil && svc.FeatureRegistry() != nil &&
		svc.FeatureRegistry().IsEnabled(features.KeyWorkflowDecisionNode)
}

func (w *WorkflowInstance) decisionModel() string {
	if w.cfg.Decision == nil {
		return ""
	}
	return w.cfg.Decision.Model
}

func (w *WorkflowInstance) decisionModelKey() string {
	if w.cfg.Decision == nil {
		return ""
	}
	return w.cfg.Decision.ModelAlias
}

func errDecisionsDisabled(nodeID string) error {
	return fmt.Errorf("decision node %q cannot run: the %s feature is disabled (set %s=true)",
		nodeID, features.KeyWorkflowDecisionNode, features.EnvVarName(features.KeyWorkflowDecisionNode))
}

func addVizDecisionFields(nodeMap map[string]any, n *NodeConfig) {
	questions := make([]map[string]any, len(n.Questions))
	for i, q := range n.Questions {
		entry := map[string]any{"key": q.Key, "type": string(q.Type), "instructions": q.Instructions}
		if len(q.Criteria) > 0 {
			entry["criteria"] = q.CriterionKeys()
		}
		questions[i] = entry
	}
	nodeMap["questions"] = questions
	if n.RouteOn != "" {
		nodeMap["route_on"] = n.RouteOn
		nodeMap["routes"] = n.Routes
	}
	if n.MinConfidence > 0 {
		nodeMap["min_confidence"] = n.MinConfidence
	}
	if n.Escalate != "" {
		nodeMap["escalate"] = n.Escalate
	}
	if n.Escalate != "" {
		nodeMap["escalation_mode"] = n.resolvedEscalationMode()
	}
	if n.Default != "" {
		nodeMap["default"] = n.Default
	}
	if n.OnProviderError != "" {
		nodeMap["on_provider_error"] = n.OnProviderError
	}
}
