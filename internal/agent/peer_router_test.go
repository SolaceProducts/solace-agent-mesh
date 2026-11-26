// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/visualization"
)

func TestPeerRouterRegisterAndResponse(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	ch := router.Register("sub-1", "", "", "agent", "", "", "", "", "", 5*time.Second)

	go func() {
		time.Sleep(10 * time.Millisecond)
		router.HandleResponse("sub-1", &broker.Message{Payload: []byte("response-data")})
	}()

	select {
	case msg := <-ch:
		if msg == nil {
			t.Fatal("expected response, got nil (timeout)")
		}
		if string(msg.Payload) != "response-data" {
			t.Errorf("payload = %q, want %q", msg.Payload, "response-data")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response")
	}
}

func TestPeerRouterTimeout(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	ch := router.Register("sub-timeout", "", "", "agent", "", "", "", "", "", 50*time.Millisecond)

	select {
	case msg := <-ch:
		if msg != nil {
			t.Fatal("expected nil (timeout), got response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for timeout signal")
	}
}

func TestPeerRouterTimeoutCancelsThePeer(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	ch := router.Register("sub-silent", "", "parent-task", "Orchestrator", "SlowWorker", "", "", "", "",
		50*time.Millisecond, WithTraceID("trace-1"))

	select {
	case msg := <-ch:
		if msg != nil {
			t.Fatal("expected nil (timeout), got response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for timeout signal")
	}

	if !waitUntil(func() bool { return len(brk.getPublished()) > 0 }) {
		t.Fatal("no tasks/cancel was published after the timeout")
	}
	published := brk.getPublished()
	if len(published) != 1 {
		t.Fatalf("published %d messages after the timeout, want the one tasks/cancel: %+v", len(published), published)
	}
	got := published[0]
	if want := a2a.AgentRequestTopic("test-ns", "SlowWorker"); got.topic != want {
		t.Errorf("cancel topic = %q, want %q", got.topic, want)
	}
	var req a2a.Request
	if err := json.Unmarshal(got.payload, &req); err != nil {
		t.Fatalf("cancel payload is not JSON-RPC: %v", err)
	}
	params, _ := req.Params.(map[string]any)
	if req.Method != "tasks/cancel" || params["id"] != "sub-silent" {
		t.Errorf("published %s for %v, want tasks/cancel for sub-silent", req.Method, params["id"])
	}
	if got.userProps[a2a.PropTraceID] != "trace-1" {
		t.Errorf("cancel user properties %v carry no traceID", got.userProps)
	}
	if got.userProps["delegating_agent_name"] != "Orchestrator" {
		t.Errorf("delegating_agent_name = %q, want Orchestrator", got.userProps["delegating_agent_name"])
	}
}

type blockingPublishBroker struct {
	*mockBroker
	release chan struct{}
}

func (b *blockingPublishBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	<-b.release
	return b.mockBroker.Publish(ctx, topic, msg)
}

func (b *blockingPublishBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	return b.Publish(ctx, topic, msg)
}

func TestPeerRouterTimeoutSignalsTheCallerBeforeTheCancelPublishCompletes(t *testing.T) {
	brk := &blockingPublishBroker{mockBroker: newMockBroker(), release: make(chan struct{})}
	t.Cleanup(func() {
		close(brk.release)
		waitUntil(func() bool { return len(brk.getPublished()) == 1 })
	})
	router := NewPeerRouter(brk, "test-ns", testLogger())

	ch := router.Register("sub-stuck", "", "parent-task", "Orchestrator", "SlowWorker", "", "", "", "", 20*time.Millisecond)

	select {
	case msg := <-ch:
		if msg != nil {
			t.Fatal("expected nil (timeout), got a response")
		}
	case <-time.After(time.Second):
		t.Fatal("the caller was not told of the timeout while the cancel publish was blocked")
	}
}

func TestPeerRouterTimerThatLostTheRaceDoesNothing(t *testing.T) {
	cases := map[string]func(router *PeerRouter, subTaskID string){
		"answered": func(router *PeerRouter, subTaskID string) {
			router.HandleResponse(subTaskID, &broker.Message{Payload: []byte("answer")})
		},
		"cancelled": func(router *PeerRouter, subTaskID string) {
			router.Cancel(subTaskID)
		},
	}
	for name, settle := range cases {
		t.Run(name, func(t *testing.T) {
			brk := newMockBroker()
			router := NewPeerRouter(brk, "test-ns", testLogger())
			ch := router.Register("sub-raced", "", "parent-task", "Orchestrator", "SlowWorker", "", "", "", "", time.Hour)
			router.mu.Lock()
			pr := router.pending["sub-raced"]
			router.mu.Unlock()

			settle(router, "sub-raced")
			signalled := len(ch)
			router.timeOut("sub-raced", pr)

			if published := brk.getPublished(); len(published) != 0 {
				t.Errorf("a timer that lost the race published %+v", published)
			}
			if len(ch) != signalled {
				t.Error("a timer that lost the race signalled the waiter again")
			}
		})
	}
}

func TestPeerRouterTimeoutResetOnStatus(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	ch := router.Register("sub-reset", "gateway/status", "parent-1", "agent", "peer", "", "", "", "", 100*time.Millisecond)

	statusEvent := map[string]any{
		"result": map[string]any{
			"taskId": "sub-reset",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"parts": []any{map[string]any{
						"kind": "data",
						"data": map[string]any{"type": "llm_invocation"},
					}},
				},
			},
		},
	}
	payload, _ := json.Marshal(statusEvent)

	for range 4 {
		time.Sleep(70 * time.Millisecond)
		router.HandleStatus("sub-reset", &broker.Message{Payload: payload})
	}

	router.HandleResponse("sub-reset", &broker.Message{Payload: []byte("success")})

	select {
	case msg := <-ch:
		if msg == nil {
			t.Fatal("expected response, got nil (timeout fired despite status resets)")
		}
		if string(msg.Payload) != "success" {
			t.Errorf("payload = %q, want %q", msg.Payload, "success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response")
	}
}

func TestPeerRouterForwardUserInputResponse(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	const namespace = "test-ns"
	const peerName = "TestPeer"
	const subTaskID = "sub-hil-1"
	const reqID = "hil-approval-abc"

	router.Register(subTaskID, "gateway/status", "parent-1", "Orchestrator", peerName, "", "client-1", "user@example.com", "user-config", 5*time.Second, WithAuthToken("jwt-token"))

	uirEvent := map[string]any{
		"result": map[string]any{
			"taskId": subTaskID,
			"status": map[string]any{
				"state": "input-required",
				"message": map[string]any{
					"parts": []any{map[string]any{
						"kind": "data",
						"data": map[string]any{
							"type":       "user_input_request",
							"request_id": reqID,
						},
					}},
				},
			},
		},
	}
	uirPayload, _ := json.Marshal(uirEvent)
	router.HandleStatus(subTaskID, &broker.Message{Payload: uirPayload})

	resp := &a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: reqID,
		Status:    "answered",
		Data:      map[string]any{"decision": "approve"},
	}
	if router.ForwardUserInputResponse(context.Background(), namespace, resp, hilSenderUser("user@example.com")) != hilDelivered {
		t.Fatal("ForwardUserInputResponse returned false; expected the peer mapping to be found")
	}

	wantTopic := a2a.AgentRequestTopic(namespace, peerName)
	var found *publishedMsg
	for _, m := range brk.getPublished() {
		if m.topic == wantTopic {
			found = &m
			break
		}
	}
	if found == nil {
		t.Fatalf("no message published to peer request topic %q; published=%v", wantTopic, brk.getPublished())
	}

	var req a2a.Request
	if err := json.Unmarshal(found.payload, &req); err != nil {
		t.Fatalf("unmarshal forwarded request: %v", err)
	}
	if req.ID != subTaskID {
		t.Errorf("forwarded request task ID = %q, want peer sub-task %q", req.ID, subTaskID)
	}

	if got := found.userProps["delegating_agent_name"]; got != "Orchestrator" {
		t.Errorf("forwarded delegating_agent_name = %q, want %q", got, "Orchestrator")
	}
	if got := found.userProps["authToken"]; got != "jwt-token" {
		t.Errorf("forwarded authToken = %q, want %q", got, "jwt-token")
	}
	if got := found.userProps["userId"]; got != "user@example.com" {
		t.Errorf("forwarded userId = %q, want %q", got, "user@example.com")
	}

	if router.ForwardUserInputResponse(context.Background(), namespace, resp, hilSenderUser("user@example.com")) == hilDelivered {
		t.Error("second ForwardUserInputResponse should return false; mapping should be consumed on first delivery")
	}

	unknown := &a2a.UserInputResponseData{Type: "user_input_response", RequestID: "nope", Status: "answered"}
	if router.ForwardUserInputResponse(context.Background(), namespace, unknown, hilSenderUser("user@example.com")) == hilDelivered {
		t.Error("ForwardUserInputResponse for unknown requestID should return false")
	}
}

func uirPayload(subTaskID, reqID string) []byte {
	p, _ := json.Marshal(map[string]any{
		"result": map[string]any{
			"taskId": subTaskID,
			"status": map[string]any{
				"state": "input-required",
				"message": map[string]any{
					"parts": []any{map[string]any{
						"kind": "data",
						"data": map[string]any{"type": "user_input_request", "request_id": reqID},
					}},
				},
			},
		},
	})
	return p
}

func TestPeerRouterForwardUserInputResponsePublishError(t *testing.T) {
	brk := newMockBroker()
	brk.publishErr = errors.New("broker down")
	router := NewPeerRouter(brk, "test-ns", testLogger())

	router.Register("sub-pub-err", "gateway/status", "parent-1", "Orchestrator", "Peer", "", "", "", "", 5*time.Second)
	router.HandleStatus("sub-pub-err", &broker.Message{Payload: uirPayload("sub-pub-err", "req-pub-err")})

	resp := &a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-pub-err", Status: "answered"}
	if router.ForwardUserInputResponse(context.Background(), "ns", resp, hilSenderUser("")) == hilDelivered {
		t.Error("expected false when broker publish fails")
	}
}

func TestPeerRouterTeardownClearsHILMapping(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	router.Register("sub-teardown", "gateway/status", "parent-1", "Orchestrator", "Peer", "", "", "", "", 5*time.Second)
	router.HandleStatus("sub-teardown", &broker.Message{Payload: uirPayload("sub-teardown", "req-teardown")})

	router.Cancel("sub-teardown")

	resp := &a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-teardown", Status: "answered"}
	if router.ForwardUserInputResponse(context.Background(), "ns", resp, hilSenderUser("")) == hilDelivered {
		t.Error("expected false after teardown cleared the HIL mapping")
	}
}

func TestInterceptControlMessage(t *testing.T) {
	agent := New(AgentConfig{Name: "A", Namespace: "ns"})
	agent.logger = testLogger()
	ctx := context.Background()

	plain := a2a.Message{Kind: "message", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hello")}}
	if handled, _, _ := agent.interceptControlMessage(ctx, "t1", plain, a2a.UserProperties{}, agent.logger); handled {
		t.Error("plain message should not be intercepted as a control message")
	}

	respData := map[string]any{"type": "user_input_response", "request_id": "no-match", "status": "answered"}
	msg := a2a.Message{Kind: "message", Role: a2a.RoleUser, Parts: []a2a.Part{{Kind: "data", Data: respData}}}
	handled, earlyResp, err := agent.interceptControlMessage(ctx, "t1", msg, a2a.UserProperties{}, agent.logger)
	if !handled {
		t.Error("user_input_response should be intercepted even when unmatched")
	}
	if earlyResp != nil || err != nil {
		t.Errorf("unmatched HIL response should yield nil earlyResp/err, got %v / %v", earlyResp, err)
	}
}

func TestUserInputRequestIDFromStatus(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    string
	}{
		{"happy path", uirPayload("sub-1", "req-xyz"), "req-xyz"},
		{"malformed json", []byte("{not json"), ""},
		{"missing result", []byte(`{"id":"x"}`), ""},
		{"missing status", []byte(`{"result":{}}`), ""},
		{"missing message", []byte(`{"result":{"status":{}}}`), ""},
		{"missing parts", []byte(`{"result":{"status":{"message":{}}}}`), ""},
		{"non-map part", []byte(`{"result":{"status":{"message":{"parts":["x"]}}}}`), ""},
		{"part without data", []byte(`{"result":{"status":{"message":{"parts":[{"kind":"text"}]}}}}`), ""},
		{"wrong data type", []byte(`{"result":{"status":{"message":{"parts":[{"data":{"type":"llm_invocation"}}]}}}}`), ""},
		{"no request_id", []byte(`{"result":{"status":{"message":{"parts":[{"data":{"type":"user_input_request"}}]}}}}`), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := UserInputRequestIDFromStatus(tc.payload); got != tc.want {
				t.Errorf("UserInputRequestIDFromStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPeerRouterCancel(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	ch := router.Register("sub-cancel", "", "", "agent", "", "", "", "", "", 5*time.Second)
	router.Cancel("sub-cancel")

	router.HandleResponse("sub-cancel", &broker.Message{Payload: []byte("late")})

	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPeerRouterCancelAll(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	ch1 := router.Register("sub-a", "", "", "agent", "", "", "", "", "", 5*time.Second)
	ch2 := router.Register("sub-b", "", "", "agent", "", "", "", "", "", 5*time.Second)

	router.CancelAll()

	for i, ch := range []<-chan *broker.Message{ch1, ch2} {
		select {
		case msg := <-ch:
			if msg != nil {
				t.Errorf("channel %d: expected nil, got response", i)
			}
		case <-time.After(1 * time.Second):
			t.Errorf("channel %d: timed out waiting for cancel signal", i)
		}
	}
}

func TestPeerRouter_AuthPromptDroppedAfterOrphan(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	authEvent := func() []byte {
		ev := map[string]any{
			"id": "sub-void",
			"result": map[string]any{
				"taskId": "sub-void",
				"kind":   "status-update",
				"status": map[string]any{
					"state": "input-required",
					"message": map[string]any{
						"parts": []any{map[string]any{
							"kind": "data",
							"data": map[string]any{"type": "authentication_required"},
						}},
					},
				},
			},
		}
		b, _ := json.Marshal(ev)
		return b
	}

	router.Register("sub-void", "gateway/status/topic", "parent-task", "orchestrator",
		"peer", "tool-call", "client", "user", "config", 50*time.Millisecond)

	router.HandleStatus("sub-void", &broker.Message{Payload: authEvent()})
	time.Sleep(30 * time.Millisecond)
	firstCount := publishedTo(brk, "gateway/status/topic")
	if firstCount == 0 {
		t.Fatal("first auth prompt (peer request live) must be forwarded to the gateway")
	}

	time.Sleep(120 * time.Millisecond)

	router.HandleStatus("sub-void", &broker.Message{Payload: authEvent()})
	time.Sleep(30 * time.Millisecond)
	if secondCount := publishedTo(brk, "gateway/status/topic"); secondCount != firstCount {
		t.Errorf("re-prompt after orphan must be DROPPED (into the void); forwarded count went %d→%d", firstCount, secondCount)
	}
	if publishedTo(brk, a2a.AgentRequestTopic("test-ns", "peer")) != 1 {
		t.Error("the timed-out sub-task was not cancelled, so it keeps re-prompting with nobody to see it")
	}
}

func publishedTo(brk *mockBroker, topic string) int {
	n := 0
	for _, p := range brk.getPublished() {
		if p.topic == topic {
			n++
		}
	}
	return n
}

func TestPeerRouterStatusForwarding(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	router.Register("sub-status", "gateway/status/topic", "parent-task-123", "orchestrator-agent",
		"peer-agent", "tool-call-42", "client-1", "user-1", "config-json", 5*time.Second)

	statusEvent := map[string]any{
		"id": "sub-status",
		"result": map[string]any{
			"taskId":    "sub-status",
			"contextId": "orig-session:sub-status:run",
			"kind":      "status-update",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId":    "sub-status",
					"contextId": "orig-session:sub-status:run",
					"parts": []any{map[string]any{
						"kind": "data",
						"data": map[string]any{"type": "llm_invocation"},
					}},
				},
			},
		},
	}
	payload, _ := json.Marshal(statusEvent)

	router.HandleStatus("sub-status", &broker.Message{Payload: payload})

	time.Sleep(50 * time.Millisecond)

	brk.mu.Lock()
	defer brk.mu.Unlock()

	if len(brk.published) == 0 {
		t.Fatal("expected forwarded status event, got none")
	}

	pub := brk.published[0]
	if pub.topic != "gateway/status/topic" {
		t.Errorf("published to %q, want %q", pub.topic, "gateway/status/topic")
	}

	if got := pub.userProps["userId"]; got != "user-1" {
		t.Errorf("forwarded userProps[userId] = %q, want %q", got, "user-1")
	}

	var forwarded map[string]any
	if err := json.Unmarshal(pub.payload, &forwarded); err != nil {
		t.Fatal(err)
	}

	if got := visualization.ExtractEventUserID(pub.userProps, forwarded); got != "user-1" {
		t.Errorf("ExtractEventUserID(forwarded) = %q, want %q", got, "user-1")
	}

	if forwarded["id"] != "parent-task-123" {
		t.Errorf("envelope id = %v, want %q", forwarded["id"], "parent-task-123")
	}

	result, _ := forwarded["result"].(map[string]any)
	if result["taskId"] != "parent-task-123" {
		t.Errorf("outer taskId = %v, want %q", result["taskId"], "parent-task-123")
	}
	if result["contextId"] != "orig-session" {
		t.Errorf("outer contextId = %v, want %q", result["contextId"], "orig-session")
	}
	status, _ := result["status"].(map[string]any)
	msg, _ := status["message"].(map[string]any)
	if _, ok := msg["taskId"]; ok {
		t.Errorf("inner message should not have taskId, got %v", msg["taskId"])
	}
	if _, ok := msg["contextId"]; ok {
		t.Errorf("inner message should not have contextId, got %v", msg["contextId"])
	}

	meta, _ := msg["metadata"].(map[string]any)
	if meta == nil {
		t.Fatal("expected metadata on inner message")
	}
	if meta["agent_name"] != "orchestrator-agent" {
		t.Errorf("metadata.agent_name = %v, want %q", meta["agent_name"], "orchestrator-agent")
	}
	if meta["forwarded_from_peer"] != "peer-agent" {
		t.Errorf("metadata.forwarded_from_peer = %v, want %q", meta["forwarded_from_peer"], "peer-agent")
	}
	if meta["function_call_id"] != "tool-call-42" {
		t.Errorf("metadata.function_call_id = %v, want %q", meta["function_call_id"], "tool-call-42")
	}
	if result["metadata"] != nil {
		t.Errorf("top-level result.metadata should be nil, got %v", result["metadata"])
	}
}

func TestPeerRouterStatusForwarding_EmptyUserIDOmitted(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	router.Register("sub-anon", "gateway/status/topic", "parent-task-123", "orchestrator-agent",
		"peer-agent", "tool-call-42", "client-1", "", "config-json", 5*time.Second)

	statusEvent := map[string]any{
		"id": "sub-anon",
		"result": map[string]any{
			"taskId": "sub-anon",
			"kind":   "status-update",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"parts": []any{map[string]any{
						"kind": "data",
						"data": map[string]any{"type": "llm_invocation"},
					}},
				},
			},
		},
	}
	payload, _ := json.Marshal(statusEvent)
	router.HandleStatus("sub-anon", &broker.Message{Payload: payload})
	time.Sleep(50 * time.Millisecond)

	brk.mu.Lock()
	defer brk.mu.Unlock()
	if len(brk.published) == 0 {
		t.Fatal("expected forwarded status event, got none")
	}
	if v, ok := brk.published[0].userProps["userId"]; ok {
		t.Errorf("userId should be omitted when caller userID is empty, got %q", v)
	}
}

func TestPeerRouterConcurrentRequests(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	const n = 10
	channels := make([]<-chan *broker.Message, n)
	for i := range n {
		id := "concurrent-" + string(rune('A'+i))
		channels[i] = router.Register(id, "", "", "agent", "", "", "", "", "", 5*time.Second)
	}

	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			id := "concurrent-" + string(rune('A'+i))
			router.HandleResponse(id, &broker.Message{Payload: []byte(id)})
		})
	}
	wg.Wait()

	for i := range n {
		id := "concurrent-" + string(rune('A'+i))
		select {
		case msg := <-channels[i]:
			if msg == nil {
				t.Errorf("channel %d: expected response, got nil", i)
			} else if string(msg.Payload) != id {
				t.Errorf("channel %d: payload = %q, want %q", i, msg.Payload, id)
			}
		case <-time.After(1 * time.Second):
			t.Errorf("channel %d: timed out", i)
		}
	}
}

