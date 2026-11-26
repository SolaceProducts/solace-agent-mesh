// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func refuse(t *testing.T, adapter *EventMeshAdapter, msg *broker.Message) string {
	t.Helper()
	return adapter.transformMessage(context.Background(), adapter.cfg.EventHandlers[0], msg)
}

func inbound(topic string, body map[string]any, props map[string]string) *broker.Message {
	payload, _ := json.Marshal(body)
	return &broker.Message{Topic: topic, Payload: payload, UserProperties: props}
}

func TestEventMeshRefusalsReportTheirReason(t *testing.T) {
	t.Run("a message claiming a system principal is an authorization refusal", func(t *testing.T) {
		adapter, _, _ := setupAdapterForAuth(t, defaultAuthAdapterOpts())
		msg := inbound("in/orders", map[string]any{"msg": "hi"}, map[string]string{"user_id": "system:default"})
		if got := refuse(t, adapter, msg); got != taxonomy.RejectForbidden {
			t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectForbidden)
		}
	})

	t.Run("an identity expression that resolves to nothing is an authorization refusal", func(t *testing.T) {
		opts := defaultAuthAdapterOpts()
		opts.gatewayDefaultIdentity = ""
		opts.handlerDefault = ""
		adapter, _, _ := setupAdapterForAuth(t, opts)
		msg := inbound("in/orders", map[string]any{"msg": "hi"}, nil)
		if got := refuse(t, adapter, msg); got != taxonomy.RejectForbidden {
			t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectForbidden)
		}
	})
}

func TestEventMeshRefusalReasonsAreInTheClosedSet(t *testing.T) {
	opts := defaultAuthAdapterOpts()
	opts.gatewayDefaultIdentity = ""
	opts.handlerDefault = ""
	adapter, _, _ := setupAdapterForAuth(t, opts)

	for _, props := range []map[string]string{nil, {"user_id": "system:default"}, {"user_id": "alice"}} {
		reason := refuse(t, adapter, inbound("in/orders", map[string]any{"msg": "hi"}, props))
		if reason == "" {
			continue
		}
		if !slices.Contains(taxonomy.RejectReasons, reason) {
			t.Errorf("refusal reason %q is outside the closed set", reason)
		}
	}
}

func TestAnAcceptedEventMeshMessageReportsNoRefusal(t *testing.T) {
	adapter, _, _ := setupAdapterForAuth(t, defaultAuthAdapterOpts())
	msg := inbound("in/orders", map[string]any{"msg": "hi"}, map[string]string{"user_id": "alice"})
	if got := refuse(t, adapter, msg); got != "" {
		t.Fatalf("an accepted event reported refusal %q", got)
	}
}

func TestAnEventMeshRefusalYieldsOneReason(t *testing.T) {
	opts := defaultAuthAdapterOpts()
	opts.gatewayDefaultIdentity = ""
	opts.handlerDefault = ""
	adapter, _, _ := setupAdapterForAuth(t, opts)

	msg := inbound("in/orders", map[string]any{"nothing": "here"}, map[string]string{"user_id": "system:default"})
	if got := refuse(t, adapter, msg); got != taxonomy.RejectForbidden {
		t.Errorf("refusal reason = %q, want the answering gate's %q alone", got, taxonomy.RejectForbidden)
	}
}

func setupAdapterForDeferredAckHandler(t *testing.T, onFailureAction string, handlerFields map[string]any) *EventMeshAdapter {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	handler := map[string]any{
		"name": "h1",
		"subscriptions": []any{
			map[string]any{"topic": "in/>"},
		},
		"input_expression":  "input.payload:msg",
		"target_agent_name": "TestAgent",
		"acknowledgment_policy": map[string]any{
			"mode": "on_completion",
			"on_failure": map[string]any{
				"action": onFailureAction,
			},
		},
	}
	for k, v := range handlerFields {
		handler[k] = v
	}

	appConfig := map[string]any{
		"namespace":                "test",
		"gateway_id":               "em-test-gw",
		"event_handlers":           []any{handler},
		"event_mesh_broker_config": map[string]any{},
	}

	svc := &stubServicesAuth{brk: brk, budgetGate: budgettest.NewGate()}
	adapter := NewEventMeshAdapter()
	adapter.SetDataBroker(brk)

	core := entrypoint.NewGatewayCore(adapter, appConfig)
	core.SetServices(svc)
	core.SetScopeResolver(&fakeScopeResolver{scopes: []string{"agent:TestAgent:invoke"}})

	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	return adapter
}

