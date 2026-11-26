// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func parseOutput(t *testing.T, output map[string]any) (*OutputHandlerConfig, error) {
	t.Helper()
	output["name"] = "out"
	output["topic_expression"] = "static:reply/topic"
	cfg, err := ParseConfig(map[string]any{"output_handlers": []any{output}}, true)
	if err != nil {
		return nil, err
	}
	return cfg.OutputHandlers["out"], nil
}

func mustParseOutput(t *testing.T, output map[string]any) *OutputHandlerConfig {
	t.Helper()
	h, err := parseOutput(t, output)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return h
}

func TestParseOutputHandler_NoDeliverySettingsIsUnchanged(t *testing.T) {
	h := mustParseOutput(t, map[string]any{"payload_expression": "task_response:text"})
	if h.DeliveryMode != "" || h.Headers != nil || h.UserProperties != nil || h.guaranteed() {
		t.Fatalf("handler without new keys = %+v, want direct with no headers or user properties", h)
	}
}

func TestParseOutputHandler_DeliverySettings(t *testing.T) {
	h := mustParseOutput(t, map[string]any{
		"delivery_mode": "guaranteed",
		"headers": map[string]any{
			"solace_dmqEligible":   true,
			"solace_timeToLive":    float64(3000),
			"solace_priority":      "static:7",
			"solace_correlationId": "template:{{text://user_data.forward_context:corr}}",
		},
		"user_properties": map[string]any{
			"source":  "static:sam",
			"attempt": 2,
			"orderId": "input.payload:orderId",
		},
	})
	if !h.guaranteed() {
		t.Fatal("delivery_mode guaranteed not parsed")
	}
	got := map[string]HeaderSetting{}
	for _, s := range h.Headers {
		got[s.Spec.Name] = s
	}
	if got["solace_dmqEligible"].Literal != true ||
		got["solace_timeToLive"].Literal != int64(3000) ||
		got["solace_priority"].Literal != 7 ||
		got["solace_correlationId"].Expression != "template:{{text://user_data.forward_context:corr}}" {
		t.Fatalf("headers = %+v", h.Headers)
	}
	wantProps := []UserPropertySetting{
		{Name: "attempt", Literal: "2"},
		{Name: "orderId", Expression: "input.payload:orderId"},
		{Name: "source", Literal: "sam"},
	}
	if !reflect.DeepEqual(h.UserProperties, wantProps) {
		t.Fatalf("user properties = %+v, want %+v", h.UserProperties, wantProps)
	}
}

func TestParseOutputHandler_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		output map[string]any
		want   string
	}{
		{"unknown delivery mode", map[string]any{"delivery_mode": "persistent"}, `delivery_mode: delivery mode "persistent" is not one of`},
		{"non-string delivery mode", map[string]any{"delivery_mode": true}, `delivery_mode: must be "direct" or "guaranteed" (got a bool)`},
		{"time-to-live beyond a time.Duration", map[string]any{"delivery_mode": "guaranteed", "headers": map[string]any{"solace_timeToLive": int64(10_000_000_000_000)}}, "headers.solace_timeToLive: solace_timeToLive must be between 0 and 9223372036854"},
		{"dmq on direct", map[string]any{"headers": map[string]any{"solace_dmqEligible": true}}, `headers.solace_dmqEligible requires delivery_mode "guaranteed"`},
		{"ttl on explicit direct", map[string]any{"delivery_mode": "direct", "headers": map[string]any{"solace_timeToLive": 10}}, `headers.solace_timeToLive requires delivery_mode "guaranteed"`},
		{"expiration on direct", map[string]any{"headers": map[string]any{"solace_expiration": 10}}, `headers.solace_expiration requires delivery_mode "guaranteed"`},
		{"is-reply", map[string]any{"headers": map[string]any{"solace_isReply": true}}, "solace_isReply is not supported"},
		{"sender timestamp", map[string]any{"headers": map[string]any{"solace_senderTimestamp": 1}}, "solace_senderTimestamp is not supported"},
		{"unknown header", map[string]any{"headers": map[string]any{"solace_colour": "red"}}, `unknown header "solace_colour"`},
		{"priority out of range", map[string]any{"headers": map[string]any{"solace_priority": 300}}, "headers.solace_priority: solace_priority must be between 0 and 255"},
		{"headers not a map", map[string]any{"headers": []any{"x"}}, "headers: expected a map"},
		{"reserved user property", map[string]any{"user_properties": map[string]any{"solace.messaging.message.priority": "static:9"}}, "reserved"},
		{"nested user property", map[string]any{"user_properties": map[string]any{"x": map[string]any{}}}, "user_properties.x: expected a string"},
		{"literal quoted in the error", map[string]any{"headers": map[string]any{"solace_priority": 300}}, "value: 300"},
		{"bare header string is not an expression", map[string]any{"headers": map[string]any{"solace_priority": "5"}}, "headers.solace_priority: unknown expression source"},
		{"bare user property string is not an expression", map[string]any{"user_properties": map[string]any{"source": "sam"}}, "user_properties.source: unknown expression source"},
		{"template with an unknown source", map[string]any{"headers": map[string]any{"solace_correlationId": "template:{{text://bogus:x}}"}}, "unknown expression source"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseOutput(t, c.output)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
			if !strings.Contains(err.Error(), `output_handlers[0]: output handler "out"`) {
				t.Errorf("err = %v, want it located at the output handler", err)
			}
		})
	}
}

