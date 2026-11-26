// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEffectiveNodeTimeout_NodeOverridesDefault(t *testing.T) {
	cfg := &WorkflowConfig{DefaultNodeTimeout: 30 * time.Second}
	state := NewExecutionState("wf", "e1")
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	node := &NodeConfig{ID: "a", Type: "agent", AgentName: "agentA", Timeout: 5 * time.Second}
	if got := e.effectiveNodeTimeout(node); got != 5*time.Second {
		t.Errorf("effectiveNodeTimeout = %v, want 5s", got)
	}
}

func TestEffectiveNodeTimeout_FallsBackToDefault(t *testing.T) {
	cfg := &WorkflowConfig{DefaultNodeTimeout: 30 * time.Second}
	state := NewExecutionState("wf", "e1")
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	node := &NodeConfig{ID: "a", Type: "agent", AgentName: "agentA"}
	if got := e.effectiveNodeTimeout(node); got != 30*time.Second {
		t.Errorf("effectiveNodeTimeout = %v, want 30s (DefaultNodeTimeout)", got)
	}
}

func TestEffectiveNodeTimeout_ZeroWhenUnset(t *testing.T) {
	cfg := &WorkflowConfig{}
	state := NewExecutionState("wf", "e1")
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	node := &NodeConfig{ID: "a", Type: "agent", AgentName: "agentA"}
	if got := e.effectiveNodeTimeout(node); got != 0 {
		t.Errorf("effectiveNodeTimeout = %v, want 0", got)
	}
}

