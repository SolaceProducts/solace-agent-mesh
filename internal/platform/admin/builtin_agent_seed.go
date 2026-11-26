// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const builtinAgentActor = "system"

const errMarshalAdditionalConfigs = "marshal additional configurations: %w"

//go:embed builtin_agents/*.yaml
var builtinAgentsFS embed.FS

const builtinOrchestratorName = platpkg.BuiltinOrchestratorName

const orchestratorSeedMarkerKey = "builtin_orchestrator"

const builtinOrchestratorPriority = 1000

const builtinSentinelName = platpkg.BuiltinSentinelName

const BuiltinActivityMonitorName = platpkg.BuiltinActivityMonitorName

var builtinOrchestratorSkillRefs = []string{"sam-knowledge", "sam-docs", "sam-build-redirect", "sam-scheduling"}

var builtinSentinelSkillRefs = []string{"sam-observability", "sam-excel", "sam-docs"}

func maybeSeedBuiltinAgents(
	ctx context.Context,
	enabled bool,
	sentinelEnabled bool,
	activityMonitorEnabled bool,
	agentRepo *store.AgentRepo,
	deploymentRepo *store.AgentDeploymentRepo,
	connectorRepo *store.ConnectorRepo,
	toolPackageRepo *store.ToolPackageRepo,
	agentRegistry *platpkg.AgentRegistry,
	skillRepo *store.SkillRepo,
	logger *slog.Logger,
) {
	if !enabled {
		return
	}
	seedAgentSvc := NewAgentService(agentRepo, deploymentRepo, connectorRepo, toolPackageRepo, agentRegistry, nil, logger)
	seedAgentSvc.WithSkillRepo(skillRepo)
	if err := SeedBuiltinAgents(ctx, seedAgentSvc, agentRepo, sentinelEnabled, activityMonitorEnabled, logger); err != nil {
		logger.Warn("failed to seed built-in agents", "error", err)
	}
}

func SeedBuiltinAgents(ctx context.Context, agentSvc *AgentService, agentRepo *store.AgentRepo, sentinelEnabled, activityMonitorEnabled bool, logger *slog.Logger) error {
	if sentinelEnabled {
		sentinelReq, serr := buildSentinelRequest()
		if serr != nil {
			return fmt.Errorf("seed builtin agents: build sentinel: %w", serr)
		}
		if cerr := createBuiltinAgent(ctx, agentSvc, agentRepo, sentinelReq, builtinSentinelName, logger); cerr != nil {
			return cerr
		}
	}

	if activityMonitorEnabled {
		activityReq, aerr := buildActivityMonitorRequest()
		if aerr != nil {
			return fmt.Errorf("seed builtin agents: build activity monitor: %w", aerr)
		}
		if cerr := createBuiltinAgent(ctx, agentSvc, agentRepo, activityReq, BuiltinActivityMonitorName, logger); cerr != nil {
			return cerr
		}
	}

	orchReq, err := buildBuiltinOrchestratorRequest()
	if err != nil {
		return fmt.Errorf("seed builtin agents: build orchestrator: %w", err)
	}
	return seedOrchestratorOnce(ctx, agentSvc, agentRepo, orchReq, logger)
}

func seedOrchestratorOnce(ctx context.Context, agentSvc *AgentService, agentRepo *store.AgentRepo, req CreateAgentRequest, logger *slog.Logger) error {
	db := agentRepo.DB()

	existing, _, _, _, _, err := agentRepo.GetByName(ctx, builtinOrchestratorName)
	if err != nil {
		return fmt.Errorf("seed builtin agents: lookup %s: %w", builtinOrchestratorName, err)
	}
	if existing != nil {
		if rerr := reconcileBuiltinAgent(ctx, agentSvc, agentRepo, req, builtinOrchestratorName, logger); rerr != nil {
			return rerr
		}
		return store.MarkSeedApplied(ctx, db, orchestratorSeedMarkerKey)
	}

	seeded, err := store.IsSeedApplied(ctx, db, orchestratorSeedMarkerKey)
	if err != nil {
		return fmt.Errorf("seed builtin agents: %w", err)
	}
	if seeded {
		logger.Debug("orchestrator previously seeded and since removed; not re-creating", "name", builtinOrchestratorName)
		return nil
	}

	if cerr := createBuiltinAgent(ctx, agentSvc, agentRepo, req, builtinOrchestratorName, logger); cerr != nil {
		return cerr
	}
	return store.MarkSeedApplied(ctx, db, orchestratorSeedMarkerKey)
}

