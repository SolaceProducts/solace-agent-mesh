// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const concurrentCtxID = "ctx-sibling"

var concurrentUser = a2a.UserProperties{UserID: "u1", ClientID: "c1"}

func singleToolCallResponse(callID, name string) *llm.ChatResponse {
	return &llm.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls:    []llm.ToolCall{{ID: callID, Name: name, Arguments: `{}`}},
	}
}

func newConcurrentSessionAgent(t *testing.T, toolName string, execFn stubExecFn, responses ...*llm.ChatResponse) (*Agent, *mockLLM) {
	t.Helper()
	agent, mock, _ := newTestAgent(responses...)
	agent.svc.(*mockServices).sessions = newSQLiteSessionStore(t)
	if err := agent.Tools().Register(&testBuiltinTool{name: toolName, description: "stub", execFn: execFn}); err != nil {
		t.Fatalf("register %s: %v", toolName, err)
	}
	return agent, mock
}

func assertNoSyntheticRepairs(t *testing.T, label string, req llm.ChatRequest) {
	t.Helper()
	for i, m := range req.Messages {
		if m.Content == danglingToolResultContent {
			t.Errorf("%s: message %d is the dangling-tool-call repair for %q; a sibling's in-flight call was treated as crashed", label, i, m.ToolCallID)
		}
		if m.Content == cancellationMarkerContent {
			t.Errorf("%s: message %d is the cancellation marker; a sibling's request was treated as cancelled", label, i)
		}
	}
}

func requestContains(req llm.ChatRequest, text string) bool {
	for _, m := range req.Messages {
		if strings.Contains(m.Content, text) {
			return true
		}
	}
	return false
}

func waitOrFail(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestConcurrentSiblingRunningToolIsNotRepairedAsCrashed(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	slowTool := func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		close(started)
		<-release
		return tool.OK("slow-result-W01"), nil
	}
	agent, mock := newConcurrentSessionAgent(t, "slow_tool", slowTool,
		singleToolCallResponse("call-a1", "slow_tool"),
		&llm.ChatResponse{Content: "pong"},
		&llm.ChatResponse{Content: "W01 done"},
		&llm.ChatResponse{Content: "status reply"},
	)

	taskADone := make(chan struct{})
	go func() {
		defer close(taskADone)
		if _, err := agent.HandleTask(context.Background(), "task-a", concUserMessage("work W01", concurrentCtxID), concurrentUser); err != nil {
			t.Errorf("task A: %v", err)
		}
	}()
	waitOrFail(t, started, "task A's tool to start")

	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}
	close(release)
	waitOrFail(t, taskADone, "task A to finish")

	if _, err := agent.HandleTask(t.Context(), "task-c", concUserMessage("status of W01?", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task C: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 4 {
		t.Fatalf("LLM calls = %d, want 4 (A, B, A after tool, C)", len(calls))
	}
	reqB, reqC := calls[1], calls[3]

	assertNoSyntheticRepairs(t, "task B while A runs", reqB)
	if !requestContains(reqB, "work W01") {
		t.Error("task B's request lost task A's request entirely")
	}
	if !requestContains(reqB, siblingInProgressContent) {
		t.Error("task B's request does not say task A is still in progress")
	}

	assertNoSyntheticRepairs(t, "task C after A finished", reqC)
	assertRealResultFollowsCall(t, reqC, "call-a1")
}

func TestConcurrentSiblingPausedCallIsNotRepairedAsCrashed(t *testing.T) {
	agent, mock := newConcurrentSessionAgent(t, "test_hil_tool", stubReturnsPendingHIL("call-h1", "hil-req-1"),
		singleToolCallResponse("call-h1", "test_hil_tool"),
		&llm.ChatResponse{Content: "pong"},
	)

	resp, err := agent.HandleTask(t.Context(), "task-a", concUserMessage("approve W02", concurrentCtxID), concurrentUser)
	if err != nil {
		t.Fatalf("task A: %v", err)
	}
	if resp != nil {
		t.Fatalf("task A should pause on the HIL call, got a terminal response")
	}
	pausedA := agent.pausedTasks.Load("task-a")
	if pausedA == nil {
		t.Fatal("task A is not paused")
	}
	t.Cleanup(func() { agent.cancelPausedTask(pausedA, cancelFromElsewhere) })

	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("LLM calls = %d, want 2", len(calls))
	}
	assertNoSyntheticRepairs(t, "task B while A is paused", calls[1])
	if !requestContains(calls[1], "approve W02") {
		t.Error("task B's request lost task A's request entirely")
	}
	if !requestContains(calls[1], siblingInProgressContent) {
		t.Error("task B's request does not say task A is still in progress")
	}
}