func TestPeerRouterUnknownResponse(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	router.HandleResponse("unknown-id", &broker.Message{Payload: []byte("data")})
}

func TestPeerRouterUnknownStatus(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())

	router.HandleStatus("unknown-id", &broker.Message{Payload: []byte("data")})
}

func TestExtractSubTaskIDFromTopic(t *testing.T) {
	tests := []struct {
		topic string
		want  string
	}{
		{"ns/a2a/v1/agent/response/myAgent/peer-req-123", "peer-req-123"},
		{"ns/a2a/v1/agent/status/myAgent/wf-req-456", "wf-req-456"},
		{"", ""},
		{"no-slash", ""},
	}
	for _, tt := range tests {
		got := extractSubTaskIDFromTopic(tt.topic)
		if got != tt.want {
			t.Errorf("extractSubTaskIDFromTopic(%q) = %q, want %q", tt.topic, got, tt.want)
		}
	}
}

func TestIsPeerStatusUpdate(t *testing.T) {
	tests := []struct {
		topic     string
		namespace string
		agent     string
		want      bool
	}{
		{"ns/a2a/v1/agent/status/myAgent/sub-1", "ns", "myAgent", true},
		{"ns/a2a/v1/agent/status/otherAgent/sub-1", "ns", "myAgent", false},
		{"ns/a2a/v1/agent/response/myAgent/sub-1", "ns", "myAgent", false},
		{"ns/a2a/v1/agent/request/myAgent", "ns", "myAgent", false},
	}
	for _, tt := range tests {
		got := isPeerStatusUpdate(tt.topic, tt.namespace, tt.agent)
		if got != tt.want {
			t.Errorf("isPeerStatusUpdate(%q, %q, %q) = %v, want %v",
				tt.topic, tt.namespace, tt.agent, got, tt.want)
		}
	}
}

