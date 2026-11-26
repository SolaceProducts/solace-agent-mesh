// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func notifyEnabledRegistry(enabled bool) *features.Registry {
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "notify_service", Default: enabled})
	return reg
}

func TestNotifyHILEnabled_GatedOnFlag(t *testing.T) {
	agent, _, _ := newTestAgent()
	if agent.notifyHILEnabled() {
		t.Error("notifyHILEnabled must be false without a feature registry")
	}

	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.featureRegistry = notifyEnabledRegistry(false)
	agent.svc = svc
	if agent.notifyHILEnabled() {
		t.Error("notifyHILEnabled must be false when notify_service is off")
	}
	svc.featureRegistry = notifyEnabledRegistry(true)
	if !agent.notifyHILEnabled() {
		t.Error("notifyHILEnabled must be true when notify_service is on")
	}
}

func TestHandleNotifyHILResponse_DeliversAndGuards(t *testing.T) {
	agent, _, _ := newTestAgent()

	ch := agent.registerHILRequest("req-1", hilTestOwner, "")
	defer agent.unregisterHILRequest("req-1")

	payload, _ := json.Marshal(a2a.UserInputResponseData{
		Type: "user_input_response", RequestID: "req-1", Status: "answered",
		Data: map[string]any{"choice": "approve"},
	})
	agent.handleNotifyHILResponse(&broker.Message{
		Topic:   a2a.NotifyResponseTopic("test-ns", "test-agent", "req-1"),
		Payload: payload,
	})
	select {
	case resp := <-ch:
		if resp.Status != "answered" || resp.Data["choice"] != "approve" {
			t.Fatalf("delivered response mismatch: %+v", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("routed HIL response was not delivered to the pending channel")
	}

	agent.handleNotifyHILResponse(&broker.Message{Payload: []byte(`{not json`)})
	missing, _ := json.Marshal(a2a.UserInputResponseData{Status: "answered"})
	agent.handleNotifyHILResponse(&broker.Message{Payload: missing})
	unknown, _ := json.Marshal(a2a.UserInputResponseData{RequestID: "ghost", Status: "answered"})
	agent.handleNotifyHILResponse(&broker.Message{Payload: unknown})
}

func TestPublishHILNotifyRequest_PublishesInputModeRequest(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	agent := New(AgentConfig{Name: "hil-agent", Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	svc.featureRegistry = notifyEnabledRegistry(true)
	agent.svc = svc
	if err := agent.Init(ctx, nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	q, err := brk.CreateQueue("t-notify-req", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.NotifyRequestSubscription("test-ns")); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	surface := json.RawMessage(`{"surfaceId":"s1"}`)
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	agent.publishHILNotifyRequest(ctx, hilRequest{requestID: "req-P", source: "ask_user_question", recipientUserID: "trent@example.org", traceID: "trace-P", surface: surface}, expires)

	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	msg, err := brk.Receive(rctx, q)
	if err != nil {
		t.Fatalf("did not receive the published NotifyRequest: %v", err)
	}
	var req a2a.NotifyRequest
	if uerr := json.Unmarshal(msg.Payload, &req); uerr != nil {
		t.Fatalf("unmarshal NotifyRequest: %v", uerr)
	}
	if req.Mode != a2a.NotifyModeInput {
		t.Errorf("Mode = %q, want input", req.Mode)
	}
	if req.RecipientUserID != "trent@example.org" {
		t.Errorf("RecipientUserID = %q", req.RecipientUserID)
	}
	if want := a2a.NotifyResponseTopic("test-ns", "hil-agent", "req-P"); req.RequesterAddr != want {
		t.Errorf("RequesterAddr = %q, want %q", req.RequesterAddr, want)
	}
	if req.ExpiresAt != expires || len(req.Surface) == 0 {
		t.Errorf("expires/surface not propagated: expiresAt=%q surfaceLen=%d", req.ExpiresAt, len(req.Surface))
	}
	if req.Metadata["trace_id"] != "trace-P" {
		t.Errorf("trace_id must be stamped on the NotifyRequest metadata for log correlation, got %v", req.Metadata["trace_id"])
	}
	if msg.UserProperties[a2a.PropTraceID] != "trace-P" {
		t.Errorf("trace_id must be forwarded on broker UserProperties, got %q", msg.UserProperties[a2a.PropTraceID])
	}
}

func TestNotifyHIL_EndToEndCrossPodLoop(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	notifyCore := notify.NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + t.TempDir() + "/notify.db",
	}, notify.NotifyExtensions{})
	notifyCore.SetServices(rt.Services())
	if err := notifyCore.Init(ctx, nil); err != nil {
		t.Fatalf("notify Init: %v", err)
	}
	if err := notifyCore.Start(ctx); err != nil {
		t.Fatalf("notify Start: %v", err)
	}
	t.Cleanup(func() { _ = notifyCore.Stop(context.Background()) })

	agent := New(AgentConfig{Name: "hil-agent", Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	svc.featureRegistry = notifyEnabledRegistry(true)
	agent.svc = svc
	if err := agent.Init(ctx, nil, svc); err != nil {
		t.Fatalf("agent Init: %v", err)
	}
	if err := agent.Start(ctx); err != nil {
		t.Fatalf("agent Start: %v", err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })

	const requestID = "e2e-hil-1"
	ch := agent.registerHILRequest(requestID, hilTestOwner, "")
	defer agent.unregisterHILRequest(requestID)
	agent.publishHILNotifyRequest(ctx, hilRequest{
		requestID: requestID, source: "ask_user_question", recipientUserID: "trent@example.org", traceID: "trace-e2e",
		surface: json.RawMessage(`{"surfaceId":"e2e-hil-1"}`),
	}, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))

	replyPayload, _ := json.Marshal(a2a.UserInputResponseData{
		Type: "user_input_response", RequestID: requestID, Status: "answered",
		Data: map[string]any{"choice": "approve"},
	})
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := brk.PublishGuaranteed(ctx, a2a.NotifyReplyTopic("test-ns", requestID),
			&broker.Message{Payload: replyPayload}); err != nil {
			t.Fatalf("publish reply: %v", err)
		}
		select {
		case resp := <-ch:
			if resp == nil || resp.Status != "answered" || resp.Data["choice"] != "approve" {
				t.Fatalf("cross-pod routed response mismatch: %+v", resp)
			}
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("HIL answer was not routed back to the agent through the notification service")
}

func TestPublishHILNotifyRequest_WebInboxOnlyRestrictsChannels(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	agent := New(AgentConfig{Name: "hil-agent", Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	svc.featureRegistry = notifyEnabledRegistry(true)
	agent.svc = svc
	if err := agent.Init(ctx, nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	q, err := brk.CreateQueue("t-notify-chan", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.NotifyRequestSubscription("test-ns")); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	surface := json.RawMessage(`{"surfaceId":"s1"}`)
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	recv := func(reqID string) a2a.NotifyRequest {
		t.Helper()
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		msg, rerr := brk.Receive(rctx, q)
		if rerr != nil {
			t.Fatalf("receive %s: %v", reqID, rerr)
		}
		var req a2a.NotifyRequest
		if uerr := json.Unmarshal(msg.Payload, &req); uerr != nil {
			t.Fatalf("unmarshal %s: %v", reqID, uerr)
		}
		return req
	}

	agent.publishHILNotifyRequest(ctx, hilRequest{requestID: "req-web", source: "ask_user_question", recipientUserID: "trent@example.org", surface: surface, webInboxOnly: true}, expires)
	if got := recv("req-web").Channels; len(got) != 1 || got[0] != "web" {
		t.Errorf("webInboxOnly=true: Channels = %v, want [web]", got)
	}

	agent.publishHILNotifyRequest(ctx, hilRequest{requestID: "req-all", source: "ask_user_question", recipientUserID: "trent@example.org", surface: surface}, expires)
	if got := recv("req-all").Channels; len(got) != 0 {
		t.Errorf("webInboxOnly=false: Channels = %v, want empty (full fan-out)", got)
	}
}

func TestHandleTask_ForegroundWebHILRestrictsFanout(t *testing.T) {
	cases := []struct {
		name            string
		caps            map[string]bool
		background      bool
		wantWebOnly     bool
		parentSessionID string
	}{
		{"foreground_web_restricts_to_web_inbox", map[string]bool{tool.CapLiveWebSurface: true}, false, true, ""},
		{"no_live_web_cap_fans_out", nil, false, false, ""},
		{"background_web_still_restricts", map[string]bool{tool.CapLiveWebSurface: true}, true, true, ""},
		{"background_no_web_fans_out", nil, true, false, ""},
		{"sub_task_stamps_the_parent_session", map[string]bool{tool.CapLiveWebSurface: true}, false, true, "root-session-9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			brk := broker.NewDevBroker()
			if err := brk.Connect(ctx); err != nil {
				t.Fatalf("connect: %v", err)
			}
			t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

			agent := New(AgentConfig{
				Name: "hil-agent", Namespace: "test-ns",
				Model: "test-model", MaxTokens: 1000, MaxLLMCalls: 5,
				SystemPrompt: "You are a test agent.",
			})
			svc := newMockServices(newMockLLM(
				&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "ask_web", Arguments: "{}"}}},
				&llm.ChatResponse{Content: "done", FinishReason: "stop"},
			), newMemSessionStore())
			svc.brk = brk
			svc.featureRegistry = notifyEnabledRegistry(true)
			agent.svc = svc
			if err := agent.Init(ctx, nil, svc); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if err := agent.Tools().Register(&testBuiltinTool{
				name:        "ask_web",
				description: "asks the user a question",
				execFn: func(ic context.Context, _ map[string]any, tCtx *tool.Context) (*tool.Result, error) {
					resp := tCtx.WaitForUserInput(ic, "req-"+tc.name, "ask_user_question", json.RawMessage(`{"surfaceId":"s"}`), 5*time.Second) //nolint:staticcheck // exercising the wired HIL waiter
					return tool.OK(resp.Status), nil
				},
			}); err != nil {
				t.Fatalf("register tool: %v", err)
			}

			q, err := brk.CreateQueue("t-hil-"+tc.name, broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.NotifyRequestSubscription("test-ns")); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}

			msg := a2a.Message{
				Kind: "message", MessageID: "m1", Role: a2a.RoleUser,
				Parts:     []a2a.Part{a2a.NewTextPart("ask me")},
				ContextID: "ctx-1",
			}
			msg.Metadata = map[string]any{}
			if tc.background {
				msg.Metadata["backgroundExecutionEnabled"] = true
			}
			wantSession := msg.ContextID
			if tc.parentSessionID != "" {
				msg.Metadata["parentSessionID"] = tc.parentSessionID
				wantSession = tc.parentSessionID
			}
			userProps := a2a.UserProperties{
				ClientID:            "web-1",
				UserID:              "trent@example.org",
				A2AStatusTopic:      "test-ns/a2a/v1/gateway/status/web-1/task-1",
				GatewayCapabilities: tc.caps,
			}

			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = agent.HandleTask(ctx, "task-1", msg, userProps)
			}()

			rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			nmsg, rerr := brk.Receive(rctx, q)
			if rerr != nil {
				t.Fatalf("did not receive NotifyRequest: %v", rerr)
			}
			var req a2a.NotifyRequest
			if uerr := json.Unmarshal(nmsg.Payload, &req); uerr != nil {
				t.Fatalf("unmarshal NotifyRequest: %v", uerr)
			}
			agent.deliverHILResponse(&a2a.UserInputResponseData{
				Type: "user_input_response", RequestID: req.RequestID, Status: "answered",
			}, hilSenderNotifyService)

			gotWebOnly := len(req.Channels) == 1 && req.Channels[0] == "web"
			if gotWebOnly != tc.wantWebOnly {
				t.Errorf("Channels = %v (webOnly=%v), want webOnly=%v", req.Channels, gotWebOnly, tc.wantWebOnly)
			}
			if got := req.Metadata["session_id"]; got != wantSession {
				t.Errorf("session_id = %v, want %q", got, wantSession)
			}

			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("HandleTask did not complete")
			}
		})
	}
}

func TestBuildToolCtxFromState_ResumeHILRestrictsFanout(t *testing.T) {
	cases := []struct {
		name        string
		caps        map[string]bool
		wantWebOnly bool
	}{
		{"web_surface_restricts_to_web_inbox", map[string]bool{tool.CapLiveWebSurface: true}, true},
		{"no_live_web_cap_fans_out", map[string]bool{tool.CapInteractiveUserInput: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			brk := broker.NewDevBroker()
			if err := brk.Connect(ctx); err != nil {
				t.Fatalf("connect: %v", err)
			}
			t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

			agent := New(AgentConfig{Name: "hil-agent", Namespace: "test-ns"})
			svc := newMockServices(newMockLLM(), newMemSessionStore())
			svc.brk = brk
			svc.featureRegistry = notifyEnabledRegistry(true)
			agent.svc = svc
			if err := agent.Init(ctx, nil, svc); err != nil {
				t.Fatalf("Init: %v", err)
			}

			statusTopic := "test-ns/a2a/v1/gateway/status/web-1/task-1"
			sp := NewStatusPublisher(brk, StatusPublisherConfig{StatusTopic: statusTopic}, slog.Default())
			state := &TaskExecutionState{
				TaskID:            "task-1",
				ContextID:         "ctx-1",
				OriginalSessionID: "ctx-1",
				UserProps: a2a.UserProperties{
					UserID:              "trent@example.org",
					ClientID:            "web-1",
					A2AStatusTopic:      statusTopic,
					GatewayCapabilities: tc.caps,
				},
			}
			toolCtx := agent.buildToolCtxFromState(ctx, state, sp)

			q, err := brk.CreateQueue("t-hil-resume-"+tc.name, broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.NotifyRequestSubscription("test-ns")); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}

			done := make(chan struct{})
			go func() {
				defer close(done)
				toolCtx.WaitForUserInput(ctx, "req-resume-"+tc.name, "ask_user_question", json.RawMessage(`{"surfaceId":"s"}`), 5*time.Second) //nolint:staticcheck // exercising the wired HIL waiter
			}()

			rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			nmsg, rerr := brk.Receive(rctx, q)
			if rerr != nil {
				t.Fatalf("did not receive NotifyRequest: %v", rerr)
			}
			var req a2a.NotifyRequest
			if uerr := json.Unmarshal(nmsg.Payload, &req); uerr != nil {
				t.Fatalf("unmarshal NotifyRequest: %v", uerr)
			}
			agent.deliverHILResponse(&a2a.UserInputResponseData{
				Type: "user_input_response", RequestID: req.RequestID, Status: "answered",
			}, hilSenderNotifyService)

			gotWebOnly := len(req.Channels) == 1 && req.Channels[0] == "web"
			if gotWebOnly != tc.wantWebOnly {
				t.Errorf("Channels = %v (webOnly=%v), want webOnly=%v", req.Channels, gotWebOnly, tc.wantWebOnly)
			}
			if got := req.Metadata["session_id"]; got != "ctx-1" {
				t.Errorf("session_id = %v, want %q", got, "ctx-1")
			}

			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("WaitForUserInput did not return")
			}
		})
	}
}

