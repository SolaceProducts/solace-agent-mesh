// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type stubBroker struct{}

func (stubBroker) Connect(_ context.Context) error    { return nil }
func (stubBroker) Disconnect(_ context.Context) error { return nil }
func (stubBroker) IsConnected() bool                  { return true }
func (stubBroker) CreateQueue(_ string, _ broker.QueueOptions) (broker.Queue, error) {
	return &broker.SimpleQueue{}, nil
}
func (stubBroker) CreateTemporaryQueue() (broker.Queue, error) { return nil, nil }
func (stubBroker) RequestReplyInbox(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}
func (stubBroker) CreateDirectSubscription() (broker.Queue, error)              { return &broker.SimpleQueue{}, nil }
func (stubBroker) DeleteQueue(_ string) error                                   { return nil }
func (stubBroker) DeprovisionQueue(_ string) error                              { return nil }
func (stubBroker) Subscribe(_ broker.Queue, _ string) error                     { return nil }
func (stubBroker) Unsubscribe(_ broker.Queue, _ string) error                   { return nil }
func (stubBroker) Publish(_ context.Context, _ string, _ *broker.Message) error { return nil }
func (stubBroker) PublishGuaranteed(_ context.Context, _ string, _ *broker.Message) error {
	return nil
}
func (stubBroker) Receive(_ context.Context, _ broker.Queue) (*broker.Message, error) {
	return nil, fmt.Errorf("not implemented")
}
func (stubBroker) Request(_ context.Context, _ string, _ *broker.Message, _ time.Duration) (*broker.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

type stubServices struct {
	budgetGate *budget.Gate
	brk        broker.Broker
	authn      auth.Authenticator
	audit      auth.AuditLogger
	agentAuth  auth.AgentAuthorizer
	specStore  runtime.ConnectorSpecStore
	features   *features.Registry
	logger     *slog.Logger
	bindings   runtime.ModelBindings
	httpClient *http.Client
	art        *artifact.ScopedService
	creds      auth.CredentialService
}

func (s *stubServices) Broker() broker.Broker              { return s.brk }
func (s *stubServices) Config() config.Config              { return nil }
func (s *stubServices) KVStore() runtime.KVStore           { return nil }
func (s *stubServices) Cache() runtime.Cache               { return nil }
func (s *stubServices) Metrics() runtime.Metrics           { return runtime.NoopMetrics{} }
func (s *stubServices) Tracer() runtime.Tracer             { return runtime.NoopTracer{} }
func (s *stubServices) LLM(_ string) llm.Client            { return nil }
func (s *stubServices) Artifacts() *artifact.ScopedService { return s.art }
func (s *stubServices) Sessions() session.Store            { return nil }
func (s *stubServices) Authenticator() auth.Authenticator {
	if s.authn != nil {
		return s.authn
	}
	return auth.NoopAuthenticator{}
}
func (s *stubServices) ToolAuthorizer() auth.ToolAuthorizer {
	return auth.NoopToolAuthorizer{}
}
func (s *stubServices) AgentAuthorizer() auth.AgentAuthorizer {
	if s.agentAuth != nil {
		return s.agentAuth
	}
	return auth.NoopAgentAuthorizer{}
}
func (s *stubServices) AuditLogger() auth.AuditLogger {
	if s.audit != nil {
		return s.audit
	}
	return auth.NoopAuditLogger{}
}
func (s *stubServices) ControlAuthorizer() auth.ControlAuthorizer {
	return auth.NoopControlAuthorizer{}
}
func (s *stubServices) CredentialService() auth.CredentialService {
	if s.creds != nil {
		return s.creds
	}
	return auth.NoopCredentialService{}
}
func (s *stubServices) Volumes() volume.Service  { return nil }
func (s *stubServices) BudgetGate() *budget.Gate { return s.budgetGate }
func (s *stubServices) HTTPClient() *http.Client {
	if s.httpClient != nil {
		return s.httpClient
	}
	return http.DefaultClient
}
func (s *stubServices) FeatureRegistry() *features.Registry { return s.features }

func (s *stubServices) UsageReporter() runtime.Component { return nil }
func (s *stubServices) ModelResolver() runtime.ModelAliasResolver {
	return nil
}
func (s *stubServices) Bindings() runtime.ModelBindings                { return s.bindings }
func (s *stubServices) ConnectorSpecStore() runtime.ConnectorSpecStore { return s.specStore }
func (s *stubServices) Logger() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

func newTestDAGExecutor(cfg *WorkflowConfig, dag *DependencyGraph, state *WorkflowExecutionState) *DAGExecutor {
	ctx := context.Background()
	execCtx := NewExecutionContext(ctx, "test-task", "test-ctx", nil, a2a.UserProperties{}, false)

	inst := &WorkflowInstance{
		cfg:            cfg,
		dag:            dag,
		svc:            &stubServices{brk: stubBroker{}},
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
	return e
}

const (
	completedSubTask = "done"
	skippedReason    = "condition not met"
)

type nodeSeed struct {
	completed, skipped, pending []string
}

func (s nodeSeed) apply(state *WorkflowExecutionState) {
	for _, id := range s.completed {
		state.CompletedNodes[id] = completedSubTask
	}
	for _, id := range s.skipped {
		state.SkippedNodes[id] = skippedReason
	}
	for _, id := range s.pending {
		state.AddPending(id)
	}
}

func newExecutorFor(t *testing.T, cfg *WorkflowConfig, state *WorkflowExecutionState) *DAGExecutor {
	t.Helper()
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("BuildDependencyGraph(%d nodes) error: %v", len(cfg.Nodes), err)
	}
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)
	return e
}

type stateView struct {
	Completed      map[string]string
	Pending        []string
	Skipped        []string
	Outputs        map[string]map[string]any
	Metadata       map[string]any
	LoopIterations map[string]int
	RetryCounts    map[string]int
	Timers         []Timer
	Finalized      bool
	Cancelled      bool
	ErrorState     *ErrorInfo
}

func diffState(want stateView, state *WorkflowExecutionState, markerIDs ...string) string {
	outputs := maps.Clone(state.NodeOutputs)
	delete(outputs, "workflow")
	got := stateView{
		Completed:      withMarkersHidden(state.CompletedNodes, markerIDs...),
		Pending:        pendingNodeIDs(state),
		Skipped:        slices.Sorted(maps.Keys(state.SkippedNodes)),
		Outputs:        outputs,
		Metadata:       state.Metadata,
		LoopIterations: state.LoopIterations,
		RetryCounts:    state.RetryCounts,
		Timers:         state.Timers,
		Finalized:      state.Finalized,
		Cancelled:      state.Cancelled,
		ErrorState:     state.ErrorState,
	}
	ignoreDuration := cmpopts.IgnoreMapEntries(func(key string, _ any) bool { return key == "__phaseb_final_duration_ns" })
	return cmp.Diff(want, got, cmpopts.EquateEmpty(), cmpopts.IgnoreFields(ErrorInfo{}, "Timestamp"), cmpopts.IgnoreFields(Timer{}, "FiresAt"), ignoreDuration)
}

