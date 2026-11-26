// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type capturingBroker struct {
	mu             sync.Mutex
	messages       []capturedMessage
	publishHook    func(topic string) error
	guaranteedHook func(topic string) error
}

type capturedMessage struct {
	Topic          string
	Payload        []byte
	UserProperties map[string]string
}

func (b *capturingBroker) Connect(_ context.Context) error    { return nil }
func (b *capturingBroker) Disconnect(_ context.Context) error { return nil }
func (b *capturingBroker) IsConnected() bool                  { return true }
func (b *capturingBroker) CreateQueue(_ string, _ broker.QueueOptions) (broker.Queue, error) {
	return &broker.SimpleQueue{}, nil
}
func (b *capturingBroker) CreateTemporaryQueue() (broker.Queue, error) { return nil, nil }
func (b *capturingBroker) RequestReplyInbox(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}
func (b *capturingBroker) CreateDirectSubscription() (broker.Queue, error) {
	return &broker.SimpleQueue{}, nil
}
func (b *capturingBroker) DeleteQueue(_ string) error      { return nil }
func (b *capturingBroker) DeprovisionQueue(_ string) error { return nil }
func (b *capturingBroker) Subscribe(_ broker.Queue, _ string) error {
	return nil
}
func (b *capturingBroker) Unsubscribe(_ broker.Queue, _ string) error { return nil }
func (b *capturingBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if b.publishHook != nil {
		if err := b.publishHook(topic); err != nil {
			return err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	payload := append([]byte(nil), msg.Payload...)
	var up map[string]string
	if len(msg.UserProperties) > 0 {
		up = make(map[string]string, len(msg.UserProperties))
		for k, v := range msg.UserProperties {
			up[k] = v
		}
	}
	b.messages = append(b.messages, capturedMessage{Topic: topic, Payload: payload, UserProperties: up})
	return nil
}
func (b *capturingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if b.guaranteedHook != nil {
		if err := b.guaranteedHook(topic); err != nil {
			return err
		}
	}
	return b.Publish(ctx, topic, msg)
}
func (b *capturingBroker) Receive(_ context.Context, _ broker.Queue) (*broker.Message, error) {
	return nil, fmt.Errorf("not implemented")
}
func (b *capturingBroker) Request(_ context.Context, _ string, _ *broker.Message, _ time.Duration) (*broker.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *capturingBroker) snapshot() []capturedMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]capturedMessage, len(b.messages))
	copy(out, b.messages)
	return out
}

type capturingServices struct {
	brk   *capturingBroker
	authz auth.AgentAuthorizer
	art   *artifact.ScopedService
}

func (s *capturingServices) Broker() broker.Broker              { return s.brk }
func (s *capturingServices) Config() config.Config              { return nil }
func (s *capturingServices) KVStore() runtime.KVStore           { return nil }
func (s *capturingServices) Cache() runtime.Cache               { return nil }
func (s *capturingServices) Metrics() runtime.Metrics           { return runtime.NoopMetrics{} }
func (s *capturingServices) Tracer() runtime.Tracer             { return runtime.NoopTracer{} }
func (s *capturingServices) LLM(_ string) llm.Client            { return nil }
func (s *capturingServices) Artifacts() *artifact.ScopedService { return s.art }
func (s *capturingServices) Sessions() session.Store            { return nil }
func (s *capturingServices) Authenticator() auth.Authenticator  { return auth.NoopAuthenticator{} }
func (s *capturingServices) ToolAuthorizer() auth.ToolAuthorizer {
	return auth.NoopToolAuthorizer{}
}
func (s *capturingServices) AgentAuthorizer() auth.AgentAuthorizer {
	if s.authz != nil {
		return s.authz
	}
	return auth.NoopAgentAuthorizer{}
}
func (s *capturingServices) AuditLogger() auth.AuditLogger { return auth.NoopAuditLogger{} }
func (s *capturingServices) ControlAuthorizer() auth.ControlAuthorizer {
	return auth.NoopControlAuthorizer{}
}
func (s *capturingServices) CredentialService() auth.CredentialService {
	return auth.NoopCredentialService{}
}
func (s *capturingServices) Volumes() volume.Service             { return nil }
func (s *capturingServices) BudgetGate() *budget.Gate            { return nil }
func (s *capturingServices) HTTPClient() *http.Client            { return http.DefaultClient }
func (s *capturingServices) FeatureRegistry() *features.Registry { return nil }

func (s *capturingServices) UsageReporter() runtime.Component { return nil }
func (s *capturingServices) ModelResolver() runtime.ModelAliasResolver {
	return nil
}
func (s *capturingServices) Bindings() runtime.ModelBindings                { return nil }
func (s *capturingServices) ConnectorSpecStore() runtime.ConnectorSpecStore { return nil }
func (s *capturingServices) Logger() *slog.Logger                           { return slog.Default() }

