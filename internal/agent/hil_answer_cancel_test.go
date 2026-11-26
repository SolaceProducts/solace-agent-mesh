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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	hilCancelTaskID    = "task-hil-cancel"
	hilCancelRequestID = "req-hil-cancel"
	hilCancelReplyTo   = "test-ns/a2a/v1/gateway/response/web-1/" + hilCancelTaskID
)

func startTaskOnTheWire(t *testing.T, agent *Agent, taskID string, props a2a.UserProperties, msg a2a.Message) <-chan struct{} {
	t.Helper()
	payload, err := json.Marshal(a2a.NewRequest(taskID, "message/send", map[string]any{"message": msg}))
	if err != nil {
		t.Fatalf("marshal task request: %v", err)
	}
	var req a2a.Request
	if err = json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("decode task request: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.processMessage(t.Context(), &broker.Message{
			Payload:        payload,
			UserProperties: a2a.EncodeUserProperties(&props),
		}, &req)
	}()
	t.Cleanup(func() { <-done })
	return done
}

func hasActiveTask(agent *Agent, taskID string) bool {
	return activeEntry(agent, taskID) != nil
}

func activeEntry(agent *Agent, taskID string) *activeTaskEntry {
	agent.activeTasksMu.Lock()
	defer agent.activeTasksMu.Unlock()
	return agent.activeTasks[taskID]
}

func controlEntryCount(agent *Agent, taskID string) int {
	agent.activeTasksMu.Lock()
	defer agent.activeTasksMu.Unlock()
	return len(agent.controlEntriesForLocked(taskID))
}

func hasPendingHIL(agent *Agent, requestID string) bool {
	agent.pendingHILMu.Lock()
	defer agent.pendingHILMu.Unlock()
	_, ok := agent.pendingHIL[requestID]
	return ok
}

func cancelledResponsesOn(brk *mockBroker, topic string) int {
	n := 0
	for _, p := range brk.getPublished() {
		if p.topic == topic && strings.Contains(string(p.payload), string(a2a.TaskStateCancelled)) {
			n++
		}
	}
	return n
}

func TestHILAnswerKeepsTheRunningTasksCancelHandle(t *testing.T) {
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "ask", Arguments: "{}"}}},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	answered := make(chan struct{})
	var answerStatus string
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "ask",
		description: "asks the user, then keeps working until cancelled",
		execFn: func(ic context.Context, _ map[string]any, tCtx *tool.Context) (*tool.Result, error) {
			got := tCtx.WaitForUserInput(ic, hilCancelRequestID, "ask_user_question", json.RawMessage(`{"surfaceId":"s"}`), time.Minute) //nolint:staticcheck // exercising the legacy in-loop HIL waiter
			if got != nil {
				answerStatus = got.Status
			}
			close(answered)
			<-ic.Done()
			return nil, ic.Err()
		},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	done := startTaskOnTheWire(t, agent, hilCancelTaskID, a2a.UserProperties{
		Timestamp: time.Now().UnixMilli(), ClientID: "web-1", UserID: hilAlice,
		ReplyTo:        hilCancelReplyTo,
		A2AStatusTopic: "test-ns/a2a/v1/gateway/status/web-1/" + hilCancelTaskID,
	}, a2a.Message{
		Kind: "message", MessageID: "m1", Role: a2a.RoleUser,
		Parts: []a2a.Part{a2a.NewTextPart("ask me")}, ContextID: "ctx-1",
	})
	if !waitUntil(func() bool { return hasPendingHIL(agent, hilCancelRequestID) }) {
		t.Fatal("the task never blocked on the user")
	}
	running := activeEntry(agent, hilCancelTaskID)

	answerOnTheWire(t, agent, hilCancelTaskID, hilAlice, userInputResponse(hilCancelRequestID, "answered", nil))
	waitOrFail(t, answered, "the answer to reach the waiting tool")
	if answerStatus != "answered" {
		t.Fatalf("the waiting tool got status %q, want answered", answerStatus)
	}

	if got := activeEntry(agent, hilCancelTaskID); got == nil || got != running {
		t.Fatal("the HIL answer replaced or removed the running task's activeTasks entry; tasks/cancel can no longer reach it")
	}

	agent.handleCancelRequest(&a2a.Request{
		ID: "cancel-1", Method: "tasks/cancel", Params: map[string]any{"id": hilCancelTaskID},
	})
	waitOrFail(t, done, "the cancelled task to stop")

	if got := cancelledResponsesOn(brk, hilCancelReplyTo); got != 1 {
		t.Errorf("cancelled responses on the task's reply topic = %d, want 1", got)
	}
	if hasActiveTask(agent, hilCancelTaskID) {
		t.Error("the cancelled task is still registered in activeTasks")
	}
}