func TestIsAgentResponseMsg(t *testing.T) {
	tests := []struct {
		method string
		id     string
		want   bool
	}{
		{"", "req-123", true},
		{"message/send", "req-123", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got := isAgentResponseMsg(tt.method, tt.id)
		if got != tt.want {
			t.Errorf("isAgentResponseMsg(%q, %q) = %v, want %v",
				tt.method, tt.id, got, tt.want)
		}
	}
}

func TestRewriteStatusEvent_MetadataAndTaskID(t *testing.T) {
	event := map[string]any{
		"id": "sub-1",
		"result": map[string]any{
			"taskId":    "sub-1",
			"contextId": "orig-ctx:sub-1:run",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId":    "sub-1",
					"contextId": "orig-ctx:sub-1:run",
					"parts": []any{map[string]any{
						"kind": "data",
						"data": map[string]any{"type": "llm_response"},
					}},
				},
			},
			"metadata": map[string]any{
				"agent_name": "peer-agent",
			},
		},
	}
	payload, _ := json.Marshal(event)

	rewritten, _ := rewriteStatusEvent(payload, "parent-task", "orchestrator", "peer-agent", "call-99", false)
	if rewritten == nil {
		t.Fatal("expected rewritten payload, got nil")
	}

	var envelope map[string]any
	if err := json.Unmarshal(rewritten, &envelope); err != nil {
		t.Fatal(err)
	}

	if envelope["id"] != "parent-task" {
		t.Errorf("envelope id = %v, want %q", envelope["id"], "parent-task")
	}

	inner := envelope["result"].(map[string]any)

	if inner["taskId"] != "parent-task" {
		t.Errorf("taskId = %v, want %q", inner["taskId"], "parent-task")
	}

	if inner["contextId"] != "orig-ctx" {
		t.Errorf("contextId = %v, want %q", inner["contextId"], "orig-ctx")
	}

	status := inner["status"].(map[string]any)
	msg := status["message"].(map[string]any)
	if _, ok := msg["contextId"]; ok {
		t.Errorf("inner message should not have contextId, got %v", msg["contextId"])
	}
	if _, ok := msg["taskId"]; ok {
		t.Errorf("inner message should not have taskId, got %v", msg["taskId"])
	}

	meta := msg["metadata"].(map[string]any)
	if meta["agent_name"] != "orchestrator" {
		t.Errorf("agent_name = %v, want %q", meta["agent_name"], "orchestrator")
	}
	if meta["forwarded_from_peer"] != "peer-agent" {
		t.Errorf("forwarded_from_peer = %v, want %q", meta["forwarded_from_peer"], "peer-agent")
	}
	if meta["function_call_id"] != "call-99" {
		t.Errorf("function_call_id = %v, want %q", meta["function_call_id"], "call-99")
	}
	if inner["metadata"] != nil {
		t.Errorf("top-level result.metadata should be nil, got %v", inner["metadata"])
	}
}

