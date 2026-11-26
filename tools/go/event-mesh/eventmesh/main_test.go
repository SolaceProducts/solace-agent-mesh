// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func withDevBroker(t *testing.T) *broker.DevBroker {
	t.Helper()
	b := broker.NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("dev broker connect: %v", err)
	}
	prev := newBroker
	newBroker = func(_ context.Context, _ BrokerCfg) (broker.Broker, error) {
		return b, nil
	}
	t.Cleanup(func() {
		newBroker = prev
		_ = b.Disconnect(context.Background())
	})
	return b
}

func startResponder(t *testing.T, b *broker.DevBroker, topicPattern string, replyPayload any) func() {
	t.Helper()
	q, err := b.CreateQueue("responder-"+topicPattern, broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err := b.Subscribe(q, topicPattern); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			msg, err := b.Receive(ctx, q)
			if err != nil {
				return
			}
			body, _ := json.Marshal(replyPayload)
			_ = b.Publish(ctx, msg.ReplyTo, &broker.Message{
				Payload:       body,
				CorrelationID: msg.CorrelationID,
			})
		}
	})
	return func() {
		cancel()
		wg.Wait()
		_ = b.DeleteQueue(q.Name())
	}
}

func defaultView() *contextView {
	return &contextView{userID: "user-42", sessionID: "sess-1", appName: "test-agent"}
}

type recordingBroker struct {
	*broker.DevBroker
	mu      sync.Mutex
	lastPub string
}

func (r *recordingBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	r.mu.Lock()
	r.lastPub = "direct"
	r.mu.Unlock()
	return r.DevBroker.Publish(ctx, topic, msg)
}

func (r *recordingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	r.mu.Lock()
	r.lastPub = "guaranteed"
	r.mu.Unlock()
	return r.DevBroker.PublishGuaranteed(ctx, topic, msg)
}

func TestProcess_QoSRouting(t *testing.T) {
	cases := []struct {
		name string
		qos  any
		want string
	}{
		{"unset defaults to guaranteed", nil, "guaranteed"},
		{"qos 1 guaranteed", 1, "guaranteed"},
		{"qos 0 direct", 0, "direct"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dev := broker.NewDevBroker()
			if err := dev.Connect(t.Context()); err != nil {
				t.Fatalf("connect: %v", err)
			}
			rec := &recordingBroker{DevBroker: dev}
			prev := newBroker
			newBroker = func(_ context.Context, _ BrokerCfg) (broker.Broker, error) { return rec, nil }
			t.Cleanup(func() { newBroker = prev; _ = dev.Disconnect(context.Background()) })

			raw := minimalConfigMap()
			raw["wait_for_response"] = false
			raw["topic"] = "acme/test/qos"
			delete(raw, "parameters")
			if tc.qos != nil {
				raw["event_mesh_config"].(map[string]any)["qos"] = tc.qos
			}

			res, err := process(t.Context(), raw, AnyParams{}, defaultView())
			if err != nil {
				t.Fatalf("process: %v", err)
			}
			if res.Status != sdk.StatusSuccess {
				t.Fatalf("status=%q msg=%q", res.Status, res.Message)
			}
			if rec.lastPub != tc.want {
				t.Fatalf("publish method = %q, want %q", rec.lastPub, tc.want)
			}
		})
	}
}

func TestProcess_QoSRouting_RequestReply(t *testing.T) {
	cases := []struct {
		name string
		qos  any
		want string
	}{
		{"unset defaults to guaranteed in request-reply", nil, "guaranteed"},
		{"qos 0 direct in request-reply", 0, "direct"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dev := broker.NewDevBroker()
			if err := dev.Connect(t.Context()); err != nil {
				t.Fatalf("connect: %v", err)
			}
			rec := &recordingBroker{DevBroker: dev}
			prev := newBroker
			newBroker = func(_ context.Context, _ BrokerCfg) (broker.Broker, error) { return rec, nil }
			t.Cleanup(func() { newBroker = prev; _ = dev.Disconnect(context.Background()) })

			stop := startResponder(t, dev, "acme/qos/rr/>", map[string]any{"ok": true})
			defer stop()

			raw := minimalConfigMap()
			raw["wait_for_response"] = true
			raw["topic"] = "acme/qos/rr/test"
			delete(raw, "parameters")
			if tc.qos != nil {
				raw["event_mesh_config"].(map[string]any)["qos"] = tc.qos
			}

			res, err := process(t.Context(), raw, AnyParams{}, defaultView())
			if err != nil {
				t.Fatalf("process: %v", err)
			}
			if res.Status != sdk.StatusSuccess {
				t.Fatalf("status=%q msg=%q", res.Status, res.Message)
			}
			if rec.lastPub != tc.want {
				t.Fatalf("Request-Reply publish method = %q, want %q", rec.lastPub, tc.want)
			}
		})
	}
}