func pendingNodeIDs(state *WorkflowExecutionState) []string {
	var ids []string
	for id := range state.PendingNodes {
		if state.IsNodePending(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

const completionMarker = "<non-empty completion marker>"

func withMarkersHidden(completed map[string]string, markerIDs ...string) map[string]string {
	view := maps.Clone(completed)
	for _, id := range markerIDs {
		if view[id] != "" {
			view[id] = completionMarker
		}
	}
	return view
}

func TestGetReadyNodes(t *testing.T) {
	chainAB := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}},
	}
	single := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}
	joinABC := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB"},
		{ID: "c", Type: "agent", AgentName: "agentC", DependsOn: []string{"a", "b"}},
	}
	tests := []struct {
		name  string
		nodes []*NodeConfig
		seed  nodeSeed
		want  []string
	}{
		{
			name: "two root nodes are both ready",
			nodes: []*NodeConfig{
				{ID: "a", Type: "agent", AgentName: "agentA"},
				{ID: "b", Type: "agent", AgentName: "agentB"},
			},
			want: []string{"a", "b"},
		},
		{name: "dependent waits while its dependency is unfinished", nodes: chainAB, want: []string{"a"}},
		{name: "dependent is ready once its dependency completed", nodes: chainAB, seed: nodeSeed{completed: []string{"a"}}, want: []string{"b"}},
		{name: "dependent is ready once its dependency was skipped", nodes: chainAB, seed: nodeSeed{skipped: []string{"a"}}, want: []string{"b"}},
		{name: "pending node is excluded", nodes: single, seed: nodeSeed{pending: []string{"a"}}, want: nil},
		{name: "completed node is excluded", nodes: single, seed: nodeSeed{completed: []string{"a"}}, want: nil},
		{
			name: "loop body node is excluded",
			nodes: []*NodeConfig{
				{ID: "loop1", Type: "loop", Condition: "true", BodyNode: "body1", MaxIterations: 10},
				{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"loop1"}},
			},
			want: []string{"loop1"},
		},
		{name: "node with two dependencies waits for the second", nodes: joinABC, seed: nodeSeed{completed: []string{"a"}}, want: []string{"b"}},
		{name: "node with two dependencies is ready when both completed", nodes: joinABC, seed: nodeSeed{completed: []string{"a", "b"}}, want: []string{"c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			tc.seed.apply(state)
			e := newExecutorFor(t, &WorkflowConfig{Nodes: tc.nodes}, state)

			got := e.getReadyNodes()

			if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("getReadyNodes() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAllNodesComplete(t *testing.T) {
	pair := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB"},
	}
	tests := []struct {
		name  string
		nodes []*NodeConfig
		seed  nodeSeed
		want  bool
	}{
		{name: "all nodes completed", nodes: pair, seed: nodeSeed{completed: []string{"a", "b"}}, want: true},
		{name: "completed and skipped nodes together count as done", nodes: pair, seed: nodeSeed{completed: []string{"a"}, skipped: []string{"b"}}, want: true},
		{name: "a node neither completed nor skipped is not done", nodes: pair, seed: nodeSeed{completed: []string{"a"}}, want: false},
		{
			name: "loop body node does not block completion",
			nodes: []*NodeConfig{
				{ID: "loop1", Type: "loop", Condition: "true", BodyNode: "body1", MaxIterations: 10},
				{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"loop1"}},
			},
			seed: nodeSeed{completed: []string{"loop1"}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			tc.seed.apply(state)
			e := newExecutorFor(t, &WorkflowConfig{Nodes: tc.nodes}, state)

			if got := e.allNodesComplete(); got != tc.want {
				t.Errorf("allNodesComplete() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllNodesComplete_Empty(t *testing.T) {
	cfg := &WorkflowConfig{Nodes: []*NodeConfig{}}
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if got := e.allNodesComplete(); !got {
		t.Errorf("allNodesComplete() = %v, want true for an empty workflow", got)
	}
}

func TestEffectiveRetryStrategy(t *testing.T) {
	nodeRetry := &RetryStrategy{Limit: 2, RetryPolicy: "OnFailure"}
	workflowRetry := &RetryStrategy{Limit: 5, RetryPolicy: "Always"}
	fallbackRetry := &RetryStrategy{Limit: 5}
	unknownNodeRetry := &RetryStrategy{Limit: 3}
	tests := []struct {
		name  string
		wf    *RetryStrategy
		nodes map[string]*NodeConfig
		id    string
		want  *RetryStrategy
	}{
		{
			name:  "node level retry takes precedence over the workflow one",
			wf:    workflowRetry,
			nodes: map[string]*NodeConfig{"n1": {ID: "n1", Retry: nodeRetry}},
			id:    "n1",
			want:  nodeRetry,
		},
		{
			name:  "node without retry falls back to the workflow one",
			wf:    fallbackRetry,
			nodes: map[string]*NodeConfig{"n1": {ID: "n1"}},
			id:    "n1",
			want:  fallbackRetry,
		},
		{
			name:  "no retry configured anywhere gives none",
			nodes: map[string]*NodeConfig{"n1": {ID: "n1"}},
			id:    "n1",
			want:  nil,
		},
		{
			name:  "unknown node falls back to the workflow one",
			wf:    unknownNodeRetry,
			nodes: map[string]*NodeConfig{},
			id:    "nonexistent",
			want:  unknownNodeRetry,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &DAGExecutor{
				cfg: &WorkflowConfig{RetryStrategy: tc.wf},
				dag: &DependencyGraph{Nodes: tc.nodes},
			}

			if got := e.effectiveRetryStrategy(tc.id); got != tc.want {
				t.Errorf("effectiveRetryStrategy(%q) = %+v, want the %+v strategy", tc.id, got, tc.want)
			}
		})
	}
}

func TestResolveNodeInput(t *testing.T) {
	agentA := &NodeConfig{ID: "a", Type: "agent", AgentName: "agentA"}
	chainAB := []*NodeConfig{
		agentA,
		{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}},
	}
	tests := []struct {
		name    string
		nodes   []*NodeConfig
		node    string
		outputs map[string]map[string]any
		want    map[string]any
	}{
		{
			name: "explicit input is returned",
			nodes: []*NodeConfig{
				{ID: "a", Type: "agent", AgentName: "agentA", Input: map[string]any{"greeting": "hello"}},
			},
			node: "a",
			want: map[string]any{"greeting": "hello"},
		},
		{
			name:    "single dependency output is the input",
			nodes:   chainAB,
			node:    "b",
			outputs: map[string]map[string]any{"a": {"output": map[string]any{"result": "from-a"}}},
			want:    map[string]any{"result": "from-a"},
		},
		{
			name:    "workflow input is the fallback",
			nodes:   []*NodeConfig{agentA},
			node:    "a",
			outputs: map[string]map[string]any{"workflow_input": {"output": map[string]any{"topic": "testing"}}},
			want:    map[string]any{"topic": "testing"},
		},
		{
			name:  "no input available gives an empty map",
			nodes: []*NodeConfig{agentA},
			node:  "a",
			want:  map[string]any{},
		},
		{
			name: "explicit input templates resolve against node outputs",
			nodes: []*NodeConfig{
				agentA,
				{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"},
					Input: map[string]any{"data": "{{a.output.result}}"}},
			},
			node:    "b",
			outputs: map[string]map[string]any{"a": {"output": map[string]any{"result": "hello-world"}}},
			want:    map[string]any{"data": "hello-world"},
		},
		{
			name:  "dependency output that is not a map falls through to the workflow input",
			nodes: chainAB,
			node:  "b",
			outputs: map[string]map[string]any{
				"a":              {"output": "just a string"},
				"workflow_input": {"output": map[string]any{"fallback": true}},
			},
			want: map[string]any{"fallback": true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			for id, out := range tc.outputs {
				state.NodeOutputs[id] = out
			}
			e := newExecutorFor(t, &WorkflowConfig{Nodes: tc.nodes}, state)

			got, err := e.resolveNodeInput(e.dag.Nodes[tc.node])

			if err != nil {
				t.Fatalf("resolveNodeInput(%q) error: %v", tc.node, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("resolveNodeInput(%q) mismatch (-want +got):\n%s", tc.node, diff)
			}
		})
	}
}

func TestResolveNodeInputWithOutputs(t *testing.T) {
	tests := []struct {
		name    string
		node    *NodeConfig
		outputs map[string]map[string]any
		want    map[string]any
	}{
		{
			name: "explicit input resolves against the given outputs",
			node: &NodeConfig{
				ID: "n1", Type: "agent", AgentName: "a",
				Input: map[string]any{"key": "{{_map_item}}"},
			},
			outputs: map[string]map[string]any{"_map_item": {"output": "item-value"}},
			want:    map[string]any{"key": "item-value"},
		},
		{
			name: "no explicit input gives an empty map",
			node: &NodeConfig{ID: "n1", Type: "agent", AgentName: "a"},
			want: map[string]any{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newExecutorFor(t, &WorkflowConfig{Nodes: []*NodeConfig{tc.node}}, NewExecutionState("wf", "e1"))

			got, err := e.resolveNodeInputWithOutputs(tc.node, tc.outputs)

			if err != nil {
				t.Fatalf("resolveNodeInputWithOutputs(%q) error: %v", tc.node.ID, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("resolveNodeInputWithOutputs(%q) mismatch (-want +got):\n%s", tc.node.ID, diff)
			}
		})
	}
}

func TestExecuteSwitchNode(t *testing.T) {
	tests := []struct {
		name        string
		nodes       []*NodeConfig
		outputs     map[string]map[string]any
		wantOutput  map[string]any
		wantSkipped []string
	}{
		{
			name: "first matching case is selected and the other branch skipped",
			nodes: []*NodeConfig{
				{ID: "sw", Type: "switch", Cases: []SwitchCase{
					{Condition: "true", Node: "branch_a"},
					{Condition: "false", Node: "branch_b"},
				}},
				{ID: "branch_a", Type: "agent", AgentName: "agentA", DependsOn: []string{"sw"}},
				{ID: "branch_b", Type: "agent", AgentName: "agentB", DependsOn: []string{"sw"}},
			},
			wantOutput:  map[string]any{"selected_branch": "branch_a", "selected_case_index": 0, "cases_unresolved": 0},
			wantSkipped: []string{"branch_b"},
		},
		{
			name: "second case is selected when the first does not match",
			nodes: []*NodeConfig{
				{ID: "sw", Type: "switch", Cases: []SwitchCase{
					{Condition: "false", Node: "branch_a"},
					{Condition: "true", Node: "branch_b"},
				}},
				{ID: "branch_a", Type: "agent", AgentName: "agentA", DependsOn: []string{"sw"}},
				{ID: "branch_b", Type: "agent", AgentName: "agentB", DependsOn: []string{"sw"}},
			},
			wantOutput:  map[string]any{"selected_branch": "branch_b", "selected_case_index": 1, "cases_unresolved": 0},
			wantSkipped: []string{"branch_a"},
		},
		{
			name: "default case is selected when no case matches",
			nodes: []*NodeConfig{
				{ID: "sw", Type: "switch", Cases: []SwitchCase{{Condition: "false", Node: "branch_a"}}, DefaultCase: "branch_default"},
				{ID: "branch_a", Type: "agent", AgentName: "agentA", DependsOn: []string{"sw"}},
				{ID: "branch_default", Type: "agent", AgentName: "agentD", DependsOn: []string{"sw"}},
			},
			wantOutput:  map[string]any{"selected_branch": "branch_default", "selected_case_index": 1, "cases_unresolved": 0},
			wantSkipped: []string{"branch_a"},
		},
		{
			name: "no match and no default selects nothing and skips the branch",
			nodes: []*NodeConfig{
				{ID: "sw", Type: "switch", Cases: []SwitchCase{{Condition: "false", Node: "branch_a"}}},
				{ID: "branch_a", Type: "agent", AgentName: "agentA", DependsOn: []string{"sw"}},
			},
			wantOutput:  map[string]any{"selected_branch": "", "selected_case_index": -1, "cases_unresolved": 0},
			wantSkipped: []string{"branch_a"},
		},
		{
			name: "condition reads a node output",
			nodes: []*NodeConfig{
				{ID: "sw", Type: "switch", Cases: []SwitchCase{{Condition: `"{{prev.output.status}}" == "ready"`, Node: "branch_a"}}},
				{ID: "branch_a", Type: "agent", AgentName: "agentA", DependsOn: []string{"sw"}},
			},
			outputs:     map[string]map[string]any{"prev": {"output": map[string]any{"status": "ready"}}},
			wantOutput:  map[string]any{"selected_branch": "branch_a", "selected_case_index": 0, "cases_unresolved": 0},
			wantSkipped: nil,
		},
		{
			name: "dependents of a skipped branch are skipped too",
			nodes: []*NodeConfig{
				{ID: "sw", Type: "switch", Cases: []SwitchCase{
					{Condition: "false", Node: "branch_a"},
					{Condition: "true", Node: "branch_b"},
				}},
				{ID: "branch_a", Type: "agent", AgentName: "agentA", DependsOn: []string{"sw"}},
				{ID: "downstream_a", Type: "agent", AgentName: "agentD", DependsOn: []string{"branch_a"}},
				{ID: "branch_b", Type: "agent", AgentName: "agentB", DependsOn: []string{"sw"}},
			},
			wantOutput:  map[string]any{"selected_branch": "branch_b", "selected_case_index": 1, "cases_unresolved": 0},
			wantSkipped: []string{"branch_a", "downstream_a"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			for id, out := range tc.outputs {
				state.NodeOutputs[id] = out
			}
			e := newExecutorFor(t, &WorkflowConfig{Nodes: tc.nodes}, state)

			e.executeSwitchNode(tc.nodes[0])

			if !state.IsNodeCompleted("sw") {
				t.Error("executeSwitchNode(sw): switch node is not completed")
			}
			wantOutputs := map[string]any{"output": tc.wantOutput}
			if diff := cmp.Diff(wantOutputs, state.NodeOutputs["sw"]); diff != "" {
				t.Errorf("executeSwitchNode(sw) NodeOutputs[sw] mismatch (-want +got):\n%s", diff)
			}
			gotSkipped := slices.Sorted(maps.Keys(state.SkippedNodes))
			if diff := cmp.Diff(tc.wantSkipped, gotSkipped, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("executeSwitchNode(sw) skipped nodes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSkipNode(t *testing.T) {
	type skipStep struct{ id, reason string }
	tests := []struct {
		name        string
		nodes       []*NodeConfig
		seed        nodeSeed
		steps       []skipStep
		wantSkipped []string
	}{
		{
			name: "dependents whose only dependency was skipped are skipped recursively",
			nodes: []*NodeConfig{
				{ID: "a", Type: "agent", AgentName: "agentA"},
				{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}},
				{ID: "c", Type: "agent", AgentName: "agentC", DependsOn: []string{"b"}},
			},
			steps:       []skipStep{{"a", "test skip"}},
			wantSkipped: []string{"a", "b", "c"},
		},
		{
			name: "dependent with another live dependency is not skipped",
			nodes: []*NodeConfig{
				{ID: "a", Type: "agent", AgentName: "agentA"},
				{ID: "x", Type: "agent", AgentName: "agentX"},
				{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a", "x"}},
			},
			steps:       []skipStep{{"a", "test skip"}},
			wantSkipped: []string{"a"},
		},
		{
			name: "dependent is skipped once all its dependencies are skipped",
			nodes: []*NodeConfig{
				{ID: "a", Type: "agent", AgentName: "agentA"},
				{ID: "x", Type: "agent", AgentName: "agentX"},
				{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a", "x"}},
			},
			steps:       []skipStep{{"a", "skip a"}, {"x", "skip x"}},
			wantSkipped: []string{"a", "b", "x"},
		},
		{
			name: "completed dependent is not skipped",
			nodes: []*NodeConfig{
				{ID: "a", Type: "agent", AgentName: "agentA"},
				{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}},
			},
			seed:        nodeSeed{completed: []string{"b"}},
			steps:       []skipStep{{"a", "test"}},
			wantSkipped: []string{"a"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			tc.seed.apply(state)
			e := newExecutorFor(t, &WorkflowConfig{Nodes: tc.nodes}, state)

			for _, s := range tc.steps {
				e.skipNode(s.id, s.reason, SkipCauseNotSelected)
			}

			got := slices.Sorted(maps.Keys(state.SkippedNodes))
			if diff := cmp.Diff(tc.wantSkipped, got); diff != "" {
				t.Errorf("skipped nodes after skipNode steps %+v mismatch (-want +got):\n%s", tc.steps, diff)
			}
		})
	}
}

func TestSkipBranch(t *testing.T) {
	tests := []struct {
		name        string
		seed        nodeSeed
		wantSkipped []string
	}{
		{name: "branch is skipped", wantSkipped: []string{"branch_a"}},
		{name: "completed branch is left alone", seed: nodeSeed{completed: []string{"branch_a"}}, wantSkipped: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []*NodeConfig{
				{ID: "sw", Type: "switch", Cases: []SwitchCase{{Condition: "true", Node: "branch_a"}}},
				{ID: "branch_a", Type: "agent", AgentName: "agentA", DependsOn: []string{"sw"}},
			}
			state := NewExecutionState("wf", "e1")
			tc.seed.apply(state)
			e := newExecutorFor(t, &WorkflowConfig{Nodes: nodes}, state)

			e.skipBranch("branch_a", nodes[0], SkipCauseNotSelected, false)

			got := slices.Sorted(maps.Keys(state.SkippedNodes))
			if diff := cmp.Diff(tc.wantSkipped, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("skipped nodes after skipBranch(branch_a) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildAgentCard(t *testing.T) {
	const (
		agentTypeURI   = "https://solace.com/a2a/extensions/agent-type"
		displayNameURI = "https://solace.com/a2a/extensions/display-name"
		schemasURI     = "https://solace.com/a2a/extensions/sam/schemas"
		vizURI         = "https://solace.com/a2a/extensions/sam/workflow-visualization"
		healthURI      = "https://solace.com/a2a/extensions/sam/health"
	)
	inputSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
	}
	outputSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"result": map[string]any{"type": "string"}},
	}
	const queueProblemID = "queue:ns/a2a/v1/agent/request/workflow_abc123"
	nodeA := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}
	workflowType := map[string]any{"type": "workflow"}
	tests := []struct {
		name         string
		cfg          *WorkflowConfig
		problemIDs   []string
		wantName     string
		wantDesc     string
		wantVersion  string
		wantURIs     []string
		wantParams   map[string]map[string]any
		wantProblems []health.Problem
	}{
		{
			name: "card carries name description version and the workflow agent type",
			cfg: &WorkflowConfig{
				Name:          "test-workflow",
				Namespace:     "ns1",
				Description:   "A test workflow",
				Version:       "2.0.0",
				Nodes:         []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
				OutputMapping: map[string]any{"result": "{{step1.output}}"},
			},
			wantName:    "test-workflow",
			wantDesc:    "A test workflow",
			wantVersion: "2.0.0",
			wantURIs:    []string{agentTypeURI, vizURI},
			wantParams:  map[string]map[string]any{agentTypeURI: workflowType},
		},
		{
			name: "empty version gets the default version",
			cfg: &WorkflowConfig{
				Name:          "wf",
				Namespace:     "ns",
				Nodes:         nodeA,
				OutputMapping: map[string]any{"out": "data"},
			},
			wantName:    "wf",
			wantVersion: "1.0.0",
			wantURIs:    []string{agentTypeURI, vizURI},
			wantParams:  map[string]map[string]any{agentTypeURI: workflowType},
		},
		{
			name: "input and output schemas are advertised",
			cfg: &WorkflowConfig{
				Name:          "wf",
				Namespace:     "ns",
				Version:       "1.0.0",
				InputSchema:   inputSchema,
				OutputSchema:  outputSchema,
				Nodes:         nodeA,
				OutputMapping: map[string]any{"result": "data"},
			},
			wantName:    "wf",
			wantVersion: "1.0.0",
			wantURIs:    []string{agentTypeURI, schemasURI, vizURI},
			wantParams: map[string]map[string]any{
				agentTypeURI: workflowType,
				schemasURI:   {"input_schema": inputSchema, "output_schema": outputSchema},
			},
		},
		{
			name: "display name is advertised",
			cfg: &WorkflowConfig{
				Name:          "workflow_abc123",
				DisplayName:   "Order Fulfilment",
				Namespace:     "ns",
				Nodes:         nodeA,
				OutputMapping: map[string]any{"out": "data"},
			},
			wantName:    "workflow_abc123",
			wantVersion: "1.0.0",
			wantURIs:    []string{agentTypeURI, displayNameURI, vizURI},
			wantParams: map[string]map[string]any{
				agentTypeURI:   workflowType,
				displayNameURI: {"display_name": "Order Fulfilment"},
			},
		},
		{
			name:        "no health extension without problems",
			cfg:         &WorkflowConfig{Name: "workflow_abc123", Namespace: "ns", Nodes: nodeA},
			wantName:    "workflow_abc123",
			wantVersion: "1.0.0",
			wantURIs:    []string{agentTypeURI, vizURI},
			wantParams:  map[string]map[string]any{agentTypeURI: workflowType},
		},
		{
			name:         "active health problem is advertised",
			cfg:          &WorkflowConfig{Name: "workflow_abc123", Namespace: "ns", Nodes: nodeA},
			problemIDs:   []string{queueProblemID},
			wantName:     "workflow_abc123",
			wantVersion:  "1.0.0",
			wantURIs:     []string{agentTypeURI, vizURI, healthURI},
			wantParams:   map[string]map[string]any{agentTypeURI: workflowType},
			wantProblems: []health.Problem{{ID: queueProblemID, Severity: health.Degraded, Detail: "provision cooldown"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorkflowInstance(tc.cfg)
			for _, id := range tc.problemIDs {
				w.health.Set(id, health.Degraded, "provision cooldown")
			}

			card := w.BuildAgentCard()

			if card.Name != tc.wantName {
				t.Errorf("BuildAgentCard().Name = %q, want %q", card.Name, tc.wantName)
			}
			if card.Description != tc.wantDesc {
				t.Errorf("BuildAgentCard().Description = %q, want %q", card.Description, tc.wantDesc)
			}
			if card.Version != tc.wantVersion {
				t.Errorf("BuildAgentCard().Version = %q, want %q", card.Version, tc.wantVersion)
			}
			var gotURIs []string
			gotParams := map[string]map[string]any{}
			for _, ext := range card.Capabilities.Extensions {
				gotURIs = append(gotURIs, ext.URI)
				if ext.URI != vizURI && ext.URI != healthURI {
					gotParams[ext.URI] = ext.Params
				}
			}
			if diff := cmp.Diff(tc.wantURIs, gotURIs); diff != "" {
				t.Errorf("BuildAgentCard() extension URIs mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantParams, gotParams); diff != "" {
				t.Errorf("BuildAgentCard() extension params mismatch (-want +got):\n%s", diff)
			}
			gotProblems := a2a.HealthProblemsFromCard(card)
			if diff := cmp.Diff(tc.wantProblems, gotProblems, cmpopts.IgnoreFields(health.Problem{}, "Since")); diff != "" {
				t.Errorf("HealthProblemsFromCard(BuildAgentCard()) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNewWorkflowInstance(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:          "my-wf",
		Namespace:     "ns",
		Nodes:         []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}},
		OutputMapping: map[string]any{"out": "data"},
	}

	w := NewWorkflowInstance(cfg)

	if w.Name() != "my-wf" {
		t.Errorf("Name() = %q, want %q", w.Name(), "my-wf")
	}
	if w.Kind() != "workflow" {
		t.Errorf("Kind() = %q, want %q", w.Kind(), "workflow")
	}
	if err := w.Health(); err == nil {
		t.Error("Health() should return error for unstarted workflow")
	}
}

func TestResolveMapItems(t *testing.T) {
	tests := []struct {
		name      string
		items     any
		outputs   map[string]map[string]any
		want      []any
		wantError string
	}{
		{
			name:  "literal list is returned",
			items: []any{"item1", "item2", "item3"},
			want:  []any{"item1", "item2", "item3"},
		},
		{
			name:    "template expression resolves to the referenced list",
			items:   "{{prev.output.list}}",
			outputs: map[string]map[string]any{"prev": {"output": map[string]any{"list": []any{"a", "b"}}}},
			want:    []any{"a", "b"},
		},
		{
			name:      "expression that is not a list is an error",
			items:     42,
			wantError: "items expression resolved to int, expected list",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := &NodeConfig{ID: "map1", Type: "map", TemplateNode: "body1", ItemsExpression: tc.items}
			nodes := []*NodeConfig{
				node,
				{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"map1"}},
			}
			state := NewExecutionState("wf", "e1")
			for id, out := range tc.outputs {
				state.NodeOutputs[id] = out
			}
			e := newExecutorFor(t, &WorkflowConfig{Nodes: nodes}, state)

			got, err := e.resolveMapItems(node)

			gotError := ""
			if err != nil {
				gotError = err.Error()
			}
			if diff := cmp.Diff(tc.wantError, gotError); diff != "" {
				t.Errorf("resolveMapItems(%v) error mismatch (-want +got):\n%s", tc.items, diff)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("resolveMapItems(%v) items mismatch (-want +got):\n%s", tc.items, diff)
			}
		})
	}
}

func TestCalculateBackoff_ZeroFactor(t *testing.T) {
	bs := &BackoffStrategy{
		Duration: 1 * time.Second,
		Factor:   0,
	}
	if got := calculateBackoff(1, bs); got != 0 {
		t.Errorf("calculateBackoff(1, %+v) = %v, want 0", bs, got)
	}
}

func TestExitHandlerNodeIDs(t *testing.T) {
	tests := []struct {
		name string
		eh   *ExitHandler
		want []string
	}{
		{"nil handler", nil, nil},
		{"always only", &ExitHandler{Always: "cleanup"}, []string{"cleanup"}},
		{"all fields", &ExitHandler{Always: "a", OnSuccess: "b", OnFailure: "c", OnCancel: "d"}, []string{"a", "b", "c", "d"}},
		{"duplicates deduplicated", &ExitHandler{Always: "x", OnSuccess: "x", OnFailure: "y"}, []string{"x", "y"}},
		{"empty strings ignored", &ExitHandler{Always: "", OnSuccess: "", OnFailure: "z"}, []string{"z"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitHandlerNodeIDs(tt.eh)
			slices.Sort(got)

			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("exitHandlerNodeIDs(%+v) mismatch (-want +got):\n%s", tt.eh, diff)
			}
		})
	}
}

func TestHandleNodeError(t *testing.T) {
	retryAlways := &RetryStrategy{
		Limit:       2,
		RetryPolicy: "Always",
		Backoff:     &BackoffStrategy{Duration: 1 * time.Millisecond, Factor: 1},
	}
	chainABC := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}},
		{ID: "c", Type: "agent", AgentName: "agentC", DependsOn: []string{"b"}},
	}
	tests := []struct {
		name           string
		cfg            *WorkflowConfig
		execID         string
		seed           nodeSeed
		inExitHandler  bool
		exitQueue      []string
		node           string
		err            error
		wantRetrying   bool
		wantSkipped    []string
		wantErrorState *ErrorInfo
		wantFinalized  bool
		wantRetries    map[string]int
		wantInExit     bool
		wantExitQueue  []string
	}{
		{
			name: "without fail fast the node and its dependent are skipped",
			cfg: &WorkflowConfig{
				Nodes: []*NodeConfig{
					{ID: "a", Type: "agent", AgentName: "agentA"},
					{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}},
				},
			},
			execID:         "e1",
			seed:           nodeSeed{pending: []string{"a"}},
			node:           "a",
			err:            errors.New("test error"),
			wantSkipped:    []string{"a", "b"},
			wantErrorState: &ErrorInfo{FailedNodeID: "a", FailureReason: "error", ErrorMessage: "test error"},
		},
		{
			name: "fail fast finalizes the execution",
			cfg: &WorkflowConfig{
				Nodes:         []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}},
				FailFast:      true,
				OutputMapping: map[string]any{"result": "data"},
			},
			execID:         "e1",
			seed:           nodeSeed{pending: []string{"a"}},
			node:           "a",
			err:            errors.New("fatal error"),
			wantErrorState: &ErrorInfo{FailedNodeID: "a", FailureReason: "error", ErrorMessage: "fatal error"},
			wantFinalized:  true,
		},
		{
			name: "retry strategy with retries left schedules a retry instead of failing",
			cfg: &WorkflowConfig{
				Nodes:         []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}},
				FailFast:      true,
				RetryStrategy: retryAlways,
				OutputMapping: map[string]any{"result": "data"},
			},
			execID:       "e1",
			seed:         nodeSeed{pending: []string{"a"}},
			node:         "a",
			err:          errors.New("retriable error"),
			wantRetrying: true,
			wantRetries:  map[string]int{"a": 1},
		},
		{
			name: "failed exit handler with none left still finalizes without failing the workflow",
			cfg: &WorkflowConfig{
				Nodes:         []*NodeConfig{{ID: "cleanup", Type: "agent", AgentName: "agentA"}},
				FailFast:      true,
				OutputMapping: map[string]any{"result": "data"},
			},
			execID:        "e1",
			seed:          nodeSeed{pending: []string{"cleanup"}},
			inExitHandler: true,
			node:          "cleanup",
			err:           errors.New("exit handler error"),
			wantFinalized: true,
		},
		{
			name: "failed exit handler advances to the next queued handler",
			cfg: &WorkflowConfig{
				Nodes: []*NodeConfig{
					{ID: "cleanup", Type: "agent", AgentName: "agentA"},
					{ID: "notify", Type: "agent", AgentName: "agentB"},
				},
				OutputMapping: map[string]any{"result": "data"},
			},
			execID:        "e-advance",
			seed:          nodeSeed{pending: []string{"cleanup"}},
			inExitHandler: true,
			exitQueue:     []string{"notify"},
			node:          "cleanup",
			err:           errors.New("exit handler error"),
			wantInExit:    true,
		},
		{
			name:           "without fail fast the skip cascades through the whole chain",
			cfg:            &WorkflowConfig{Nodes: chainABC, FailFast: false},
			execID:         "e1",
			seed:           nodeSeed{pending: []string{"a"}},
			node:           "a",
			err:            errors.New("boom"),
			wantSkipped:    []string{"a", "b", "c"},
			wantErrorState: &ErrorInfo{FailedNodeID: "a", FailureReason: "error", ErrorMessage: "boom"},
		},
		{
			name: "without fail fast a direct dependent is skipped even with another live dependency",
			cfg: &WorkflowConfig{
				Nodes: []*NodeConfig{
					{ID: "a", Type: "agent", AgentName: "agentA"},
					{ID: "x", Type: "agent", AgentName: "agentX"},
					{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a", "x"}},
				},
				FailFast: false,
			},
			execID:         "e1",
			seed:           nodeSeed{pending: []string{"a"}},
			node:           "a",
			err:            errors.New("boom"),
			wantSkipped:    []string{"a", "b"},
			wantErrorState: &ErrorInfo{FailedNodeID: "a", FailureReason: "error", ErrorMessage: "boom"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", tc.execID)
			tc.seed.apply(state)
			state.InExitHandler = tc.inExitHandler
			state.ExitHandlerQueue = tc.exitQueue
			e := newExecutorFor(t, tc.cfg, state)

			gotRetrying := e.handleNodeError(tc.node, tc.err)

			if gotRetrying != tc.wantRetrying {
				t.Errorf("handleNodeError(%q, %v) = %v, want %v", tc.node, tc.err, gotRetrying, tc.wantRetrying)
			}
			if diff := cmp.Diff(tc.wantSkipped, slices.Sorted(maps.Keys(state.SkippedNodes)), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("handleNodeError(%q, %v) skipped nodes mismatch (-want +got):\n%s", tc.node, tc.err, diff)
			}
			if diff := cmp.Diff(tc.wantErrorState, state.ErrorState, cmpopts.IgnoreFields(ErrorInfo{}, "Timestamp")); diff != "" {
				t.Errorf("handleNodeError(%q, %v) ErrorState mismatch (-want +got):\n%s", tc.node, tc.err, diff)
			}
			if state.Finalized != tc.wantFinalized {
				t.Errorf("handleNodeError(%q, %v): Finalized = %v, want %v", tc.node, tc.err, state.Finalized, tc.wantFinalized)
			}
			if diff := cmp.Diff(tc.wantRetries, state.RetryCounts, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("handleNodeError(%q, %v) RetryCounts mismatch (-want +got):\n%s", tc.node, tc.err, diff)
			}
			if state.InExitHandler != tc.wantInExit {
				t.Errorf("handleNodeError(%q, %v): InExitHandler = %v, want %v", tc.node, tc.err, state.InExitHandler, tc.wantInExit)
			}
			if diff := cmp.Diff(tc.wantExitQueue, state.ExitHandlerQueue, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("handleNodeError(%q, %v) ExitHandlerQueue mismatch (-want +got):\n%s", tc.node, tc.err, diff)
			}
		})
	}
}

func TestHandleCompletion(t *testing.T) {
	agentA := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}
	tests := []struct {
		name           string
		cfg            *WorkflowConfig
		completed      map[string]string
		pending        []string
		metadata       map[string]any
		loopIterations map[string]int
		markerNodes    []string
		completion     NodeCompletion
		want           stateView
	}{
		{
			name:       "successful completion records sub-task and output",
			cfg:        &WorkflowConfig{Nodes: agentA},
			pending:    []string{"a"},
			completion: NodeCompletion{NodeID: "a", SubTaskID: "sub-1", Output: map[string]any{"result": "done"}},
			want: stateView{
				Completed: map[string]string{"a": "sub-1"},
				Outputs:   map[string]map[string]any{"a": {"output": map[string]any{"result": "done"}}},
			},
		},
		{
			name:       "error with fail fast finalizes the execution",
			cfg:        &WorkflowConfig{Nodes: agentA, FailFast: true, OutputMapping: map[string]any{"r": "d"}},
			pending:    []string{"a"},
			completion: NodeCompletion{NodeID: "a", Error: errors.New("agent crashed")},
			want: stateView{
				Finalized:  true,
				ErrorState: &ErrorInfo{FailedNodeID: "a", FailureReason: "error", ErrorMessage: "agent crashed"},
			},
		},
		{
			name:       "nil output completes the node without storing an output",
			cfg:        &WorkflowConfig{Nodes: agentA},
			pending:    []string{"a"},
			completion: NodeCompletion{NodeID: "a", SubTaskID: "sub-1", Output: nil},
			want:       stateView{Completed: map[string]string{"a": "sub-1"}},
		},
		{
			name:       "stale retry for a finished node changes nothing",
			cfg:        &WorkflowConfig{Nodes: agentA},
			completed:  map[string]string{"a": "sub-old"},
			completion: NodeCompletion{NodeID: "a", Retry: true},
			want:       stateView{Completed: map[string]string{"a": "sub-old"}},
		},
		{
			name: "map child completion is routed to the map node",
			cfg: &WorkflowConfig{Nodes: []*NodeConfig{
				{ID: "map1", Type: "map", TemplateNode: "body1", ItemsExpression: []any{"x"}},
				{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"map1"}},
			}},
			pending: []string{"map1"},
			metadata: map[string]any{
				"map1_items":     []any{"x"},
				"map1_results":   make([]any, 1),
				"map1_completed": 0,
				"map1_total":     1,
			},
			markerNodes: []string{"map1"},
			completion:  NodeCompletion{NodeID: "body1", MapNodeID: "map1", ItemIndex: 0, Output: map[string]any{"result": "mapped"}},
			want: stateView{
				Completed: map[string]string{"map1": completionMarker},
				Outputs:   map[string]map[string]any{"map1": {"output": map[string]any{"results": []any{map[string]any{"result": "mapped"}}}}},
				Metadata: map[string]any{
					"map1_items":     []any{"x"},
					"map1_results":   []any{map[string]any{"result": "mapped"}},
					"map1_completed": 1,
					"map1_total":     1,
				},
			},
		},
		{
			name: "loop child completion is routed to the loop node",
			cfg: &WorkflowConfig{Nodes: []*NodeConfig{
				{ID: "loop1", Type: "loop", Condition: "false", BodyNode: "body1", MaxIterations: 10},
				{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"loop1"}},
			}},
			pending:        []string{"loop1"},
			loopIterations: map[string]int{"loop1": 0},
			completion:     NodeCompletion{NodeID: "body1", LoopNodeID: "loop1", Output: map[string]any{"iter_result": "ok"}},
			want: stateView{
				Completed: map[string]string{"loop1": "loop_condition_false"},
				Outputs: map[string]map[string]any{
					"body1": {"output": map[string]any{"iter_result": "ok"}},
					"loop1": {"output": map[string]any{"iterations_completed": 1, "stopped_reason": "condition_false"}},
				},
				LoopIterations: map[string]int{"loop1": 1},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			maps.Copy(state.CompletedNodes, tc.completed)
			for _, id := range tc.pending {
				state.AddPending(id)
			}
			maps.Copy(state.Metadata, tc.metadata)
			maps.Copy(state.LoopIterations, tc.loopIterations)
			e := newExecutorFor(t, tc.cfg, state)

			e.handleCompletion(tc.completion)

			if diff := diffState(tc.want, state, tc.markerNodes...); diff != "" {
				t.Errorf("handleCompletion(%+v) state mismatch (-want +got):\n%s", tc.completion, diff)
			}
		})
	}
}

func TestHandleCompletion_ContextCancelled(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
	}
	cfg := &WorkflowConfig{Nodes: nodes}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e.execCtx.Ctx = ctx

	state.AddPending("a")
	e.handleCompletion(NodeCompletion{
		NodeID:    "a",
		SubTaskID: "sub-1",
		Output:    map[string]any{"result": "late"},
	})

	if state.IsNodeCompleted("a") {
		t.Error("should not process completion when execution is cancelled")
	}
}

