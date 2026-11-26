// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type agentSnapshotSource interface {
	GetByName(ctx context.Context, name string) (*store.Agent, []string, []string, []string, []string, error)
	GetByID(ctx context.Context, id string) (*store.Agent, []string, []string, []string, []string, error)
}

func agentIDFromInstanceName(name string) (string, bool) {
	id, kind, ok := a2a.IDFromInstanceName(name)
	if !ok || kind != a2a.KindAgent {
		return "", false
	}
	return id, true
}

type connectorSnapshotSource interface {
	GetByID(ctx context.Context, id string) (*store.Connector, error)
}

type agentRegistrySource interface {
	Get(name string) *a2a.AgentCard
}

type modelSnapshotResolver func(aliasOrID string) (raw map[string]any, snapshot map[string]any)

type modelSnapshotLookup func(ctx context.Context, aliasOrID string) (raw map[string]any, snapshot map[string]any, err error)

var errNoDecisionModelLookup = errors.New("no decision model lookup is configured")

const toolsExtensionURI = "https://solace.com/a2a/extensions/sam/tools"

func (s *RunService) buildAgentSnapshot(ctx context.Context, agentName string) map[string]any {
	if s.agentRepo != nil {
		agent, toolsets, inputModes, outputModes, connectorIDs, err := s.agentRepo.GetByName(ctx, agentName)
		if err != nil {
			s.logger.Warn("eval: agent repo lookup failed; falling back to registry", "agentName", agentName, "error", err)
		} else if agent != nil {
			return s.snapshotFromDBAgent(ctx, agent, toolsets, inputModes, outputModes, connectorIDs)
		}
		if id, ok := agentIDFromInstanceName(agentName); ok {
			agent, toolsets, inputModes, outputModes, connectorIDs, err := s.agentRepo.GetByID(ctx, id)
			if err != nil {
				s.logger.Warn("eval: agent repo ID lookup failed; falling back to registry", "agentName", agentName, "id", id, "error", err)
			} else if agent != nil {
				return s.snapshotFromDBAgent(ctx, agent, toolsets, inputModes, outputModes, connectorIDs)
			}
		}
	}
	if reg := s.agentRegistry; reg != nil {
		if snap := snapshotFromCard(reg.Get(agentName)); snap != nil {
			return snap
		}
	}
	return map[string]any{"name": agentName}
}

func (s *RunService) snapshotFromDBAgent(
	ctx context.Context,
	agent *store.Agent,
	toolsets, inputModes, outputModes, connectorIDs []string,
) map[string]any {
	connectors := make([]map[string]any, 0, len(connectorIDs))
	if s.connectorRepo != nil {
		for _, id := range connectorIDs {
			c, err := s.connectorRepo.GetByID(ctx, id)
			if err != nil || c == nil {
				if err != nil {
					s.logger.Warn("eval: connector lookup failed; dropping from snapshot",
						"agentName", agent.Name, "connectorID", id, "error", err)
				}
				continue
			}
			connectors = append(connectors, map[string]any{
				"id":      c.ID,
				"name":    c.Name,
				"type":    c.Type,
				"subtype": c.Subtype,
			})
		}
	}

	skills := decodeJSONArrayOrNil(agent.Skills)

	var modelConfig map[string]any
	if agent.ModelProvider != nil && *agent.ModelProvider != "" && s.modelResolver != nil {
		_, modelConfig = s.modelResolver(*agent.ModelProvider)
	}

	return map[string]any{
		"id":           agent.ID,
		"name":         agent.Name,
		"description":  agent.Description,
		"type":         agent.Type,
		"systemPrompt": agent.SystemPrompt,
		"toolsets":     toolsets,
		"connectors":   connectors,
		"skills":       skills,
		"inputModes":   inputModes,
		"outputModes":  outputModes,
		"modelConfig":  modelConfig,
		"createdAt":    agent.CreatedTime,
		"updatedAt":    agent.UpdatedTime,
		"createdBy":    agent.CreatedBy,
		"updatedBy":    agent.UpdatedBy,
	}
}

func snapshotFromCard(card *a2a.AgentCard) map[string]any {
	if card == nil {
		return nil
	}
	skills := make([]map[string]any, 0, len(card.Skills))
	for _, sk := range card.Skills {
		skills = append(skills, map[string]any{
			"id":          sk.ID,
			"name":        sk.Name,
			"description": sk.Description,
		})
	}
	return map[string]any{
		"name":        card.Name,
		"description": nilIfEmpty(card.Description),
		"skills":      skills,
		"tools":       extractToolsFromCard(card),
		"inputModes":  card.DefaultInputModes,
		"outputModes": card.DefaultOutputModes,
		"origin":      "discovered",
	}
}

func extractToolsFromCard(card *a2a.AgentCard) []map[string]any {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != toolsExtensionURI {
			continue
		}
		raw, _ := ext.Params["tools"].([]any)
		out := make([]map[string]any, 0, len(raw))
		for _, t := range raw {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			id, _ := tm["id"].(string)
			name, _ := tm["name"].(string)
			if name == "" {
				name = id
			}
			if id == "" && name == "" {
				continue
			}
			out = append(out, map[string]any{
				"id":          tm["id"],
				"name":        name,
				"description": tm["description"],
			})
		}
		return out
	}
	return []map[string]any{}
}