func TestProcess_ReplyModeRouting(t *testing.T) {
	const sacKey = "__solace_ai_connector_broker_request_response_topic__"
	cases := []struct {
		name      string
		replyMode any
		wantSAC   bool
	}{
		{"default sets SAC headers", nil, true},
		{"temp_queue sets SAC headers", "temp_queue", true},
		{"p2p_inbox omits SAC headers", "p2p_inbox", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := withDevBroker(t)

			q, err := b.CreateQueue("resp", broker.QueueOptions{})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}
			if subErr := b.Subscribe(q, "acme/rr/>"); subErr != nil {
				t.Fatalf("Subscribe: %v", subErr)
			}
			var mu sync.Mutex
			var gotProps map[string]string
			ctx, cancel := context.WithCancel(t.Context())
			var wg sync.WaitGroup
			wg.Go(func() {
				for {
					req, recvErr := b.Receive(ctx, q)
					if recvErr != nil {
						return
					}
					mu.Lock()
					gotProps = req.UserProperties
					mu.Unlock()
					_ = b.Publish(ctx, req.ReplyTo, &broker.Message{
						Payload:       []byte(`{"ok":true}`),
						CorrelationID: req.CorrelationID,
					})
				}
			})
			t.Cleanup(func() { cancel(); wg.Wait(); _ = b.DeleteQueue(q.Name()) })

			raw := minimalConfigMap()
			raw["topic"] = "acme/rr/test"
			raw["wait_for_response"] = true
			delete(raw, "parameters")
			if tc.replyMode != nil {
				raw["event_mesh_config"].(map[string]any)["reply_mode"] = tc.replyMode
			}

			res, err := process(t.Context(), raw, AnyParams{}, defaultView())
			if err != nil {
				t.Fatalf("process: %v", err)
			}
			if res.Status != sdk.StatusSuccess {
				t.Fatalf("status=%q msg=%q", res.Status, res.Message)
			}
			mu.Lock()
			_, hasSAC := gotProps[sacKey]
			mu.Unlock()
			if hasSAC != tc.wantSAC {
				t.Fatalf("SAC reply header present=%v, want %v", hasSAC, tc.wantSAC)
			}
		})
	}
}

