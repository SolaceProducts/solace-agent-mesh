// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestHandleCancelRequest_PublishesSyntheticCancelledResponse(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-stuck"
	entry := &activeTaskEntry{
		cancel:      func() {},
		replyTo:     "test/reply",
		contextID:   "ctx-stuck",
		userProps:   a2a.UserProperties{UserID: "u", ClientID: "c", TraceID: "trace-1"},
		publishOnce: &sync.Once{},
	}
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = entry
	agent.activeTasksMu.Unlock()

	req := &a2a.Request{
		ID:     taskID,
		Method: "tasks/cancel",
		Params: map[string]any{"id": taskID},
	}
	agent.handleCancelRequest(req)

	published := brk.getPublished()
	if len(published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(published))
	}
	if published[0].topic != "test/reply" {
		t.Errorf("publish topic = %q, want %q", published[0].topic, "test/reply")
	}

	var resp a2a.Response
	if err := json.Unmarshal(published[0].payload, &resp); err != nil {
		t.Fatalf("unmarshal published response: %v", err)
	}
	if resp.ID != taskID {
		t.Errorf("resp.ID = %q, want %q", resp.ID, taskID)
	}
	body, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(body), "canceled") {
		t.Errorf("expected canceled state in result, got %s", string(body))
	}
}

func TestHandleCancelRequest_StampsRecordedTokenUsage(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-cancel-usage"
	entry := &activeTaskEntry{
		cancel:      func() {},
		replyTo:     "test/reply",
		contextID:   "ctx-usage",
		userProps:   a2a.UserProperties{UserID: "u", ClientID: "c"},
		publishOnce: &sync.Once{},
	}
	entry.recordUsage(taskUsageSnapshot{
		usage:           llm.Usage{PromptTokens: 230, CompletionTokens: 30, CachedTokens: 10, ReasoningTokens: 6, TotalTokens: 260},
		lastInputTokens: 130,
	})
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = entry
	agent.activeTasksMu.Unlock()

	agent.handleCancelRequest(&a2a.Request{ID: taskID, Method: "tasks/cancel", Params: map[string]any{"id": taskID}})

	published := brk.getPublished()
	if len(published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(published))
	}
	var resp a2a.Response
	if err := json.Unmarshal(published[0].payload, &resp); err != nil {
		t.Fatalf("unmarshal published response: %v", err)
	}
	body, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var task struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(body, &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	tu, ok := task.Metadata["token_usage"].(map[string]any)
	if !ok {
		t.Fatalf("cancelled response carried no token_usage: %#v", task.Metadata)
	}
	for key, want := range map[string]float64{
		"total_input_tokens":        230,
		"total_output_tokens":       30,
		"total_cached_input_tokens": 10,
		"total_reasoning_tokens":    6,
		"total_tokens":              260,
		"last_input_tokens":         130,
	} {
		if got, _ := tu[key].(float64); got != want {
			t.Errorf("token_usage[%q] = %v, want %v", key, tu[key], want)
		}
	}
}