func TestRewriteStatusEvent_WorkflowSignalsForwarded(t *testing.T) {
	cases := []string{
		"workflow_execution_start",
		"workflow_execution_result",
		"workflow_node_execution_start",
		"workflow_node_execution_result",
		"workflow_map_progress",
	}
	for _, sig := range cases {
		t.Run(sig, func(t *testing.T) {
			event := map[string]any{
				"result": map[string]any{
					"taskId": "sub-1",
					"status": map[string]any{
						"state": "working",
						"message": map[string]any{
							"taskId": "sub-1",
							"parts": []any{
								map[string]any{
									"kind": "data",
									"data": map[string]any{
										"type": sig,
									},
								},
							},
						},
					},
				},
			}
			payload, _ := json.Marshal(event)

			rewritten, gotSig := rewriteStatusEvent(payload, "parent-task", "orchestrator", "peer-wf", "call-1", false)
			if rewritten == nil {
				t.Fatalf("workflow signal %q must be forwarded, got nil (sig=%q)", sig, gotSig)
			}

			var envelope map[string]any
			if err := json.Unmarshal(rewritten, &envelope); err != nil {
				t.Fatal(err)
			}
			inner := envelope["result"].(map[string]any)
			if inner["taskId"] != "parent-task" {
				t.Errorf("taskId = %v, want %q", inner["taskId"], "parent-task")
			}
			meta := inner["status"].(map[string]any)["message"].(map[string]any)["metadata"].(map[string]any)
			if meta["forwarded_from_peer"] != "peer-wf" {
				t.Errorf("forwarded_from_peer = %v, want %q", meta["forwarded_from_peer"], "peer-wf")
			}
			if meta["original_peer_event_taskId"] != "sub-1" {
				t.Errorf("original_peer_event_taskId = %v, want %q", meta["original_peer_event_taskId"], "sub-1")
			}
			chain, ok := meta["peer_task_chain"].([]any)
			if !ok || len(chain) != 1 || chain[0] != "sub-1" {
				t.Errorf("peer_task_chain = %v, want [sub-1]", meta["peer_task_chain"])
			}
		})
	}
}

