// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func publishInvoke(ctx context.Context, t *testing.T, brk broker.Broker, ns, toolName string) broker.Queue {
	t.Helper()
	replyTopic := fmt.Sprintf("%s/reply/%s/%d", ns, toolName, time.Now().UnixNano())
	replyQ, err := brk.CreateQueue(replyTopic, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(replyQ, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}
	params := remotetool.InvokeParams{ToolName: toolName, TaskID: "task-1", SessionID: "sess-1", Args: map[string]any{}}
	reqID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	payload, err := json.Marshal(a2a.NewRequest(reqID, remotetool.MethodInvoke, params))
	if err != nil {
		t.Fatal(err)
	}
	up := map[string]string{
		remotetool.PropReplyTo:  replyTopic,
		remotetool.PropStatusTo: "",
		remotetool.PropClientID: "test-agent",
		remotetool.PropUserID:   "test-user",
	}
	if err := brk.Publish(ctx, remotetool.InvokeTopic(ns, toolName), &broker.Message{
		Payload: payload, UserProperties: up, CorrelationID: reqID,
	}); err != nil {
		t.Fatal(err)
	}
	return replyQ
}

func receiveWithin(ctx context.Context, brk broker.Broker, q broker.Queue, d time.Duration) (json.RawMessage, error) {
	rctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	resp, err := brk.Receive(rctx, q)
	if err != nil {
		return nil, err
	}
	resp.Ack()
	return resp.Payload, nil
}

func isSubscribed(w *Worker, tool string) bool {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	_, ok := w.subscribedTools[tool]
	return ok
}

func TestStopLeavesDurableSubscriptionForSuccessor(t *testing.T) {
	ctx := t.Context()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	manifestPath := writeTestManifest(t)

	wA := newTestWorker(t, svc, manifestPath)
	if err := wA.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := wA.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !isSubscribed(wA, "echo_tool") {
		t.Fatal("precondition: pod A should subscribe echo_tool on Start")
	}
	if err := wA.Stop(ctx); err != nil {
		t.Fatalf("stop pod A: %v", err)
	}

	replyQ := publishInvoke(ctx, t, brk, "test-ns", "echo_tool")

	wB := newTestWorker(t, svc, manifestPath)
	if err := wB.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := wB.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wB.Stop(context.Background()) })

	if _, err := receiveWithin(ctx, brk, replyQ, 8*time.Second); err != nil {
		t.Fatalf("BUG-1: invoke published during the STR restart gap was lost "+
			"(no response from successor pod): %v", err)
	}
}

func TestDataPathMissDoesNotUnsubscribe(t *testing.T) {
	ctx := t.Context()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	manifestPath := writeTestManifest(t)

	w := newTestWorker(t, svc, manifestPath)
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	const late = "late_tool"
	lateTopic := remotetool.InvokeTopic("test-ns", late)
	if err := brk.Subscribe(w.queue, lateTopic); err != nil {
		t.Fatal(err)
	}
	w.subsMu.Lock()
	w.subscribedTools[late] = struct{}{}
	w.subsMu.Unlock()

	resp1, err1 := invokeToolOverBroker(t, ctx, brk, late, nil)
	if err1 != nil {
		t.Fatalf("first invoke got no response: %v", err1)
	}
	if code, _ := parseErrorResponse(t, resp1); code != remotetool.ErrToolNotFound {
		t.Fatalf("first invoke: want ErrToolNotFound, got %q", code)
	}

	if !isSubscribed(w, late) {
		t.Fatal("BUG-2: a transient tool-not-found removed the tool from subscribedTools")
	}

	resp2, err2 := invokeToolOverBroker(t, ctx, brk, late, nil)
	if err2 != nil {
		t.Fatalf("BUG-2: subscription torn down by the miss; second invoke was not routed: %v", err2)
	}
	if code, _ := parseErrorResponse(t, resp2); code != remotetool.ErrToolNotFound {
		t.Fatalf("second invoke: want ErrToolNotFound, got %q", code)
	}
}
