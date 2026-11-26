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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const twoGWEOutputTopic = "out/results"

type gweOfEntrypoint struct {
	adapter *EventMeshAdapter
	core    *entrypoint.GatewayCore
}

func newEntrypointGWEs(t *testing.T, n int, ackPolicy map[string]any) ([]gweOfEntrypoint, *store.DB, broker.Broker) {
	t.Helper()
	db := store.TestDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	gwes := make([]gweOfEntrypoint, n)
	for i := range gwes {
		appConfig := map[string]any{
			"namespace":  "test",
			"gateway_id": "em-test-gw",
			"event_handlers": []any{map[string]any{
				"name":                  "h1",
				"subscriptions":         []any{map[string]any{"topic": "in/>"}},
				"input_expression":      "input.payload:msg",
				"target_agent_name":     "TestAgent",
				"on_success":            "ok",
				"acknowledgment_policy": ackPolicy,
			}},
			"output_handlers": []any{map[string]any{
				"name":               "ok",
				"topic_expression":   "static:" + twoGWEOutputTopic,
				"payload_expression": "task_response:text",
				"payload_format":     "text",
			}},
			"event_mesh_broker_config": map[string]any{},
		}
		adapter := NewEventMeshAdapter()
		adapter.SetDataBroker(brk)
		core := entrypoint.NewGatewayCore(adapter, appConfig)
		core.SetServices(&stubServicesAuth{brk: brk})
		core.SetScopeResolver(&fakeScopeResolver{scopes: []string{"agent:TestAgent:invoke"}})
		if err := core.Init(t.Context(), nil); err != nil {
			t.Fatalf("core init: %v", err)
		}
		core.UseDatabaseForTest(db)
		core.UseLeaderIngress()
		adapter.ingress = newDBIngressStore(db, core.GatewayID())
		adapter.held = newHeldEvents(adapter.ingress, adapter.logger)
		gwes[i] = gweOfEntrypoint{adapter: adapter, core: core}
	}
	return gwes, db, brk
}

func onCompletion(action string) map[string]any {
	return map[string]any{"mode": "on_completion", "on_failure": map[string]any{"action": action}}
}

func inputEvent(id string, acked, nacked *atomic.Bool) *broker.Message {
	payload, _ := json.Marshal(map[string]any{"msg": "approve order 42?"})
	msg := broker.NewDeliveredMessage(id, "in/orders", payload, func() { acked.Store(true) }, func() { nacked.Store(true) })
	msg.ReplicationGroupMessageID = id
	return msg
}

func tapTopic(t *testing.T, brk broker.Broker, name, topic string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, topic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return q
}

func receiveAll(t *testing.T, brk broker.Broker, q broker.Queue) []*broker.Message {
	t.Helper()
	var got []*broker.Message
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		msg, err := brk.Receive(ctx, q)
		cancel()
		if err != nil || msg == nil {
			return got
		}
		msg.Ack()
		got = append(got, msg)
	}
}

func requestTaskIDs(t *testing.T, msgs []*broker.Message) []string {
	t.Helper()
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		ids = append(ids, req.ID)
	}
	return ids
}

func answered(text string) *a2a.Task {
	return &a2a.Task{Status: a2a.TaskStatus{
		State:   a2a.TaskStateCompleted,
		Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewTextPart(text)}},
	}}
}

func inboundRow(t *testing.T, db *store.DB, messageID string) (state, taskID, outcome string) {
	t.Helper()
	var tid, out *string
	if err := db.QueryRowContext(t.Context(),
		`SELECT state, task_id, outcome FROM entrypoint_inbound_messages WHERE message_id = ?`, messageID).
		Scan(&state, &tid, &out); err != nil {
		t.Fatalf("read inbound row: %v", err)
	}
	if tid != nil {
		taskID = *tid
	}
	if out != nil {
		outcome = *out
	}
	return state, taskID, outcome
}

