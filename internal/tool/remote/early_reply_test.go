// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"testing"
	"time"
)

func TestAReplyThatArrivesBeforeAnyoneWaitsForItIsKeptForTheWaiter(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)
	replied := make(chan struct{})
	simulateSTR(t, brk, "echo", func(corrID, replyTo, _ string) {
		replyWith(t, brk, replyTo, corrID, "early")
		close(replied)
	})
	call, err := exe.Begin(t.Context(), "corr-early", &InvokeRequest{ToolName: "echo", TimeoutSeconds: 5}, nil)
	if err != nil {
		t.Fatalf("Begin() = %v, want nil", err)
	}
	waitOrFail(t, replied, "the STR to reply")
	deadline := time.Now().Add(5 * time.Second)
	for len(call.pr.responseCh) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	resp, err := call.Wait(waitCtx)

	if err != nil {
		t.Fatalf("Wait() = %v, want the reply that arrived before it was called", err)
	}
	if got := resp.Result.ToolResult["message"]; got != "early" {
		t.Errorf("Wait() result message = %v, want %q", got, "early")
	}
}

func waitOrFail(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
