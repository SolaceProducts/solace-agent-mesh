// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func assertToolCallsPaired(t *testing.T, msgs []llm.ChatMessage) {
	t.Helper()
	resultCounts := make(map[string]int)
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			resultCounts[m.ToolCallID]++
		}
	}
	for i, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			switch resultCounts[tc.ID] {
			case 1:
			case 0:
				t.Errorf("orphan tool_use %q (%s) on message %d has no tool_result", tc.ID, tc.Name, i)
			default:
				t.Errorf("tool_use %q (%s) on message %d has %d tool_results, want 1", tc.ID, tc.Name, i, resultCounts[tc.ID])
			}
		}
	}
}

func registerAddTool(t *testing.T, agent *Agent) {
	t.Helper()
	err := agent.Tools().Register(&testBuiltinTool{
		name:        "add",
		description: "Adds two numbers",
		params: &tool.JSONSchema{
			Type: "object",
			Properties: map[string]any{
				"a": map[string]any{"type": "number"},
				"b": map[string]any{"type": "number"},
			},
		},
		execFn: func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
			a := args["a"].(float64)
			b := args["b"].(float64)
			return tool.OK(fmt.Sprintf("%.0f", a+b)), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleTaskMixedNotifyHallucinationAndValidCall(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			Content:      "Adding and notifying.",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-notify", Name: "_notify_artifact_save", Arguments: `{"filename":"f.txt","version":1,"status":"saved"}`},
				{ID: "call-add", Name: "add", Arguments: `{"a":2,"b":3}`},
			},
		},
		&llm.ChatResponse{Content: "The sum is 5.", FinishReason: "stop"},
	)
	registerAddTool(t, agent)

	resp, err := agent.HandleTask(
		t.Context(),
		"task-mixed",
		newUserMessage("What is 2 + 3?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	msgs := calls[1].Messages
	assertToolCallsPaired(t, msgs)

	var addResult, notifyResult string
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		switch m.ToolCallID {
		case "call-add":
			addResult = m.Content
		case "call-notify":
			notifyResult = m.Content
		}
	}
	if !strings.Contains(addResult, `"status":"success"`) {
		t.Errorf("valid call result missing or wrong: %q", addResult)
	}
	if !strings.Contains(notifyResult, "internal side-effect tool") {
		t.Errorf("hallucinated call should get the corrective error result, got %q", notifyResult)
	}
}

func TestHandleTaskMixedNotifyHallucinationWithCreatedArtifacts(t *testing.T) {
	artifactContent := BlockPrefixSaveArtifact + " filename=\"hello.txt\" description=\"A greeting\"\nHello World\n" + BlockDelimiterClose + "\nSaved, now adding."

	mock := newMockLLM(
		&llm.ChatResponse{
			Content:      artifactContent,
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-notify", Name: "_notify_artifact_save", Arguments: `{"filename":"hello.txt","version":1,"status":"saved"}`},
				{ID: "call-add", Name: "add", Arguments: `{"a":2,"b":3}`},
			},
		},
		&llm.ChatResponse{Content: "All done.", FinishReason: "stop"},
	)
	store := newMemSessionStore()

	agent := New(AgentConfig{
		Name:         "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  5,
		Namespace:    "test-ns",
	})
	svc := newMockServices(mock, store)
	svc.brk = newMockBroker()
	svc.arts = &artifact.ScopedService{
		Inner:     memoryArtifactStore(),
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	agent.svc = svc
	agent.Init(t.Context(), nil, svc) //nolint:errcheck // test setup, error checked via agent behavior
	registerAddTool(t, agent)

	resp, err := agent.HandleTask(
		t.Context(),
		"task-mixed-artifact",
		newUserMessage("Create hello.txt and add 2+3"),
		a2a.UserProperties{
			ClientID:       "user-1",
			UserID:         "user-1",
			A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	msgs := calls[1].Messages
	assertToolCallsPaired(t, msgs)

	var sawHostNotify, sawNotifyError, sawAddResult bool
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		switch {
		case strings.HasPrefix(m.ToolCallID, hostNotifyCallIDPrefix):
			sawHostNotify = true
		case m.ToolCallID == "call-notify":
			sawNotifyError = strings.Contains(m.Content, "internal side-effect tool")
		case m.ToolCallID == "call-add":
			sawAddResult = strings.Contains(m.Content, `"status":"success"`)
		}
	}
	if !sawHostNotify {
		t.Error("expected injected host-notify tool result for the created artifact")
	}
	if !sawNotifyError {
		t.Error("expected corrective error result for the hallucinated _notify_artifact_save call")
	}
	if !sawAddResult {
		t.Error("expected success result for the valid add call")
	}
}

func TestResumePairsSyntheticNotifyErrorAfterPause(t *testing.T) {
	const ns, name = "ns", "resume-notify-agent"

	artifactContent := BlockPrefixSaveArtifact + " filename=\"hello.txt\" description=\"A greeting\"\nHello World\n" + BlockDelimiterClose + "\nSaved, delegating."

	mock := newMockLLM(
		&llm.ChatResponse{
			Content:      artifactContent,
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-notify", Name: "_notify_artifact_save", Arguments: `{"filename":"f.txt","version":1,"status":"saved"}`},
				{ID: "call-add", Name: "add", Arguments: `{"a":2,"b":3}`},
				{ID: "call-wf", Name: "workflow_self", Arguments: "{}"},
			},
		},
		&llm.ChatResponse{Content: "all done", FinishReason: "stop"},
	)

	agent := New(AgentConfig{Name: name, Namespace: ns, MaxLLMCalls: 5})
	svc := newMockServices(mock, newMemSessionStore())
	svc.brk = newMockBroker()
	svc.arts = &artifact.ScopedService{
		Inner:     memoryArtifactStore(),
		Scope:     "namespace",
		Namespace: ns,
	}
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	agent.logger = discardLogger()
	agent.runCtx = t.Context()
	registerAddTool(t, agent)

	deliverCh := make(chan func(*tool.Result), 1)
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "workflow_self",
		description: "pausing delegation tool",
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			r := tool.Pending("waiting for workflow", tool.WithData(map[string]any{
				"sub_task_id":  "st",
				"pending_kind": "workflow",
			}))
			r.OnDelivery = func(deliver func(*tool.Result)) { deliverCh <- deliver }
			return r, nil
		},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	submitTaskMessage(t, agent, ns, name, "taskN", "ctxN")
	waitForPausedTask(t, agent, "taskN")

	select {
	case deliver := <-deliverCh:
		deliver(tool.OK("workflow finished"))
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher never invoked the tool's OnDelivery")
	}

	deadline := time.After(5 * time.Second)
	for agent.pausedTasks.Load("taskN") != nil {
		select {
		case <-deadline:
			t.Fatal("task did not resume to completion after delivery")
		case <-time.After(5 * time.Millisecond):
		}
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}

	msgs := calls[1].Messages
	assertToolCallsPaired(t, msgs)

	var sawHostNotify, sawNotifyError, sawAddResult, sawWorkflowResult bool
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		switch {
		case strings.HasPrefix(m.ToolCallID, hostNotifyCallIDPrefix):
			sawHostNotify = true
		case m.ToolCallID == "call-notify":
			sawNotifyError = strings.Contains(m.Content, "internal side-effect tool")
		case m.ToolCallID == "call-add":
			sawAddResult = strings.Contains(m.Content, `"message":"5"`)
		case m.ToolCallID == "call-wf":
			sawWorkflowResult = strings.Contains(m.Content, "workflow finished")
		}
	}
	if !sawHostNotify {
		t.Error("expected injected host-notify tool result for the created artifact")
	}
	if !sawNotifyError {
		t.Error("expected the synthetic notify error replayed after resume")
	}
	if !sawAddResult {
		t.Error("expected the sync add result paired with call-add after resume (dispatch-index skew)")
	}
	if !sawWorkflowResult {
		t.Error("expected the delivered workflow result after resume")
	}
}

