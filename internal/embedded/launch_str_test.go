// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sandbox"
)

type fakeComponent struct {
	stopped bool
	stopErr error
}

func (f *fakeComponent) Init(context.Context, config.Config) error { return nil }
func (f *fakeComponent) Start(context.Context) error               { return nil }
func (f *fakeComponent) Stop(context.Context) error                { f.stopped = true; return f.stopErr }
func (f *fakeComponent) Health() error                             { return nil }

func TestLaunchContainerSTRWorkersIfConfigured_PropagatesLaunchError(t *testing.T) {
	inst := newTestInstance(Config{})

	inst.sandboxOrch = &sandbox.Orchestrator{}

	sentinel := errors.New("simulated launch failure")
	var calledWith []samapp.AppEntry
	inst.launchContainerSTRWorkersFn = func(_ context.Context, entries []samapp.AppEntry) error {
		calledWith = entries
		return sentinel
	}

	entries := []samapp.AppEntry{{Name: "str-test"}}
	err := inst.launchContainerSTRWorkersIfConfigured(t.Context(), entries)
	if err == nil {
		t.Fatal("expected non-nil error when launch fails, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected error to wrap sentinel, got: %v", err)
	}
	if !strings.Contains(err.Error(), "launch container STR workers") {
		t.Errorf("expected wrapped message %q, got: %v", "launch container STR workers", err)
	}
	if len(calledWith) != 1 || calledWith[0].Name != "str-test" {
		t.Errorf("expected hook to receive STR entries, got %+v", calledWith)
	}
}

func TestLaunchContainerSTRWorkersIfConfigured_NoopWhenNoOrchestrator(t *testing.T) {
	inst := newTestInstance(Config{})
	inst.sandboxOrch = nil

	called := false
	inst.launchContainerSTRWorkersFn = func(_ context.Context, _ []samapp.AppEntry) error {
		called = true
		return errors.New("must not be called")
	}

	if err := inst.launchContainerSTRWorkersIfConfigured(t.Context(), nil); err != nil {
		t.Fatalf("expected nil error when sandboxOrch is nil, got: %v", err)
	}
	if called {
		t.Error("hook must not be called when sandboxOrch is nil")
	}
}

func TestSetupContainerSTR_DoesNotLeakOrchestratorOnFailure(t *testing.T) {
	inst := newTestInstance(Config{
		ContainerRuntime: &sandbox.Runtime{Name: "docker", Path: "/dev/null", Version: "test"},
		SandboxDir:       "/nonexistent/sandbox/dir",
	})

	err := inst.setupContainerSTR(t.Context(), nil)
	if err == nil {
		t.Fatal("expected setupContainerSTR to fail with stub runtime + non-existent sandbox dir")
	}
	if inst.sandboxOrch != nil {
		t.Error("expected inst.sandboxOrch to remain nil after setupContainerSTR failure")
	}
}

func TestStop_RunsCleanupEvenWhenCtxFiresBeforeDone(t *testing.T) {
	inst := newTestInstance(Config{})

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	inst.sharedBroker = brk

	inst.done = make(chan struct{})

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	stopErr := inst.Stop(stopCtx)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("Stop took %v — expected <1s when shutdown-listener goroutine was never spawned", elapsed)
	}

	if stopErr != nil {
		t.Errorf("expected nil error when wait-on-done is skipped, got: %v", stopErr)
	}

	if brk.IsConnected() {
		t.Error("expected sharedBroker.Disconnect to have run during Stop cleanup phase")
	}
}

func TestStop_DrivesRtStopWhenGoroutineNotSpawned(t *testing.T) {
	inst := newTestInstance(Config{HealthAddr: "127.0.0.1:0"})
	setupMinimalRuntime(t, inst)

	fc := &fakeComponent{stopErr: errors.New("simulated component stop error")}
	inst.rt.Register("fake", fc)

	inst.startHealthServer()
	if inst.healthServer == nil {
		t.Fatal("healthServer should be set after startHealthServer with a usable addr")
	}

	inst.sandboxOrch = &sandbox.Orchestrator{}

	if err := inst.rt.Init(t.Context()); err != nil {
		t.Fatalf("rt.Init: %v", err)
	}
	if err := inst.rt.Start(t.Context()); err != nil {
		t.Fatalf("rt.Start: %v", err)
	}

	if inst.runtimeStopperSpawned {
		t.Fatal("test setup invalid: runtimeStopperSpawned should be false")
	}

	if err := inst.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if !fc.stopped {
		t.Error("expected rt.Stop to be driven from Stop's cleanup block when runtimeStopperSpawned is false")
	}
}

func TestStop_AfterFailedStart_DoesNotDeadlock(t *testing.T) {
	inst := newTestInstance(Config{})
	setupMinimalRuntime(t, inst)

	runCtx, cancel := context.WithCancel(t.Context())
	inst.cancel = cancel
	inst.done = make(chan struct{})

	if err := inst.rt.Init(runCtx); err != nil {
		t.Fatalf("rt.Init: %v", err)
	}
	if err := inst.rt.Start(runCtx); err != nil {
		t.Fatalf("rt.Start: %v", err)
	}

	inst.runtimeStopperSpawned = true
	go func() {
		defer close(inst.done)
		<-runCtx.Done()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := inst.rt.Stop(stopCtx); err != nil {
			inst.logger.Warn("runtime stop error", "error", err)
		}
	}()

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- inst.Stop(context.Background())
	}()

	select {
	case err := <-stopDone:
		_ = err
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return within 10s — likely deadlock (cleanup goroutine pattern broken)")
	}
}

func TestStop_PropagatesCtxErrAndCleansUpWhenWaitTimesOut(t *testing.T) {
	inst := newTestInstance(Config{})

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	inst.sharedBroker = brk

	inst.runtimeStopperSpawned = true
	inst.done = make(chan struct{})

	stopCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	stopErr := inst.Stop(stopCtx)

	if stopErr == nil {
		t.Error("expected Stop to return ctx.Err() when wait-on-done times out, got nil")
	}

	if brk.IsConnected() {
		t.Error("expected sharedBroker.Disconnect to have run via cleanupCtx fallback")
	}
}

func TestLaunchContainerSTRWorkersIfConfigured_NilOnSuccess(t *testing.T) {
	inst := newTestInstance(Config{})
	inst.sandboxOrch = &sandbox.Orchestrator{}
	inst.launchContainerSTRWorkersFn = func(_ context.Context, _ []samapp.AppEntry) error {
		return nil
	}
	if err := inst.launchContainerSTRWorkersIfConfigured(t.Context(), nil); err != nil {
		t.Errorf("expected nil error on successful launch, got: %v", err)
	}
}

func TestPortCheckCommandFor(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{goos: "windows", want: "'netstat -ano | findstr 127.0.0.1:8090'"},
		{goos: "linux", want: "'lsof -i 127.0.0.1:8090'"},
		{goos: "darwin", want: "'lsof -i 127.0.0.1:8090'"},
	}
	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			got := portCheckCommandFor(tc.goos, "127.0.0.1:8090")
			if got != tc.want {
				t.Errorf("portCheckCommandFor(%q, ...) = %q, want %q", tc.goos, got, tc.want)
			}
		})
	}
}
