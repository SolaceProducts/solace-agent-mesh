// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTierForMethod(t *testing.T) {
	tests := []struct {
		method   string
		wantTier tier
	}{
		{methodChatPostMessage, special},
		{methodChatUpdate, tier2},
		{methodFilesInfo, tier3},
		{methodConversationsList, tier2},
		{"unknown.method", tier3},
	}
	for _, tt := range tests {
		got := tierForMethod(tt.method)
		if got != tt.wantTier {
			t.Errorf("tierForMethod(%q) = %v, want %v", tt.method, got, tt.wantTier)
		}
	}
}

func TestRateLimiter_FirstCallInstant(t *testing.T) {
	rl := newRateLimiter()

	start := time.Now()
	if err := rl.throttle(t.Context(), methodChatUpdate); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("first throttle took %v, expected near-instant", elapsed)
	}
}

func TestRateLimiter_DifferentTiersIndependent(t *testing.T) {
	rl := newRateLimiter()
	if err := rl.throttle(t.Context(), methodChatUpdate); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	start := time.Now()
	if err := rl.throttle(t.Context(), methodChatPostMessage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("different tier throttle took %v, expected near-instant", elapsed)
	}
}

func TestRateLimiter_Disabled(t *testing.T) {
	rl := newRateLimiterDisabled()
	if err := rl.throttle(t.Context(), methodChatUpdate); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	start := time.Now()
	if err := rl.throttle(t.Context(), methodChatUpdate); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("disabled limiter throttle took %v, expected near-instant", elapsed)
	}
}

func TestRateLimiter_CancelledContext(t *testing.T) {
	rl := newRateLimiter()
	if err := rl.throttle(t.Context(), methodChatUpdate); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := rl.throttle(ctx, methodChatUpdate)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRateLimiter_CancelDuringWait(t *testing.T) {
	rl := newRateLimiter()
	if err := rl.throttle(t.Context(), methodChatPostMessage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	err := rl.throttle(ctx, methodChatPostMessage)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("throttle took %v, expected early exit on cancel", elapsed)
	}
}