func TestCancelPausedTask_FilteredCallDoesNotSkewSyncResultPairing(t *testing.T) {
	agent, _, store := newTestAgent()

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u-skew",
		SessionID: "s-skew",
		ContextID: "s-skew",
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.TaskID = "task-skew"
	state.EffectiveSessionID = "s-skew"
	state.UserProps.UserID = "u-skew"
	pausedAssistant := llm.ChatMessage{Role: "assistant", Content: "mixed batch", ToolCalls: []llm.ToolCall{
		{ID: hostNotifyCallIDPrefix + "art1", Name: "_notify_artifact_save", Arguments: "{}"},
		{ID: "call-notify", Name: "_notify_artifact_save", Arguments: "{}"},
		{ID: "call-sync-a", Name: "fast_tool", Arguments: "{}"},
		{ID: "call-sync-b", Name: "fast_tool", Arguments: "{}"},
		{ID: "call-pending", Name: "peer_slow", Arguments: "{}"},
	}}
	hostNotifyResult := llm.ChatMessage{
		Role:       "tool",
		ToolCallID: hostNotifyCallIDPrefix + "art1",
		ToolName:   "_notify_artifact_save",
		Content:    `{"status":"success"}`,
	}
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "do mixed work"},
		pausedAssistant,
		hostNotifyResult,
	}
	state.PausedAssistantMsg = pausedAssistant
	state.HistoryOffset = 0
	state.SyntheticToolErrors = []llm.ChatMessage{{
		Role:       "tool",
		ToolCallID: "call-notify",
		ToolName:   "_notify_artifact_save",
		Content:    "filtered",
	}}
	state.SyncResults = map[int]*tool.Result{
		0: {Status: tool.StatusSuccess, Message: "fast done", Data: map[string]any{"value": 42.0}},
		1: {Status: tool.StatusSuccess, Message: "fast done", Data: map[string]any{"value": 7.0}},
	}
	state.PendingCalls = map[string]*PendingToolCall{
		"call-pending": {ToolCallID: "call-pending", ToolName: "peer_slow", Kind: PendingCallPeer, Index: 2},
	}
	state.TotalPending = 1
	state.PausedAt = time.Now()
	agent.pausedTasks.Store(state)

	agent.cancelPausedTask(state, cancelFromElsewhere)

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u-skew", "s-skew")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if len(saved.Messages) != 8 {
		t.Fatalf("expected 8 persisted messages, got %d: %+v", len(saved.Messages), saved.Messages)
	}

	byID := make(map[string]string)
	for _, m := range saved.Messages {
		if m.Role != "tool" {
			continue
		}
		if _, dup := byID[m.ToolCallID]; dup {
			t.Errorf("duplicate tool result for %q", m.ToolCallID)
		}
		byID[m.ToolCallID] = m.Content
	}
	for _, tc := range pausedAssistant.ToolCalls {
		if _, ok := byID[tc.ID]; !ok {
			t.Errorf("orphan tool_use %q (%s): no persisted tool result", tc.ID, tc.Name)
		}
	}
	if byID[hostNotifyCallIDPrefix+"art1"] != `{"status":"success"}` {
		t.Errorf("host-notify result should remain the pre-pause injection, got %q", byID[hostNotifyCallIDPrefix+"art1"])
	}
	if byID["call-notify"] != cancelledToolResultContent {
		t.Errorf("filtered call should get a cancelled stub, got %q", byID["call-notify"])
	}
	if !strings.Contains(byID["call-sync-a"], `"status":"success"`) || !strings.Contains(byID["call-sync-a"], `"value":42`) {
		t.Errorf("first sync result must pair with call-sync-a (dispatch-index skew), got %q", byID["call-sync-a"])
	}
	if !strings.Contains(byID["call-sync-b"], `"status":"success"`) || !strings.Contains(byID["call-sync-b"], `"value":7`) {
		t.Errorf("second sync result must pair with call-sync-b (dispatch-index skew), got %q", byID["call-sync-b"])
	}
	if byID["call-pending"] != cancelledToolResultContent {
		t.Errorf("pending call should get a cancelled stub, got %q", byID["call-pending"])
	}
}
