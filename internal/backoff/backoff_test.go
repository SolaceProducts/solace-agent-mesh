// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package backoff

import (
	"context"
	"testing"
	"time"
)

func TestExponential_DoublesUntilMax(t *testing.T) {
	t.Parallel()
	b := Exponential{Initial: 10 * time.Millisecond, Max: 40 * time.Millisecond}

	want := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond,
	}
	for i, w := range want {
		if got := b.Current(); got != w {
			t.Fatalf("iter %d: Current() = %v, want %v", i, got, w)
		}
		if err := b.Wait(context.Background()); err != nil {
			t.Fatalf("iter %d: Wait returned %v", i, err)
		}
	}
}

func TestExponential_NextDoublesUntilMaxWithoutSleeping(t *testing.T) {
	t.Parallel()
	b := Exponential{Initial: 10 * time.Millisecond, Max: 40 * time.Millisecond}

	want := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		40 * time.Millisecond,
	}
	start := time.Now()
	for i, w := range want {
		if got := b.Next(); got != w {
			t.Fatalf("iter %d: Next() = %v, want %v", i, got, w)
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Next() appears to sleep: %d calls took %v", len(want), elapsed)
	}

	b.Reset()
	if got := b.Next(); got != b.Initial {
		t.Fatalf("after Reset, Next() = %v, want %v", got, b.Initial)
	}
}

func TestExponential_NextJitterStaysWithinBounds(t *testing.T) {
	t.Parallel()
	b := Exponential{Initial: 100 * time.Millisecond, Max: 100 * time.Millisecond, Jitter: 0.2}
	for i := range 50 {
		got := b.Next()
		if got < 80*time.Millisecond || got > 120*time.Millisecond {
			t.Fatalf("iter %d: Next() = %v, want within [80ms,120ms]", i, got)
		}
	}
}

func TestExponential_ResetReturnsToInitial(t *testing.T) {
	t.Parallel()
	b := Exponential{Initial: 5 * time.Millisecond, Max: 100 * time.Millisecond}

	for range 3 {
		if err := b.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if b.Current() == b.Initial {
		t.Fatal("expected backoff to have advanced past Initial")
	}

	b.Reset()
	if got := b.Current(); got != b.Initial {
		t.Fatalf("after Reset, Current = %v, want %v", got, b.Initial)
	}
}

func TestExponential_CancelsOnContextDone(t *testing.T) {
	t.Parallel()
	b := Exponential{Initial: 5 * time.Second, Max: 10 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := b.Wait(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error on context cancel")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Wait took %v; expected to return promptly on cancel", elapsed)
	}
}

func TestExponential_RateLimits_RegressionFor395(t *testing.T) {
	t.Parallel()
	b := Exponential{Initial: 20 * time.Millisecond, Max: 200 * time.Millisecond}

	const iters = 5
	start := time.Now()
	for range iters {
		if err := b.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)

	minExpected := 400 * time.Millisecond
	if elapsed < minExpected {
		t.Fatalf("%d Wait() calls completed in %v; expected at least %v (loop is spinning)",
			iters, elapsed, minExpected)
	}
}

func TestExponential_JitterStaysWithinBounds(t *testing.T) {
	t.Parallel()
	b := Exponential{Initial: 10 * time.Millisecond, Max: 10 * time.Millisecond, Jitter: 0.5}

	for range 5 {
		start := time.Now()
		if err := b.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		if elapsed > 50*time.Millisecond {
			t.Fatalf("Wait took %v; jitter should have capped it near 15ms", elapsed)
		}
	}
}