const (
	authCancelTaskID   = "task-auth-cancel"
	authCancelReplyTo  = "test-ns/a2a/v1/gateway/response/web-1/" + authCancelTaskID
	authReplayTaskID   = "task-auth-replay"
	authReplayCallID   = "call-auth-replay"
	authReplayUserID   = "u1"
	authReplayCredKey  = "test-key"
	cancelStopDeadline = 5 * time.Second
	drainProbe         = 300 * time.Millisecond
)

func controlRequestOnTheWire(t *testing.T, taskID, userID string, response any) (*broker.Message, *a2a.Request) {
	t.Helper()
	msg := a2a.Message{Kind: "message", MessageID: "m-" + taskID, Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewDataPart(response)}, TaskID: taskID}
	payload, err := json.Marshal(a2a.NewRequest(taskID, "message/send", map[string]any{"message": msg}))
	if err != nil {
		t.Fatalf("marshal control message: %v", err)
	}
	var req a2a.Request
	if err = json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("decode control message: %v", err)
	}
	return &broker.Message{
		Payload: payload,
		UserProperties: a2a.EncodeUserProperties(&a2a.UserProperties{
			Timestamp: time.Now().UnixMilli(), ClientID: "web-1", UserID: userID,
		}),
	}, &req
}

func TestAuthResponseKeepsTheRunningTasksCancelHandle(t *testing.T) {
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "work", Arguments: "{}"}}},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	started := make(chan struct{})
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "work",
		description: "keeps working until cancelled",
		execFn: func(ic context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			close(started)
			<-ic.Done()
			return nil, ic.Err()
		},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	done := startTaskOnTheWire(t, agent, authCancelTaskID, a2a.UserProperties{
		Timestamp: time.Now().UnixMilli(), ClientID: "web-1", UserID: hilAlice,
		ReplyTo:        authCancelReplyTo,
		A2AStatusTopic: "test-ns/a2a/v1/gateway/status/web-1/" + authCancelTaskID,
	}, a2a.Message{
		Kind: "message", MessageID: "m1", Role: a2a.RoleUser,
		Parts: []a2a.Part{a2a.NewTextPart("do the work")}, ContextID: "ctx-1",
	})
	waitOrFail(t, started, "the tool to start")
	running := activeEntry(agent, authCancelTaskID)
	if running == nil {
		t.Fatal("the running task is not registered in activeTasks")
	}

	answerOnTheWire(t, agent, authCancelTaskID, hilAlice, authResp(authCancelTaskID, "call-elsewhere", authReplayCredKey))

	if got := activeEntry(agent, authCancelTaskID); got != running {
		t.Fatal("the auth_response replaced or removed the running task's activeTasks entry; tasks/cancel can no longer reach it")
	}

	agent.handleCancelRequest(&a2a.Request{
		ID: "cancel-auth", Method: "tasks/cancel", Params: map[string]any{"id": authCancelTaskID},
	})
	waitOrFail(t, done, "the cancelled task to stop")
	if got := cancelledResponsesOn(brk, authCancelReplyTo); got != 1 {
		t.Errorf("cancelled responses on the task's reply topic = %d, want 1", got)
	}
}

func replayUntilCancelled(started chan<- struct{}, stopped chan<- error) stubExecFn {
	return func(ic context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		started <- struct{}{}
		<-ic.Done()
		stopped <- ic.Err()
		return tool.Error("the replay was cancelled"), nil
	}
}

func handleControlInBackground(t *testing.T, agent *Agent, taskID, userID string, response any) <-chan struct{} {
	t.Helper()
	bmsg, req := controlRequestOnTheWire(t, taskID, userID, response)
	handled := make(chan struct{})
	go func() {
		defer close(handled)
		agent.processMessage(t.Context(), bmsg, req)
	}()
	t.Cleanup(func() { <-handled })
	return handled
}

func cancelOnTheWire(agent *Agent, taskID string) {
	agent.handleCancelRequest(&a2a.Request{
		ID: "cancel-" + taskID, Method: "tasks/cancel", Params: map[string]any{"id": taskID},
	})
}