func TestTwoGWEs_OnCompletionAnsweredOnTheOtherGWE_AcksTheHeldEvent(t *testing.T) {
	gwes, db, brk := newEntrypointGWEs(t, 2, onCompletion("nack"))
	a, b := gwes[0].adapter, gwes[1].adapter
	requests := tapTopic(t, brk, "agent-requests", a2a.AgentRequestTopic("test", "TestAgent"))
	outputs := tapTopic(t, brk, "outputs", twoGWEOutputTopic)
	var acked, nacked atomic.Bool

	if reason := a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &acked, &nacked)); reason != "" {
		t.Fatalf("refusal reason = %q", reason)
	}
	if got := receiveAll(t, brk, requests); len(got) != 0 {
		t.Fatalf("GWE A submitted %d requests; only the leader submits", len(got))
	}
	b.submitPass(t.Context())
	ids := requestTaskIDs(t, receiveAll(t, brk, requests))
	if len(ids) != 1 {
		t.Fatalf("agent requests = %v, want one", ids)
	}
	if state, taskID, _ := inboundRow(t, db, "h1/evt-1"); state != store.InboundDone || taskID != ids[0] {
		t.Fatalf("inbound row = (%s, %s), want done as the submitted task %s", state, taskID, ids[0])
	}

	b.handleFinalResponse(t.Context(), ids[0], answered("approved"))

	if got := receiveAll(t, brk, outputs); len(got) != 1 || string(got[0].Payload) != "approved" {
		t.Fatalf("outputs = %v, want the answer once", got)
	}
	if acked.Load() || nacked.Load() {
		t.Fatal("GWE A settled its event before reading the outcome")
	}
	a.held.poll(t.Context())
	if !acked.Load() || nacked.Load() {
		t.Errorf("acked=%v nacked=%v, want GWE A to ack its held event", acked.Load(), nacked.Load())
	}
	if _, _, outcome := inboundRow(t, db, "h1/evt-1"); outcome != store.InboundOutcomeAck {
		t.Errorf("outcome = %q, want ack", outcome)
	}
}

func TestTwoGWEs_CopyOfAStoredEvent_WaitsForTheSameTask(t *testing.T) {
	gwes, _, brk := newEntrypointGWEs(t, 2, onCompletion("nack"))
	a, b := gwes[0].adapter, gwes[1].adapter
	requests := tapTopic(t, brk, "agent-requests", a2a.AgentRequestTopic("test", "TestAgent"))
	var aAcked, aNacked, bAcked, bNacked atomic.Bool

	a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &aAcked, &aNacked))
	b.transformMessage(t.Context(), b.cfg.EventHandlers[0], inputEvent("evt-1", &bAcked, &bNacked))
	b.submitPass(t.Context())
	b.submitPass(t.Context())

	ids := requestTaskIDs(t, receiveAll(t, brk, requests))
	if len(ids) != 1 {
		t.Fatalf("agent requests = %v, want the event submitted once", ids)
	}
	b.handleFinalResponse(t.Context(), ids[0], answered("approved"))
	if !bAcked.Load() || bNacked.Load() {
		t.Errorf("copy acked=%v nacked=%v, want acked by the answer", bAcked.Load(), bNacked.Load())
	}
}

func TestTwoGWEs_RejectedEventWithNack_IsSubmittedAgainAsANewTask(t *testing.T) {
	gwes, db, brk := newEntrypointGWEs(t, 2, onCompletion("nack"))
	a, b := gwes[0].adapter, gwes[1].adapter
	b.cfg.EventHandlers[0].OnSuccess = "missing"
	requests := tapTopic(t, brk, "agent-requests", a2a.AgentRequestTopic("test", "TestAgent"))
	var acked, nacked atomic.Bool

	a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &acked, &nacked))
	b.submitPass(t.Context())
	first := requestTaskIDs(t, receiveAll(t, brk, requests))
	b.handleFinalResponse(t.Context(), first[0], answered("approved"))
	a.held.poll(t.Context())

	if acked.Load() || !nacked.Load() {
		t.Fatalf("acked=%v nacked=%v, want nacked through on_failure", acked.Load(), nacked.Load())
	}
	var redeliveredAck, redeliveredNack atomic.Bool
	b.transformMessage(t.Context(), b.cfg.EventHandlers[0], inputEvent("evt-1", &redeliveredAck, &redeliveredNack))
	b.submitPass(t.Context())
	second := requestTaskIDs(t, receiveAll(t, brk, requests))
	if len(second) != 1 || second[0] == first[0] {
		t.Fatalf("retry requests = %v after %v, want one under a new task", second, first)
	}
	if _, taskID, outcome := inboundRow(t, db, "h1/evt-1"); taskID != second[0] || outcome != "" {
		t.Errorf("inbound row = (%s, %q), want the retry's task with no outcome yet", taskID, outcome)
	}
}

