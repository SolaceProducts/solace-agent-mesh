// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const oSeriesRejection = "Unsupported parameter: 'temperature' is not supported with this model."

type tempRejectingLLM struct {
	mu         sync.Mutex
	sawTemp    []bool
	response   string
	noToolCall bool
}

func (m *tempRejectingLLM) ChatCompletion(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.mu.Lock()
	hadTemp := req.Temperature != nil
	m.sawTemp = append(m.sawTemp, hadTemp)
	first := len(m.sawTemp) == 1
	m.mu.Unlock()

	if hadTemp && first {
		return nil, &llm.Error{StatusCode: 400, Message: oSeriesRejection}
	}
	if hadTemp {
		return nil, &llm.Error{StatusCode: 400, Message: oSeriesRejection}
	}
	if len(req.Tools) > 0 && !m.noToolCall {
		return &llm.ChatResponse{ToolCalls: []llm.ToolCall{{
			ID:        "call_1",
			Name:      req.Tools[0].Name,
			Arguments: m.response,
		}}}, nil
	}
	return &llm.ChatResponse{Content: m.response}, nil
}

func (m *tempRejectingLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, nil
}

func (m *tempRejectingLLM) assertProbedThenOmitted(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sawTemp) < 2 {
		t.Fatalf("saw %d calls (%v), want at least 2 — the site did not retry, so it is not using the fallback", len(m.sawTemp), m.sawTemp)
	}
	if !m.sawTemp[0] {
		t.Errorf("first call carried no temperature (%v) — the site stopped pinning one", m.sawTemp)
	}
	if m.sawTemp[1] {
		t.Errorf("retry still carried a temperature (%v) — the fallback did not drop it", m.sawTemp)
	}
}

func TestSessionTitle_RecoversFromTemperatureRejection(t *testing.T) {
	s1 := idgen.New()
	mock := &tempRejectingLLM{response: "Weather Discussion"}
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	if err := sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle",
		strings.NewReader(`{"userMessage":"What is the weather today?","agentResponse":"It is sunny and warm."}`))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}

	deadline := time.Now().Add(5 * time.Second)
	var name *string
	for time.Now().Before(deadline) {
		session, err := sessRepo.GetByID(context.Background(), s1)
		if err == nil && session != nil && session.Name != nil {
			name = session.Name
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if name == nil || *name != "Weather Discussion" {
		t.Fatalf("title = %v, want the generated title — a rejection must not degrade to the fallback title", name)
	}
	mock.assertProbedThenOmitted(t)
}

func TestPromptBuilderChat_RecoversFromTemperatureRejection(t *testing.T) {
	mock := &tempRejectingLLM{response: `{"message":"Here is a template","templateUpdates":{"name":"t"}}`}
	h := setupPromptBuilderWithLLM(t, mock)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/chat",
		strings.NewReader(`{"message":"help me write a prompt"}`))
	h.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "An unexpected error occurred") {
		t.Errorf("prompt builder returned its canned error despite the fallback: %s", rec.Body.String())
	}
	mock.assertProbedThenOmitted(t)
}

func TestTaskBuilderChat_RecoversFromTemperatureRejection(t *testing.T) {
	mock := &tempRejectingLLM{response: `{"message":"Scheduled","taskUpdates":{},"confidence":0.9}`}
	a := &taskBuilderAssistant{logger: slog.Default()}

	resp, err := a.processMessage(t.Context(), &TaskBuilderChatRequest{Message: "schedule the daily report"}, generalModel{client: mock}, nil)
	if err != nil {
		t.Fatalf("processMessage should have recovered via the fallback: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	mock.assertProbedThenOmitted(t)
}

func TestValidateConflict_RecoversFromTemperatureRejection(t *testing.T) {
	mock := &tempRejectingLLM{
		response:   `{"conflict":true,"confidence":0.95,"reason":"overlaps","affectedFields":["scheduleExpression"]}`,
		noToolCall: true,
	}
	a := &taskBuilderAssistant{logger: slog.Default()}

	resp := a.validateConflict(t.Context(), &ValidateConflictRequest{
		Instructions:       "run the daily report",
		ScheduleType:       "cron",
		ScheduleExpression: "0 9 * * *",
	}, generalModel{client: mock}, nil)
	if !resp.Conflict {
		t.Error("validateConflict failed open despite the fallback — a real conflict was dropped")
	}
	mock.assertProbedThenOmitted(t)
}

func TestTaskBuilderAssistant_MemoSkipsProbeWhenModelKnown(t *testing.T) {
	mock := &tempRejectingLLM{response: `{"message":"ok","taskUpdates":{},"confidence":0.9}`}
	a := &taskBuilderAssistant{logger: slog.Default()}

	for range 2 {
		if _, err := a.processMessage(t.Context(), &TaskBuilderChatRequest{Message: "schedule it"}, generalModel{client: mock, memoKey: "o3-mini"}, nil); err != nil {
			t.Fatalf("processMessage: %v", err)
		}
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sawTemp) != 3 {
		t.Fatalf("calls = %v, want 3 (probe, retry, then one memoized call)", mock.sawTemp)
	}
	if mock.sawTemp[2] {
		t.Error("second call re-probed with a temperature; the memo did not apply")
	}
}

func TestTaskBuilderAssistant_NoMemoUnderPlaceholderKeyBecauseRotationWouldGoUnseen(t *testing.T) {
	mock := &tempRejectingLLM{response: `{"message":"ok","taskUpdates":{},"confidence":0.9}`}
	a := &taskBuilderAssistant{logger: slog.Default()}
	unnamed := generalModel{client: mock}

	for range 2 {
		if _, err := a.processMessage(t.Context(), &TaskBuilderChatRequest{Message: "schedule it"}, unnamed, nil); err != nil {
			t.Fatalf("processMessage: %v", err)
		}
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sawTemp) != 4 {
		t.Fatalf("calls = %v, want 4 (probe+retry twice) — a placeholder key must not be memoized", mock.sawTemp)
	}
	if !mock.sawTemp[2] {
		t.Error("second call skipped the probe, so a non-invalidatable verdict was cached")
	}
}