func waitStopped(t *testing.T, stopped <-chan error, what string) {
	t.Helper()
	select {
	case err := <-stopped:
		if err == nil {
			t.Errorf("%s stopped without a cancelled context", what)
		}
	case <-time.After(cancelStopDeadline):
		t.Fatalf("tasks/cancel did not stop %s", what)
	}
}

func TestAuthResponseReplayForAPausedTaskCanBeCancelled(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	started := make(chan struct{}, 1)
	stopped := make(chan error, 1)
	agent := newTestAgentWithStubTool(t, "stubtool", replayUntilCancelled(started, stopped))
	pausedStateWithAuthEntry(t, agent, authReplayTaskID, authReplayCallID)
	registerStubAuthHandler(t, agent, authReplayCredKey)

	handled := handleControlInBackground(t, agent, authReplayTaskID, authReplayUserID, authResp(authReplayTaskID, authReplayCallID, authReplayCredKey))

	waitOrFail(t, started, "the paused tool to be replayed")
	if got := controlEntryCount(agent, authReplayTaskID); got != 1 {
		t.Fatalf("control entries for the task = %d, want 1; the shutdown drain cannot see the replay and tasks/cancel cannot reach it", got)
	}
	began := time.Now()
	drainCtx, cancelDrain := context.WithTimeout(t.Context(), drainProbe)
	agent.drainActiveTasks(drainCtx)
	cancelDrain()
	if waited := time.Since(began); waited < drainProbe {
		t.Errorf("shutdown drain returned after %v while the replay was in flight; want it to wait out its %v budget", waited, drainProbe)
	}

	cancelOnTheWire(agent, authReplayTaskID)
	waitStopped(t, stopped, "the replayed tool")
	waitOrFail(t, handled, "the auth_response handling to finish")
	if got := controlEntryCount(agent, authReplayTaskID); got != 0 {
		t.Errorf("control entries left after the answer was handled = %d, want 0", got)
	}
}

func TestTwoAuthResponsesForOnePausedTaskAreBothCancellable(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	started := make(chan struct{}, 2)
	stopped := make(chan error, 2)
	agent := newTestAgentWithStubTool(t, "stubtool", replayUntilCancelled(started, stopped))
	state := pausedStateWithAuthEntry(t, agent, authReplayTaskID, authReplayCallID)
	const secondCall = "call-auth-replay-2"
	state.mu.Lock()
	state.TotalPending = 2
	state.PendingCalls[secondCall] = &PendingToolCall{
		ToolCallID: secondCall, ToolName: "stubtool", Kind: PendingCallAuth, Index: 1,
		OriginalArgs: map[string]any{"q": "second"},
	}
	state.PausedAssistantMsg = llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{
		{ID: authReplayCallID, Name: "stubtool", Arguments: `{"q":"test"}`},
		{ID: secondCall, Name: "stubtool", Arguments: `{"q":"second"}`},
	}}
	state.mu.Unlock()
	registerStubAuthHandler(t, agent, authReplayCredKey)

	first := handleControlInBackground(t, agent, authReplayTaskID, authReplayUserID, authResp(authReplayTaskID, authReplayCallID, authReplayCredKey))
	waitOrFail(t, started, "the first paused tool to be replayed")
	second := handleControlInBackground(t, agent, authReplayTaskID, authReplayUserID, authResp(authReplayTaskID, secondCall, authReplayCredKey))
	if !waitUntil(func() bool { return controlEntryCount(agent, authReplayTaskID) == 2 }) {
		t.Fatalf("control entries for the task = %d, want 2; the second answer would run unreachable", controlEntryCount(agent, authReplayTaskID))
	}

	cancelOnTheWire(agent, authReplayTaskID)
	waitStopped(t, stopped, "the first replayed tool")
	waitOrFail(t, first, "the first auth_response to finish")
	waitOrFail(t, second, "the second auth_response to finish")
	if got := controlEntryCount(agent, authReplayTaskID); got != 0 {
		t.Errorf("control entries left after both answers were handled = %d, want 0", got)
	}
}

func replayPerTask(started chan<- string, stopped map[string]chan error) stubExecFn {
	return func(ic context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		key, _ := args["q"].(string)
		started <- key
		<-ic.Done()
		if ch, ok := stopped[key]; ok {
			ch <- ic.Err()
		}
		return tool.Error("the replay was cancelled"), nil
	}
}

