// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	cannotCancelRemoteCallMessage = "cannot cancel a paused remote tool call: its tool is not registered here"
	delegatedReportTool           = "delegate_report"
	delegatedReportCall           = "call-r3"
	delegatedReportSubTask        = "peer-sub-task-1"
	remoteKindWithoutIDTool       = "pending_without_id"
	remoteKindWithoutIDCall       = "call-r4"
)

func registerPendingStub(t *testing.T, agent *Agent, name string, result *tool.Result) {
	t.Helper()
	err := agent.tools.Register(&testBuiltinTool{
		name:        name,
		description: "pending stub for the stand-down test",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			return result, nil
		},
	})
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
}

func cancelsPublishedFor(cancels broker.Queue, strBrk *broker.DevBroker, topics []string, settle time.Duration) map[string]int {
	seen := map[string]int{}
	deadline := time.Now().Add(5 * time.Second)
	var settledAt time.Time
	for time.Now().Before(deadline) {
		if len(seen) == len(topics) && settledAt.IsZero() {
			settledAt = time.Now().Add(settle)
		}
		if !settledAt.IsZero() && time.Now().After(settledAt) {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		msg, err := strBrk.Receive(ctx, cancels)
		cancel()
		if err != nil || msg == nil {
			continue
		}
		msg.Ack()
		if slices.Contains(topics, msg.Topic) {
			seen[msg.Topic]++
		}
	}
	return seen
}

func TestAHolderThatStandsDownAtThePauseCancelsEveryRemoteCallItDispatched(t *testing.T) {
	store := &checkpointWriteHook{Store: session.NewMemoryStore(), onWrite: func() error { time.Sleep(callRunsFor); return session.ErrNotOwner }}
	var logs *syncLogBuffer
	agent, mock, _ := replicaOf(t, store, "owner", func(a *Agent) { logs = captureAgentLogs(a) })
	rec := &toolAuditRecorder{}
	agent.svc.(*mockServices).auditLog = rec
	strBrk := strBroker(t)
	exe := registerRemoteTool(t, agent, strBrk)
	registerRemoteToolOn(t, agent, exe, siblingRemoteTool)
	registerPendingStub(t, agent, delegatedReportTool, &tool.Result{Status: tool.StatusPending, Message: "delegated", Data: map[string]any{"sub_task_id": delegatedReportSubTask}})
	registerPendingStub(t, agent, remoteKindWithoutIDTool, &tool.Result{
		Status: tool.StatusPending, Message: "claims a remote pause", OnDelivery: func(func(*tool.Result)) {},
		Data: map[string]any{"pending_kind": "remote_tool"},
	})
	mock.responses = []*llm.ChatResponse{{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{
		{ID: personRequestCall, Name: remoteReportTool, Arguments: `{}`},
		{ID: siblingCall, Name: siblingRemoteTool, Arguments: `{}`},
		{ID: delegatedReportCall, Name: delegatedReportTool, Arguments: `{}`},
		{ID: remoteKindWithoutIDCall, Name: remoteKindWithoutIDTool, Arguments: `{}`},
	}}}
	firstIDs, _ := heldSTRForTool(t, strBrk, remoteReportTool, failingSTRReply)
	secondIDs, _ := heldSTRForTool(t, strBrk, siblingRemoteTool, failingSTRReply)
	cancels, err := strBrk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create cancel subscription: %v", err)
	}
	if err := strBrk.Subscribe(cancels, "test-ns/>"); err != nil {
		t.Fatalf("subscribe to test-ns/>: %v", err)
	}

	if _, err := agent.HandleTask(t.Context(), "task-remote-lost-cancel", messageWithParentSession("build the reports"), holderUser()); err != nil {
		t.Fatalf("task: %v", err)
	}
	cancelTopics := []string{
		remotetool.CancelTopic("test-ns", waitForCorrID(t, firstIDs)),
		remotetool.CancelTopic("test-ns", waitForCorrID(t, secondIDs)),
	}

	gotCancels := cancelsPublishedFor(cancels, strBrk, cancelTopics, 200*time.Millisecond)
	wantCancels := map[string]int{cancelTopics[0]: 1, cancelTopics[1]: 1}
	if diff := cmp.Diff(wantCancels, gotCancels); diff != "" {
		t.Errorf("cancels published to the STR per dispatched call (-want +got):\n%s", diff)
	}
	auditedCalls := map[string]int{
		remoteReportTool:  len(rec.eventsFor(remoteReportTool)),
		siblingRemoteTool: len(rec.eventsFor(siblingRemoteTool)),
	}
	if diff := cmp.Diff(map[string]int{remoteReportTool: 1, siblingRemoteTool: 1}, auditedCalls); diff != "" {
		t.Errorf("undelivered audit events per dispatched call (-want +got):\n%s", diff)
	}
	if got := logs.records(cannotCancelRemoteCallMessage); len(got) != 0 {
		t.Errorf("stand-down tried to cancel %d armed calls that are not remote tool calls with an id, want none: %v", len(got), got)
	}
}