func TestRewriteStatusEvent_TwoWorkflowsStaySeparate(t *testing.T) {
	mkEvent := func(subTaskID, nodeID string) []byte {
		event := map[string]any{
			"result": map[string]any{
				"taskId": subTaskID,
				"status": map[string]any{
					"state": "working",
					"message": map[string]any{
						"taskId": subTaskID,
						"parts": []any{
							map[string]any{
								"kind": "data",
								"data": map[string]any{
									"type":    "workflow_node_execution_start",
									"node_id": nodeID,
								},
							},
						},
					},
				},
			},
		}
		b, _ := json.Marshal(event)
		return b
	}

	chains := map[string]string{}
	peers := map[string]string{
		"SimpleSequentialWorkflow":  "seq-sub",
		"ParallelExecutionWorkflow": "par-sub",
	}
	for peer, subTaskID := range peers {
		rewritten, _ := rewriteStatusEvent(mkEvent(subTaskID, "validate"), "parent-task", "Orchestrator", peer, "call-x", false)
		if rewritten == nil {
			t.Fatalf("%s: workflow_* signal must be forwarded, got nil", peer)
		}
		var envelope map[string]any
		if err := json.Unmarshal(rewritten, &envelope); err != nil {
			t.Fatal(err)
		}
		meta := envelope["result"].(map[string]any)["status"].(map[string]any)["message"].(map[string]any)["metadata"].(map[string]any)
		leaf, _ := meta["original_peer_event_taskId"].(string)
		if leaf != subTaskID {
			t.Errorf("%s: original_peer_event_taskId = %q, want %q", peer, leaf, subTaskID)
		}
		chains[peer] = leaf
	}
	if chains["SimpleSequentialWorkflow"] == chains["ParallelExecutionWorkflow"] {
		t.Errorf("two workflows collapsed onto the same origin task_id %q", chains["SimpleSequentialWorkflow"])
	}
}