func buildEvaluatorSnapshot(ctx context.Context, evals []*store.EvalEvaluator, deps evaluatorSnapshotDeps) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(evals))
	for _, e := range evals {
		if e.Type != evaluatorTypeDecision {
			raw, modelSnap := resolveEvaluatorModel(e, deps.resolver)
			out = append(out, evaluatorSnapshotEntry(e, raw, modelSnap))
			continue
		}
		if err := deps.refuseDisabledDecision(e); err != nil {
			return nil, err
		}
		raw, modelSnap, err := deps.lookupDecisionModel(ctx, e)
		if err != nil {
			return nil, fmt.Errorf("evaluator %s: look up decision model %q: %w", e.ID, derefString(e.Model), err)
		}
		dict := evaluatorSnapshotEntry(e, raw, modelSnap)
		cfg, err := decisionSnapshot(e, raw, modelSnap, deps)
		if err != nil {
			return nil, err
		}
		dict["decision_config"] = cfg
		out = append(out, dict)
	}
	return out, nil
}

type evaluatorSnapshotDeps struct {
	resolver       modelSnapshotResolver
	decisionLookup modelSnapshotLookup
	registry       *features.Registry
	targets        map[string]*store.EvalEvaluator
	logger         *slog.Logger
}

func (d evaluatorSnapshotDeps) refuseDisabledDecision(e *store.EvalEvaluator) error {
	if d.registry == nil || decisionEvaluatorsEnabled(d.registry) {
		return nil
	}
	msg := fmt.Sprintf("Decision evaluator %q cannot run: %s.", e.Name, decisionDisabledMessage())
	return &ValidationError{Message: msg, Field: "evaluatorIds", Details: map[string][]string{"evaluatorIds": {msg}}}
}

func (d evaluatorSnapshotDeps) lookupDecisionModel(ctx context.Context, e *store.EvalEvaluator) (raw, modelSnap map[string]any, err error) {
	if e.Model == nil || *e.Model == "" {
		return nil, nil, nil
	}
	if d.decisionLookup == nil {
		return nil, nil, errNoDecisionModelLookup
	}
	return d.decisionLookup(ctx, *e.Model)
}

func resolveEvaluatorModel(e *store.EvalEvaluator, resolver modelSnapshotResolver) (raw, modelSnap map[string]any) {
	judged := e.Type == evaluatorTypeLLMJudge
	if resolver == nil || !judged || e.Model == nil || *e.Model == "" {
		return nil, nil
	}
	return resolver(*e.Model)
}

func evaluatorSnapshotEntry(e *store.EvalEvaluator, raw, modelSnap map[string]any) map[string]any {
	dict := map[string]any{
		"id":              e.ID,
		"name":            e.Name,
		"description":     e.Description,
		"type":            e.Type,
		"heuristic_type":  e.HeuristicType,
		"is_default":      e.IsDefault,
		"model":           e.Model,
		"prompt_template": e.PromptTemplate,
	}
	if e.ChoiceScores != nil {
		var cs map[string]any
		if err := json.Unmarshal([]byte(*e.ChoiceScores), &cs); err == nil {
			dict["choice_scores"] = cs
		}
	}
	if m, ok := raw["model"].(string); ok {
		dict["resolved_model_name"] = m
	}
	if modelSnap != nil {
		dict["modelConfig"] = modelSnap
	}
	return dict
}

func decisionSnapshot(e *store.EvalEvaluator, raw, modelSnap map[string]any, deps evaluatorSnapshotDeps) (*decisionRunConfig, error) {
	stored, err := parseDecisionConfig(e.DecisionConfig)
	if err != nil || stored == nil {
		return nil, fmt.Errorf("evaluator %s: decision settings could not be read: %w", e.ID, cmp.Or(err, errors.New("missing")))
	}
	if raw == nil || !samapp.IsDecisionModel(raw) {
		state := "is not configured"
		if raw != nil {
			state = "is now a chat model"
		}
		msg := fmt.Sprintf("Decision evaluator %q names decision model %q, which %s. Register a decision model or change the evaluator's model before running.",
			e.Name, derefString(e.Model), state)
		return nil, &ValidationError{Message: msg, Field: "evaluatorIds", Details: map[string][]string{"evaluatorIds": {msg}}}
	}
	if _, err := choiceScoresOf(e); err != nil {
		return nil, err
	}
	cfg := &decisionRunConfig{DecisionConfig: *stored, ResolvedModel: freezeDecisionModel(*e.Model, raw, modelSnap, deps.logger)}
	if id := cfg.EscalateToEvaluatorID; id != nil {
		if target := deps.targets[*id]; target != nil {
			targetRaw, targetSnap := resolveEvaluatorModel(target, deps.resolver)
			cfg.EscalateTo = evaluatorSnapshotEntry(target, targetRaw, targetSnap)
		}
	}
	return cfg, nil
}

func enrichExperimentModelSnapshots(
	models []map[string]any,
	resolver modelSnapshotResolver,
) []map[string]any {
	if resolver == nil {
		return models
	}
	out := make([]map[string]any, 0, len(models))
	for _, mc := range models {
		entry := make(map[string]any, len(mc)+2)
		maps.Copy(entry, mc)
		mcID, _ := entry["modelConfigId"].(string)
		if mcID != "" {
			raw, snapshot := resolver(mcID)
			if raw != nil {
				if m, ok := raw["model"].(string); ok {
					entry["resolvedModel"] = m
				}
			}
			if snapshot != nil {
				entry["modelConfig"] = snapshot
			}
		}
		out = append(out, entry)
	}
	return out
}

func decodeJSONArrayOrNil(raw *string) []any {
	if raw == nil || *raw == "" {
		return nil
	}
	var out []any
	if err := json.Unmarshal([]byte(*raw), &out); err != nil {
		return nil
	}
	return out
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
