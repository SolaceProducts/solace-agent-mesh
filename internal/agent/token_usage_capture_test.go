// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestHandleTask_TokenUsage_SurfacesReasoningAndCached(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{
		Content:      "answer",
		FinishReason: "stop",
		Usage:        &llm.Usage{PromptTokens: 100, CompletionTokens: 40, TotalTokens: 140, CachedTokens: 20, CacheWriteTokens: 5, ReasoningTokens: 15},
	})

	resp, err := agent.HandleTask(
		context.Background(),
		"task-tokens-1",
		newUserMessage("hello"),
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	tu := taskTokenUsage(t, resp)

	assertNum := func(key string, want float64) {
		t.Helper()
		if got, _ := tu[key].(float64); got != want {
			t.Errorf("token_usage[%q] = %v, want %v", key, tu[key], want)
		}
	}
	assertNum("total_input_tokens", 100)
	assertNum("total_output_tokens", 40)
	assertNum("total_cached_input_tokens", 20)
	assertNum("total_cache_write_tokens", 5)
	assertNum("total_reasoning_tokens", 15)

	bySource, _ := tu["by_source"].(map[string]any)
	agentSrc, _ := bySource["agent"].(map[string]any)
	if got, _ := agentSrc["reasoning_tokens"].(float64); got != 15 {
		t.Errorf("by_source.agent.reasoning_tokens = %v, want 15", agentSrc["reasoning_tokens"])
	}
	if got, _ := agentSrc["cached_input_tokens"].(float64); got != 20 {
		t.Errorf("by_source.agent.cached_input_tokens = %v, want 20", agentSrc["cached_input_tokens"])
	}
	if got, _ := agentSrc["cache_write_tokens"].(float64); got != 5 {
		t.Errorf("by_source.agent.cache_write_tokens = %v, want 5", agentSrc["cache_write_tokens"])
	}

	byModel, _ := tu["by_model"].(map[string]any)
	if len(byModel) != 1 {
		t.Fatalf("expected exactly one by_model entry, got %d: %#v", len(byModel), byModel)
	}
	for model, e := range byModel {
		entry, _ := e.(map[string]any)
		if got, _ := entry["reasoning_tokens"].(float64); got != 15 {
			t.Errorf("by_model[%q].reasoning_tokens = %v, want 15", model, entry["reasoning_tokens"])
		}
		if got, _ := entry["cache_write_tokens"].(float64); got != 5 {
			t.Errorf("by_model[%q].cache_write_tokens = %v, want 5", model, entry["cache_write_tokens"])
		}
	}
}

func TestInternalLLMCall_PublishesCachedAndReasoningTokens(t *testing.T) {
	mockClient := newMockLLM(&llm.ChatResponse{
		Content: "summary",
		Usage:   &llm.Usage{PromptTokens: 30, CompletionTokens: 12, TotalTokens: 42, CachedTokens: 8, ReasoningTokens: 20},
	})
	h := newInternalLLMHarness(t, mockClient, "task-tok-signal")

	if _, err := h.agent.internalLLMCall(t.Context(), internalCallSinks{sp: h.sp}, "", "sys", "user", 0.2); err != nil {
		t.Fatalf("internalLLMCall: %v", err)
	}

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	usage, ok := signals[1]["usage"].(map[string]any)
	if !ok {
		t.Fatalf("signal[1].usage missing: %+v", signals[1])
	}
	if got, _ := usage["cached_input_tokens"].(float64); got != 8 {
		t.Errorf("usage.cached_input_tokens = %v, want 8", usage["cached_input_tokens"])
	}
	if got, _ := usage["reasoning_tokens"].(float64); got != 20 {
		t.Errorf("usage.reasoning_tokens = %v, want 20", usage["reasoning_tokens"])
	}
}

func TestHandleTask_TokenUsage_AccumulatesAcrossTurns(t *testing.T) {
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{
			ToolCalls:    []llm.ToolCall{{ID: "tc-1", Name: "_continue_generation", Arguments: "{}"}},
			FinishReason: "tool_calls",
			Usage:        &llm.Usage{PromptTokens: 100, CompletionTokens: 20, CachedTokens: 10, ReasoningTokens: 5, TotalTokens: 120},
		},
		&llm.ChatResponse{
			Content:      "final answer",
			FinishReason: "stop",
			Usage:        &llm.Usage{PromptTokens: 130, CompletionTokens: 30, CachedTokens: 15, ReasoningTokens: 7, TotalTokens: 160},
		},
	)

	resp, err := agent.HandleTask(
		context.Background(),
		"task-multi-turn",
		newUserMessage("hi"),
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	tu := taskTokenUsage(t, resp)
	for key, want := range map[string]float64{
		"total_input_tokens":        230,
		"total_output_tokens":       50,
		"total_cached_input_tokens": 25,
		"total_reasoning_tokens":    12,
	} {
		if got, _ := tu[key].(float64); got != want {
			t.Errorf("token_usage[%q] = %v, want %v (sum of both turns)", key, tu[key], want)
		}
	}
}