func TestRewriteStatusEvent_FiltersArtifactCreationProgress(t *testing.T) {
	event := map[string]any{
		"result": map[string]any{
			"taskId": "sub-1",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId": "sub-1",
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{
								"type": "artifact_creation_progress",
							},
						},
					},
				},
			},
		},
	}
	payload, _ := json.Marshal(event)

	rewritten, _ := rewriteStatusEvent(payload, "parent-task", "orch", "peer", "call-1", false)
	if rewritten != nil {
		t.Errorf("expected nil (filtered), got payload")
	}
}

func artifactProgressEvent(filename string) map[string]any {
	return map[string]any{
		"result": map[string]any{
			"taskId": "sub-1",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId": "sub-1",
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{
								"type":     "artifact_creation_progress",
								"filename": filename,
							},
						},
					},
				},
			},
		},
	}
}

func TestRewriteStatusEvent_ForwardsArtifactProgressWhenOptedIn(t *testing.T) {
	payload, _ := json.Marshal(artifactProgressEvent("draft_report.md"))

	rewritten, sigType := rewriteStatusEvent(payload, "parent-task", "orch", "peer", "call-1", true)
	if rewritten == nil {
		t.Fatalf("expected a real content artifact to be forwarded when opted in, got nil (sigType=%q)", sigType)
	}
	if sigType != "artifact_creation_progress" {
		t.Errorf("sigType = %q, want artifact_creation_progress", sigType)
	}
}

