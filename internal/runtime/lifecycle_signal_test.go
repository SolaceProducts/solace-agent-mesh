// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package runtime

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type blockingInitComponent struct {
	mockComponent
	entered   chan struct{}
	cancelled atomic.Bool
}

func (c *blockingInitComponent) Init(ctx context.Context, _ config.Config) error {
	close(c.entered)
	<-ctx.Done()
	c.cancelled.Store(true)
	return ctx.Err()
}

func TestRuntimeRunSignalDuringInitCancelsStartup(t *testing.T) {
	rt := newTestRuntime()
	comp := &blockingInitComponent{entered: make(chan struct{})}
	rt.Register("blocking", comp)
	runErr := make(chan error, 1)
	go func() { runErr <- rt.Run(t.Context()) }()
	select {
	case <-comp.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Init never started")
	}

	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := self.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runErr:
		if err == nil || !strings.Contains(err.Error(), "startup cancelled") {
			t.Fatalf("Run returned %v; want the cancelled startup reported", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after SIGTERM during Init")
	}
	if !comp.cancelled.Load() {
		t.Fatal("the component's Init context was not cancelled")
	}
}

type blockingStartComponent struct {
	mockComponent
	entered chan struct{}
}

func (c *blockingStartComponent) Start(ctx context.Context) error {
	close(c.entered)
	<-ctx.Done()
	return ctx.Err()
}

type stopRecordingComponent struct {
	mockComponent
	stops         atomic.Int32
	cancelledStop atomic.Bool
}

func (c *stopRecordingComponent) Stop(ctx context.Context) error {
	c.stops.Add(1)
	if ctx.Err() != nil {
		c.cancelledStop.Store(true)
	}
	return nil
}

func TestRuntimeRunSignalDuringStartStopsStartedComponentsOnce(t *testing.T) {
	rt := newTestRuntime()
	started := &stopRecordingComponent{}
	blocking := &blockingStartComponent{entered: make(chan struct{})}
	rt.Register("started", started)
	rt.Register("blocking", blocking)
	runErr := make(chan error, 1)
	go func() { runErr <- rt.Run(t.Context()) }()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Start never reached the blocking component")
	}

	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := self.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runErr:
		if err == nil || !strings.Contains(err.Error(), "startup cancelled") {
			t.Fatalf("Run returned %v; want the cancelled startup reported", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after SIGTERM during Start")
	}
	if got := started.stops.Load(); got != 1 {
		t.Fatalf("started component stopped %d times; want once", got)
	}
	if started.cancelledStop.Load() {
		t.Fatal("started component was stopped with an already-cancelled context, leaving it no grace period")
	}
}

type stuckInitComponent struct {
	mockComponent
	entered chan struct{}
	release chan struct{}
}

func (c *stuckInitComponent) Init(context.Context, config.Config) error {
	close(c.entered)
	<-c.release
	return nil
}

func TestRuntimeRunSignalDuringAStuckStartupExitsAfterTheAbortTimeout(t *testing.T) {
	rt := newTestRuntime()
	rt.startupAbortTimeout = 200 * time.Millisecond
	comp := &stuckInitComponent{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(comp.release) })
	rt.Register("stuck", comp)
	runErr := make(chan error, 1)
	go func() { runErr <- rt.Run(t.Context()) }()
	select {
	case <-comp.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Init never started")
	}

	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := self.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runErr:
		if err == nil || !strings.Contains(err.Error(), "did not unwind") {
			t.Fatalf("Run returned %v; want the startup reported as not unwinding", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after SIGTERM during a startup that ignores cancellation")
	}
}

func TestRuntimeRunContextCancelledDuringInitCancelsStartup(t *testing.T) {
	rt := newTestRuntime()
	comp := &blockingInitComponent{entered: make(chan struct{})}
	rt.Register("blocking", comp)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- rt.Run(ctx) }()
	select {
	case <-comp.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Init never started")
	}

	cancel()
	select {
	case err := <-runErr:
		if err == nil || !strings.Contains(err.Error(), "startup cancelled") {
			t.Fatalf("Run returned %v; want the cancelled startup reported", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context was cancelled during Init")
	}
	if !comp.cancelled.Load() {
		t.Fatal("the component's Init context was not cancelled")
	}
}
