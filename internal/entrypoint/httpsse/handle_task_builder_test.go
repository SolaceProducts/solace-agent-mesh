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
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func setupTaskBuilderTest(t *testing.T, llmClient llm.Client) *taskBuilderHandler {
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
			"database_url": testDBURL(t, "builder"),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}

	core := entrypoint.NewGatewayCore(adapter, appConfig)

	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithDefaultLLM(llmClient),
	)
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	db := core.Store()
	if db == nil {
		t.Fatal("expected DB")
	}
	t.Cleanup(func() { _ = db.Close() })

	h := newTaskBuilderHandler(core, slog.Default())
	if h == nil {
		t.Fatal("expected task builder handler to be created")
	}
	return h
}

func TestTaskBuilderGreeting(t *testing.T) {
	mock := &mockLLMClient{response: "unused"}
	h := setupTaskBuilderTest(t, mock)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks/builder/greeting", nil)
	h.handleGreeting(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message == "" {
		t.Error("expected non-empty greeting message")
	}
	if resp.ReadyToSave {
		t.Error("greeting should not be ready to save")
	}
	if resp.Confidence != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", resp.Confidence)
	}
}

func TestTaskBuilderChat_HappyPath(t *testing.T) {
	mock := &mockLLMClient{
		response: `{"message":"I'll set up a daily report for you.","taskUpdates":{"name":"Daily Report","scheduleType":"cron","scheduleExpression":"0 9 * * *"},"confidence":0.85,"readyToSave":false}`,
	}
	h := setupTaskBuilderTest(t, mock)

	body, _ := json.Marshal(TaskBuilderChatRequest{
		Message:         "Generate a daily report at 9 AM",
		AvailableAgents: []AvailableAgent{{Name: "ReportAgent"}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body))
	h.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message == "" {
		t.Error("expected non-empty message")
	}
	if resp.TaskUpdates["name"] != "Daily Report" {
		t.Errorf("expected name='Daily Report', got %v", resp.TaskUpdates["name"])
	}
	if resp.Confidence != 0.85 {
		t.Errorf("expected confidence 0.85, got %f", resp.Confidence)
	}
}

func TestTaskBuilderChat_CodeFences(t *testing.T) {
	mock := &mockLLMClient{
		response: "```json\n{\"message\":\"stripped\",\"confidence\":0.8}\n```",
	}
	h := setupTaskBuilderTest(t, mock)

	body, _ := json.Marshal(TaskBuilderChatRequest{Message: "help me"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body))
	h.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "stripped" {
		t.Errorf("expected 'stripped', got %q", resp.Message)
	}
}

func TestTaskBuilderChat_InvalidJSON(t *testing.T) {
	mock := &mockLLMClient{response: "This is not JSON at all, sorry!"}
	h := setupTaskBuilderTest(t, mock)

	body, _ := json.Marshal(TaskBuilderChatRequest{Message: "help"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body))
	h.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != fallbackMessage {
		t.Errorf("expected fallback message, got %q", resp.Message)
	}
}

func TestTaskBuilderChat_NoToolCallSurfacesProse(t *testing.T) {
	const prose = "What time should the report run, and what should it include?"
	mock := &mockLLMClient{response: prose, noToolCall: true}
	h := setupTaskBuilderTest(t, mock)

	body, _ := json.Marshal(TaskBuilderChatRequest{Message: "Generate a daily report"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body))
	h.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != prose {
		t.Errorf("expected prose surfaced, got %q", resp.Message)
	}
	if resp.Message == fallbackMessage {
		t.Error("must not dead-end on the generic fallback when prose is available")
	}
}

func TestTaskBuilderChat_NoToolCallEmptyContent(t *testing.T) {
	mock := &mockLLMClient{response: "", noToolCall: true}
	h := setupTaskBuilderTest(t, mock)

	body, _ := json.Marshal(TaskBuilderChatRequest{Message: "help"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body))
	h.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != fallbackMessage {
		t.Errorf("expected fallback message for empty content, got %q", resp.Message)
	}
}

func TestTaskBuilderChat_EmptyMessage(t *testing.T) {
	mock := &mockLLMClient{response: "unused"}
	h := setupTaskBuilderTest(t, mock)

	body, _ := json.Marshal(TaskBuilderChatRequest{Message: ""})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body))
	h.handleChat(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTaskBuilderChat_LLMError(t *testing.T) {
	mock := &mockLLMClient{err: fmt.Errorf("LLM unavailable")}
	h := setupTaskBuilderTest(t, mock)

	body, _ := json.Marshal(TaskBuilderChatRequest{Message: "help me"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body))
	h.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (graceful fallback), got %d", rec.Code)
	}

	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message == "" {
		t.Error("expected non-empty fallback message")
	}
	if resp.ReadyToSave {
		t.Error("fallback should not be ready to save")
	}
}

func TestTaskBuilderChat_InvalidBody(t *testing.T) {
	mock := &mockLLMClient{response: "unused"}
	h := setupTaskBuilderTest(t, mock)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat",
		bytes.NewReader([]byte("not json")))
	h.handleChat(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTaskBuilderChat_WithConversationHistory(t *testing.T) {
	mock := &mockLLMClient{
		response: `{"message":"Got it, setting up cron.","taskUpdates":{"scheduleType":"cron"},"confidence":0.7}`,
	}
	h := setupTaskBuilderTest(t, mock)

	body, _ := json.Marshal(TaskBuilderChatRequest{
		Message: "Make it daily",
		ConversationHistory: []ConversationTurn{
			{Role: "user", Content: "I want to schedule a report"},
			{Role: "assistant", Content: "What schedule would you like?"},
		},
		CurrentTask:     map[string]any{"name": "My Report"},
		AvailableAgents: []AvailableAgent{{Name: "ReportAgent"}, {Name: "AnalyticsAgent"}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body))
	h.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TaskUpdates["scheduleType"] != "cron" {
		t.Errorf("expected schedule_type=cron, got %v", resp.TaskUpdates["scheduleType"])
	}
}

func TestNewTaskBuilderHandler_NilWithoutLLM(t *testing.T) {

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
			"database_url": testDBURL(t, "builder-nollm"),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}

	core := entrypoint.NewGatewayCore(adapter, appConfig)
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Store().Close() })

	h := newTaskBuilderHandler(core, slog.Default())
	if h != nil {
		t.Error("expected nil handler when LLM is not configured")
	}
}

func TestNewTaskBuilderHandler_RegistersWithBindingsOnly(t *testing.T) {
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
			"database_url": testDBURL(t, "builder-bindings"),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}

	core := entrypoint.NewGatewayCore(adapter, appConfig)
	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithBindings(bindingsFor(&mockLLMClient{response: "unused"}, "general")),
	)
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Store().Close() })

	if h := newTaskBuilderHandler(core, slog.Default()); h == nil {
		t.Fatal("expected task builder handler to register with a bindings registry and no defaultLLM")
	}
}