func TestHandleTask_RecordsUsageOntoActiveEntry(t *testing.T) {
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{
			ToolCalls:    []llm.ToolCall{{ID: "tc-1", Name: "_continue_generation", Arguments: "{}"}},
			FinishReason: "tool_calls",
			Usage:        &llm.Usage{PromptTokens: 30, CompletionTokens: 10, CachedTokens: 1, ReasoningTokens: 1, TotalTokens: 40},
		},
		&llm.ChatResponse{
			Content:      "done",
			FinishReason: "stop",
			Usage:        &llm.Usage{PromptTokens: 50, CompletionTokens: 15, CachedTokens: 3, ReasoningTokens: 2, TotalTokens: 65},
		},
	)

	taskID := "task-records-usage"
	entry := &activeTaskEntry{cancel: func() {}, publishOnce: &sync.Once{}}
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = entry
	agent.activeTasksMu.Unlock()

	if _, err := agent.HandleTask(context.Background(), taskID, newUserMessage("hi"),
		a2a.UserProperties{ClientID: "u1", UserID: "u1"}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	got := entry.usageSnapshot()
	if got.usage.PromptTokens != 80 || got.usage.CompletionTokens != 25 ||
		got.usage.CachedTokens != 4 || got.usage.ReasoningTokens != 3 {
		t.Errorf("entry snapshot usage = %+v, want {80 25 _ 4 3} — continueTaskLoop must record usage onto the active entry", got.usage)
	}
	if got.lastInputTokens != 50 {
		t.Errorf("entry snapshot lastInputTokens = %d, want 50 (the second turn, not the 80 sum)", got.lastInputTokens)
	}
}

func TestHandleTask_CancelAfterTurnsReportsLastInputNotSum(t *testing.T) {
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{
			ToolCalls:    []llm.ToolCall{{ID: "tc-1", Name: "_continue_generation", Arguments: "{}"}},
			FinishReason: "tool_calls",
			Usage:        &llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		},
		&llm.ChatResponse{
			Content:      "final",
			FinishReason: "stop",
			Usage:        &llm.Usage{PromptTokens: 130, CompletionTokens: 30, TotalTokens: 160},
		},
	)
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-cancel-after-turns"
	entry := &activeTaskEntry{
		cancel:      func() {},
		replyTo:     "test/reply",
		contextID:   "ctx-cancel",
		userProps:   a2a.UserProperties{UserID: "u", ClientID: "c"},
		publishOnce: &sync.Once{},
	}
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = entry
	agent.activeTasksMu.Unlock()

	if _, err := agent.HandleTask(t.Context(), taskID, newUserMessage("hi"),
		a2a.UserProperties{ClientID: "u", UserID: "u"}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	agent.handleCancelRequest(&a2a.Request{ID: taskID, Method: "tasks/cancel", Params: map[string]any{"id": taskID}})

	tu := cancelledTokenUsage(t, brk, "test/reply")
	if tu == nil {
		t.Fatalf("cancelled response carried no token_usage")
	}
	for key, want := range map[string]float64{
		"total_input_tokens": 230,
		"last_input_tokens":  130,
	} {
		if got, _ := tu[key].(float64); got != want {
			t.Errorf("token_usage[%q] = %v, want %v", key, tu[key], want)
		}
	}
}

func taskTokenUsage(t *testing.T, resp *a2a.Response) map[string]any {
	t.Helper()
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var task struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	tu, ok := task.Metadata["token_usage"].(map[string]any)
	if !ok {
		t.Fatalf("metadata.token_usage missing or wrong shape: %#v", task.Metadata)
	}
	return tu
}

