// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type startGatedInstance struct {
	*mockInstance
	started chan struct{}
	release chan struct{}
}

func (g *startGatedInstance) Start(ctx context.Context) error {
	close(g.started)
	<-g.release
	return g.mockInstance.Start(ctx)
}

func TestStopWaitsForAnInFlightAddAndStart(t *testing.T) {
	gated := &startGatedInstance{
		mockInstance: newMockInstance("deploying", "agent"),
		started:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseStart := func() { releaseOnce.Do(func() { close(gated.release) }) }
	t.Cleanup(releaseStart)
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(config.Config) (Instance, error) { return gated, nil })

	added := make(chan error, 1)
	go func() {
		_, err := exe.AddAndStart(t.Context(), config.FromMap(map[string]any{"kind": "agent", "name": "deploying"}))
		added <- err
	}()
	<-gated.started

	stopped := make(chan error, 1)
	go func() { stopped <- exe.Stop(t.Context()) }()

	select {
	case <-stopped:
		t.Fatal("Stop returned while the instance was still inside Start")
	case <-time.After(100 * time.Millisecond):
	}
	if n := gated.stopCalled.Load(); n != 0 {
		t.Fatalf("Stop called %d times while Start was still running, want 0", n)
	}

	releaseStart()
	if err := <-added; err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}
	if err := <-stopped; err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n := gated.stopCalled.Load(); n != 1 {
		t.Errorf("Stop called %d times after Start finished, want 1", n)
	}
}

func TestAddAndStartIsRefusedOnceStopHasBegun(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})
	if err := exe.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	_, err := exe.AddAndStart(t.Context(), config.FromMap(map[string]any{"kind": "agent", "name": "late"}))
	if !errors.Is(err, ErrExecutorStopping) {
		t.Fatalf("AddAndStart after Stop: err = %v, want ErrExecutorStopping", err)
	}
	if len(exe.Instances()) != 0 {
		t.Errorf("instances after a refused add = %d, want 0", len(exe.Instances()))
	}
}

func TestStartInstanceIsRefusedOnceStopHasBegun(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("toggled", "agent")
	exe.AddInstance(m)
	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()

	if err := exe.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := exe.StartInstance(t.Context(), "toggled"); !errors.Is(err, ErrExecutorStopping) {
		t.Fatalf("StartInstance after Stop: err = %v, want ErrExecutorStopping", err)
	}
	if n := m.startCalled.Load(); n != 0 {
		t.Errorf("Start called %d times after Stop began, want 0", n)
	}
}

func TestStopGivesUpOnAnInFlightStartAtItsDeadline(t *testing.T) {
	gated := &startGatedInstance{
		mockInstance: newMockInstance("hung", "agent"),
		started:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	t.Cleanup(func() { close(gated.release) })
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(config.Config) (Instance, error) { return gated, nil })

	go func() {
		_, _ = exe.AddAndStart(t.Context(), config.FromMap(map[string]any{"kind": "agent", "name": "hung"}))
	}()
	<-gated.started

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- exe.Stop(ctx) }()

	select {
	case err := <-stopped:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Stop past its deadline: err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop kept waiting for the hung Start after its deadline")
	}
}