func completedTask(text string) *a2a.Task {
	return &a2a.Task{Status: a2a.TaskStatus{
		State:   "completed",
		Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewTextPart(text)}},
	}}
}

func publishVia(t *testing.T, output map[string]any, forward map[string]any, mockBrk *mockPublishBroker) mockPublished {
	t.Helper()
	h := mustParseOutput(t, output)
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{"out": h})
	a.dataBroker = mockBrk
	a.seedPending(t, "task-hdr", &pendingTask{handlerCfg: &EventHandlerConfig{OnSuccess: "out"}, forwardContext: forward})
	a.handleFinalResponse(t.Context(), "task-hdr", completedTask("hello"))
	msgs := mockBrk.published()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	return msgs[0]
}

func TestPublishResponse_DefaultIsBareDirectMessage(t *testing.T) {
	got := publishVia(t, map[string]any{"payload_expression": "task_response:text", "payload_format": "text"}, nil, &mockPublishBroker{})
	if got.Guaranteed {
		t.Fatal("an output without delivery_mode must publish direct")
	}
	want := &broker.Message{Payload: []byte("hello")}
	if !reflect.DeepEqual(got.Message, want) {
		t.Fatalf("message = %+v, want only the payload", got.Message)
	}
}

func TestPublishResponse_GuaranteedWithHeadersAndUserProperties(t *testing.T) {
	got := publishVia(t, map[string]any{
		"payload_expression": "task_response:text",
		"payload_format":     "text",
		"delivery_mode":      "guaranteed",
		"headers": map[string]any{
			"solace_dmqEligible":            true,
			"solace_timeToLive":             3000,
			"solace_expiration":             int64(1_900_000_000_000),
			"solace_priority":               7,
			"solace_sequenceNumber":         "user_data.forward_context:seq",
			"solace_correlationId":          "template:corr-{{text://user_data.forward_context:corr}}",
			"solace_applicationMessageId":   "static:lt-1",
			"solace_applicationMessageType": "static:order.reply",
			"solace_senderId":               "static:sam",
			"solace_httpContentEncoding":    "static:gzip",
		},
		"user_properties": map[string]any{
			"source":  "static:sam",
			"orderId": "user_data.forward_context:order",
			"reply":   "task_response:text",
			"batch":   float64(1234567),
		},
	}, map[string]any{"corr": "abc", "order": float64(1234567), "seq": float64(42)}, &mockPublishBroker{})

	if !got.Guaranteed {
		t.Fatal("delivery_mode guaranteed must use PublishGuaranteed")
	}
	msg := got.Message
	if msg.TTL != 3*time.Second || msg.CorrelationID != "corr-abc" {
		t.Errorf("TTL=%v CorrelationID=%q, want 3s and corr-abc", msg.TTL, msg.CorrelationID)
	}
	dmq, prio, seq := true, 7, uint64(42)
	wantHeaders := &broker.PublishHeaders{
		DMQEligible:            &dmq,
		Expiration:             time.UnixMilli(1_900_000_000_000),
		Priority:               &prio,
		SequenceNumber:         &seq,
		ApplicationMessageID:   "lt-1",
		ApplicationMessageType: "order.reply",
		SenderID:               "sam",
		HTTPContentEncoding:    "gzip",
	}
	if !reflect.DeepEqual(msg.Headers, wantHeaders) {
		t.Errorf("headers = %+v, want %+v", msg.Headers, wantHeaders)
	}
	wantProps := map[string]string{"source": "sam", "orderId": "1234567", "reply": "hello", "batch": "1234567"}
	if !reflect.DeepEqual(msg.UserProperties, wantProps) {
		t.Errorf("user properties = %v, want %v", msg.UserProperties, wantProps)
	}
}