func TestHandleMapChildCompletion(t *testing.T) {
	type step struct {
		completion    NodeCompletion
		wantMetadata  map[string]any
		wantOutput    map[string]any
		wantCompleted map[string]string
		wantPending   []string
	}
	tests := []struct {
		name     string
		items    []any
		metadata map[string]any
		steps    []step
	}{
		{
			name:  "failed item is recorded and the map completes with the last item",
			items: []any{"a", "b"},
			metadata: map[string]any{
				"map1_items":     []any{"a", "b"},
				"map1_results":   make([]any, 2),
				"map1_completed": 0,
				"map1_total":     2,
			},
			steps: []step{
				{
					completion: NodeCompletion{NodeID: "body1", MapNodeID: "map1", ItemIndex: 0, Error: errors.New("item 0 failed")},
					wantMetadata: map[string]any{
						"map1_items":     []any{"a", "b"},
						"map1_results":   []any{map[string]any{"error": "item 0 failed"}, nil},
						"map1_completed": 1,
						"map1_total":     2,
					},
					wantPending: []string{"map1"},
				},
				{
					completion: NodeCompletion{NodeID: "body1", MapNodeID: "map1", ItemIndex: 1, Output: map[string]any{"ok": true}},
					wantMetadata: map[string]any{
						"map1_items":     []any{"a", "b"},
						"map1_results":   []any{map[string]any{"error": "item 0 failed"}, map[string]any{"ok": true}},
						"map1_completed": 2,
						"map1_total":     2,
					},
					wantOutput: map[string]any{"output": map[string]any{
						"results": []any{map[string]any{"error": "item 0 failed"}, map[string]any{"ok": true}},
					}},
					wantCompleted: map[string]string{"map1": completionMarker},
				},
			},
		},
		{
			name:  "unlimited concurrency only tracks progress until the last item",
			items: []any{"a", "b", "c"},
			metadata: map[string]any{
				"map1_items":     []any{"a", "b", "c"},
				"map1_results":   make([]any, 3),
				"map1_completed": 0,
				"map1_total":     3,
			},
			steps: []step{
				{
					completion: NodeCompletion{NodeID: "body1", MapNodeID: "map1", ItemIndex: 0, Output: map[string]any{"r": "a"}},
					wantMetadata: map[string]any{
						"map1_items":     []any{"a", "b", "c"},
						"map1_results":   []any{map[string]any{"r": "a"}, nil, nil},
						"map1_completed": 1,
						"map1_total":     3,
					},
					wantPending: []string{"map1"},
				},
				{
					completion: NodeCompletion{NodeID: "body1", MapNodeID: "map1", ItemIndex: 1, Output: map[string]any{"r": "b"}},
					wantMetadata: map[string]any{
						"map1_items":     []any{"a", "b", "c"},
						"map1_results":   []any{map[string]any{"r": "a"}, map[string]any{"r": "b"}, nil},
						"map1_completed": 2,
						"map1_total":     3,
					},
					wantPending: []string{"map1"},
				},
				{
					completion: NodeCompletion{NodeID: "body1", MapNodeID: "map1", ItemIndex: 2, Output: map[string]any{"r": "c"}},
					wantMetadata: map[string]any{
						"map1_items":     []any{"a", "b", "c"},
						"map1_results":   []any{map[string]any{"r": "a"}, map[string]any{"r": "b"}, map[string]any{"r": "c"}},
						"map1_completed": 3,
						"map1_total":     3,
					},
					wantOutput: map[string]any{"output": map[string]any{
						"results": []any{map[string]any{"r": "a"}, map[string]any{"r": "b"}, map[string]any{"r": "c"}},
					}},
					wantCompleted: map[string]string{"map1": completionMarker},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []*NodeConfig{
				{ID: "map1", Type: "map", TemplateNode: "body1", ItemsExpression: tc.items},
				{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"map1"}},
			}
			state := NewExecutionState("wf", "e1")
			maps.Copy(state.Metadata, tc.metadata)
			state.AddPending("map1")
			e := newExecutorFor(t, &WorkflowConfig{Nodes: nodes}, state)

			for i, s := range tc.steps {
				e.handleMapChildCompletion(s.completion)

				if diff := cmp.Diff(s.wantMetadata, state.Metadata); diff != "" {
					t.Errorf("step %d: handleMapChildCompletion(%+v) Metadata mismatch (-want +got):\n%s", i, s.completion, diff)
				}
				if diff := cmp.Diff(s.wantOutput, state.NodeOutputs["map1"]); diff != "" {
					t.Errorf("step %d: handleMapChildCompletion(%+v) NodeOutputs[map1] mismatch (-want +got):\n%s", i, s.completion, diff)
				}
				if diff := cmp.Diff(s.wantCompleted, withMarkersHidden(state.CompletedNodes, "map1"), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("step %d: handleMapChildCompletion(%+v) CompletedNodes mismatch (-want +got):\n%s", i, s.completion, diff)
				}
				if diff := cmp.Diff(s.wantPending, pendingNodeIDs(state), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("step %d: handleMapChildCompletion(%+v) pending nodes mismatch (-want +got):\n%s", i, s.completion, diff)
				}
			}
		})
	}
}

