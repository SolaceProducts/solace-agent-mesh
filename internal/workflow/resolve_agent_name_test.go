// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func TestResolveAgentName(t *testing.T) {
	w := &WorkflowInstance{
		cfg:    &WorkflowConfig{},
		logger: slog.Default(),
	}

	uuid := "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	seedCard(w, a2a.AgentCard{
		Name: uuid,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI: "https://solace.com/a2a/extensions/display-name",
					Params: map[string]any{
						"display_name": "uppercaser",
					},
				},
			},
		},
	})
	seedCard(w, a2a.AgentCard{Name: "Concierge"})

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"display-name resolves to broker name", "uppercaser", uuid},
		{"broker name passes through unchanged", uuid, uuid},
		{"name-matching card returns its broker name", "Concierge", "Concierge"},
		{"unknown name falls through (caller decides)", "ghost", "ghost"},
		{"empty input returns empty without panic", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.resolveAgentName(tc.in); got != tc.want {
				t.Errorf("resolveAgentName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDispatchAgentNode_PublishesToResolvedBrokerTopic(t *testing.T) {
	const (
		brokerName  = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
		displayName = "uppercaser"
	)

	nodes := []*NodeConfig{
		{ID: "step1", Type: "agent", AgentName: displayName},
	}
	cfg := &WorkflowConfig{
		Name:      "TestWorkflow",
		Namespace: "test-ns",
		Nodes:     nodes,
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("TestWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	seedCard(e.instance, a2a.AgentCard{
		Name: brokerName,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI: "https://solace.com/a2a/extensions/display-name",
					Params: map[string]any{
						"display_name": displayName,
					},
				},
			},
		},
	})

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, brokerName)
	unwantedTopic := a2a.AgentRequestTopic(cfg.Namespace, displayName)
	for _, m := range brk.snapshot() {
		if m.Topic == unwantedTopic {
			t.Fatalf("published to display-name topic %q; resolution should have rewritten it to %q",
				unwantedTopic, wantTopic)
		}
	}
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			return
		}
	}
	t.Fatalf("no message published to resolved broker topic %q; saw topics=%v",
		wantTopic, capturedTopics(brk))
}

func TestDispatchAgentNode_UnknownAgentNamePassesThrough(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "step1", Type: "agent", AgentName: "Steel"},
	}
	cfg := &WorkflowConfig{
		Name:      "TestWorkflow",
		Namespace: "test-ns",
		Nodes:     nodes,
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("TestWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, "Steel")
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			return
		}
	}
	t.Fatalf("no message published to %q; saw topics=%v", wantTopic, capturedTopics(brk))
}

func TestLaunchMapIteration_PublishesToResolvedBrokerTopic(t *testing.T) {
	const (
		brokerName  = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
		displayName = "uppercaser"
	)
	template := &NodeConfig{ID: "double_each", Type: "agent", AgentName: displayName}
	mapNode := &NodeConfig{ID: "fan_out", Type: "map", TemplateNode: "double_each"}
	cfg := &WorkflowConfig{Name: "MapWorkflow", Namespace: "test-ns", Nodes: []*NodeConfig{mapNode, template}}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("MapWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, brokerName, displayName)

	e.launchMapIteration(context.Background(), mapNode, map[string]any{"value": 1}, 0)

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, brokerName)
	unwanted := a2a.AgentRequestTopic(cfg.Namespace, displayName)
	assertPublishedToBrokerTopic(t, brk, wantTopic, unwanted)
}

func TestDispatchLoopIteration_PublishesToResolvedBrokerTopic(t *testing.T) {
	const (
		brokerName  = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
		displayName = "uppercaser"
	)
	body := &NodeConfig{ID: "worker", Type: "agent", AgentName: displayName}
	loopNode := &NodeConfig{ID: "retry_loop", Type: "loop", BodyNode: "worker", MaxIterations: 3}
	cfg := &WorkflowConfig{Name: "LoopWorkflow", Namespace: "test-ns", Nodes: []*NodeConfig{loopNode, body}}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("LoopWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, brokerName, displayName)

	e.dispatchLoopIteration(context.Background(), loopNode, 0)

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, brokerName)
	unwanted := a2a.AgentRequestTopic(cfg.Namespace, displayName)
	assertPublishedToBrokerTopic(t, brk, wantTopic, unwanted)
}

