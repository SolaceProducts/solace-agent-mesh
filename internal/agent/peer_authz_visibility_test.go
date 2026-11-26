// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func registerDiscoveredPeer(t *testing.T, agent *Agent, name string) {
	t.Helper()
	card := a2a.AgentCard{
		Name:        name,
		Description: name + " description",
		URL:         "solace:ns/a2a/v1/agent/request/" + name,
	}
	data, _ := json.Marshal(card)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})
	if agent.tools.GetTool("peer_"+name) == nil {
		t.Fatalf("setup: peer_%s should be registered", name)
	}
}

func TestBuildLLMTools_HidesUnauthorizedPeer(t *testing.T) {
	agent := newEnterpriseAgentHarness(t)
	registerDiscoveredPeer(t, agent, "allowed-agent")
	registerDiscoveredPeer(t, agent, "restricted-agent")

	identity := &auth.Identity{
		UserID: "sam_dev_user",
		Scopes: []string{"agent:allowed-agent:invoke"},
	}

	names := map[string]bool{}
	for _, d := range agent.buildLLMTools(t.Context(), identity, nil, "") {
		names[d.Name] = true
	}

	if !names["peer_allowed-agent"] {
		t.Errorf("peer_allowed-agent should be visible (user holds agent:allowed-agent:invoke)")
	}
	if names["peer_restricted-agent"] {
		t.Errorf("peer_restricted-agent must be hidden: user lacks agent:restricted-agent:invoke, but the LLM can still see and attempt it")
	}
}

func TestBuildPeerInfo_HidesUnauthorizedPeer(t *testing.T) {
	agent := newEnterpriseAgentHarness(t)
	registerDiscoveredPeer(t, agent, "allowed-agent")
	registerDiscoveredPeer(t, agent, "restricted-agent")

	identity := &auth.Identity{
		UserID: "sam_dev_user",
		Scopes: []string{"agent:allowed-agent:invoke"},
	}

	got := map[string]bool{}
	for _, p := range agent.buildPeerInfo(context.Background(), identity) {
		got[p.Name] = true
	}

	if !got["allowed-agent"] {
		t.Errorf("allowed-agent should appear in peer info")
	}
	if got["restricted-agent"] {
		t.Errorf("restricted-agent must not appear in peer info (user lacks invoke scope)")
	}
}

func TestBuildLLMTools_WorkflowPeerGatedOnInvokeScope(t *testing.T) {
	setup := func(t *testing.T) *Agent {
		agent := newEnterpriseAgentHarness(t)
		card := a2a.AgentCard{
			Name:        "flow-agent",
			Description: "a workflow peer",
			URL:         "solace:ns/a2a/v1/agent/request/flow-agent",
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{
					URI:    ExtensionURIAgentType,
					Params: map[string]any{"type": "workflow"},
				}},
			},
		}
		data, _ := json.Marshal(card)
		agent.handleDiscoveryMessage(&broker.Message{Payload: data})
		if agent.tools.GetTool("workflow_flow_agent") == nil {
			t.Fatal("setup: workflow_flow_agent should be registered")
		}
		return agent
	}

	visible := func(agent *Agent, scopes []string) bool {
		identity := &auth.Identity{UserID: "sam_dev_user", Scopes: scopes}
		for _, d := range agent.buildLLMTools(context.Background(), identity, nil, "") {
			if d.Name == "workflow_flow_agent" {
				return true
			}
		}
		return false
	}

	t.Run("hidden without invoke scope", func(t *testing.T) {
		if visible(setup(t), []string{"agent:other:invoke"}) {
			t.Fatal("workflow_flow_agent must be hidden: user lacks workflow:flow-agent:invoke")
		}
	})

	t.Run("agent-invoke does not reveal workflow", func(t *testing.T) {
		if visible(setup(t), []string{"agent:flow-agent:invoke", "agent:*:invoke"}) {
			t.Fatal("workflow_flow_agent must be hidden: agent invoke scopes do not grant workflow invoke")
		}
	})

	t.Run("visible with invoke scope", func(t *testing.T) {
		if !visible(setup(t), []string{"workflow:flow-agent:invoke"}) {
			t.Fatal("workflow_flow_agent should be visible: user holds workflow:flow-agent:invoke")
		}
	})
}

