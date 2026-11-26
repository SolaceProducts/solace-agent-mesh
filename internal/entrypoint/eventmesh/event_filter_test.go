// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var largeOrdersFilter = map[string]any{
	"function": "and_op",
	"params": map[string]any{"positional": []any{
		map[string]any{"invoke": map[string]any{
			"function": "equal",
			"params":   map[string]any{"positional": []any{"evaluate_expression(input.topic_levels:1)", "orders"}},
		}},
		map[string]any{"invoke": map[string]any{
			"function": "greater_than",
			"params":   map[string]any{"positional": []any{"evaluate_expression(input.payload:order.total)", 1000}},
		}},
	}},
}

var urgentTextFilter = map[string]any{
	"function": "in_op",
	"params":   map[string]any{"positional": []any{"URGENT", "evaluate_expression(input.payload:)"}},
}

type filterServices struct {
	*stubServicesAuth
	metrics *observability.RecordingMetrics
	logger  *slog.Logger
}

func (s *filterServices) Metrics() runtime.Metrics { return s.metrics }
func (s *filterServices) Logger() *slog.Logger     { return s.logger }

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type filterGWE struct {
	adapter *EventMeshAdapter
	brk     broker.Broker
	metrics *observability.RecordingMetrics
	logs    *syncBuffer
}

func newFilterGWE(t *testing.T, handlerFields map[string]any) *filterGWE {
	t.Helper()
	db := store.TestDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	handler := map[string]any{
		"name":              "h1",
		"subscriptions":     []any{map[string]any{"topic": "in/>"}},
		"input_expression":  "input.payload:msg",
		"target_agent_name": "TestAgent",
	}
	for k, v := range handlerFields {
		handler[k] = v
	}
	logs := &syncBuffer{}
	svc := &filterServices{
		stubServicesAuth: &stubServicesAuth{brk: brk},
		metrics:          observability.NewRecordingMetrics(),
		logger:           slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	adapter := NewEventMeshAdapter()
	adapter.SetDataBroker(brk)
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":                "test",
		"gateway_id":               "em-test-gw",
		"default_user_identity":    "alice",
		"event_handlers":           []any{handler},
		"event_mesh_broker_config": map[string]any{},
	})
	core.SetServices(svc)
	core.SetScopeResolver(&fakeScopeResolver{scopes: []string{"agent:TestAgent:invoke"}})
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	core.UseDatabaseForTest(db)
	adapter.ingress = newDBIngressStore(db, core.GatewayID())
	adapter.held = newHeldEvents(adapter.ingress, adapter.logger)
	return &filterGWE{adapter: adapter, brk: brk, metrics: svc.metrics, logs: logs}
}

func (g *filterGWE) agentRequests(t *testing.T) broker.Queue {
	t.Helper()
	return tapTopic(t, g.brk, "tap-"+t.Name(), a2a.AgentRequestTopic("test", "TestAgent"))
}

func requestTexts(t *testing.T, msgs []*broker.Message) []string {
	t.Helper()
	texts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		var req struct {
			Params struct {
				Message a2a.Message `json:"message"`
			} `json:"params"`
		}
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		for _, p := range req.Params.Message.Parts {
			if p.Kind == "text" {
				texts = append(texts, p.Text)
			}
		}
	}
	slices.Sort(texts)
	return texts
}

func publishJSON(t *testing.T, brk broker.Broker, topic string, body map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(body)
	if err := brk.Publish(t.Context(), topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func receiveUntil(t *testing.T, brk broker.Broker, q broker.Queue, want int) []*broker.Message {
	t.Helper()
	var got []*broker.Message
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < want && time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		msg, err := brk.Receive(ctx, q)
		cancel()
		if err == nil && msg != nil {
			msg.Ack()
			got = append(got, msg)
		}
	}
	return append(got, receiveAll(t, brk, q)...)
}

func (g *filterGWE) largeOrderSentinel(t *testing.T) *broker.Message {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"msg": "sentinel", "order": map[string]any{"total": 5000}})
	if err != nil {
		t.Fatalf("marshal sentinel: %v", err)
	}
	return &broker.Message{ID: "m-sentinel", Topic: "in/orders/eu", Payload: payload}
}