func TestRewriteStatusEvent_StillFiltersWorkflowHandoffArtifactWhenOptedIn(t *testing.T) {
	payload, _ := json.Marshal(artifactProgressEvent("wf_write_draft_output_abc123.json"))

	rewritten, sigType := rewriteStatusEvent(payload, "parent-task", "orch", "peer", "call-1", true)
	if rewritten != nil {
		t.Errorf("expected a wf_-prefixed handoff artifact to stay filtered even when opted in, got payload (sigType=%q)", sigType)
	}
}

func artifactProgressEventWithVersion(filename, status string, version any) map[string]any {
	return map[string]any{
		"result": map[string]any{
			"taskId": "sub-1",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId": "sub-1",
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{
								"type":     "artifact_creation_progress",
								"filename": filename,
								"status":   status,
								"version":  version,
							},
						},
					},
				},
			},
		},
	}
}

func TestRewriteStatusEvent_StillFiltersUnpersistedCompletedWhenOptedIn(t *testing.T) {
	payload, _ := json.Marshal(artifactProgressEventWithVersion("draft_report.md", "completed", nil))

	rewritten, sigType := rewriteStatusEvent(payload, "parent-task", "orch", "peer", "call-1", true)
	if rewritten != nil {
		t.Errorf("expected a completed event with no version to stay filtered even when opted in, got payload (sigType=%q)", sigType)
	}
}

func TestRewriteStatusEvent_ForwardsPersistedCompletedWhenOptedIn(t *testing.T) {
	payload, _ := json.Marshal(artifactProgressEventWithVersion("draft_report.md", "completed", 3))

	rewritten, sigType := rewriteStatusEvent(payload, "parent-task", "orch", "peer", "call-1", true)
	if rewritten == nil {
		t.Fatalf("expected a completed event with a real version to be forwarded, got nil (sigType=%q)", sigType)
	}
}

func TestRewriteStatusEvent_FiltersPlainTextEvents(t *testing.T) {
	event := map[string]any{
		"result": map[string]any{
			"taskId": "sub-1",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId": "sub-1",
					"parts": []any{
						map[string]any{"kind": "text", "text": "streaming text..."},
					},
				},
			},
		},
	}
	payload, _ := json.Marshal(event)

	rewritten, _ := rewriteStatusEvent(payload, "parent-task", "orch", "peer", "call-1", false)
	if rewritten != nil {
		t.Errorf("expected nil (plain text filtered), got payload")
	}
}

func testLogger() *slog.Logger {
	return slog.Default().With("test", true)
}

func TestPeerRouterRegister_WithForwardArtifactProgress(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())
	ch := router.Register("sub-fap", "", "", "agent", "peer", "", "", "", "", 5*time.Second, WithForwardArtifactProgress())
	if ch == nil {
		t.Fatal("Register returned nil channel")
	}
	router.CancelAll()
}

func TestRewriteStatusEvent_PreservesNestedPeerChain(t *testing.T) {
	cEvent := map[string]any{
		"result": map[string]any{
			"taskId": "task-C",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId": "task-C",
					"parts": []any{map[string]any{"kind": "data", "data": map[string]any{
						"type": "tool_invocation_start", "tool_name": "x", "function_call_id": "fc",
					}}},
				},
			},
		},
	}
	payload, _ := json.Marshal(cEvent)

	hop1, sig := rewriteStatusEvent(payload, "task-B", "agentB", "agentC", "", false)
	if hop1 == nil {
		t.Fatalf("hop1 unexpectedly filtered (sig=%s)", sig)
	}
	m1 := peerInnerMeta(t, hop1)
	if m1["original_peer_event_taskId"] != "task-C" {
		t.Errorf("hop1 original_peer_event_taskId = %v, want task-C", m1["original_peer_event_taskId"])
	}
	if !peerChainEquals(m1["peer_task_chain"], []string{"task-C"}) {
		t.Errorf("hop1 peer_task_chain = %v, want [task-C]", m1["peer_task_chain"])
	}

	hop2, _ := rewriteStatusEvent(hop1, "task-A", "agentA", "agentB", "", false)
	if hop2 == nil {
		t.Fatal("hop2 unexpectedly filtered")
	}
	m2 := peerInnerMeta(t, hop2)
	if m2["original_peer_event_taskId"] != "task-C" {
		t.Errorf("hop2 original_peer_event_taskId = %v, want task-C (leaf preserved across re-forward)", m2["original_peer_event_taskId"])
	}
	if !peerChainEquals(m2["peer_task_chain"], []string{"task-C", "task-B"}) {
		t.Errorf("hop2 peer_task_chain = %v, want [task-C task-B]", m2["peer_task_chain"])
	}
}

func peerInnerMeta(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatal(err)
	}
	return env["result"].(map[string]any)["status"].(map[string]any)["message"].(map[string]any)["metadata"].(map[string]any)
}