func TestAgentSchemas_ExactBrokerNameLookup(t *testing.T) {
	const (
		brokerName  = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
		displayName = "uppercaser"
	)
	w := &WorkflowInstance{
		cfg:    &WorkflowConfig{},
		logger: slog.Default(),
	}
	seedCard(w, a2a.AgentCard{
		Name: brokerName,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI:    "https://solace.com/a2a/extensions/display-name",
					Params: map[string]any{"display_name": displayName},
				},
				{
					URI: "https://solace.com/a2a/extensions/sam/schemas",
					Params: map[string]any{
						"input_schema":  map[string]any{"type": "object"},
						"output_schema": map[string]any{"type": "object"},
					},
				},
			},
		},
	})

	in, out := w.agentSchemas(brokerName)
	if in == nil || out == nil {
		t.Fatalf("schemas not found via broker name: in=%v out=%v", in, out)
	}
	if in, out := w.agentSchemas(displayName); in != nil || out != nil {
		t.Fatalf("display name must not be re-resolved; got in=%v out=%v", in, out)
	}
}

func TestResolveAgentName_CollisionPicksLowestBrokerName(t *testing.T) {
	w := &WorkflowInstance{
		cfg:    &WorkflowConfig{},
		logger: slog.Default(),
	}
	for _, n := range []string{"agent_zzz", "agent_aaa", "agent_mmm"} {
		seedCard(w, a2a.AgentCard{
			Name: n,
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{
					URI:    "https://solace.com/a2a/extensions/display-name",
					Params: map[string]any{"display_name": "duplicate"},
				}},
			},
		})
	}

	for range 50 {
		if got := w.resolveAgentName("duplicate"); got != "agent_aaa" {
			t.Fatalf("collision resolution non-deterministic: got %q, want %q", got, "agent_aaa")
		}
	}
}