func TestBuildStructuredResponse_StampsLastInputTokens(t *testing.T) {
	agent, _, _ := newTestAgent()

	resp, err := agent.buildStructuredResponse("task-structured", newUserMessage("go"),
		&a2a.StructuredInvocationResult{Type: "structured_invocation_result", Status: "success"},
		taskUsageSnapshot{
			usage:           llm.Usage{PromptTokens: 230, CompletionTokens: 40, TotalTokens: 270},
			lastInputTokens: 130,
		}, nil)
	if err != nil {
		t.Fatalf("buildStructuredResponse: %v", err)
	}

	tu := taskTokenUsage(t, resp)
	for key, want := range map[string]float64{
		"total_input_tokens": 230,
		"last_input_tokens":  130,
	} {
		if got, _ := tu[key].(float64); got != want {
			t.Errorf("token_usage[%q] = %v, want %v", key, tu[key], want)
		}
	}
}

func TestBuildStructuredResponse_NoUsageOmitsBlock(t *testing.T) {
	agent, _, _ := newTestAgent()

	resp, err := agent.buildStructuredResponse("task-structured-empty", newUserMessage("go"),
		&a2a.StructuredInvocationResult{Type: "structured_invocation_result", Status: "error"},
		taskUsageSnapshot{}, nil)
	if err != nil {
		t.Fatalf("buildStructuredResponse: %v", err)
	}
	task, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("Result type = %T", resp.Result)
	}
	if _, present := task.Metadata["token_usage"]; present {
		t.Errorf("token_usage must be absent when no usage was recorded, got %v", task.Metadata["token_usage"])
	}
}

func TestTaskUsageSnapshotAdd(t *testing.T) {
	var snap taskUsageSnapshot
	snap.add(&llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, CachedTokens: 10, CacheWriteTokens: 4, ReasoningTokens: 5})
	snap.add(&llm.Usage{PromptTokens: 130, CompletionTokens: 30, TotalTokens: 160, CachedTokens: 15, CacheWriteTokens: 6, ReasoningTokens: 7})

	want := taskUsageSnapshot{
		usage:           llm.Usage{PromptTokens: 230, CompletionTokens: 50, TotalTokens: 280, CachedTokens: 25, CacheWriteTokens: 10, ReasoningTokens: 12},
		lastInputTokens: 130,
	}
	if snap != want {
		t.Errorf("after two adds = %+v, want %+v", snap, want)
	}

	snap.add(nil)
	if snap != want {
		t.Errorf("add(nil) mutated the snapshot: %+v, want %+v", snap, want)
	}
}

func TestReleaseActiveTask_KeepsLiveResumeEntry(t *testing.T) {
	agent, _, _ := newTestAgent()
	taskID := "task-pause-resume"

	original := &activeTaskEntry{cancel: func() {}, publishOnce: &sync.Once{}}
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = original
	agent.activeTasksMu.Unlock()

	resume := &activeTaskEntry{cancel: func() {}, publishOnce: &sync.Once{}}
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = resume
	agent.activeTasksMu.Unlock()

	agent.releaseActiveTask(taskID, original)

	agent.activeTasksMu.Lock()
	got := agent.activeTasks[taskID]
	agent.activeTasksMu.Unlock()
	if got != resume {
		t.Fatalf("original run's release evicted the live resume entry (got %v)", got)
	}

	agent.recordActiveTaskUsage(taskID, taskUsageSnapshot{
		usage:           llm.Usage{PromptTokens: 28000, CompletionTokens: 400},
		lastInputTokens: 20000,
	})
	if snap := resume.usageSnapshot(); snap.usage.PromptTokens != 28000 || snap.lastInputTokens != 20000 {
		t.Errorf("recordActiveTaskUsage no-oped for the resumed run: %+v", snap)
	}

	agent.releaseActiveTask(taskID, resume)
	agent.activeTasksMu.Lock()
	_, present := agent.activeTasks[taskID]
	agent.activeTasksMu.Unlock()
	if present {
		t.Error("resume entry leaked after its own release")
	}
}

func TestReleaseActiveTask_ResumeFinishesFirst(t *testing.T) {
	agent, _, _ := newTestAgent()
	taskID := "task-resume-first"

	original := &activeTaskEntry{cancel: func() {}, publishOnce: &sync.Once{}}
	resume := &activeTaskEntry{cancel: func() {}, publishOnce: &sync.Once{}}
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = resume
	agent.activeTasksMu.Unlock()

	agent.releaseActiveTask(taskID, resume)
	agent.releaseActiveTask(taskID, original)

	agent.activeTasksMu.Lock()
	_, present := agent.activeTasks[taskID]
	agent.activeTasksMu.Unlock()
	if present {
		t.Error("expected the task to be deregistered")
	}
}