func TestParseWorkflowConfig_DefaultNodeTimeoutMatchesPython(t *testing.T) {
	raw := map[string]any{
		"output_mapping": map[string]any{"r": "d"},
		"nodes": []any{
			map[string]any{"id": "a", "type": "agent", "agent_name": "x"},
		},
	}
	cfg, err := ParseWorkflowConfig("wf", "ns", raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := 5 * time.Minute
	if cfg.DefaultNodeTimeout != want {
		t.Errorf("DefaultNodeTimeout = %v, want %v (Python parity, 300s)", cfg.DefaultNodeTimeout, want)
	}
	if cfg.Nodes[0].Timeout != want {
		t.Errorf("node.Timeout default = %v, want %v", cfg.Nodes[0].Timeout, want)
	}
}

func TestParseWorkflowConfig_ExplicitDefaultNodeTimeoutOverrides(t *testing.T) {
	raw := map[string]any{
		"output_mapping":       map[string]any{"r": "d"},
		"default_node_timeout": "10s",
		"nodes": []any{
			map[string]any{"id": "a", "type": "agent", "agent_name": "x"},
		},
	}
	cfg, err := ParseWorkflowConfig("wf", "ns", raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.DefaultNodeTimeout != 10*time.Second {
		t.Errorf("DefaultNodeTimeout = %v, want 10s", cfg.DefaultNodeTimeout)
	}
}

func countPendingNodeTimers(state *WorkflowExecutionState) int {
	n := 0
	for _, t := range state.Timers {
		if t.Kind == TimerKindNodeTimeout {
			n++
		}
	}
	return n
}

func hasPendingNodeTimer(state *WorkflowExecutionState, subTaskID string) bool {
	for _, t := range state.Timers {
		if t.Kind == TimerKindNodeTimeout && t.SubTaskID == subTaskID {
			return true
		}
	}
	return false
}

func TestArmNodeTimer_FiresAndPushesTimeoutCompletion(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "n1", Type: "agent", AgentName: "agentA", Timeout: 20 * time.Millisecond},
	}
	cfg := &WorkflowConfig{Nodes: nodes, FailFast: true, OutputMapping: map[string]any{"r": "d"}}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	state.AddPending("n1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	e.mu.Lock()
	e.armNodeTimer(nodes[0], "sub-1", NodeCompletion{NodeID: "n1", SubTaskID: "sub-1"})
	e.reconcileTimers()
	e.mu.Unlock()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		done := state.ErrorState != nil
		e.mu.Unlock()
		if done {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if state.ErrorState == nil {
		t.Fatal("expected ErrorState set after timeout")
	}
	if !strings.Contains(state.ErrorState.ErrorMessage, "timed out") {
		t.Errorf("error message = %q, want contains 'timed out'", state.ErrorState.ErrorMessage)
	}
	if state.ErrorState.FailedNodeID != "n1" {
		t.Errorf("FailedNodeID = %q, want n1", state.ErrorState.FailedNodeID)
	}
	if hasPendingNodeTimer(state, "sub-1") {
		t.Error("state.Timers should no longer contain sub-1 after firing")
	}
}

func TestArmNodeTimer_NoTimerWhenZero(t *testing.T) {
	cfg := &WorkflowConfig{}
	state := NewExecutionState("wf", "e1")
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	node := &NodeConfig{ID: "n1", Type: "agent", AgentName: "agentA"}
	e.armNodeTimer(node, "sub-1", NodeCompletion{NodeID: "n1", SubTaskID: "sub-1"})

	if got := countPendingNodeTimers(state); got != 0 {
		t.Errorf("expected no timer armed, got %d", got)
	}
}

func TestCancelNodeTimer_PreventsCallback(t *testing.T) {
	cfg := &WorkflowConfig{}
	state := NewExecutionState("wf", "e1")
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	node := &NodeConfig{ID: "n1", Type: "agent", AgentName: "agentA", Timeout: 100 * time.Millisecond}
	e.mu.Lock()
	e.armNodeTimer(node, "sub-1", NodeCompletion{NodeID: "n1", SubTaskID: "sub-1"})
	e.reconcileTimers()
	e.cancelNodeTimer("sub-1")
	e.reconcileTimers()
	e.mu.Unlock()

	time.Sleep(200 * time.Millisecond)
	e.mu.Lock()
	defer e.mu.Unlock()
	if state.ErrorState != nil {
		t.Fatalf("unexpected ErrorState after cancel: %+v", state.ErrorState)
	}
	if hasPendingNodeTimer(state, "sub-1") {
		t.Error("state.Timers should not contain sub-1 after cancelNodeTimer")
	}
}

func TestCancelAllNodeTimers_StopsAll(t *testing.T) {
	cfg := &WorkflowConfig{}
	state := NewExecutionState("wf", "e1")
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	node := &NodeConfig{ID: "n1", Type: "agent", AgentName: "agentA", Timeout: 100 * time.Millisecond}
	e.mu.Lock()
	e.armNodeTimer(node, "sub-1", NodeCompletion{NodeID: "n1", SubTaskID: "sub-1"})
	e.armNodeTimer(node, "sub-2", NodeCompletion{NodeID: "n1", SubTaskID: "sub-2"})
	e.cancelAllNodeTimers()
	e.reconcileTimers()
	e.mu.Unlock()

	time.Sleep(200 * time.Millisecond)
	e.mu.Lock()
	defer e.mu.Unlock()
	if state.ErrorState != nil {
		t.Fatalf("unexpected ErrorState after cancelAllNodeTimers: %+v", state.ErrorState)
	}
	if got := countPendingNodeTimers(state); got != 0 {
		t.Errorf("expected 0 node timers in state, got %d", got)
	}
}

func TestCancelNodeTimer_RaceWithManagerFire(t *testing.T) {
	cfg := &WorkflowConfig{}
	state := NewExecutionState("wf", "e1")
	dag := &DependencyGraph{Nodes: map[string]*NodeConfig{}, InnerNodes: map[string]bool{}}
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	node := &NodeConfig{ID: "n1", Type: "agent", AgentName: "agentA", Timeout: 1 * time.Millisecond}

	const N = 50
	e.mu.Lock()
	for i := range N {
		subTaskID := fmt.Sprintf("sub-%d", i)
		e.armNodeTimer(node, subTaskID, NodeCompletion{NodeID: "n1", SubTaskID: subTaskID})
		if i%2 == 0 {
			e.cancelNodeTimer(subTaskID)
		}
	}
	e.reconcileTimers()
	e.mu.Unlock()

	time.Sleep(150 * time.Millisecond)
	e.mu.Lock()
	defer e.mu.Unlock()
	if got := countPendingNodeTimers(state); got != 0 {
		t.Errorf("expected 0 node timers after fire window, got %d", got)
	}
}

func TestRun_AgentNodeTimeoutTriggersFailure(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "work", Type: "agent", AgentName: "agentA", Timeout: 10 * time.Millisecond},
	}
	cfg := &WorkflowConfig{
		Name:          "test-wf",
		Namespace:     "ns",
		Nodes:         nodes,
		FailFast:      true,
		OutputMapping: map[string]any{"r": "d"},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		done := e.state.Finalized
		e.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !e.state.Finalized {
		t.Fatal("workflow should be finalized after timeout")
	}
	if state.ErrorState == nil {
		t.Fatal("ErrorState should be set")
	}
	if state.ErrorState.FailedNodeID != "work" {
		t.Errorf("FailedNodeID = %q, want work", state.ErrorState.FailedNodeID)
	}
	if !strings.Contains(state.ErrorState.ErrorMessage, "timed out") {
		t.Errorf("ErrorMessage = %q, want contains 'timed out'", state.ErrorState.ErrorMessage)
	}
}

func TestRun_DefaultNodeTimeoutAppliesWhenNodeTimeoutZero(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "work", Type: "agent", AgentName: "agentA"},
	}
	cfg := &WorkflowConfig{
		Name:               "test-wf",
		Namespace:          "ns",
		Nodes:              nodes,
		FailFast:           true,
		DefaultNodeTimeout: 10 * time.Millisecond,
		OutputMapping:      map[string]any{"r": "d"},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		done := e.state.Finalized
		e.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if state.ErrorState == nil || !strings.Contains(state.ErrorState.ErrorMessage, "timed out") {
		t.Errorf("expected timeout failure, got ErrorState=%+v", state.ErrorState)
	}
}

