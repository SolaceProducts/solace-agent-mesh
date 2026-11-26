// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestPublishLLMResponse_RedactsToolCallArgs(t *testing.T) {
	h := newToolStatusHarness(t, "task-llmresp-1")

	h.sp.PublishLLMResponse(t.Context(), "test-model", "", nil,
		LLMResponseToolCall{
			ID:   "c1",
			Name: "fetch",
			Args: map[string]any{
				"url":     "https://api.example.com/v1?api_key=sk-live-4242&q=weather",
				"headers": map[string]any{"Authorization": "Bearer sk-live-9999"},
			},
		},
		LLMResponseToolCall{
			ID:       "c2",
			Name:     "_notify_artifact_save",
			Injected: true,
			Args:     map[string]any{"api_key": "sk-live-7777"},
		},
	)

	signals := drainStatusSignals(t, h.broker, h.queue, 1)
	published, err := json.Marshal(signals[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{"sk-live-4242", "sk-live-9999", "sk-live-7777"} {
		if strings.Contains(string(published), secret) {
			t.Errorf("llm_response leaked %q: %s", secret, published)
		}
	}
	if !strings.Contains(string(published), "q=weather") {
		t.Errorf("llm_response dropped the non-credential query: %s", published)
	}
}

func TestPublishToolResult_RedactsCredentials(t *testing.T) {
	h := newToolStatusHarness(t, "task-toolresult-1")

	h.sp.PublishToolResult(t.Context(), "web_request", map[string]any{
		"url":                  "https://api.example.com/v1?api_key=sk-live-4242&q=weather",
		"response_status_code": 200,
	}, "c1")

	signals := drainStatusSignals(t, h.broker, h.queue, 1)
	published, err := json.Marshal(signals[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(published), "sk-live-4242") {
		t.Errorf("tool_result leaked a credential: %s", published)
	}
	if !strings.Contains(string(published), "q=weather") {
		t.Errorf("tool_result dropped the non-credential query: %s", published)
	}
}

func TestPublishLLMInvocation_ArgsNeverReachTheWire(t *testing.T) {
	h := newToolStatusHarness(t, "task-llminv-1")

	h.sp.PublishLLMInvocation(t.Context(), &llm.ChatRequest{
		Model: "test-model",
		Messages: []llm.ChatMessage{{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID: "c1", Name: "fetch",
				Arguments: `{"headers":{"Authorization":"Bearer sk-live-9999"}}`,
			}},
		}},
	}, "test-model")

	signals := drainStatusSignals(t, h.broker, h.queue, 1)
	if signals[0]["type"] != "llm_invocation" {
		t.Fatalf("signal[0].type = %v, want llm_invocation", signals[0]["type"])
	}
	published, err := json.Marshal(signals[0])
	if err != nil {
		t.Fatalf("marshal published signal: %v", err)
	}
	if strings.Contains(string(published), "sk-live-9999") {
		t.Errorf("llm_invocation now emits real tool arguments and needs redaction: %s", published)
	}
	if !strings.Contains(string(published), "fetch") {
		t.Errorf("llm_invocation dropped the tool name, so the leak check proves nothing: %s", published)
	}
}

func TestRedactToolArgs_LeavesHILGatePredicateIntact(t *testing.T) {
	const url = "https://api.example.com/v1?api_key=sk-live-4242"
	args := map[string]any{"url": url, "headers": map[string]any{"Authorization": "Bearer sk-live-9999"}}
	cfg := HILToolConfig{ApprovalWhen: []ApprovalRule{{Arg: "url", Op: OpEq, Value: url}}}

	if !cfg.ShouldGate(args) {
		t.Fatal("precondition: the rule must match before redaction")
	}
	redacted := redactToolArgs(args)
	if !cfg.ShouldGate(args) {
		t.Error("redaction mutated the caller's map; the HIL approval gate stopped firing")
	}
	if encoded, _ := json.Marshal(redacted); strings.Contains(string(encoded), "sk-live-4242") {
		t.Errorf("the returned copy was not redacted: %s", encoded)
	}
}

func TestFormatMessageContent_RedactsToolResult(t *testing.T) {
	content := formatMessageContent(&llm.ChatMessage{
		Role: "tool", ToolCallID: "c1", ToolName: "web_request",
		Content: `{"url":"https://api.example.com/v1?api_key=sk-live-4242","status":200}`,
	})

	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "sk-live-4242") {
		t.Errorf("function_response leaked a credential: %s", encoded)
	}
	if !strings.Contains(string(encoded), "api.example.com") {
		t.Errorf("function_response lost the non-credential content: %s", encoded)
	}
}

func TestGenerateApprovalSurface_RedactsMessageTemplate(t *testing.T) {
	surface, err := generateApprovalSurface("req-1", "web_request", "Agent", map[string]any{
		"url":     "https://api.example.com/v1?api_key=sk-live-4242",
		"api_key": "sk-live-9999",
	}, nil, "Send to {{.url}} with {{.api_key}}?", false, time.Minute)
	if err != nil {
		t.Fatalf("generateApprovalSurface: %v", err)
	}
	for _, secret := range []string{"sk-live-4242", "sk-live-9999"} {
		if strings.Contains(string(surface), secret) {
			t.Errorf("approval message leaked %q: %s", secret, surface)
		}
	}
}

func TestGenerateApprovalSurface_RedactsCredentials(t *testing.T) {
	surface, err := generateApprovalSurface("req-1", "web_request", "Agent", map[string]any{
		"url":     "https://api.example.com/v1?api_key=sk-live-4242&q=weather",
		"headers": map[string]any{"Authorization": "Bearer sk-live-9999"},
	}, nil, "", true, time.Minute)
	if err != nil {
		t.Fatalf("generateApprovalSurface: %v", err)
	}
	for _, secret := range []string{"sk-live-4242", "sk-live-9999"} {
		if strings.Contains(string(surface), secret) {
			t.Errorf("approval surface leaked %q: %s", secret, surface)
		}
	}
	for _, keep := range []string{"url", "headers", "q=weather"} {
		if !strings.Contains(string(surface), keep) {
			t.Errorf("approval surface dropped %q: %s", keep, surface)
		}
	}
}
