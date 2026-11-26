// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestANewPauseClearsTheReplayedFlag(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "slowtool", func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("done"), nil
	})

	state := newTestTaskState(agent)
	state.pauseReplayed.Store(true)
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-old", Name: "peer_agent"}}},
		{Role: "tool", ToolCallID: "tc-old", Content: "the first peer answered"},
	}
	state.PausedAssistantMsg = state.Messages[1]

	calls := []llm.ToolCall{{ID: "tc-new", Name: "peer_agent", Arguments: `{}`}}
	state.Messages = append(state.Messages, llm.ChatMessage{Role: "assistant", ToolCalls: calls})
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-new": {ToolCallID: "tc-new", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-new"},
	}
	state.TotalPending = 1
	recordPausedBatch(state)

	if state.pauseReplayed.Load() {
		t.Error("a fresh pause left the replayed flag set — the new batch is never paired, so the next request carries an assistant tool_call with no tool_result and the provider rejects it outright")
	}
	if state.PausedAssistantMsg.ToolCalls[0].ID != "tc-new" {
		t.Fatalf("the pause recorded the wrong assistant message: %+v", state.PausedAssistantMsg.ToolCalls)
	}
}

func TestAdoptingAReplayedPauseDoesNotReAppendSyntheticErrors(t *testing.T) {
	store := session.NewMemoryStore()
	agent, mock, _ := replicaOf(t, store, "adopter")

	ctx := context.Background()
	taskID, sessionID := idgen.New(), idgen.New()

	state := fullTaskState()
	state.TaskID = taskID
	state.EffectiveSessionID = sessionID
	state.ContextID = sessionID
	state.OriginalSessionID = sessionID
	state.IsRunBased = false
	state.StructuredCtx = nil
	state.LoopIteration = 1
	state.MaxCalls = 5
	state.ReplyTo = ""
	state.UserProps.ReplyTo = ""
	synthetic := llm.ChatMessage{Role: "tool", ToolCallID: "tc-bad", Content: "unknown tool"}
	state.SyntheticToolErrors = []llm.ChatMessage{synthetic}
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "tc-1", Name: "peer_agent"},
			{ID: "tc-bad", Name: "nope"},
		}},
		{Role: "tool", ToolCallID: "tc-1", Content: "the peer answered"},
		synthetic,
	}
	state.PausedAssistantMsg = state.Messages[1]
	state.PendingCalls = map[string]*PendingToolCall{}
	state.SyncResults = map[int]*tool.Result{0: tool.OK("the peer answered")}
	state.TotalPending = 0
	state.pauseReplayed.Store(true)

	if err := store.Create(ctx, &session.Session{
		AppName: "test-agent", UserID: state.UserProps.UserID,
		SessionID: sessionID, ContextID: sessionID,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	blob, marshalErr := marshalTaskState(state)
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	lapsed := time.Now().Add(-time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", AgentName: "test-agent",
		UserID: state.UserProps.UserID, SessionID: sessionID,
		PendingCount: 0, State: blob,
		OwnerID: "the-replica-that-died", LeaseExpiresAt: &lapsed,
		TraceID: state.UserProps.TraceID,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	ownerDies(t, store, taskID)

	agent.resumeAbandonedCheckpoints(ctx, time.Now())
	waitForCheckpointGone(t, store, taskID)

	calls := mock.getCalls()
	if len(calls) == 0 {
		t.Fatal("the adopted task never called the LLM, so the resume did not run")
	}
	counted := map[string]int{}
	for _, m := range calls[0].Messages {
		if m.ToolCallID != "" {
			counted[m.ToolCallID]++
		}
	}
	for _, id := range []string{"tc-1", "tc-bad"} {
		if counted[id] != 1 {
			t.Errorf("the resumed turn sent %d tool results for tool_use %s, want exactly 1 — two is a duplicate the provider rejects, zero is an unpaired tool_call it rejects just as hard", counted[id], id)
		}
	}
}

func TestCancellingAReplayedPauseSynthesizesNothing(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("done"), nil
	})

	state := newTestTaskState(agent)
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "peer_agent"}}},
		{Role: "tool", ToolCallID: "tc-1", Content: "the peer answered"},
	}
	state.PausedAssistantMsg = state.Messages[1]
	state.SyncResults = map[int]*tool.Result{0: tool.OK("the peer answered")}
	state.PendingCalls = map[string]*PendingToolCall{}
	state.pauseReplayed.Store(true)
	before := len(state.Messages)

	agent.synthesizePendingCancelledResults(state)

	if len(state.Messages) != before {
		t.Errorf("cancelling a task whose batch was already paired appended %d more messages — every tool_use in the transcript now answers twice, and repairDanglingToolCalls hides the break rather than the duplication",
			len(state.Messages)-before)
	}

	state.pauseReplayed.Store(false)
	agent.synthesizePendingCancelledResults(state)
	if len(state.Messages) == before {
		t.Error("cancelling an unpaired batch synthesized nothing, leaving a dangling tool_use for the next turn")
	}
}

func waitForCheckpointGone(t *testing.T, store session.Store, taskID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cp, _ := store.GetCheckpoint(context.Background(), taskID); cp == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
