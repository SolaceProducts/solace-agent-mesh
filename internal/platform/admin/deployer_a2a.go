// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type A2AProxyDeployer struct {
	a2aRepo   *store.A2AAgentRepo
	namespace string
}

func NewA2AProxyDeployer(a2aRepo *store.A2AAgentRepo, namespace string) *A2AProxyDeployer {
	return &A2AProxyDeployer{a2aRepo: a2aRepo, namespace: namespace}
}

func (*A2AProxyDeployer) Kind() string { return "proxy" }

func (d *A2AProxyDeployer) Validate(ctx context.Context, input DeployInput) error {
	if input.Agent == nil {
		return &ValidationError{Message: "agent is required"}
	}
	if input.Action == DeployActionUndeploy {
		return nil
	}
	a2a, err := d.a2aRepo.GetByID(ctx, input.Agent.ID)
	if err != nil {
		return fmt.Errorf("load a2a agent: %w", err)
	}
	if a2a == nil {
		return &ValidationError{Message: fmt.Sprintf("a2a_agents row missing for agent %s", input.Agent.ID)}
	}
	if a2a.AgentURL == "" {
		return &ValidationError{Message: "remote agent URL is required"}
	}
	return requireRemoteAgentAuthFilled(a2a)
}

func (d *A2AProxyDeployer) InstanceName(agent *store.Agent) string {
	if agent == nil {
		return ""
	}
	return AgentInstanceName(agent)
}

func (d *A2AProxyDeployer) BuildPayloads(ctx context.Context, input DeployInput) (BuildResult, error) {
	if input.Agent == nil {
		return BuildResult{}, fmt.Errorf("agent is nil")
	}
	a2a, err := d.a2aRepo.GetByID(ctx, input.Agent.ID)
	if err != nil {
		return BuildResult{}, fmt.Errorf("load a2a agent: %w", err)
	}
	if a2a == nil {
		return BuildResult{}, &NotFoundError{Resource: "remote agent", ID: input.Agent.ID}
	}

	appConfig := buildA2AProxyAppConfig(a2a, d.namespace)
	instanceName := A2AAgentInstanceName(a2a)
	payload := ControlPayload{
		"kind":       "proxy",
		"name":       instanceName,
		"app_config": appConfig,
	}

	dataSnapshot := DeployedRemoteAgentInfo{
		AgentURL:              a2a.AgentURL,
		Name:                  a2a.Name,
		Description:           derefStr(a2a.Description),
		AgentCardLocation:     a2a.AgentCardLocation,
		AgentCardURL:          derefStr(a2a.AgentCardURL),
		AgentCardAuthType:     derefStr(a2a.AgentCardAuthType),
		AgentAuthType:         derefStr(a2a.AgentAuthType),
		RequestTimeoutSeconds: a2a.RequestTimeoutSeconds,
		Skills:                unmarshalJSONAny(a2a.Skills),
		InputModes:            unmarshalJSONAny(a2a.InputModes),
		OutputModes:           unmarshalJSONAny(a2a.OutputModes),
		ProtocolVersion:       storedProtocolPin(a2a),
		TrustedCardJWKSURLs:   storedTrustedCardJWKSURLs(a2a),
		RequireSignedCard:     a2a.RequireSignedCard,
	}
	snapshot, err := json.Marshal(dataSnapshot)
	if err != nil {
		return BuildResult{}, fmt.Errorf("marshal snapshot: %w", err)
	}

	payloads := []ControlPayload{payload}
	card := &agentCardSurface{Name: a2a.Name, Description: derefStr(a2a.Description)}
	surfaceHashes, err := computeAgentSurfaceHashes(ctx, payloads, card, nil)
	if err != nil {
		return BuildResult{}, fmt.Errorf("compute agent surface hashes: %w", err)
	}

	return BuildResult{
		Payloads:      payloads,
		SnapshotYAML:  string(snapshot),
		DataSnapshot:  dataSnapshot,
		ConfigHash:    computeA2AAgentConfigHash(a2a),
		SurfaceHashes: surfaceHashes,
	}, nil
}

func (*A2AProxyDeployer) ReplayFromSnapshot(_ string) ([]ControlPayload, error) {
	return nil, ErrNoReplay
}

func proxyAuthConfigs(a *store.A2AAgent) (agent, card map[string]any) {
	if agentAuthType := derefStr(a.AgentAuthType); agentAuthType != "" && agentAuthType != authTypeNone && a.AgentAuthConfig != nil {
		if agent = unmarshalAuthConfig(*a.AgentAuthConfig); agent != nil {
			normalizeAuthConfig(agent, agentAuthType)
		}
	}
	if cardAuthType := derefStr(a.AgentCardAuthType); cardAuthType != "" && cardAuthType != authTypeNone && a.AgentCardAuthConfig != nil {
		if card = unmarshalAuthConfig(*a.AgentCardAuthConfig); card != nil {
			normalizeAuthConfig(card, cardAuthType)
		}
	}
	return agent, card
}

func buildA2AProxyAppConfig(a *store.A2AAgent, namespace string) map[string]any {
	cardLocation := a.AgentCardLocation
	appConfig := map[string]any{
		"namespace":    cmp.Or(namespace, "solace-agent-mesh"),
		"url":          a.AgentURL,
		"display_name": a.Name,
	}

	if cardLocation == "custom" && a.AgentCardURL != nil && *a.AgentCardURL != "" {
		appConfig["agent_card_path"] = *a.AgentCardURL
	} else if cardLocation == "none" {
		appConfig["agent_card_data"] = buildStaticCardData(a)
	}

	if a.RequestTimeoutSeconds > 0 {
		appConfig["request_timeout_seconds"] = a.RequestTimeoutSeconds
	}

	agentAuth, cardAuth := proxyAuthConfigs(a)
	if agentAuth != nil {
		appConfig["authentication"] = agentAuth
		if cardLocation == "well_known" || cardLocation == "custom" {
			appConfig["use_auth_for_agent_card"] = true
		}
	}
	if cardAuth != nil {
		appConfig["agent_card_authentication"] = cardAuth
	}

	if a.TaskHeaders != nil {
		var headers map[string]string
		if json.Unmarshal([]byte(*a.TaskHeaders), &headers) == nil && len(headers) > 0 {
			appConfig["task_headers"] = headers
		}
	}

	if a.AgentCardHeaders != nil {
		var headers map[string]string
		if json.Unmarshal([]byte(*a.AgentCardHeaders), &headers) == nil && len(headers) > 0 {
			appConfig["agent_card_headers"] = headers
		}
	}

	addCardTrustToProxyConfig(appConfig, a)
	return appConfig
}

func buildStaticCardData(a *store.A2AAgent) map[string]any {
	cardData := map[string]any{"name": a.Name}
	if desc := derefStr(a.Description); desc != "" {
		cardData["description"] = desc
	}
	if skills := unmarshalJSONAny(a.Skills); skills != nil {
		cardData["skills"] = skills
	}
	if modes := unmarshalJSONAny(a.InputModes); modes != nil {
		cardData["defaultInputModes"] = modes
	}
	if modes := unmarshalJSONAny(a.OutputModes); modes != nil {
		cardData["defaultOutputModes"] = modes
	}
	return cardData
}
