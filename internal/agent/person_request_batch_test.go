// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	siblingRemoteTool = "other_report"
	siblingLocalTool  = "sibling_ok"
	siblingDoneText   = "sibling done"
	personRequestCall = "call-r1"
	siblingCall       = "call-r2"
)

type batchCall struct {
	id    string
	tool  string
	reply func(corrID string) []byte
}

func pausedOnABatch(t *testing.T, store session.Store, calls []batchCall, configure ...func(*Agent)) (*Agent, *mockLLM) {
	t.Helper()
	agent, mock, _ := replicaOf(t, store, "owner", configure...)
	strBrk := strBroker(t)
	exe := registerRemoteTool(t, agent, strBrk)
	registerRemoteToolOn(t, agent, exe, siblingRemoteTool)
	registerSiblingLocalTool(t, agent)

	var toolCalls []llm.ToolCall
	for _, c := range calls {
		toolCalls = append(toolCalls, llm.ToolCall{ID: c.id, Name: c.tool, Arguments: `{}`})
	}
	mock.responses = []*llm.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: toolCalls},
		{Content: "Approved and done."},
	}
	type held struct {
		ids     <-chan string
		release func()
	}
	var holds []held
	for _, c := range calls {
		if c.reply != nil {
			ids, release := heldSTRForTool(t, strBrk, c.tool, c.reply)
			holds = append(holds, held{ids, release})
		}
	}

	user := concurrentUser
	user.ReplyTo = resumedReplyTopic
	if resp, err := agent.HandleTask(t.Context(), personWaitTaskID, concUserMessage("build the reports", "ctx-batch"), user); err != nil || resp != nil {
		t.Fatalf("the task should pause on the batch: resp=%v err=%v", resp, err)
	}
	for _, h := range holds {
		waitForCorrID(t, h.ids)
		h.release()
	}
	return agent, mock
}

func registerSiblingLocalTool(t *testing.T, agent *Agent) {
	t.Helper()
	err := agent.Tools().Register(&testBuiltinTool{
		name:        siblingLocalTool,
		description: "completes at once",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			return tool.OK(siblingDoneText), nil
		},
	})
	if err != nil {
		t.Fatalf("register %s: %v", siblingLocalTool, err)
	}
}

func labelledToolTexts(req llm.ChatRequest) []string {
	var labels []string
	for _, text := range toolTextsOf(req) {
		switch {
		case strings.Contains(text, "approved"):
			labels = append(labels, "the person's answer")
		case strings.Contains(text, siblingDoneText):
			labels = append(labels, "the sibling's result")
		case text == idInUseResult:
			labels = append(labels, "a refusal: the request id is in use")
		default:
			labels = append(labels, text)
		}
	}
	return labels
}

func pendingCallOf(agent *Agent, callID string) *PendingToolCall {
	state := agent.pausedTasks.Load(personWaitTaskID)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if pc := state.PendingCalls[callID]; pc != nil {
		snapshot := *pc
		return &snapshot
	}
	return nil
}