func newCapturingExecutor(t *testing.T, cfg *WorkflowConfig, dag *DependencyGraph, state *WorkflowExecutionState) (*DAGExecutor, *capturingBroker) {
	t.Helper()
	brk := &capturingBroker{}
	ctx := context.Background()
	props := a2a.UserProperties{A2AStatusTopic: "test/status"}
	execCtx := NewExecutionContext(ctx, "test-task", "test-ctx", nil, props, false)

	inst := &WorkflowInstance{
		cfg:            cfg,
		dag:            dag,
		svc:            &capturingServices{brk: brk},
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

func extractDataPart(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Result struct {
			Status struct {
				Message struct {
					Parts []struct {
						Kind string         `json:"kind"`
						Data map[string]any `json:"data"`
					} `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v\npayload=%s", err, string(payload))
	}
	parts := envelope.Result.Status.Message.Parts
	if len(parts) == 0 {
		t.Fatalf("no parts in payload: %s", string(payload))
	}
	return parts[0].Data
}

func TestWorkflowExecutionStart_JSONShape(t *testing.T) {
	data := WorkflowExecutionStartData{
		Type:             string(a2a.SignalWorkflowExecutionStart),
		WorkflowName:     "MyWorkflow",
		ExecutionID:      "exec-1",
		InputArtifactRef: nil,
		WorkflowInput:    map[string]any{"value": 5.0},
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	requiredKeys := []string{"type", "workflow_name", "execution_id", "input_artifact_ref", "workflow_input"}
	for _, k := range requiredKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in payload: %s", k, string(raw))
		}
	}
	if v, ok := got["input_artifact_ref"]; !ok || v != nil {
		t.Errorf("input_artifact_ref should be JSON null: got ok=%v value=%v", ok, v)
	}
	forbidden := []string{"input", "node_count"}
	for _, k := range forbidden {
		if _, ok := got[k]; ok {
			t.Errorf("forbidden key %q present in payload: %s", k, string(raw))
		}
	}
	if got["workflow_input"].(map[string]any)["value"] != 5.0 {
		t.Errorf("workflow_input.value = %v, want 5.0", got["workflow_input"])
	}
}

func TestWorkflowExecutionStart_WithArtifactRef(t *testing.T) {
	ref := "artifact://wf/input.json?version=1"
	data := WorkflowExecutionStartData{
		Type:             string(a2a.SignalWorkflowExecutionStart),
		WorkflowName:     "W",
		ExecutionID:      "e",
		InputArtifactRef: &ref,
		WorkflowInput:    map[string]any{},
	}
	raw, _ := json.Marshal(data)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if got["input_artifact_ref"] != ref {
		t.Errorf("input_artifact_ref = %v, want %q", got["input_artifact_ref"], ref)
	}
}

func TestWorkflowNodeExecutionStart_HasAllFields(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "validate", Type: "agent", AgentName: "DataDoubler"},
	}
	cfg := &WorkflowConfig{Nodes: nodes, Name: "ParallelExecutionWorkflow"}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("ParallelExecutionWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	e.publishNodeStart(nodes[0], "", "sub-1")

	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}

	data := extractDataPart(t, msgs[0].Payload)
	expected := []string{
		"type", "node_id", "node_type", "agent_name", "input_artifact_ref",
		"iteration_index", "condition", "true_branch", "false_branch",
		"true_branch_label", "false_branch_label", "sub_task_id",
		"parent_node_id", "cases", "default_branch", "wait_for",
		"join_strategy", "join_n", "max_iterations", "loop_delay",
		"parallel_group_id", "depends_on", "tool_name",
	}
	for _, k := range expected {
		if _, ok := data[k]; !ok {
			t.Errorf("missing key %q in workflow_node_execution_start payload; keys=%v", k, keysOf(data))
		}
	}
	if deps, ok := data["depends_on"].([]any); !ok || len(deps) != 0 {
		t.Errorf("depends_on should be an empty array for a root node, got %v", data["depends_on"])
	}
	if data["agent_name"] != "DataDoubler" {
		t.Errorf("agent_name = %v, want DataDoubler", data["agent_name"])
	}
	if data["sub_task_id"] != "sub-1" {
		t.Errorf("sub_task_id = %v, want sub-1", data["sub_task_id"])
	}
	nullFields := []string{
		"input_artifact_ref", "iteration_index", "condition", "true_branch",
		"false_branch", "true_branch_label", "false_branch_label",
		"parent_node_id", "cases", "default_branch", "wait_for",
		"join_strategy", "join_n", "max_iterations", "loop_delay",
		"parallel_group_id", "tool_name",
	}
	for _, k := range nullFields {
		if v, ok := data[k]; !ok || v != nil {
			t.Errorf("field %q should be JSON null: ok=%v value=%v", k, ok, v)
		}
	}
}

func TestWorkflowNodeExecutionStart_ParallelSiblings(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "square_value", Type: "agent", AgentName: "NumberSquarer"},
		{ID: "increment_value", Type: "agent", AgentName: "NumberIncrementer"},
	}
	cfg := &WorkflowConfig{Nodes: nodes, Name: "ParallelExecutionWorkflow"}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("ParallelExecutionWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	ready := []string{"square_value", "increment_value"}
	groupID := e.assignParallelGroup(ready)
	if groupID == "" {
		t.Errorf("parallel group id should be non-empty")
	}

	for _, id := range ready {
		e.publishNodeStart(e.dag.Nodes[id], groupID, "sub-"+id)
	}

	msgs := brk.snapshot()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	seenIndexes := map[float64]bool{}
	for i, m := range msgs {
		data := extractDataPart(t, m.Payload)
		gotGroup, _ := data["parallel_group_id"].(string)
		if gotGroup != groupID {
			t.Errorf("message %d: parallel_group_id = %q, want %q", i, gotGroup, groupID)
		}
		idx, ok := data["iteration_index"].(float64)
		if !ok {
			t.Errorf("message %d: iteration_index should be a number, got %v", i, data["iteration_index"])
			continue
		}
		if seenIndexes[idx] {
			t.Errorf("message %d: duplicate iteration_index %v", i, idx)
		}
		seenIndexes[idx] = true
	}
	if len(seenIndexes) != 2 {
		t.Errorf("expected 2 distinct iteration_index values, got %v", seenIndexes)
	}
	if !seenIndexes[0] || !seenIndexes[1] {
		t.Errorf("expected iteration_index values {0,1}, got %v", seenIndexes)
	}
}

func TestWorkflowNodeExecutionStart_DependsOn(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "health", Type: "agent", AgentName: "A"},
		{ID: "feedback_q", Type: "agent", AgentName: "A"},
		{ID: "sql_kpi", Type: "tool", ToolName: "query_data_with_sql", DependsOn: []string{"health", "feedback_q"}},
	}
	cfg := &WorkflowConfig{Nodes: nodes, Name: "DependsOnWorkflow"}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("DependsOnWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	e.publishNodeStart(nodes[2], "", "sub-1")

	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(msgs))
	}
	data := extractDataPart(t, msgs[0].Payload)
	deps, ok := data["depends_on"].([]any)
	if !ok {
		t.Fatalf("depends_on should be an array, got %T (%v)", data["depends_on"], data["depends_on"])
	}
	got := make([]string, 0, len(deps))
	for _, d := range deps {
		s, _ := d.(string)
		got = append(got, s)
	}
	want := []string{"health", "feedback_q"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("depends_on = %v, want %v", got, want)
	}
	if data["tool_name"] != "query_data_with_sql" {
		t.Errorf("tool_name = %v, want query_data_with_sql", data["tool_name"])
	}
}

func TestWorkflowNodeExecutionStart_SwitchNodeFields(t *testing.T) {
	node := &NodeConfig{
		ID:        "router",
		Type:      "switch",
		Condition: "x > 0",
		Cases: []SwitchCase{
			{Condition: "x > 10", Node: "big_path"},
			{Condition: "x > 0", Node: "small_path"},
		},
		DefaultCase: "fallback_path",
	}
	branchBig := &NodeConfig{ID: "big_path", Type: "agent", AgentName: "A", DependsOn: []string{"router"}}
	branchSmall := &NodeConfig{ID: "small_path", Type: "agent", AgentName: "A", DependsOn: []string{"router"}}
	branchDefault := &NodeConfig{ID: "fallback_path", Type: "agent", AgentName: "A", DependsOn: []string{"router"}}
	cfg := &WorkflowConfig{Nodes: []*NodeConfig{node, branchBig, branchSmall, branchDefault}, Name: "SwitchWorkflow"}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("SwitchWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	e.publishNodeStart(node, "", "")

	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	data := extractDataPart(t, msgs[0].Payload)

	if data["condition"] != "x > 0" {
		t.Errorf("condition = %v, want %q", data["condition"], "x > 0")
	}
	if data["default_branch"] != "fallback_path" {
		t.Errorf("default_branch = %v, want fallback_path", data["default_branch"])
	}
	cases, ok := data["cases"].([]any)
	if !ok || len(cases) != 2 {
		t.Fatalf("cases should be a list of 2, got %v", data["cases"])
	}
	if data["sub_task_id"] != nil {
		t.Errorf("sub_task_id should be null for switch node, got %v", data["sub_task_id"])
	}
	if data["agent_name"] != nil {
		t.Errorf("agent_name should be null for switch node, got %v", data["agent_name"])
	}
}

func TestWorkflowNodeExecutionStart_LoopNodeFields(t *testing.T) {
	body := &NodeConfig{ID: "body", Type: "agent", AgentName: "Worker"}
	node := &NodeConfig{
		ID:            "retry_loop",
		Type:          "loop",
		Condition:     "output.retryable == true",
		MaxIterations: 3,
		Delay:         500 * time.Millisecond,
		BodyNode:      "body",
	}
	cfg := &WorkflowConfig{Nodes: []*NodeConfig{node, body}, Name: "LoopWorkflow"}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("LoopWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	e.publishNodeStart(node, "", "")

	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	data := extractDataPart(t, msgs[0].Payload)

	if data["condition"] != "output.retryable == true" {
		t.Errorf("condition = %v", data["condition"])
	}
	if n, ok := data["max_iterations"].(float64); !ok || n != 3 {
		t.Errorf("max_iterations = %v (type %T)", data["max_iterations"], data["max_iterations"])
	}
	if data["loop_delay"] != "500ms" {
		t.Errorf("loop_delay = %v, want 500ms", data["loop_delay"])
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
