// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestStopForProcessExit_DoesNotWaitForATaskWaitingOnAPerson(t *testing.T) {
	model := newMockLLM(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "ask", Arguments: "{}"}}},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	startCtx, exitProcess := context.WithCancel(t.Context())
	defer exitProcess()
	agent, brk := startDrainAgentUnder(startCtx, t, model)
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "ask",
		description: "asks the user",
		execFn: func(ic context.Context, _ map[string]any, tCtx *tool.Context) (*tool.Result, error) {
			tCtx.WaitForUserInput(ic, "question-1", "ask_user_question", json.RawMessage(`{"surfaceId":"s"}`), time.Hour) //nolint:staticcheck // the in-loop wait is what ask_user uses
			return tool.OK("answered"), nil
		},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	const taskID, replyTo = "task-asking", "test-ns/reply/asking"
	acks := deliverTaskRequestWith(t, brk, taskID, a2a.UserProperties{
		ReplyTo: replyTo, A2AStatusTopic: "test-ns/a2a/v1/gateway/status/gw-1/" + taskID,
	})
	if !waitUntil(func() bool { return hasPendingHIL(agent, "question-1") }) {
		t.Fatal("the task never asked the user")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if err := agent.StopForProcessExit(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if waited := time.Since(started); waited > 2*time.Second {
		t.Errorf("stop waited %s for a task waiting on a person, want it to return at once", waited)
	}
	exitProcess()

	if !waitUntil(func() bool { return requestsInProcess(agent) == 0 }) {
		t.Fatal("the cut task did not end after the exit")
	}
	if acks.Load() != 0 {
		t.Errorf("request of the task cut by the exit acknowledged %d times, want 0", acks.Load())
	}
	if got := len(publishedOn(brk, replyTo)); got != 0 {
		t.Errorf("%d answers published for a task cut by the exit, want none", got)
	}
	if storedToolCall(agent, taskID, "c1") {
		t.Error("the turn cut by the exit was stored; the redelivered task would continue from the cut answer instead of asking again")
	}
}

func storedToolCall(agent *Agent, taskID, toolCallID string) bool {
	store := agent.svc.Sessions().(*memSessionStore)
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, sess := range store.sessions {
		for _, m := range sess.Messages {
			if m.TaskID == taskID && m.ToolCallID == toolCallID {
				return true
			}
		}
	}
	return false
}