func peerChainEquals(v any, want []string) bool {
	arr, ok := v.([]any)
	if !ok || len(arr) != len(want) {
		return false
	}
	for i, w := range want {
		if arr[i] != w {
			return false
		}
	}
	return true
}

func TestRewriteStatusEventKeepsWorkflowToolNodeAttribution(t *testing.T) {
	attribution := map[string]any{
		"agent_name": "wf", "workflow_name": "wf", "node_id": "lookup_node", "node_type": "tool",
		"sub_task_id": "sub-1", "retry_group_id": "g-1", "retry_attempt": float64(0),
	}
	event := map[string]any{
		"jsonrpc": "2.0",
		"id":      "wf-task",
		"result": map[string]any{
			"taskId":    "wf-task",
			"contextId": "orig-ctx:wf-task:run",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"parts":    []any{map[string]any{"kind": "data", "data": map[string]any{"type": "tool_invocation_start", "function_call_id": "sub-1"}}},
					"metadata": attribution,
				},
			},
			"metadata": attribution,
		},
	}
	payload, _ := json.Marshal(event)

	rewritten, _ := rewriteStatusEvent(payload, "parent-task", "orchestrator", "wf", "call-7", false)

	var out struct {
		Result struct {
			Metadata map[string]any `json:"metadata"`
			Status   struct {
				Message struct {
					Metadata map[string]any `json:"metadata"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rewritten, &out); err != nil {
		t.Fatal(err)
	}
	meta := out.Result.Status.Message.Metadata
	for _, key := range []string{"node_type", "node_id", "workflow_name", "sub_task_id", "retry_group_id", "retry_attempt"} {
		if meta[key] != attribution[key] {
			t.Errorf("forwarded %s = %v, want %v", key, meta[key], attribution[key])
		}
	}
	if meta["forwarded_from_peer"] != "wf" || meta["function_call_id"] != "call-7" {
		t.Errorf("forwarding keys changed: %#v", meta)
	}
}

func TestRewriteStatusEventAddsNoAttributionToOtherEvents(t *testing.T) {
	event := map[string]any{
		"jsonrpc": "2.0",
		"id":      "sub-1",
		"result": map[string]any{
			"taskId": "sub-1",
			"status": map[string]any{
				"state":   "working",
				"message": map[string]any{"parts": []any{map[string]any{"kind": "data", "data": map[string]any{"type": "tool_invocation_start"}}}},
			},
			"metadata": map[string]any{"agent_name": "peer-agent", "node_id": "n1"},
		},
	}
	payload, _ := json.Marshal(event)
	rewritten, _ := rewriteStatusEvent(payload, "parent-task", "orchestrator", "peer-agent", "call-1", false)
	if strings.Contains(string(rewritten), "node_id") {
		t.Errorf("an event not tagged as a workflow tool node gained attribution: %s", rewritten)
	}
}

func TestRewriteStatusEventKeepsWorkflowToolNodeAttributionAcrossTwoHops(t *testing.T) {
	attribution := map[string]any{"workflow_name": "wf", "node_id": "lookup_node", "node_type": "tool", "sub_task_id": "sub-1"}
	event := map[string]any{
		"jsonrpc": "2.0",
		"id":      "wf-task",
		"result": map[string]any{
			"taskId": "wf-task",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"parts":    []any{map[string]any{"kind": "data", "data": map[string]any{"type": "tool_result", "function_call_id": "sub-1"}}},
					"metadata": attribution,
				},
			},
			"metadata": attribution,
		},
	}
	payload, _ := json.Marshal(event)
	firstHop, _ := rewriteStatusEvent(payload, "b-task", "agent-b", "wf", "call-1", false)
	secondHop, _ := rewriteStatusEvent(firstHop, "a-task", "agent-a", "agent-b", "call-2", false)

	var out struct {
		Result struct {
			Status struct {
				Message struct {
					Metadata map[string]any `json:"metadata"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(secondHop, &out); err != nil {
		t.Fatal(err)
	}
	for key, want := range attribution {
		if got := out.Result.Status.Message.Metadata[key]; got != want {
			t.Errorf("after two hops %s = %v, want %v", key, got, want)
		}
	}
}

type stalledPublishBroker struct {
	*mockBroker
}

func (b stalledPublishBroker) Publish(ctx context.Context, _ string, _ *broker.Message) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestPeerRouterCancelWithPropagationWithinGivesUpWhenItsContextEnds(t *testing.T) {
	router := NewPeerRouter(stalledPublishBroker{newMockBroker()}, "test-ns", testLogger())
	done := router.Register("sub-1", "", "parent-1", "agent", "peer", "", "", "", "", time.Minute)
	const within = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()

	began := time.Now()
	router.CancelWithPropagationWithin(ctx, "sub-1", "test-ns")
	if took := time.Since(began); took > peerCancelPublishWait/2 {
		t.Fatalf("the cancel took %v on a stalled broker, want about the %v its context allowed", took, within)
	}
	select {
	case msg := <-done:
		if msg != nil {
			t.Fatalf("the cancelled call completed with %s, want nil", msg.Payload)
		}
	default:
		t.Fatal("the cancelled call was not completed")
	}
}
