// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestExecuteSwitchNode_CompoundCaseSelectsOnTheArmThatResolved(t *testing.T) {
	const compound = `'{{find_user.output.hits}}' == '' or '{{find_user.output.status}}' == 'success'`

	switchNode := &NodeConfig{
		ID:          "gate",
		Type:        "switch",
		Cases:       []SwitchCase{{Condition: compound, Node: "rescue"}},
		DefaultCase: "carry_on",
	}
	rescue := &NodeConfig{ID: "rescue", Type: "agent", AgentName: "a", DependsOn: []string{"gate"}}
	carryOn := &NodeConfig{ID: "carry_on", Type: "agent", AgentName: "b", DependsOn: []string{"gate"}}

	allNodes := []*NodeConfig{switchNode, rescue, carryOn}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e := newTestDAGExecutor(&WorkflowConfig{Nodes: allNodes}, dag, state)
	defer e.execCtx.Cancel()

	var logs bytes.Buffer
	e.logger = slog.New(slog.NewTextHandler(&logs, nil))

	e.executeSwitchNode(switchNode)

	out := state.NodeOutputs["gate"]["output"].(map[string]any)
	if got := out["selected_branch"]; got != "rescue" {
		t.Errorf("selected_branch = %v, want rescue; the live arm was true", got)
	}
	if got := out["cases_unresolved"]; got != 1 {
		t.Errorf("cases_unresolved = %v, want 1; the case decided, but on a dead arm", got)
	}
	if !strings.Contains(logs.String(), "hits") {
		t.Errorf("the stale reference was not named in a WARN: %s", logs.String())
	}
}

func TestEvaluateWhenClause_CompoundStaleStillDispatches(t *testing.T) {
	const compound = `'{{find_user.output.hits}}' == '' or '{{find_user.output.status}}' == 'success'`

	allNodes := []*NodeConfig{
		{ID: "find_user", Type: "tool", ToolName: "search"},
		{ID: "consumer", Type: "agent", When: compound},
	}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e := newTestDAGExecutor(&WorkflowConfig{Nodes: allNodes, FailFast: true}, dag, state)
	defer e.execCtx.Cancel()

	proceed, stop := e.evaluateWhenClause("consumer", compound)
	if !proceed || stop {
		t.Errorf("proceed, stop = %v, %v; want true, false", proceed, stop)
	}
	if state.IsNodeSkipped("consumer") {
		t.Error("node was skipped; its live arm said yes")
	}
	if state.ErrorState != nil {
		t.Errorf("run was failed: %v", state.ErrorState)
	}
}

func TestExecuteSwitchNode_AllCasesAbstainWithNoDefault(t *testing.T) {
	switchNode := &NodeConfig{
		ID:   "gate",
		Type: "switch",
		Cases: []SwitchCase{
			{Condition: `'{{find_user.output.hits}}' == '{}'`, Node: "a"},
			{Condition: `'{{find_user.output.total}}' == '0'`, Node: "b"},
		},
	}
	a := &NodeConfig{ID: "a", Type: "agent", AgentName: "a", DependsOn: []string{"gate"}}
	b := &NodeConfig{ID: "b", Type: "agent", AgentName: "b", DependsOn: []string{"gate"}}

	allNodes := []*NodeConfig{switchNode, a, b}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e := newTestDAGExecutor(&WorkflowConfig{Nodes: allNodes}, dag, state)
	defer e.execCtx.Cancel()

	var logs bytes.Buffer
	e.logger = slog.New(slog.NewTextHandler(&logs, nil))

	e.executeSwitchNode(switchNode)

	out := state.NodeOutputs["gate"]["output"].(map[string]any)
	if got := out["selected_branch"]; got != "" {
		t.Errorf("selected_branch = %v, want empty", got)
	}
	if got := out["cases_unresolved"]; got != 2 {
		t.Errorf("cases_unresolved = %v, want 2 — otherwise this is indistinguishable "+
			"from every case legitimately evaluating false", got)
	}
	if !strings.Contains(logs.String(), "switch selected no branch") {
		t.Errorf("no node-level WARN said the switch chose nothing: %s", logs.String())
	}
	for _, id := range []string{"a", "b"} {
		if !state.IsNodeSkipped(id) {
			t.Errorf("branch %q should have been skipped", id)
		}
	}
}

func TestSkipMetadata_MarksOnlyTheAbstention(t *testing.T) {
	abstain := skipMetadata("when clause references a field the upstream node did not produce", true)
	if abstain["unresolved_reference"] != true {
		t.Errorf("abstention not marked: %#v", abstain)
	}
	if abstain["skip_reason"] == "" {
		t.Error("reason was dropped")
	}

	deliberate := skipMetadata(`not selected by switch "sw"`, false)
	if _, present := deliberate["unresolved_reference"]; present {
		t.Errorf("a deliberate prune must not claim an unresolved reference: %#v", deliberate)
	}
	if deliberate["skip_reason"] != `not selected by switch "sw"` {
		t.Errorf("reason = %v, want the prune reason", deliberate["skip_reason"])
	}
}

func TestSwitchResultMetadata_MatchesTheCardsContract(t *testing.T) {
	twoCases := &NodeConfig{ID: "sw", Cases: []SwitchCase{{Node: "a"}, {Node: "b"}}}

	matched := switchResultMetadata(twoCases, "a", 0, 0)
	if matched["selected_branch"] != "a" || matched["selected_case_index"] != 0 {
		t.Errorf("a matched case must report its index: %#v", matched)
	}

	def := switchResultMetadata(twoCases, "fallback", len(twoCases.Cases), 0)
	if def["selected_branch"] != "fallback" {
		t.Errorf("default branch name missing: %#v", def)
	}
	if idx, present := def["selected_case_index"]; !present || idx != nil {
		t.Errorf("default must send an explicit null, not %v — otherwise the card "+
			"renders Matched Case #%v", idx, len(twoCases.Cases)+1)
	}

	none := switchResultMetadata(twoCases, "", -1, 2)
	if _, present := none["selected_branch"]; present {
		t.Errorf("nothing selected must omit selected_branch, or the card draws an "+
			"empty green success box: %#v", none)
	}
	if _, present := none["selected_case_index"]; present {
		t.Errorf("nothing selected must omit the index, or the card renders "+
			"Matched Case #0: %#v", none)
	}
	if none["cases_unresolved"] != 2 {
		t.Errorf("cases_unresolved = %v, want 2 — the only signal that the switch "+
			"could not decide rather than legitimately not matching", none["cases_unresolved"])
	}
}
