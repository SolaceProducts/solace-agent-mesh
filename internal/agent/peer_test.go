// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestPeerToolName(t *testing.T) {
	pt := &peerAgentTool{peerName: "data-agent"}
	if pt.Name() != "peer_data-agent" {
		t.Errorf("name = %q", pt.Name())
	}
}

func TestPeerToolDescription(t *testing.T) {
	pt := &peerAgentTool{
		peerName: "helper",
		agent:    New(AgentConfig{Name: "caller"}),
		card: a2a.AgentCard{
			Description: "A helpful agent",
			Skills: []a2a.AgentSkill{
				{Name: "Search"},
				{Name: "Analyze"},
			},
		},
	}

	desc := pt.Description()
	if desc == "" {
		t.Error("description should not be empty")
	}
	if !contains(desc, "helpful agent") {
		t.Errorf("missing card description: %q", desc)
	}
	if !contains(desc, "Search") || !contains(desc, "Analyze") {
		t.Errorf("missing skills: %q", desc)
	}
}

func TestPeerToolDescriptionUsesDisplayName(t *testing.T) {
	pt := &peerAgentTool{
		peerName: "agent_019e9445_40fe",
		card: a2a.AgentCard{
			Name:        "agent_019e9445_40fe",
			Description: "A helpful agent",
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{
					URI:    ExtensionURIDisplayName,
					Params: map[string]any{"display_name": "Customer Support"},
				}},
			},
		},
	}

	desc := pt.Description()
	if !contains(desc, "Customer Support") {
		t.Errorf("description should name the agent by its display name: %q", desc)
	}
	if contains(desc, `"agent_019e9445_40fe" agent`) {
		t.Errorf("description should not name the agent by its internal slug: %q", desc)
	}
}

func TestPeerToolDescriptionListsAttachedSkillsButNotBuiltins(t *testing.T) {
	pt := &peerAgentTool{
		peerName: "helper",
		agent:    New(AgentConfig{Name: "caller"}),
		card: a2a.AgentCard{
			Description: "A helpful agent",
			Skills:      []a2a.AgentSkill{{Name: "Search"}},
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{
					URI: "https://solace.com/a2a/extensions/sam/skills",
					Params: map[string]any{
						"skills": []any{
							map[string]any{"name": "nw-freeze-policy", "description": "Freeze rules"},
							map[string]any{"name": "sam-templates", "description": "Built-in"},
						},
					},
				}},
			},
		},
	}

	desc := pt.Description()
	if !contains(desc, "Search") || !contains(desc, "nw-freeze-policy") {
		t.Errorf("description should list the card's and the attached skills: %q", desc)
	}
	if contains(desc, "sam-templates") {
		t.Errorf("built-in skill listed in peer description: %q", desc)
	}
}

func TestPeerToolParameters(t *testing.T) {
	pt := &peerAgentTool{peerName: "test"}
	params := pt.Parameters()

	if params.Type != "object" {
		t.Errorf("type = %q", params.Type)
	}
	if _, ok := params.Properties["task_description"]; !ok {
		t.Error("missing task_description property")
	}
	if _, ok := params.Properties["artifacts"]; !ok {
		t.Error("missing artifacts property")
	}
	if len(params.Required) != 1 || params.Required[0] != "task_description" {
		t.Errorf("required = %v", params.Required)
	}
}

func TestPeerToolIsLongRunning(t *testing.T) {
	pt := &peerAgentTool{peerName: "test"}
	if !pt.IsLongRunning() {
		t.Error("peer tool should be long-running")
	}
}

func TestPeerToolImplementsTool(t *testing.T) {
	var _ tool.BuiltinTool = (*peerAgentTool)(nil)
}

func TestIsPeerAllowedDefault(t *testing.T) {
	agent := New(AgentConfig{Name: "test"})
	if !agent.isPeerAllowed(a2a.AgentCard{Name: "any-agent"}) {
		t.Error("should allow all by default")
	}
}

func TestIsPeerAllowed_ExplicitEmptyDeniesAll(t *testing.T) {
	agent := New(AgentConfig{
		Name:          "test",
		PeerAllowList: []string{},
	})
	if agent.isPeerAllowed(a2a.AgentCard{Name: "any-agent"}) {
		t.Error("explicit empty allow list should deny every peer")
	}
	if agent.isPeerAllowed(a2a.AgentCard{Name: "another-agent"}) {
		t.Error("explicit empty allow list should deny every peer (second card)")
	}
}

