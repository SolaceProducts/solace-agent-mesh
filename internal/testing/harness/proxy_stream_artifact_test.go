// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func TestProxyStreamingFinalResponse(t *testing.T) {
	const answer = "Response from downstream agent."

	cases := []struct {
		name       string
		responses  []json.RawMessage
		wantText   string
		wantNoText bool
	}{
		{
			name:      "terminal_task_only",
			responses: []json.RawMessage{rawTask(answer)},
			wantText:  answer,
		},
		{
			name: "final_statusupdate_with_text",
			responses: []json.RawMessage{
				rawStatusUpdate(false, "working", "working..."),
				rawStatusUpdate(true, "completed", answer),
			},
			wantText: answer,
		},
		{
			name: "text_artifact_then_empty_final_statusupdate",
			responses: []json.RawMessage{
				rawTextArtifact(answer),
				json.RawMessage(`{"taskId":"d","contextId":"session_repro","kind":"status-update","final":true,"status":{"state":"completed"}}`),
			},
			wantText: answer,
		},
		{
			name: "text_artifact_then_completed_task_no_message",
			responses: []json.RawMessage{
				rawTextArtifact(answer),
				json.RawMessage(`{"id":"d","contextId":"session_repro","kind":"task","status":{"state":"completed"}}`),
			},
			wantText: answer,
		},
		{
			name: "text_artifact_then_final_with_own_text",
			responses: []json.RawMessage{
				rawTextArtifact("streamed chunk"),
				rawStatusUpdate(true, "completed", answer),
			},
			wantText: answer,
		},
		{
			name: "multiple_text_artifacts_then_empty_final_statusupdate",
			responses: []json.RawMessage{
				rawTextArtifact("chunk one"),
				rawTextArtifact("chunk two"),
				json.RawMessage(`{"taskId":"d","contextId":"session_repro","kind":"status-update","final":true,"status":{"state":"completed"}}`),
			},
			wantText: "chunk one\nchunk two",
		},
		{
			name: "text_artifact_then_input_required_signal",
			responses: []json.RawMessage{
				rawTextArtifact(answer),
				json.RawMessage(`{"taskId":"d","contextId":"session_repro","kind":"status-update","final":true,"status":{"state":"input-required","message":{"role":"agent","messageId":"s1","kind":"message","parts":[{"kind":"data","data":{"a2a_signal_type":"authentication_required"}}]}}}`),
			},
			wantNoText: true,
		},
		{
			name: "empty_final_statusupdate_no_artifact",
			responses: []json.RawMessage{
				json.RawMessage(`{"taskId":"d","contextId":"session_repro","kind":"status-update","final":true,"status":{"state":"completed"}}`),
			},
			wantNoText: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, ok := runProxyStreaming(t, tc.responses)
			if !ok {
				t.Fatalf("no final response received on gateway response topic")
			}
			switch {
			case tc.wantNoText:
				if text != "" {
					t.Errorf("expected no text in final response, got %q", text)
				}
			default:
				if text != tc.wantText {
					t.Errorf("final response text = %q, want %q", text, tc.wantText)
				}
			}
		})
	}
}

func runProxyStreaming(t *testing.T, responses []json.RawMessage) (string, bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(&testWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(context.Background()) }()

	mock := NewMockDownstreamServer(responses, nil, nil, true)
	defer mock.Close()

	const agentName = "TestAgent_Proxied"
	cfgMap := map[string]any{
		"kind": "proxy",
		"name": agentName,
		"app_config": map[string]any{
			"name":                          agentName,
			"namespace":                     "test-ns",
			"url":                           mock.Server.URL,
			"agent_card_path":               "/.well-known/agent-card.json",
			"ssl_verify":                    false,
			"discovery_interval_seconds":    0,
			"task_state_ttl_minutes":        60,
			"task_cleanup_interval_minutes": 0,
			"use_agent_card_url":            false,
		},
	}

	svc := &proxyTestServices{
		brk:      brk,
		sessions: session.NewMemoryStore(),
		arts:     &artifact.ScopedService{Inner: artmem.New(), Scope: "namespace", Namespace: "test-ns"},
		logger:   logger,
	}
	inst, err := proxy.NewInstance(config.FromMap(cfgMap))
	if err != nil {
		t.Fatalf("new instance: %v", err)
	}
	if err = inst.Init(ctx, nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err = inst.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = inst.Stop(context.Background()) }()

	time.Sleep(100 * time.Millisecond)

	const gwID = "gw-repro"
	taskID := idgen.New()
	respTopic := a2a.GatewayResponseTopic("test-ns", gwID, taskID)
	statusTopic := a2a.GatewayStatusTopic("test-ns", gwID, taskID)

	respQ, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create resp sub: %v", err)
	}
	if err = brk.Subscribe(respQ, respTopic); err != nil {
		t.Fatalf("subscribe resp: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Hello")},
		ContextID: "session_repro",
	}
	req := a2a.NewRequest(taskID, "message/stream", map[string]any{"message": msg})
	payload, _ := json.Marshal(req)
	userProps := a2a.EncodeUserProperties(&a2a.UserProperties{
		Timestamp:      time.Now().UnixMilli(),
		ClientID:       gwID,
		UserID:         "repro@example.com",
		ReplyTo:        respTopic,
		A2AStatusTopic: statusTopic,
	})

	reqTopic := a2a.AgentRequestTopic("test-ns", agentName)
	if err = brk.PublishGuaranteed(ctx, reqTopic, &broker.Message{Payload: payload, UserProperties: userProps}); err != nil {
		t.Fatalf("publish request: %v", err)
	}

	recvCtx, recvCancel := context.WithTimeout(ctx, 5*time.Second)
	defer recvCancel()
	got, err := brk.Receive(recvCtx, respQ)
	if err != nil {
		return "", false
	}
	return finalResponseText(t, got.Payload), true
}

func finalResponseText(t *testing.T, payload []byte) string {
	t.Helper()
	var env struct {
		Result struct {
			Status struct {
				Message *struct {
					Parts []struct {
						Kind string `json:"kind"`
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}
	if env.Result.Status.Message == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range env.Result.Status.Message.Parts {
		if p.Kind == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func rawTask(text string) json.RawMessage {
	return json.RawMessage(`{"id":"d","contextId":"session_repro","kind":"task","status":{"state":"completed","message":{"role":"agent","messageId":"m1","kind":"message","parts":[{"kind":"text","text":"` + text + `"}]}}}`)
}

func rawStatusUpdate(final bool, state, text string) json.RawMessage {
	f := "false"
	if final {
		f = "true"
	}
	return json.RawMessage(`{"taskId":"d","contextId":"session_repro","kind":"status-update","final":` + f + `,"status":{"state":"` + state + `","message":{"role":"agent","messageId":"s1","kind":"message","parts":[{"kind":"text","text":"` + text + `"}]}}}`)
}

func rawTextArtifact(text string) json.RawMessage {
	return json.RawMessage(`{"taskId":"d","contextId":"session_repro","kind":"artifact-update","artifact":{"artifactId":"a1","name":"answer","parts":[{"kind":"text","text":"` + text + `"}]}}`)
}