func TestSuppressCrossChannelHIL(t *testing.T) {
	cases := []struct {
		name string
		caps map[string]bool
		want bool
	}{
		{"live web surface suppresses", map[string]bool{tool.CapLiveWebSurface: true}, true},
		{"non-web interactive fans out", map[string]bool{tool.CapInteractiveUserInput: true}, false},
		{"no caps fans out", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := suppressCrossChannelHIL(c.caps); got != c.want {
				t.Errorf("suppressCrossChannelHIL(%v) = %v, want %v", c.caps, got, c.want)
			}
		})
	}
}

func TestPublishHILNotifyRequest_StampsTheChatSession(t *testing.T) {
	for _, tc := range []struct {
		name, sessionID string
	}{
		{"in a chat", "web-session-1"},
		{"outside any chat", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			brk := broker.NewDevBroker()
			if err := brk.Connect(ctx); err != nil {
				t.Fatalf("connect: %v", err)
			}
			t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

			agent := New(AgentConfig{Name: "hil-agent", Namespace: "test-ns"})
			svc := newMockServices(newMockLLM(), newMemSessionStore())
			svc.brk = brk
			svc.featureRegistry = notifyEnabledRegistry(true)
			agent.svc = svc
			if err := agent.Init(ctx, nil, svc); err != nil {
				t.Fatalf("Init: %v", err)
			}
			q, err := brk.CreateQueue("t-notify-session", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.NotifyRequestSubscription("test-ns")); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}

			expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
			agent.publishHILNotifyRequest(ctx, hilRequest{requestID: "req-S", source: "tool_approval", recipientUserID: "alice@example.com", sessionID: tc.sessionID, surface: json.RawMessage(`{"surfaceId":"s1"}`)}, expires)

			rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			msg, err := brk.Receive(rctx, q)
			if err != nil {
				t.Fatalf("did not receive the published NotifyRequest: %v", err)
			}
			var req a2a.NotifyRequest
			if uerr := json.Unmarshal(msg.Payload, &req); uerr != nil {
				t.Fatalf("unmarshal NotifyRequest: %v", uerr)
			}
			got, present := req.Metadata["session_id"]
			if tc.sessionID == "" {
				if present {
					t.Errorf("session_id = %v, want it absent for a prompt outside any chat", got)
				}
				return
			}
			if got != tc.sessionID {
				t.Errorf("session_id = %v, want %q", got, tc.sessionID)
			}
		})
	}
}