func TestCancelStopsEveryAnswerForTheTaskAndNoOther(t *testing.T) {
	agent, _, _ := newTestAgent()
	const taskID, otherID = "task-two-answers", "task-other"
	var mine []context.Context
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		mine = append(mine, ctx)
		agent.trackControlEntry(taskID, &activeTaskEntry{cancel: stopTaskFunc(cancel), publishOnce: &sync.Once{}})
	}
	otherCtx, otherCancel := context.WithCancel(context.Background())
	t.Cleanup(otherCancel)
	agent.trackControlEntry(otherID, &activeTaskEntry{cancel: stopTaskFunc(otherCancel), publishOnce: &sync.Once{}})

	cancelOnTheWire(agent, taskID)

	for i, ctx := range mine {
		if ctx.Err() == nil {
			t.Errorf("answer %d for the cancelled task is still running", i+1)
		}
	}
	if otherCtx.Err() != nil {
		t.Error("cancelling one task stopped an answer being handled for another task")
	}
}

func TestCancellingOneTaskLeavesAnotherTasksAnswerRunning(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	tasks := []string{"A", "B"}
	started := make(chan string, len(tasks))
	stopped := map[string]chan error{}
	for _, k := range tasks {
		stopped[k] = make(chan error, 1)
	}
	agent := newTestAgentWithStubTool(t, "stubtool", replayPerTask(started, stopped))
	handled := map[string]<-chan struct{}{}
	for _, k := range tasks {
		taskID, callID := "task-"+k, "call-"+k
		state := pausedStateWithAuthEntry(t, agent, taskID, callID)
		state.mu.Lock()
		state.PendingCalls[callID].OriginalArgs = map[string]any{"q": k}
		state.PausedAssistantMsg = assistantMsgWithOneToolCall(callID, "stubtool", `{"q":"`+k+`"}`)
		state.mu.Unlock()
	}
	registerStubAuthHandler(t, agent, authReplayCredKey)
	for _, k := range tasks {
		handled[k] = handleControlInBackground(t, agent, "task-"+k, authReplayUserID, authResp("task-"+k, "call-"+k, authReplayCredKey))
	}
	for range tasks {
		select {
		case <-started:
		case <-time.After(cancelStopDeadline):
			t.Fatal("both paused tools were not replayed")
		}
	}

	cancelOnTheWire(agent, "task-A")
	waitStopped(t, stopped["A"], "task A's replay")
	select {
	case <-stopped["B"]:
		t.Fatal("cancelling task A stopped task B's replay")
	case <-time.After(drainProbe):
	}
	if got := controlEntryCount(agent, "task-B"); got != 1 {
		t.Fatalf("control entries for task B after cancelling task A = %d, want 1", got)
	}

	cancelOnTheWire(agent, "task-B")
	waitStopped(t, stopped["B"], "task B's replay")
	waitOrFail(t, handled["A"], "task A's answer to finish")
	waitOrFail(t, handled["B"], "task B's answer to finish")
	for _, k := range tasks {
		if got := controlEntryCount(agent, "task-"+k); got != 0 {
			t.Errorf("control entries left for task %s = %d, want 0", k, got)
		}
	}
}

func TestAuthResponseDuringThePauseUnwindIsCancellable(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	started := make(chan struct{}, 1)
	stopped := make(chan error, 1)
	agent := newTestAgentWithStubTool(t, "stubtool", replayUntilCancelled(started, stopped))
	pausedStateWithAuthEntry(t, agent, authReplayTaskID, authReplayCallID)
	registerStubAuthHandler(t, agent, authReplayCredKey)
	unwinding := &activeTaskEntry{cancel: func() {}, publishOnce: &sync.Once{}}
	agent.activeTasksMu.Lock()
	agent.activeTasks[authReplayTaskID] = unwinding
	agent.activeTasksMu.Unlock()

	handled := handleControlInBackground(t, agent, authReplayTaskID, authReplayUserID, authResp(authReplayTaskID, authReplayCallID, authReplayCredKey))

	waitOrFail(t, started, "the paused tool to be replayed")
	if got := controlEntryCount(agent, authReplayTaskID); got != 1 {
		t.Fatalf("control entries for the task = %d, want 1", got)
	}
	if activeEntry(agent, authReplayTaskID) != unwinding {
		t.Fatal("the auth_response replaced the unwinding task's activeTasks entry")
	}

	cancelOnTheWire(agent, authReplayTaskID)
	waitStopped(t, stopped, "the replayed tool")
	waitOrFail(t, handled, "the auth_response handling to finish")
}
