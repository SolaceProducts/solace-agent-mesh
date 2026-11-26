// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestScheduleRequestLoop_SurvivesAPanickingHandlerAndKeepsServing(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	var handled atomic.Int32
	svc.scheduleRequestHandler = func(ctx context.Context, msg *broker.Message) {
		if handled.Add(1) == 1 {
			panic("poison schedule request")
		}
		svc.handleScheduleRequest(ctx, msg)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		svc.scheduleRequestLoop(ctx)
		close(done)
	}()

	req := a2a.ScheduleRequest{
		RequestID: "00000000-0000-7000-8000-0000000000ee", Name: "after-poison",
		ScheduleType: "one_time", ScheduleExpression: "2099-01-01T00:00:00Z",
		TargetType: "workflow", TargetName: "reconcile", TaskMessage: "go",
	}
	payload, _ := json.Marshal(req)
	brk := svc.core.svc.Broker()
	stamped := map[string]string{a2a.PropUserID: "bob@example.com"}

	deadline := time.After(5 * time.Second)
	for {
		_ = brk.PublishGuaranteed(ctx, a2a.ScheduleRequestTopic("test-ns"), &broker.Message{Payload: payload, UserProperties: stamped})
		if got, _ := svc.repo.GetByID(ctx, req.RequestID); got != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("no task created within 5s after a panicking request; handler saw %d messages", handled.Load())
		case <-time.After(50 * time.Millisecond):
		}
	}
	if handled.Load() < 2 {
		t.Fatalf("the loop handled %d messages through the recovering handler, want at least 2", handled.Load())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduleRequestLoop did not exit on context cancel")
	}
}

func TestHandleScheduleRequestRecovering_RepliesWithARefusalBeforeDropping(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	svc.scheduleRequestHandler = func(context.Context, *broker.Message) { panic("poison schedule request") }
	brk := svc.core.svc.Broker()
	replies, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("CreateDirectSubscription: %v", err)
	}
	const replyTopic = "test-ns/schedule/reply/requester-1"
	if err = brk.Subscribe(replies, replyTopic); err != nil {
		t.Fatalf("Subscribe(%q): %v", replyTopic, err)
	}
	acked := 0
	msg := broker.NewDeliveredMessage("m1", "t", []byte(`{}`), func() { acked++ }, func() { t.Error("a panicking request must not be nacked") })
	msg.ReplyTo = replyTopic
	msg.CorrelationID = "req-1"

	svc.handleScheduleRequestRecovering(t.Context(), msg)

	if acked != 1 {
		t.Fatalf("acks = %d, want 1", acked)
	}
	receiveCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	reply, err := brk.Receive(receiveCtx, replies)
	if err != nil || reply == nil {
		t.Fatalf("no reply to the requester: %v", err)
	}
	var resp a2a.ScheduleResponse
	if err := json.Unmarshal(reply.Payload, &resp); err != nil {
		t.Fatalf("reply payload: %v", err)
	}
	if resp.OK || resp.Error == "" || resp.RequestID != "req-1" {
		t.Fatalf("reply = %+v, want a refusal for req-1 with an error message", resp)
	}
}
