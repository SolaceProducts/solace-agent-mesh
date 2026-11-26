// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestDispatchAgentNode_WorkflowTargetUnresolvedFailsFast(t *testing.T) {
	nodes := []*NodeConfig{{ID: "detect_1", Type: "workflow", WorkflowName: "DetectCountsFromUrl"}}
	cfg := &WorkflowConfig{Name: parentInstanceName, Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("Parent", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	seedCard(e.instance, a2a.AgentCard{Name: "workflow_019e0000-0000-7000-8000-000000000001"})

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	deadTopic := a2a.AgentRequestTopic(cfg.Namespace, "DetectCountsFromUrl")
	for _, m := range brk.snapshot() {
		if m.Topic == deadTopic {
			t.Fatalf("published to subscriber-less topic %q; should have failed fast", deadTopic)
		}
	}
	if e.state.ErrorState == nil || e.state.ErrorState.FailedNodeID != "detect_1" {
		t.Fatalf("expected fast-fail ErrorState for detect_1, got %+v", e.state.ErrorState)
	}
	if !strings.Contains(e.state.ErrorState.ErrorMessage, "DetectCountsFromUrl") {
		t.Errorf("error message should name the unresolved target; got %q", e.state.ErrorState.ErrorMessage)
	}
}

func TestDispatchAgentNode_WorkflowTargetInstanceNameFormPassesThrough(t *testing.T) {
	instanceName := a2a.InstanceName(a2a.KindWorkflow, "019f910e-7e42-737a-87eb-c300c9672a4e")
	nodes := []*NodeConfig{{ID: "detect_1", Type: "workflow", WorkflowName: instanceName}}
	cfg := &WorkflowConfig{Name: "Parent", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("Parent", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	if e.state.ErrorState != nil {
		t.Fatalf("instance-name target must not fast-fail; got ErrorState %+v", e.state.ErrorState)
	}
	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, instanceName)
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			return
		}
	}
	t.Fatalf("no message published to instance-name topic %q; saw %v", wantTopic, capturedTopics(brk))
}

func TestDispatchWorkflowNode_PlainNameColdCachePublishes(t *testing.T) {
	nodes := []*NodeConfig{{ID: "child", Type: "workflow", WorkflowName: "SiblingWorkflow"}}
	cfg := &WorkflowConfig{Name: "ParentWorkflow", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("ParentWorkflow", "exec-cold")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	defer e.execCtx.Cancel()

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	if e.state.ErrorState != nil {
		t.Fatalf("node must not fast-fail on a cold card cache, got %+v", e.state.ErrorState)
	}
	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, "SiblingWorkflow")
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			return
		}
	}
	t.Fatalf("no message published to %q; saw topics=%v", wantTopic, capturedTopics(brk))
}

func TestDispatchWorkflowNode_SelfReferencePublishesBecauseOwnCardIsNeverCached(t *testing.T) {
	nodes := []*NodeConfig{{ID: "recurse", Type: "workflow", WorkflowName: "SelfWorkflow"}}
	cfg := &WorkflowConfig{Name: "SelfWorkflow", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("SelfWorkflow", "exec-self")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	defer e.execCtx.Cancel()

	seedCard(e.instance, a2a.AgentCard{Name: "other_wf"})

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	if e.state.ErrorState != nil {
		t.Fatalf("a self-referencing workflow node must not fast-fail, got %+v", e.state.ErrorState)
	}
	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, "SelfWorkflow")
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			return
		}
	}
	t.Fatalf("no message published to %q; saw topics=%v", wantTopic, capturedTopics(brk))
}