func (g *filterGWE) requireOnlySentinelDelivered(t *testing.T, requests broker.Queue) {
	t.Helper()
	g.adapter.processIncomingMessage(t.Context(), g.adapter.cfg.EventHandlers[0], g.largeOrderSentinel(t))
	if got := requestTexts(t, receiveUntil(t, g.brk, requests, 1)); !slices.Equal(got, []string{"sentinel"}) {
		t.Fatalf("submitted %q after the filtered event and a matching sentinel, want only the sentinel", got)
	}
}

func TestEventFilter_OnlyMatchingEventsBecomeTasks(t *testing.T) {
	g := newFilterGWE(t, map[string]any{"event_filter": largeOrdersFilter})
	requests := g.agentRequests(t)
	if err := g.adapter.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = g.adapter.Stop(context.Background()) })

	publishJSON(t, g.brk, "in/orders/eu", map[string]any{"msg": "small order", "order": map[string]any{"total": 5}})
	publishJSON(t, g.brk, "in/refunds/eu", map[string]any{"msg": "refund", "order": map[string]any{"total": 5000}})
	publishJSON(t, g.brk, "in/orders/eu", map[string]any{"msg": "unreadable total", "order": map[string]any{"total": "lots"}})
	publishJSON(t, g.brk, "in/orders/eu", map[string]any{"msg": "large order", "order": map[string]any{"total": 5000}})

	got := requestTexts(t, receiveUntil(t, g.brk, requests, 2))
	want := []string{"large order", "unreadable total"}
	if !slices.Equal(got, want) {
		t.Fatalf("submitted %q, want %q: a match and an undecidable event become tasks, the rest do not", got, want)
	}
	for outcome, n := range map[string]int64{filterOutcomeMatch: 1, filterOutcomeNoMatch: 2, filterOutcomeError: 1} {
		if c := g.metrics.CounterSum(metricEventFilterEvaluations, "event_rule.name", "h1", "outcome", outcome); c != n {
			t.Errorf("%s evaluations = %d, want %d", outcome, c, n)
		}
	}
}

func TestEventFilter_AbsentFilterSubmitsEveryEvent(t *testing.T) {
	g := newFilterGWE(t, nil)
	requests := g.agentRequests(t)
	if err := g.adapter.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = g.adapter.Stop(context.Background()) })

	publishJSON(t, g.brk, "in/refunds/eu", map[string]any{"msg": "refund", "order": map[string]any{"total": 5}})

	if got := requestTexts(t, receiveUntil(t, g.brk, requests, 1)); !slices.Equal(got, []string{"refund"}) {
		t.Fatalf("submitted %q, want [refund]", got)
	}
	if g.metrics.HasCounter(metricEventFilterEvaluations) {
		t.Error("a rule without event_filter recorded a filter evaluation")
	}
}

func TestEventFilter_TextPayload(t *testing.T) {
	g := newFilterGWE(t, map[string]any{
		"event_filter":     urgentTextFilter,
		"payload_format":   "text",
		"input_expression": "input.payload:",
	})
	requests := g.agentRequests(t)
	handler := g.adapter.cfg.EventHandlers[0]

	for _, body := range []string{"routine heartbeat", "URGENT: disk full on node 3", "URGENT: sentinel"} {
		g.adapter.processIncomingMessage(t.Context(), handler, &broker.Message{Topic: "in/alerts", Payload: []byte(body)})
	}

	want := []string{"URGENT: disk full on node 3", "URGENT: sentinel"}
	if got := requestTexts(t, receiveUntil(t, g.brk, requests, len(want))); !slices.Equal(got, want) {
		t.Fatalf("submitted %q, want only the urgent alerts %q", got, want)
	}
}

