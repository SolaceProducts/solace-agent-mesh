// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"log/slog"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestComputeBudget_BoundsAndReleases(t *testing.T) {
	b := newComputeBudget(2)
	for i := 0; i < 2; i++ {
		if err := b.acquire(t.Context()); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := b.acquire(ctx); err == nil {
		t.Fatal("third acquire should block until the budget frees")
	}
	b.release()
	if err := b.acquire(t.Context()); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if limit, held, waiting := b.snapshot(); limit != 2 || held != 2 || waiting != 0 {
		t.Fatalf("snapshot = (%d, %d, %d), want (2, 2, 0)", limit, held, waiting)
	}
}

func TestComputeBudget_GrowWakesWaiter(t *testing.T) {
	b := newComputeBudget(1)
	if err := b.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := b.acquire(t.Context()); err == nil {
			admitted.Store(true)
		}
	}()
	waitForWaiting(t, b, 1)
	b.setLimit(2)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter was not admitted after the budget grew")
	}
	if !admitted.Load() {
		t.Fatal("waiter should have been admitted")
	}
}

func TestComputeBudget_ShrinkNeverPreemptsButBlocksNewAdmissions(t *testing.T) {
	b := newComputeBudget(3)
	for i := 0; i < 3; i++ {
		if err := b.acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	b.setLimit(1)
	if _, held, _ := b.snapshot(); held != 3 {
		t.Fatalf("held = %d after shrink, want 3 (running tools are never preempted)", held)
	}
	b.release()
	b.release()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := b.acquire(ctx); err == nil {
		t.Fatal("acquire should block: one tool still holds the single remaining slot")
	}
	b.release()
	if err := b.acquire(t.Context()); err != nil {
		t.Fatalf("acquire once held drops to the new limit: %v", err)
	}
}

func TestComputeBudget_CancelWhileWaitingClearsWaiter(t *testing.T) {
	b := newComputeBudget(1)
	if err := b.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- b.acquire(ctx) }()
	waitForWaiting(t, b, 1)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("acquire should fail once ctx is cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled acquire did not return")
	}
	if _, held, waiting := b.snapshot(); held != 1 || waiting != 0 {
		t.Fatalf("snapshot after cancel = held %d waiting %d, want 1/0", held, waiting)
	}
}

func waitForWaiting(t *testing.T, b *computeBudget, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, waiting := b.snapshot(); waiting == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiting count never reached %d", want)
}

