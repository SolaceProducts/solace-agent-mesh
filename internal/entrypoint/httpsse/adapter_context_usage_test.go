// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func finalResponseFixture(taskID, sessionID string, tokenUsage map[string]any) []byte {
	envelope := map[string]any{
		"jsonrpc": "2.0",
		"id":      taskID,
		"result": map[string]any{
			"id":        taskID,
			"contextId": sessionID,
			"kind":      "task",
			"status":    map[string]any{"state": "completed"},
			"metadata": map[string]any{
				"token_usage": tokenUsage,
			},
		},
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return out
}

func taskFromFixture(taskID, sessionID string, tokenUsage map[string]any) *a2a.Task {
	return &a2a.Task{
		ID:        taskID,
		ContextID: sessionID,
		Kind:      "task",
		Status:    a2a.TaskStatus{State: "completed"},
		Metadata: map[string]any{
			"token_usage": tokenUsage,
		},
	}
}

func TestOnFinalResponse_InjectsContextUsage(t *testing.T) {
	adapter, _, sessRepo, chatRepo, _ := setupTestAdapterWithDB(t)
	ctx := context.Background()

	sid := idgen.New()
	user := "sam_dev_user"
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := chatRepo.Create(ctx, &store.ChatTask{
			ID: idgen.New(), SessionID: sid, UserID: user, MessageBubbles: "[]",
		}); err != nil {
			t.Fatal(err)
		}
	}
	priorIn, priorOut := 1000, 200
	priorDetails := `{"by_model": {"claude-opus-4-7": {"input_tokens": 1000, "output_tokens": 200, "max_input_tokens": 200000}}}`
	if err := adapter.taskRepo.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: user,
		StartTime:         time.Now().Add(-time.Minute).UnixMilli(),
		ExecutionMode:     "foreground",
		SessionID:         &sid,
		TotalInputTokens:  &priorIn,
		TotalOutputTokens: &priorOut,
		TokenUsageDetails: &priorDetails,
	}); err != nil {
		t.Fatal(err)
	}

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, sid, user)
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	currentIn, currentOut := 8421, 1240
	usage := map[string]any{
		"total_input_tokens":        currentIn,
		"total_output_tokens":       currentOut,
		"total_cached_input_tokens": 0,
		"by_model": map[string]any{
			"claude-opus-4-7": map[string]any{
				"input_tokens":     currentIn,
				"output_tokens":    currentOut,
				"max_input_tokens": 200000,
			},
		},
	}
	raw := finalResponseFixture(taskID, sid, usage)
	task := taskFromFixture(taskID, sid, usage)

	adapter.OnFinalResponse(ctx, taskID, task, raw)

	select {
	case evt := <-ch:
		if evt.EventType != "final_response" {
			t.Fatalf("event type = %q, want final_response", evt.EventType)
		}
		if !evt.Final {
			t.Fatal("expected Final=true")
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(evt.Data, &envelope); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if _, ok := envelope["result"]; !ok {
			t.Error("envelope missing `result` (existing field clobbered)")
		}
		cuRaw, ok := envelope["context_usage"]
		if !ok {
			t.Fatalf("envelope missing `context_usage`; got keys: %v", keys(envelope))
		}
		var cu ContextUsageResponse
		if err := json.Unmarshal(cuRaw, &cu); err != nil {
			t.Fatalf("unmarshal context_usage: %v", err)
		}
		if cu.SessionID != sid {
			t.Errorf("sessionId = %q, want %q", cu.SessionID, sid)
		}
		if cu.CurrentContextTokens != currentIn {
			t.Errorf("currentContextTokens = %d, want %d (latest task's input)",
				cu.CurrentContextTokens, currentIn)
		}
		if cu.PromptTokens != priorIn+currentIn {
			t.Errorf("promptTokens = %d, want %d (prior + current)",
				cu.PromptTokens, priorIn+currentIn)
		}
		if cu.CompletionTokens != priorOut+currentOut {
			t.Errorf("completionTokens = %d, want %d (prior + current)",
				cu.CompletionTokens, priorOut+currentOut)
		}
		if cu.MaxInputTokens == nil || *cu.MaxInputTokens != 200000 {
			t.Errorf("maxInputTokens = %v, want 200000 (stamped)", cu.MaxInputTokens)
		}
		if cu.Model != "claude-opus-4-7" {
			t.Errorf("model = %q, want claude-opus-4-7", cu.Model)
		}
		if cu.TotalTasks != 2 || cu.TotalMessages != 4 {
			t.Errorf("totals = (tasks=%d, messages=%d), want (2, 4)",
				cu.TotalTasks, cu.TotalMessages)
		}
	default:
		t.Fatal("expected SSE event but channel was empty")
	}
}