func assertRealResultFollowsCall(t *testing.T, req llm.ChatRequest, callID string) {
	t.Helper()
	for i, m := range req.Messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 || m.ToolCalls[0].ID != callID {
			continue
		}
		if i+1 >= len(req.Messages) {
			t.Fatalf("call %s is the last message; its result is missing", callID)
		}
		next := req.Messages[i+1]
		if next.Role != "tool" || next.ToolCallID != callID || isSyntheticToolResult(next) {
			t.Fatalf("message after call %s = {role:%s id:%s content:%.80q}, want its stored result",
				callID, next.Role, next.ToolCallID, next.Content)
		}
		return
	}
	t.Fatalf("call %s not found in the request", callID)
}

func runningTaskIDs(t *testing.T, agent *Agent) []string {
	t.Helper()
	ids, err := agent.svc.Sessions().ListRunningTasks(context.Background(), agent.cfg.Name, concurrentUser.UserID, concurrentCtxID, time.Now())
	if err != nil {
		t.Fatalf("list running tasks: %v", err)
	}
	return ids
}

func TestRunningClaimFollowsTheTaskLifecycle(t *testing.T) {
	agent, _ := newConcurrentSessionAgent(t, "test_hil_tool", stubReturnsPendingHIL("call-h1", "hil-req-1"),
		singleToolCallResponse("call-h1", "test_hil_tool"),
		&llm.ChatResponse{Content: "done"},
	)

	if _, err := agent.HandleTask(t.Context(), "task-a", concUserMessage("approve W02", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task A: %v", err)
	}
	if got := runningTaskIDs(t, agent); len(got) != 1 || got[0] != "task-a" {
		t.Fatalf("running while paused = %v, want [task-a]", got)
	}

	agent.cancelPausedTask(agent.pausedTasks.Load("task-a"), cancelFromElsewhere)
	if got := runningTaskIDs(t, agent); len(got) != 0 {
		t.Errorf("running after cancel = %v, want none", got)
	}

	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("hello", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}
	if got := runningTaskIDs(t, agent); len(got) != 0 {
		t.Errorf("running after task B finished = %v, want none", got)
	}
}

func TestConcurrentSiblingWithLapsedClaimIsRepairedAsCrashed(t *testing.T) {
	agent, mock := newConcurrentSessionAgent(t, "unused", alwaysOKTool(), &llm.ChatResponse{Content: "pong"})
	store := agent.svc.Sessions()
	ctx := context.Background()
	if _, err := agent.getOrCreateSession(ctx, concurrentUser.UserID, concurrentCtxID, ""); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.AppendMessages(ctx, agent.cfg.Name, concurrentUser.UserID, concurrentCtxID, []session.Message{
		{Role: "user", Content: "work W03", TaskID: "dead-task"},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	expired := time.Now().Add(-runningTaskLease - checkpointClockSkewAllowance - time.Second)
	if err := store.RegisterRunningTask(ctx, session.RunningTask{
		TaskID: "dead-task", AppName: agent.cfg.Name, UserID: concurrentUser.UserID, SessionID: concurrentCtxID,
	}, "dead-owner", expired); err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := agent.HandleTask(t.Context(), "task-b", concUserMessage("reply with pong", concurrentCtxID), concurrentUser); err != nil {
		t.Fatalf("task B: %v", err)
	}
	req := mock.getCalls()[0]
	if !requestContains(req, cancellationMarkerContent) {
		t.Error("a crashed sibling's request was not marked interrupted")
	}
	if requestContains(req, siblingInProgressContent) {
		t.Error("a crashed sibling was shown as still in progress")
	}
}
