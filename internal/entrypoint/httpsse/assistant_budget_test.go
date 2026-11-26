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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type unreachableLLM struct{ t *testing.T }

func (m unreachableLLM) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.t.Error("the model was called while the budget was spent")
	return &llm.ChatResponse{}, nil
}

func (m unreachableLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	m.t.Error("the model was called while the budget was spent")
	return nil, nil
}

const budgetRefusalCopy = "usage budget is used up until"

func TestPromptBuilderChat_ASpentBudgetTellsTheUserWithoutCallingTheModel(t *testing.T) {
	h := setupPromptBuilderWithLLM(t, unreachableLLM{t})
	budgettest.BlockMesh(t, h.core.BudgetGate(), time.Now().Add(time.Hour))

	rec := httptest.NewRecorder()
	h.handleChat(rec, httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(`{"message":"create template"}`)))

	var resp ChatBuilderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("response = %d (decode %v), want 200 with a builder reply", rec.Code, err)
	}
	if !resp.IsError || !resp.IsWarning || !strings.Contains(resp.Message, budgetRefusalCopy) {
		t.Errorf("reply = %+v, want a warning-level error naming the spent budget", resp)
	}
}

func TestTaskBuilderChat_ASpentBudgetTellsTheUserWithoutCallingTheModel(t *testing.T) {
	h := setupTaskBuilderTest(t, unreachableLLM{t})
	budgettest.BlockMesh(t, h.core.BudgetGate(), time.Now().Add(time.Hour))

	body, _ := json.Marshal(TaskBuilderChatRequest{Message: "remind me every Monday"})
	rec := httptest.NewRecorder()
	h.handleChat(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body)))

	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("response = %d (decode %v), want 200 with a builder reply", rec.Code, err)
	}
	if !resp.IsError || !resp.IsWarning || !strings.Contains(resp.Message, budgetRefusalCopy) || resp.ReadyToSave {
		t.Errorf("reply = %+v, want a warning-level error naming the spent budget and not ready to save", resp)
	}
}

type failingLLM struct{}

func (failingLLM) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, errors.New("provider unavailable")
}

func (failingLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("provider unavailable")
}

func TestPromptBuilderChat_AModelFailureIsAnErrorNotAWarning(t *testing.T) {
	h := setupPromptBuilderWithLLM(t, failingLLM{})

	rec := httptest.NewRecorder()
	h.handleChat(rec, httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(`{"message":"create template"}`)))

	var resp ChatBuilderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("response = %d (decode %v), want 200 with a builder reply", rec.Code, err)
	}
	if !resp.IsError || resp.IsWarning {
		t.Errorf("reply = %+v, want an error that is not a warning", resp)
	}
}

func TestTaskBuilderChat_AModelFailureIsAnErrorNotAWarning(t *testing.T) {
	h := setupTaskBuilderTest(t, failingLLM{})

	body, _ := json.Marshal(TaskBuilderChatRequest{Message: "remind me every Monday"})
	rec := httptest.NewRecorder()
	h.handleChat(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body)))

	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("response = %d (decode %v), want 200 with a builder reply", rec.Code, err)
	}
	if !resp.IsError || resp.IsWarning || resp.ReadyToSave {
		t.Errorf("reply = %+v, want an error that is not a warning and not ready to save", resp)
	}
}
