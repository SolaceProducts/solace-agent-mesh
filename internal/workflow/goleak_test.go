// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestAdvance_NoResidentGoroutinesAfterSuccess(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreCurrent(),
	)

	nodes := []*NodeConfig{
		{ID: "a", Type: "switch", Cases: []SwitchCase{{Condition: "true", Node: "b"}}},
		{ID: "b", Type: "switch", Cases: []SwitchCase{}, DependsOn: []string{"a"}},
	}
	cfg := &WorkflowConfig{
		Name:          "test-wf",
		Namespace:     "ns",
		Nodes:         nodes,
		OutputMapping: map[string]any{"ok": true},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("wf", "e1")
	e := newTestDAGExecutor(cfg, dag, state)

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}
	if !e.state.Finalized {
		t.Fatal("switch-only workflow should finalize synchronously")
	}
	time.Sleep(20 * time.Millisecond)
}

func TestAdvance_NoResidentGoroutinesAfterWorkflowTimeout(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
	}
	cfg := &WorkflowConfig{
		Name:          "test-wf",
		Namespace:     "ns",
		Nodes:         nodes,
		OutputMapping: map[string]any{"r": "d"},
	}
	dag, _ := BuildDependencyGraph(nodes)
	state := NewExecutionState("wf", "e1")
	state.WorkflowDeadline = time.Now().Add(-time.Millisecond)
	e := newTestDAGExecutor(cfg, dag, state)

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}
	if !e.state.Finalized {
		t.Fatal("elapsed-deadline workflow should finalize via cancellation")
	}
	time.Sleep(20 * time.Millisecond)
}

func TestTimerManager_ConstantGoroutineAcrossManyExecutions(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	var fired int64
	m := NewInProcessTimerManager(slog.Default(), func(_ string, _ Event) {
		atomic.AddInt64(&fired, 1)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Start(ctx)

	now := time.Now()
	for ex := range 10 {
		execID := "exec-" + itoa(ex)
		m.Schedule(execID, []Timer{
			{Kind: TimerKindRetry, NodeID: "n1", FiresAt: now.Add(time.Duration(10+ex) * time.Millisecond)},
			{Kind: TimerKindNodeTimeout, NodeID: "n2", SubTaskID: "s2", FiresAt: now.Add(time.Duration(20+ex) * time.Millisecond)},
			{Kind: TimerKindWorkflowTimeout, FiresAt: now.Add(time.Duration(30+ex) * time.Millisecond)},
		})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&fired) == 30 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt64(&fired); got != 30 {
		t.Fatalf("fired = %d, want 30", got)
	}

	m.Schedule("late-exec", []Timer{
		{Kind: TimerKindRetry, NodeID: "n1", FiresAt: time.Now().Add(5 * time.Second)},
		{Kind: TimerKindNodeTimeout, NodeID: "n2", SubTaskID: "s2", FiresAt: time.Now().Add(5 * time.Second)},
	})
	m.Cancel("late-exec")

	cancel()
	time.Sleep(20 * time.Millisecond)
}
