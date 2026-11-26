// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const slowSiblingTool = "slow_sibling"

type slowSibling struct {
	started chan struct{}
	release func()
}

func registerSlowSibling(t *testing.T, replica *Agent) slowSibling {
	t.Helper()
	started := make(chan struct{})
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	err := replica.Tools().Register(&testBuiltinTool{name: slowSiblingTool, description: "stub", execFn: func(ctx context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		close(started)
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return tool.OK("sibling done"), nil
	}})
	if err != nil {
		t.Fatalf("register %s: %v", slowSiblingTool, err)
	}
	return slowSibling{started: started, release: release}
}

func runTaskInBackground(t *testing.T, replica *Agent, taskID string) <-chan string {
	t.Helper()
	outcome := make(chan string, 1)
	go func() {
		resp, err := replica.HandleTask(t.Context(), taskID, concUserMessage("do both", "ctx-"+taskID), approvalUser(taskID))
		switch {
		case err != nil:
			outcome <- "error: " + err.Error()
		case resp != nil:
			outcome <- "answered"
		default:
			outcome <- "paused"
		}
	}()
	return outcome
}

var approvalRequestIDPattern = regexp.MustCompile(`hil-approval-[A-Za-z0-9_-]+`)

func publishedApprovalRequestID(brk *mockBroker) string {
	for _, m := range brk.getPublished() {
		if id := approvalRequestIDPattern.Find(m.payload); id != nil {
			return string(id)
		}
	}
	return ""
}

func waitForSiblingToRun(t *testing.T, sibling slowSibling) {
	t.Helper()
	select {
	case <-sibling.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow sibling tool never started")
	}
}

func finishPause(t *testing.T, outcome <-chan string) {
	t.Helper()
	select {
	case got := <-outcome:
		if got != "paused" {
			t.Errorf("HandleTask outcome = %q, want paused", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the task did not pause after its sibling tool finished")
	}
}

func TestQuestionAnsweredWhileASiblingToolRunsResumesWithThatAnswer(t *testing.T) {
	holder, mock, brk := replicaOf(t, session.NewMemoryStore(), "holder")
	var sawPublisher bool
	if err := holder.Tools().Register(&testBuiltinTool{name: "ask_like", description: "stub", execFn: askLikeTool(t, &sawPublisher)}); err != nil {
		t.Fatalf("register ask_like: %v", err)
	}
	sibling := registerSlowSibling(t, holder)
	mock.responses = []*llm.ChatResponse{{
		FinishReason: "tool_calls",
		ToolCalls:    []llm.ToolCall{{ID: "call-ask", Name: "ask_like", Arguments: `{}`}, {ID: "call-slow", Name: slowSiblingTool, Arguments: `{}`}},
	}, {Content: "Thanks, EMEA it is."}}

	outcome := runTaskInBackground(t, holder, "task-early")
	waitForSiblingToRun(t, sibling)
	if !waitUntil(func() bool { return publishedUserInputRequest(brk, askRequestID) }) {
		t.Fatal("the question was never published")
	}

	if got := holder.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Errorf("routeUserInputResponse() = %v, want delivered", got)
	}
	sibling.release()
	finishPause(t, outcome)

	assertResumedToCompletion(t, holder, mock, brk, "task-early", "EMEA")
}

func TestApprovalGivenWhileASiblingToolRunsResumesAndRunsTheCallOnce(t *testing.T) {
	holder, mock, brk, runs := approvalReplica(t, session.NewMemoryStore(), "holder", time.Hour)
	sibling := registerSlowSibling(t, holder)
	mock.responses = []*llm.ChatResponse{{
		FinishReason: "tool_calls",
		ToolCalls:    []llm.ToolCall{{ID: "call-send", Name: approvalTool, Arguments: approvalToolArgs}, {ID: "call-slow", Name: slowSiblingTool, Arguments: `{}`}},
	}, {Content: "All done."}}

	outcome := runTaskInBackground(t, holder, "task-early-approval")
	waitForSiblingToRun(t, sibling)
	var requestID string
	if !waitUntil(func() bool { requestID = publishedApprovalRequestID(brk); return requestID != "" }) {
		t.Fatal("the approval request was never published")
	}

	if got := holder.routeUserInputResponse(t.Context(), approve(requestID, true), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Errorf("routeUserInputResponse() = %v, want delivered", got)
	}
	sibling.release()
	finishPause(t, outcome)

	assertResumedToCompletion(t, holder, mock, brk, "task-early-approval", approvalToolOut)
	if runs.Load() != 1 {
		t.Errorf("the approved call ran %d times, want 1", runs.Load())
	}
}

func TestAnotherUsersAnswerWhileASiblingToolRunsIsRefusedAndNotApplied(t *testing.T) {
	holder, mock, brk := replicaOf(t, session.NewMemoryStore(), "holder")
	var sawPublisher bool
	if err := holder.Tools().Register(&testBuiltinTool{name: "ask_like", description: "stub", execFn: askLikeTool(t, &sawPublisher)}); err != nil {
		t.Fatalf("register ask_like: %v", err)
	}
	sibling := registerSlowSibling(t, holder)
	mock.responses = []*llm.ChatResponse{{
		FinishReason: "tool_calls",
		ToolCalls:    []llm.ToolCall{{ID: "call-ask", Name: "ask_like", Arguments: `{}`}, {ID: "call-slow", Name: slowSiblingTool, Arguments: `{}`}},
	}, {Content: "Thanks."}}

	outcome := runTaskInBackground(t, holder, "task-early-refused")
	waitForSiblingToRun(t, sibling)
	if !waitUntil(func() bool { return publishedUserInputRequest(brk, askRequestID) }) {
		t.Fatal("the question was never published")
	}

	if got := holder.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser("mallory")); got != hilRefused {
		t.Errorf("routeUserInputResponse() = %v, want refused", got)
	}
	sibling.release()
	finishPause(t, outcome)

	state := holder.pausedTasks.Load("task-early-refused")
	if state == nil {
		t.Fatal("the task is not paused")
	}
	if remaining := atomic.LoadInt32(&state.TotalPending); remaining != 1 {
		t.Errorf("TotalPending = %d, want 1: a refused answer must leave the question open", remaining)
	}
}

func TestAnswerForAQuestionThatEndedWithoutPausingMatchesNothing(t *testing.T) {
	holder, mock, brk := replicaOf(t, session.NewMemoryStore(), "holder")
	err := holder.Tools().Register(&testBuiltinTool{name: "ask_then_fail", description: "stub", execFn: func(ctx context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
		toolCtx.PublishUserInput(ctx, askRequestID, "ask_user_question", json.RawMessage(`{}`), time.Hour)
		return tool.Error("could not wait for the answer"), nil
	}})
	if err != nil {
		t.Fatalf("register ask_then_fail: %v", err)
	}
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-ask", "ask_then_fail"), {Content: "Never mind."}}

	resp, err := holder.HandleTask(t.Context(), "task-ended", concUserMessage("ask me", "ctx-ended"), approvalUser("task-ended"))
	if err != nil || resp == nil {
		t.Fatalf("HandleTask() = %v, %v, want the finished task's response", resp, err)
	}
	if !publishedUserInputRequest(brk, askRequestID) {
		t.Fatal("the question was never published")
	}

	if got := holder.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilUnmatched {
		t.Errorf("routeUserInputResponse() = %v, want unmatched", got)
	}
}
