// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import "testing"

func TestSwitchThatSelectedABranchPublishesNoAbstentionForAnUnevaluatedCase(t *testing.T) {
	switchNode := &NodeConfig{
		ID:   "gate",
		Type: "switch",
		Cases: []SwitchCase{
			{Condition: `'{{find_user.output.hits}}' == '{}'`, Node: "rescue"},
			{Condition: `'a' == 'a'`, Node: "main"},
		},
	}
	rescue := &NodeConfig{ID: "rescue", Type: "agent", AgentName: "a", DependsOn: []string{"gate"}}
	mainBranch := &NodeConfig{ID: "main", Type: "agent", AgentName: "b", DependsOn: []string{"gate"}}
	allNodes := []*NodeConfig{switchNode, rescue, mainBranch}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)
	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: allNodes}, dag, state)
	defer e.execCtx.Cancel()

	e.executeSwitchNode(switchNode)

	if got := e.state.SkipCause("rescue"); got != SkipCauseUnresolved {
		t.Errorf("rescue cause = %q, want %q", got, SkipCauseUnresolved)
	}
	metadata, _ := nodeResultData(t, brk, "rescue")["metadata"].(map[string]any)
	if _, present := metadata["unresolved_reference"]; present {
		t.Errorf("flag published for a switch that selected a branch: %#v", metadata)
	}
	if want := `case for it could not be evaluated by switch "gate"`; metadata["skip_reason"] != want {
		t.Errorf("skip_reason = %v, want %q", metadata["skip_reason"], want)
	}
}

func TestSwitchResultMetadataReachesTheWire(t *testing.T) {
	switchNode := &NodeConfig{
		ID:    "gate",
		Type:  "switch",
		Cases: []SwitchCase{{Condition: `'{{find_user.output.hits}}' == '{}'`, Node: "rescue"}},
	}
	rescue := &NodeConfig{ID: "rescue", Type: "agent", AgentName: "a", DependsOn: []string{"gate"}}

	allNodes := []*NodeConfig{switchNode, rescue}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: allNodes}, dag, state)
	defer e.execCtx.Cancel()

	e.executeSwitchNode(switchNode)

	gate := nodeResultData(t, brk, "gate")
	metadata, ok := gate["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("no metadata on the switch result: %#v", gate)
	}
	if _, present := metadata["selected_branch"]; present {
		t.Errorf("selected_branch present when nothing was selected: %#v", metadata)
	}
	if got := metadata["cases_unresolved"]; got != float64(1) {
		t.Errorf("cases_unresolved = %#v, want 1 — the only signal that the switch "+
			"could not decide rather than legitimately not matching", got)
	}
}

func TestSkipMetadataReachesTheWireOnlyForAnAbstention(t *testing.T) {
	allNodes := []*NodeConfig{
		{ID: "find_user", Type: "tool", ToolName: "search"},
		{ID: "abstained", Type: "agent", AgentName: "a"},
		{ID: "pruned", Type: "agent", AgentName: "b"},
	}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: allNodes}, dag, state)
	defer e.execCtx.Cancel()

	e.skipNodeCause("abstained", "when clause references a field the upstream node did not produce", SkipCauseUnresolved, true)
	e.skipNode("pruned", `not selected by switch "sw"`, SkipCauseNotSelected)

	abstained, _ := nodeResultData(t, brk, "abstained")["metadata"].(map[string]any)
	if abstained["unresolved_reference"] != true {
		t.Errorf("abstention not marked on the wire: %#v", abstained)
	}
	if abstained["skip_reason"] == nil {
		t.Errorf("skip_reason missing on the wire: %#v", abstained)
	}

	pruned, _ := nodeResultData(t, brk, "pruned")["metadata"].(map[string]any)
	if _, present := pruned["unresolved_reference"]; present {
		t.Errorf("a deliberate prune claimed an unresolved reference: %#v", pruned)
	}
	if pruned["skip_reason"] == nil {
		t.Errorf("a deliberate prune lost its reason: %#v", pruned)
	}
}

func TestSkipMetadataNamesTheAgentASkippedNodeWouldHaveRun(t *testing.T) {
	allNodes := []*NodeConfig{
		{ID: "billing", Type: "agent", AgentName: "BillingAgent"},
		{ID: "escalation", Type: "workflow", WorkflowName: "ReviewWorkflow"},
		{ID: "lookup", Type: "tool", ToolName: "search"},
	}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: allNodes}, dag, NewExecutionState("wf", "e1"))
	defer e.execCtx.Cancel()

	e.skipNode("billing", `not selected by decision "triage"`, SkipCauseNotSelected)
	e.skipNode("escalation", `not selected by decision "triage"`, SkipCauseNotSelected)
	e.skipNode("lookup", `not selected by decision "triage"`, SkipCauseNotSelected)

	billing, _ := nodeResultData(t, brk, "billing")["metadata"].(map[string]any)
	if billing["agent_name"] != "BillingAgent" {
		t.Errorf("agent_name = %#v, want BillingAgent: %#v", billing["agent_name"], billing)
	}
	escalation, _ := nodeResultData(t, brk, "escalation")["metadata"].(map[string]any)
	if escalation["agent_name"] != "ReviewWorkflow" {
		t.Errorf("agent_name = %#v, want the child workflow ReviewWorkflow, as the start event names it: %#v", escalation["agent_name"], escalation)
	}
	lookup, _ := nodeResultData(t, brk, "lookup")["metadata"].(map[string]any)
	if _, present := lookup["agent_name"]; present {
		t.Errorf("a tool node's skip claimed an agent: %#v", lookup)
	}
}

