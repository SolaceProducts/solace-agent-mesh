// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestDAGExecutor_PublishScheduleRequest(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))

	const ns = "test-ns"
	q, err := brk.CreateQueue("schedule-sub", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.ScheduleRequestSubscription(ns)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	e := &DAGExecutor{
		cfg:      &WorkflowConfig{Name: "wf", Namespace: ns},
		instance: &WorkflowInstance{svc: rt.Services()},
		logger:   slog.Default(),
	}

	req := a2a.ScheduleRequest{
		RequestID:    "wf-S",
		Name:         "follow-up",
		ScheduleType: "interval",
		TargetType:   "workflow",
		TargetName:   "wf",
		SenderUserID: "wf-agent",
	}
	if err = e.publishScheduleRequest(ctx, req); err != nil {
		t.Fatalf("publishScheduleRequest: %v", err)
	}

	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if msg.Topic != a2a.ScheduleRequestTopic(ns) {
		t.Errorf("topic = %q", msg.Topic)
	}
	if got := msg.UserProperties[a2a.PropUserID]; got != "wf-agent" {
		t.Errorf("sender principal on UserProperties = %q, want wf-agent", got)
	}
	var got a2a.ScheduleRequest
	if err = json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("payload not a ScheduleRequest: %v", err)
	}
	if got.TargetName != "wf" || got.TargetType != "workflow" {
		t.Errorf("unexpected ScheduleRequest: %+v", got)
	}
}