func TestHandleLoopIterationComplete(t *testing.T) {
	body := &NodeConfig{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"loop1"}}
	tests := []struct {
		name       string
		cfg        *WorkflowConfig
		iteration  int
		completion NodeCompletion
		want       stateView
	}{
		{
			name: "loop stops at max iterations",
			cfg: &WorkflowConfig{Nodes: []*NodeConfig{
				{ID: "loop1", Type: "loop", Condition: "true", BodyNode: "body1", MaxIterations: 2}, body,
			}},
			iteration:  1,
			completion: NodeCompletion{NodeID: "body1", Output: map[string]any{"data": "iter1"}},
			want: stateView{
				Completed: map[string]string{"loop1": "loop_max_iterations"},
				Outputs: map[string]map[string]any{
					"body1": {"output": map[string]any{"data": "iter1"}},
					"loop1": {"output": map[string]any{"iterations_completed": 2, "stopped_reason": "max_iterations"}},
				},
				LoopIterations: map[string]int{"loop1": 2},
			},
		},
		{
			name: "iteration error with fail fast finalizes the execution",
			cfg: &WorkflowConfig{
				Nodes: []*NodeConfig{
					{ID: "loop1", Type: "loop", Condition: "true", BodyNode: "body1", MaxIterations: 10}, body,
				},
				FailFast:      true,
				OutputMapping: map[string]any{"x": "y"},
			},
			completion: NodeCompletion{NodeID: "body1", Error: errors.New("iteration failed")},
			want: stateView{
				Finalized:      true,
				ErrorState:     &ErrorInfo{FailedNodeID: "loop1", FailureReason: "error", ErrorMessage: "iteration failed"},
				LoopIterations: map[string]int{"loop1": 0},
			},
		},
		{
			name: "loop stops when the condition is false",
			cfg: &WorkflowConfig{Nodes: []*NodeConfig{
				{ID: "loop1", Type: "loop", Condition: "false", BodyNode: "body1", MaxIterations: 10}, body,
			}},
			completion: NodeCompletion{NodeID: "body1", Output: map[string]any{"val": 1}},
			want: stateView{
				Completed: map[string]string{"loop1": "loop_condition_false"},
				Outputs: map[string]map[string]any{
					"body1": {"output": map[string]any{"val": 1}},
					"loop1": {"output": map[string]any{"iterations_completed": 1, "stopped_reason": "condition_false"}},
				},
				LoopIterations: map[string]int{"loop1": 1},
			},
		},
		{
			name: "condition reading the body output stops the loop",
			cfg: &WorkflowConfig{Nodes: []*NodeConfig{
				{ID: "loop1", Type: "loop", Condition: `"{{body1.output.status}}" == "continue"`, BodyNode: "body1", MaxIterations: 10}, body,
			}},
			completion: NodeCompletion{NodeID: "body1", Output: map[string]any{"status": "done"}},
			want: stateView{
				Completed: map[string]string{"loop1": "loop_condition_false"},
				Outputs: map[string]map[string]any{
					"body1": {"output": map[string]any{"status": "done"}},
					"loop1": {"output": map[string]any{"iterations_completed": 1, "stopped_reason": "condition_false"}},
				},
				LoopIterations: map[string]int{"loop1": 1},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			state.LoopIterations["loop1"] = tc.iteration
			state.AddPending("loop1")
			e := newExecutorFor(t, tc.cfg, state)

			e.handleLoopIterationComplete(e.execCtx.Ctx, "loop1", tc.completion)

			if diff := diffState(tc.want, state); diff != "" {
				t.Errorf("handleLoopIterationComplete(loop1, %+v) state mismatch (-want +got):\n%s", tc.completion, diff)
			}
		})
	}
}

