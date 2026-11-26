// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type gatedTaskDelivery struct {
	acked  *atomic.Int32
	nacked *atomic.Int32
	done   chan struct{}
}

func deliverGatedTaskRequest(ctx context.Context, t *testing.T, agent *Agent, taskID string) *gatedTaskDelivery {
	t.Helper()
	user := approvalUser(taskID)
	payload, err := json.Marshal(a2a.NewRequest(taskID, "message/send", map[string]any{"message": concUserMessage("send the weekly report", idgen.New())}))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	delivery := &gatedTaskDelivery{acked: &atomic.Int32{}, nacked: &atomic.Int32{}, done: make(chan struct{})}
	msg := broker.NewDeliveredMessage("delivery-"+taskID, a2a.AgentRequestTopic("test-ns", "test-agent"), payload,
		func() { delivery.acked.Add(1) }, func() { delivery.nacked.Add(1) })
	user.Timestamp = time.Now().UnixMilli()
	msg.UserProperties = a2a.EncodeUserProperties(&user)
	go func() {
		defer close(delivery.done)
		agent.processMessage(ctx, msg, &req)
	}()
	return delivery
}

func unsavedGatedTask(ctx context.Context, t *testing.T, taskID string) (*Agent, *mockLLM, *mockBroker, *approvalRuns, *gatedTaskDelivery, string) {
	t.Helper()
	store := failingRegistration{Store: session.NewMemoryStore(), err: errors.New("the session database is unreachable")}
	holder, mock, brk, runs := approvalReplica(t, store, "holder", time.Hour)
	mock.responses = []*llm.ChatResponse{{
		FinishReason: "tool_calls",
		ToolCalls:    []llm.ToolCall{{ID: "call-send", Name: approvalTool, Arguments: approvalToolArgs}},
	}, {Content: "All done."}}
	delivery := deliverGatedTaskRequest(ctx, t, holder, taskID)
	if !waitUntil(func() bool { return holder.pausedTasks.Load(taskID) != nil }) {
		t.Fatal("the task never paused on its approval")
	}
	state := holder.pausedTasks.Load(taskID)
	state.mu.Lock()
	requestID := state.PendingCalls["call-send"].HILRequestID
	state.mu.Unlock()
	return holder, mock, brk, runs, delivery, requestID
}

func TestApprovalWaitThatCouldNotBeCheckpointedKeepsItsRequestUnackedUntilTheTaskEnds(t *testing.T) {
	holder, mock, brk, runs, delivery, requestID := unsavedGatedTask(t.Context(), t, "task-unsaved")

	if brieflyBecomes(func() bool { return delivery.acked.Load() != 0 }) {
		t.Fatalf("the request was acked %d times while its task waited on an approval it could not checkpoint, so a lost process loses the task", delivery.acked.Load())
	}
	if got := holder.routeUserInputResponse(t.Context(), approve(requestID, true), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}

	assertResumedToCompletion(t, holder, mock, brk, "task-unsaved", approvalToolOut)
	select {
	case <-delivery.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the request message was never settled after the task finished")
	}
	if delivery.acked.Load() != 1 || delivery.nacked.Load() != 0 || runs.Load() != 1 {
		t.Errorf("acked %d, nacked %d, approved call runs %d; want 1, 0 and 1", delivery.acked.Load(), delivery.nacked.Load(), runs.Load())
	}
}

func TestPeerWaitThatCouldNotBeCheckpointedStillAcksItsRequestAtThePause(t *testing.T) {
	store := failingRegistration{Store: session.NewMemoryStore(), err: errors.New("the session database is unreachable")}
	agent, mock, _ := replicaOf(t, store, "holder")
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "delegate_like",
		description: "starts a peer call",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			return tool.Pending("waiting for the peer", tool.WithData(map[string]any{"sub_task_id": "st-unsaved"})), nil
		},
	}); err != nil {
		t.Fatalf("register delegate_like: %v", err)
	}
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-peer", "delegate_like"), {Content: "All done."}}

	delivery := deliverGatedTaskRequest(t.Context(), t, agent, "task-peer-unsaved")

	select {
	case <-delivery.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the request stayed held for a peer wait, which keeps the behaviour it has without the feature")
	}
	if delivery.acked.Load() != 1 {
		t.Errorf("acked %d times at the pause of a peer wait, want 1", delivery.acked.Load())
	}
}

func TestOnlyTheWaitsThatBlockedBeforeTheFeatureHoldTheirRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind PendingCallKind
		want bool
	}{
		{name: "a remote tool call holds the request", kind: PendingCallRemoteTool, want: true},
		{name: "a question to the user holds the request", kind: PendingCallHILAsk, want: true},
		{name: "a tool approval holds the request", kind: PendingCallToolApproval, want: true},
		{name: "a peer call keeps acking at the pause", kind: PendingCallPeer, want: false},
		{name: "a workflow call keeps acking at the pause", kind: PendingCallWorkflow, want: false},
		{name: "a tool's own approval wait keeps acking at the pause", kind: PendingCallHILApproval, want: false},
		{name: "a sign-in keeps acking at the pause", kind: PendingCallAuth, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			armed := []armedPendingCall{{pc: &PendingToolCall{Kind: tc.kind}}}

			if got := holdsABlockingWait(armed); got != tc.want {
				t.Errorf("holdsABlockingWait(%v) = %v, want %v", tc.kind, got, tc.want)
			}
		})
	}
}

