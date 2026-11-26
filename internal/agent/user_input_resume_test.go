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
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

const askRequestID = "req-region"

func pausedOnQuestion(t *testing.T, store session.Store, owner *Agent, taskID string) *TaskExecutionState {
	t.Helper()
	state := pausedOnPeerCall(t, store, taskID, "")
	state.Messages[1] = llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "ask_user_question"}}}
	state.PausedAssistantMsg = state.Messages[1]
	pc := state.PendingCalls["tc-1"]
	pc.ToolName = "ask_user_question"
	pc.Kind = PendingCallHILAsk
	pc.SubTaskID = ""
	pc.HILRequestID = askRequestID
	pc.Timeout = time.Hour
	pc.OriginalArgs = map[string]any{"questions": []any{map[string]any{"question": "Which region?"}}}
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)
	return state
}

func regionAnswer() *a2a.UserInputResponseData {
	return &a2a.UserInputResponseData{
		Type: "user_input_response", RequestID: askRequestID, Status: "answered",
		Data: map[string]any{"answers": map[string]any{"q0": "EMEA"}},
	}
}

func relayedAnswer(t *testing.T, brk *mockBroker) *broker.Message {
	t.Helper()
	topic := a2a.NotifyResponseTopic("test-ns", "test-agent", askRequestID)
	for _, m := range brk.getPublished() {
		if m.topic == topic {
			return &broker.Message{Topic: topic, Payload: m.payload}
		}
	}
	t.Fatalf("the answer was not republished on %s", topic)
	return nil
}

func TestAnswerOnAnotherReplicaReachesTheLiveHolder(t *testing.T) {
	store := session.NewMemoryStore()
	owner, ownerLLM, ownerBrk := replicaOf(t, store, "owner")
	other, otherLLM, otherBrk := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, owner, "task-ask")

	if got := other.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}
	owner.handleNotifyHILResponse(relayedAnswer(t, otherBrk))

	assertResumedToCompletion(t, owner, ownerLLM, ownerBrk, "task-ask", "EMEA")
	if len(otherLLM.getCalls()) != 0 {
		t.Errorf("the relaying replica ran %d LLM calls; the holder must run the turn", len(otherLLM.getCalls()))
	}
}

func TestHolderCollectsARecordedAnswerWhenTheWakeUpIsLost(t *testing.T) {
	store := session.NewMemoryStore()
	owner, ownerLLM, ownerBrk := replicaOf(t, store, "owner")
	other, _, _ := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, owner, "task-ask")

	if got := other.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}
	owner.collectRecordedAnswers(t.Context())

	assertResumedToCompletion(t, owner, ownerLLM, ownerBrk, "task-ask", "EMEA")
}

func TestAnswerAfterTheHolderDiedFinishesTheTaskElsewhere(t *testing.T) {
	forEachDurableStore(t, func(t *testing.T, store session.Store) {
		owner, _, _ := replicaOf(t, store, "owner")
		survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
		state := pausedOnQuestion(t, store, owner, "task-ask")
		owner.pausedTasks.Delete("task-ask")
		loseOwner(t, store, state, 1)

		if got := survivor.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
			t.Fatalf("delivery = %v, want delivered", got)
		}
		assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, "task-ask", "EMEA")
	})
}

func askLikeTool(t *testing.T, sawPublisher *bool) stubExecFn {
	return func(ctx context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
		*sawPublisher = toolCtx.PublishUserInput != nil
		if toolCtx.PublishUserInput == nil {
			return tool.Error("no way to publish the question"), nil
		}
		toolCtx.PublishUserInput(ctx, askRequestID, "ask_user_question", json.RawMessage(`{}`), time.Hour)
		return tool.Pending("Waiting for the user's answer", tool.WithData(map[string]any{
			"pending_kind":   hilsurface.PendingKindAsk,
			"hil_request_id": askRequestID,
			"hil_timeout_ns": float64(time.Hour),
			"questions":      []any{map[string]any{"question": "Which region?"}},
		}), tool.WithTrustedPending(tool.TrustedPendingHILAsk)), nil
	}
}

