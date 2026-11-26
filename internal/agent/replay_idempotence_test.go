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

func TestAdoptingAReplayedPauseDoesNotRePairItsToolCalls(t *testing.T) {
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
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "delegate this"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "peer_agent"}}},
		{Role: "tool", ToolCallID: "tc-1", Content: "the peer answered"},
	}
	state.PausedAssistantMsg = state.Messages[1]
	state.SyntheticToolErrors = nil
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

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cp, _ := store.GetCheckpoint(ctx, taskID); cp == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	calls := mock.getCalls()
	if len(calls) == 0 {
		t.Fatal("the adopted task never called the LLM, so the resume did not run")
	}
	sent := 0
	for _, m := range calls[0].Messages {
		if m.ToolCallID == "tc-1" {
			sent++
		}
	}
	if sent != 1 {
		t.Errorf("the resumed turn sent %d tool results for tool_use tc-1, want exactly 1 — two means the adopter re-paired a batch that was already replayed, zero means it paired nothing at all, and Anthropic and Vertex reject either outright", sent)
	}

	sess, err := store.Get(ctx, "test-agent", state.UserProps.UserID, sessionID)
	if err != nil || sess == nil {
		t.Fatalf("read back the session: %v, %v", sess, err)
	}
	persisted := 0
	for _, m := range sess.Messages {
		if m.Role == "tool" {
			persisted++
		}
	}
	if persisted > 1 {
		t.Errorf("session history holds %d tool results for one tool_use — the user's transcript shows the peer answering twice", persisted)
	}

	if state.SyntheticToolErrors != nil {
		t.Fatal("fixture drift: this test documents itself as not covering the synthetic-error branch")
	}
}
