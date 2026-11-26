// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2arpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type rpcTestBroker struct {
	*broker.DevBroker
	mu        sync.Mutex
	published []brokerMsg

	onPublish func(topic string, msg *broker.Message)
}

type brokerMsg struct {
	topic   string
	payload []byte
	props   map[string]string
}

func newTestBroker() *rpcTestBroker {
	dev := broker.NewDevBroker()
	_ = dev.Connect(context.Background())
	return &rpcTestBroker{DevBroker: dev}
}

func (b *rpcTestBroker) answer(request *broker.Message, payload []byte) {
	_ = b.DevBroker.Publish(context.Background(), request.UserProperties[a2a.PropReplyTo], &broker.Message{Payload: payload})
}

func (b *rpcTestBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	b.mu.Lock()
	b.published = append(b.published, brokerMsg{topic: topic, payload: msg.Payload, props: msg.UserProperties})
	hook := b.onPublish
	b.mu.Unlock()
	if hook != nil {
		go hook(topic, msg)
	}
	return b.DevBroker.Publish(ctx, topic, msg)
}

func (b *rpcTestBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	return b.Publish(ctx, topic, msg)
}

func (b *rpcTestBroker) lastPublished() brokerMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.published) == 0 {
		return brokerMsg{}
	}
	return b.published[len(b.published)-1]
}

func extractRPCID(t *testing.T, m brokerMsg) string {
	t.Helper()
	rt := m.props[a2a.PropReplyTo]
	idx := strings.LastIndex(rt, "/")
	if idx < 0 {
		t.Fatalf("invalid replyTo: %q", rt)
	}
	return rt[idx+1:]
}

const (
	testNS    = "test-ns"
	testGW    = "gw-1"
	testURI   = "https://solace.com/sam/ext/test/v1"
	testAgent = "MyAgent"
)

func TestClient_Call_HappyPath(t *testing.T) {
	brk := newTestBroker()
	c := New(Config{Broker: brk, Namespace: testNS, GatewayID: testGW, Timeout: 2 * time.Second})

	brk.onPublish = func(_ string, msg *broker.Message) {
		var req a2a.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Errorf("unmarshal published request: %v", err)
			return
		}
		resp := &a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"ok": true, "n": 7},
		}
		payload, _ := json.Marshal(resp)
		brk.answer(msg, payload)
	}

	raw, err := c.Call(t.Context(), testAgent, "", "session/echo", testURI, map[string]string{"s": "hi"})
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["n"].(float64) != 7 || got["ok"].(bool) != true {
		t.Fatalf("unexpected result: %v", got)
	}

	pub := brk.lastPublished()
	wantTopic := a2a.AgentRequestTopic(testNS, testAgent)
	if pub.topic != wantTopic {
		t.Fatalf("topic = %q, want %q", pub.topic, wantTopic)
	}
	if pub.props[a2a.PropA2AExtensions] != testURI {
		t.Fatalf("a2aExtensions = %q, want %q", pub.props[a2a.PropA2AExtensions], testURI)
	}
	if !strings.HasPrefix(pub.props[a2a.PropReplyTo], "test-ns/sam/v1/gateway/rpc/reply/") {
		t.Fatalf("replyTo = %q", pub.props[a2a.PropReplyTo])
	}
	if pub.props[a2a.PropClientID] != testGW {
		t.Fatalf("clientId = %q", pub.props[a2a.PropClientID])
	}
}

func TestClient_Call_StampsUserID(t *testing.T) {
	brk := newTestBroker()
	c := New(Config{Broker: brk, Namespace: testNS, GatewayID: testGW, Timeout: 2 * time.Second})

	brk.onPublish = func(_ string, msg *broker.Message) {
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)
		payload, _ := json.Marshal(&a2a.Response{
			JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"ok": true},
		})
		brk.answer(msg, payload)
	}

	const wantUser = "alice@example.com"
	if _, err := c.Call(t.Context(), testAgent, wantUser, "session/compact", testURI, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	pub := brk.lastPublished()
	if got := pub.props[a2a.PropUserID]; got != wantUser {
		t.Fatalf("userId on wire = %q, want %q", got, wantUser)
	}
}

func TestClient_Call_TimeoutHonoursContext(t *testing.T) {
	brk := newTestBroker()
	c := New(Config{Broker: brk, Namespace: testNS, GatewayID: testGW, Timeout: time.Hour})

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := c.Call(ctx, testAgent, "", "session/echo", testURI, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline-exceeded, got %v", err)
	}
}