func TestEventFilter_NoMatchAcksAndStoresNothing(t *testing.T) {
	for _, ackPolicy := range []map[string]any{
		{"mode": "on_receive"},
		{"mode": "on_completion", "on_failure": map[string]any{"action": "nack"}},
	} {
		t.Run(ackPolicy["mode"].(string), func(t *testing.T) {
			g := newFilterGWE(t, map[string]any{"event_filter": largeOrdersFilter, "acknowledgment_policy": ackPolicy})
			requests := g.agentRequests(t)

			var acked, nacked atomic.Bool
			payload, _ := json.Marshal(map[string]any{"msg": "small order", "order": map[string]any{"total": 5}})
			msg := broker.NewDeliveredMessage("m-1", "in/orders/eu", payload, func() { acked.Store(true) }, func() { nacked.Store(true) })
			msg.ReplicationGroupMessageID = "m-1"
			g.adapter.processIncomingMessage(t.Context(), g.adapter.cfg.EventHandlers[0], msg)

			if !acked.Load() || nacked.Load() {
				t.Errorf("acked=%v nacked=%v, want acked and not nacked", acked.Load(), nacked.Load())
			}
			stored, err := g.adapter.ingress.stored(t.Context(), submitPassBatch)
			if err != nil {
				t.Fatalf("read stored events: %v", err)
			}
			if len(stored) != 0 {
				t.Errorf("a filtered event left %d stored event(s) for the submit pass", len(stored))
			}
			g.requireOnlySentinelDelivered(t, requests)
		})
	}
}

func TestEventFilter_RedeliveryIsDecidedAgain(t *testing.T) {
	g := newFilterGWE(t, map[string]any{"event_filter": largeOrdersFilter})
	requests := g.agentRequests(t)
	payload, _ := json.Marshal(map[string]any{"msg": "small order", "order": map[string]any{"total": 5}})
	for _, redelivered := range []bool{false, true} {
		var acked, nacked atomic.Bool
		msg := broker.NewDeliveredMessage("m-1", "in/orders/eu", payload, func() { acked.Store(true) }, func() { nacked.Store(true) })
		msg.ReplicationGroupMessageID = "m-1"
		msg.Redelivered = redelivered
		g.adapter.processIncomingMessage(t.Context(), g.adapter.cfg.EventHandlers[0], msg)
		if !acked.Load() || nacked.Load() {
			t.Errorf("redelivered=%v: acked=%v nacked=%v, want acked", redelivered, acked.Load(), nacked.Load())
		}
	}
	g.requireOnlySentinelDelivered(t, requests)
}

func TestEventFilter_PayloadNeverReachesTheLogs(t *testing.T) {
	const canary = "CANARY-event-filter-payload-must-not-reach-logs"
	g := newFilterGWE(t, map[string]any{"event_filter": largeOrdersFilter})
	handler := g.adapter.cfg.EventHandlers[0]

	dropped, _ := json.Marshal(map[string]any{"msg": canary, "order": map[string]any{"total": 5, "note": canary}})
	g.adapter.processIncomingMessage(t.Context(), handler, &broker.Message{ID: "m-drop", Topic: "in/orders/eu", Payload: dropped})
	undecidable, _ := json.Marshal(map[string]any{"msg": "order", "order": map[string]any{"total": canary}})
	g.adapter.processIncomingMessage(t.Context(), handler, &broker.Message{ID: "m-error", Topic: "in/orders/eu", Payload: undecidable})

	logs := g.logs.String()
	for _, record := range []string{"event dropped by event_filter", "event_filter could not be evaluated; delivering the event"} {
		if !strings.Contains(logs, record) {
			t.Fatalf("the filter path did not log %q; the canary below would prove nothing:\n%s", record, logs)
		}
	}
	for _, id := range []string{"messageID=m-drop", "messageID=m-error"} {
		if !strings.Contains(logs, id) {
			t.Errorf("filter records do not carry %s", id)
		}
	}
	if strings.Contains(logs, canary) {
		t.Errorf("payload content reached the logs:\n%s", logs)
	}
}