func TestIsPeerAllowed_NilVsEmptySemantics(t *testing.T) {
	nilAgent := New(AgentConfig{Name: "test", PeerAllowList: nil})
	if !nilAgent.isPeerAllowed(a2a.AgentCard{Name: "peer"}) {
		t.Error("nil PeerAllowList should default to allow-all")
	}

	emptyAgent := New(AgentConfig{Name: "test", PeerAllowList: []string{}})
	if emptyAgent.isPeerAllowed(a2a.AgentCard{Name: "peer"}) {
		t.Error("non-nil empty PeerAllowList should deny all")
	}
}

func TestIsPeerAllowedWithAllowList(t *testing.T) {
	agent := New(AgentConfig{
		Name:          "test",
		PeerAllowList: []string{"data-*"},
	})

	if !agent.isPeerAllowed(a2a.AgentCard{Name: "data-agent"}) {
		t.Error("data-agent should be allowed")
	}
	if agent.isPeerAllowed(a2a.AgentCard{Name: "web-agent"}) {
		t.Error("web-agent should not be allowed")
	}
}

func TestIsPeerAllowedWithDenyList(t *testing.T) {
	agent := New(AgentConfig{
		Name:         "test",
		PeerDenyList: []string{"internal-*"},
	})

	if !agent.isPeerAllowed(a2a.AgentCard{Name: "data-agent"}) {
		t.Error("data-agent should be allowed")
	}
	if agent.isPeerAllowed(a2a.AgentCard{Name: "internal-agent"}) {
		t.Error("internal-agent should be denied")
	}
}

func TestIsPeerAllowedDenyOverridesAllow(t *testing.T) {
	agent := New(AgentConfig{
		Name:          "test",
		PeerAllowList: []string{"*"},
		PeerDenyList:  []string{"banned"},
	})

	if !agent.isPeerAllowed(a2a.AgentCard{Name: "good"}) {
		t.Error("good should be allowed")
	}
	if agent.isPeerAllowed(a2a.AgentCard{Name: "banned"}) {
		t.Error("banned should be denied")
	}
}

func TestIsPeerAllowed_MatchesDisplayName(t *testing.T) {
	agent := New(AgentConfig{
		Name:          "self",
		PeerAllowList: []string{"Jira and Confluence", "Research"},
	})

	mkCard := func(slug, display string) a2a.AgentCard {
		c := a2a.AgentCard{Name: slug}
		if display != "" {
			c.Capabilities.Extensions = []a2a.AgentExtension{{
				URI:    ExtensionURIDisplayName,
				Params: map[string]any{"display_name": display},
			}}
		}
		return c
	}

	if !agent.isPeerAllowed(mkCard("agent_019e1876_5f14_7f40_b463_b564c754128c", "Jira and Confluence")) {
		t.Error("card with matching display name should be allowed")
	}

	agent2 := New(AgentConfig{
		Name:          "self",
		PeerAllowList: []string{"agent_*"},
	})
	if !agent2.isPeerAllowed(mkCard("agent_019e1876_5f14_7f40_b463_b564c754128c", "Jira and Confluence")) {
		t.Error("card with matching slug should be allowed")
	}

	if agent.isPeerAllowed(mkCard("agent_other", "Something Else")) {
		t.Error("card matching neither slug nor display name should be rejected")
	}

	agent3 := New(AgentConfig{
		Name:          "self",
		PeerAllowList: []string{"agent_*"},
		PeerDenyList:  []string{"Jira and Confluence"},
	})
	if agent3.isPeerAllowed(mkCard("agent_019e1876_5f14_7f40_b463_b564c754128c", "Jira and Confluence")) {
		t.Error("deny by display name should override slug allow")
	}
}

func TestHandleDiscoveryMessage(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "self",
		Namespace: "ns",
	})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card := a2a.AgentCard{
		Name:        "peer-a",
		Description: "Peer A",
		URL:         "solace:ns/a2a/v1/agent/request/peer-a",
	}
	data, _ := json.Marshal(card)

	agent.handleDiscoveryMessage(&broker.Message{Payload: data})

	if knownPeer(agent, "peer-a") == nil {
		t.Error("peer-a should be registered")
	}

	if agent.tools.GetTool("peer_peer-a") == nil {
		t.Error("peer tool should be registered")
	}
}

