// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const (
	hintDisplayName  = "Account Briefer"
	hintInstanceName = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
)

func parseHintedAgentNode(t *testing.T, id, instanceName string) *NodeConfig {
	t.Helper()
	raw := map[string]any{
		"id":         id,
		"type":       "agent",
		"agent_name": hintDisplayName,
	}
	if instanceName != "" {
		raw["agent_instance_name"] = instanceName
	}
	node, err := parseNodeConfig(raw, time.Minute)
	if err != nil {
		t.Fatalf("parseNodeConfig: %v", err)
	}
	return node
}

func TestDispatch_ColdCacheUsesDeployTimeAgentInstanceName(t *testing.T) {
	wantTopic := a2a.AgentRequestTopic("test-ns", hintInstanceName)
	literalTopic := a2a.AgentRequestTopic("test-ns", hintDisplayName)

	t.Run("agent node", func(t *testing.T) {
		node := parseHintedAgentNode(t, "write_up", hintInstanceName)
		cfg := &WorkflowConfig{Name: "AccountHealthOne", Namespace: "test-ns", Nodes: []*NodeConfig{node}}
		dag, err := BuildDependencyGraph(cfg.Nodes)
		if err != nil {
			t.Fatal(err)
		}
		e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState(cfg.Name, "exec-1"))

		e.dispatchAgentNode(context.Background(), node, "subtask-1")

		assertPublishedToBrokerTopic(t, brk, wantTopic, literalTopic)
	})

	t.Run("map template", func(t *testing.T) {
		template := parseHintedAgentNode(t, "brief_each", hintInstanceName)
		mapNode := &NodeConfig{ID: "fan_out", Type: "map", TemplateNode: "brief_each"}
		cfg := &WorkflowConfig{Name: "MapWorkflow", Namespace: "test-ns", Nodes: []*NodeConfig{mapNode, template}}
		dag, err := BuildDependencyGraph(cfg.Nodes)
		if err != nil {
			t.Fatal(err)
		}
		e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState(cfg.Name, "exec-1"))

		e.launchMapIteration(context.Background(), mapNode, map[string]any{"value": 1}, 0)

		assertPublishedToBrokerTopic(t, brk, wantTopic, literalTopic)
	})

	t.Run("loop body", func(t *testing.T) {
		body := parseHintedAgentNode(t, "worker", hintInstanceName)
		loopNode := &NodeConfig{ID: "retry_loop", Type: "loop", BodyNode: "worker", MaxIterations: 3}
		cfg := &WorkflowConfig{Name: "LoopWorkflow", Namespace: "test-ns", Nodes: []*NodeConfig{loopNode, body}}
		dag, err := BuildDependencyGraph(cfg.Nodes)
		if err != nil {
			t.Fatal(err)
		}
		e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState(cfg.Name, "exec-1"))

		e.dispatchLoopIteration(context.Background(), loopNode, 0)

		assertPublishedToBrokerTopic(t, brk, wantTopic, literalTopic)
	})
}

func TestDispatchAgentNode_LiveCardWinsOverStaleInstanceName(t *testing.T) {
	const liveBrokerName = "agent_0199aaaa_bbbb_7ccc_8ddd_eeeeffff0000"
	node := parseHintedAgentNode(t, "write_up", hintInstanceName)
	cfg := &WorkflowConfig{Name: "AccountHealthOne", Namespace: "test-ns", Nodes: []*NodeConfig{node}}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState(cfg.Name, "exec-1"))
	seedDisplayNameCard(e.instance, liveBrokerName, hintDisplayName)

	e.dispatchAgentNode(context.Background(), node, "subtask-1")

	assertPublishedToBrokerTopic(t, brk,
		a2a.AgentRequestTopic(cfg.Namespace, liveBrokerName),
		a2a.AgentRequestTopic(cfg.Namespace, hintInstanceName))
}

func TestParseNodeConfig_AgentInstanceNameKeepsOnlyAgentKind(t *testing.T) {
	cases := []struct {
		name, value, want string
	}{
		{"agent instance name kept", hintInstanceName, hintInstanceName},
		{"absent", "", ""},
		{"workflow instance name dropped", "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f", ""},
		{"plain name dropped", "Account Briefer", ""},
		{"non-uuid suffix dropped", "agent_not_a_uuid", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := parseHintedAgentNode(t, "write_up", tc.value)
			if node.AgentInstanceName != tc.want {
				t.Fatalf("AgentInstanceName = %q, want %q", node.AgentInstanceName, tc.want)
			}
		})
	}
}