func TestSkipMetadataPlacesANodeThatNeverStarted(t *testing.T) {
	allNodes := []*NodeConfig{
		{ID: "step", Type: "tool", ToolName: "next_step"},
		{ID: "merge", Type: "tool", ToolName: "merge_pr", DependsOn: []string{"step"}, When: "'a' == 'b'"},
		{ID: "notify", Type: "agent", AgentName: "Notifier", DependsOn: []string{"merge"}},
	}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: allNodes}, dag, NewExecutionState("wf", "e1"))
	defer e.execCtx.Cancel()

	e.skipNodeCause("merge", "condition not met", SkipCauseConditionFalse, false)

	merge, _ := nodeResultData(t, brk, "merge")["metadata"].(map[string]any)
	if merge["node_type"] != "tool" || merge["tool_name"] != "merge_pr" {
		t.Errorf("tool node skip lost its type or tool: %#v", merge)
	}
	if deps, _ := merge["depends_on"].([]any); len(deps) != 1 || deps[0] != "step" {
		t.Errorf("depends_on = %#v, want [step]", merge["depends_on"])
	}

	notify, _ := nodeResultData(t, brk, "notify")["metadata"].(map[string]any)
	if notify["node_type"] != "agent" || notify["agent_name"] != "Notifier" {
		t.Errorf("cascaded skip lost its type or agent: %#v", notify)
	}
	if _, present := notify["tool_name"]; present {
		t.Errorf("an agent node's skip claimed a tool: %#v", notify)
	}
	if deps, _ := notify["depends_on"].([]any); len(deps) != 1 || deps[0] != "merge" {
		t.Errorf("depends_on = %#v, want [merge]", notify["depends_on"])
	}
	if want := "all dependencies skipped (parent: merge)"; notify["skip_reason"] != want {
		t.Errorf("cascade skip_reason = %#v, want %q", notify["skip_reason"], want)
	}
}

func TestWhenClauseAbstentionMarkerIsWired(t *testing.T) {
	const partiallyStale = `'{{find_user.output.status}}' == 'nope' and '{{find_user.output.hits}}' == '0'`

	allNodes := []*NodeConfig{
		{ID: "find_user", Type: "tool", ToolName: "search"},
		{ID: "consumer", Type: "agent", AgentName: "a", When: partiallyStale},
	}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: allNodes}, dag, state)
	defer e.execCtx.Cancel()

	proceed, stop := e.evaluateWhenClause("consumer", partiallyStale)
	if proceed || stop {
		t.Fatalf("proceed, stop = %v, %v; the live arm settles this false", proceed, stop)
	}

	metadata, _ := nodeResultData(t, brk, "consumer")["metadata"].(map[string]any)
	if metadata["unresolved_reference"] != true {
		t.Errorf("the partially-resolved abstention was published as an ordinary skip: %#v", metadata)
	}
}

func TestAbstentionMarkerSurvivesTheSkipCascade(t *testing.T) {
	switchNode := &NodeConfig{
		ID:    "gate",
		Type:  "switch",
		Cases: []SwitchCase{{Condition: `'{{find_user.output.hits}}' == '{}'`, Node: "rescue"}},
	}
	rescue := &NodeConfig{ID: "rescue", Type: "agent", AgentName: "a", DependsOn: []string{"gate"}}
	downstream := &NodeConfig{ID: "downstream", Type: "agent", AgentName: "b", DependsOn: []string{"rescue"}}

	allNodes := []*NodeConfig{switchNode, rescue, downstream}
	dag, err := BuildDependencyGraph(allNodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	ranNodeOutputs(state)

	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: allNodes}, dag, state)
	defer e.execCtx.Cancel()

	e.executeSwitchNode(switchNode)

	for _, nodeID := range []string{"rescue", "downstream"} {
		metadata, _ := nodeResultData(t, brk, nodeID)["metadata"].(map[string]any)
		if metadata["unresolved_reference"] != true {
			t.Errorf("%s: abstention marker lost in the cascade: %#v", nodeID, metadata)
		}
	}
}

func TestLoopStoppedReasonReachesTheWire(t *testing.T) {
	nodes := loopNodes(`'{{body1.output.count}}' != '10'`, 10)
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	e, brk := newCapturingExecutor(t, &WorkflowConfig{Nodes: nodes}, dag, state)
	defer e.execCtx.Cancel()

	state.LoopIterations["loop1"] = 0
	state.AddPending("loop1")
	e.handleLoopIterationComplete(e.execCtx.Ctx, "loop1", NodeCompletion{
		NodeID: "body1",
		Output: map[string]any{"status": "done"},
	})

	metadata, _ := nodeResultData(t, brk, "loop1")["metadata"].(map[string]any)
	if got := metadata["stopped_reason"]; got != "condition_unresolved" {
		t.Errorf("stopped_reason = %#v on the wire, want condition_unresolved", got)
	}
}

func nodeResultData(t *testing.T, brk *capturingBroker, nodeID string) map[string]any {
	t.Helper()
	brk.mu.Lock()
	published := append([]capturedMessage(nil), brk.messages...)
	brk.mu.Unlock()
	for _, published := range published {
		data := extractDataPart(t, published.Payload)
		if data["node_id"] == nodeID && data["type"] == "workflow_node_execution_result" {
			return data
		}
	}
	t.Fatalf("no workflow_node_execution_result published for node %q", nodeID)
	return nil
}