func TestRun_AgentNodeTimeoutMessageIncludesDuration(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "work", Type: "agent", AgentName: "agentA", Timeout: 17 * time.Millisecond},
	}
	cfg := &WorkflowConfig{
		Name:          "test-wf",
		Namespace:     "ns",
		Nodes:         nodes,
		FailFast:      true,
		OutputMapping: map[string]any{"r": "d"},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "t", ContextID: "c"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		done := e.state.Finalized
		e.mu.Unlock()
		if done {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if state.ErrorState == nil {
		t.Fatal("expected ErrorState set after timeout")
	}
	if !strings.Contains(state.ErrorState.ErrorMessage, "17ms") {
		t.Errorf("error message = %q, want contains resolved duration '17ms'", state.ErrorState.ErrorMessage)
	}
}

func TestRun_TimerCancelledOnNormalCompletion(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "work", Type: "agent", AgentName: "agentA", Timeout: 500 * time.Millisecond},
	}
	cfg := &WorkflowConfig{
		Name:          "test-wf",
		Namespace:     "ns",
		Nodes:         nodes,
		FailFast:      true,
		OutputMapping: map[string]any{"r": "{{work.output.data}}"},
	}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}
	subTaskID, ok := e.state.SubTaskForNode("work")
	if !ok {
		t.Fatal("work should have been dispatched with a sub-task")
	}
	if err := e.Advance(e.execCtx.Ctx, &EventNodeComplete{
		NodeID:    "work",
		SubTaskID: subTaskID,
		Output:    map[string]any{"data": "ok"},
	}); err != nil {
		t.Fatalf("Advance(EventNodeComplete): %v", err)
	}

	if !e.state.Finalized {
		t.Fatal("workflow should finalize")
	}
	if !state.IsNodeCompleted("work") {
		t.Error("work should be completed")
	}
	time.Sleep(600 * time.Millisecond)
	e.mu.Lock()
	defer e.mu.Unlock()
	if got := countPendingNodeTimers(state); got != 0 {
		t.Errorf("expected 0 node timers after completion, got %d", got)
	}
}

func TestRun_TimerCleanupOnContextCancel(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "work", Type: "agent", AgentName: "agentA", Timeout: 5 * time.Second},
	}
	cfg := &WorkflowConfig{
		Name:            "test-wf",
		Namespace:       "ns",
		Nodes:           nodes,
		WorkflowTimeout: 20 * time.Millisecond,
		OutputMapping:   map[string]any{"r": "d"},
	}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		done := e.state.Finalized
		e.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if got := countPendingNodeTimers(state); got != 0 {
		t.Errorf("expected 0 node timers after workflow cancellation, got %d", got)
	}
}

func TestRun_TimeoutTriggersRetry(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "work", Type: "agent", AgentName: "agentA", Timeout: 10 * time.Millisecond},
	}
	cfg := &WorkflowConfig{
		Name:      "test-wf",
		Namespace: "ns",
		Nodes:     nodes,
		FailFast:  true,
		RetryStrategy: &RetryStrategy{
			Limit:       2,
			RetryPolicy: "Always",
			Backoff: &BackoffStrategy{
				Duration: 1 * time.Millisecond,
				Factor:   1,
			},
		},
		OutputMapping: map[string]any{"r": "d"},
	}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		done := e.state.Finalized
		e.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if state.ErrorState == nil {
		t.Fatal("ErrorState should be set after retries exhausted")
	}
	if state.RetryCounts["work"] < 2 {
		t.Errorf("RetryCounts[work] = %d, want >= 2 (retries should have fired)", state.RetryCounts["work"])
	}
}