func TestAnswerMessageToATaskWhoseRequestIsHeldIsAckedAtOnce(t *testing.T) {
	holder, _, _, _, original, requestID := unsavedGatedTask(t.Context(), t, "task-unsaved-answer")
	swallowResumes(t)
	msg, req, counts := answerDelivery(t, approve(requestID, true), "task-unsaved-answer", concurrentUser.UserID, false)
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		holder.processMessage(t.Context(), msg, req)
	}()

	select {
	case <-answered:
	case <-time.After(3 * time.Second):
		t.Fatal("the answer message stayed parked behind the held request of the task it answers")
	}
	if counts.acked.Load() != 1 || original.acked.Load() != 0 {
		t.Errorf("answer acked %d times, original request acked %d times; want 1 and 0", counts.acked.Load(), original.acked.Load())
	}
}

func TestRepauseThatCouldNotBeCheckpointedHoldsNoRequest(t *testing.T) {
	store := &registrationSwitch{Store: session.NewMemoryStore()}
	holder, mock, _, _ := approvalReplica(t, store, "holder", time.Hour)
	mock.responses = []*llm.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call-send", Name: approvalTool, Arguments: approvalToolArgs}}},
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call-send-2", Name: approvalTool, Arguments: approvalToolArgs}}},
		{Content: "All done."},
	}
	delivery := deliverGatedTaskRequest(t.Context(), t, holder, "task-repause")
	select {
	case <-delivery.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the first pause did not release its request")
	}
	state := holder.pausedTasks.Load("task-repause")
	state.mu.Lock()
	requestID := state.PendingCalls["call-send"].HILRequestID
	state.mu.Unlock()
	failure := errors.New("the session database is unreachable")
	store.failWith.Store(&failure)

	holder.routeUserInputResponse(t.Context(), approve(requestID, true), hilSenderUser(concurrentUser.UserID))

	if !waitUntil(func() bool {
		repaused := holder.pausedTasks.Load("task-repause")
		if repaused == nil {
			return false
		}
		repaused.mu.Lock()
		defer repaused.mu.Unlock()
		_, waitsOnSecondCall := repaused.PendingCalls["call-send-2"]
		return waitsOnSecondCall
	}) {
		t.Fatal("the resumed turn never paused again")
	}
	if holder.pausedTasks.Load("task-repause").heldRequest() != nil {
		t.Error("a re-pause that could not be checkpointed holds a request message, but its original request was acked at the first pause")
	}
}

func TestRepauseOfAnAdoptedTaskThatCouldNotBeCheckpointedHoldsNoRequest(t *testing.T) {
	base := session.NewMemoryStore()
	store := &registrationSwitch{Store: base}
	owner, _, _ := replicaOf(t, base, "owner")
	survivor, survivorLLM, _, _ := approvalReplica(t, store, "survivor", time.Hour)
	survivorLLM.responses = []*llm.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call-send", Name: approvalTool, Arguments: approvalToolArgs}}},
		{Content: "All done."},
	}
	state := pausedOnPeerCall(t, base, "task-adopted-repause", "sub-adopted-repause")
	state.UserProps.A2AStatusTopic = "test-ns/a2a/v1/gateway/status/gw/task-adopted-repause"
	failure := errors.New("the session database is unreachable")
	store.failWith.Store(&failure)

	adoptAfterAnswer(t, base, owner, survivor, state, "peer finished the work")

	if !waitUntil(func() bool {
		repaused := survivor.pausedTasks.Load("task-adopted-repause")
		if repaused == nil {
			return false
		}
		repaused.mu.Lock()
		defer repaused.mu.Unlock()
		_, waitsOnApproval := repaused.PendingCalls["call-send"]
		return waitsOnApproval
	}) {
		t.Fatal("the adopted turn never paused on the approval")
	}
	if survivor.pausedTasks.Load("task-adopted-repause").heldRequest() != nil {
		t.Error("an adopted task's re-pause holds a request message, but the adopting replica has none")
	}
}

func TestStopWhileAnUncheckpointedWaitHoldsItsRequestLeavesItForRedelivery(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	_, _, _, _, delivery, _ := unsavedGatedTask(ctx, t, "task-unsaved-stop")

	stop()

	select {
	case <-delivery.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the held request was never released after the stop")
	}
	if delivery.acked.Load() != 0 {
		t.Errorf("the request was acked %d times although its task was cut off, so it would not be redelivered", delivery.acked.Load())
	}
}