func TestBuildRequestParts(t *testing.T) {
	agentA := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}
	plainAndText := []*NodeConfig{
		{ID: "plain", Type: "agent", AgentName: "agentA"},
		{ID: "text", Type: "agent", AgentName: "agentA", OutputSchemaOverride: map[string]any{"type": "string"}},
	}
	tests := []struct {
		name     string
		nodes    []*NodeConfig
		node     string
		input    map[string]any
		artifact string
		suffix   string
		outputs  map[string]map[string]any
		want     []a2a.Part
	}{
		{
			name:     "invocation request data part is followed by the input file part",
			nodes:    agentA,
			node:     "a",
			input:    map[string]any{"key": "value"},
			artifact: "art-name",
			want: []a2a.Part{
				{Kind: "data", Data: map[string]any{
					"type":                      "structured_invocation_request",
					"workflow_name":             "wf1",
					"node_id":                   "a",
					"input_schema":              map[string]any(nil),
					"output_schema":             map[string]any{"type": "object"},
					"suggested_output_filename": "wf_a_output_e1.json",
				}},
				{Kind: "file", File: &a2a.FileContent{Bytes: "eyJrZXkiOiJ2YWx1ZSJ9", Name: "art-name", MIMEType: "application/json"}},
			},
		},
		{
			name: "instruction is resolved against node outputs into a text part",
			nodes: []*NodeConfig{
				{ID: "a", Type: "agent", AgentName: "agentA", Instruction: "Process the data from {{prev.output.source}}"},
			},
			node:     "a",
			input:    map[string]any{},
			artifact: "art",
			outputs:  map[string]map[string]any{"prev": {"output": map[string]any{"source": "database"}}},
			want: []a2a.Part{
				{Kind: "data", Data: map[string]any{
					"type":                      "structured_invocation_request",
					"workflow_name":             "wf1",
					"node_id":                   "a",
					"input_schema":              map[string]any(nil),
					"output_schema":             map[string]any{"type": "object"},
					"suggested_output_filename": "wf_a_output_e1.json",
				}},
				{Kind: "text", Text: "Process the data from database"},
			},
		},
		{
			name: "schema overrides and filename suffix are sent",
			nodes: []*NodeConfig{
				{
					ID: "a", Type: "agent", AgentName: "agentA",
					InputSchemaOverride:  map[string]any{"type": "object"},
					OutputSchemaOverride: map[string]any{"type": "string"},
				},
			},
			node:     "a",
			input:    map[string]any{"x": 1},
			artifact: "art",
			suffix:   "_0",
			want: []a2a.Part{
				{Kind: "data", Data: map[string]any{
					"type":                      "structured_invocation_request",
					"workflow_name":             "wf1",
					"node_id":                   "a",
					"input_schema":              map[string]any{"type": "object"},
					"output_schema":             map[string]any{"type": "string"},
					"suggested_output_filename": "wf_a_output_e1_0.json",
				}},
				{Kind: "file", File: &a2a.FileContent{Bytes: "eyJ4IjoxfQ==", Name: "art", MIMEType: "application/json"}},
			},
		},
		{
			name:  "empty input has no file part",
			nodes: agentA,
			node:  "a",
			input: map[string]any{},
			want: []a2a.Part{
				{Kind: "data", Data: map[string]any{
					"type":                      "structured_invocation_request",
					"workflow_name":             "wf1",
					"node_id":                   "a",
					"input_schema":              map[string]any(nil),
					"output_schema":             map[string]any{"type": "object"},
					"suggested_output_filename": "wf_a_output_e1.json",
				}},
			},
		},
		{
			name:  "agent node without an output schema asks for an object",
			nodes: plainAndText,
			node:  "plain",
			want: []a2a.Part{
				{Kind: "data", Data: map[string]any{
					"type":                      "structured_invocation_request",
					"workflow_name":             "wf1",
					"node_id":                   "plain",
					"input_schema":              map[string]any(nil),
					"output_schema":             map[string]any{"type": "object"},
					"suggested_output_filename": "wf_plain_output_e1.json",
				}},
			},
		},
		{
			name:  "authored output schema is sent as written",
			nodes: plainAndText,
			node:  "text",
			want: []a2a.Part{
				{Kind: "data", Data: map[string]any{
					"type":                      "structured_invocation_request",
					"workflow_name":             "wf1",
					"node_id":                   "text",
					"input_schema":              map[string]any(nil),
					"output_schema":             map[string]any{"type": "string"},
					"suggested_output_filename": "wf_text_output_e1.json",
				}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &WorkflowConfig{Name: "wf1", Nodes: tc.nodes}
			e := newExecutorFor(t, cfg, NewExecutionState("wf1", "e1"))
			maps.Copy(e.state.NodeOutputs, tc.outputs)
			node := e.dag.Nodes[tc.node]

			got := e.buildRequestParts(node, node.AgentName, tc.artifact, tc.input, tc.suffix, e.state.NodeOutputs)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("buildRequestParts(%q) mismatch (-want +got):\n%s", tc.node, diff)
			}
		})
	}
}

