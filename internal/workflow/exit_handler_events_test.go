// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func nodeEventStatuses(t *testing.T, brk *capturingBroker) (starts []string, results map[string]string) {
	t.Helper()
	results = map[string]string{}
	for _, msg := range brk.snapshot() {
		data := extractDataPart(t, msg.Payload)
		nodeID, _ := data["node_id"].(string)
		switch data["type"] {
		case string(a2a.SignalWorkflowNodeExecStart):
			starts = append(starts, nodeID)
		case string(a2a.SignalWorkflowNodeExecResult):
			status, _ := data["status"].(string)
			results[nodeID] = status
		}
	}
	return starts, results
}

func exitChainExecutor(t *testing.T, handler *NodeConfig, execID string, tools ...any) (*DAGExecutor, *capturingBroker) {
	t.Helper()
	nodes := []*NodeConfig{
		{ID: "main", Type: "agent", AgentName: "mainAgent"},
		handler,
	}
	cfg := &WorkflowConfig{
		Name:          a2a.InstanceName(a2a.KindWorkflow, "019f9100-0000-7000-8000-00000000fee2"),
		Namespace:     "ns",
		Nodes:         nodes,
		OnExit:        &ExitHandler{Always: handler.ID},
		OutputMapping: map[string]any{"out": "{{main.output}}"},
		Tools:         tools,
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState(cfg.Name, execID)
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	if len(tools) > 0 {
		if err := e.instance.initToolsAndConnectors(context.Background(), e.instance.svc); err != nil {
			t.Fatalf("init tools: %v", err)
		}
	}
	return e, brk
}

func TestExitHandler_SynchronousFailure_PublishesTerminalResult(t *testing.T) {
	e, brk := exitChainExecutor(t,
		&NodeConfig{ID: "cleanup", Type: "workflow", WorkflowName: "NotDeployedWorkflow"},
		"exec-exit-sync-event")

	seedCard(e.instance, a2a.AgentCard{Name: "someOtherAgent"})

	e.beginExitChain("success", "")

	starts, results := nodeEventStatuses(t, brk)
	if len(starts) == 0 {
		t.Fatal("no node_execution_start published for the exit handler")
	}
	status, ok := results["cleanup"]
	if !ok {
		t.Fatalf("exit handler published a start but no terminal result; the node never leaves its running state in the UI (starts=%v, results=%v)", starts, results)
	}
	if status != "error" {
		t.Errorf("result status = %q, want %q", status, "error")
	}
}

func TestExitHandler_AsyncFailure_PublishesTerminalResult(t *testing.T) {
	e, brk := exitChainExecutor(t,
		&NodeConfig{ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"},
		"exec-exit-async-fail")

	e.state.InExitHandler = true
	e.state.AddPending("cleanup")
	e.advanceExitHandlers(NodeCompletion{
		NodeID:    "cleanup",
		SubTaskID: "sub-1",
		Error:     errors.New("cleanup agent reported failure"),
		ErrorKind: ErrorKindFailure,
	})

	_, results := nodeEventStatuses(t, brk)
	if results["cleanup"] != "error" {
		t.Errorf("result status = %q, want %q (results=%v)", results["cleanup"], "error", results)
	}
}

func TestExitHandler_Success_PublishesTerminalResult(t *testing.T) {
	e, brk := exitChainExecutor(t,
		&NodeConfig{ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"},
		"exec-exit-async-ok")

	e.state.InExitHandler = true
	e.state.AddPending("cleanup")
	e.advanceExitHandlers(NodeCompletion{
		NodeID:    "cleanup",
		SubTaskID: "sub-1",
		Output:    map[string]any{"deleted": 1},
	})

	_, results := nodeEventStatuses(t, brk)
	if results["cleanup"] != "success" {
		t.Errorf("result status = %q, want %q (results=%v)", results["cleanup"], "success", results)
	}
}

func TestExitHandler_ToolNodeInvokesTheTool(t *testing.T) {
	e, brk := exitChainExecutor(t,
		&NodeConfig{ID: "cleanup", Type: "tool", ToolName: "get_current_time"},
		"exec-exit-tool",
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"})

	e.beginExitChain("success", "")
	waitForFinalize(t, e, e.state)

	for _, msg := range brk.snapshot() {
		if strings.Contains(msg.Topic, "/agent/request/") {
			t.Errorf("exit handler published an agent request to %q; a tool handler must run in-process, and this topic has no subscriber", msg.Topic)
		}
	}
	_, results := nodeEventStatuses(t, brk)
	if results["cleanup"] != "success" {
		t.Errorf("tool exit handler result = %q, want %q (results=%v)", results["cleanup"], "success", results)
	}
}

func TestExitHandler_UnsupportedNodeTypeFailsLoudly(t *testing.T) {
	branch := &NodeConfig{ID: "branch", Type: "agent", AgentName: "branchAgent", DependsOn: []string{"cleanup"}}

	for _, handler := range []*NodeConfig{
		{ID: "cleanup", Type: "switch", Cases: []SwitchCase{{Condition: "true", Node: "branch"}}},
		{ID: "cleanup", Type: "map", ItemsExpression: []any{1, 2}, TemplateNode: "branch"},
		{ID: "cleanup", Type: "loop", Condition: "false", BodyNode: "branch"},
	} {
		t.Run(handler.Type, func(t *testing.T) {
			nodes := []*NodeConfig{
				{ID: "main", Type: "agent", AgentName: "mainAgent"},
				handler,
				branch,
			}
			cfg := &WorkflowConfig{
				Name:          a2a.InstanceName(a2a.KindWorkflow, "019f9100-0000-7000-8000-00000000fee3"),
				Namespace:     "ns",
				Nodes:         nodes,
				OnExit:        &ExitHandler{Always: "cleanup"},
				OutputMapping: map[string]any{"out": "{{main.output}}"},
			}
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatal(err)
			}
			state := NewExecutionState(cfg.Name, "exec-exit-unsupported-"+handler.Type)
			e, brk := newCapturingExecutor(t, cfg, dag, state)

			e.beginExitChain("success", "")

			if !e.state.Finalized {
				t.Fatal("the exit chain stalled: nothing published a final response, so the caller waits forever")
			}
			_, results := nodeEventStatuses(t, brk)
			if results["cleanup"] != "error" {
				t.Errorf("result status = %q, want %q so the failure is visible (results=%v)", results["cleanup"], "error", results)
			}
		})
	}
}
