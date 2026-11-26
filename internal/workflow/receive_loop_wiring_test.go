// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

type controllableReceiveBroker struct {
	broker.Broker
	mu  sync.Mutex
	err error
}

func (b *controllableReceiveBroker) setErr(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

func (b *controllableReceiveBroker) Receive(ctx context.Context, _ broker.Queue) (*broker.Message, error) {
	b.mu.Lock()
	err := b.err
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Millisecond):
		return nil, nil
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool, failMsg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(failMsg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWorkflowReceiveLoop_ProvisionCooldownDegradesThenRecovers(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "cooldown-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}
	brk := &controllableReceiveBroker{Broker: stubBroker{}}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop(context.Background()) //nolint:errcheck // test cleanup

	brk.setErr(broker.ErrProvisionCooldown)
	waitForCondition(t, 2*time.Second, func() bool { return len(w.health.Snapshot()) > 0 },
		"receiveLoopOn never reported Degraded health for a provision cooldown — Health option not wired")
	for _, p := range w.health.Snapshot() {
		if p.Severity == health.Fatal {
			t.Errorf("cooldown reported as Fatal, want Degraded (a retryable condition must never be terminal): %+v", p)
		}
	}

	brk.setErr(nil)
	waitForCondition(t, 2*time.Second, func() bool { return len(w.health.Snapshot()) == 0 },
		"health never cleared after the cooldown lifted")
}

func TestWorkflowReceiveLoop_UnplannedQueueLossIsFatal(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "queue-loss-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}
	brk := &controllableReceiveBroker{Broker: stubBroker{}}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.cancelFn()

	brk.setErr(broker.ErrQueueNotFound)
	waitForCondition(t, 2*time.Second, func() bool {
		for _, p := range w.health.Snapshot() {
			if p.Severity == health.Fatal {
				return true
			}
		}
		return false
	}, "unplanned ErrQueueNotFound never reported as Fatal — Health option not wired")
}

func TestWorkflowStart_ClearsHealth(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "restart-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}
	w := NewWorkflowInstance(cfg)
	brk := &controllableReceiveBroker{Broker: stubBroker{}}
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	w.health.Set("queue:leftover", health.Fatal, "queue no longer exists")

	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop(context.Background()) //nolint:errcheck // test cleanup

	if problems := w.health.Snapshot(); problems != nil {
		t.Errorf("health.Snapshot() after Start = %v, want nil — a restarted workflow must not carry a permanently orphaned problem from its previous run", problems)
	}
}