func createBuiltinAgent(ctx context.Context, agentSvc *AgentService, agentRepo *store.AgentRepo, req CreateAgentRequest, name string, logger *slog.Logger) error {
	if _, err := agentSvc.Create(ctx, req, builtinAgentActor); err != nil {
		if _, ok := errors.AsType[*ConflictError](err); ok {
			return reconcileBuiltinAgent(ctx, agentSvc, agentRepo, req, name, logger)
		}
		return fmt.Errorf("seed builtin agents: create %s: %w", name, err)
	}
	logger.Info("seeded built-in agent", "name", name)
	return nil
}

func reconcileBuiltinAgent(ctx context.Context, agentSvc *AgentService, agentRepo *store.AgentRepo, req CreateAgentRequest, name string, logger *slog.Logger) error {
	existing, _, _, _, _, err := agentRepo.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("seed builtin agents: reconcile %s: lookup: %w", name, err)
	}
	if existing == nil {
		return nil
	}
	if existing.UpdatedBy != builtinAgentActor {
		logger.Debug("built-in agent operator-customized; skipping reconcile", "name", name, "updatedBy", existing.UpdatedBy)
		return nil
	}

	live, err := agentSvc.GetByID(ctx, existing.ID)
	if err != nil {
		return fmt.Errorf("seed builtin agents: reconcile %s: load: %w", name, err)
	}
	if !builtinConfigDrifted(req, live) {
		return nil
	}
	if _, err := agentSvc.Update(ctx, existing.ID, builtinUpdateRequest(req), builtinAgentActor); err != nil {
		return fmt.Errorf("seed builtin agents: reconcile %s: update: %w", name, err)
	}
	logger.Info("reconciled built-in agent to embedded config", "name", name)
	return nil
}

func builtinConfigDrifted(desired CreateAgentRequest, live *AgentResponse) bool {
	if desired.SystemPrompt != live.SystemPrompt || desired.Description != live.Description {
		return true
	}
	if !sameStringSet(desired.Toolsets, live.Toolsets) || !sameStringSet(desired.SkillRefs, live.SkillRefs) {
		return true
	}
	if !sameStringSet(desired.InputModes, live.InputModes) || !sameStringSet(desired.OutputModes, live.OutputModes) {
		return true
	}
	if !sameStringSet(desired.ModelProvider, live.ModelProvider) {
		return true
	}
	if !sameToolsetConfigs(desired.ToolsetConfigs, live.ToolsetConfigs) {
		return true
	}
	return !sameJSONObject(desired.AdditionalConfigurations, live.AdditionalConfigurations)
}

func sameToolsetConfigs(a, b []ToolsetConfigBinding) bool {
	norm := func(tc []ToolsetConfigBinding) map[string]any {
		m := make(map[string]any, len(tc))
		for _, c := range tc {
			entry := map[string]any{}
			if len(c.ConfigValues) > 0 {
				entry["configValues"] = c.ConfigValues
			}
			if len(c.VolumeBindings) > 0 {
				entry["volumeBindings"] = c.VolumeBindings
			}
			if len(c.NetworkBindings) > 0 {
				entry["networkBindings"] = c.NetworkBindings
			}
			m[c.ToolsetName] = entry
		}
		return m
	}
	av, _ := json.Marshal(norm(a))
	bv, _ := json.Marshal(norm(b))
	return string(av) == string(bv)
}

func builtinUpdateRequest(req CreateAgentRequest) UpdateAgentRequest {
	return UpdateAgentRequest{
		Name:                     req.Name,
		Description:              req.Description,
		SystemPrompt:             req.SystemPrompt,
		Skills:                   req.Skills,
		SkillRefs:                req.SkillRefs,
		Toolsets:                 req.Toolsets,
		ToolsetConfigs:           req.ToolsetConfigs,
		InputModes:               req.InputModes,
		OutputModes:              req.OutputModes,
		ModelProvider:            req.ModelProvider,
		AdditionalConfigurations: req.AdditionalConfigurations,
	}
}

func sameStringSet(a, b []string) bool {
	as := slices.Clone(a)
	bs := slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(slices.Compact(as), slices.Compact(bs))
}

