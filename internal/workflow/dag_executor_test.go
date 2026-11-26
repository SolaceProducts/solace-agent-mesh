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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func TestWorkflowDisplayName(t *testing.T) {
	cases := []struct {
		name        string
		cfg         *WorkflowConfig
		wantDisplay string
	}{
		{
			name:        "falls back to Name when DisplayName is unset",
			cfg:         &WorkflowConfig{Name: "workflow_01a0450f_4a44_79fa_ae94_52ca5f019da3"},
			wantDisplay: "workflow_01a0450f_4a44_79fa_ae94_52ca5f019da3",
		},
		{
			name:        "prefers DisplayName over the routable Name",
			cfg:         &WorkflowConfig{Name: "workflow_01a0450f_4a44_79fa_ae94_52ca5f019da3", DisplayName: "QA - No input"},
			wantDisplay: "QA - No input",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &DAGExecutor{cfg: tc.cfg}
			if got := e.workflowDisplayName(); got != tc.wantDisplay {
				t.Errorf("workflowDisplayName() = %q, want %q", got, tc.wantDisplay)
			}
		})
	}
}

func TestCalculateBackoff_NilStrategy(t *testing.T) {
	if d := calculateBackoff(0, nil); d != 1*time.Second {
		t.Errorf("attempt 0: got %v, want 1s", d)
	}
	if d := calculateBackoff(1, nil); d != 2*time.Second {
		t.Errorf("attempt 1: got %v, want 2s", d)
	}
	if d := calculateBackoff(2, nil); d != 4*time.Second {
		t.Errorf("attempt 2: got %v, want 4s", d)
	}
}