func TestCancelActiveSubTasks_PublishesToResolvedBrokerTopic(t *testing.T) {
	const (
		brokerName  = "agent_0199aaaa_bbbb_75d8_828e_77a0d1d9221f"
		displayName = "uppercaser"
	)
	nodes := []*NodeConfig{{ID: "step1", Type: "agent", AgentName: displayName}}
	cfg := &WorkflowConfig{Name: "TestWorkflow", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("TestWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, brokerName, displayName)

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	afterDispatch := len(brk.snapshot())
	e.instance.cancelActiveSubTasks(&WorkflowExecution{State: state})

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, brokerName)
	unwanted := a2a.AgentRequestTopic(cfg.Namespace, displayName)
	assertCancelTopic(t, brk, afterDispatch, wantTopic, unwanted)
}

func TestCancelActiveSubTasks_MapIterations(t *testing.T) {
	const (
		brokerName  = "agent_0199aaaa_bbbb_75d8_828e_77a0d1d9221f"
		displayName = "uppercaser"
	)
	template := &NodeConfig{ID: "double_each", Type: "agent", AgentName: displayName}
	mapNode := &NodeConfig{ID: "fan_out", Type: "map", TemplateNode: "double_each"}
	cfg := &WorkflowConfig{Name: "MapWorkflow", Namespace: "test-ns", Nodes: []*NodeConfig{mapNode, template}}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("MapWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, brokerName, displayName)

	e.launchMapIteration(context.Background(), mapNode, map[string]any{"value": 1}, 0)
	e.launchMapIteration(context.Background(), mapNode, map[string]any{"value": 2}, 1)

	if got := len(state.SnapshotSubTasks()); got != 2 {
		t.Fatalf("expected 2 tracked sub-tasks, got %d", got)
	}
	afterDispatch := len(brk.snapshot())
	e.instance.cancelActiveSubTasks(&WorkflowExecution{State: state})

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, brokerName)
	unwanted := a2a.AgentRequestTopic(cfg.Namespace, displayName)
	cancels := 0
	for _, m := range brk.snapshot()[afterDispatch:] {
		if m.Topic == unwanted {
			t.Fatalf("cancel published to display-name topic %q; want %q", unwanted, wantTopic)
		}
		if m.Topic == wantTopic {
			cancels++
		}
	}
	if cancels != 2 {
		t.Fatalf("expected 2 cancels to broker topic %q, got %d", wantTopic, cancels)
	}
}

func TestCancelActiveSubTasks_LoopIterations_UsesRecordedTargetAfterCardRemoved(t *testing.T) {
	const (
		brokerName  = "agent_0199loop_cafe"
		displayName = "Loop Cancel Agent"
	)
	body := &NodeConfig{ID: "worker", Type: "agent", AgentName: displayName}
	loopNode := &NodeConfig{ID: "retry_loop", Type: "loop", BodyNode: "worker", MaxIterations: 3}
	cfg := &WorkflowConfig{Name: "LoopWorkflow", Namespace: "test-ns", Nodes: []*NodeConfig{loopNode, body}}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("LoopWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, brokerName, displayName)

	e.dispatchLoopIteration(context.Background(), loopNode, 0)

	unseedCard(e.instance, brokerName)

	afterDispatch := len(brk.snapshot())
	e.instance.cancelActiveSubTasks(&WorkflowExecution{State: state})

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, brokerName)
	unwanted := a2a.AgentRequestTopic(cfg.Namespace, displayName)
	assertCancelTopic(t, brk, afterDispatch, wantTopic, unwanted)
}

func TestCancelActiveSubTasks_UsesRecordedTargetAfterCardRemoved(t *testing.T) {
	const (
		brokerName  = "agent_0199aaaa_bbbb_75d8_828e_77a0d1d9221f"
		displayName = "uppercaser"
	)
	nodes := []*NodeConfig{{ID: "step1", Type: "agent", AgentName: displayName}}
	cfg := &WorkflowConfig{Name: "TestWorkflow", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("TestWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, brokerName, displayName)

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	unseedCard(e.instance, brokerName)

	afterDispatch := len(brk.snapshot())
	e.instance.cancelActiveSubTasks(&WorkflowExecution{State: state})

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, brokerName)
	unwanted := a2a.AgentRequestTopic(cfg.Namespace, displayName)
	assertCancelTopic(t, brk, afterDispatch, wantTopic, unwanted)
}

func TestCancelActiveSubTasks_LegacySnapshotFallsBackToRegistry(t *testing.T) {
	const (
		brokerName  = "agent_0199aaaa_bbbb_75d8_828e_77a0d1d9221f"
		displayName = "uppercaser"
	)
	nodes := []*NodeConfig{{ID: "step1", Type: "agent", AgentName: displayName}}
	cfg := &WorkflowConfig{Name: "TestWorkflow", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("TestWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	seedDisplayNameCard(e.instance, brokerName, displayName)

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	state.SubTaskTargets = nil

	afterDispatch := len(brk.snapshot())
	e.instance.cancelActiveSubTasks(&WorkflowExecution{State: state})

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, brokerName)
	unwanted := a2a.AgentRequestTopic(cfg.Namespace, displayName)
	assertCancelTopic(t, brk, afterDispatch, wantTopic, unwanted)
}

func assertCancelTopic(t *testing.T, brk *capturingBroker, from int, want, unwanted string) {
	t.Helper()
	cancels := brk.snapshot()[from:]
	for _, m := range cancels {
		if m.Topic == unwanted {
			t.Fatalf("cancel published to display-name topic %q; want %q", unwanted, want)
		}
	}
	for _, m := range cancels {
		if m.Topic == want {
			return
		}
	}
	topics := make([]string, 0, len(cancels))
	for _, m := range cancels {
		topics = append(topics, m.Topic)
	}
	t.Fatalf("no cancel published to resolved broker topic %q; saw topics=%v", want, topics)
}

type recordingAuthorizer struct {
	mu    sync.Mutex
	names []string
	allow bool
}

func (r *recordingAuthorizer) AuthorizeAgent(_ context.Context, _ *auth.Identity, name string) error {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
	if r.allow {
		return nil
	}
	return fmt.Errorf("denied: %s", name)
}

func (r *recordingAuthorizer) AuthorizeAgentScopes(_ context.Context, _ *auth.Identity, _ []string) error {
	return nil
}

func (r *recordingAuthorizer) FilterAgents(_ context.Context, _ *auth.Identity, agentNames []string) []string {
	if r.allow {
		return agentNames
	}
	return nil
}

func (r *recordingAuthorizer) AuthorizeWorkflow(_ context.Context, _ *auth.Identity, name string) error {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
	if r.allow {
		return nil
	}
	return fmt.Errorf("denied: %s", name)
}

func (r *recordingAuthorizer) FilterWorkflows(_ context.Context, _ *auth.Identity, workflowNames []string) []string {
	if r.allow {
		return workflowNames
	}
	return nil
}

func (r *recordingAuthorizer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.names))
	copy(out, r.names)
	return out
}

func TestDispatchAgentNode_DoesNotAuthorizePerNode(t *testing.T) {
	const (
		brokerName  = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
		displayName = "uppercaser"
	)
	nodes := []*NodeConfig{{ID: "step1", Type: "agent", AgentName: displayName}}
	cfg := &WorkflowConfig{Name: "TestWorkflow", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("TestWorkflow", "exec-1")

	recorder := &recordingAuthorizer{allow: false}
	e, brk := newCapturingExecutorWithAuthz(t, cfg, dag, state, recorder)
	seedDisplayNameCard(e.instance, brokerName, displayName)

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("expected zero per-node authorize calls (gate moved to the workflow boundary), got %d: %v", len(got), got)
	}
	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, brokerName)
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			return
		}
	}
	t.Fatalf("agent node was not dispatched despite deny-all authorizer; saw topics=%v", capturedTopics(brk))
}