func TestAPersonsWaitInABatchKeepsTheSiblingResultsAndResumesOnTheAnswerAlone(t *testing.T) {
	type observed struct {
		WaitKind          PendingCallKind
		TotalPending      int32
		CheckpointPending int
		LLMCallsAtWait    int
		ToolResults       []string
		LLMCallsAtEnd     int
	}
	plainReply := func(corrID string) []byte { return remoteToolReplyPayload(t, corrID, siblingDoneText) }
	personFirst := []string{"the person's answer", "the sibling's result"}
	personLast := []string{"the sibling's result", "the person's answer"}
	tests := []struct {
		name  string
		batch []batchCall
		want  []string
	}{
		{
			name: "a sibling tool that completed at dispatch",
			batch: []batchCall{
				{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonSTRReply(0)},
				{id: siblingCall, tool: siblingLocalTool},
			},
			want: personFirst,
		},
		{
			name: "a sibling remote call that was answered with a plain result",
			batch: []batchCall{
				{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonSTRReply(0)},
				{id: siblingCall, tool: siblingRemoteTool, reply: plainReply},
			},
			want: personFirst,
		},
		{
			name: "a person's request that is the second call of the batch",
			batch: []batchCall{
				{id: siblingCall, tool: siblingRemoteTool, reply: plainReply},
				{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonSTRReply(0)},
			},
			want: personLast,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			agent, mock := pausedOnABatch(t, store, tt.batch)
			if !waitUntil(func() bool { return waitingOnAPerson(agent) }) {
				t.Fatalf("the task never waited on the person: %+v", pendingCallOf(agent, personRequestCall))
			}
			cp, err := store.GetCheckpoint(t.Context(), personWaitTaskID)
			if err != nil || cp == nil {
				t.Fatalf("checkpoint: %v", err)
			}
			state := agent.pausedTasks.Load(personWaitTaskID)
			got := observed{
				WaitKind:          pendingCallOf(agent, personRequestCall).Kind,
				TotalPending:      atomic.LoadInt32(&state.TotalPending),
				CheckpointPending: cp.PendingCount,
				LLMCallsAtWait:    len(mock.getCalls()),
			}

			if outcome := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); outcome != hilDelivered {
				t.Fatalf("delivery = %v, want delivered", outcome)
			}
			if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
				t.Fatalf("the answer did not resume the task: %d LLM calls", len(mock.getCalls()))
			}
			got.LLMCallsAtEnd = len(mock.getCalls())
			got.ToolResults = labelledToolTexts(mock.getCalls()[1])

			want := observed{
				WaitKind:          PendingCallHILApproval,
				TotalPending:      1,
				CheckpointPending: 1,
				LLMCallsAtWait:    1,
				ToolResults:       tt.want,
				LLMCallsAtEnd:     2,
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("a batch with a person's wait (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTwoCallsOfOneBatchAskingTheSamePersonForTheSameRequestWaitOnceAndRefuseTheSecond(t *testing.T) {
	agent, mock := pausedOnABatch(t, session.NewMemoryStore(), []batchCall{
		{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonSTRReply(0)},
		{id: siblingCall, tool: siblingRemoteTool, reply: askingForAPersonSTRReply(0)},
	})
	if !waitUntil(func() bool { return waitingOnAPerson(agent) }) {
		t.Fatal("the task never waited on the person")
	}
	if second := pendingCallOf(agent, siblingCall); second != nil {
		t.Errorf("the second call asking for the same request = %+v, want it refused and not waiting", second)
	}

	if outcome := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); outcome != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", outcome)
	}
	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("the answer did not resume the task: %d LLM calls", len(mock.getCalls()))
	}

	want := []string{"the person's answer", "a refusal: the request id is in use"}
	if diff := cmp.Diff(want, labelledToolTexts(mock.getCalls()[1])); diff != "" {
		t.Errorf("tool results of the resumed turn (-want +got):\n%s", diff)
	}
}

func TestAnAnswerHeldForAnIdTwoCallsOfOneBatchAskedForStillReachesTheCallThatWaits(t *testing.T) {
	store := newGatedStore(func(_ *gatedStore, _, corrID string) bool { return corrID != "" })
	agent, mock := pausedOnABatch(t, store, []batchCall{
		{id: personRequestCall, tool: remoteReportTool, reply: askingForAPersonSTRReply(0)},
		{id: siblingCall, tool: siblingRemoteTool, reply: askingForAPersonSTRReply(0)},
	})
	waitOrFail(t, store.reached, "the first delivery to reach the record of its result")
	if outcome := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); outcome != hilDelivered {
		t.Fatalf("delivery before the task paused on the request = %v, want the answer held", outcome)
	}

	close(store.proceed)

	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("the held answer never reached the task: %d LLM calls", len(mock.getCalls()))
	}
	want := []string{"the person's answer", "a refusal: the request id is in use"}
	if diff := cmp.Diff(want, labelledToolTexts(mock.getCalls()[1])); diff != "" {
		t.Errorf("tool results of the resumed turn (-want +got):\n%s", diff)
	}
}
