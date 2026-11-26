// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestADuplicateReplyForAPausedCallDoesNotStallTheRepliesOfOtherCalls(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)
	simulateSTR(t, brk, "dup_a", func(corrID, replyTo, _ string) {
		replyWith(t, brk, replyTo, corrID, "first")
		replyWith(t, brk, replyTo, corrID, "duplicate")
	})
	simulateSTR(t, brk, "dup_b", func(corrID, replyTo, _ string) {
		replyWith(t, brk, replyTo, corrID, "other call")
	})
	paused, err := exe.Begin(t.Context(), "corr-dup-a", &InvokeRequest{ToolName: "dup_a", TimeoutSeconds: 5}, nil)
	if err != nil {
		t.Fatalf("Begin(dup_a) = %v, want nil", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(paused.pr.responseCh) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	other, err := exe.Begin(t.Context(), "corr-dup-b", &InvokeRequest{ToolName: "dup_b", TimeoutSeconds: 5}, nil)
	if err != nil {
		t.Fatalf("Begin(dup_b) = %v, want nil", err)
	}

	waitCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	resp, err := other.Wait(waitCtx)

	if err != nil {
		t.Fatalf("Wait() on another call = %v, want its reply although the first call was answered twice", err)
	}
	if got := resp.Result.ToolResult["message"]; got != "other call" {
		t.Errorf("Wait() result message = %v, want %q", got, "other call")
	}
}

func TestCancelRemoteForACallThisExecutorDoesNotHoldStillCancelsItAtTheSTR(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)
	const adoptedCorrID = "corr-adopted-from-a-dead-replica"
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create cancel subscription: %v", err)
	}
	if err = brk.Subscribe(q, remotetool.CancelTopic(testNamespace, adoptedCorrID)); err != nil {
		t.Fatalf("subscribe to the cancel topic: %v", err)
	}

	exe.CancelRemote(adoptedCorrID)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil || msg == nil {
		t.Fatalf("Receive() = %v, %v, want a cancel for %q", msg, err, adoptedCorrID)
	}
	if want := remotetool.CancelTopic(testNamespace, adoptedCorrID); msg.Topic != want {
		t.Errorf("cancel published on %q, want %q", msg.Topic, want)
	}
}

func TestACallThatCannotBeStartedAnswersInlineEvenWhenCallsCanPause(t *testing.T) {
	t.Parallel()
	exe, _ := newTestExecutor(t)
	rt := NewTool(&ToolConfig{ToolName: "echo", TimeoutSeconds: 5}, exe)

	got, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())

	if err != nil {
		t.Fatalf("Execute() = %v, want a failed result", err)
	}
	if got.Status == tool.StatusPending || got.Status == tool.StatusSuccess {
		t.Errorf("Execute() status = %s %q, want a failure because the executor was never started", got.Status, got.Message)
	}
}

func TestAToolWithAStaticAuthHandlerAnswersInlineEvenWhenCallsCanPause(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)
	rt := NewTool(&ToolConfig{
		ToolName:       "bearer_tool",
		TimeoutSeconds: 5,
		Auth:           &toolauth.AuthConfig{Type: "bearer", Credential: &toolauth.AuthCredential{Token: "static-bearer-abc"}},
	}, exe)
	simulateSTR(t, brk, "bearer_tool", func(corrID, replyTo, _ string) {
		replyWith(t, brk, replyTo, corrID, "answered inline")
	})

	got, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())

	if err != nil {
		t.Fatalf("Execute() = %v, want a result", err)
	}
	if got.Status != tool.StatusSuccess || got.OnDelivery != nil {
		t.Errorf("Execute() = %s %q with a delivery hook %v, want a finished success", got.Status, got.Message, got.OnDelivery != nil)
	}
}
