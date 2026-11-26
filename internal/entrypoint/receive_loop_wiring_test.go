// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
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

func newControllableTaskLogger(t *testing.T) (*TaskLoggerService, *controllableReceiveBroker) {
	t.Helper()
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := &controllableReceiveBroker{Broker: newMockBroker()}

	cfg := &TaskLoggerConfig{
		Enabled:              true,
		LogStatusUpdates:     true,
		LogArtifactEvents:    true,
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		QueueSize:            100,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "wiring-gw", slog.Default())
	return svc, brk
}

func TestTaskLoggerReceiveLoop_ProvisionCooldownDegradesThenRecovers(t *testing.T) {
	svc, brk := newControllableTaskLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	brk.setErr(broker.ErrProvisionCooldown)
	waitForCondition(t, 2*time.Second, func() bool { return len(svc.health.Snapshot()) > 0 },
		"brokerReceiveLoop never reported Degraded health for a provision cooldown — Health option not wired")
	for _, p := range svc.health.Snapshot() {
		if p.Severity == health.Fatal {
			t.Errorf("cooldown reported as Fatal, want Degraded (a retryable condition must never be terminal): %+v", p)
		}
	}

	brk.setErr(nil)
	waitForCondition(t, 2*time.Second, func() bool { return len(svc.health.Snapshot()) == 0 },
		"health never cleared after the cooldown lifted")
}

func TestTaskLoggerReceiveLoop_UnplannedQueueLossIsFatal(t *testing.T) {
	svc, brk := newControllableTaskLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	brk.setErr(broker.ErrQueueNotFound)
	waitForCondition(t, 2*time.Second, func() bool {
		for _, p := range svc.health.Snapshot() {
			if p.Severity == health.Fatal {
				return true
			}
		}
		return false
	}, "unplanned ErrQueueNotFound never reported as Fatal — Health option not wired")
}

func TestTaskLoggerStart_ClearsHealth(t *testing.T) {
	svc, _ := newControllableTaskLogger(t)

	svc.health.Set("queue:leftover", health.Fatal, "queue no longer exists")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	if problems := svc.health.Snapshot(); problems != nil {
		t.Errorf("health.Snapshot() after Start = %v, want nil — a restarted task logger must not carry a permanently orphaned problem from its previous run", problems)
	}
}