func sameJSONObject(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func buildBuiltinOrchestratorRequest() (CreateAgentRequest, error) {
	data, err := builtinAgentsFS.ReadFile("builtin_agents/orchestrator.yaml")
	if err != nil {
		return CreateAgentRequest{}, fmt.Errorf("read embedded orchestrator: %w", err)
	}

	req, err := ParseAgentYAML(data)
	if err != nil {
		return CreateAgentRequest{}, err
	}

	req.SkillRefs = builtinOrchestratorSkillRefs

	extra, err := json.Marshal(builtinOrchestratorAdditionalConfig())
	if err != nil {
		return CreateAgentRequest{}, fmt.Errorf(errMarshalAdditionalConfigs, err)
	}
	req.AdditionalConfigurations = extra

	return req, nil
}

var buildSentinelRequest = buildBuiltinSentinelRequest

func buildBuiltinSentinelRequest() (CreateAgentRequest, error) {
	data, err := builtinAgentsFS.ReadFile("builtin_agents/sentinel.yaml")
	if err != nil {
		return CreateAgentRequest{}, fmt.Errorf("read embedded sentinel: %w", err)
	}

	req, err := ParseAgentYAML(data)
	if err != nil {
		return CreateAgentRequest{}, err
	}

	req.SkillRefs = builtinSentinelSkillRefs

	req.ToolsetConfigs = []ToolsetConfigBinding{}

	extra, err := json.Marshal(builtinSentinelAdditionalConfig())
	if err != nil {
		return CreateAgentRequest{}, fmt.Errorf(errMarshalAdditionalConfigs, err)
	}
	req.AdditionalConfigurations = extra

	return req, nil
}

var buildActivityMonitorRequest = buildBuiltinActivityMonitorRequest

func buildBuiltinActivityMonitorRequest() (CreateAgentRequest, error) {
	data, err := builtinAgentsFS.ReadFile("builtin_agents/activity.yaml")
	if err != nil {
		return CreateAgentRequest{}, fmt.Errorf("read embedded activity monitor: %w", err)
	}
	req, err := ParseAgentYAML(data)
	if err != nil {
		return CreateAgentRequest{}, err
	}
	req.ToolsetConfigs = []ToolsetConfigBinding{}

	extra, err := json.Marshal(builtinActivityMonitorAdditionalConfig())
	if err != nil {
		return CreateAgentRequest{}, fmt.Errorf(errMarshalAdditionalConfigs, err)
	}
	req.AdditionalConfigurations = extra
	return req, nil
}

func builtinActivityMonitorAdditionalConfig() map[string]any {
	return withGatewayContextInjection(map[string]any{
		"agentCard": map[string]any{
			"welcome": map[string]any{
				"message": "Activity Monitor — your view into your own agent and workflow activity",
				"suggestions": []map[string]any{
					{
						"label":    "Show me today's activity",
						"prompt":   "Show me my activity report for the last 24 hours.",
						"autoSend": true,
					},
					{
						"label":    "How many tokens did I use last week?",
						"prompt":   "How many tokens did my agents and workflows consume over the last 7 days?",
						"autoSend": true,
					},
					{
						"label":    "Which of my tasks failed recently?",
						"prompt":   "What tasks of mine failed in the last 24 hours, and which agents ran them?",
						"autoSend": true,
					},
				},
			},
		},
	})
}

func builtinSentinelAdditionalConfig() map[string]any {
	cfg := map[string]any{
		"requiredScopes": []string{rbac.ScopeAnalyticsRead},
		"agentCard": map[string]any{
			"welcome": map[string]any{
				"message": "Observability Agent — your view into mesh health, failures, usage, and logs",
				"suggestions": []map[string]any{
					{
						"label":    "Mesh health",
						"prompt":   "Give me a health summary of the mesh over the last 24 hours.",
						"autoSend": true,
					},
					{
						"label":    "Recent failures",
						"prompt":   "What tasks failed in the last 24 hours, and why?",
						"autoSend": true,
					},
					{
						"label":    "Usage report",
						"prompt":   "Show me token usage by day for the last 7 days, with a chart.",
						"autoSend": true,
					},
				},
			},
		},
	}
	return withGatewayContextInjection(cfg)
}

func builtinOrchestratorAdditionalConfig() map[string]any {
	return withGatewayContextInjection(map[string]any{
		"interAgentCommunication": map[string]any{
			"allowList": []string{"*"},
		},
		"agentCard": map[string]any{
			"priority": builtinOrchestratorPriority,
			"welcome": map[string]any{
				"message": "What should we work on?",
				"suggestions": []map[string]any{
					{
						"label":    "What can SAM do?",
						"prompt":   "Show me a few real examples of workers you can build — like a support rep, a code reviewer, or an ops watchdog. What do they actually do day to day?",
						"autoSend": true,
					},
					{
						"label":    "What's cool in SAM?",
						"prompt":   "What are the most impressive things SAM can do that would surprise me? Show me the wow factor.",
						"autoSend": true,
					},
					{
						"label":    "How is SAM different?",
						"prompt":   "What makes SAM different from just using ChatGPT or other AI tools? Why would I want an event-driven AI workforce instead?",
						"autoSend": true,
					},
				},
			},
		},
	})
}

func withGatewayContextInjection(cfg map[string]any) map[string]any {
	cfg["injectSystemPurpose"] = true
	cfg["injectResponseFormat"] = true
	cfg["injectUserProfile"] = true
	return cfg
}