const (
	warnHeaderUnset       = "output header left unset: its value could not be used"
	warnUserPropertyUnset = "output user property left unset: its value could not be used"
)

func decodedLogLines(t *testing.T, logs string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for line := range strings.Lines(logs) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		lines = append(lines, rec)
	}
	return lines
}

func findLog(lines []map[string]any, msg string) map[string]any {
	for _, rec := range lines {
		if rec["msg"] == msg {
			return rec
		}
	}
	return nil
}

func publishWithLogs(t *testing.T, output, forward map[string]any) (*broker.Message, []map[string]any, string, bool) {
	t.Helper()
	mockBrk := &mockPublishBroker{}
	h := mustParseOutput(t, output)
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{"out": h})
	a.dataBroker = mockBrk
	logs := captureLogs(a)

	var acked, nacked atomic.Bool
	a.holdFor(t, "task-log", testMsg(&acked, &nacked), AckFailurePolicy{Action: "nack"})
	a.seedPending(t, "task-log", &pendingTask{handlerCfg: &EventHandlerConfig{OnSuccess: "out"}, forwardContext: forward, deferredACK: true})
	a.handleFinalResponse(t.Context(), "task-log", completedTask("ok"))

	msgs := mockBrk.published()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want the reply published", len(msgs))
	}
	if a.heldCount(t) != 0 || !acked.Load() || nacked.Load() {
		t.Errorf("input event acked=%v nacked=%v, want acked: the reply was published", acked.Load(), nacked.Load())
	}
	return msgs[0].Message, decodedLogLines(t, logs.String()), logs.String(), acked.Load()
}

func TestPublishResponse_UnusableHeaderValueIsLeftUnsetAndKeptOutOfLogs(t *testing.T) {
	const canary = "CANARY-model-content-7f3a"
	cases := []struct {
		name   string
		header string
		value  any
	}{
		{"bool", "solace_dmqEligible", canary},
		{"whole number", "solace_priority", canary},
		{"out of range", "solace_priority", float64(918273)},
		{"unsigned", "solace_sequenceNumber", canary},
		{"string from an object", "solace_correlationId", map[string]any{"secret": canary}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, lines, raw, _ := publishWithLogs(t, map[string]any{
				"payload_expression": "task_response:text",
				"delivery_mode":      "guaranteed",
				"headers": map[string]any{
					c.header:                      "user_data.forward_context:v",
					"solace_applicationMessageId": "static:kept",
				},
			}, map[string]any{"v": c.value})

			if msg.Headers == nil || msg.Headers.ApplicationMessageID != "kept" || msg.CorrelationID != "" ||
				msg.Headers.DMQEligible != nil || msg.Headers.Priority != nil || msg.Headers.SequenceNumber != nil {
				t.Errorf("headers = %+v corr=%q, want only the literal header set", msg.Headers, msg.CorrelationID)
			}
			rec := findLog(lines, warnHeaderUnset)
			if rec == nil || rec["level"] != "WARN" || rec["headerName"] != c.header || rec["handlerName"] != "out" || rec["taskID"] != "task-log" {
				t.Errorf("warning = %v, want WARN %q with headerName=%s, handlerName=out, taskID=task-log", rec, warnHeaderUnset, c.header)
			}
			for _, leaked := range []string{canary, "918273"} {
				if strings.Contains(raw, leaked) {
					t.Errorf("logs quote the evaluated value %q:\n%s", leaked, raw)
				}
			}
		})
	}
}

func TestPublishResponse_UnusableUserPropertyIsLeftUnsetAndLogged(t *testing.T) {
	msg, lines, raw, _ := publishWithLogs(t, map[string]any{
		"payload_expression": "task_response:text",
		"user_properties": map[string]any{
			"whole": "user_data.forward_context:obj",
			"kept":  "static:yes",
		},
	}, map[string]any{"obj": map[string]any{"secret": "CANARY-user-prop"}})

	if !reflect.DeepEqual(msg.UserProperties, map[string]string{"kept": "yes"}) {
		t.Errorf("user properties = %v, want only the literal one", msg.UserProperties)
	}
	rec := findLog(lines, warnUserPropertyUnset)
	if rec == nil || rec["level"] != "WARN" || rec["userPropertyName"] != "whole" || rec["handlerName"] != "out" || rec["taskID"] != "task-log" {
		t.Errorf("warning = %v, want WARN %q with userPropertyName=whole, handlerName=out, taskID=task-log", rec, warnUserPropertyUnset)
	}
	if strings.Contains(raw, "CANARY-user-prop") {
		t.Errorf("logs quote the evaluated value:\n%s", raw)
	}
}

