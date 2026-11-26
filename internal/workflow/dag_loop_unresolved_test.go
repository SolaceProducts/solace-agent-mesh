// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import "testing"

func loopNodes(condition string, maxIterations int) []*NodeConfig {
	return []*NodeConfig{
		{ID: "loop1", Type: "loop", Condition: condition, BodyNode: "body1", MaxIterations: maxIterations},
		{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"loop1"}},
	}
}

func driveOneIteration(t *testing.T, nodes []*NodeConfig, bodyOutput map[string]any) (*WorkflowExecutionState, map[string]any) {
	t.Helper()
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(&WorkflowConfig{Nodes: nodes}, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	state.LoopIterations["loop1"] = 0
	state.AddPending("loop1")
	e.handleLoopIterationComplete(e.execCtx.Ctx, "loop1", NodeCompletion{
		NodeID: "body1",
		Output: bodyOutput,
	})

	out, ok := state.NodeOutputs["loop1"]
	if !ok {
		return state, nil
	}
	return state, out["output"].(map[string]any)
}

func TestHandleLoopIterationComplete_UnquotedStaleConditionStopsInsteadOfFailing(t *testing.T) {
	nodes := loopNodes("{{body1.output.count}} < 10", 10)
	state, out := driveOneIteration(t, nodes, map[string]any{"val": 1})

	if out == nil {
		t.Fatal("loop did not terminate")
	}
	if got := out["stopped_reason"]; got != "condition_unresolved" {
		t.Errorf("stopped_reason = %v, want condition_unresolved", got)
	}
	if state.ErrorState != nil {
		t.Errorf("loop node was failed: %v", state.ErrorState)
	}
	if got := state.CompletedNodes["loop1"]; got != "loop_condition_unresolved" {
		t.Errorf("CompletedNodes = %q, want loop_condition_unresolved — the sentinel and "+
			"the output must not disagree about why the loop stopped", got)
	}
}

func TestHandleLoopIterationComplete_AbstentionOutranksMaxIterations(t *testing.T) {
	nodes := loopNodes("{{body1.output.count}} < 10", 1)
	state, out := driveOneIteration(t, nodes, map[string]any{"val": 1})

	if out == nil {
		t.Fatal("loop did not terminate")
	}
	if got := out["stopped_reason"]; got != "condition_unresolved" {
		t.Errorf("stopped_reason = %v, want condition_unresolved (the cap landed on the same iteration)", got)
	}
	if got := state.CompletedNodes["loop1"]; got != "loop_condition_unresolved" {
		t.Errorf("CompletedNodes = %q, want loop_condition_unresolved", got)
	}
}

func TestHandleLoopIterationComplete_MaxIterationsStillReported(t *testing.T) {
	nodes := loopNodes("true", 1)
	state, out := driveOneIteration(t, nodes, map[string]any{"val": 1})

	if out == nil {
		t.Fatal("loop did not terminate")
	}
	if got := out["stopped_reason"]; got != "max_iterations" {
		t.Errorf("stopped_reason = %v, want max_iterations", got)
	}
	if got := state.CompletedNodes["loop1"]; got != "loop_max_iterations" {
		t.Errorf("CompletedNodes = %q, want loop_max_iterations", got)
	}
}

func TestHandleLoopIterationComplete_NegatedStaleArmStopsInsteadOfSpinning(t *testing.T) {
	nodes := loopNodes(
		`'{{body1.output.status}}' == 'running' or '{{body1.output.count}}' != '10'`, 100)
	state, out := driveOneIteration(t, nodes, map[string]any{"status": "done"})

	if out == nil {
		t.Fatal("loop did not terminate: the negated stale arm is keeping it running")
	}
	if got := out["stopped_reason"]; got != "condition_unresolved" {
		t.Errorf("stopped_reason = %v, want condition_unresolved", got)
	}
	if got := out["iterations_completed"]; got != 1 {
		t.Errorf("iterations_completed = %v, want 1 — every extra iteration is a real body dispatch", got)
	}
	if state.ErrorState != nil {
		t.Errorf("loop node was failed: %v", state.ErrorState)
	}
}

func TestHandleLoopIterationComplete_CompoundStaleArmStillDecides(t *testing.T) {
	nodes := loopNodes(`'{{body1.output.missing}}' == '' or '{{body1.output.status}}' == 'go'`, 10)
	_, out := driveOneIteration(t, nodes, map[string]any{"status": "go"})

	if out != nil {
		t.Fatalf("loop terminated with %v; the live arm said continue", out)
	}
}