func TestClient_Call_RPCErrorPropagates(t *testing.T) {
	brk := newTestBroker()
	c := New(Config{Broker: brk, Namespace: testNS, GatewayID: testGW, Timeout: 2 * time.Second})

	brk.onPublish = func(_ string, msg *broker.Message) {
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)
		errResp := &a2a.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &a2a.Error{
				Code:    -32001,
				Message: "session not found",
				Data:    map[string]string{"sessionId": "x"},
			},
		}
		payload, _ := json.Marshal(errResp)
		brk.answer(msg, payload)
	}

	_, err := c.Call(t.Context(), testAgent, "", "session/compact", testURI, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error is not *RPCError: %v", err)
	}
	if rpcErr.Code != -32001 || rpcErr.Message != "session not found" {
		t.Fatalf("unexpected RPCError: %+v", rpcErr)
	}
	if data, ok := rpcErr.Data.(map[string]any); !ok || data["sessionId"] != "x" {
		t.Fatalf("RPCError.Data not propagated: %#v", rpcErr.Data)
	}
}

func TestClient_Call_ConcurrentCorrelation(t *testing.T) {
	brk := newTestBroker()
	c := New(Config{Broker: brk, Namespace: testNS, GatewayID: testGW, Timeout: 5 * time.Second})

	brk.onPublish = func(_ string, msg *broker.Message) {
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)
		resp := &a2a.Response{JSONRPC: "2.0", ID: req.ID, Result: req.ID}
		payload, _ := json.Marshal(resp)
		brk.answer(msg, payload)
	}

	const N = 32
	var wg sync.WaitGroup
	var fails int64
	for range N {
		wg.Go(func() {
			raw, err := c.Call(t.Context(), testAgent, "", "session/echo", testURI, nil)
			if err != nil {
				atomic.AddInt64(&fails, 1)
				return
			}
			var got string
			_ = json.Unmarshal(raw, &got)
			if got == "" {
				atomic.AddInt64(&fails, 1)
			}
		})
	}
	wg.Wait()
	if fails > 0 {
		t.Fatalf("%d failures out of %d concurrent calls", fails, N)
	}
}

func TestClient_Call_ExtractedRPCID(t *testing.T) {
	brk := newTestBroker()
	c := New(Config{Broker: brk, Namespace: testNS, GatewayID: testGW, Timeout: 2 * time.Second})

	brk.onPublish = func(_ string, msg *broker.Message) {
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)
		topicID := extractRPCID(t, brokerMsg{props: msg.UserProperties})
		if topicID != req.ID {
			t.Errorf("replyTo rpcID %q != request id %q", topicID, req.ID)
		}
		resp := &a2a.Response{JSONRPC: "2.0", ID: req.ID, Result: nil}
		payload, _ := json.Marshal(resp)
		brk.answer(msg, payload)
	}
	if _, err := c.Call(t.Context(), testAgent, "", "session/echo", testURI, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
}

func TestNew_DefaultsTimeout(t *testing.T) {
	c := New(Config{Broker: newTestBroker(), Namespace: testNS, GatewayID: testGW})
	if c.timeout != DefaultTimeout {
		t.Fatalf("timeout = %v, want %v", c.timeout, DefaultTimeout)
	}
}

func TestClient_Call_RequiresArgs(t *testing.T) {
	c := New(Config{Broker: newTestBroker(), Namespace: testNS, GatewayID: testGW})
	cases := []struct {
		name             string
		agent, method, u string
	}{
		{"missing agent", "", "m", "u"},
		{"missing method", "a", "", "u"},
		{"missing uri", "a", "m", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Call(t.Context(), tc.agent, "", tc.method, tc.u, nil); err == nil {
				t.Fatal("expected error for missing arg")
			}
		})
	}

	bad := New(Config{Namespace: testNS, GatewayID: testGW})
	if _, err := bad.Call(t.Context(), "a", "", "m", "u", nil); err == nil {
		t.Fatal("expected error for nil broker")
	}
}

func TestClient_Call_MalformedResponseSurfacesError(t *testing.T) {
	brk := newTestBroker()
	c := New(Config{Broker: brk, Namespace: testNS, GatewayID: testGW, Timeout: 2 * time.Second})

	brk.onPublish = func(_ string, msg *broker.Message) {
		var req a2a.Request
		_ = json.Unmarshal(msg.Payload, &req)
		brk.answer(msg, []byte("not-json{"))
	}

	_, err := c.Call(t.Context(), testAgent, "", "session/echo", testURI, nil)
	if err == nil {
		t.Fatal("expected error from malformed response")
	}
	if !strings.Contains(err.Error(), "unmarshal response") {
		t.Fatalf("error = %v; want contains 'unmarshal response'", err)
	}
}

func TestRPCError_ErrorString(t *testing.T) {
	e := &RPCError{Code: -32001, Message: "missing"}
	if e.Error() == "" {
		t.Fatal("empty error string")
	}
}