func TestBuildLLMTools_WorkflowPeerRequiresWorkflowScope(t *testing.T) {
	const wfCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const dashedUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"
	const toolName = "workflow_" + wfCard

	setup := func(t *testing.T) *Agent {
		agent := newEnterpriseAgentHarness(t)
		card := a2a.AgentCard{
			Name:        wfCard,
			Description: "a deployed workflow peer",
			URL:         "solace:ns/a2a/v1/agent/request/" + wfCard,
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{
					URI:    ExtensionURIAgentType,
					Params: map[string]any{"type": "workflow"},
				}},
			},
		}
		data, _ := json.Marshal(card)
		agent.handleDiscoveryMessage(&broker.Message{Payload: data})
		if agent.tools.GetTool(toolName) == nil {
			t.Fatalf("setup: %s should be registered", toolName)
		}
		return agent
	}

	visible := func(agent *Agent, scopes []string) bool {
		identity := &auth.Identity{UserID: "sam_dev_user", Scopes: scopes}
		for _, d := range agent.buildLLMTools(context.Background(), identity, nil, "") {
			if d.Name == toolName {
				return true
			}
		}
		return false
	}

	if visible(setup(t), []string{"agent:*:invoke"}) {
		t.Error("deployed workflow peer must be hidden from an agent:*:invoke holder")
	}
	if !visible(setup(t), []string{"workflow:" + dashedUUID + ":invoke"}) {
		t.Error("deployed workflow peer must be visible to a workflow:<uuid>:invoke holder")
	}
	if !visible(setup(t), []string{"workflow:*:invoke"}) {
		t.Error("deployed workflow peer must be visible to a workflow:*:invoke holder")
	}
}

func TestBuildLLMTools_HidesPeerMissingRequiredScopes(t *testing.T) {
	agent := newEnterpriseAgentHarness(t)
	registerDiscoveredPeer(t, agent, "open-agent")

	adminCard := a2a.AgentCard{
		Name:        "obs-agent",
		Description: "admin-only observability peer",
		URL:         "solace:ns/a2a/v1/agent/request/obs-agent",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIRequiredScopes,
				Params: map[string]any{"scopes": []string{"analytics:_:read"}},
			}},
		},
	}
	data, _ := json.Marshal(adminCard)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})
	if agent.tools.GetTool("peer_obs-agent") == nil {
		t.Fatal("setup: peer_obs-agent should be registered")
	}

	visible := func(scopes []string) map[string]bool {
		identity := &auth.Identity{UserID: "sam_dev_user", Scopes: scopes}
		names := map[string]bool{}
		for _, d := range agent.buildLLMTools(t.Context(), identity, nil, "") {
			names[d.Name] = true
		}
		return names
	}

	got := visible([]string{"agent:*:invoke"})
	if !got["peer_open-agent"] {
		t.Error("peer_open-agent should be visible (no required_scopes)")
	}
	if got["peer_obs-agent"] {
		t.Error("peer_obs-agent must be hidden: caller has agent:*:invoke but lacks analytics:_:read")
	}

	if !visible([]string{"agent:*:invoke", "analytics:_:read"})["peer_obs-agent"] {
		t.Error("peer_obs-agent should be visible to a caller holding analytics:_:read")
	}
}

func TestBuildLLMTools_NoIdentity_CommunityShowsAllPeers(t *testing.T) {
	agent := New(AgentConfig{Name: "caller", Namespace: "ns"})
	if err := agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())); err != nil {
		t.Fatalf("Init: %v", err)
	}
	registerDiscoveredPeer(t, agent, "peer-a")
	registerDiscoveredPeer(t, agent, "peer-b")

	names := map[string]bool{}
	for _, d := range agent.buildLLMTools(context.Background(), nil, nil, "") {
		names[d.Name] = true
	}
	if !names["peer_peer-a"] || !names["peer_peer-b"] {
		t.Errorf("community mode (NoopAgentAuthorizer) should expose all peers; got %v", names)
	}
}
