// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestHandleMessage_ASpentMeshBudgetIsA429WithRetryAfterAndNoTask(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	resetsAt := time.Now().Add(2 * time.Hour).UTC()
	budgettest.BlockMesh(t, core.BudgetGate(), resetsAt)
	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	var logs safeBuffer
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	brk := core.Services().Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "test-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user",` +
		`"parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent"}}}}`
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", h.handleMessageStream)
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d %s, want 429", rec.Code, rec.Body.String())
	}
	retryAfter, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retryAfter < int((2*time.Hour).Seconds())-60 || retryAfter > int((2*time.Hour).Seconds()) {
		t.Errorf("Retry-After = %q, want about two hours in seconds", rec.Header().Get("Retry-After"))
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != entrypoint.BudgetExhaustedErrorCode || !strings.Contains(resp.Error.Message, "budget") {
		t.Errorf("error = %+v, want code %d naming the budget", resp.Error, entrypoint.BudgetExhaustedErrorCode)
	}
	if line := logs.String(); !strings.Contains(line, `msg="task refused: a budget is spent"`) || !strings.Contains(line, "budgetTier=mesh") || !strings.Contains(line, "userID=user-1") {
		t.Errorf("refusal log = %q, want one line naming the mesh tier and the user", line)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if msg, _ := brk.Receive(ctx, q); msg != nil {
		t.Error("a refused task was published to the agent")
	}
}

func TestHandleMessage_AnUnconfirmedBudgetStateIsA429ToRetrySoon(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	budgettest.Unconfirm(t, core.BudgetGate())
	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	var logs safeBuffer
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user",` +
		`"parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent"}}}}`
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:send", h.handleMessageSend)
	req := httptest.NewRequest("POST", "/api/v1/message:send", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d %s, want 429", rec.Code, rec.Body.String())
	}
	if retryAfter, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || retryAfter < 1 || retryAfter > 30 {
		t.Errorf("Retry-After = %q, want at most a heartbeat", rec.Header().Get("Retry-After"))
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != entrypoint.BudgetUnconfirmedErrorCode || !strings.Contains(resp.Error.Message, "try again in a few minutes") {
		t.Errorf("error = %+v, want code %d with the unconfirmed-state wording, not the spent-budget code", resp.Error, entrypoint.BudgetUnconfirmedErrorCode)
	}
	if line := logs.String(); !strings.Contains(line, `level=DEBUG msg="task refused: the budget state is not confirmed yet"`) || strings.Contains(line, "failed to submit task") {
		t.Errorf("refusal log = %q, want one DEBUG line and no submit failure", line)
	}
}

func TestHandleMessage_ASubmitThatFailsForAnotherReasonIsLoggedOnceAsAnError(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	var logs safeBuffer
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if err := core.Services().Broker().Disconnect(t.Context()); err != nil {
		t.Fatalf("disconnect broker: %v", err)
	}
	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user",` +
		`"parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent"}}}}`
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:send", h.handleMessageSend)
	req := httptest.NewRequest("POST", "/api/v1/message:send", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d %s, want 500", rec.Code, rec.Body.String())
	}
	if n := strings.Count(logs.String(), `level=ERROR msg="failed to submit task"`); n != 1 {
		t.Errorf("submit failure logged %d times as an ERROR, want once:\n%s", n, logs.String())
	}
	if strings.Contains(logs.String(), "task refused") {
		t.Errorf("a non-budget failure was logged as a budget refusal:\n%s", logs.String())
	}
}