func TestDispatchAgentNode_ForwardsAuthTokenToWorkflowTarget(t *testing.T) {
	nodes := []*NodeConfig{{ID: "step1", Type: "workflow", WorkflowName: "nested_wf"}}
	cfg := &WorkflowConfig{Name: "OuterWorkflow", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("OuterWorkflow", "exec-1")
	e, brk := newCapturingExecutorWithAuthz(t, cfg, dag, state, &recordingAuthorizer{allow: true})
	e.execCtx.UserProps.AuthToken = "test-jwt"
	seedCard(e.instance, a2a.AgentCard{Name: "nested_wf"})

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, "nested_wf")
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			if got := m.UserProperties["authToken"]; got != "test-jwt" {
				t.Fatalf("nested workflow dispatch must forward the caller's authToken (so the nested receiver gate can re-check workflow:<nested>:invoke); got authToken=%q", got)
			}
			return
		}
	}
	t.Fatalf("no dispatch to nested workflow topic %q; saw %v", wantTopic, capturedTopics(brk))
}

func newCapturingExecutorWithAuthz(t *testing.T, cfg *WorkflowConfig, dag *DependencyGraph, state *WorkflowExecutionState, authz auth.AgentAuthorizer) (*DAGExecutor, *capturingBroker) {
	t.Helper()
	brk := &capturingBroker{}
	ctx := context.Background()
	props := a2a.UserProperties{A2AStatusTopic: "test/status"}
	execCtx := NewExecutionContext(ctx, "test-task", "test-ctx", nil, props, false)
	execCtx.Identity = &auth.Identity{}

	inst := &WorkflowInstance{
		cfg:            cfg,
		dag:            dag,
		svc:            &capturingServices{brk: brk, authz: authz},
		logger:         slog.Default(),
		executions:     make(map[string]*WorkflowExecution),
		executionsByID: make(map[string]*WorkflowExecution),
		subTaskIndex:   make(map[string]string),
	}
	e := &DAGExecutor{
		cfg:      cfg,
		dag:      dag,
		state:    state,
		execCtx:  execCtx,
		instance: inst,
		logger:   slog.Default(),
	}
	inst.timerManager = NewInProcessTimerManager(slog.Default(), func(execID string, event Event) {
		if execID == state.ExecutionID {
			_ = e.Advance(ctx, event)
		}
	})
	go inst.timerManager.Start(execCtx.Ctx)
	t.Cleanup(func() { execCtx.Cancel() })
	return e, brk
}

func seedDisplayNameCard(w *WorkflowInstance, brokerName, displayName string) {
	seedCard(w, a2a.AgentCard{
		Name: brokerName,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    "https://solace.com/a2a/extensions/display-name",
				Params: map[string]any{"display_name": displayName},
			}},
		},
	})
}

func assertPublishedToBrokerTopic(t *testing.T, brk *capturingBroker, want, unwanted string) {
	t.Helper()
	for _, m := range brk.snapshot() {
		if m.Topic == unwanted {
			t.Fatalf("published to display-name topic %q; resolution should have rewritten it to %q",
				unwanted, want)
		}
	}
	for _, m := range brk.snapshot() {
		if m.Topic == want {
			return
		}
	}
	t.Fatalf("no message published to resolved broker topic %q; saw topics=%v",
		want, capturedTopics(brk))
}

func capturedTopics(brk *capturingBroker) []string {
	msgs := brk.snapshot()
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Topic)
	}
	return out
}