func TestOnFinalResponse_DedupsLatestTaskRow(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)
	ctx := context.Background()

	sid := idgen.New()
	user := "sam_dev_user"
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}

	taskID := idgen.New()
	currentIn, currentOut := 5000, 1000
	currentDetails := `{"by_model": {"gpt-4o": {"input_tokens": 5000, "output_tokens": 1000, "max_input_tokens": 128000}}}`
	if err := adapter.taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: user,
		StartTime:         time.Now().UnixMilli(),
		ExecutionMode:     "foreground",
		SessionID:         &sid,
		TotalInputTokens:  &currentIn,
		TotalOutputTokens: &currentOut,
		TokenUsageDetails: &currentDetails,
	}); err != nil {
		t.Fatal(err)
	}

	adapter.sseManager.RegisterTask(taskID, sid, user)
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	usage := map[string]any{
		"total_input_tokens":  currentIn,
		"total_output_tokens": currentOut,
		"by_model": map[string]any{
			"gpt-4o": map[string]any{
				"input_tokens":     currentIn,
				"output_tokens":    currentOut,
				"max_input_tokens": 128000,
			},
		},
	}
	raw := finalResponseFixture(taskID, sid, usage)
	task := taskFromFixture(taskID, sid, usage)

	adapter.OnFinalResponse(ctx, taskID, task, raw)

	evt := <-ch
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(evt.Data, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var cu ContextUsageResponse
	if err := json.Unmarshal(envelope["context_usage"], &cu); err != nil {
		t.Fatalf("unmarshal context_usage: %v", err)
	}
	if cu.PromptTokens != currentIn {
		t.Errorf("promptTokens = %d, want %d (single contribution; double-count regression?)",
			cu.PromptTokens, currentIn)
	}
	if cu.CompletionTokens != currentOut {
		t.Errorf("completionTokens = %d, want %d (single contribution)",
			cu.CompletionTokens, currentOut)
	}
	if cu.CurrentContextTokens != currentIn {
		t.Errorf("currentContextTokens = %d, want %d", cu.CurrentContextTokens, currentIn)
	}
}

func TestOnFinalResponse_UnknownModelNullsLimit(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)
	ctx := context.Background()

	sid := idgen.New()
	user := "sam_dev_user"
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, sid, user)
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	usage := map[string]any{
		"total_input_tokens":  100,
		"total_output_tokens": 10,
		"by_model": map[string]any{
			"made-up-model": map[string]any{
				"input_tokens":  100,
				"output_tokens": 10,
			},
		},
	}
	raw := finalResponseFixture(taskID, sid, usage)
	task := taskFromFixture(taskID, sid, usage)

	adapter.OnFinalResponse(ctx, taskID, task, raw)

	evt := <-ch
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(evt.Data, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var cu ContextUsageResponse
	if err := json.Unmarshal(envelope["context_usage"], &cu); err != nil {
		t.Fatalf("unmarshal context_usage: %v", err)
	}
	if cu.MaxInputTokens != nil {
		t.Errorf("maxInputTokens = %v, want nil for unknown model", cu.MaxInputTokens)
	}
	if cu.UsagePercentage != 0 {
		t.Errorf("usagePercentage = %v, want 0 (no denominator)", cu.UsagePercentage)
	}
}

func TestOnFinalResponse_NoContextUsageWithoutMetadata(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)
	ctx := context.Background()

	sid := idgen.New()
	user := "sam_dev_user"
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, sid, user)
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	envelope := map[string]any{
		"jsonrpc": "2.0",
		"id":      taskID,
		"result": map[string]any{
			"id":        taskID,
			"contextId": sid,
			"kind":      "task",
			"status":    map[string]any{"state": "completed"},
			"metadata":  map[string]any{},
		},
	}
	raw, _ := json.Marshal(envelope)
	task := &a2a.Task{
		ID: taskID, ContextID: sid, Kind: "task",
		Status:   a2a.TaskStatus{State: "completed"},
		Metadata: map[string]any{},
	}

	adapter.OnFinalResponse(ctx, taskID, task, raw)

	evt := <-ch
	if evt.EventType != "final_response" {
		t.Fatalf("event type = %q, want final_response", evt.EventType)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(evt.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got["context_usage"]; ok {
		t.Errorf("context_usage should be absent when no token_usage stamped; got keys: %v", keys(got))
	}
}

func TestOnFinalResponse_NoContextUsageForADecisionOnlyWorkflowRun(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)
	ctx := t.Context()

	sid := idgen.New()
	user := "sam_dev_user"
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, sid, user)
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	usage := map[string]any{
		"total_input_tokens": 400, "total_output_tokens": 0, "total_cached_input_tokens": 0, "total_tokens": 400,
		"by_source": map[string]any{"decision": map[string]any{"input_tokens": 400, "output_tokens": 0}},
		"by_model":  map[string]any{"jev-1.13.0": map[string]any{"input_tokens": 400, "output_tokens": 0, "kind": "decision"}},
	}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": taskID, "result": map[string]any{
		"id": taskID, "contextId": sid, "kind": "task", "status": map[string]any{"state": "completed"}, "metadata": map[string]any{"token_usage": usage},
	}})
	task := &a2a.Task{ID: taskID, ContextID: sid, Kind: "task", Status: a2a.TaskStatus{State: "completed"}, Metadata: map[string]any{"token_usage": usage}}

	adapter.OnFinalResponse(ctx, taskID, task, raw)

	evt := <-ch
	var got map[string]json.RawMessage
	if err := json.Unmarshal(evt.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got["context_usage"]; ok {
		t.Errorf("context_usage present for a decision-only workflow run; keys: %v", keys(got))
	}
}

