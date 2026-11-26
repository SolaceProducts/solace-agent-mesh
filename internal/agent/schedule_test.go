// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type fakeReplyBroker struct {
	broker.Broker
	failTempQueue      bool
	failReplySubscribe bool
	failPublish        bool
}

func (f *fakeReplyBroker) CreateTemporaryQueue() (broker.Queue, error) {
	if f.failTempQueue {
		return nil, errors.New("temporary queues disabled on this broker")
	}
	return f.Broker.CreateTemporaryQueue()
}

func (f *fakeReplyBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if f.failPublish {
		return errors.New("broker rejected the publish")
	}
	return f.Broker.PublishGuaranteed(ctx, topic, msg)
}

func (f *fakeReplyBroker) Subscribe(q broker.Queue, topicPattern string) error {
	if f.failReplySubscribe && strings.Contains(topicPattern, "/schedule/reply/") {
		return errors.New("reply subscriptions not permitted")
	}
	return f.Broker.Subscribe(q, topicPattern)
}

func scheduleReqHarness(t *testing.T) (*Agent, broker.Broker, broker.Queue) {
	t.Helper()
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	agent := New(AgentConfig{Name: "sched-agent", Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	agent.svc = svc
	if err := agent.Init(ctx, nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	q, err := brk.CreateQueue("t-schedule-req", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.ScheduleRequestSubscription("test-ns")); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return agent, brk, q
}

func serveOneScheduleRequest(t *testing.T, brk broker.Broker, q broker.Queue, ok bool, rejectMsg string) <-chan *broker.Message {
	t.Helper()
	ctx := t.Context()
	got := make(chan *broker.Message, 1)
	go func() {
		msg, err := brk.Receive(ctx, q)
		if err != nil || msg == nil {
			close(got)
			return
		}
		msg.Ack()
		var req a2a.ScheduleRequest
		_ = json.Unmarshal(msg.Payload, &req)
		resp := a2a.ScheduleResponse{RequestID: req.RequestID, OK: ok, TaskID: req.RequestID}
		if !ok {
			resp.Error = rejectMsg
			resp.ErrorKind = "validation"
		}
		data, _ := json.Marshal(resp)
		if req.ReplyTo != "" {
			_ = brk.PublishGuaranteed(ctx, req.ReplyTo,
				&broker.Message{Payload: data, CorrelationID: req.RequestID})
		}
		got <- msg
	}()
	return got
}

func TestPublishScheduleRequest_StampsSenderPrincipalAndAccepts(t *testing.T) {
	agent, brk, q := scheduleReqHarness(t)
	got := serveOneScheduleRequest(t, brk, q, true, "")

	req := a2a.ScheduleRequest{
		RequestID:    "req-S",
		Name:         "nightly",
		ScheduleType: "cron",
		TargetName:   "reconcile",
		SenderUserID: "trent@example.org",
	}
	if err := agent.publishScheduleRequest(t.Context(), req); err != nil {
		t.Fatalf("publishScheduleRequest: %v", err)
	}

	msg := <-got
	if msg == nil {
		t.Fatal("service never received the ScheduleRequest")
	}
	if msg.Topic != a2a.ScheduleRequestTopic("test-ns") {
		t.Errorf("topic = %q", msg.Topic)
	}
	if got := msg.UserProperties[a2a.PropUserID]; got != "trent@example.org" {
		t.Errorf("sender principal on UserProperties = %q, want trent@example.org", got)
	}
	var sent a2a.ScheduleRequest
	if err := json.Unmarshal(msg.Payload, &sent); err != nil {
		t.Fatalf("payload not a ScheduleRequest: %v", err)
	}
	if sent.RequestID != "req-S" || sent.TargetName != "reconcile" {
		t.Errorf("unexpected ScheduleRequest: %+v", sent)
	}
	if sent.ReplyTo != a2a.ScheduleReplyTopic("test-ns", "req-S") {
		t.Errorf("ReplyTo = %q, want the per-request reply topic", sent.ReplyTo)
	}
}

func TestPublishScheduleRequest_SurfacesRejection(t *testing.T) {
	agent, brk, q := scheduleReqHarness(t)
	const reason = `targetName "Ghost" is not a registered agent`
	serveOneScheduleRequest(t, brk, q, false, reason)

	err := agent.publishScheduleRequest(t.Context(), a2a.ScheduleRequest{
		RequestID: "req-R", Name: "bad", ScheduleType: "cron", TargetName: "Ghost",
		SenderUserID: "trent@example.org",
	})
	if err == nil {
		t.Fatal("expected a rejection error, got nil (silent success)")
	}
	if err.Error() != reason {
		t.Errorf("error = %q, want the gateway's rejection reason %q", err.Error(), reason)
	}
}

func TestPublishScheduleRequest_FallsBackToFireAndForget(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(broker.Broker) broker.Broker
	}{
		{"temporary queue unavailable", func(b broker.Broker) broker.Broker {
			return &fakeReplyBroker{Broker: b, failTempQueue: true}
		}},
		{"reply subscribe rejected", func(b broker.Broker) broker.Broker {
			return &fakeReplyBroker{Broker: b, failReplySubscribe: true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, brk, q := scheduleReqHarness(t)
			agent.svc.(*mockServices).brk = tc.wrap(brk)

			req := a2a.ScheduleRequest{
				RequestID: "req-FF", Name: "nightly", ScheduleType: "cron",
				TargetName: "reconcile", SenderUserID: "trent@example.org",
			}
			if err := agent.publishScheduleRequest(t.Context(), req); err != nil {
				t.Fatalf("fallback should still publish, got %v", err)
			}

			msg, err := brk.Receive(t.Context(), q)
			if err != nil {
				t.Fatalf("receive: %v", err)
			}
			if got := msg.UserProperties[a2a.PropUserID]; got != "trent@example.org" {
				t.Errorf("sender principal = %q, want trent@example.org", got)
			}
			var sent a2a.ScheduleRequest
			if err := json.Unmarshal(msg.Payload, &sent); err != nil {
				t.Fatalf("payload not a ScheduleRequest: %v", err)
			}
			if sent.ReplyTo != "" {
				t.Errorf("fire-and-forget must omit ReplyTo, got %q", sent.ReplyTo)
			}
		})
	}
}

func TestPublishScheduleRequest_PublishFailureIsReported(t *testing.T) {
	for _, tc := range []struct {
		name string
		brk  func(broker.Broker) broker.Broker
	}{
		{"confirmed path", func(b broker.Broker) broker.Broker {
			return &fakeReplyBroker{Broker: b, failPublish: true}
		}},
		{"fire-and-forget fallback", func(b broker.Broker) broker.Broker {
			return &fakeReplyBroker{Broker: b, failTempQueue: true, failPublish: true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, brk, _ := scheduleReqHarness(t)
			agent.svc.(*mockServices).brk = tc.brk(brk)
			err := agent.publishScheduleRequest(t.Context(), a2a.ScheduleRequest{
				RequestID: "req-P", Name: "n", ScheduleType: "cron",
				TargetName: "reconcile", SenderUserID: "trent@example.org",
			})
			if err == nil || !strings.Contains(err.Error(), "publish schedule request") {
				t.Fatalf("expected a publish error, got %v", err)
			}
		})
	}
}

func TestPublishScheduleRequest_TimeoutReportsUnconfirmed(t *testing.T) {
	agent, _, _ := scheduleReqHarness(t)

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	err := agent.publishScheduleRequest(ctx, a2a.ScheduleRequest{
		RequestID: "req-T", Name: "slow", ScheduleType: "cron",
		TargetName: "reconcile", SenderUserID: "trent@example.org",
	})
	if err == nil {
		t.Fatal("expected an unconfirmed error when no reply arrives")
	}
	if !strings.Contains(err.Error(), "could not be confirmed") {
		t.Errorf("error = %q, want the couldn't-confirm message", err.Error())
	}
}

func TestPublishScheduleRequest_RejectWithoutReason(t *testing.T) {
	agent, brk, q := scheduleReqHarness(t)
	serveOneScheduleRequest(t, brk, q, false, "")

	err := agent.publishScheduleRequest(t.Context(), a2a.ScheduleRequest{
		RequestID: "req-NR", Name: "n", ScheduleType: "cron",
		TargetName: "reconcile", SenderUserID: "trent@example.org",
	})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected a generic rejection error, got %v", err)
	}
}

func TestPublishScheduleRequest_UnreadableReply(t *testing.T) {
	agent, brk, q := scheduleReqHarness(t)
	go func() {
		msg, err := brk.Receive(t.Context(), q)
		if err != nil || msg == nil {
			return
		}
		msg.Ack()
		var req a2a.ScheduleRequest
		_ = json.Unmarshal(msg.Payload, &req)
		_ = brk.PublishGuaranteed(t.Context(), req.ReplyTo,
			&broker.Message{Payload: []byte("not json"), CorrelationID: req.RequestID})
	}()

	err := agent.publishScheduleRequest(t.Context(), a2a.ScheduleRequest{
		RequestID: "req-U", Name: "n", ScheduleType: "cron",
		TargetName: "reconcile", SenderUserID: "trent@example.org",
	})
	if err == nil || !strings.Contains(err.Error(), "unreadable response") {
		t.Fatalf("expected an unreadable-response error, got %v", err)
	}
}

func TestPublishScheduleRequest_IgnoresForeignCorrelation(t *testing.T) {
	agent, brk, q := scheduleReqHarness(t)
	go func() {
		msg, err := brk.Receive(t.Context(), q)
		if err != nil || msg == nil {
			return
		}
		msg.Ack()
		var req a2a.ScheduleRequest
		_ = json.Unmarshal(msg.Payload, &req)
		decoy, _ := json.Marshal(a2a.ScheduleResponse{RequestID: "other", OK: false, Error: "not yours"})
		_ = brk.PublishGuaranteed(t.Context(), req.ReplyTo,
			&broker.Message{Payload: decoy, CorrelationID: "other"})
		real, _ := json.Marshal(a2a.ScheduleResponse{RequestID: req.RequestID, OK: true, TaskID: req.RequestID})
		_ = brk.PublishGuaranteed(t.Context(), req.ReplyTo,
			&broker.Message{Payload: real, CorrelationID: req.RequestID})
	}()

	if err := agent.publishScheduleRequest(t.Context(), a2a.ScheduleRequest{
		RequestID: "req-C", Name: "n", ScheduleType: "cron",
		TargetName: "reconcile", SenderUserID: "trent@example.org",
	}); err != nil {
		t.Fatalf("foreign correlation must be skipped and the real accept honored, got %v", err)
	}
}