func TestHandleDiscoveryMessage_PlatformShape_DisplayNameAllowList(t *testing.T) {
	agent := New(AgentConfig{
		Name:          "self",
		Namespace:     "ns",
		PeerAllowList: []string{"TestPeerDisplay"},
	})
	if err := agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())); err != nil {
		t.Fatalf("Init: %v", err)
	}

	const peerSlug = "agent_019e1876_5f14_7f40_b463_b564c754128c"
	card := a2a.AgentCard{
		Name:        peerSlug,
		Description: "Peer with platform-shaped card",
		URL:         "solace:ns/a2a/v1/agent/request/" + peerSlug,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    ExtensionURIDisplayName,
				Params: map[string]any{"display_name": "TestPeerDisplay"},
			}},
		},
	}
	data, _ := json.Marshal(card)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})

	if knownPeer(agent, peerSlug) == nil {
		t.Fatal("peer should be registered under its slug name despite allowList using display name")
	}

	if agent.tools.GetTool("peer_"+peerSlug) == nil {
		t.Fatal("peer tool should be registered under peer_<slug>")
	}
}

func TestHandleDiscoveryMessageIgnoresSelf(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "self",
		Namespace: "ns",
	})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card := a2a.AgentCard{Name: "self"}
	data, _ := json.Marshal(card)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})

	if knownPeer(agent, "self") != nil {
		t.Error("should not register self")
	}
}

func TestHandleDiscoveryMessageDenied(t *testing.T) {
	agent := New(AgentConfig{
		Name:         "self",
		Namespace:    "ns",
		PeerDenyList: []string{"blocked"},
	})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card := a2a.AgentCard{Name: "blocked"}
	data, _ := json.Marshal(card)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})

	if knownPeer(agent, "blocked") != nil {
		t.Error("blocked peer should not be registered")
	}
}

func TestHandleDiscoveryMessageUpdatesExisting(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "self",
		Namespace: "ns",
	})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card1 := a2a.AgentCard{Name: "peer-a", Description: "v1"}
	data1, _ := json.Marshal(card1)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data1})

	card2 := a2a.AgentCard{Name: "peer-a", Description: "v2"}
	data2, _ := json.Marshal(card2)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data2})

	got := knownPeer(agent, "peer-a")
	if got == nil || got.Description != "v2" {
		t.Error("should update to v2")
	}
}

func TestHandleDiscoveryMessage_IgnoresSupersededInstanceRemoval(t *testing.T) {
	agent := New(AgentConfig{Name: "self", Namespace: "ns"})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card := a2a.AgentCard{
		Name:        "peer-a",
		Description: "Peer A",
		URL:         "solace:ns/a2a/v1/agent/request/peer-a",
	}
	data, _ := json.Marshal(card)

	deliver := func(props map[string]string) {
		agent.handleDiscoveryMessage(&broker.Message{Payload: data, UserProperties: props})
	}

	deliver(map[string]string{"instance_id": "A"})
	deliver(map[string]string{"instance_id": "B"})

	deliver(map[string]string{"removed": "true", "instance_id": "A"})
	if knownPeer(agent, "peer-a") == nil {
		t.Fatal("stale removal from superseded instance A evicted the live peer")
	}
	if agent.tools.GetTool("peer_peer-a") == nil {
		t.Fatal("stale removal should not unregister the live peer tool")
	}

	deliver(map[string]string{"removed": "true", "instance_id": "B"})
	if knownPeer(agent, "peer-a") != nil {
		t.Error("removal from current instance B should deregister the peer")
	}
	if agent.tools.GetTool("peer_peer-a") != nil {
		t.Error("removal from current instance B should unregister the peer tool")
	}
}

func TestHandleDiscoveryMessageRemovesPeer(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "self",
		Namespace: "ns",
	})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card := a2a.AgentCard{
		Name:        "peer-a",
		Description: "Peer A",
		URL:         "solace:ns/a2a/v1/agent/request/peer-a",
	}
	data, _ := json.Marshal(card)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})

	if knownPeer(agent, "peer-a") == nil {
		t.Fatal("peer-a should be registered before removal")
	}
	if agent.tools.GetTool("peer_peer-a") == nil {
		t.Fatal("peer tool should be registered before removal")
	}

	agent.handleDiscoveryMessage(&broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"removed": "true"},
	})

	if knownPeer(agent, "peer-a") != nil {
		t.Error("peer-a should be removed from registry")
	}
	if agent.tools.GetTool("peer_peer-a") != nil {
		t.Error("peer tool should be unregistered")
	}
}

