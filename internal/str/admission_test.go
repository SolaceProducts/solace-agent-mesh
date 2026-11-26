// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func acquireSlots(ctx context.Context, r *SandboxRunner) (*slotLease, *remotetool.InvokeError) {
	lease, err := r.acquireInFlight(ctx)
	if err != nil {
		return nil, err
	}
	if err := lease.acquireCompute(ctx); err != nil {
		lease.Release()
		return nil, err
	}
	return lease, nil
}

func acquireBlocks(t *testing.T, r *SandboxRunner) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	lease, err := acquireSlots(ctx, r)
	if err != nil {
		return true
	}
	lease.Release()
	return false
}

func TestSlotLease_YieldFreesComputeForOthers(t *testing.T) {
	r := &SandboxRunner{
		compute:     newComputeBudget(1),
		inflightSem: make(chan struct{}, 8),
	}

	lease, err := acquireSlots(t.Context(), r)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if !acquireBlocks(t, r) {
		t.Fatal("compute budget should be full before the yield")
	}

	lease.YieldCompute()

	other, err := acquireSlots(t.Context(), r)
	if err != nil {
		t.Fatalf("acquire should succeed after yield: %v", err)
	}

	rctx, rcancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer rcancel()
	lease.ResumeCompute(rctx)
	if lease.holdsCPU {
		t.Fatal("resume should block while the only compute slot is held by another tool")
	}

	other.Release()
	lease.ResumeCompute(t.Context())
	if !lease.holdsCPU {
		t.Fatal("resume should re-acquire the compute slot once it is free")
	}
	lease.Release()
}

func TestSlotLease_YieldKeepsInFlightSlot(t *testing.T) {
	r := &SandboxRunner{
		compute:     newComputeBudget(4),
		inflightSem: make(chan struct{}, 1),
	}

	lease, err := acquireSlots(t.Context(), r)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	lease.YieldCompute()

	if !acquireBlocks(t, r) {
		t.Fatal("in-flight slot must be retained during a yield (it bounds resident memory)")
	}

	lease.Release()
	if acquireBlocks(t, r) {
		t.Fatal("in-flight slot should free once the run completes")
	}
}

func TestAcquireSlots_InFlightBoundsExecutions(t *testing.T) {
	r := &SandboxRunner{
		compute:     newComputeBudget(8),
		inflightSem: make(chan struct{}, 1),
	}

	lease, err := acquireSlots(t.Context(), r)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !acquireBlocks(t, r) {
		t.Fatal("acquire should block once the in-flight ceiling is reached")
	}
	lease.Release()
}

func TestResolveInFlight(t *testing.T) {
	const (
		gb2 = 2 << 30
		gb8 = 8 << 30
	)
	cases := []struct {
		name            string
		memLimitBytes   int64
		perToolRSSBytes int64
		want            int
	}{
		{"auto from 2GiB keeps a fifth for the STR", gb2, 0, 6},
		{"auto from 8GiB reserves 512MiB", gb8, 0, 30},
		{"production pod 4700Mi", 4700 << 20, 0, 16},
		{"tiny pod still admits the compute floor", 128 << 20, 0, minComputeBudget},
		{"unknown memory falls back to at least the static default", 0, 0, max(fallbackMaxInFlight, defaultComputeBudget(runtime.GOMAXPROCS(0)))},
		{"smaller per-tool RSS allows more in flight", gb2, 128 << 20, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveInFlight(tc.memLimitBytes, tc.perToolRSSBytes)
			if got != tc.want {
				t.Errorf("resolveInFlight(mem=%d, rss=%d) = %d, want %d",
					tc.memLimitBytes, tc.perToolRSSBytes, got, tc.want)
			}
		})
	}
}

