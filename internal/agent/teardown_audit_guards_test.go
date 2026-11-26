// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type strInvocation struct {
	corrID  string
	replyTo string
}

func capturedSTR(t *testing.T, brk *broker.DevBroker, count int) <-chan strInvocation {
	t.Helper()
	q, err := brk.CreateQueue("str-"+idgen.New(), broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create STR queue: %v", err)
	}
	if err := brk.Subscribe(q, remotetool.InvokeTopic("test-ns", remoteReportTool)); err != nil {
		t.Fatalf("subscribe STR queue to %s invokes: %v", remoteReportTool, err)
	}
	invocations := make(chan strInvocation, count)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for range count {
			msg, recvErr := brk.Receive(ctx, q)
			if recvErr != nil {
				return
			}
			msg.Ack()
			invocations <- strInvocation{corrID: msg.CorrelationID, replyTo: msg.UserProperties[remotetool.PropReplyTo]}
		}
	}()
	return invocations
}

func nextInvocation(t *testing.T, invocations <-chan strInvocation) strInvocation {
	t.Helper()
	select {
	case inv := <-invocations:
		return inv
	case <-time.After(5 * time.Second):
		t.Fatal("the STR never received the invocation")
		return strInvocation{}
	}
}

func twoRemoteCallsResponse() *llm.ChatResponse {
	return &llm.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls: []llm.ToolCall{
			{ID: "call-r1", Name: remoteReportTool, Arguments: `{}`},
			{ID: "call-r2", Name: remoteReportTool, Arguments: `{}`},
		},
	}
}

func TestCancellingATaskAuditsOnlyTheRemoteCallsWhoseReplyHasNotBeenDelivered(t *testing.T) {
	agent, mock, _ := replicaOf(t, session.NewMemoryStore(), "owner")
	rec := &toolAuditRecorder{}
	agent.svc.(*mockServices).auditLog = rec
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{twoRemoteCallsResponse()}
	invocations := capturedSTR(t, strBrk, 2)

	if resp, err := agent.HandleTask(t.Context(), "task-remote-partial", messageWithParentSession("build both reports"), holderUser()); err != nil || resp != nil {
		t.Fatalf("the task should pause on the remote tools: resp=%v err=%v", resp, err)
	}
	first := nextInvocation(t, invocations)
	nextInvocation(t, invocations)
	state := agent.pausedTasks.Load("task-remote-partial")
	if state == nil {
		t.Fatal("the task is not paused")
	}
	publishSTRReply(t, strBrk, first.replyTo, first.corrID, "first report ready")
	if !waitUntil(func() bool { return len(rec.eventsFor(remoteReportTool)) == 1 }) {
		t.Fatal("the delivered reply was never audited")
	}

	agent.cancelPausedTask(state, cancelFromElsewhere)

	var succeeded, failed int
	for _, e := range rec.eventsFor(remoteReportTool) {
		if e.Success {
			succeeded++
		} else {
			failed++
		}
	}
	if succeeded != 1 || failed != 1 {
		t.Errorf("audit events = %d succeeded and %d failed, want 1 and 1: the delivered call keeps its outcome and only the undelivered one is audited as ended", succeeded, failed)
	}
}

func TestCancellingATaskWhoseRowAnotherProcessTookAuditsNoRemoteCall(t *testing.T) {
	store := session.NewMemoryStore()
	agent, mock, _ := replicaOf(t, store, "owner")
	rec := &toolAuditRecorder{}
	agent.svc.(*mockServices).auditLog = rec
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool)}
	ids, _ := heldSTR(t, strBrk, "never sent")

	if resp, err := agent.HandleTask(t.Context(), "task-remote-taken", messageWithParentSession("build the report"), holderUser()); err != nil || resp != nil {
		t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
	}
	corrID := waitForCorrID(t, ids)
	state := agent.pausedTasks.Load("task-remote-taken")
	if state == nil {
		t.Fatal("the task is not paused")
	}
	if _, err := store.RecordToolResult(t.Context(), "task-remote-taken", corrID, &session.ToolCallResult{ToolCallID: "call-r1", ToolName: remoteReportTool, Content: "adopted elsewhere"}); err != nil {
		t.Fatalf("resolve the call as another replica would: %v", err)
	}
	if cp, err := takeOver(t, store, "task-remote-taken", "successor"); err != nil || cp == nil {
		t.Fatalf("takeOver() = %+v (err %v), want the successor to adopt the resolved task", cp, err)
	}

	agent.cancelPausedTask(state, cancelFromElsewhere)

	if events := rec.eventsFor(remoteReportTool); len(events) != 0 {
		t.Errorf("audit events = %+v, want none: the process that took the row owns the call's outcome", events)
	}
}