func TestTaskPausesOnAQuestionAndResumesOnTheAnswer(t *testing.T) {
	forEachDurableStore(t, func(t *testing.T, store session.Store) {
		agent, mock, _ := replicaOf(t, store, "owner")
		var sawPublisher bool
		if err := agent.Tools().Register(&testBuiltinTool{name: "ask_like", description: "stub", execFn: askLikeTool(t, &sawPublisher)}); err != nil {
			t.Fatalf("register ask_like: %v", err)
		}
		mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-ask", "ask_like"), {Content: "Thanks, EMEA it is."}}
		user := concurrentUser
		user.A2AStatusTopic = "test-ns/a2a/v1/gateway/status/gw/task-ask"

		if resp, err := agent.HandleTask(t.Context(), "task-ask", concUserMessage("make a report", idgen.New()), user); err != nil || resp != nil {
			t.Fatalf("the task should pause on the question: resp=%v err=%v", resp, err)
		}
		if !sawPublisher {
			t.Fatal("the tool was not given PublishUserInput although durable resume is on")
		}
		state := agent.pausedTasks.Load("task-ask")
		if state == nil {
			t.Fatal("the task is not paused")
		}
		pc := state.PendingCalls["call-ask"]
		if pc == nil || pc.Kind != PendingCallHILAsk || pc.HILRequestID != askRequestID || pc.OriginalArgs["questions"] == nil {
			t.Fatalf("pending call = %+v, want a hil_ask call carrying its questions", pc)
		}

		if got := agent.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(user.UserID)); got != hilDelivered {
			t.Fatalf("delivery = %v, want delivered", got)
		}
		if !waitUntil(func() bool { return len(mock.getCalls()) == 2 && agent.pausedTasks.Load("task-ask") == nil }) {
			t.Fatalf("the task did not resume: %d LLM calls", len(mock.getCalls()))
		}
		if !requestContains(mock.getCalls()[1], "EMEA") {
			t.Error("the resumed turn did not see the user's answer")
		}
	})
}

func blockingAskTool(sawPublisher *bool) stubExecFn {
	return func(ctx context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
		*sawPublisher = toolCtx.PublishUserInput != nil
		resp := toolCtx.WaitForUserInput(ctx, askRequestID, "ask_user_question", json.RawMessage(`{}`), time.Minute) //nolint:staticcheck // the blocking wait this test pins
		if resp == nil {
			return tool.Error("no answer arrived"), nil
		}
		return hilsurface.AskUserQuestionResult([]any{map[string]any{"question": "Which region?"}}, resp, time.Minute), nil
	}
}

func TestQuestionsStillBlockWithoutDurableResume(t *testing.T) {
	var sawPublisher bool
	agent, mock := newConcurrentSessionAgent(t, "ask_like", blockingAskTool(&sawPublisher),
		singleToolCallResponse("call-ask", "ask_like"), &llm.ChatResponse{Content: "Thanks, EMEA it is."})
	agent.svc.(*mockServices).brk = newMockBroker()
	user := concurrentUser
	user.A2AStatusTopic = "test-ns/a2a/v1/gateway/status/gw/task-ask"
	done := make(chan *a2a.Response, 1)
	go func() {
		resp, _ := agent.HandleTask(context.Background(), "task-ask", concUserMessage("make a report", concurrentCtxID), user)
		done <- resp
	}()
	if !waitUntil(func() bool { return pendingHILRequestID(agent) == askRequestID }) {
		t.Fatal("the question never started its blocking wait")
	}
	if agent.pausedTasks.Load("task-ask") != nil {
		t.Fatal("the task paused although the agent does not checkpoint its pauses")
	}

	if got := agent.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(user.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered to the blocked tool", got)
	}

	select {
	case resp := <-done:
		if resp == nil {
			t.Fatal("the blocking task returned no response")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the task did not finish after the answer")
	}
	if sawPublisher {
		t.Error("a tool was offered PublishUserInput although the agent does not checkpoint its pauses")
	}
	if calls := mock.getCalls(); len(calls) != 2 || !requestContains(calls[1], "EMEA") {
		t.Errorf("LLM calls = %d, want 2 with the user's answer in the second", len(calls))
	}
}

func TestAnswerFromAnotherUserIsRefusedOnAnyReplica(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	other, _, otherBrk := replicaOf(t, store, "other")
	pausedOnQuestion(t, store, owner, "task-ask")

	if got := other.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser("mallory")); got != hilRefused {
		t.Fatalf("delivery = %v, want refused", got)
	}
	cp, err := store.GetCheckpoint(context.Background(), "task-ask")
	if err != nil || cp == nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if len(cp.ToolResults) != 0 {
		t.Errorf("a refused answer was recorded: %+v", cp.ToolResults)
	}
	topic := a2a.NotifyResponseTopic("test-ns", "test-agent", askRequestID)
	for _, m := range otherBrk.getPublished() {
		if m.topic == topic {
			t.Error("a refused answer was republished")
		}
	}
}