func TestSyncPeers_HidesPeerPastTTL(t *testing.T) {
	agent := New(AgentConfig{
		Name:                       "self",
		Namespace:                  "ns",
		HealthCheckIntervalSeconds: 1,
		HealthCheckTTLSeconds:      10,
	})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	agent.InjectPeerCard(a2a.AgentCard{Name: "peer-a"})
	if agent.tools.GetTool("peer_peer-a") == nil {
		t.Fatal("peer tool should be registered")
	}

	setPeerClock(agent, 20*time.Second)
	if knownPeer(agent, "peer-a") != nil || agent.tools.GetTool("peer_peer-a") != nil {
		t.Fatal("a peer last seen 20s ago should be hidden with a 10s TTL")
	}
}

func TestHandleDiscoveryMessageRemovesWorkflowPeer(t *testing.T) {
	agent := New(AgentConfig{
		Name:      "self",
		Namespace: "ns",
	})
	agent.Init(context.Background(), nil, newMockServices(newMockLLM(), newMemSessionStore())) //nolint:errcheck // test

	card := a2a.AgentCard{
		Name: "my-workflow",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: ExtensionURIAgentType, Params: map[string]any{"type": "workflow"}},
			},
		},
	}
	data, _ := json.Marshal(card)
	agent.handleDiscoveryMessage(&broker.Message{Payload: data})

	if agent.tools.GetTool("workflow_my_workflow") == nil {
		t.Fatal("workflow tool should be registered before removal")
	}

	agent.handleDiscoveryMessage(&broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"removed": "true"},
	})

	if knownPeer(agent, "my-workflow") != nil {
		t.Error("my-workflow should be removed from registry")
	}
	if agent.tools.GetTool("workflow_my_workflow") != nil {
		t.Error("workflow tool should be unregistered")
	}
}