func TestTwoGWEs_UnmarkedSubmit_IsSubmittedAgainUnderTheSameTask(t *testing.T) {
	gwes, db, brk := newEntrypointGWEs(t, 2, map[string]any{"mode": "on_receive"})
	a, b := gwes[0].adapter, gwes[1].adapter
	requests := tapTopic(t, brk, "agent-requests", a2a.AgentRequestTopic("test", "TestAgent"))
	var acked, nacked atomic.Bool

	a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &acked, &nacked))
	if !acked.Load() {
		t.Fatal("on_receive event not acked once stored")
	}
	rows, err := a.ingress.stored(t.Context(), 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("stored = %v (err %v)", rows, err)
	}
	if b.submitStored(t.Context(), rows[0]) != "" {
		t.Fatal("submit refused")
	}
	if _, err := db.ExecContext(t.Context(),
		`UPDATE entrypoint_inbound_messages SET state = ?, payload = ? WHERE message_id = ?`, store.InboundStored, rows[0].Payload, "h1/evt-1"); err != nil {
		t.Fatalf("undo mark: %v", err)
	}
	gwes[0].adapter.submitPass(t.Context())

	ids := requestTaskIDs(t, receiveAll(t, brk, requests))
	if len(ids) != 2 || ids[0] != ids[1] || ids[0] != rows[0].TaskID {
		t.Errorf("agent requests = %v, want the stored task %s twice", ids, rows[0].TaskID)
	}
}

func TestHeldEvents_TimeoutWithoutOutcome_AppliesTheFailurePolicy(t *testing.T) {
	for _, action := range []string{"ack", "nack"} {
		t.Run(action, func(t *testing.T) {
			s := newMemIngressStore()
			h := newHeldEvents(s, testLogger())
			if _, err := s.store(t.Context(), "evt-1", "task-1", "{}"); err != nil {
				t.Fatal(err)
			}
			var acked, nacked atomic.Bool
			h.hold("evt-1", testMsg(&acked, &nacked), time.Minute, AckFailurePolicy{Action: action})
			now := time.Now()
			h.now = func() time.Time { return now }

			h.poll(t.Context())
			if acked.Load() || nacked.Load() || h.count() != 1 {
				t.Fatal("settled before the timeout")
			}
			h.now = func() time.Time { return now.Add(2 * time.Minute) }
			h.poll(t.Context())

			if acked.Load() != (action == "ack") || nacked.Load() != (action == "nack") {
				t.Errorf("acked=%v nacked=%v, want the %s policy", acked.Load(), nacked.Load(), action)
			}
			if action == "nack" {
				if rows, _ := s.stored(t.Context(), 10); len(rows) != 0 {
					t.Errorf("stored events = %v, want the event reset before the nack", rows)
				}
			}
		})
	}
}

func TestHeldEvents_DrainAll_NacksAndKeepsTheStoredEvents(t *testing.T) {
	s := newMemIngressStore()
	h := newHeldEvents(s, testLogger())
	if _, err := s.store(t.Context(), "evt-1", "task-1", "{}"); err != nil {
		t.Fatal(err)
	}
	var acked, nacked atomic.Bool
	h.hold("evt-1", testMsg(&acked, &nacked), time.Hour, AckFailurePolicy{Action: "nack"})

	h.drainAll()

	if !nacked.Load() || acked.Load() || h.count() != 0 {
		t.Errorf("acked=%v nacked=%v held=%d, want nacked and released", acked.Load(), nacked.Load(), h.count())
	}
	if rows, _ := s.stored(t.Context(), 10); len(rows) != 1 {
		t.Errorf("stored events = %v, want the event kept", rows)
	}
}
