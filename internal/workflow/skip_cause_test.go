// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func newSkipCauseExecutor(t *testing.T) *DAGExecutor {
	t.Helper()
	nodes := []*NodeConfig{
		{ID: "root", Type: "agent", AgentName: "a"},
		{ID: "child", Type: "agent", AgentName: "a", DependsOn: []string{"root"}},
		{ID: "grandchild", Type: "agent", AgentName: "a", DependsOn: []string{"child"}},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	e := newTestDAGExecutor(&WorkflowConfig{Name: "wf", Nodes: nodes}, dag, NewExecutionState("wf", "e1"))
	t.Cleanup(e.execCtx.Cancel)
	return e
}

func TestSkipCauseCascadeKeepsTheRootCause(t *testing.T) {
	t.Run("from a prune", func(t *testing.T) {
		e := newSkipCauseExecutor(t)
		e.skipBranch("root", &NodeConfig{ID: "sw", Type: "switch"}, SkipCauseNotSelected, false)
		for _, id := range []string{"root", "child", "grandchild"} {
			if cause := e.state.SkipCause(id); cause != SkipCauseNotSelected {
				t.Errorf("%s cause = %q, want %q", id, cause, SkipCauseNotSelected)
			}
		}
	})
	t.Run("from a failure", func(t *testing.T) {
		e := newSkipCauseExecutor(t)
		e.cfg.FailFast = false
		e.handleNodeError("root", errors.New("boom"))
		want := map[string]SkipCause{"root": SkipCauseFailed, "child": SkipCauseUpstreamFailed, "grandchild": SkipCauseUpstreamFailed}
		for id, cause := range want {
			if got := e.state.SkipCause(id); got != cause {
				t.Errorf("%s cause = %q, want %q", id, got, cause)
			}
		}
		if reason := e.state.SkippedNodes["child"]; reason != `dependency "root" failed` {
			t.Errorf("child reason = %q, want the display reason kept", reason)
		}
	})
	t.Run("from a switch that could not decide", func(t *testing.T) {
		e := newSkipCauseExecutor(t)
		e.skipBranch("root", &NodeConfig{ID: "sw", Type: "switch"}, SkipCauseUnresolved, true)
		if cause := e.state.SkipCause("grandchild"); cause != SkipCauseUnresolved {
			t.Errorf("grandchild cause = %q, want %q", cause, SkipCauseUnresolved)
		}
	})
}

func TestSkipCauseOfAJoinIsTheGravestOfItsDependencies(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "f", Type: "agent", AgentName: "a"},
		{ID: "b", Type: "agent", AgentName: "a", DependsOn: []string{"f"}},
		{ID: "a", Type: "agent", AgentName: "a"},
		{ID: "x", Type: "agent", AgentName: "a", DependsOn: []string{"a", "b"}},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	e := newTestDAGExecutor(&WorkflowConfig{Name: "wf", Nodes: nodes}, dag, NewExecutionState("wf", "e1"))
	t.Cleanup(e.execCtx.Cancel)
	e.cfg.FailFast = false
	e.handleNodeError("f", errors.New("boom"))
	e.skipBranch("a", &NodeConfig{ID: "sw", Type: "switch"}, SkipCauseNotSelected, false)
	if got := e.state.SkipCause("x"); got != SkipCauseUpstreamFailed {
		t.Errorf("x cause = %q, want %q", got, SkipCauseUpstreamFailed)
	}
}

func TestRoutesOfAnEscalationWhoseWhenCouldNotBeEvaluatedAreNotPruned(t *testing.T) {
	doc := strings.Replace(escalationRoutedWorkflow,
		"  - {id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage]}\n",
		"  - {id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage], when: \"'{{triage.output.missing}}' == 'x'\"}\n", 1)
	run := newEscalationRoutedRun(t, doc, &fakeJev{respond: departmentAnswer("billing", 0.5)}, "tech")
	run.execute(t)
	run.e.mu.Lock()
	defer run.e.mu.Unlock()
	for _, id := range []string{"billing", "tech"} {
		if got := run.e.state.SkipCause(id); got != SkipCauseUnresolved {
			t.Errorf("%s cause = %q, want %q; skipped=%v causes=%v", id, got, SkipCauseUnresolved,
				run.e.state.SkippedNodes, run.e.state.SkipCauses)
		}
	}
}

func TestSkipCauseOfABranchADecisionChoseAgainstIsNotSelected(t *testing.T) {
	t.Run("the decision's own answer", func(t *testing.T) {
		run := newDecisionRun(t, routedDecisionWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.9)}, true)
		run.execute(t)
		run.e.mu.Lock()
		defer run.e.mu.Unlock()
		if got := run.e.state.SkipCause("tech"); got != SkipCauseNotSelected {
			t.Errorf("tech cause = %q, want %q", got, SkipCauseNotSelected)
		}
	})
	t.Run("the escalation's answer", func(t *testing.T) {
		run := newEscalationRoutedRun(t, escalationRoutedWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.5)}, "tech")
		run.execute(t)
		run.e.mu.Lock()
		defer run.e.mu.Unlock()
		if got := run.e.state.SkipCause("billing"); got != SkipCauseNotSelected {
			t.Errorf("billing cause = %q, want %q; skipped=%v", got, SkipCauseNotSelected, run.e.state.SkippedNodes)
		}
	})
}

func TestSkipCauseOfADiamondBelowAFailureIsUpstreamFailed(t *testing.T) {
	for name, xDeps := range map[string][]string{"f first": {"f", "b"}, "b first": {"b", "f"}} {
		t.Run(name, func(t *testing.T) {
			nodes := []*NodeConfig{
				{ID: "f", Type: "agent", AgentName: "a"},
				{ID: "b", Type: "agent", AgentName: "a", DependsOn: []string{"f"}},
				{ID: "x", Type: "agent", AgentName: "a", DependsOn: xDeps},
			}
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatal(err)
			}
			e := newTestDAGExecutor(&WorkflowConfig{Name: "wf", Nodes: nodes}, dag, NewExecutionState("wf", "e1"))
			t.Cleanup(e.execCtx.Cancel)
			e.cfg.FailFast = false
			e.handleNodeError("f", errors.New("boom"))
			if got := e.state.SkipCause("x"); got != SkipCauseUpstreamFailed {
				t.Errorf("x cause = %q, want %q", got, SkipCauseUpstreamFailed)
			}
		})
	}
}

func TestSkipCausesInAPersistedSnapshot(t *testing.T) {
	state := NewExecutionState("wf", "e1")
	state.markSkipped("pruned", "condition not met", SkipCauseConditionFalse)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored WorkflowExecutionState
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if cause := restored.SkipCause("pruned"); cause != SkipCauseConditionFalse {
		t.Errorf("restored cause = %q, want %q", cause, SkipCauseConditionFalse)
	}

	var old WorkflowExecutionState
	if err := json.Unmarshal([]byte(`{"skipped_nodes":{"a":"condition not met"}}`), &old); err != nil {
		t.Fatal(err)
	}
	if !old.IsNodeSkipped("a") || old.SkipCause("a") != SkipCauseUnknown || old.SkipCause("a").Pruned() {
		t.Errorf("an old snapshot's skip = skipped %v, cause %q; want skipped with an unknown cause", old.IsNodeSkipped("a"), old.SkipCause("a"))
	}
	old.markSkipped("b", "condition not met", SkipCauseConditionFalse)
	if old.SkipCause("b") != SkipCauseConditionFalse {
		t.Error("a skip recorded after restoring an old snapshot lost its cause")
	}
}