func TestPeerToolExecute(t *testing.T) {
	var agent *Agent
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatal(err)
		}

		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
				Message: &a2a.Message{
					Parts: []a2a.Part{a2a.NewTextPart("peer response")},
				},
			},
		})
		data, _ := json.Marshal(resp)

		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{
		Name:      "caller",
		Namespace: "ns",
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	pt := &peerAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	result, err := pt.Execute(context.Background(), map[string]any{
		"task_description": "do something",
	}, &tool.Context{UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if result.Message != "peer response" {
		t.Errorf("message = %q", result.Message)
	}
}

func TestPeerToolExecuteArtifactsMetadata(t *testing.T) {
	var (
		agent         *Agent
		capturedReqMu = struct {
			req a2a.Request
			ok  bool
		}{}
	)
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatal(err)
		}
		capturedReqMu.req = req
		capturedReqMu.ok = true

		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
				Message: &a2a.Message{
					Parts: []a2a.Part{a2a.NewTextPart("ok")},
				},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{Name: "caller", Namespace: "ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	pt := &peerAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	_, err := pt.Execute(context.Background(), map[string]any{
		"task_description": "analyze",
		"artifacts": []any{
			map[string]any{"filename": "sales_data.csv", "version": 0},
			map[string]any{"filename": "notes.txt"},
		},
	}, &tool.Context{UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !capturedReqMu.ok {
		t.Fatal("peer request was not published")
	}

	params, ok := capturedReqMu.req.Params.(map[string]any)
	if !ok {
		t.Fatalf("unexpected Params type: %T", capturedReqMu.req.Params)
	}
	msgAny, ok := params["message"]
	if !ok {
		t.Fatal("missing message in params")
	}
	msgMap, ok := msgAny.(map[string]any)
	if !ok {
		t.Fatalf("unexpected message type: %T", msgAny)
	}
	metaAny, ok := msgMap["metadata"]
	if !ok {
		t.Fatal("missing metadata on peer A2A message")
	}
	meta, ok := metaAny.(map[string]any)
	if !ok {
		t.Fatalf("unexpected metadata type: %T", metaAny)
	}
	artsAny, ok := meta["invoked_with_artifacts"]
	if !ok {
		t.Fatalf("metadata missing invoked_with_artifacts; have keys: %v", keysOf(meta))
	}
	arts, ok := artsAny.([]any)
	if !ok {
		t.Fatalf("invoked_with_artifacts wrong type: %T", artsAny)
	}
	if len(arts) != 2 {
		t.Fatalf("len(invoked_with_artifacts) = %d, want 2", len(arts))
	}
	first, ok := arts[0].(map[string]any)
	if !ok {
		t.Fatalf("artifact[0] wrong type: %T", arts[0])
	}
	if first["filename"] != "sales_data.csv" {
		t.Errorf("artifact[0].filename = %v, want sales_data.csv", first["filename"])
	}
}

func TestPeerToolExecuteUsesCallerDisplayName(t *testing.T) {
	var (
		agent    *Agent
		captured *a2a.Request
	)
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatal(err)
		}
		captured = &req

		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State:   a2a.TaskStateCompleted,
				Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("ok")}},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{Name: "caller", Namespace: "ns"})
	agent.cfg.DisplayName = "Data Analyst"
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	pt := &peerAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	if _, err := pt.Execute(context.Background(), map[string]any{
		"task_description": "analyze",
	}, &tool.Context{UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}
	if captured == nil {
		t.Fatal("peer request was not published")
	}

	params, ok := captured.Params.(map[string]any)
	if !ok {
		t.Fatalf("unexpected Params type: %T", captured.Params)
	}
	msgMap, ok := params["message"].(map[string]any)
	if !ok {
		t.Fatal("missing or malformed message in params")
	}
	partsAny, ok := msgMap["parts"].([]any)
	if !ok {
		t.Fatalf("unexpected parts type: %T", msgMap["parts"])
	}
	var text string
	for _, p := range partsAny {
		part, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if part["kind"] == "text" {
			if s, ok := part["text"].(string); ok {
				text += s
			}
		}
	}

	if !strings.Contains(text, "Request from Data Analyst.") {
		t.Errorf("peer message should name the caller by display name; got:\n%s", text)
	}
	if strings.Contains(text, "Request from caller.") {
		t.Errorf("peer message should not name the caller by its slug; got:\n%s", text)
	}
}

func TestPeerToolExecuteArtifactsMetadataOmittedWhenEmpty(t *testing.T) {
	var agent *Agent
	var captured *a2a.Request
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Fatal(err)
		}
		captured = &req

		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State:   a2a.TaskStateCompleted,
				Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("ok")}},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{Name: "caller", Namespace: "ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	pt := &peerAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	if _, err := pt.Execute(context.Background(), map[string]any{
		"task_description": "analyze",
	}, &tool.Context{UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}
	assertNoInvokedArtifacts(t, captured, "missing key")

	captured = nil
	if _, err := pt.Execute(context.Background(), map[string]any{
		"task_description": "analyze",
		"artifacts":        []any{},
	}, &tool.Context{UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}
	assertNoInvokedArtifacts(t, captured, "empty list")
}