func TestOnFinalResponse_RaceFreeUnderConcurrency(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)
	ctx := context.Background()

	sid := idgen.New()
	user := "sam_dev_user"
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}

	const n = 8
	type result struct {
		prompt int
	}
	results := make([]result, n)
	var wg sync.WaitGroup

	for i := range n {
		taskID := idgen.New()
		adapter.sseManager.RegisterTask(taskID, sid, user)
		ch, _, _ := adapter.sseManager.Subscribe(taskID)

		usage := map[string]any{
			"total_input_tokens":  100 + i,
			"total_output_tokens": 10,
			"by_model": map[string]any{
				"claude-opus-4-7": map[string]any{
					"input_tokens":     100 + i,
					"output_tokens":    10,
					"max_input_tokens": 200000,
				},
			},
		}
		raw := finalResponseFixture(taskID, sid, usage)
		task := taskFromFixture(taskID, sid, usage)

		wg.Go(func() {
			adapter.OnFinalResponse(ctx, taskID, task, raw)
			evt := <-ch
			var env map[string]json.RawMessage
			if err := json.Unmarshal(evt.Data, &env); err != nil {
				t.Errorf("unmarshal: %v", err)
				return
			}
			var cu ContextUsageResponse
			if err := json.Unmarshal(env["context_usage"], &cu); err != nil {
				t.Errorf("unmarshal context_usage: %v", err)
				return
			}
			results[i] = result{prompt: cu.PromptTokens}
		})

		ti := 100 + i
		to := 10
		details, _ := json.Marshal(usage)
		s := string(details)
		_ = adapter.taskRepo.Create(ctx, &store.Task{
			ID: taskID, UserID: user,
			StartTime:         time.Now().UnixMilli(),
			ExecutionMode:     "foreground",
			SessionID:         &sid,
			TotalInputTokens:  &ti,
			TotalOutputTokens: &to,
			TokenUsageDetails: &s,
		})
	}

	wg.Wait()

	const fullTotal = 8*100 + 28

	for i, r := range results {
		minExpected := 100 + i
		if r.prompt < minExpected {
			t.Errorf("task %d: promptTokens = %d, want >= %d (own contribution)",
				i, r.prompt, minExpected)
		}
		if r.prompt > fullTotal {
			t.Errorf("task %d: promptTokens = %d, want <= %d (dedup regression — current task counted twice?)",
				i, r.prompt, fullTotal)
		}
	}

	maxSeen := 0
	for _, r := range results {
		if r.prompt > maxSeen {
			maxSeen = r.prompt
		}
	}
	const minOfMax = 200
	if maxSeen < minOfMax {
		t.Errorf("max observed promptTokens = %d, want >= %d (no broadcast saw any peer's contribution)",
			maxSeen, minOfMax)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestOnFinalResponse_CancelledTaskDoesNotPinContextUsage(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)
	ctx := t.Context()

	sid := idgen.New()
	user := "sam_dev_user"
	if err := sessRepo.Create(ctx, &store.Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, sid, user)
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	usage := map[string]any{
		"total_input_tokens":  230,
		"total_output_tokens": 50,
		"last_input_tokens":   130,
	}
	envelope := map[string]any{
		"jsonrpc": "2.0",
		"id":      taskID,
		"result": map[string]any{
			"id":        taskID,
			"contextId": sid,
			"kind":      "task",
			"status":    map[string]any{"state": "canceled"},
			"metadata":  map[string]any{"token_usage": usage},
		},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	task := &a2a.Task{
		ID: taskID, ContextID: sid, Kind: "task",
		Status:   a2a.TaskStatus{State: "canceled"},
		Metadata: map[string]any{"token_usage": usage},
	}

	adapter.OnFinalResponse(ctx, taskID, task, raw)

	evt := <-ch
	var got map[string]json.RawMessage
	if err := json.Unmarshal(evt.Data, &got); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	blob, ok := got["context_usage"]
	if !ok {
		t.Fatalf("cancelled final response carried no context_usage; keys: %v", keys(got))
	}
	var cu ContextUsageResponse
	if err := json.Unmarshal(blob, &cu); err != nil {
		t.Fatalf("unmarshal context_usage: %v", err)
	}
	if cu.CurrentContextTokens != 130 {
		t.Errorf("currentContextTokens = %d, want 130 (the last call, not the 230 cumulative sum)",
			cu.CurrentContextTokens)
	}
	if cu.PromptTokens != 230 {
		t.Errorf("promptTokens = %d, want 230 (cumulative)", cu.PromptTokens)
	}
}
