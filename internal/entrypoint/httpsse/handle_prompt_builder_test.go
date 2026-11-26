// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestPromptBuilderInit(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)

	h := &promptBuilderHandler{
		core:   adapter.core,
		logger: adapter.logger,
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/prompts/chat/init", nil)
	h.handleInit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}

	if msg, _ := raw["message"].(string); msg == "" {
		t.Error("expected non-empty greeting message")
	}
	if conf, _ := raw["confidence"].(float64); conf != 1.0 {
		t.Errorf("expected confidence=1.0, got %v", raw["confidence"])
	}
	for _, forbidden := range []string{"templateUpdates", "readyToSave", "isError"} {
		if _, ok := raw[forbidden]; ok {
			t.Errorf("init response must not include %q (Python returns only message+confidence)", forbidden)
		}
	}
}

func TestPromptBuilderChat_PromptsDisabled(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	h := &promptBuilderHandler{
		core:   adapter.core,
		logger: adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	body := `{"message":"help me create a template"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestPromptBuilderChat_EmptyMessage(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	h := &promptBuilderHandler{
		core:       adapter.core,
		logger:     adapter.logger,
		promptRepo: store.NewPromptRepo(core.Store()),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	body := `{"message":""}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestPromptBuilderChat_NoLLM(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	h := &promptBuilderHandler{
		core:       adapter.core,
		logger:     adapter.logger,
		promptRepo: store.NewPromptRepo(core.Store()),
		groupRepo:  store.NewPromptGroupRepo(core.Store()),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	body := `{"message":"create a template"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ChatBuilderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Message == "" {
		t.Error("expected non-empty fallback message")
	}
	if resp.Confidence != 0.5 {
		t.Errorf("expected confidence=0.5 for no-LLM fallback, got %f", resp.Confidence)
	}
	if !resp.IsError {
		t.Error("expected is_error=true for no-LLM fallback")
	}
	if resp.TemplateUpdates == nil {
		t.Error("expected template_updates to be non-nil (Python returns {} not null)")
	}
}

func TestPromptBuilderChat_InvalidBody(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	h := &promptBuilderHandler{
		core:       adapter.core,
		logger:     adapter.logger,
		promptRepo: store.NewPromptRepo(core.Store()),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader("not json"))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func setupPromptBuilderWithLLM(t *testing.T, llmClient llm.Client) *promptBuilderHandler {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()
	appConfig := map[string]any{
		"namespace":          "test-ns",
		"session_secret_key": "test-secret",
		"gateway_id":         "test-gw",
		"fastapi_host":       "127.0.0.1",
		"fastapi_port":       0,
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": testDBURL(t, "pb"),
		},
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		t.Fatalf("load community flags: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithDefaultLLM(llmClient),
		runtime.WithFeatureRegistry(flagRegistry),
	)
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if db := core.Store(); db != nil {
		t.Cleanup(func() { _ = db.Close() })
	}
	adapter.core = core
	adapter.logger = slog.Default()

	return &promptBuilderHandler{
		core:       core,
		groupRepo:  store.NewPromptGroupRepo(core.Store()),
		promptRepo: store.NewPromptRepo(core.Store()),
		logger:     adapter.logger,
	}
}

func TestPromptBuilderChat_WithLLM_ValidJSON(t *testing.T) {
	mock := &mockLLMClient{response: `{"message":"Here is your template","confidence":0.9,"readyToSave":false}`}
	h := setupPromptBuilderWithLLM(t, mock)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	body := `{"message":"create a greeting template"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ChatBuilderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "Here is your template" {
		t.Errorf("message = %q, want 'Here is your template'", resp.Message)
	}
	if resp.Confidence != 0.9 {
		t.Errorf("confidence = %f, want 0.9", resp.Confidence)
	}
}

func TestPromptBuilderChat_WithLLM_InvalidJSONFallback(t *testing.T) {
	mock := &mockLLMClient{response: "Just a plain text response, not JSON", noToolCall: true}
	h := setupPromptBuilderWithLLM(t, mock)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	body := `{"message":"help me"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ChatBuilderResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Message != "Just a plain text response, not JSON" {
		t.Errorf("expected raw LLM text as fallback, got %q", resp.Message)
	}
	if resp.Confidence != 0.5 {
		t.Errorf("expected confidence=0.5 for unparseable JSON, got %f", resp.Confidence)
	}
}

func TestPromptBuilderChat_WithLLM_Error(t *testing.T) {
	mock := &mockLLMClient{err: fmt.Errorf("LLM service unavailable")}
	h := setupPromptBuilderWithLLM(t, mock)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	body := `{"message":"create template"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ChatBuilderResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Confidence != 0.3 {
		t.Errorf("expected confidence=0.3 for LLM error fallback, got %f", resp.Confidence)
	}
	if !resp.IsError {
		t.Error("expected is_error=true for LLM error fallback")
	}
}

func TestPromptBuilderChat_WithLLM_ConversationHistoryAndTemplate(t *testing.T) {
	mock := &mockLLMClient{response: `{"message":"Updated template","confidence":0.95,"readyToSave":true,"templateUpdates":{"name":"greeting","command":"/greet"}}`}
	h := setupPromptBuilderWithLLM(t, mock)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	body := `{
		"message": "make it friendlier",
		"conversationHistory": [
			{"role": "user", "content": "create a greeting template"},
			{"role": "assistant", "content": "Here is a draft"}
		],
		"currentTemplate": {"name": "greeting", "command": "/greet"}
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ChatBuilderResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.ReadyToSave {
		t.Error("expected ready_to_save=true")
	}
	if resp.TemplateUpdates == nil {
		t.Fatal("expected template_updates to be non-nil")
	}
	if resp.TemplateUpdates.Name != "greeting" {
		t.Errorf("template name = %q, want 'greeting'", resp.TemplateUpdates.Name)
	}
}

func TestPromptBuilderChat_WithLLM_MarkdownCodeFence(t *testing.T) {
	mock := &mockLLMClient{response: "```json\n{\"message\":\"stripped\",\"confidence\":0.8}\n```", noToolCall: true}
	h := setupPromptBuilderWithLLM(t, mock)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	body := `{"message":"test"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ChatBuilderResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Message != "stripped" {
		t.Errorf("expected 'stripped', got %q", resp.Message)
	}
}

func TestPromptBuilderChat_DoesNotLogModelCompletionContent(t *testing.T) {
	const prose = "CANARY-model-prose-must-not-be-logged, definitely not JSON"

	var logs bytes.Buffer
	mock := &mockLLMClient{response: prose, noToolCall: true}
	h := setupPromptBuilderWithLLM(t, mock)
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/chat", h.handleChat)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(`{"message":"help me"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if !strings.Contains(logs.String(), "prompt builder JSON parse error") {
		t.Fatalf("the parse-error path did not run, so this asserts nothing: %q", logs.String())
	}
	if strings.Contains(logs.String(), prose) {
		t.Errorf("the model's completion was logged verbatim: %q", logs.String())
	}
	if !strings.Contains(logs.String(), "contentLen=") {
		t.Errorf("contentLen is missing, so an operator cannot see that the model returned prose at all: %q", logs.String())
	}

	var resp ChatBuilderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Message != prose {
		t.Errorf("response Message = %q, want the raw prose — the fallback behaviour changed", resp.Message)
	}
}
