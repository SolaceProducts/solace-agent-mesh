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

func TestDAGExecutor_PublishNotification(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))

	const ns = "test-ns"
	q, err := brk.CreateQueue("notify-sub", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.NotifyRequestSubscription(ns)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	e := &DAGExecutor{
		cfg:      &WorkflowConfig{Name: "wf", Namespace: ns},
		instance: &WorkflowInstance{svc: rt.Services()},
		logger:   slog.Default(),
	}

	req := a2a.NotifyRequest{
		RequestID:       "wf-1",
		RecipientUserID: "trent@example.org",
		Mode:            a2a.NotifyModeNotify,
		SenderUserID:    "wf-agent",
		Message:         "from a workflow node",
		Metadata:        map[string]any{"trace_id": "tr-1"},
	}
	if err = e.publishNotification(ctx, req); err != nil {
		t.Fatalf("publishNotification: %v", err)
	}

	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if msg.Topic != a2a.NotifyRequestTopic(ns, "trent@example.org") {
		t.Errorf("topic = %q", msg.Topic)
	}
	if got := msg.UserProperties[a2a.PropUserID]; got != "wf-agent" {
		t.Errorf("sender principal on UserProperties = %q, want wf-agent", got)
	}
	if got := msg.UserProperties[a2a.PropTraceID]; got != "tr-1" {
		t.Errorf("traceID on UserProperties = %q, want tr-1 (from Metadata[trace_id])", got)
	}
	var got a2a.NotifyRequest
	if err = json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("payload not a NotifyRequest: %v", err)
	}
	if got.Message != "from a workflow node" || got.RecipientUserID != "trent@example.org" {
		t.Errorf("unexpected NotifyRequest: %+v", got)
	}
}