func TestResolveNodeTarget_DisplayNameCollision(t *testing.T) {
	const (
		stale      = "agent_01a0d489_1edb_7e38_8fe3_ae989b624e5e"
		current    = "agent_01a0d48a_a94a_75c4_a782_5f3649365416"
		ephemeral  = "agent_01a0d48b_0000_7000_8000_000000000001"
		workflowID = "workflow_01a0d48c_0000_7000_8000_000000000002"
	)
	newInstance := func(cards ...string) *WorkflowInstance {
		w := &WorkflowInstance{cfg: &WorkflowConfig{}, logger: slog.Default()}
		for _, c := range cards {
			seedDisplayNameCard(w, c, "decider")
		}
		return w
	}

	t.Run("two live agents is an error naming both", func(t *testing.T) {
		_, _, err := newInstance(stale, current).resolveNodeTarget("decider", a2a.KindAgent)
		if err == nil {
			t.Fatal("want ambiguity error, got nil")
		}
		if !strings.Contains(err.Error(), stale) || !strings.Contains(err.Error(), current) {
			t.Errorf("error should list both candidates; got %q", err)
		}
	})

	t.Run("ephemeral test wrapper does not collide", func(t *testing.T) {
		w := newInstance(current, ephemeral)
		addCardExtensions(w, ephemeral,
			a2a.AgentExtension{URI: a2a.EphemeralAgentExtensionURI})
		got, known, err := w.resolveNodeTarget("decider", a2a.KindAgent)
		if err != nil || !known || got != current {
			t.Fatalf("got (%q, %v, %v), want (%q, true, nil)", got, known, err, current)
		}
	})

	t.Run("workflow card does not collide with agent node", func(t *testing.T) {
		got, known, err := newInstance(current, workflowID).resolveNodeTarget("decider", a2a.KindAgent)
		if err != nil || !known || got != current {
			t.Fatalf("got (%q, %v, %v), want (%q, true, nil)", got, known, err, current)
		}
	})

	t.Run("plain-named workflow card does not collide with agent node", func(t *testing.T) {
		const plainWorkflow = "MuleWorkflow"
		w := newInstance(current, plainWorkflow)
		addCardExtensions(w, plainWorkflow,
			a2a.AgentExtension{URI: a2a.ExtensionURIAgentType, Params: map[string]any{"type": "workflow"}})
		got, known, err := w.resolveNodeTarget("decider", a2a.KindAgent)
		if err != nil || !known || got != current {
			t.Fatalf("got (%q, %v, %v), want (%q, true, nil)", got, known, err, current)
		}
	})

	t.Run("plain-named agent card does not collide with workflow node", func(t *testing.T) {
		const plainAgent = "PlainAgent"
		got, known, err := newInstance(workflowID, plainAgent).resolveNodeTarget("decider", a2a.KindWorkflow)
		if err != nil || !known || got != workflowID {
			t.Fatalf("got (%q, %v, %v), want (%q, true, nil)", got, known, err, workflowID)
		}
	})

	t.Run("plain-named agent cards collide for an agent node", func(t *testing.T) {
		const (
			plainA = "PlainA"
			plainB = "PlainB"
		)
		_, _, err := newInstance(plainA, plainB).resolveNodeTarget("decider", a2a.KindAgent)
		if err == nil {
			t.Fatal("want ambiguity error, got nil")
		}
		if !strings.Contains(err.Error(), plainA) || !strings.Contains(err.Error(), plainB) {
			t.Errorf("error should list both candidates; got %q", err)
		}
	})

	t.Run("exact broker name wins over display names", func(t *testing.T) {
		got, known, err := newInstance(stale, current).resolveNodeTarget(current, a2a.KindAgent)
		if err != nil || !known || got != current {
			t.Fatalf("got (%q, %v, %v), want (%q, true, nil)", got, known, err, current)
		}
	})
}

