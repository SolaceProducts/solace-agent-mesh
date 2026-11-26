// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions/sessioncontrol"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func seedSession(store *memSessionStore, agentName, userID, sessionID string) *session.Session {
	now := time.Now()
	sess := &session.Session{
		AppName:   agentName,
		UserID:    userID,
		SessionID: sessionID,
		CreatedAt: now,
		UpdatedAt: now,
		Messages: []session.Message{
			{Role: "user", Content: "hello", Timestamp: now},
			{Role: "assistant", Content: "hi there", Timestamp: now},
			{Role: "user", Content: "what is the capital of france?", Timestamp: now},
			{Role: "assistant", Content: "paris", Timestamp: now},
			{Role: "user", Content: "and germany?", Timestamp: now},
			{Role: "assistant", Content: "berlin", Timestamp: now},
		},
	}
	_ = store.Create(context.Background(), sess)
	return sess
}

func TestCompactSession_HappyPath(t *testing.T) {
	mock := newMockLLM(&llm.ChatResponse{
		Content:      "summary text",
		FinishReason: "stop",
		Usage:        &llm.Usage{PromptTokens: 120, CompletionTokens: 30},
	})
	store := newMemSessionStore()
	a := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	svc := newMockServices(mock, store)
	a.svc = svc
	if err := a.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	seedSession(store, "test-agent", "user-1", "sess-1")

	res, err := a.CompactSession(t.Context(), extensions.RequestInfo{UserID: "user-1"}, sessioncontrol.CompactParams{SessionID: "sess-1", TargetPercent: 50})
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	if res.SummarizerInputTokens != 120 || res.SummarizerOutputTokens != 30 {
		t.Errorf("usage not propagated: %+v", res)
	}
	if res.SummarizedMessagesCount == 0 {
		t.Error("expected non-zero summarized count")
	}
	if res.PostCompactionRemainingTokens == 0 {
		t.Error("expected non-zero post-compaction estimate")
	}
}

func TestCompactSession_RejectsEmptySessionID(t *testing.T) {
	a := New(AgentConfig{Name: "test-agent"})
	a.svc = newMockServices(newMockLLM(), newMemSessionStore())

	_, err := a.CompactSession(t.Context(), extensions.RequestInfo{UserID: "user-1"}, sessioncontrol.CompactParams{SessionID: ""})
	if err == nil {
		t.Fatal("expected error for empty sessionID")
	}
	var extErr *extensions.Error
	if !errors.As(err, &extErr) || extErr.Code != -32602 {
		t.Fatalf("expected invalid-params, got %v", err)
	}
}

func TestCompactSession_NotFound(t *testing.T) {
	mock := newMockLLM()
	store := newMemSessionStore()
	a := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	svc := newMockServices(mock, store)
	a.svc = svc
	if err := a.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	_, err := a.CompactSession(t.Context(), extensions.RequestInfo{UserID: "user-1"}, sessioncontrol.CompactParams{SessionID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	var extErr *extensions.Error
	if !errors.As(err, &extErr) || extErr.Code != extensions.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %v", err)
	}
}

func TestCompactSession_AlreadyInProgress(t *testing.T) {
	a := New(AgentConfig{Name: "test-agent"})
	a.svc = newMockServices(newMockLLM(), newMemSessionStore())

	locks := a.compactionLocksMap()
	release, ok := locks.tryLock("sess-busy")
	if !ok {
		t.Fatal("could not acquire initial lock")
	}
	defer release()

	_, err := a.CompactSession(t.Context(), extensions.RequestInfo{UserID: "user-1"}, sessioncontrol.CompactParams{SessionID: "sess-busy"})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	var extErr *extensions.Error
	if !errors.As(err, &extErr) || extErr.Code != extensions.CodeConflict {
		t.Fatalf("expected CodeConflict, got %v", err)
	}
}

func TestCompactSession_ProviderFailureSanitizedWithoutLLMErrorPrefix(t *testing.T) {
	mock := &erroringLLM{err: &llm.Error{
		Message:    "<html><head><title>504 Gateway Time-out</title></head></html>",
		StatusCode: 504,
	}}
	store := newMemSessionStore()
	a := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	svc := newMockServices(mock, store)
	a.svc = svc
	if err := a.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	seedSession(store, "test-agent", "user-1", "sess-llm")

	_, err := a.CompactSession(t.Context(), extensions.RequestInfo{UserID: "user-1"}, sessioncontrol.CompactParams{SessionID: "sess-llm"})
	if err == nil {
		t.Fatal("expected error")
	}
	var extErr *extensions.Error
	if !errors.As(err, &extErr) || extErr.Code != extensions.CodeInternal {
		t.Fatalf("expected internal error, got %v", err)
	}
	if strings.Contains(strings.ToLower(extErr.Message), "<html") {
		t.Errorf("provider HTML body reached the user-facing message: %q", extErr.Message)
	}
	if strings.Contains(extErr.Message, "LLM error:") {
		t.Errorf("message = %q, want no \"LLM error:\" prefix", extErr.Message)
	}
}

func TestCompactSession_NotAdvertisedWithoutSessionStore(t *testing.T) {
	a := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	svc := newMockServices(newMockLLM(), nil)
	a.svc = svc
	if err := a.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	card := a.BuildAgentCard()
	for _, e := range card.Capabilities.Extensions {
		if e.URI == "https://solace.com/sam/ext/session-control/v1" {
			t.Fatal("session-control must not be advertised when no session store is configured")
		}
	}
}

func TestCompactSession_RegisteredOnAgentCard(t *testing.T) {
	mock := newMockLLM()
	store := newMemSessionStore()
	a := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	svc := newMockServices(mock, store)
	a.svc = svc
	if err := a.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	card := a.BuildAgentCard()
	found := false
	for _, e := range card.Capabilities.Extensions {
		if e.URI == "https://solace.com/sam/ext/session-control/v1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("session-control extension not advertised on agent card")
	}
}

type erroringLLM struct {
	err error
}

func (e *erroringLLM) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, e.err
}

func (e *erroringLLM) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, e.err
}

func TestEstimatePostCompactionTokens(t *testing.T) {
	cm := &CompactionMetadata{
		Summary:        "this is a short summary",
		CoveredThrough: 2,
	}
	msgs := []session.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "after compaction"},
	}
	got := estimatePostCompactionTokens(msgs, cm)
	if got <= 0 {
		t.Fatalf("expected non-zero post-compaction tokens, got %d", got)
	}
	if estimatePostCompactionTokens(msgs, nil) != 0 {
		t.Fatal("nil metadata must return 0")
	}
}