func assertNoInvokedArtifacts(t *testing.T, req *a2a.Request, label string) {
	t.Helper()
	if req == nil {
		t.Fatalf("%s: peer request was not published", label)
	}
	params, ok := req.Params.(map[string]any)
	if !ok {
		t.Fatalf("%s: unexpected Params type: %T", label, req.Params)
	}
	msgMap, ok := params["message"].(map[string]any)
	if !ok {
		t.Fatalf("%s: missing or malformed message", label)
	}
	meta, ok := msgMap["metadata"].(map[string]any)
	if !ok {
		return
	}
	if _, present := meta["invoked_with_artifacts"]; present {
		t.Errorf("%s: invoked_with_artifacts should be absent when artifacts are empty", label)
	}
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestPeerToolExecuteMessageSizeExceeded(t *testing.T) {
	agent := New(AgentConfig{
		Name:                "caller",
		Namespace:           "ns",
		MaxMessageSizeBytes: 50,
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	pt := &peerAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	result, err := pt.Execute(context.Background(), map[string]any{
		"task_description": "This is a task description that will produce a message larger than 50 bytes",
	}, &tool.Context{UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "exceeds maximum limit") {
		t.Errorf("message = %q, should mention size limit", result.Message)
	}
}

func TestPeerToolExecuteMissingDescription(t *testing.T) {
	pt := &peerAgentTool{peerName: "test"}
	result, err := pt.Execute(context.Background(), map[string]any{}, &tool.Context{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Error("should error without task_description")
	}
}

func TestPeerToolExecuteHonorsConfiguredTimeout(t *testing.T) {
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
	})

	agent := New(AgentConfig{
		Name:                      "caller",
		Namespace:                 "ns",
		PeerRequestTimeoutSeconds: 1,
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test

	pt := &peerAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	start := time.Now()
	result, err := pt.Execute(context.Background(), map[string]any{
		"task_description": "never answered",
	}, &tool.Context{UserID: "user-1"})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "timed out") {
		t.Errorf("message = %q, should mention timeout", result.Message)
	}
	if elapsed > 10*time.Second {
		t.Errorf("elapsed = %v, configured timeout was 1s (legacy was 10m)", elapsed)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

type mockBrokerWithPublishHook struct {
	mockBroker
	publishHook func(topic string, msg *broker.Message)
}

func newMockBrokerWithPublishHook(hook func(string, *broker.Message)) *mockBrokerWithPublishHook {
	return &mockBrokerWithPublishHook{
		mockBroker:  *newMockBroker(),
		publishHook: hook,
	}
}

func (b *mockBrokerWithPublishHook) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	if err := b.mockBroker.Publish(ctx, topic, msg); err != nil {
		return err
	}
	if b.publishHook != nil {
		b.publishHook(topic, msg)
	}
	return nil
}

func (b *mockBrokerWithPublishHook) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	return b.Publish(ctx, topic, msg)
}

func TestPeerToolExecute_NilIdentity_EnterpriseRBAC_Denies(t *testing.T) {
	agent := newEnterpriseAgentHarness(t)

	pt := &peerAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	result, err := pt.Execute(context.Background(), map[string]any{
		"task_description": "do X",
	}, &tool.Context{Identity: nil, SessionID: "s"})
	if err != nil {
		t.Fatalf("Execute returned unexpected Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want %q; message=%q",
			result.Status, tool.StatusError, result.Message)
	}
	if !strings.Contains(result.Message, "agent delegation denied") {
		t.Errorf("message = %q, want contains 'agent delegation denied'", result.Message)
	}
	if !strings.Contains(result.Message, "no identity provided") {
		t.Errorf("message = %q, want contains 'no identity provided'", result.Message)
	}
}

func TestPeerToolExecute_NilIdentity_NoopAuthorizer_Allows(t *testing.T) {
	var agent *Agent
	brk := newMockBrokerWithPublishHook(func(topic string, msg *broker.Message) {
		if !strings.Contains(topic, "/agent/request/") {
			return
		}
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)
		resp := a2a.NewResponse(req.ID, a2a.Task{
			ID:   req.ID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State:   a2a.TaskStateCompleted,
				Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("ok")}},
			},
		})
		data, _ := json.Marshal(resp)
		go agent.peerRouter.HandleResponse(req.ID, &broker.Message{Payload: data})
	})

	agent = New(AgentConfig{Name: "caller", Namespace: "ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	pt := &peerAgentTool{
		peerName:  "target",
		card:      a2a.AgentCard{Name: "target"},
		agent:     agent,
		namespace: "ns",
	}

	result, err := pt.Execute(context.Background(), map[string]any{
		"task_description": "do X",
	}, &tool.Context{Identity: nil, UserID: "user-1"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("no-auth mode: status = %q, want %q; message=%q",
			result.Status, tool.StatusSuccess, result.Message)
	}
	if result.Message != "ok" {
		t.Errorf("no-auth mode: message = %q, want %q", result.Message, "ok")
	}
}

func TestIsInternalCard(t *testing.T) {
	t.Parallel()

	if isInternalCard(a2a.AgentCard{Name: "plain"}) {
		t.Error("card with no extensions should not be internal")
	}

	if isInternalCard(a2a.AgentCard{
		Name: "ext-no-flag",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{URI: ExtensionURIInternal}},
		},
	}) {
		t.Error("extension without internal flag should not be internal")
	}

	if !isInternalCard(a2a.AgentCard{
		Name: "internal",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    ExtensionURIInternal,
				Params: map[string]any{"internal": true},
			}},
		},
	}) {
		t.Error("internal=true card should be flagged")
	}

	if isInternalCard(a2a.AgentCard{
		Name: "explicit-false",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    ExtensionURIInternal,
				Params: map[string]any{"internal": false},
			}},
		},
	}) {
		t.Error("internal=false card should not be flagged")
	}
}

