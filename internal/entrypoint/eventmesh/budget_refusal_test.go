// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func spendMeshBudget(t *testing.T, adapter *EventMeshAdapter) {
	t.Helper()
	budgettest.BlockMesh(t, adapter.core.BudgetGate(), time.Now().Add(24*time.Hour))
}

func testAgentQueue(t *testing.T, adapter *EventMeshAdapter) broker.Queue {
	t.Helper()
	return budgettest.TapAgentRequests(t, adapter.dataBroker, "test", "TestAgent")
}

func TestEventMeshUnconfirmedBudgetKeepsTheEventForTheNextPass(t *testing.T) {
	for _, mode := range []string{"on_completion", "on_receive"} {
		t.Run(mode, func(t *testing.T) {
			adapter := setupAdapterForDeferredAckHandler(t, "nack", map[string]any{
				"acknowledgment_policy": map[string]any{"mode": mode},
			})
			budgettest.Unconfirm(t, adapter.core.BudgetGate())
			q := testAgentQueue(t, adapter)
			var acked, nacked atomic.Bool

			reason := refuse(t, adapter, settleTrackedInbound(&acked, &nacked))

			if reason != "" {
				t.Errorf("refusal reason = %q, want none: the event is submitted again", reason)
			}
			if nacked.Load() {
				t.Error("the event was nacked; it is stored for the next pass")
			}
			if acked.Load() != (mode == "on_receive") {
				t.Errorf("acked = %v, want acked only in on_receive", acked.Load())
			}
			rows, err := adapter.ingress.stored(t.Context(), 10)
			if err != nil || len(rows) != 1 {
				t.Fatalf("stored events = %v (err %v), want the event kept", rows, err)
			}
			budgettest.AssertNotDelivered(t, adapter.dataBroker, q)
		})
	}
}

func settleTrackedInbound(acked, nacked *atomic.Bool) *broker.Message {
	payload, _ := json.Marshal(map[string]any{"msg": "hi"})
	return broker.NewDeliveredMessage("evt-1", "in/orders", payload,
		func() { acked.Store(true) },
		func() { nacked.Store(true) },
	)
}

func TestEventMeshBudgetRefusalAcksADeferredMessageEvenUnderANackPolicy(t *testing.T) {
	adapter := setupAdapterForDeferredAckHandler(t, "nack", nil)
	spendMeshBudget(t, adapter)
	q := testAgentQueue(t, adapter)
	var acked, nacked atomic.Bool

	reason := refuse(t, adapter, settleTrackedInbound(&acked, &nacked))

	if reason != taxonomy.RejectBudgetExhausted {
		t.Errorf("refusal reason = %q, want %q", reason, taxonomy.RejectBudgetExhausted)
	}
	if !acked.Load() {
		t.Error("a budget-refused message was not acked, so the broker would redeliver it")
	}
	if nacked.Load() {
		t.Error("a budget-refused message was nacked for a redelivery that can only be refused again")
	}
	if n := adapter.held.count(); n != 0 {
		t.Errorf("deferred ACKs still tracking %d messages after the refusal", n)
	}
	budgettest.AssertNotDelivered(t, adapter.dataBroker, q)
}

func TestEventMeshBudgetRefusalOnReceiveAcksOnceAndReportsTheBudget(t *testing.T) {
	adapter := setupAdapterForDeferredAckHandler(t, "nack", map[string]any{
		"acknowledgment_policy": map[string]any{"mode": "on_receive"},
	})
	spendMeshBudget(t, adapter)
	q := testAgentQueue(t, adapter)
	var acked, nacked atomic.Bool

	reason := refuse(t, adapter, settleTrackedInbound(&acked, &nacked))

	if reason != taxonomy.RejectBudgetExhausted {
		t.Errorf("refusal reason = %q, want %q", reason, taxonomy.RejectBudgetExhausted)
	}
	if !acked.Load() || nacked.Load() {
		t.Errorf("acked=%v nacked=%v, want acked and not nacked", acked.Load(), nacked.Load())
	}
	budgettest.AssertNotDelivered(t, adapter.dataBroker, q)
}

func TestEventMeshBudgetRefusalIsPublishedOnTheErrorOutput(t *testing.T) {
	for _, mode := range []string{"on_completion", "on_receive"} {
		t.Run(mode, func(t *testing.T) {
			adapter := setupAdapterForDeferredAckHandler(t, "nack", map[string]any{
				"acknowledgment_policy": map[string]any{"mode": mode, "on_failure": map[string]any{"action": "nack"}},
				"forward_context":       map[string]any{"event_id": "input.payload:id"},
			})
			adapter.cfg.OutputHandlers["errs"] = &OutputHandlerConfig{
				Name:              "errs",
				TopicExpression:   "template:out/errors/{{text://user_data.forward_context:event_id}}",
				PayloadExpression: "task_response:a2a_task_response",
				PayloadFormat:     "json",
			}
			adapter.cfg.EventHandlers[0].OnError = "errs"
			spendMeshBudget(t, adapter)
			errorTopic := refusedEventErrors(t, adapter.dataBroker, "out/errors/evt-42")
			var acked, nacked atomic.Bool
			payload, _ := json.Marshal(map[string]any{"msg": "hi", "id": "evt-42"})

			refuse(t, adapter, broker.NewDeliveredMessage("evt-42", "in/orders", payload,
				func() { acked.Store(true) },
				func() { nacked.Store(true) },
			))

			if !acked.Load() || nacked.Load() {
				t.Errorf("acked=%v nacked=%v, want the refused event acked", acked.Load(), nacked.Load())
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			published, err := adapter.dataBroker.Receive(ctx, errorTopic)
			if err != nil || published == nil {
				t.Fatalf("no refusal on the Error output for the refused event's forward context: %v", err)
			}
			var got struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(published.Payload, &got); err != nil {
				t.Fatalf("decode Error output %s: %v", published.Payload, err)
			}
			if got.Error.Code != entrypoint.BudgetExhaustedErrorCode {
				t.Errorf("error code = %d, want %d", got.Error.Code, entrypoint.BudgetExhaustedErrorCode)
			}
			if !strings.Contains(got.Error.Message, "this deployment's usage budget is used up until") {
				t.Errorf("error message = %q, want the spent-budget refusal text", got.Error.Message)
			}
		})
	}
}

func refusedEventErrors(t *testing.T, brk broker.Broker, topic string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue("budget-test-errors", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, topic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return q
}
