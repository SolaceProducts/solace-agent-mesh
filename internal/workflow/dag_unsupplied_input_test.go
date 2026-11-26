// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"log/slog"
	"testing"
)

func supplyInput(state *WorkflowExecutionState, input map[string]any) {
	state.NodeOutputs["workflow_input"] = map[string]any{"output": input}
}

func quietLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func TestWhenClause_UnsuppliedInputSkipIsOrdinaryOnTheWire(t *testing.T) {
	const when = `'{{workflow.input.call_id}}' != '' or '{{workflow.input.text}}' != ''`

	allNodes := []*NodeConfig{{ID: "fetch", Type: "tool", ToolName: "get_call_segments", When: when}}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	supplyInput(state, map[string]any{"segments": []any{"s1"}})

	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: allNodes, FailFast: true}, dag, state)
	defer e.execCtx.Cancel()
	var logs bytes.Buffer
	e.logger = quietLogger(&logs)

	if proceed, stop := e.evaluateWhenClause("fetch", when); proceed || stop {
		t.Fatalf("proceed, stop = %v, %v; want false, false", proceed, stop)
	}

	metadata, _ := nodeResultData(t, brk, "fetch")["metadata"].(map[string]any)
	if got, want := metadata["skip_reason"], "when clause depends on a workflow input the caller did not supply"; got != want {
		t.Errorf("skip_reason = %#v, want %q", got, want)
	}
	if _, present := metadata["unresolved_reference"]; present {
		t.Errorf("an omitted optional input was flagged as a stale reference on the wire: %#v", metadata)
	}
	if logs.Len() != 0 {
		t.Errorf("an ordinary skip logged at INFO or above, on every run that omits the input:\n%s", logs.String())
	}
}

func TestWhenClause_SkippedBranchAndUnsuppliedInputSkipQuietly(t *testing.T) {
	const when = `'{{branch_a.output.x}}' != '' or '{{workflow.input.y}}' != ''`

	allNodes := []*NodeConfig{
		{ID: "gate", Type: "agent", AgentName: "g"},
		{ID: "branch_a", Type: "agent", AgentName: "a", DependsOn: []string{"gate"}},
		{ID: "consumer", Type: "agent", AgentName: "b", DependsOn: []string{"gate", "branch_a"}, When: when},
	}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	supplyInput(state, map[string]any{})
	state.NodeOutputs["gate"] = map[string]any{"output": map[string]any{"status": "success"}}
	state.CompletedNodes["gate"] = "agent"
	state.SkippedNodes["branch_a"] = `not selected by switch "gate"`

	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: allNodes, FailFast: true}, dag, state)
	defer e.execCtx.Cancel()
	var logs bytes.Buffer
	e.logger = quietLogger(&logs)

	if proceed, stop := e.evaluateWhenClause("consumer", when); proceed || stop {
		t.Fatalf("proceed, stop = %v, %v; want false, false", proceed, stop)
	}
	metadata, _ := nodeResultData(t, brk, "consumer")["metadata"].(map[string]any)
	if got, want := metadata["skip_reason"], "when clause depends on a value no node produced and the caller did not supply"; got != want {
		t.Errorf("skip_reason = %#v, want %q", got, want)
	}
	if _, present := metadata["unresolved_reference"]; present {
		t.Errorf("a branch not taken was flagged as a stale reference on the wire: %#v", metadata)
	}
	if logs.Len() != 0 {
		t.Errorf("an ordinary skip logged at INFO or above:\n%s", logs.String())
	}
}

func TestEvaluateWhenClause_BrokenPredicateOverUnsuppliedInputFailsTheNode(t *testing.T) {
	const when = `'{{workflow.input.call_id}}' != '' and (`

	allNodes := []*NodeConfig{{ID: "fetch", Type: "tool", ToolName: "get_call_segments", When: when}}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	supplyInput(state, map[string]any{})

	e := newTestDAGExecutor(&WorkflowConfig{Nodes: allNodes, FailFast: true}, dag, state)
	defer e.execCtx.Cancel()

	proceed, stop := e.evaluateWhenClause("fetch", when)
	if proceed {
		t.Error("proceed = true; a malformed predicate must not dispatch")
	}
	if !stop {
		t.Error("stop = false; a malformed predicate must fail the node under fail_fast, as with the input supplied")
	}
	if state.IsNodeSkipped("fetch") {
		t.Error("a malformed predicate was reported as a skip")
	}
}

