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
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	repliesWithAPersonRequestTool = "replies_with_a_person_request"
	staysRunningTool              = "stays_running"
	deliveredRequestID            = "req-delivered-at-wiring"
)

func registerBatchOfAnAlreadyDeliveredPersonRequestAndARunningCall(t *testing.T, agent *Agent, runningCall func(deliver func(*tool.Result))) {
	t.Helper()
	register := func(name string, onDelivery func(deliver func(*tool.Result))) {
		err := agent.Tools().Register(&testBuiltinTool{
			name:        name,
			description: name,
			execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
				r := tool.Pending("waiting", tool.WithData(map[string]any{"sub_task_id": "sub-" + name}))
				r.OnDelivery = onDelivery
				return r, nil
			},
		})
		if err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	register(repliesWithAPersonRequestTool, func(deliver func(*tool.Result)) {
		deliver(tool.Pending("waiting for a person", tool.WithData(map[string]any{
			"pending_kind":   "hil_approval",
			"hil_request_id": deliveredRequestID,
			"hil_timeout_ns": float64(time.Hour),
		})))
	})
	register(staysRunningTool, runningCall)
}

func TestAPersonsAnswerToARequestDeliveredWhileTheBatchWasBeingPausedIsHeldUntilTheTaskWaitsOnIt(t *testing.T) {
	agent, _, _ := replicaOf(t, session.NewMemoryStore(), "owner")
	registerBatchOfAnAlreadyDeliveredPersonRequestAndARunningCall(t, agent, func(func(*tool.Result)) {})
	state := newTestTaskState(agent)

	_, paused := dispatchBatch(t, agent, state, []llm.ToolCall{
		{ID: "call-delivered", Name: repliesWithAPersonRequestTool, Arguments: `{}`},
		{ID: "call-running", Name: staysRunningTool, Arguments: `{}`},
	})
	if !paused {
		t.Fatal("dispatch did not pause the batch")
	}

	if got := agent.deliverHILResponse(answerFor(deliveredRequestID), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Errorf("deliverHILResponse() = %v for a person's answer that beat the task's wait, want delivered (held until the wait exists)", got)
	}
}

func TestAPersonsAnswerHeldWhileTheBatchWasBeingPausedIsAppliedWhenTheTaskWaitsOnIt(t *testing.T) {
	holder, mock, brk := replicaOf(t, session.NewMemoryStore(), "holder")
	runningCallDelivery := make(chan func(*tool.Result), 1)
	registerBatchOfAnAlreadyDeliveredPersonRequestAndARunningCall(t, holder, func(deliver func(*tool.Result)) {
		runningCallDelivery <- deliver
	})
	mock.responses = []*llm.ChatResponse{{
		FinishReason: "tool_calls",
		ToolCalls: []llm.ToolCall{
			{ID: "call-delivered", Name: repliesWithAPersonRequestTool, Arguments: `{}`},
			{ID: "call-running", Name: staysRunningTool, Arguments: `{}`},
		},
	}, {Content: "Both are done."}}
	outcome := runTaskInBackground(t, holder, "task-delivered-slot")
	finishPause(t, outcome)
	if got := holder.routeUserInputResponse(t.Context(), answerFor(deliveredRequestID), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("routeUserInputResponse() = %v for an answer that beat the task's wait, want delivered", got)
	}

	var deliver func(*tool.Result)
	select {
	case deliver = <-runningCallDelivery:
	case <-time.After(5 * time.Second):
		t.Fatal("the running call was never given a delivery")
	}
	deliver(tool.OK("the running call finished"))

	assertResumedToCompletion(t, holder, mock, brk, "task-delivered-slot", "the running call finished")
}