func TestProcess_TempQueueReplyBeforeReceive(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	respQ, err := dev.CreateQueue("race-responder", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := dev.Subscribe(respQ, "acme/race/>"); subErr != nil {
		t.Fatal(subErr)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		defer wg.Done()
		for {
			req, recvErr := dev.Receive(ctx, respQ)
			if recvErr != nil {
				return
			}
			body, _ := json.Marshal(map[string]any{"ok": true})
			_ = dev.Publish(ctx, req.ReplyTo, &broker.Message{
				Payload:       body,
				CorrelationID: req.CorrelationID,
			})
		}
	}()
	t.Cleanup(func() { cancel(); wg.Wait(); _ = dev.DeleteQueue(respQ.Name()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ BrokerCfg) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	raw := minimalConfigMap()
	raw["topic"] = "acme/race/test"
	raw["wait_for_response"] = true
	delete(raw, "parameters")

	res, err := process(t.Context(), raw, AnyParams{}, defaultView())
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got status=%q msg=%q", res.Status, res.Message)
	}
}

func TestHandle_RequestReplyHappyPath(t *testing.T) {
	b := withDevBroker(t)
	stop := startResponder(t, b, "acme/weather/>", map[string]any{
		"temperature": 21.5,
		"unit":        "celsius",
	})
	defer stop()

	cfg := map[string]any{
		"tool_name":   "GetWeather",
		"description": "Gets the current weather.",
		"topic":       "acme/weather/{request_id}",
		"parameters": []any{
			map[string]any{"name": "city", "type": "string", "required": true, "payload_path": "location.city"},
			map[string]any{"name": "request_id", "type": "string", "required": true},
		},
		"event_mesh_config": map[string]any{
			"broker_config":     map[string]any{"broker_url": "tcp://x", "broker_vpn": "default"},
			"request_expiry_ms": 2000,
			"payload_format":    "json",
		},
	}

	res, err := process(t.Context(), cfg, AnyParams{
		"city":       "Ottawa",
		"request_id": "req-1",
	}, defaultView())
	if err != nil {
		t.Fatalf("process returned error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %q (msg=%q)", res.Status, res.Message)
	}
	got, ok := res.Data["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected payload map, got %#v", res.Data["payload"])
	}
	if got["unit"] != "celsius" {
		t.Fatalf("unexpected reply payload: %#v", got)
	}
}

func TestHandle_FireAndForget(t *testing.T) {
	b := withDevBroker(t)

	received := make(chan *broker.Message, 1)
	q, err := b.CreateQueue("listener", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if subErr := b.Subscribe(q, "acme/events/>"); subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	ctx := t.Context()
	go func() {
		msg, recvErr := b.Receive(ctx, q)
		if recvErr == nil {
			received <- msg
		}
	}()

	waitFalse := false
	cfg := map[string]any{
		"tool_name":         "FireEvent",
		"topic":             "acme/events/{kind}",
		"wait_for_response": waitFalse,
		"parameters": []any{
			map[string]any{"name": "kind", "type": "string", "required": true},
			map[string]any{"name": "body", "type": "string", "payload_path": "body"},
		},
		"event_mesh_config": map[string]any{
			"broker_config":  map[string]any{"broker_url": "tcp://x", "broker_vpn": "default"},
			"payload_format": "json",
		},
	}

	res, err := process(t.Context(), cfg, AnyParams{
		"kind": "user.signup",
		"body": "hello",
	}, defaultView())
	if err != nil {
		t.Fatalf("process returned error: %v", err)
	}
	if res.Status != sdk.StatusSuccess || res.Message != "Request sent asynchronously." {
		t.Fatalf("unexpected publish-subscribe result: status=%q msg=%q", res.Status, res.Message)
	}

	select {
	case msg := <-received:
		var body map[string]any
		if err := json.Unmarshal(msg.Payload, &body); err != nil {
			t.Fatalf("payload not json: %v", err)
		}
		if body["body"] != "hello" {
			t.Fatalf("unexpected body: %#v", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event never delivered")
	}
}

func TestHandle_TopicMissingParam(t *testing.T) {
	cfg := map[string]any{
		"tool_name": "GetWeather",
		"topic":     "acme/weather/{city}",
		"parameters": []any{
			map[string]any{"name": "city", "type": "string", "required": true},
		},
		"event_mesh_config": map[string]any{
			"broker_config": map[string]any{"broker_url": "tcp://x", "broker_vpn": "default"},
		},
	}
	res, err := process(t.Context(), cfg, AnyParams{}, defaultView())
	if err != nil {
		t.Fatalf("process returned error: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("expected error result, got %q", res.Status)
	}
	if !strings.Contains(res.Message, `missing required input property "city" for topic address`) {
		t.Fatalf("unexpected error message: %q", res.Message)
	}
}

func TestHandle_SACReplyHeadersOnRequest(t *testing.T) {
	b := withDevBroker(t)
	stop := startResponder(t, b, "acme/sac/>", map[string]any{"ok": true})
	defer stop()

	captured := make(chan *broker.Message, 1)
	q, err := b.CreateQueue("sniffer-sac", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if subErr := b.Subscribe(q, "acme/sac/>"); subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	go func() {
		msg, recvErr := b.Receive(t.Context(), q)
		if recvErr == nil {
			captured <- msg
		}
	}()

	cfg := map[string]any{
		"tool_name": "SACInterop",
		"topic":     "acme/sac/{id}",
		"parameters": []any{
			map[string]any{"name": "id", "type": "string", "required": true},
		},
		"event_mesh_config": map[string]any{
			"broker_config":     map[string]any{"broker_url": "tcp://x", "broker_vpn": "default"},
			"request_expiry_ms": 2000,
		},
	}
	res, err := process(t.Context(), cfg, AnyParams{"id": "1"}, defaultView())
	if err != nil || res.Status != sdk.StatusSuccess {
		t.Fatalf("process: status=%q err=%v msg=%q", res.Status, err, res.Message)
	}

	select {
	case msg := <-captured:
		topicHeader := msg.UserProperties["__solace_ai_connector_broker_request_response_topic__"]
		if topicHeader == "" {
			t.Fatalf("SAC reply-topic header not set: %#v", msg.UserProperties)
		}
		if topicHeader != msg.ReplyTo {
			t.Fatalf("SAC reply-topic header (%q) does not match ReplyTo (%q)", topicHeader, msg.ReplyTo)
		}
		metaJSON := msg.UserProperties["__solace_ai_connector_broker_request_reply_metadata__"]
		if metaJSON == "" {
			t.Fatalf("SAC reply-metadata header not set: %#v", msg.UserProperties)
		}
		var stack []map[string]string
		if err := json.Unmarshal([]byte(metaJSON), &stack); err != nil {
			t.Fatalf("metadata not valid JSON: %v", err)
		}
		if len(stack) != 1 {
			t.Fatalf("expected single metadata entry, got %d", len(stack))
		}
		if stack[0]["request_id"] != msg.CorrelationID {
			t.Fatalf("request_id (%q) does not match CorrelationID (%q)", stack[0]["request_id"], msg.CorrelationID)
		}
		if stack[0]["response_topic"] != msg.ReplyTo {
			t.Fatalf("response_topic (%q) does not match ReplyTo (%q)", stack[0]["response_topic"], msg.ReplyTo)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request never received")
	}
}

func TestHandle_SACReplyHeaderKeyOverride(t *testing.T) {
	b := withDevBroker(t)
	stop := startResponder(t, b, "acme/sac2/>", map[string]any{"ok": true})
	defer stop()

	captured := make(chan *broker.Message, 1)
	q, _ := b.CreateQueue("sniffer-sac2", broker.QueueOptions{})
	_ = b.Subscribe(q, "acme/sac2/>")
	go func() {
		msg, err := b.Receive(t.Context(), q)
		if err == nil {
			captured <- msg
		}
	}()

	cfg := map[string]any{
		"tool_name": "Override",
		"topic":     "acme/sac2/x",
		"parameters": []any{
			map[string]any{"name": "id", "type": "string"},
		},
		"event_mesh_config": map[string]any{
			"broker_config":                      map[string]any{"broker_url": "tcp://x", "broker_vpn": "default"},
			"request_expiry_ms":                  2000,
			"user_properties_reply_topic_key":    "myReplyTopic",
			"user_properties_reply_metadata_key": "myReplyMeta",
		},
	}
	res, err := process(t.Context(), cfg, AnyParams{}, defaultView())
	if err != nil || res.Status != sdk.StatusSuccess {
		t.Fatalf("process: %v / %q", err, res.Message)
	}

	select {
	case msg := <-captured:
		if msg.UserProperties["myReplyTopic"] == "" {
			t.Fatalf("custom topic key not honored: %#v", msg.UserProperties)
		}
		if msg.UserProperties["myReplyMeta"] == "" {
			t.Fatalf("custom metadata key not honored: %#v", msg.UserProperties)
		}
		if _, has := msg.UserProperties[defaultReplyTopicKey]; has {
			t.Fatalf("default key should not be set when override is configured")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request never received")
	}
}

func TestHandle_ContextExpressionFromUserID(t *testing.T) {
	b := withDevBroker(t)
	stop := startResponder(t, b, "acme/audit/>", map[string]any{"ok": true})
	defer stop()

	receivedPayload := make(chan map[string]any, 1)

	q, err := b.CreateQueue("sniffer", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if subErr := b.Subscribe(q, "acme/audit/>"); subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	ctx := t.Context()
	go func() {
		msg, recvErr := b.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		var v map[string]any
		_ = json.Unmarshal(msg.Payload, &v)
		receivedPayload <- v
	}()

	cfg := map[string]any{
		"tool_name": "Audit",
		"topic":     "acme/audit/{event}",
		"parameters": []any{
			map[string]any{"name": "event", "type": "string", "required": true},
			map[string]any{"name": "user_id", "context_expression": "user_id", "payload_path": "meta.user_id"},
		},
		"event_mesh_config": map[string]any{
			"broker_config":     map[string]any{"broker_url": "tcp://x", "broker_vpn": "default"},
			"request_expiry_ms": 2000,
		},
	}

	res, err := process(t.Context(), cfg, AnyParams{"event": "click"}, defaultView())
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %q (%q)", res.Status, res.Message)
	}

	select {
	case payload := <-receivedPayload:
		meta, ok := payload["meta"].(map[string]any)
		if !ok || meta["user_id"] != "user-42" {
			t.Fatalf("user_id not from context: %#v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request never received")
	}
}

func TestHandle_TimeoutReturnsError(t *testing.T) {
	withDevBroker(t)
	cfg := map[string]any{
		"tool_name": "NoOne",
		"topic":     "void/{id}",
		"parameters": []any{
			map[string]any{"name": "id", "type": "string", "required": true},
		},
		"event_mesh_config": map[string]any{
			"broker_config":     map[string]any{"broker_url": "tcp://x", "broker_vpn": "default"},
			"request_expiry_ms": 200,
		},
	}

	start := time.Now()
	res, err := process(t.Context(), cfg, AnyParams{"id": "1"}, defaultView())
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("expected error, got %q", res.Status)
	}
	if res.Message != "Request failed. No response received." {
		t.Fatalf("unexpected error message: %q", res.Message)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestHandle_BadConfig(t *testing.T) {
	cfg := map[string]any{
		"topic": "acme/x",
		"event_mesh_config": map[string]any{
			"broker_config": map[string]any{"broker_url": "tcp://x", "broker_vpn": "default"},
		},
	}
	res, err := process(t.Context(), cfg, AnyParams{}, defaultView())
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("expected error result, got %q", res.Status)
	}
	if !strings.Contains(res.Message, "Configuration error") {
		t.Fatalf("expected config error, got %q", res.Message)
	}
}