func TestHandleCancelRequest_NoUsageRecordedOmitsBlock(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-cancel-no-usage"
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = &activeTaskEntry{
		cancel:      func() {},
		replyTo:     "test/reply",
		contextID:   "ctx-none",
		userProps:   a2a.UserProperties{UserID: "u", ClientID: "c"},
		publishOnce: &sync.Once{},
	}
	agent.activeTasksMu.Unlock()

	agent.handleCancelRequest(&a2a.Request{ID: taskID, Method: "tasks/cancel", Params: map[string]any{"id": taskID}})

	published := brk.getPublished()
	if len(published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(published))
	}
	var resp a2a.Response
	if err := json.Unmarshal(published[0].payload, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	body, _ := json.Marshal(resp.Result)
	var task struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(body, &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if _, present := task.Metadata["token_usage"]; present {
		t.Errorf("expected no token_usage when no LLM turn completed, got %v", task.Metadata["token_usage"])
	}
}

func TestHandleCancelRequest_PublishOnceGatesDoublePublish(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-once"
	entry := &activeTaskEntry{
		cancel:      func() {},
		replyTo:     "test/reply",
		contextID:   "ctx-once",
		userProps:   a2a.UserProperties{UserID: "u", ClientID: "c"},
		publishOnce: &sync.Once{},
	}
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = entry
	agent.activeTasksMu.Unlock()

	agent.handleCancelRequest(&a2a.Request{
		ID:     taskID,
		Method: "tasks/cancel",
		Params: map[string]any{"id": taskID},
	})

	entry.publishOnce.Do(func() {
		agent.publishTaskResponse(taskID, a2a.NewErrorResponse(taskID, -32000, "late real response"),
			entry.replyTo, entry.userProps)
	})

	published := brk.getPublished()
	if len(published) != 1 {
		t.Fatalf("publishOnce dedup failed: got %d publishes, want 1", len(published))
	}
}

func TestHandleCancelRequest_NoReplyToSkipsSyntheticPublish(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-no-replyto"
	var cancelCalled bool
	entry := &activeTaskEntry{
		cancel:      func() { cancelCalled = true },
		replyTo:     "",
		contextID:   "ctx-noreplyto",
		userProps:   a2a.UserProperties{UserID: "u"},
		publishOnce: &sync.Once{},
	}
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = entry
	agent.activeTasksMu.Unlock()

	agent.handleCancelRequest(&a2a.Request{
		ID:     taskID,
		Method: "tasks/cancel",
		Params: map[string]any{"id": taskID},
	})

	if !cancelCalled {
		t.Error("cancel fn should have been called")
	}
	if got := brk.getPublished(); len(got) != 0 {
		t.Errorf("no replyTo: expected 0 publishes, got %d", len(got))
	}
}

func TestHandleCancelRequest_EvictsInputRequiredTask(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "hil-task"
	state := pausedStateWithHILEntry(t, agent, taskID, "call-h", "hil-req-1")
	state.UserProps.ReplyTo = "test/reply"

	if agent.pausedTasks.Load(taskID) == nil {
		t.Fatal("precondition: HIL task should be parked in pausedTasks")
	}

	agent.handleCancelRequest(&a2a.Request{
		ID:     taskID,
		Method: "tasks/cancel",
		Params: map[string]any{"id": taskID},
	})

	if agent.pausedTasks.Load(taskID) != nil {
		t.Error("input-required task not evicted from pausedTasks after tasks/cancel")
	}

	found := false
	for _, p := range brk.getPublished() {
		if p.topic == "test/reply" && strings.Contains(string(p.payload), "canceled") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a canceled response on the reply topic; published=%v", brk.getPublished())
	}
}

func TestHandleCancelRequest_BothMapsRunsPausedTeardown(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-both-maps"
	state := pausedStateWithHILEntry(t, agent, taskID, "call-h", "hil-req-1")
	state.UserProps.ReplyTo = "test/reply"

	var activeCancelled bool
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = &activeTaskEntry{
		cancel:      func() { activeCancelled = true },
		replyTo:     "test/reply",
		contextID:   "ctx-both",
		userProps:   a2a.UserProperties{UserID: "u"},
		publishOnce: &sync.Once{},
	}
	agent.activeTasksMu.Unlock()

	agent.handleCancelRequest(&a2a.Request{
		ID:     taskID,
		Method: "tasks/cancel",
		Params: map[string]any{"id": taskID},
	})

	if !activeCancelled {
		t.Error("active context cancel fn should have been called for a task in both maps")
	}
	if agent.pausedTasks.Load(taskID) != nil {
		t.Error("paused entry not evicted: cancel took the active-only path instead of the full paused teardown")
	}
	if !state.Cancelled.Load() {
		t.Error("state.Cancelled not set: a late delivery would spuriously resume the cancelled task")
	}

	canceled := 0
	for _, p := range brk.getPublished() {
		if p.topic == "test/reply" && strings.Contains(string(p.payload), "canceled") {
			canceled++
		}
	}
	if canceled != 1 {
		t.Errorf("expected exactly 1 canceled response on the reply topic, got %d", canceled)
	}
}

func TestCancelPausedTask_StampsTokenUsage(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-paused-usage"
	state := pausedStateWithHILEntry(t, agent, taskID, "call-h", "hil-req-1")
	state.UserProps.ReplyTo = "test/reply"
	state.TotalUsage = llm.Usage{PromptTokens: 200, CompletionTokens: 45, CachedTokens: 12, ReasoningTokens: 9, TotalTokens: 245}
	state.LastInputTokens = 110

	agent.cancelPausedTask(state, cancelFromElsewhere)

	tu := cancelledTokenUsage(t, brk, "test/reply")
	if tu == nil {
		t.Fatalf("paused cancel carried no token_usage")
	}
	for key, want := range map[string]float64{
		"total_input_tokens":        200,
		"total_output_tokens":       45,
		"total_cached_input_tokens": 12,
		"total_reasoning_tokens":    9,
		"total_tokens":              245,
		"last_input_tokens":         110,
	} {
		if got, _ := tu[key].(float64); got != want {
			t.Errorf("token_usage[%q] = %v, want %v", key, tu[key], want)
		}
	}
}

func TestCancelPausedTask_PrefersActiveSnapshotOverLiveState(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-paused-resuming"
	state := pausedStateWithHILEntry(t, agent, taskID, "call-h", "hil-req-2")
	state.UserProps.ReplyTo = "test/reply"
	state.TotalUsage = llm.Usage{PromptTokens: 999, CompletionTokens: 999}
	state.LastInputTokens = 999
	entry := &activeTaskEntry{cancel: func() {}, publishOnce: &sync.Once{}}
	entry.recordUsage(taskUsageSnapshot{
		usage:           llm.Usage{PromptTokens: 300, CompletionTokens: 70},
		lastInputTokens: 160,
	})
	agent.activeTasksMu.Lock()
	agent.activeTasks[taskID] = entry
	agent.activeTasksMu.Unlock()

	agent.cancelPausedTask(state, cancelFromElsewhere)

	tu := cancelledTokenUsage(t, brk, "test/reply")
	if tu == nil {
		t.Fatalf("paused cancel carried no token_usage")
	}
	for key, want := range map[string]float64{
		"total_input_tokens": 300,
		"last_input_tokens":  160,
	} {
		if got, _ := tu[key].(float64); got != want {
			t.Errorf("token_usage[%q] = %v, want %v (the atomic snapshot, not the live racy struct)", key, tu[key], want)
		}
	}
}

func cancelledTokenUsage(t *testing.T, brk *mockBroker, topic string) map[string]any {
	t.Helper()
	for _, p := range brk.getPublished() {
		if p.topic != topic || !strings.Contains(string(p.payload), "canceled") {
			continue
		}
		var resp a2a.Response
		if err := json.Unmarshal(p.payload, &resp); err != nil {
			t.Fatalf("unmarshal published response: %v", err)
		}
		body, err := json.Marshal(resp.Result)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		var task struct {
			Metadata map[string]any `json:"metadata"`
		}
		if err := json.Unmarshal(body, &task); err != nil {
			t.Fatalf("unmarshal task: %v", err)
		}
		tu, _ := task.Metadata["token_usage"].(map[string]any)
		return tu
	}
	t.Fatalf("no cancelled response published to %s", topic)
	return nil
}

func TestCancelPausedTask_Idempotent(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	taskID := "task-idempotent"
	state := pausedStateWithHILEntry(t, agent, taskID, "call-h", "hil-req-1")
	state.UserProps.ReplyTo = "test/reply"

	agent.cancelPausedTask(state, cancelFromElsewhere)
	agent.cancelPausedTask(state, cancelFromElsewhere)

	canceled := 0
	for _, p := range brk.getPublished() {
		if p.topic == "test/reply" && strings.Contains(string(p.payload), "canceled") {
			canceled++
		}
	}
	if canceled != 1 {
		t.Errorf("cancelPausedTask not idempotent: expected 1 canceled publish, got %d", canceled)
	}
}

func TestPublishTaskResponse_DetachedContext(t *testing.T) {
	agent, _, _ := newTestAgent()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	parentCtx, cancel := context.WithCancel(t.Context())
	cancel()
	_ = parentCtx

	agent.publishTaskResponse("task-detached",
		a2a.NewErrorResponse("task-detached", -32000, "test"),
		"test/topic",
		a2a.UserProperties{UserID: "u"})

	if got := brk.getPublished(); len(got) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(got))
	}
}