func setupAdapterForDeferredAck(t *testing.T, onFailureAction string) *EventMeshAdapter {
	t.Helper()
	return setupAdapterForDeferredAckHandler(t, onFailureAction, map[string]any{
		"target_agent_name":        "UnauthorizedAgent",
		"user_identity_expression": "input.user_properties:user_id",
		"default_user_identity":    "handler-default",
	})
}

func TestEventMeshDeferredNackedSubmitFailureIsNotReported(t *testing.T) {
	adapter := setupAdapterForDeferredAck(t, "nack")
	msg := inbound("in/orders", map[string]any{"msg": "hi"}, map[string]string{"user_id": "alice"})
	if got := refuse(t, adapter, msg); got != "" {
		t.Errorf("refusal reason = %q, want empty (nacked for redelivery, not terminal)", got)
	}
}

func TestEventMeshDeferredAckedSubmitFailureIsReported(t *testing.T) {
	adapter := setupAdapterForDeferredAck(t, "ack")
	msg := inbound("in/orders", map[string]any{"msg": "hi"}, map[string]string{"user_id": "alice"})
	if got := refuse(t, adapter, msg); got != taxonomy.RejectForbidden {
		t.Errorf("refusal reason = %q, want %q (terminal, ack drops the message)", got, taxonomy.RejectForbidden)
	}
}

func setupAdapterForDeferredPreSubmit(t *testing.T, onFailureAction string) *EventMeshAdapter {
	t.Helper()
	return setupAdapterForDeferredAckHandler(t, onFailureAction, map[string]any{
		"input_expression": "input.topic_levels:not-a-number",
	})
}

func TestEventMeshDeferredNackedPreSubmitFailureIsNotReported(t *testing.T) {
	adapter := setupAdapterForDeferredPreSubmit(t, "nack")
	msg := inbound("in/orders", map[string]any{"msg": "hi"}, nil)
	if got := refuse(t, adapter, msg); got != "" {
		t.Errorf("refusal reason = %q, want empty (nacked for redelivery, not terminal)", got)
	}
}

func TestEventMeshDeferredAckedPreSubmitFailureIsReported(t *testing.T) {
	adapter := setupAdapterForDeferredPreSubmit(t, "ack")
	msg := inbound("in/orders", map[string]any{"msg": "hi"}, nil)
	if got := refuse(t, adapter, msg); got != taxonomy.RejectInternal {
		t.Errorf("refusal reason = %q, want %q (terminal, ack drops the message)", got, taxonomy.RejectInternal)
	}
}

func setupAdapterForDeferredIdentityDiscard(t *testing.T, onFailureAction string) *EventMeshAdapter {
	t.Helper()
	return setupAdapterForDeferredAckHandler(t, onFailureAction, map[string]any{
		"user_identity_expression": "input.user_properties:user_id",
	})
}

func TestEventMeshDeferredNackedIdentityDiscardIsNotReported(t *testing.T) {
	adapter := setupAdapterForDeferredIdentityDiscard(t, "nack")
	msg := inbound("in/orders", map[string]any{"msg": "hi"}, map[string]string{"user_id": "system:default"})
	if got := refuse(t, adapter, msg); got != "" {
		t.Errorf("refusal reason = %q, want empty (nacked for redelivery, not terminal)", got)
	}
}

func TestEventMeshDeferredAckedIdentityDiscardIsReported(t *testing.T) {
	adapter := setupAdapterForDeferredIdentityDiscard(t, "ack")
	msg := inbound("in/orders", map[string]any{"msg": "hi"}, map[string]string{"user_id": "system:default"})
	if got := refuse(t, adapter, msg); got != taxonomy.RejectForbidden {
		t.Errorf("refusal reason = %q, want %q (terminal, ack drops the message)", got, taxonomy.RejectForbidden)
	}
}