func TestParseCPUStat(t *testing.T) {
	cases := []struct {
		name     string
		contents string
		want     cpuThrottleSample
		wantOK   bool
	}{
		{"cgroup v2", "usage_usec 1234\nuser_usec 1000\nsystem_usec 234\nnr_periods 120\nnr_throttled 30\nthrottled_usec 500000\n", cpuThrottleSample{120, 30}, true},
		{"cgroup v1", "nr_periods 7\nnr_throttled 0\nthrottled_time 0\n", cpuThrottleSample{7, 0}, true},
		{"no quota", "usage_usec 99\nnr_periods 0\nnr_throttled 0\n", cpuThrottleSample{0, 0}, true},
		{"missing nr_periods", "usage_usec 99\n", cpuThrottleSample{}, false},
		{"garbage", "not a cpu.stat", cpuThrottleSample{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseCPUStat([]byte(tc.contents))
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("parseCPUStat = (%+v, %v), want (%+v, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestThrottledRatio(t *testing.T) {
	cases := []struct {
		name      string
		prev, cur cpuThrottleSample
		want      float64
	}{
		{"half the periods throttled", cpuThrottleSample{100, 10}, cpuThrottleSample{110, 15}, 0.5},
		{"no throttling", cpuThrottleSample{100, 10}, cpuThrottleSample{110, 10}, 0},
		{"no quota: no periods elapsed", cpuThrottleSample{0, 0}, cpuThrottleSample{0, 0}, 0},
		{"counter reset", cpuThrottleSample{100, 50}, cpuThrottleSample{5, 1}, 0},
	}
	for _, tc := range cases {
		if got := throttledRatio(tc.prev, tc.cur); got != tc.want {
			t.Errorf("%s: throttledRatio = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNextComputeBudget(t *testing.T) {
	cases := []struct {
		name                      string
		cur, floor, ceil, waiting int
		throttled                 float64
		sustained, calm           bool
		want                      int
	}{
		{"idle pod, nobody waiting: hold", 8, 1, 23, 0, 0, false, true, 8},
		{"lightly throttled, tools queued: grow by one", 8, 1, 23, 3, 0.01, false, false, 9},
		{"first unthrottled window, tools queued: still grow by one", 8, 1, 23, 3, 0, false, false, 9},
		{"calm for two windows, three tools queued: grow by three", 8, 1, 23, 3, 0, false, true, 11},
		{"calm, many tools queued: at most double", 2, 1, 23, 6, 0, false, true, 4},
		{"grow stops at the in-flight ceiling", 23, 1, 23, 3, 0, false, true, 23},
		{"mild throttling with demand: hold", 8, 1, 23, 3, 0.1, false, false, 8},
		{"one throttled window (import burst): hold", 16, 1, 23, 0, 0.9, false, false, 16},
		{"sustained throttling: back off a quarter", 16, 1, 23, 0, 0.5, true, false, 12},
		{"sustained throttling on a small budget: back off one", 3, 1, 23, 0, 0.9, true, false, 2},
		{"never below the core floor", 2, 2, 23, 0, 1.0, true, false, 2},
		{"cur above ceil is pulled back", 40, 1, 23, 0, 0, false, true, 23},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextComputeBudget(tc.cur, tc.floor, tc.ceil, tc.waiting, tc.throttled, tc.sustained, tc.calm)
			if got != tc.want {
				t.Errorf("nextComputeBudget(%d, %d, %d, %d, %v, %v, %v) = %d, want %d",
					tc.cur, tc.floor, tc.ceil, tc.waiting, tc.throttled, tc.sustained, tc.calm, got, tc.want)
			}
		})
	}
}

type fakeThrottle struct {
	mu      chan struct{}
	samples []cpuThrottleSample
	i       int
}

func (f *fakeThrottle) read() (cpuThrottleSample, bool) {
	f.mu <- struct{}{}
	defer func() { <-f.mu }()
	if f.i < len(f.samples) {
		s := f.samples[f.i]
		f.i++
		return s, true
	}
	return f.samples[len(f.samples)-1], true
}

func newFakeThrottle(samples ...cpuThrottleSample) *fakeThrottle {
	return &fakeThrottle{mu: make(chan struct{}, 1), samples: samples}
}

func newControllerRunner(t *testing.T, initial, inflight int, read func() (cpuThrottleSample, bool)) *SandboxRunner {
	t.Helper()
	r := NewSandboxRunner(SandboxConfig{MaxConcurrentExecutions: initial, MaxInFlight: inflight, Mode: "direct"}, slog.Default())
	r.cpuThrottle = read
	return r
}

func TestAdaptiveComputeBudget_GrowsWhileIdleWithDemand(t *testing.T) {
	var samples []cpuThrottleSample
	for i := uint64(0); i < 100; i++ {
		samples = append(samples, cpuThrottleSample{periods: i * 10})
	}
	r := newControllerRunner(t, 1, 4, newFakeThrottle(samples...).read)

	lease, ierr := acquireSlots(t.Context(), r)
	if ierr != nil {
		t.Fatal(ierr)
	}
	defer lease.Release()
	waiterCtx, waiterCancel := context.WithCancel(t.Context())
	defer waiterCancel()
	go func() {
		if l, err := acquireSlots(waiterCtx, r); err == nil {
			l.Release()
		}
	}()
	waitForWaiting(t, r.compute, 1)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.runAdaptiveComputeBudgetEvery(ctx, 5*time.Millisecond)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if limit, _, _ := r.compute.snapshot(); limit >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if limit, _, _ := r.compute.snapshot(); limit < 2 {
		t.Fatalf("compute budget = %d, want it to grow past 1 with a queued tool and no throttling", limit)
	}
}

func TestAdaptiveComputeBudget_ShrinksUnderThrottling(t *testing.T) {
	var samples []cpuThrottleSample
	for i := uint64(0); i < 100; i++ {
		samples = append(samples, cpuThrottleSample{periods: i * 10, throttled: i * 10})
	}
	r := newControllerRunner(t, 32, 32, newFakeThrottle(samples...).read)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.runAdaptiveComputeBudgetEvery(ctx, 5*time.Millisecond)
	}()

	floor := defaultComputeBudget(runtime.GOMAXPROCS(0))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if limit, _, _ := r.compute.snapshot(); limit <= floor {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if limit, _, _ := r.compute.snapshot(); limit > floor {
		t.Fatalf("compute budget = %d under full throttling, want <= floor %d", limit, floor)
	}
}

func TestAdaptiveComputeBudget_SingleBurstDoesNotShrink(t *testing.T) {
	samples := []cpuThrottleSample{{0, 0}, {10, 10}}
	for i := uint64(2); i < 100; i++ {
		samples = append(samples, cpuThrottleSample{periods: i * 10, throttled: 10})
	}
	initial := defaultComputeBudget(runtime.GOMAXPROCS(0)) + 4
	r := newControllerRunner(t, initial, initial*4, newFakeThrottle(samples...).read)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	r.runAdaptiveComputeBudgetEvery(ctx, 5*time.Millisecond)
	if limit, _, _ := r.compute.snapshot(); limit != initial {
		t.Fatalf("compute budget = %d after a single throttled window, want %d (unchanged)", limit, initial)
	}
}

func TestAdaptiveComputeBudget_DisabledWithoutCgroup(t *testing.T) {
	r := newControllerRunner(t, 8, 16, func() (cpuThrottleSample, bool) { return cpuThrottleSample{}, false })
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.runAdaptiveComputeBudgetEvery(t.Context(), time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("controller should return immediately without cgroup cpu accounting")
	}
	if limit, _, _ := r.compute.snapshot(); limit != 8 {
		t.Fatalf("compute budget = %d, want the static default 8 left in place", limit)
	}
}

func TestAdaptiveComputeBudget_HoldsWithoutCPUQuota(t *testing.T) {
	var samples []cpuThrottleSample
	for i := 0; i < 100; i++ {
		samples = append(samples, cpuThrottleSample{periods: 0, throttled: 0})
	}
	r := newControllerRunner(t, 1, 4, newFakeThrottle(samples...).read)

	lease, ierr := acquireSlots(t.Context(), r)
	if ierr != nil {
		t.Fatal(ierr)
	}
	defer lease.Release()
	waiterCtx, waiterCancel := context.WithCancel(t.Context())
	defer waiterCancel()
	go func() {
		if l, err := acquireSlots(waiterCtx, r); err == nil {
			l.Release()
		}
	}()
	waitForWaiting(t, r.compute, 1)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	r.runAdaptiveComputeBudgetEvery(ctx, 5*time.Millisecond)

	if limit, _, _ := r.compute.snapshot(); limit != 1 {
		t.Fatalf("compute budget = %d without a CPU quota, want the initial 1 held", limit)
	}
}

func TestAdaptiveComputeBudget_KeepsExplicitBudgetBelowCoreFloor(t *testing.T) {
	var samples []cpuThrottleSample
	for i := uint64(0); i < 100; i++ {
		samples = append(samples, cpuThrottleSample{periods: i * 10})
	}
	r := newControllerRunner(t, 1, 4, newFakeThrottle(samples...).read)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	r.runAdaptiveComputeBudgetEvery(ctx, 5*time.Millisecond)
	if limit, _, _ := r.compute.snapshot(); limit != 1 {
		t.Fatalf("compute budget = %d with an explicit budget of 1 and no demand, want 1", limit)
	}
}
