// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"testing"
	"time"
)

func (h *TestHarness) AssertSSEEventBufferState(t *testing.T) {
	t.Helper()

	expected := h.scenario.ExpectedSSEEventBufferState
	if expected == nil {
		return
	}

	httpGW, ok := h.gateway.(*HTTPGateway)
	if !ok {
		t.Error("expected_sse_event_buffer_state requires go-http gateway mode")
		return
	}

	timeout := 10 * time.Second
	if expected.PollTimeoutSeconds > 0 {
		timeout = time.Duration(expected.PollTimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout+5*time.Second)
	defer cancel()

	if err := httpGW.AgeSSEEventBufferRows(ctx, 0); err != nil {
		t.Errorf("expected_sse_event_buffer_state: age rows failed: %v", err)
		return
	}

	if expected.DelaySeconds > 0 {
		select {
		case <-ctx.Done():
			t.Errorf("expected_sse_event_buffer_state: context cancelled during initial delay: %v", ctx.Err())
			return
		case <-time.After(time.Duration(expected.DelaySeconds) * time.Second):
		}
	}

	deadline := time.Now().Add(timeout)
	var lastCount int
	for time.Now().Before(deadline) {
		count, err := httpGW.CountSSEEventBufferRows(ctx)
		if err != nil {
			t.Errorf("expected_sse_event_buffer_state: count query failed: %v", err)
			return
		}
		lastCount = count
		if count == expected.ExpectedCount {
			return
		}
		select {
		case <-ctx.Done():
			t.Errorf("expected_sse_event_buffer_state: context cancelled while polling: %v", ctx.Err())
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Errorf("expected_sse_event_buffer_state: count = %d, want %d (timed out after %s)",
		lastCount, expected.ExpectedCount, timeout)
}