func TestDefaultComputeBudget(t *testing.T) {
	cases := []struct {
		gomaxprocs int
		want       int
	}{
		{1, 2},
		{2, 4},
		{3, 6},
		{8, 16},
	}
	for _, tc := range cases {
		if got := defaultComputeBudget(tc.gomaxprocs); got != tc.want {
			t.Errorf("defaultComputeBudget(%d) = %d, want %d", tc.gomaxprocs, got, tc.want)
		}
	}
}

func TestSandboxConfig_ComputeClampedToInFlight(t *testing.T) {
	c := SandboxConfig{MaxConcurrentExecutions: 64, MaxInFlight: 4}
	c.setDefaults()
	if c.MaxConcurrentExecutions != 4 {
		t.Errorf("compute budget = %d, want 4 (clamped to in-flight)", c.MaxConcurrentExecutions)
	}
	if c.MaxInFlight != 4 {
		t.Errorf("in-flight budget = %d, want 4 (pinned value untouched)", c.MaxInFlight)
	}
}

func TestSandboxConfig_DefaultsSizeFromCores(t *testing.T) {
	var c SandboxConfig
	c.setDefaults()
	if c.MaxConcurrentExecutions < 1 {
		t.Errorf("compute budget default = %d, want >= 1", c.MaxConcurrentExecutions)
	}
	if c.MaxInFlight < c.MaxConcurrentExecutions {
		t.Errorf("in-flight budget %d must be >= compute budget %d", c.MaxInFlight, c.MaxConcurrentExecutions)
	}
	if want := min(defaultComputeBudget(runtime.GOMAXPROCS(0)), c.MaxInFlight); c.MaxConcurrentExecutions != want {
		t.Errorf("compute budget default = %d, want %d (oversubscribed cores clamped to in-flight)", c.MaxConcurrentExecutions, want)
	}
}

func TestSlotLeaseRelease(t *testing.T) {
	r := NewSandboxRunner(SandboxConfig{MaxConcurrentExecutions: 1, MaxInFlight: 2, Mode: "direct"}, slog.Default())

	lease, ierr := acquireSlots(t.Context(), r)
	if ierr != nil {
		t.Fatalf("acquireSlots: %v", ierr)
	}
	if _, held, _ := r.compute.snapshot(); held != 1 || len(r.inflightSem) != 1 {
		t.Fatalf("after acquire: compute held=%d inflightSem=%d, want 1/1", held, len(r.inflightSem))
	}

	lease.Release()
	if _, held, _ := r.compute.snapshot(); held != 0 || len(r.inflightSem) != 0 {
		t.Fatalf("after release: compute held=%d inflightSem=%d, want 0/0", held, len(r.inflightSem))
	}

	lease.Release()
	if _, held, _ := r.compute.snapshot(); held != 0 || len(r.inflightSem) != 0 {
		t.Fatalf("after double release: compute held=%d inflightSem=%d, want 0/0", held, len(r.inflightSem))
	}
}

func TestSlotLease_ResumeAfterReleaseGivesSlotBack(t *testing.T) {
	r := &SandboxRunner{
		compute:     newComputeBudget(1),
		inflightSem: make(chan struct{}, 8),
	}

	leaseA, err := acquireSlots(t.Context(), r)
	if err != nil {
		t.Fatalf("acquire leaseA: %v", err)
	}

	leaseB, ierr := r.acquireInFlight(t.Context())
	if ierr != nil {
		t.Fatalf("acquire leaseB in-flight: %v", ierr)
	}

	got := make(chan bool, 1)
	go func() { got <- leaseB.ResumeCompute(t.Context()) }()
	waitForComputeWaiters(t, r, 1)

	leaseB.Release()

	leaseA.Release()

	select {
	case resumed := <-got:
		if resumed {
			t.Fatal("ResumeCompute should report false: the lease was released while parked")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ResumeCompute never returned after the slot freed")
	}

	if _, held, _ := r.compute.snapshot(); held != 0 {
		t.Fatalf("compute held = %d after the race, want 0 (won slot handed back)", held)
	}
}
