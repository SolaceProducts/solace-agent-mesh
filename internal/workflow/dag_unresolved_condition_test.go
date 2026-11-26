// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import "testing"

func ranNodeOutputs(state *WorkflowExecutionState) {
	state.NodeOutputs["find_user"] = map[string]any{
		"output": map[string]any{"status": "success", "message": "empty response"},
	}
	state.CompletedNodes["find_user"] = "tool"
}

func TestExecuteSwitchNode_UnresolvedCaseDoesNotSelectBranch(t *testing.T) {
	for name, cond := range map[string]string{
		"equality":   `'{{find_user.output.mcp_structured_content}}' == '{}'`,
		"inequality": `'{{find_user.output.mcp_structured_content}}' != '{}'`,
	} {
		t.Run(name, func(t *testing.T) {
			switchNode := &NodeConfig{
				ID:          "gate",
				Type:        "switch",
				Cases:       []SwitchCase{{Condition: cond, Node: "rescue"}},
				DefaultCase: "carry_on",
			}
			rescue := &NodeConfig{ID: "rescue", Type: "agent", AgentName: "a", DependsOn: []string{"gate"}}
			carryOn := &NodeConfig{ID: "carry_on", Type: "agent", AgentName: "b", DependsOn: []string{"gate"}}

			allNodes := []*NodeConfig{switchNode, rescue, carryOn}
			cfg := &WorkflowConfig{Nodes: allNodes}
			dag, err := BuildDependencyGraph(allNodes)
			if err != nil {
				t.Fatal(err)
			}
			state := NewExecutionState("wf", "e1")
			ranNodeOutputs(state)

			e := newTestDAGExecutor(cfg, dag, state)
			defer e.execCtx.Cancel()

			e.executeSwitchNode(switchNode)

			out := state.NodeOutputs["gate"]["output"].(map[string]any)
			if got := out["selected_branch"]; got != "carry_on" {
				t.Errorf("selected_branch = %v, want the default; an unresolved case must not match", got)
			}
			if !state.IsNodeSkipped("rescue") {
				t.Error("rescue branch should have been skipped")
			}
		})
	}
}

func TestEvaluateWhenClause_UnresolvedSkipsRatherThanFails(t *testing.T) {
	node := &NodeConfig{
		ID:   "consumer",
		Type: "agent",
		When: `'{{find_user.output.mcp_structured_content}}' != '{}'`,
	}
	allNodes := []*NodeConfig{{ID: "find_user", Type: "tool", ToolName: "search"}, node}
	cfg := &WorkflowConfig{Nodes: allNodes, FailFast: true}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	proceed, stop := e.evaluateWhenClause(node.ID, node.When)

	if proceed {
		t.Error("proceed = true; an unresolved when: must not dispatch the node")
	}
	if stop {
		t.Error("stop = true; an unresolved reference must not trip fail-fast")
	}
	if !state.IsNodeSkipped("consumer") {
		t.Error("node should have been skipped")
	}
	if state.ErrorState != nil {
		t.Errorf("run was failed by an unresolved when: %v", state.ErrorState)
	}
}

func TestEvaluateWhenClause_UnquotedStaleDoesNotFailTheRun(t *testing.T) {
	const unquoted = `{{find_user.output.hits}} > 0`

	allNodes := []*NodeConfig{
		{ID: "find_user", Type: "tool", ToolName: "search"},
		{ID: "consumer", Type: "agent", When: unquoted},
	}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e := newTestDAGExecutor(&WorkflowConfig{Nodes: allNodes, FailFast: true}, dag, state)
	defer e.execCtx.Cancel()

	proceed, stop := e.evaluateWhenClause("consumer", unquoted)
	if proceed {
		t.Error("proceed = true; an unresolved when: must not dispatch")
	}
	if stop {
		t.Error("stop = true; an unresolved reference must not trip fail-fast")
	}
	if !state.IsNodeSkipped("consumer") {
		t.Error("node should have been skipped")
	}
	if state.ErrorState != nil {
		t.Errorf("run was failed by an unresolved when: %v", state.ErrorState)
	}
}

func TestEvaluateWhenClause_PresentFieldStillGates(t *testing.T) {
	allNodes := []*NodeConfig{
		{ID: "find_user", Type: "tool", ToolName: "search"},
		{ID: "consumer", Type: "agent"},
	}
	cfg := &WorkflowConfig{Nodes: allNodes}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	proceed, stop := e.evaluateWhenClause("consumer", `'{{find_user.output.status}}' == 'success'`)
	if !proceed || stop {
		t.Errorf("proceed, stop = %v, %v; want true, false", proceed, stop)
	}
}

func TestEvaluateWhenClause_UnsuppliedInputSkips(t *testing.T) {
	const when = `'{{workflow.input.call_id}}' != '' or '{{workflow.input.text}}' != ''`

	allNodes := []*NodeConfig{{ID: "fetch", Type: "tool", ToolName: "get_call_segments", When: when}}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	state.NodeOutputs["workflow_input"] = map[string]any{
		"output": map[string]any{"segments": []any{"s1"}},
	}

	e := newTestDAGExecutor(&WorkflowConfig{Nodes: allNodes, FailFast: true}, dag, state)
	defer e.execCtx.Cancel()

	proceed, stop := e.evaluateWhenClause("fetch", when)
	if proceed {
		t.Error("proceed = true; the node would run on inputs nobody supplied")
	}
	if stop {
		t.Error("stop = true; an unsupplied input must not trip fail-fast")
	}
	if !state.IsNodeSkipped("fetch") {
		t.Error("node should have been skipped")
	}
	if got, want := state.SkippedNodes["fetch"], "when clause depends on a workflow input the caller did not supply"; got != want {
		t.Errorf("skip reason = %q, want %q", got, want)
	}
	if state.ErrorState != nil {
		t.Errorf("run was failed by an unsupplied input: %v", state.ErrorState)
	}
}