func TestPublishResponse_HeadersReachAConsumerThroughTheDevBroker(t *testing.T) {
	devBroker := broker.NewDevBroker()
	if err := devBroker.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	q, err := devBroker.CreateQueue("reply-consumer", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = devBroker.Subscribe(q, "reply/topic"); err != nil {
		t.Fatal(err)
	}

	h := mustParseOutput(t, map[string]any{
		"payload_expression": "task_response:text",
		"payload_format":     "text",
		"delivery_mode":      "guaranteed",
		"headers": map[string]any{
			"solace_dmqEligible":    true,
			"solace_timeToLive":     5000,
			"solace_priority":       9,
			"solace_correlationId":  "user_data.forward_context:corr",
			"solace_sequenceNumber": "user_data.forward_context:seq",
		},
		"user_properties": map[string]any{"source": "static:sam"},
	})
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{"out": h})
	a.dataBroker = devBroker
	a.seedPending(t, "task-dev", &pendingTask{
		handlerCfg:     &EventHandlerConfig{OnSuccess: "out"},
		forwardContext: map[string]any{"corr": "corr-7", "seq": float64(3)},
	})
	a.handleFinalResponse(t.Context(), "task-dev", completedTask("done"))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	got, err := devBroker.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no reply reached the consumer: %v", err)
	}
	dmq, prio, seq := true, 9, uint64(3)
	want := &broker.PublishHeaders{DMQEligible: &dmq, Priority: &prio, SequenceNumber: &seq}
	if string(got.Payload) != "done" || got.TTL != 5*time.Second || got.CorrelationID != "corr-7" ||
		!reflect.DeepEqual(got.Headers, want) || !reflect.DeepEqual(got.UserProperties, map[string]string{"source": "sam"}) {
		t.Errorf("received payload=%q ttl=%v corr=%q headers=%+v props=%v", got.Payload, got.TTL, got.CorrelationID, got.Headers, got.UserProperties)
	}
}

func TestPublishResponse_HeaderExpressionResults(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		forward map[string]any
		check   func(*broker.Message) bool
	}{
		{"empty text leaves a string header unset", "solace_applicationMessageId", map[string]any{"v": ""}, func(m *broker.Message) bool { return m.Headers == nil }},
		{"blank text leaves a string header unset", "solace_applicationMessageId", map[string]any{"v": "   "}, func(m *broker.Message) bool { return m.Headers == nil }},
		{"a missing field leaves a string header unset", "solace_applicationMessageId", map[string]any{}, func(m *broker.Message) bool { return m.Headers == nil }},
		{"blank text leaves a typed header unset", "solace_priority", map[string]any{"v": " "}, func(m *broker.Message) bool { return m.Headers == nil }},
		{"zero priority is set", "solace_priority", map[string]any{"v": float64(0)}, func(m *broker.Message) bool {
			return m.Headers != nil && m.Headers.Priority != nil && *m.Headers.Priority == 0
		}},
		{"false DMQ eligible is set", "solace_dmqEligible", map[string]any{"v": false}, func(m *broker.Message) bool {
			return m.Headers != nil && m.Headers.DMQEligible != nil && !*m.Headers.DMQEligible
		}},
		{"text true DMQ eligible is set", "solace_dmqEligible", map[string]any{"v": "true"}, func(m *broker.Message) bool {
			return m.Headers != nil && m.Headers.DMQEligible != nil && *m.Headers.DMQEligible
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, lines, _, _ := publishWithLogs(t, map[string]any{
				"payload_expression": "task_response:text",
				"delivery_mode":      "guaranteed",
				"headers":            map[string]any{c.header: "user_data.forward_context:v"},
			}, c.forward)
			if !c.check(msg) {
				t.Errorf("message headers = %+v", msg.Headers)
			}
			if rec := findLog(lines, warnHeaderUnset); rec != nil {
				t.Errorf("a resolved or absent value must not warn, got %v", rec)
			}
		})
	}
}

