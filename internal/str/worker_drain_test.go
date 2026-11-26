// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func subscribeReply(t *testing.T, brk broker.Broker, toolName string) broker.Queue {
	t.Helper()
	replyTopic := "test-ns/reply/" + toolName
	q, err := brk.CreateQueue(replyTopic, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, replyTopic); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestStop_DrainsInFlightInvocationBeforeReturning(t *testing.T) {
	const toolName = "drain_tool"
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	brk := newTestBroker(t)
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID:  "drain-worker",
		Namespace: "test-ns",
		Sandbox: SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 10,
			MaxInFlight:             1,
		},
		InProcessTools: []*samtoolsdk.ToolDef{newBlockingDrainTool(toolName, started, release)},
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	replyQ := subscribeReply(t, brk, toolName)

	ctx := t.Context()
	publishDrainInvoke(t, ctx, brk, toolName, "req-drain")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("invocation never started")
	}

	go func() {
		time.Sleep(200 * time.Millisecond)
		release <- struct{}{}
	}()

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- w.Stop(ctx)
	}()

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the in-flight invocation completed")
	}

	recvCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, replyQ)
	if err != nil || msg == nil {
		t.Fatalf("expected a response for the drained invocation, got err=%v msg=%v", err, msg)
	}
	msg.Ack()
}

func TestStop_LeavesRunningCallForRedelivery(t *testing.T) {
	const toolName = "straggler_tool"
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	brk := newAckTrackingBroker(t)
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID:  "straggler-worker",
		Namespace: "test-ns",
		Sandbox: SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 10,
			MaxInFlight:             1,
		},
		InProcessTools: []*samtoolsdk.ToolDef{newBlockingDrainTool(toolName, started, release)},
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	replyQ := subscribeReply(t, brk, toolName)

	const reqID = "req-straggler"
	publishDrainInvoke(t, t.Context(), brk, toolName, reqID)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("invocation never started")
	}

	const budget = 300 * time.Millisecond
	stopCtx, cancelStop := context.WithTimeout(t.Context(), budget)
	defer cancelStop()
	begun := time.Now()
	if err := w.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if took := time.Since(begun); took < budget || took > budget+2*time.Second {
		t.Errorf("Stop took %v, want it to wait the %v budget and then return", took, budget)
	}

	if n := w.activeInvocationCount(); n != 0 {
		t.Errorf("activeInvocationCount = %d after Stop, want 0", n)
	}
	if brk.acked(reqID) {
		t.Error("the call still running at the stop budget was acknowledged; the next STR never gets it")
	}
	recvCtx, cancelRecv := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancelRecv()
	if msg, _ := brk.Receive(recvCtx, replyQ); msg != nil {
		t.Errorf("the call still running at the stop budget was answered: %s", msg.Payload)
	}
}

func TestStop_ReturnsAtOnceWhenIdle(t *testing.T) {
	brk := newTestBroker(t)
	w := newTestWorker(t, &testServices{brk: brk}, writeTestManifest(t))
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	stopCtx, cancelStop := context.WithTimeout(t.Context(), time.Minute)
	defer cancelStop()
	begun := time.Now()
	if err := w.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begun); took > 2*time.Second {
		t.Errorf("idle Stop took %v, want it to return without waiting the budget", took)
	}
}

func TestStop_CancelListenerStaysAliveDuringDrain(t *testing.T) {
	const toolName = "cancel_during_drain_tool"
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	brk := newTestBroker(t)
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID:  "cancel-drain-worker",
		Namespace: "test-ns",
		Sandbox: SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 10,
			MaxInFlight:             1,
		},
		InProcessTools: []*samtoolsdk.ToolDef{newBlockingDrainTool(toolName, started, release)},
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	ctx := t.Context()
	const reqID = "req-cancel-during-drain"
	publishDrainInvoke(t, ctx, brk, toolName, reqID)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("invocation never started")
	}

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- w.Stop(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	if err := brk.Publish(ctx, remotetool.CancelTopic("test-ns", reqID), &broker.Message{}); err != nil {
		t.Fatalf("publish cancel: %v", err)
	}

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after a broker cancel mid-drain; cancel-listener loop was not alive during drain")
	}
}

func TestWaitForRunningCalls_WaitsForTheDispatchLoopToHandOff(t *testing.T) {
	w := &Worker{logger: slog.Default()}
	w.dispatchStarted.Store(true)

	done := make(chan int, 1)
	go func() { done <- w.waitForRunningCalls(t.Context()) }()
	select {
	case <-done:
		t.Fatal("the wait ended while the dispatch loop could still hand off a call")
	case <-time.After(3 * runningCallsPollInterval):
	}

	w.inFlight.Add(1)
	w.dispatchExited.Store(true)
	time.Sleep(2 * runningCallsPollInterval)
	w.inFlight.Add(-1)
	select {
	case remaining := <-done:
		if remaining != 0 {
			t.Errorf("remaining = %d, want 0", remaining)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait did not end after the handed-off call finished")
	}
}