func TestAdvance(t *testing.T) {
	const subTaskPlaceholder = "<dispatched sub-task>"
	type step struct {
		event Event
		want  stateView
	}
	start := &EventStart{TaskID: "test-task", ContextID: "test-ctx"}
	tests := []struct {
		name        string
		cfg         *WorkflowConfig
		markerNodes []string
		pending     []string
		steps       []step
	}{
		{
			name: "cancel event finalizes the execution as cancelled",
			cfg: &WorkflowConfig{
				Nodes:         []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}},
				OutputMapping: map[string]any{"r": "d"},
			},
			steps: []step{{event: &EventCancel{Reason: "test"}, want: stateView{Finalized: true, Cancelled: true}}},
		},
		{
			name:    "node completion event completes the pending node",
			cfg:     &WorkflowConfig{Nodes: []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}},
			pending: []string{"a"},
			steps: []step{{
				event: &EventNodeComplete{NodeID: "a", SubTaskID: "sub-1", Output: map[string]any{"done": true}},
				want: stateView{
					Completed: map[string]string{"a": "sub-1"},
					Outputs:   map[string]map[string]any{"a": {"output": map[string]any{"done": true}}},
					Metadata:  map[string]any{"__phaseb_final_output_map": map[string]any{}},
					Finalized: true,
				},
			}},
		},
		{
			name: "start event runs a chain of switch nodes to the end",
			cfg: &WorkflowConfig{
				Name:      "test-wf",
				Namespace: "ns",
				Nodes: []*NodeConfig{
					{ID: "a", Type: "switch", Cases: []SwitchCase{{Condition: "true", Node: "selected"}}},
					{ID: "selected", Type: "switch", Cases: []SwitchCase{{Condition: "true", Node: "done_marker"}}, DependsOn: []string{"a"}},
					{ID: "done_marker", Type: "switch", Cases: []SwitchCase{}, DependsOn: []string{"selected"}},
				},
				OutputMapping: map[string]any{"status": "ok"},
			},
			markerNodes: []string{"a", "selected", "done_marker"},
			steps: []step{{event: start, want: stateView{
				Completed: map[string]string{"a": completionMarker, "selected": completionMarker, "done_marker": completionMarker},
				Outputs: map[string]map[string]any{
					"a":           {"output": map[string]any{"cases_unresolved": 0, "selected_branch": "selected", "selected_case_index": 0}},
					"selected":    {"output": map[string]any{"cases_unresolved": 0, "selected_branch": "done_marker", "selected_case_index": 0}},
					"done_marker": {"output": map[string]any{"cases_unresolved": 0, "selected_branch": "", "selected_case_index": -1}},
				},
				Metadata:  map[string]any{"__phaseb_final_output_map": map[string]any{"status": "ok"}},
				Finalized: true,
			}}},
		},
		{
			name: "start event skips a node whose when clause is false",
			cfg: &WorkflowConfig{
				Name:          "test-wf",
				Namespace:     "ns",
				Nodes:         []*NodeConfig{{ID: "a", Type: "switch", Cases: []SwitchCase{}, When: "false"}},
				OutputMapping: map[string]any{"status": "skipped"},
			},
			steps: []step{{event: start, want: stateView{
				Skipped:   []string{"a"},
				Metadata:  map[string]any{"__phaseb_final_output_map": map[string]any{"status": "skipped"}},
				Finalized: true,
			}}},
		},
		{
			name: "start event fails fast on an unknown node type",
			cfg: &WorkflowConfig{
				Name:          "test-wf",
				Namespace:     "ns",
				Nodes:         []*NodeConfig{{ID: "a", Type: "unknown_type"}},
				FailFast:      true,
				OutputMapping: map[string]any{"r": "d"},
			},
			steps: []step{{event: start, want: stateView{
				Finalized:  true,
				ErrorState: &ErrorInfo{FailedNodeID: "a", FailureReason: "error", ErrorMessage: `unknown node type "unknown_type"`},
			}}},
		},
		{
			name: "start event runs independent switch nodes in parallel",
			cfg: &WorkflowConfig{
				Name:      "par-wf",
				Namespace: "ns",
				Nodes: []*NodeConfig{
					{ID: "sw1", Type: "switch", Cases: []SwitchCase{{Condition: "true", Node: "b1"}}},
					{ID: "sw2", Type: "switch", Cases: []SwitchCase{{Condition: "true", Node: "b2"}}},
					{ID: "b1", Type: "switch", Cases: []SwitchCase{}, DependsOn: []string{"sw1"}},
					{ID: "b2", Type: "switch", Cases: []SwitchCase{}, DependsOn: []string{"sw2"}},
				},
				OutputMapping: map[string]any{"ok": true},
			},
			markerNodes: []string{"sw1", "sw2", "b1", "b2"},
			steps: []step{{event: start, want: stateView{
				Completed: map[string]string{"sw1": completionMarker, "sw2": completionMarker, "b1": completionMarker, "b2": completionMarker},
				Outputs: map[string]map[string]any{
					"sw1": {"output": map[string]any{"cases_unresolved": 0, "selected_branch": "b1", "selected_case_index": 0}},
					"sw2": {"output": map[string]any{"cases_unresolved": 0, "selected_branch": "b2", "selected_case_index": 0}},
					"b1":  {"output": map[string]any{"cases_unresolved": 0, "selected_branch": "", "selected_case_index": -1}},
					"b2":  {"output": map[string]any{"cases_unresolved": 0, "selected_branch": "", "selected_case_index": -1}},
				},
				Metadata:  map[string]any{"__phaseb_final_output_map": map[string]any{"ok": true}},
				Finalized: true,
			}}},
		},
		{
			name: "agent node stays pending until its completion event arrives",
			cfg: &WorkflowConfig{
				Name:      "test-wf",
				Namespace: "ns",
				Nodes: []*NodeConfig{
					{ID: "prep", Type: "switch", Cases: []SwitchCase{{Condition: "true", Node: "work"}}},
					{ID: "work", Type: "agent", AgentName: "agentA", DependsOn: []string{"prep"}},
				},
				OutputMapping: map[string]any{"result": "{{work.output.data}}"},
			},
			markerNodes: []string{"prep"},
			steps: []step{
				{
					event: start,
					want: stateView{
						Completed: map[string]string{"prep": completionMarker},
						Pending:   []string{"work"},
						Outputs:   map[string]map[string]any{"prep": {"output": map[string]any{"cases_unresolved": 0, "selected_branch": "work", "selected_case_index": 0}}},
					},
				},
				{
					event: &EventNodeComplete{NodeID: "work", SubTaskID: subTaskPlaceholder, Output: map[string]any{"data": "result-value"}},
					want: stateView{
						Completed: map[string]string{"prep": completionMarker, "work": subTaskPlaceholder},
						Outputs: map[string]map[string]any{
							"prep": {"output": map[string]any{"cases_unresolved": 0, "selected_branch": "work", "selected_case_index": 0}},
							"work": {"output": map[string]any{"data": "result-value"}},
						},
						Metadata:  map[string]any{"__phaseb_final_output_map": map[string]any{"result": "result-value"}},
						Finalized: true,
					},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			for _, id := range tc.pending {
				state.AddPending(id)
			}
			e := newExecutorFor(t, tc.cfg, state)

			for i, s := range tc.steps {
				event, want := s.event, s.want
				if done, ok := event.(*EventNodeComplete); ok && done.SubTaskID == subTaskPlaceholder {
					subTaskID, found := state.SubTaskForNode(done.NodeID)
					if !found {
						t.Fatalf("step %d: node %q has no dispatched sub-task", i, done.NodeID)
					}
					resolved := *done
					resolved.SubTaskID = subTaskID
					event = &resolved
					want.Completed = maps.Clone(s.want.Completed)
					for id, sub := range want.Completed {
						if sub == subTaskPlaceholder {
							want.Completed[id] = subTaskID
						}
					}
				}

				if err := e.Advance(e.execCtx.Ctx, event); err != nil {
					t.Fatalf("step %d: Advance(%+v) error: %v", i, s.event, err)
				}

				if diff := diffState(want, state, tc.markerNodes...); diff != "" {
					t.Errorf("step %d: Advance(%+v) state mismatch (-want +got):\n%s", i, s.event, diff)
				}
			}
		})
	}
}

