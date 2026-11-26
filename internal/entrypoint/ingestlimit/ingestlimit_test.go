// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package ingestlimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

const refusalWait = 50 * time.Millisecond

func mustAcquire(t *testing.T, b *Budget, n int64) *Hold {
	t.Helper()
	h, err := b.Acquire(t.Context(), n)
	if err != nil {
		t.Fatalf("Acquire(%d) = %v, want the bytes", n, err)
	}
	return h
}

func requireRefused(t *testing.T, b *Budget, n int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), refusalWait)
	defer cancel()
	if h, err := b.Acquire(ctx, n); err == nil {
		h.Release()
		t.Fatalf("Acquire(%d) was granted, want it to wait out its context", n)
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire(%d) = %v, want the context's deadline", n, err)
	}
}

func TestAcquireWaitsForReleasedBytes(t *testing.T) {
	b := New(100)
	first := mustAcquire(t, b, 70)
	requireRefused(t, b, 31)

	granted := make(chan *Hold)
	go func() {
		h, err := b.Acquire(context.Background(), 31)
		if err != nil {
			t.Errorf("Acquire after the release = %v", err)
		}
		granted <- h
	}()
	first.Release()
	select {
	case h := <-granted:
		h.Release()
	case <-time.After(5 * time.Second):
		t.Fatal("a waiter was not granted the bytes another hold released")
	}
}

func TestAcquireOverCapacityFailsAtOnce(t *testing.T) {
	b := New(100)
	start := time.Now()
	if _, err := b.Acquire(context.Background(), 101); !errors.Is(err, ErrOverCapacity) {
		t.Fatalf("Acquire(101) = %v, want ErrOverCapacity", err)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("Acquire over capacity waited %v, want an immediate refusal", waited)
	}
}

func TestShrinkGivesBackTheExcess(t *testing.T) {
	b := New(100)
	h := mustAcquire(t, b, 100)
	h.Shrink(40)
	requireRefused(t, b, 61)
	mustAcquire(t, b, 60).Release()
	h.Release()
	mustAcquire(t, b, 100).Release()
}

func TestShrinkNeverGrowsTheHold(t *testing.T) {
	b := New(100)
	h := mustAcquire(t, b, 40)
	h.Shrink(80)
	requireRefused(t, b, 61)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	rest, err := b.Acquire(ctx, 60)
	if err != nil {
		t.Fatalf("Acquire(60) beside a 40-byte hold shrunk to 80 = %v, want the 60 still free", err)
	}
	rest.Release()
	h.Release()
	mustAcquire(t, b, 100).Release()
}

func TestReleaseTwiceGivesBackOnce(t *testing.T) {
	b := New(100)
	h := mustAcquire(t, b, 60)
	h.Release()
	h.Release()
	full := mustAcquire(t, b, 100)
	requireRefused(t, b, 1)
	full.Release()
}

func TestInboundIsOneSharedBudget(t *testing.T) {
	if got := Inbound().Capacity(); got != InboundCapacity {
		t.Errorf("Inbound capacity = %d, want %d", got, InboundCapacity)
	}
	all := mustAcquire(t, Inbound(), InboundCapacity)
	requireRefused(t, Inbound(), 1)
	all.Release()
}