func TestPublishResponse_LargeNumbersInHeaderTemplates(t *testing.T) {
	for _, c := range []struct {
		expr string
		want string
	}{
		{"user_data.forward_context:id", "1234567"},
		{"template:order-{{text://user_data.forward_context:id}}", "order-1.234567e+06"},
		{"template:order-{{json://user_data.forward_context:id}}", "order-1234567"},
	} {
		t.Run(c.expr, func(t *testing.T) {
			got := publishVia(t, map[string]any{
				"payload_expression": "task_response:text",
				"headers":            map[string]any{"solace_correlationId": c.expr},
			}, map[string]any{"id": float64(1234567)}, &mockPublishBroker{})
			if got.Message.CorrelationID != c.want {
				t.Errorf("correlation id = %q, want %q", got.Message.CorrelationID, c.want)
			}
		})
	}
}

func TestPublishResponse_UserPropertyExpressionResults(t *testing.T) {
	for _, c := range []struct {
		name    string
		forward map[string]any
		want    map[string]string
	}{
		{"empty text leaves it unset", map[string]any{"v": ""}, nil},
		{"blank text leaves it unset", map[string]any{"v": "  "}, nil},
		{"a missing field leaves it unset", map[string]any{}, nil},
		{"false is set", map[string]any{"v": false}, map[string]string{"p": "false"}},
		{"zero is set", map[string]any{"v": float64(0)}, map[string]string{"p": "0"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			msg, lines, _, _ := publishWithLogs(t, map[string]any{
				"payload_expression": "task_response:text",
				"user_properties":    map[string]any{"p": "user_data.forward_context:v"},
			}, c.forward)
			if !reflect.DeepEqual(msg.UserProperties, c.want) {
				t.Errorf("user properties = %v, want %v", msg.UserProperties, c.want)
			}
			if rec := findLog(lines, warnUserPropertyUnset); rec != nil {
				t.Errorf("a resolved or absent value must not warn, got %v", rec)
			}
		})
	}
}

func TestPublishResponse_GuaranteedAckPrecedesInputAck(t *testing.T) {
	var acked, nacked, ackedBeforeBrokerAck atomic.Bool
	mockBrk := &mockPublishBroker{}
	mockBrk.beforeGuaranteedAck = func() { ackedBeforeBrokerAck.Store(acked.Load() || nacked.Load()) }

	h := mustParseOutput(t, map[string]any{"payload_expression": "task_response:text", "delivery_mode": "guaranteed"})
	a := newTestAdapter(nil, map[string]*OutputHandlerConfig{"out": h})
	a.dataBroker = mockBrk
	a.holdFor(t, "task-order", testMsg(&acked, &nacked), AckFailurePolicy{Action: "nack"})
	a.seedPending(t, "task-order", &pendingTask{handlerCfg: &EventHandlerConfig{OnSuccess: "out"}, deferredACK: true})

	a.handleFinalResponse(t.Context(), "task-order", completedTask("ok"))

	if a.heldCount(t) != 0 {
		t.Fatal("input event still held after a successful guaranteed publish")
	}
	if ackedBeforeBrokerAck.Load() {
		t.Fatal("input event was settled before the guaranteed publish was acknowledged")
	}
	if !acked.Load() || nacked.Load() {
		t.Errorf("input event acked=%v nacked=%v, want acked", acked.Load(), nacked.Load())
	}
}

func TestPublishResponse_GuaranteedPublishFailureFollowsAckFailurePolicy(t *testing.T) {
	for _, c := range []struct {
		action                string
		wantAcked, wantNacked bool
	}{
		{action: "nack", wantNacked: true},
		{action: "ack", wantAcked: true},
	} {
		t.Run(c.action, func(t *testing.T) {
			mockBrk := &mockPublishBroker{guaranteedErr: errors.New("spool over quota")}
			h := mustParseOutput(t, map[string]any{"payload_expression": "task_response:text", "delivery_mode": "guaranteed"})
			a := newTestAdapter(nil, map[string]*OutputHandlerConfig{"out": h})
			a.dataBroker = mockBrk

			var acked, nacked atomic.Bool
			a.holdFor(t, "task-fail", testMsg(&acked, &nacked), AckFailurePolicy{Action: c.action})
			a.seedPending(t, "task-fail", &pendingTask{handlerCfg: &EventHandlerConfig{OnSuccess: "out"}, deferredACK: true})
			a.handleFinalResponse(t.Context(), "task-fail", completedTask("ok"))

			if a.heldCount(t) != 0 {
				t.Fatal("input event still held after the publish failed")
			}
			if acked.Load() != c.wantAcked || nacked.Load() != c.wantNacked {
				t.Errorf("acked=%v nacked=%v, want acked=%v nacked=%v", acked.Load(), nacked.Load(), c.wantAcked, c.wantNacked)
			}
		})
	}
}