func TestExecuteMapNode_EmptyItems(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "map1", Type: "map", TemplateNode: "body1", ItemsExpression: []any{}},
		{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"map1"}},
	}
	state := NewExecutionState("wf", "e1")
	e := newExecutorFor(t, &WorkflowConfig{Nodes: nodes}, state)

	e.executeMapNode(e.execCtx.Ctx, nodes[0])

	want := stateView{
		Completed: map[string]string{"map1": "map_empty"},
		Outputs:   map[string]map[string]any{"map1": {"output": map[string]any{"results": []any{}}}},
	}
	if diff := diffState(want, state); diff != "" {
		t.Errorf("executeMapNode(map1) with no items: state mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteMapNode_MaxItemsLimit(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "map1", Type: "map", TemplateNode: "body1",
			ItemsExpression: []any{"a", "b", "c", "d"}, MaxItems: 2},
		{ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"map1"}},
	}
	state := NewExecutionState("wf", "e1")
	e := newExecutorFor(t, &WorkflowConfig{Nodes: nodes}, state)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	e.executeMapNode(ctx, nodes[0])

	want := stateView{
		Pending: []string{"map1"},
		Metadata: map[string]any{
			"map1_items":       []any{"a", "b"},
			"map1_results":     []any{nil, nil},
			"map1_done":        []bool{false, false},
			"map1_completed":   0,
			"map1_total":       2,
			"map1_concurrency": 2,
		},
	}
	if diff := diffState(want, state); diff != "" {
		t.Errorf("executeMapNode(map1) with MaxItems 2 over 4 items and a cancelled context: state mismatch (-want +got):\n%s", diff)
	}
}

func TestScheduleRetry_IncrementsCountAndMarksPending(t *testing.T) {
	cfg := &WorkflowConfig{
		Nodes: []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}},
		RetryStrategy: &RetryStrategy{
			Limit: 3,
			Backoff: &BackoffStrategy{
				Duration: 1 * time.Millisecond,
				Factor:   1,
			},
		},
	}
	state := NewExecutionState("wf", "e1")
	e := newExecutorFor(t, cfg, state)

	e.scheduleRetry("a", errors.New("transient"))

	want := stateView{
		Pending:     []string{"a"},
		RetryCounts: map[string]int{"a": 1},
		Timers:      []Timer{{Kind: TimerKindRetry, NodeID: "a"}},
	}
	if diff := diffState(want, state); diff != "" {
		t.Errorf("scheduleRetry(a) state mismatch (-want +got):\n%s", diff)
	}
}

func TestRedispatchNode(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *WorkflowConfig
		timers   []Timer
		subTasks map[string]string
		want     stateView
	}{
		{
			name:     "dispatching again drains the retry timer and the stale sub-task",
			cfg:      &WorkflowConfig{Nodes: []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}},
			timers:   []Timer{{Kind: TimerKindRetry, NodeID: "a", FiresAt: time.Now()}},
			subTasks: map[string]string{"stale-sub": "a"},
			want:     stateView{Pending: []string{"a"}},
		},
		{
			name: "unknown node type fails the node and finalizes with fail fast",
			cfg: &WorkflowConfig{
				Nodes:         []*NodeConfig{{ID: "a", Type: "bad_type"}},
				FailFast:      true,
				OutputMapping: map[string]any{"r": "d"},
			},
			want: stateView{
				Finalized:  true,
				ErrorState: &ErrorInfo{FailedNodeID: "a", FailureReason: "error", ErrorMessage: `cannot retry node type "bad_type"`},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			for _, timer := range tc.timers {
				state.AddTimer(timer)
			}
			state.AddPending("a")
			for subTaskID, nodeID := range tc.subTasks {
				state.TrackSubTask(subTaskID, nodeID)
			}
			e := newExecutorFor(t, tc.cfg, state)

			e.redispatchNode("a")

			if diff := diffState(tc.want, state); diff != "" {
				t.Errorf("redispatchNode(a) state mismatch (-want +got):\n%s", diff)
			}
			for subTaskID := range tc.subTasks {
				if nodeID, ok := state.NodeForSubTask(subTaskID); ok {
					t.Errorf("redispatchNode(a): stale sub-task %q still maps to node %q", subTaskID, nodeID)
				}
			}
		})
	}
}