func TestCalculateBackoff_CustomStrategy(t *testing.T) {
	bs := &BackoffStrategy{
		Duration:    500 * time.Millisecond,
		Factor:      3.0,
		MaxDuration: 10 * time.Second,
	}

	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 500 * time.Millisecond},
		{1, 1500 * time.Millisecond},
		{2, 4500 * time.Millisecond},
		{3, 10 * time.Second},
		{4, 10 * time.Second},
	}

	for _, tc := range cases {
		got := calculateBackoff(tc.attempt, bs)
		if got != tc.want {
			t.Errorf("attempt %d: got %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestCalculateBackoff_NoMaxDuration(t *testing.T) {
	bs := &BackoffStrategy{
		Duration: 1 * time.Second,
		Factor:   2.0,
	}

	if d := calculateBackoff(10, bs); d != 1024*time.Second {
		t.Errorf("attempt 10 uncapped: got %v, want 1024s", d)
	}
}

func TestShouldRetryNode_NoStrategy(t *testing.T) {
	e := &DAGExecutor{
		cfg:   &WorkflowConfig{},
		dag:   &DependencyGraph{Nodes: map[string]*NodeConfig{"n1": {ID: "n1"}}},
		state: NewExecutionState("wf", "exec1"),
	}
	if e.shouldRetryNode("n1", ErrorKindFailure) {
		t.Error("should not retry without a strategy")
	}
}

func TestShouldRetryNode_WorkflowLevel(t *testing.T) {
	e := &DAGExecutor{
		cfg: &WorkflowConfig{
			RetryStrategy: &RetryStrategy{Limit: 2},
		},
		dag:   &DependencyGraph{Nodes: map[string]*NodeConfig{"n1": {ID: "n1"}}},
		state: NewExecutionState("wf", "exec1"),
	}

	if !e.shouldRetryNode("n1", ErrorKindFailure) {
		t.Error("should retry (0 < 2)")
	}

	e.state.RetryCounts["n1"] = 1
	if !e.shouldRetryNode("n1", ErrorKindFailure) {
		t.Error("should retry (1 < 2)")
	}

	e.state.RetryCounts["n1"] = 2
	if e.shouldRetryNode("n1", ErrorKindFailure) {
		t.Error("should not retry (2 >= 2)")
	}
}

func TestShouldRetryNode_NodeLevelOverridesWorkflow(t *testing.T) {
	e := &DAGExecutor{
		cfg: &WorkflowConfig{
			RetryStrategy: &RetryStrategy{Limit: 5},
		},
		dag: &DependencyGraph{Nodes: map[string]*NodeConfig{
			"n1": {ID: "n1", Retry: &RetryStrategy{Limit: 1}},
		}},
		state: NewExecutionState("wf", "exec1"),
	}

	if !e.shouldRetryNode("n1", ErrorKindFailure) {
		t.Error("should retry (0 < 1)")
	}

	e.state.RetryCounts["n1"] = 1
	if e.shouldRetryNode("n1", ErrorKindFailure) {
		t.Error("should not retry (1 >= 1, node-level limit, not workflow's 5)")
	}
}

func TestShouldRetryNode_PolicyGating(t *testing.T) {
	makeExec := func(policy string) *DAGExecutor {
		return &DAGExecutor{
			cfg: &WorkflowConfig{
				RetryStrategy: &RetryStrategy{Limit: 1, RetryPolicy: policy},
			},
			dag:   &DependencyGraph{Nodes: map[string]*NodeConfig{"n1": {ID: "n1"}}},
			state: NewExecutionState("wf", "exec1"),
		}
	}

	cases := []struct {
		name   string
		policy string
		kind   ErrorKind
		want   bool
	}{
		{"Always retries Failure", "Always", ErrorKindFailure, true},
		{"Always retries Error", "Always", ErrorKindError, true},
		{"Always retries Unknown (treated as Failure)", "Always", ErrorKindUnknown, true},
		{"OnFailure retries Failure", "OnFailure", ErrorKindFailure, true},
		{"OnFailure skips Error", "OnFailure", ErrorKindError, false},
		{"OnFailure retries Unknown (treated as Failure)", "OnFailure", ErrorKindUnknown, true},
		{"OnError retries Error", "OnError", ErrorKindError, true},
		{"OnError skips Failure", "OnError", ErrorKindFailure, false},
		{"OnError skips Unknown (Unknown→Failure)", "OnError", ErrorKindUnknown, false},
		{"empty defaults to OnFailure (Failure)", "", ErrorKindFailure, true},
		{"empty defaults to OnFailure (Error)", "", ErrorKindError, false},
		{"unknown policy falls back to OnFailure (Failure)", "Whatever", ErrorKindFailure, true},
		{"unknown policy falls back to OnFailure (Error)", "Whatever", ErrorKindError, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := makeExec(tc.policy)
			if got := e.shouldRetryNode("n1", tc.kind); got != tc.want {
				t.Errorf("policy=%q kind=%v: got %v, want %v", tc.policy, tc.kind, got, tc.want)
			}
		})
	}
}

func TestTotalRetryCount(t *testing.T) {
	e := &DAGExecutor{
		state: NewExecutionState("wf", "exec1"),
	}

	if got := e.totalRetryCount(); got != 0 {
		t.Errorf("totalRetryCount = %d, want 0", got)
	}

	e.state.RetryCounts["n1"] = 2
	e.state.RetryCounts["n2"] = 3

	if got := e.totalRetryCount(); got != 5 {
		t.Errorf("totalRetryCount = %d, want 5", got)
	}
}

func TestGetInheritedBranch_NoDeps(t *testing.T) {
	e := &DAGExecutor{
		dag: &DependencyGraph{
			Nodes: map[string]*NodeConfig{
				"a": {ID: "a", DependsOn: nil},
			},
		},
		state: NewExecutionState("wf", "exec1"),
	}
	_, _, ok := e.getInheritedBranch("a")
	if ok {
		t.Error("node with no deps should not inherit a branch")
	}
}

func TestGetInheritedBranch_SingleDepSameBranch(t *testing.T) {
	e := &DAGExecutor{
		dag: &DependencyGraph{
			Nodes: map[string]*NodeConfig{
				"a": {ID: "a"},
				"b": {ID: "b", DependsOn: []string{"a"}},
			},
		},
		state: NewExecutionState("wf", "exec1"),
	}
	e.state.ParallelBranchAssignments = map[string]map[string]int{
		"group1": {"a": 0},
	}

	groupID, branchIdx, ok := e.getInheritedBranch("b")
	if !ok {
		t.Fatal("expected branch inheritance")
	}
	if groupID != "group1" {
		t.Errorf("groupID = %q, want group1", groupID)
	}
	if branchIdx != 0 {
		t.Errorf("branchIdx = %d, want 0", branchIdx)
	}
}

func TestGetInheritedBranch_MultipleDeps_DifferentBranches(t *testing.T) {
	e := &DAGExecutor{
		dag: &DependencyGraph{
			Nodes: map[string]*NodeConfig{
				"a": {ID: "a"},
				"b": {ID: "b"},
				"c": {ID: "c", DependsOn: []string{"a", "b"}},
			},
		},
		state: NewExecutionState("wf", "exec1"),
	}
	e.state.ParallelBranchAssignments = map[string]map[string]int{
		"group1": {"a": 0, "b": 1},
	}

	_, _, ok := e.getInheritedBranch("c")
	if ok {
		t.Error("join point should not inherit a branch")
	}
}

func TestGetInheritedBranch_SkippedDepsIgnored(t *testing.T) {
	e := &DAGExecutor{
		dag: &DependencyGraph{
			Nodes: map[string]*NodeConfig{
				"a": {ID: "a"},
				"b": {ID: "b"},
				"c": {ID: "c", DependsOn: []string{"a", "b"}},
			},
		},
		state: NewExecutionState("wf", "exec1"),
	}
	e.state.ParallelBranchAssignments = map[string]map[string]int{
		"group1": {"a": 0, "b": 1},
	}
	e.state.SkippedNodes["b"] = "condition not met"

	groupID, branchIdx, ok := e.getInheritedBranch("c")
	if !ok {
		t.Fatal("expected branch inheritance when only one non-skipped dep")
	}
	if groupID != "group1" || branchIdx != 0 {
		t.Errorf("got group=%q branch=%d, want group1/0", groupID, branchIdx)
	}
}

func TestExitHandlerNodeSelection(t *testing.T) {
	tests := []struct {
		name    string
		onExit  *ExitHandler
		outcome string
		want    []string
	}{
		{
			name:    "success runs always+onSuccess",
			onExit:  &ExitHandler{Always: "cleanup", OnSuccess: "notify", OnFailure: "alert"},
			outcome: "success",
			want:    []string{"cleanup", "notify"},
		},
		{
			name:    "failure runs always+onFailure",
			onExit:  &ExitHandler{Always: "cleanup", OnSuccess: "notify", OnFailure: "alert"},
			outcome: "failure",
			want:    []string{"cleanup", "alert"},
		},
		{
			name:    "cancelled runs always+onCancel",
			onExit:  &ExitHandler{Always: "cleanup", OnCancel: "cancel_handler"},
			outcome: "cancelled",
			want:    []string{"cleanup", "cancel_handler"},
		},
		{
			name:    "simple string form runs always",
			onExit:  &ExitHandler{Always: "cleanup"},
			outcome: "success",
			want:    []string{"cleanup"},
		},
		{
			name:    "empty handlers for outcome",
			onExit:  &ExitHandler{OnSuccess: "notify"},
			outcome: "failure",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitHandlerNodesToRun(tt.onExit, tt.outcome)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("node[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func newPublishFinalTestExecutor(t *testing.T) (*DAGExecutor, *capturingBroker) {
	t.Helper()
	brk := &capturingBroker{}
	cfg := &WorkflowConfig{Name: "workflow_01a0450f", DisplayName: "QA - No input"}
	w := &WorkflowInstance{
		cfg:    cfg,
		svc:    &capturingServices{brk: brk},
		logger: slog.Default(),
	}
	state := NewExecutionState(cfg.Name, "exec-1")
	execCtx := &WorkflowExecutionContext{
		TaskID:    "parent-task",
		ContextID: "ctx-1",
		UserProps: a2a.UserProperties{ReplyTo: "ns/a2a/v1/gateway/response/abc"},
	}
	return &DAGExecutor{cfg: cfg, state: state, execCtx: execCtx, instance: w, logger: slog.Default()}, brk
}

func finalResponseText(t *testing.T, brk *capturingBroker) string {
	t.Helper()
	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published response, got %d", len(msgs))
	}
	return string(msgs[0].Payload)
}

func TestPublishFinalSuccess_UsesDisplayNameInCompletionText(t *testing.T) {
	e, brk := newPublishFinalTestExecutor(t)
	e.publishFinalSuccess()

	text := finalResponseText(t, brk)
	if !strings.Contains(text, `Workflow \"QA - No input\" completed successfully.`) {
		t.Errorf("response does not contain display-name completion text: %s", text)
	}
	if strings.Contains(text, e.cfg.Name+`\" completed`) {
		t.Errorf("response uses the routable Name instead of DisplayName: %s", text)
	}
}

func TestPublishFinalSuccess_UsesDisplayNameInArtifactDescription(t *testing.T) {
	e, brk := newPublishFinalTestExecutor(t)
	art := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	e.instance.svc = &capturingServices{brk: brk, art: art}

	e.publishFinalSuccess()

	keys, err := art.Inner.ListKeys(context.Background(), artifact.KeyScope{AppName: art.Namespace, UserID: "", SessionID: e.execCtx.ContextID})
	var filename string
	for _, k := range keys {
		if !strings.HasSuffix(k, ".metadata.json") {
			filename = k
		}
	}
	if err != nil || filename == "" {
		t.Fatalf("expected a saved artifact, got keys %v (err %v)", keys, err)
	}
	meta, err := art.Inner.GetVersionMetadata(context.Background(), artifact.Locator{
		AppName: art.Namespace, SessionID: e.execCtx.ContextID, Filename: filename,
	}, nil)
	if err != nil {
		t.Fatalf("GetVersionMetadata: %v", err)
	}
	if meta.Description != `Output of workflow QA - No input` {
		t.Errorf("artifact description = %q, want display name in it", meta.Description)
	}
}

func TestPublishFinalFailure_UsesDisplayNameInCompletionText(t *testing.T) {
	e, brk := newPublishFinalTestExecutor(t)
	e.state.PendingErrorMsg = "boom"
	e.publishFinalFailure()

	text := finalResponseText(t, brk)
	if !strings.Contains(text, `Workflow \"QA - No input\" failed: boom`) {
		t.Errorf("response does not contain display-name failure text: %s", text)
	}
	if strings.Contains(text, e.cfg.Name+`\" failed`) {
		t.Errorf("response uses the routable Name instead of DisplayName: %s", text)
	}
}

func TestPublishFinalCancelled_UsesDisplayNameInCompletionText(t *testing.T) {
	e, brk := newPublishFinalTestExecutor(t)
	e.publishFinalCancelled()

	text := finalResponseText(t, brk)
	if !strings.Contains(text, `Workflow \"QA - No input\" was cancelled.`) {
		t.Errorf("response does not contain display-name cancellation text: %s", text)
	}
	if strings.Contains(text, e.cfg.Name+`\" was cancelled`) {
		t.Errorf("response uses the routable Name instead of DisplayName: %s", text)
	}
}
