// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

var parentInstanceName = a2a.InstanceName(a2a.KindWorkflow, "019f9100-0000-7000-8000-00000000abcd")

func warmCache(e *DAGExecutor) {
	seedCard(e.instance, a2a.AgentCard{Name: "workflow_019e0000-0000-7000-8000-000000000001"})
}

func TestLaunchMapIteration_WorkflowTargetUnresolvedFailsFast(t *testing.T) {
	template := &NodeConfig{ID: "detect_one", Type: "workflow", WorkflowName: "DetectCountsFromUrl"}
	mapNode := &NodeConfig{ID: "fan", Type: "map", TemplateNode: "detect_one", ItemsExpression: "{{workflow.input.urls}}"}
	nodes := []*NodeConfig{mapNode, template}
	cfg := &WorkflowConfig{Name: parentInstanceName, Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState(cfg.Name, "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	warmCache(e)

	state.Metadata[mapNode.ID+"_results"] = make([]any, 1)
	state.Metadata[mapNode.ID+"_done"] = make([]bool, 1)

	e.launchMapIteration(context.Background(), mapNode, "http://example/cam1.jpg", 0)

	deadTopic := a2a.AgentRequestTopic(cfg.Namespace, "DetectCountsFromUrl")
	for _, m := range brk.snapshot() {
		if m.Topic == deadTopic {
			t.Fatalf("map iteration published to subscriber-less topic %q; should have failed fast", deadTopic)
		}
	}

	results, _ := state.Metadata[mapNode.ID+"_results"].([]any)
	if len(results) == 0 || results[0] == nil {
		t.Fatalf("expected branch 0 to be completed with an error, got %+v", results)
	}
	branch, ok := results[0].(map[string]any)
	if !ok {
		t.Fatalf("branch 0 result is %T, want map[string]any carrying an error", results[0])
	}
	msg, _ := branch["error"].(string)
	if !strings.Contains(msg, "DetectCountsFromUrl") {
		t.Errorf("branch error should name the unresolved target; got %q", msg)
	}
}

func TestDispatchLoopIteration_WorkflowTargetUnresolvedFailsFast(t *testing.T) {
	body := &NodeConfig{ID: "refine_step", Type: "workflow", WorkflowName: "RefineSubWorkflow"}
	loopNode := &NodeConfig{ID: "refine", Type: "loop", BodyNode: "refine_step", Condition: "false", MaxIterations: 3}
	nodes := []*NodeConfig{loopNode, body}
	cfg := &WorkflowConfig{Name: parentInstanceName, Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState(cfg.Name, "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	warmCache(e)

	e.dispatchLoopIteration(context.Background(), loopNode, 0)

	deadTopic := a2a.AgentRequestTopic(cfg.Namespace, "RefineSubWorkflow")
	for _, m := range brk.snapshot() {
		if m.Topic == deadTopic {
			t.Fatalf("loop iteration published to subscriber-less topic %q; should have failed fast", deadTopic)
		}
	}
	if e.state.ErrorState == nil {
		t.Fatal("expected the loop node to fail fast, got no ErrorState")
	}
	if !strings.Contains(e.state.ErrorState.ErrorMessage, "RefineSubWorkflow") {
		t.Errorf("error message should name the unresolved target; got %q", e.state.ErrorState.ErrorMessage)
	}
}

const (
	ambiguousStale   = "agent_01a0d489_1edb_7e38_8fe3_ae989b624e5e"
	ambiguousCurrent = "agent_01a0d48a_a94a_75c4_a782_5f3649365416"
)

func assertNotPublishedToCandidates(t *testing.T, brk *capturingBroker, namespace string) {
	t.Helper()
	for _, m := range brk.snapshot() {
		for _, name := range []string{ambiguousStale, ambiguousCurrent, "decider"} {
			if m.Topic == a2a.AgentRequestTopic(namespace, name) {
				t.Fatalf("published to %q; an ambiguous target must not be dispatched", m.Topic)
			}
		}
	}
}

func TestLaunchMapIteration_AmbiguousTargetFailsFast(t *testing.T) {
	template := &NodeConfig{ID: "decide_one", Type: "agent", AgentName: "decider"}
	mapNode := &NodeConfig{ID: "fan", Type: "map", TemplateNode: "decide_one", ItemsExpression: "{{workflow.input.urls}}"}
	nodes := []*NodeConfig{mapNode, template}
	cfg := &WorkflowConfig{Name: parentInstanceName, Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState(cfg.Name, "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, ambiguousStale, "decider")
	seedDisplayNameCard(e.instance, ambiguousCurrent, "decider")

	state.Metadata[mapNode.ID+"_results"] = make([]any, 1)
	state.Metadata[mapNode.ID+"_done"] = make([]bool, 1)

	e.launchMapIteration(context.Background(), mapNode, "http://example/cam1.jpg", 0)

	assertNotPublishedToCandidates(t, brk, cfg.Namespace)
	results, _ := state.Metadata[mapNode.ID+"_results"].([]any)
	if len(results) == 0 || results[0] == nil {
		t.Fatalf("expected branch 0 to be completed with an error, got %+v", results)
	}
	branch, ok := results[0].(map[string]any)
	if !ok {
		t.Fatalf("branch 0 result is %T, want map[string]any carrying an error", results[0])
	}
	msg, _ := branch["error"].(string)
	if !strings.Contains(msg, "matches multiple components") {
		t.Errorf("branch error should explain the ambiguity; got %q", msg)
	}
}

func TestDispatchLoopIteration_AmbiguousTargetFailsFast(t *testing.T) {
	body := &NodeConfig{ID: "decide_step", Type: "agent", AgentName: "decider"}
	loopNode := &NodeConfig{ID: "refine", Type: "loop", BodyNode: "decide_step", Condition: "false", MaxIterations: 3}
	nodes := []*NodeConfig{loopNode, body}
	cfg := &WorkflowConfig{Name: parentInstanceName, Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState(cfg.Name, "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, ambiguousStale, "decider")
	seedDisplayNameCard(e.instance, ambiguousCurrent, "decider")

	e.dispatchLoopIteration(context.Background(), loopNode, 0)

	assertNotPublishedToCandidates(t, brk, cfg.Namespace)
	if e.state.ErrorState == nil {
		t.Fatal("expected the loop node to fail fast, got no ErrorState")
	}
	if !strings.Contains(e.state.ErrorState.ErrorMessage, "matches multiple components") {
		t.Errorf("error message should explain the ambiguity; got %q", e.state.ErrorState.ErrorMessage)
	}
}

func TestDispatchAgentNode_YamlAuthoredParentPublishesPlainName(t *testing.T) {
	nodes := []*NodeConfig{{ID: "call_child", Type: "workflow", WorkflowName: "SiblingWorkflow"}}
	cfg := &WorkflowConfig{Name: "ParentWorkflow", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState(cfg.Name, "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	warmCache(e)

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	if e.state.ErrorState != nil {
		t.Fatalf("a YAML-authored parent must not fast-fail a plain-name target; got %+v", e.state.ErrorState)
	}
	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, "SiblingWorkflow")
	found := false
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a publish to %q so the running sibling receives it", wantTopic)
	}
}

func TestRoutableWorkflowTargetReason_Exemptions(t *testing.T) {
	instanceTarget := a2a.InstanceName(a2a.KindWorkflow, "019f910e-7e42-737a-87eb-c300c9672a4e")

	cases := []struct {
		name       string
		parentName string
		target     string
		warm       bool
		want       string
	}{
		{"instance-name target always routes", parentInstanceName, instanceTarget, true, "instanceName"},
		{"self reference in instance-name form matches instanceName first", parentInstanceName, parentInstanceName, true, "instanceName"},
		{"self reference by plain name routes", "SelfWorkflow", "SelfWorkflow", true, "selfReference"},
		{"cold cache proves nothing", parentInstanceName, "DetectCountsFromUrl", false, "coldCardCache"},
		{"yaml-authored parent never fast-fails", "ParentWorkflow", "SiblingWorkflow", true, "yamlAuthoredParent"},
		{"deployed parent, warm cache, unhydrated target fails fast", parentInstanceName, "DetectCountsFromUrl", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []*NodeConfig{{ID: "n", Type: "workflow", WorkflowName: tc.target}}
			cfg := &WorkflowConfig{Name: tc.parentName, Namespace: "test-ns", Nodes: nodes}
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatal(err)
			}
			e, _ := newCapturingExecutor(t, cfg, dag, NewExecutionState(cfg.Name, "exec-1"))
			e.instance.logger = slog.Default()
			if tc.warm {
				warmCache(e)
			}
			if got := e.routableWorkflowTargetReason(tc.target); got != tc.want {
				t.Errorf("routableWorkflowTargetReason(%q) with parent %q warm=%v = %q, want %q",
					tc.target, tc.parentName, tc.warm, got, tc.want)
			}
		})
	}
}