func TestRedispatchNode_NonexistentNode(t *testing.T) {
	cfg := &WorkflowConfig{
		Nodes:         []*NodeConfig{},
		FailFast:      true,
		OutputMapping: map[string]any{"r": "d"},
	}
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()
	state.AddPending("nonexistent")

	e.redispatchNode("nonexistent")

	want := stateView{
		Finalized:  true,
		ErrorState: &ErrorInfo{FailedNodeID: "nonexistent", FailureReason: "error", ErrorMessage: `node "nonexistent" not found for retry`},
	}
	if diff := diffState(want, state); diff != "" {
		t.Errorf("redispatchNode(nonexistent) state mismatch (-want +got):\n%s", diff)
	}
}

func TestAdvance_ElapsedDeadline(t *testing.T) {
	agentA := []*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}
	tests := []struct {
		name  string
		cfg   *WorkflowConfig
		event Event
		want  stateView
	}{
		{
			name:  "workflow timeout event cancels the execution",
			cfg:   &WorkflowConfig{Nodes: agentA, OutputMapping: map[string]any{"r": "d"}},
			event: &EventWorkflowTimeout{},
			want:  stateView{Finalized: true, Cancelled: true},
		},
		{
			name: "start event with the deadline already passed cancels the execution",
			cfg: &WorkflowConfig{
				Name:            "test-wf",
				Namespace:       "ns",
				Nodes:           agentA,
				WorkflowTimeout: 1 * time.Millisecond,
				OutputMapping:   map[string]any{"r": "d"},
			},
			event: &EventStart{TaskID: "test-task", ContextID: "test-ctx"},
			want:  stateView{Finalized: true, Cancelled: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			state.WorkflowDeadline = time.Now().Add(-time.Millisecond)
			e := newExecutorFor(t, tc.cfg, state)

			if err := e.Advance(e.execCtx.Ctx, tc.event); err != nil {
				t.Fatalf("Advance(%+v) error: %v", tc.event, err)
			}

			if diff := diffState(tc.want, state); diff != "" {
				t.Errorf("Advance(%+v) state mismatch (-want +got):\n%s", tc.event, diff)
			}
		})
	}
}

func TestRun_EmptyWorkflow(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:          "empty-wf",
		Namespace:     "ns",
		Nodes:         []*NodeConfig{},
		OutputMapping: map[string]any{"result": "nothing"},
	}
	dag := &DependencyGraph{
		Nodes:      map[string]*NodeConfig{},
		InnerNodes: map[string]bool{},
		Forward:    map[string][]string{},
		Reverse:    map[string][]string{},
	}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}

	if !e.state.Finalized {
		t.Error("empty workflow should finalize successfully")
	}
}

func TestBeginExitChain(t *testing.T) {
	type exitChain struct {
		InExitHandler    bool
		ExitHandlersRan  bool
		PendingOutcome   string
		ExitHandlerQueue []string
	}
	tests := []struct {
		name         string
		cfg          *WorkflowConfig
		dag          *DependencyGraph
		errorState   *ErrorInfo
		outcome      string
		errMsg       string
		want         stateView
		wantWorkflow map[string]any
		wantChain    exitChain
	}{
		{
			name:         "no on_exit block publishes the final response at once",
			cfg:          &WorkflowConfig{OnExit: nil},
			dag:          &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}},
			outcome:      "success",
			want:         stateView{Finalized: true},
			wantWorkflow: map[string]any{"run_id": "e1"},
			wantChain:    exitChain{PendingOutcome: "success"},
		},
		{
			name:       "failure outcome and failed node are injected as workflow status",
			cfg:        &WorkflowConfig{OnExit: &ExitHandler{}},
			dag:        &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}},
			errorState: &ErrorInfo{FailedNodeID: "bad_node", ErrorMessage: "oops"},
			outcome:    "failure",
			errMsg:     "node failed",
			want: stateView{
				Finalized:  true,
				ErrorState: &ErrorInfo{FailedNodeID: "bad_node", ErrorMessage: "oops"},
			},
			wantWorkflow: map[string]any{
				"run_id": "e1",
				"status": "failure",
				"error":  map[string]any{"message": "node failed", "node_id": "bad_node"},
			},
			wantChain: exitChain{ExitHandlersRan: true, PendingOutcome: "failure"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := NewExecutionState("wf", "e1")
			state.ErrorState = tc.errorState
			e := newTestDAGExecutor(tc.cfg, tc.dag, state)
			defer e.execCtx.Cancel()

			e.beginExitChain(tc.outcome, tc.errMsg)

			if diff := diffState(tc.want, state); diff != "" {
				t.Errorf("beginExitChain(%q, %q) state mismatch (-want +got):\n%s", tc.outcome, tc.errMsg, diff)
			}
			if diff := cmp.Diff(tc.wantWorkflow, state.NodeOutputs["workflow"]); diff != "" {
				t.Errorf("beginExitChain(%q, %q) NodeOutputs[workflow] mismatch (-want +got):\n%s", tc.outcome, tc.errMsg, diff)
			}
			gotChain := exitChain{state.InExitHandler, state.ExitHandlersRan, state.PendingOutcome, state.ExitHandlerQueue}
			if diff := cmp.Diff(tc.wantChain, gotChain, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("beginExitChain(%q, %q) exit chain mismatch (-want +got):\n%s", tc.outcome, tc.errMsg, diff)
			}
		})
	}
}

func TestBeginExitChain_HandlersThatAlreadyRanAreNotAnnouncedAgain(t *testing.T) {
	cfg := &WorkflowConfig{OnExit: &ExitHandler{Always: "cleanup"}}
	dag := &DependencyGraph{
		Nodes:      map[string]*NodeConfig{"cleanup": {ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"}},
		InnerNodes: map[string]bool{},
	}
	state := NewExecutionState("wf", "e1")
	state.ExitHandlersRan = true
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	e.beginExitChain("success", "")

	if diff := cmp.Diff(map[string]any{"run_id": "e1"}, state.NodeOutputs["workflow"]); diff != "" {
		t.Errorf("beginExitChain(%q, %q) NodeOutputs[workflow] mismatch (-want +got):\n%s", "success", "", diff)
	}
}

func TestPublishNodeStart_DoesNotPanic(t *testing.T) {
	for _, nodeType := range []string{"agent", "workflow", "switch", "loop", "map"} {
		t.Run(nodeType, func(t *testing.T) {
			node := &NodeConfig{
				ID:            "n1",
				Type:          nodeType,
				AgentName:     "agentA",
				WorkflowName:  "wfA",
				Condition:     "true",
				MaxIterations: 5,
				Delay:         100 * time.Millisecond,
				Cases: []SwitchCase{
					{Condition: "true", Node: "branch"},
				},
				DefaultCase: "default_branch",
			}
			nodes := []*NodeConfig{node}
			cfg := &WorkflowConfig{Nodes: nodes}
			dag, _ := BuildDependencyGraph(nodes)
			state := NewExecutionState("wf", "e1")
			e := newTestDAGExecutor(cfg, dag, state)
			defer e.execCtx.Cancel()

			e.publishNodeStart(node, "pg-1", "subtask-1")
		})
	}
}

func TestPublishNodeResult_DoesNotPanic(t *testing.T) {
	cfg := &WorkflowConfig{}
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	errMsg := "something went wrong"
	e.publishNodeResult("node1", "error", &errMsg)
	e.publishNodeResult("node2", "success", nil)
}

func TestShortID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"uuid keeps its last eight characters", "019dd5ed-1223-786c-b52c-7a8a4997e653", "4997e653"},
		{"short id is unchanged", "abcd", "abcd"},
		{"empty id stays empty", "", ""},
		{"exactly eight characters are unchanged", "01234567", "01234567"},
		{"nine characters keep the last eight", "012345678", "12345678"},
		{"dashes anywhere are stripped before truncation", "---abcdef-12345678---", "12345678"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortID(tc.in); got != tc.want {
				t.Errorf("shortID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestInjectWorkflowStatus_PreservesRunID(t *testing.T) {
	state := NewExecutionState("wf", "exec-1")
	state.NodeOutputs["workflow"]["task_id"] = "task-1"
	e := &DAGExecutor{state: state}

	e.injectWorkflowStatus("completed", "")

	cases := map[string]any{
		"{{workflow.run_id}}":  "exec-1",
		"{{workflow.task_id}}": "task-1",
		"{{workflow.status}}":  "completed",
	}
	for expr, want := range cases {
		got, err := resolveTemplate(expr, state.NodeOutputs, nil)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", expr, err)
		}
		if got != want {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}
}

func TestExitHandler_SynchronousDispatchFailure_StillFinalizes(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "main", Type: "agent", AgentName: "mainAgent"},
		{ID: "cleanup", Type: "workflow", WorkflowName: "NotDeployedWorkflow"},
	}
	cfg := &WorkflowConfig{
		Name:          a2a.InstanceName(a2a.KindWorkflow, "019f9100-0000-7000-8000-00000000fee1"),
		Namespace:     "ns",
		Nodes:         nodes,
		OnExit:        &ExitHandler{Always: "cleanup"},
		OutputMapping: map[string]any{"out": "{{main.output}}"},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("BuildDependencyGraph(%d nodes) error: %v", len(nodes), err)
	}
	state := NewExecutionState(cfg.Name, "exec-exit-fail")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	seedCard(e.instance, a2a.AgentCard{Name: "someOtherAgent"})

	e.beginExitChain("success", "")

	if e.state.InExitHandler {
		t.Error("InExitHandler still true — the exit chain never advanced")
	}
	if !e.state.Finalized {
		t.Fatal("workflow never finalized: the failed exit handler stalled the chain, so no final response was published")
	}
}
