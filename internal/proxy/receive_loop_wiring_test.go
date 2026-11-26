// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
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

func newControllableBrokerProxy(t *testing.T) (*ProxyInstance, *controllableReceiveBroker) {
	t.Helper()
	pc := &ProxyConfig{Name: "wiring-proxy", Namespace: "test-ns"}
	p := newTestProxy(pc)
	p.cfg.DiscoveryIntervalSeconds = 0
	p.cfg.TaskCleanupIntervalMinutes = 0

	dev := broker.NewDevBroker()
	if err := dev.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	brk := &controllableReceiveBroker{Broker: dev}
	p.svc = &mockServices{brk: brk}
	p.adapter = &mockAdapter{card: &a2a.AgentCard{Name: "downstream", Description: "test agent"}}
	return p, brk
}

func TestProxyReceiveLoop_ProvisionCooldownDegradesThenRecovers(t *testing.T) {
	p, brk := newControllableBrokerProxy(t)
	if err := p.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup

	brk.setErr(broker.ErrProvisionCooldown)
	waitForCondition(t, 2*time.Second, func() bool { return len(p.health.Snapshot()) > 0 },
		"receiveLoop never reported Degraded health for a provision cooldown — Health option not wired")
	for _, prob := range p.health.Snapshot() {
		if prob.Severity == health.Fatal {
			t.Errorf("cooldown reported as Fatal, want Degraded (a retryable condition must never be terminal): %+v", prob)
		}
	}

	brk.setErr(nil)
	waitForCondition(t, 2*time.Second, func() bool { return len(p.health.Snapshot()) == 0 },
		"health never cleared after the cooldown lifted")
}

func TestProxyReceiveLoop_UnplannedQueueLossIsFatal(t *testing.T) {
	p, brk := newControllableBrokerProxy(t)
	if err := p.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-p.started
	defer p.cancelFn()

	brk.setErr(broker.ErrQueueNotFound)
	waitForCondition(t, 2*time.Second, func() bool {
		for _, prob := range p.health.Snapshot() {
			if prob.Severity == health.Fatal {
				return true
			}
		}
		return false
	}, "unplanned ErrQueueNotFound never reported as Fatal — Health option not wired")
}