func TestRun_RetryPolicyOnFailureSkipsSystemError(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "work", Type: "agent", AgentName: "agentA", Timeout: 10 * time.Millisecond},
	}
	cfg := &WorkflowConfig{
		Name:      "test-wf",
		Namespace: "ns",
		Nodes:     nodes,
		FailFast:  true,
		RetryStrategy: &RetryStrategy{
			Limit:       2,
			RetryPolicy: "OnFailure",
			Backoff: &BackoffStrategy{
				Duration: 1 * time.Millisecond,
				Factor:   1,
			},
		},
		OutputMapping: map[string]any{"r": "d"},
	}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		done := e.state.Finalized
		e.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if state.ErrorState == nil {
		t.Fatal("ErrorState should be set after the timeout terminates the workflow")
	}
	if got := state.RetryCounts["work"]; got != 0 {
		t.Errorf("RetryCounts[work] = %d, want 0 (policy=OnFailure should not retry a timeout, which is a system Error)", got)
	}
}

func TestRun_RetryPolicyOnErrorRetriesSystemError(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "work", Type: "agent", AgentName: "agentA", Timeout: 10 * time.Millisecond},
	}
	cfg := &WorkflowConfig{
		Name:      "test-wf",
		Namespace: "ns",
		Nodes:     nodes,
		FailFast:  true,
		RetryStrategy: &RetryStrategy{
			Limit:       2,
			RetryPolicy: "OnError",
			Backoff: &BackoffStrategy{
				Duration: 1 * time.Millisecond,
				Factor:   1,
			},
		},
		OutputMapping: map[string]any{"r": "d"},
	}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		done := e.state.Finalized
		e.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if state.ErrorState == nil {
		t.Fatal("ErrorState should be set after retries exhaust on a timeout")
	}
	if got := state.RetryCounts["work"]; got < 2 {
		t.Errorf("RetryCounts[work] = %d, want >= 2 (policy=OnError must retry a timeout)", got)
	}
}

func TestExecuteExitHandlers_IndependentPerHandlerBudget(t *testing.T) {
	always := &NodeConfig{ID: "always_h", Type: "agent", AgentName: "agentA", Timeout: 10 * time.Millisecond}
	onFail := &NodeConfig{ID: "fail_h", Type: "agent", AgentName: "agentB", Timeout: 10 * time.Millisecond}
	cfg := &WorkflowConfig{
		Name:      "test-wf",
		Namespace: "ns",
		OnExit: &ExitHandler{
			Always:    "always_h",
			OnFailure: "fail_h",
		},
	}
	dag := &DependencyGraph{
		Nodes: map[string]*NodeConfig{
			"always_h": always,
			"fail_h":   onFail,
		},
		InnerNodes: map[string]bool{"always_h": true, "fail_h": true},
		Forward:    map[string][]string{},
		Reverse:    map[string][]string{},
	}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	e.mu.Lock()
	e.beginExitChain("failure", "boom")
	e.reconcileTimers()
	e.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, haveFail := e.state.SubTaskForNode("fail_h")
		if haveFail {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if _, ok := e.state.SubTaskForNode("always_h"); !ok {
		t.Error("always_h handler should have been dispatched")
	}
	if _, ok := e.state.SubTaskForNode("fail_h"); !ok {
		t.Error("fail_h handler should have been dispatched (independent budget)")
	}
}

func TestExecuteExitHandlers_FirstSlowSecondStillRuns(t *testing.T) {
	always := &NodeConfig{ID: "slow_h", Type: "agent", AgentName: "agentA", Timeout: 20 * time.Millisecond}
	onFail := &NodeConfig{ID: "second_h", Type: "agent", AgentName: "agentB", Timeout: 20 * time.Millisecond}
	cfg := &WorkflowConfig{
		Name:      "test-wf",
		Namespace: "ns",
		OnExit: &ExitHandler{
			Always:    "slow_h",
			OnFailure: "second_h",
		},
	}
	dag := &DependencyGraph{
		Nodes: map[string]*NodeConfig{
			"slow_h":   always,
			"second_h": onFail,
		},
		InnerNodes: map[string]bool{"slow_h": true, "second_h": true},
		Forward:    map[string][]string{},
		Reverse:    map[string][]string{},
	}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	e.mu.Lock()
	e.beginExitChain("failure", "boom")
	e.reconcileTimers()
	e.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := e.state.SubTaskForNode("second_h"); ok {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if _, ok := e.state.SubTaskForNode("second_h"); !ok {
		t.Fatal("second_h must run even after slow first handler — independent budgets")
	}
}

func TestHandleCompletion_DropsDuplicateAfterTimeout(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "work", Type: "agent", AgentName: "agentA"},
	}
	cfg := &WorkflowConfig{
		Name:          "test-wf",
		Namespace:     "ns",
		Nodes:         nodes,
		FailFast:      true,
		OutputMapping: map[string]any{"r": "d"},
	}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	state.CompletedNodes["work"] = "done"
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	e.handleCompletion(NodeCompletion{
		NodeID:    "work",
		SubTaskID: "sub-1",
		Error:     fmt.Errorf("phantom timeout"),
	})

	if state.ErrorState != nil {
		t.Errorf("ErrorState should not be set by duplicate completion: %+v", state.ErrorState)
	}
	if e.state.Finalized {
		t.Error("workflow should not finalize from a duplicate completion")
	}
}

