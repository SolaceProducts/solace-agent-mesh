// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestInvokeToolNode_IterationPaths_NoToolsBlock_FailsNodeNotProcess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		routing NodeCompletion
	}{
		{"map template", NodeCompletion{NodeID: "body", SubTaskID: "sub-1", MapNodeID: "each", ItemIndex: 0}},
		{"loop body", NodeCompletion{NodeID: "body", SubTaskID: "sub-1", LoopNodeID: "until_done"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &NodeConfig{
				ID:       "body",
				Type:     "tool",
				ToolName: "never_registered",
				Input:    map[string]any{"anything": "1"},
			}
			nodes := []*NodeConfig{node}
			cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatalf("dag: %v", err)
			}

			e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
			defer e.execCtx.Cancel()
			var logged bytes.Buffer
			e.logger = slog.New(slog.NewTextHandler(&logged, nil))

			e.invokeToolNode(node, map[string]any{"anything": "1"}, tc.routing)
			e.instance.wg.Wait()

			if e.state.ErrorState == nil {
				t.Fatal("no failure recorded: the iteration neither ran nor reported an error")
			}
			if got := e.state.ErrorState.FailedNodeID; got != "body" {
				t.Errorf("FailedNodeID = %q, want body", got)
			}
			if msg := e.state.ErrorState.ErrorMessage; !strings.Contains(msg, "`tools:` or `connectors:`") {
				t.Errorf("error message does not name the missing tools block: %q", msg)
			}
			if strings.Contains(logged.String(), "tool node panicked") {
				t.Error("the missing tools block reached the panic backstop instead of the guard")
			}
		})
	}
}

type panickingTool struct{ *schemaTool }

func (panickingTool) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	panic("tool blew up")
}

func TestInvokeToolNode_PanickingTool_FailsNodeNotProcess(t *testing.T) {
	node := &NodeConfig{ID: "boom_node", Type: "tool", ToolName: "boom"}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()

	ts := tool.NewSet()
	if err := ts.Register(panickingTool{&schemaTool{name: "boom", authored: true}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts

	e.invokeToolNode(node, map[string]any{}, NodeCompletion{NodeID: "boom_node", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if e.state.ErrorState == nil {
		t.Fatal("panicking tool recorded no failure")
	}
	if got := e.state.ErrorState.FailedNodeID; got != "boom_node" {
		t.Errorf("FailedNodeID = %q, want boom_node", got)
	}
	if msg := e.state.ErrorState.ErrorMessage; !strings.Contains(msg, "tool blew up") {
		t.Errorf("recovered panic value absent from the error: %q", msg)
	}
}

func TestInvokeToolNode_PanicInIteration_IdentifiesItemAndThrottlesStacks(t *testing.T) {
	node := &NodeConfig{ID: "boom_node", Type: "tool", ToolName: "boom"}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()
	var logged bytes.Buffer
	e.logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ts := tool.NewSet()
	if err := ts.Register(panickingTool{&schemaTool{name: "boom", authored: true}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts

	e.mu.Lock()
	for i := range 3 {
		e.invokeToolNode(node, map[string]any{}, NodeCompletion{
			NodeID:    "boom_node",
			SubTaskID: fmt.Sprintf("sub-%d", i),
			MapNodeID: "each",
			ItemIndex: i,
		})
	}
	e.mu.Unlock()
	e.instance.wg.Wait()

	out := logged.String()

	var panicLines []string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "tool node panicked") {
			panicLines = append(panicLines, line)
		}
	}
	if len(panicLines) < 3 {
		t.Fatalf("got %d panic records for 3 panicking iterations", len(panicLines))
	}
	joined := strings.Join(panicLines, "\n")
	for i := range 3 {
		if !strings.Contains(joined, fmt.Sprintf("subTaskID=sub-%d", i)) {
			t.Errorf("panic record for iteration %d does not identify its subTaskID", i)
		}
	}
	for i := range 3 {
		if !strings.Contains(joined, fmt.Sprintf("itemIndex=%d", i)) {
			t.Errorf("panic record for iteration %d does not record itemIndex", i)
		}
	}
	if n := strings.Count(out, "[running]:"); n != 1 {
		t.Errorf("logged %d stacks for 3 panicking iterations, want 1", n)
	}
}

func TestInvokeToolNode_TwoDistinctPanickingNodes_BothReportInFull(t *testing.T) {
	first := &NodeConfig{ID: "node_a", Type: "tool", ToolName: "boom"}
	second := &NodeConfig{ID: "node_b", Type: "tool", ToolName: "boom"}
	nodes := []*NodeConfig{first, second}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()
	var logged bytes.Buffer
	e.logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ts := tool.NewSet()
	if err := ts.Register(panickingTool{&schemaTool{name: "boom", authored: true}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts

	e.invokeToolNode(first, map[string]any{}, NodeCompletion{NodeID: "node_a", SubTaskID: "sub-a", MapNodeID: "each_a"})
	e.instance.wg.Wait()
	e.invokeToolNode(second, map[string]any{}, NodeCompletion{NodeID: "node_b", SubTaskID: "sub-b", MapNodeID: "each_b"})
	e.instance.wg.Wait()

	out := logged.String()
	for _, id := range []string{"node_a", "node_b"} {
		var found bool
		for line := range strings.SplitSeq(out, "\n") {
			if strings.Contains(line, "tool node panicked") && strings.Contains(line, "node="+id) && strings.Contains(line, "level=ERROR") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s did not report its panic at ERROR; a distinct bug was demoted as a repeat", id)
		}
	}
	if n := strings.Count(out, "[running]:"); n != 2 {
		t.Errorf("logged %d stacks for 2 distinct panicking nodes, want 2", n)
	}
}