var orderTypeFilter = map[string]any{
	"function": "equal",
	"params":   map[string]any{"positional": []any{"evaluate_expression(input.payload:type)", "order"}},
}

var misspelledOrderTypeFilter = map[string]any{
	"function": "equal",
	"params":   map[string]any{"positional": []any{"evaluate_expression(input.payload:typ)", "order"}},
}

func isUndecidableWarning(line string) bool {
	return strings.Contains(line, "level=WARN") &&
		strings.Contains(line, "event_filter could not be evaluated; delivering the event") &&
		strings.Contains(line, "messageID=m-undecidable")
}

func TestEventFilter_UndecidableEventIsDeliveredAndWarnedUnderEqual(t *testing.T) {
	cases := []struct {
		name    string
		filter  map[string]any
		payload []byte
	}{
		{"missing field", orderTypeFilter, []byte(`{"other": "x"}`)},
		{"non-JSON payload", orderTypeFilter, []byte("this is not json")},
		{"empty payload", orderTypeFilter, nil},
		{"JSON array payload", orderTypeFilter, []byte(`[1, 2]`)},
		{"field of another type", orderTypeFilter, []byte(`{"type": 7}`)},
		{"misspelled path", misspelledOrderTypeFilter, []byte(`{"type": "order"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newFilterGWE(t, map[string]any{"event_filter": tc.filter, "input_expression": "input.topic"})
			requests := g.agentRequests(t)
			handler := g.adapter.cfg.EventHandlers[0]

			g.adapter.processIncomingMessage(t.Context(), handler, &broker.Message{ID: "m-undecidable", Topic: "in/undecidable", Payload: tc.payload})

			if got := requestTexts(t, receiveUntil(t, g.brk, requests, 1)); !slices.Equal(got, []string{"in/undecidable"}) {
				t.Fatalf("submitted %q, want the undecidable event delivered", got)
			}
			if c := g.metrics.CounterSum(metricEventFilterEvaluations, "event_rule.name", "h1", "outcome", filterOutcomeError); c != 1 {
				t.Errorf("error evaluations = %d, want 1", c)
			}
			if c := g.metrics.CounterSum(metricEventFilterEvaluations, "event_rule.name", "h1", "outcome", filterOutcomeNoMatch); c != 0 {
				t.Errorf("no_match evaluations = %d, want 0: an undecidable event is not a non-match", c)
			}
			logs := g.logs.String()
			if !slices.ContainsFunc(strings.Split(logs, "\n"), isUndecidableWarning) {
				t.Errorf("an undecidable event must be logged at WARN with its message id:\n%s", logs)
			}
		})
	}
}

func TestEventFilter_DecidedEventsUnderEqualAreNotWarned(t *testing.T) {
	g := newFilterGWE(t, map[string]any{"event_filter": orderTypeFilter, "input_expression": "input.topic"})
	requests := g.agentRequests(t)
	handler := g.adapter.cfg.EventHandlers[0]

	g.adapter.processIncomingMessage(t.Context(), handler, &broker.Message{ID: "m-order", Topic: "in/order", Payload: []byte(`{"type": "order"}`)})
	g.adapter.processIncomingMessage(t.Context(), handler, &broker.Message{ID: "m-refund", Topic: "in/refund", Payload: []byte(`{"type": "refund"}`)})
	g.adapter.processIncomingMessage(t.Context(), handler, &broker.Message{ID: "m-sentinel", Topic: "in/sentinel", Payload: []byte(`{"type": "order"}`)})

	want := []string{"in/order", "in/sentinel"}
	if got := requestTexts(t, receiveUntil(t, g.brk, requests, len(want))); !slices.Equal(got, want) {
		t.Fatalf("submitted %q, want the order and the sentinel but not the refund: %q", got, want)
	}
	if strings.Contains(g.logs.String(), "event_filter could not be evaluated") {
		t.Errorf("a decided event logged a filter warning:\n%s", g.logs.String())
	}
}