func TestExecuteSwitchNode_UnsuppliedInputFallsToDefaultQuietly(t *testing.T) {
	for name, cond := range map[string]string{
		"omitted input":                  `'{{workflow.input.mode}}' == 'fast'`,
		"omitted input and node not run": `'{{branch_a.output.x}}' != '' or '{{workflow.input.y}}' != ''`,
	} {
		t.Run(name, func(t *testing.T) {
			switchNode := &NodeConfig{
				ID:          "gate",
				Type:        "switch",
				Cases:       []SwitchCase{{Condition: cond, Node: "fast_path"}},
				DefaultCase: "slow_path",
			}
			fast := &NodeConfig{ID: "fast_path", Type: "agent", AgentName: "a", DependsOn: []string{"gate"}}
			slow := &NodeConfig{ID: "slow_path", Type: "agent", AgentName: "b", DependsOn: []string{"gate"}}

			allNodes := []*NodeConfig{switchNode, fast, slow}
			dag, err := BuildDependencyGraph(allNodes)
			if err != nil {
				t.Fatal(err)
			}
			state := NewExecutionState("wf", "e1")
			supplyInput(state, map[string]any{})

			e := newTestDAGExecutor(&WorkflowConfig{Nodes: allNodes}, dag, state)
			defer e.execCtx.Cancel()
			var logs bytes.Buffer
			e.logger = quietLogger(&logs)

			e.executeSwitchNode(switchNode)

			out := state.NodeOutputs["gate"]["output"].(map[string]any)
			if got := out["selected_branch"]; got != "slow_path" {
				t.Errorf("selected_branch = %v, want the default", got)
			}
			if got := out["cases_unresolved"]; got != 0 {
				t.Errorf("cases_unresolved = %v, want 0; an ordinary absence is not a case that could not decide", got)
			}
			if logs.Len() != 0 {
				t.Errorf("an ordinary fall-through logged at INFO or above:\n%s", logs.String())
			}
		})
	}
}

func TestHandleLoopIterationComplete_UnsuppliedInputStopsAsConditionFalse(t *testing.T) {
	for name, cond := range map[string]string{
		"omitted input":                  `'{{body1.output.done}}' != 'true' and '{{workflow.input.stop_early}}' != 'true'`,
		"omitted input and node not run": `'{{body1.output.done}}' != 'true' and ('{{ghost.output.x}}' == 'go' or '{{workflow.input.y}}' == 'go')`,
	} {
		t.Run(name, func(t *testing.T) {
			nodes := loopNodes(cond, 10)
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatal(err)
			}
			state := NewExecutionState("wf", "e1")
			supplyInput(state, map[string]any{})
			e := newTestDAGExecutor(&WorkflowConfig{Nodes: nodes}, dag, state)
			t.Cleanup(e.execCtx.Cancel)
			var logs bytes.Buffer
			e.logger = quietLogger(&logs)

			state.LoopIterations["loop1"] = 0
			state.AddPending("loop1")
			e.handleLoopIterationComplete(e.execCtx.Ctx, "loop1", NodeCompletion{
				NodeID: "body1",
				Output: map[string]any{"done": "false"},
			})

			out, ok := state.NodeOutputs["loop1"]
			if !ok {
				t.Fatal("loop did not terminate")
			}
			if got := out["output"].(map[string]any)["stopped_reason"]; got != "condition_false" {
				t.Errorf("stopped_reason = %v, want condition_false", got)
			}
			if state.ErrorState != nil {
				t.Errorf("loop node was failed: %v", state.ErrorState)
			}
			for _, line := range bytes.Split(logs.Bytes(), []byte("\n")) {
				if bytes.Contains(line, []byte("level=WARN")) {
					t.Errorf("an ordinary absence warned as if it were a stale field: %s", line)
				}
			}
		})
	}
}