func TestParseResponse_JSONRPCErrorReturnsToolError(t *testing.T) {
	pt := &peerAgentTool{peerName: "data-agent"}

	errResp := a2a.NewErrorResponse("task-1", -32603, "auth token exchange failed: bad code")
	payload, err := json.Marshal(errResp)
	if err != nil {
		t.Fatal(err)
	}

	result, err := pt.parseResponse(&broker.Message{Payload: payload})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "data-agent") {
		t.Errorf("message = %q, want peer name included", result.Message)
	}
	if !strings.Contains(result.Message, "auth token exchange failed") {
		t.Errorf("message = %q, want underlying error message included", result.Message)
	}
}

func TestPeerContextMessage(t *testing.T) {
	msg := PeerContextMessage("Data Analyst", "do the thing")

	if !strings.HasPrefix(msg, "Request from Data Analyst. "+PeerTaskSentinel) {
		t.Errorf("caller display name not in preamble: %q", msg)
	}
	if !strings.HasSuffix(msg, "do the thing") {
		t.Errorf("preamble should end with the task text: %q", msg)
	}
}

func TestRunBasedDelegationForwardsDisplaysTemplateSignals(t *testing.T) {
	dispatchers := []struct {
		name    string
		execute func(agent *Agent, toolCtx *tool.Context) (*tool.Result, error)
	}{
		{name: "peer call", execute: func(agent *Agent, toolCtx *tool.Context) (*tool.Result, error) {
			pt := &peerAgentTool{peerName: "target", card: a2a.AgentCard{Name: "target"}, agent: agent, namespace: "ns"}
			return pt.Execute(t.Context(), map[string]any{"task_description": "analyze"}, toolCtx)
		}},
		{name: "sub-task", execute: func(agent *Agent, toolCtx *tool.Context) (*tool.Result, error) {
			st := &subTaskTool{agent: agent}
			return st.Execute(t.Context(), map[string]any{"instruction": "analyze", "type": "fresh"}, toolCtx)
		}},
	}
	for _, d := range dispatchers {
		for _, displays := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s, caller flag %v", d.name, displays), func(t *testing.T) {
				agent, _, _ := newTestAgent()
				brk := newMockBroker()
				agent.svc.(*mockServices).brk = brk
				agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)
				toolCtx := &tool.Context{UserID: "user-1", SessionID: "parent-session", AgentName: agent.cfg.Name, ParentTaskID: "task-parent", PauseResume: true, DisplaysTemplateSignals: displays}

				if _, err := d.execute(agent, toolCtx); err != nil {
					t.Fatal(err)
				}

				meta := publishedRequestMetadata(t, brk)
				got, present := meta[a2a.MetadataDisplaysTemplateSignals]
				if displays && got != true {
					t.Errorf("%s = %v, want true forwarded from the caller", a2a.MetadataDisplaysTemplateSignals, got)
				}
				if !displays && present {
					t.Errorf("%s = %v, want the key absent when the caller did not receive it", a2a.MetadataDisplaysTemplateSignals, got)
				}
			})
		}
	}
}

func publishedRequestMetadata(t *testing.T, brk *mockBroker) map[string]any {
	t.Helper()
	brk.mu.Lock()
	published := brk.published
	brk.mu.Unlock()
	var requests []publishedMsg
	for _, p := range published {
		if strings.Contains(p.topic, "/agent/request/") {
			requests = append(requests, p)
		}
	}
	if len(requests) != 1 {
		t.Fatalf("published %d agent requests, want 1", len(requests))
	}
	var req struct {
		Params struct {
			Message a2a.Message `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(requests[0].payload, &req); err != nil {
		t.Fatalf("unmarshal published request: %v", err)
	}
	return req.Params.Message.Metadata
}

func knownPeer(agent *Agent, name string) *a2a.AgentCard {
	for _, card := range agent.peerCards() {
		if card.Name == name {
			return &card
		}
	}
	return nil
}