func TestDispatchAgentNode_AmbiguousTargetFailsFast(t *testing.T) {
	const (
		stale   = "agent_01a0d489_1edb_7e38_8fe3_ae989b624e5e"
		current = "agent_01a0d48a_a94a_75c4_a782_5f3649365416"
	)
	nodes := []*NodeConfig{{ID: "decide", Type: "agent", AgentName: "decider"}}
	cfg := &WorkflowConfig{Name: "Parent", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("Parent", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, stale, "decider")
	seedDisplayNameCard(e.instance, current, "decider")

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	for _, m := range brk.snapshot() {
		for _, name := range []string{stale, current, "decider"} {
			if m.Topic == a2a.AgentRequestTopic(cfg.Namespace, name) {
				t.Fatalf("published to %q; an ambiguous target must not be dispatched", m.Topic)
			}
		}
	}
	if e.state.ErrorState == nil || e.state.ErrorState.FailedNodeID != "decide" {
		t.Fatalf("expected node failure for decide, got %+v", e.state.ErrorState)
	}
	if !strings.Contains(e.state.ErrorState.ErrorMessage, "matches multiple components") {
		t.Errorf("error should explain the ambiguity; got %q", e.state.ErrorState.ErrorMessage)
	}
}

func TestResolveNodeTarget_KindMismatch(t *testing.T) {
	const (
		agentCard    = "agent_01a0d48a_a94a_75c4_a782_5f3649365416"
		workflowCard = "workflow_01a0d48c_0000_7000_8000_000000000002"
	)
	newInstance := func(cards ...string) *WorkflowInstance {
		w := &WorkflowInstance{cfg: &WorkflowConfig{}, logger: slog.Default()}
		for _, c := range cards {
			seedDisplayNameCard(w, c, "Child")
		}
		return w
	}

	t.Run("agent node matching only a workflow card", func(t *testing.T) {
		_, _, err := newInstance(workflowCard).resolveNodeTarget("Child", a2a.KindAgent)
		if err == nil || err.Error() != "is a workflow; use a workflow node" {
			t.Fatalf("got %v, want the use-a-workflow-node error", err)
		}
	})

	t.Run("workflow node matching only an agent card", func(t *testing.T) {
		_, _, err := newInstance(agentCard).resolveNodeTarget("Child", a2a.KindWorkflow)
		if err == nil || err.Error() != "is an agent; use an agent node" {
			t.Fatalf("got %v, want the use-an-agent-node error", err)
		}
	})

	t.Run("ephemeral-only match is unknown, not an error", func(t *testing.T) {
		w := newInstance(workflowCard)
		addCardExtensions(w, workflowCard,
			a2a.AgentExtension{URI: a2a.EphemeralAgentExtensionURI})
		got, known, err := w.resolveNodeTarget("Child", a2a.KindAgent)
		if err != nil || known || got != "Child" {
			t.Fatalf("got (%q, %v, %v), want (%q, false, nil)", got, known, err, "Child")
		}
	})
}

func TestDispatchWorkflowNode_ConsidersOnlyWorkflowCards(t *testing.T) {
	const (
		workflowCard = "workflow_01a0d48c_0000_7000_8000_000000000002"
		agentCard    = "agent_01a0d48a_a94a_75c4_a782_5f3649365416"
	)
	nodes := []*NodeConfig{{ID: "n1", Type: "workflow", WorkflowName: "Child"}}
	cfg := &WorkflowConfig{Name: "Parent", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("Parent", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, workflowCard, "Child")
	seedDisplayNameCard(e.instance, agentCard, "Child")

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	if e.state.ErrorState != nil {
		t.Fatalf("node must dispatch to the workflow card, got ErrorState %+v", e.state.ErrorState)
	}
	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, workflowCard)
	unwanted := a2a.AgentRequestTopic(cfg.Namespace, agentCard)
	for _, m := range brk.snapshot() {
		if m.Topic == unwanted {
			t.Fatalf("published to agent card topic %q; a workflow node must only target workflow cards", unwanted)
		}
	}
	if requestPublishedTo(brk, wantTopic) == nil {
		t.Fatalf("no request published to workflow card topic %q; saw %v", wantTopic, capturedTopics(brk))
	}
}

func TestDispatchWorkflowNode_AmbiguousTargetFailsFast(t *testing.T) {
	const (
		stale   = "workflow_01a0d489_1edb_7e38_8fe3_ae989b624e5e"
		current = "workflow_01a0d48a_a94a_75c4_a782_5f3649365416"
	)
	nodes := []*NodeConfig{{ID: "n1", Type: "workflow", WorkflowName: "Child"}}
	cfg := &WorkflowConfig{Name: "Parent", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("Parent", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, stale, "Child")
	seedDisplayNameCard(e.instance, current, "Child")

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	assertNoRequestPublished(t, brk, cfg.Namespace, stale, current, "Child")
	if e.state.ErrorState == nil || e.state.ErrorState.FailedNodeID != "n1" {
		t.Fatalf("expected node failure for n1, got %+v", e.state.ErrorState)
	}
	if !strings.Contains(e.state.ErrorState.ErrorMessage, "matches multiple components") {
		t.Errorf("error should explain the ambiguity; got %q", e.state.ErrorState.ErrorMessage)
	}
}

func TestDispatchAgentNode_WorkflowOnlyMatchFailsFast(t *testing.T) {
	const workflowCard = "workflow_01a0d48c_0000_7000_8000_000000000002"
	nodes := []*NodeConfig{{ID: "n1", Type: "agent", AgentName: "Child"}}
	cfg := &WorkflowConfig{Name: "Parent", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("Parent", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, workflowCard, "Child")

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	assertNoRequestPublished(t, brk, cfg.Namespace, workflowCard, "Child")
	if e.state.ErrorState == nil || e.state.ErrorState.FailedNodeID != "n1" {
		t.Fatalf("expected node failure for n1, got %+v", e.state.ErrorState)
	}
	if !strings.Contains(e.state.ErrorState.ErrorMessage, "is a workflow") {
		t.Errorf("error should tell the author to use a workflow node; got %q", e.state.ErrorState.ErrorMessage)
	}
}

func TestDispatchAgentNode_SchemaFromDispatchedTarget(t *testing.T) {
	const (
		ephemeralBroker = "agent_01a0d48b_0000_7000_8000_000000000001"
		realBroker      = "agent_01a0d48c_0000_7000_8000_000000000002"
		schemasURI      = "https://solace.com/a2a/extensions/sam/schemas"
	)
	nodes := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "uppercaser"}}
	cfg := &WorkflowConfig{Name: "Parent", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("Parent", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	seedDisplayNameCard(e.instance, ephemeralBroker, "uppercaser")
	addCardExtensions(e.instance, ephemeralBroker,
		a2a.AgentExtension{URI: a2a.EphemeralAgentExtensionURI},
		a2a.AgentExtension{URI: schemasURI, Params: map[string]any{
			"input_schema":  map[string]any{"type": "WRONG"},
			"output_schema": map[string]any{"type": "WRONG"},
		}})
	seedDisplayNameCard(e.instance, realBroker, "uppercaser")
	addCardExtensions(e.instance, realBroker,
		a2a.AgentExtension{URI: schemasURI, Params: map[string]any{
			"input_schema":  map[string]any{"type": "object"},
			"output_schema": map[string]any{"type": "string"},
		}})

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	topic := a2a.AgentRequestTopic(cfg.Namespace, realBroker)
	msg := requestPublishedTo(brk, topic)
	if msg == nil {
		t.Fatalf("no request published to %q; saw %v", topic, capturedTopics(brk))
	}
	sir := structuredInvocationRequest(t, msg.Payload)
	in, _ := sir["input_schema"].(map[string]any)
	if in["type"] != "object" {
		t.Errorf("input_schema = %v, want the dispatched target's {type: object}", sir["input_schema"])
	}
	out, _ := sir["output_schema"].(map[string]any)
	if out["type"] != "string" {
		t.Errorf("output_schema = %v, want the dispatched target's {type: string}", sir["output_schema"])
	}
}

func requestPublishedTo(brk *capturingBroker, topic string) *capturedMessage {
	for _, m := range brk.snapshot() {
		if m.Topic == topic {
			return &m
		}
	}
	return nil
}

func assertNoRequestPublished(t *testing.T, brk *capturingBroker, namespace string, targets ...string) {
	t.Helper()
	for _, name := range targets {
		if topic := a2a.AgentRequestTopic(namespace, name); requestPublishedTo(brk, topic) != nil {
			t.Fatalf("published to %q; a failed target must not be dispatched", topic)
		}
	}
}

func structuredInvocationRequest(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var req struct {
		Params struct {
			Message struct {
				Parts []struct {
					Data map[string]any `json:"data"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	for _, p := range req.Params.Message.Parts {
		if p.Data["type"] == "structured_invocation_request" {
			return p.Data
		}
	}
	t.Fatalf("no structured_invocation_request part in %s", payload)
	return nil
}