func TestHandleMapChildCompletion_DropsDuplicate(t *testing.T) {
	mapNode := &NodeConfig{
		ID:           "m",
		Type:         "map",
		TemplateNode: "inner",
	}
	inner := &NodeConfig{ID: "inner", Type: "agent", AgentName: "agentA"}
	cfg := &WorkflowConfig{
		Name:      "test-wf",
		Namespace: "ns",
		Nodes:     []*NodeConfig{mapNode, inner},
	}
	dag := &DependencyGraph{
		Nodes:      map[string]*NodeConfig{"m": mapNode, "inner": inner},
		InnerNodes: map[string]bool{"inner": true},
		Forward:    map[string][]string{},
		Reverse:    map[string][]string{},
	}
	state := NewExecutionState("wf", "e1")
	state.Metadata["m_results"] = []any{nil, nil}
	state.Metadata["m_done"] = make([]bool, 2)
	state.Metadata["m_completed"] = 0
	state.Metadata["m_total"] = 2
	state.ActiveBranches["m"] = []BranchInfo{{Index: 0, SubTaskID: "sub-0", Type: "map"}}

	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	e.handleMapChildCompletion(NodeCompletion{
		MapNodeID: "m",
		ItemIndex: 0,
		SubTaskID: "sub-0",
		Output:    map[string]any{"v": 1},
	})

	if c := state.Metadata["m_completed"].(int); c != 1 {
		t.Errorf("after first completion, _completed = %d, want 1", c)
	}

	e.handleMapChildCompletion(NodeCompletion{
		MapNodeID: "m",
		ItemIndex: 0,
		SubTaskID: "sub-0",
		Error:     fmt.Errorf("phantom timeout"),
	})

	if c := state.Metadata["m_completed"].(int); c != 1 {
		t.Errorf("after duplicate, _completed = %d, want 1 (idempotent)", c)
	}
}

func TestHandleMapChildCompletion_NilOutputStillDedups(t *testing.T) {
	mapNode := &NodeConfig{ID: "m", Type: "map", TemplateNode: "inner"}
	inner := &NodeConfig{ID: "inner", Type: "agent", AgentName: "agentA"}
	cfg := &WorkflowConfig{Name: "test-wf", Namespace: "ns", Nodes: []*NodeConfig{mapNode, inner}}
	dag := &DependencyGraph{
		Nodes:      map[string]*NodeConfig{"m": mapNode, "inner": inner},
		InnerNodes: map[string]bool{"inner": true},
		Forward:    map[string][]string{},
		Reverse:    map[string][]string{},
	}
	state := NewExecutionState("wf", "e1")
	state.Metadata["m_results"] = []any{nil}
	state.Metadata["m_done"] = make([]bool, 1)
	state.Metadata["m_completed"] = 0
	state.Metadata["m_total"] = 1
	state.ActiveBranches["m"] = []BranchInfo{{Index: 0, SubTaskID: "s0", Type: "map"}}

	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	e.handleMapChildCompletion(NodeCompletion{
		MapNodeID: "m", ItemIndex: 0, SubTaskID: "s0", Output: nil,
	})
	if c := state.Metadata["m_completed"].(int); c != 1 {
		t.Fatalf("after first completion _completed = %d, want 1", c)
	}

	e.handleMapChildCompletion(NodeCompletion{
		MapNodeID: "m", ItemIndex: 0, SubTaskID: "s0", Error: fmt.Errorf("phantom timeout"),
	})
	if c := state.Metadata["m_completed"].(int); c != 1 {
		t.Errorf("after duplicate _completed = %d, want 1 (idempotent despite nil first output)", c)
	}
}
