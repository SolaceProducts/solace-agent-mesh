// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestEventMeshIdentityFailureReplyCarriesForwardContextHeaders(t *testing.T) {
	adapter := setupAdapterForDeferredAckHandler(t, "ack", map[string]any{
		"user_identity_expression": "input.user_properties:user_id",
		"forward_context":          map[string]any{"corr": "input.payload:corr"},
	})
	adapter.cfg.OutputHandlers["out"] = mustParseOutput(t, map[string]any{
		"payload_expression": "task_response:a2a_task_response",
		"payload_format":     "json",
		"headers":            map[string]any{"solace_correlationId": "user_data.forward_context:corr"},
	})
	adapter.cfg.EventHandlers[0].OnError = "out"
	replies := refusedEventErrors(t, adapter.dataBroker, "reply/topic")

	var acked, nacked atomic.Bool
	payload, _ := json.Marshal(map[string]any{"msg": "hi", "corr": "abc-9"})
	refuse(t, adapter, broker.NewDeliveredMessage("evt-9", "in/orders", payload,
		func() { acked.Store(true) },
		func() { nacked.Store(true) },
	))

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	reply, err := adapter.dataBroker.Receive(ctx, replies)
	if err != nil || reply == nil {
		t.Fatalf("no identity-failure reply on the Error output: %v", err)
	}
	if !acked.Load() || nacked.Load() {
		t.Errorf("acked=%v nacked=%v, want the refused event acked under the ack failure policy", acked.Load(), nacked.Load())
	}
	if reply.CorrelationID != "abc-9" {
		t.Errorf("reply correlation id = %q, want abc-9 from the rule's forward context", reply.CorrelationID)
	}
	var got struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(reply.Payload, &got); err != nil {
		t.Fatalf("decode reply %s: %v", reply.Payload, err)
	}
	if got.Error.Code != identityFailureCode {
		t.Errorf("error code = %d, want %d", got.Error.Code, identityFailureCode)
	}
}
