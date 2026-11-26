// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type reasoningModelLLM struct {
	mu           sync.Mutex
	needsTokens  int
	content      string
	gotMaxTokens int
}

func (m *reasoningModelLLM) ChatCompletion(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.mu.Lock()
	m.gotMaxTokens = req.MaxTokens
	m.mu.Unlock()

	if req.MaxTokens > 0 && req.MaxTokens < m.needsTokens {
		return &llm.ChatResponse{Content: "", FinishReason: "length"}, nil
	}
	return &llm.ChatResponse{Content: m.content, FinishReason: "stop"}, nil
}

func (m *reasoningModelLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("reasoningModelLLM: streaming not implemented")
}

func (m *reasoningModelLLM) budgetSeen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gotMaxTokens
}

func driveTitleGeneration(t *testing.T, client llm.Client) (string, string) {
	t.Helper()
	logBuf := &safeBuffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	sessionID := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, client)
	if err := sessRepo.Create(t.Context(), &store.Session{ID: sessionID, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}

	h := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", h.handleGenerateTitle)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sessionID+"/generateTitle",
		strings.NewReader(`{"userMessage":"What is the weather in Ottawa today?","agentResponse":"Sunny, about 22 degrees."}`))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := sessRepo.GetByID(t.Context(), sessionID)
		if err == nil && s != nil && s.Name != nil {
			return *s.Name, logBuf.String()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no title was stored within the deadline")
	return "", ""
}

func noUsableTitleRecord(t *testing.T, logs string) map[string]any {
	t.Helper()
	for line := range strings.SplitSeq(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if msg, _ := rec["msg"].(string); msg == msgTitleNoUsableTitle {
			return rec
		}
	}
	return nil
}

const reasoningBudgetFloor = 1024

func TestSessionTitle_ReasoningModelBudgetProducesTitle(t *testing.T) {
	client := &reasoningModelLLM{needsTokens: reasoningBudgetFloor, content: "Ottawa Weather"}

	title, logs := driveTitleGeneration(t, client)

	if title != "Ottawa Weather" {
		t.Errorf("title = %q, want the generated title — a reasoning model could not finish inside the budget", title)
	}
	if got := client.budgetSeen(); got != titleMaxTokens {
		t.Errorf("MaxTokens reaching the provider = %d, want titleMaxTokens (%d)", got, titleMaxTokens)
	}
	if titleMaxTokens < reasoningBudgetFloor {
		t.Errorf("titleMaxTokens = %d, below the %d a reasoning model needs to emit a title",
			titleMaxTokens, reasoningBudgetFloor)
	}
	if rec := noUsableTitleRecord(t, logs); rec != nil {
		t.Errorf("successful title generation logged the fallback warning: %v", rec)
	}
}

func TestSessionTitle_EmptyContentIsLogged(t *testing.T) {
	client := &reasoningModelLLM{needsTokens: 1 << 30, content: "unused"}

	title, logs := driveTitleGeneration(t, client)

	if title != "What is the weather in Ottawa today?" {
		t.Errorf("title = %q, want the fallback (the model returned nothing usable)", title)
	}

	rec := noUsableTitleRecord(t, logs)
	if rec == nil {
		t.Fatalf("the empty-title fallback logged nothing; it is silent again. logs:\n%s", logs)
	}
	if got := rec["finishReason"]; got != "length" {
		t.Errorf("finishReason = %v, want \"length\" — the field that identifies budget exhaustion", got)
	}
	if got := rec["contentLen"]; got != float64(0) {
		t.Errorf("contentLen = %v, want 0 for an empty completion", got)
	}
	if got := rec["maxTokens"]; got != float64(titleMaxTokens) {
		t.Errorf("maxTokens = %v, want %d", got, titleMaxTokens)
	}
}

func TestSessionTitle_StrippedToEmptyIsLoggedWithLength(t *testing.T) {
	const stripped = "**"
	client := &reasoningModelLLM{needsTokens: 1, content: stripped}

	title, logs := driveTitleGeneration(t, client)

	if title != "What is the weather in Ottawa today?" {
		t.Errorf("title = %q, want the fallback (content stripped to empty)", title)
	}
	rec := noUsableTitleRecord(t, logs)
	if rec == nil {
		t.Fatalf("a stripped-to-empty title logged nothing. logs:\n%s", logs)
	}
	if got := rec["contentLen"]; got != float64(len(stripped)) {
		t.Errorf("contentLen = %v, want %d — a non-zero length is what distinguishes 'stripped' from 'model returned nothing'",
			got, len(stripped))
	}
	if got := rec["finishReason"]; got != "stop" {
		t.Errorf("finishReason = %v, want \"stop\" (the call completed normally)", got)
	}
}
