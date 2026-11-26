// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func cancelRequestFor(taskID string) *a2a.Request {
	return &a2a.Request{ID: "req-cancel", Method: "tasks/cancel", Params: map[string]any{"id": taskID}}
}

func awaitSTRCancel(t *testing.T, strBrk *broker.DevBroker, cancels broker.Queue, corrID string) bool {
	t.Helper()
	cancelTopic := remotetool.CancelTopic("test-ns", corrID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		msg, recvErr := strBrk.Receive(ctx, cancels)
		cancel()
		if recvErr == nil && msg != nil {
			msg.Ack()
			if msg.Topic == cancelTopic {
				return true
			}
		}
	}
	return false
}

func TestCancelOnAnotherReplicaTellsTheSTRAndAuditsTheOpenRemoteCall(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	other, _, _ := replicaOf(t, store, "other")
	rec := &toolAuditRecorder{}
	other.svc.(*mockServices).auditLog = rec
	strBrk := strBroker(t)
	registerRemoteTool(t, other, strBrk)
	cancels, err := strBrk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create cancel subscription: %v", err)
	}
	if err := strBrk.Subscribe(cancels, "test-ns/>"); err != nil {
		t.Fatalf("subscribe to test-ns/>: %v", err)
	}
	const taskID, corrID = "task-remote-cancel", "corr-remote-cancel"
	state := pausedOnRemoteCallWithOriginalSession(t, store, owner, taskID, corrID, "parent-session-cancel")

	other.handleCancelRequest(cancelRequestFor(taskID))

	if !awaitSTRCancel(t, strBrk, cancels, corrID) {
		t.Error("the STR was never told to cancel the call of a task cancelled on a replica that does not hold it")
	}
	want := []auth.ToolExecutionEvent{{
		UserID: state.UserProps.UserID, CredentialID: auditAdoptedKey, AgentName: "test-agent", ToolName: remoteReportTool,
		SessionID: "parent-session-cancel", TraceID: state.UserProps.TraceID, Success: false, Error: auditedFailureErrorType,
	}}
	if diff := cmp.Diff(want, rec.eventsFor(remoteReportTool), cmpopts.IgnoreFields(auth.ToolExecutionEvent{}, "DurationMs")); diff != "" {
		t.Errorf("audit events for the cancelled remote call (-want +got):\n%s", diff)
	}
}

func TestCancelOnAnotherReplicaAuditsTheRefusalOfAnUnansweredApproval(t *testing.T) {
	store := session.NewMemoryStore()
	holder, holderLLM, _, _ := approvalReplica(t, store, "holder", time.Hour)
	other, _, _, otherRuns := approvalReplica(t, store, "other", time.Hour)
	rec := &toolAuditRecorder{}
	other.svc.(*mockServices).auditLog = rec
	startGatedTask(t, holder, holderLLM, "task-cancel-elsewhere")

	other.handleCancelRequest(cancelRequestFor("task-cancel-elsewhere"))

	if ev := rec.only(t); ev.ApprovalRefusal != string(tool.RefusalCancelled) || ev.ToolName != approvalTool {
		t.Errorf("audit = %+v, want a cancelled refusal of %s", ev, approvalTool)
	}
	if otherRuns.Load() != 0 {
		t.Errorf("the replica that cancelled ran the approved call %d times", otherRuns.Load())
	}
}

func TestCancelAuditsAnApprovalThatWasAnsweredButNotYetSettled(t *testing.T) {
	holder, mock, _, runs := approvalReplica(t, session.NewMemoryStore(), "holder", time.Hour)
	rec := &toolAuditRecorder{}
	holder.svc.(*mockServices).auditLog = rec
	_, requestID := startGatedTask(t, holder, mock, "task-answered-then-cancelled")
	swallowResumes(t)
	if got := holder.routeUserInputResponse(t.Context(), approve(requestID, true), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}

	holder.handleCancelRequest(cancelRequestFor("task-answered-then-cancelled"))

	if ev := rec.only(t); ev.ApprovalRefusal != string(tool.RefusalCancelled) || ev.ToolName != approvalTool {
		t.Errorf("audit = %+v, want a cancelled refusal of %s for the approved call that never ran", ev, approvalTool)
	}
	if runs.Load() != 0 {
		t.Errorf("a cancelled task ran its approved call %d times", runs.Load())
	}
}

func TestHolderThatLostTheTaskDoesNotAuditTheApprovalAnotherReplicaCancelled(t *testing.T) {
	store := session.NewMemoryStore()
	holder, holderLLM, _, _ := approvalReplica(t, store, "holder", time.Hour)
	other, _, _, _ := approvalReplica(t, store, "other", time.Hour)
	holderAudit, otherAudit := &toolAuditRecorder{}, &toolAuditRecorder{}
	holder.svc.(*mockServices).auditLog = holderAudit
	other.svc.(*mockServices).auditLog = otherAudit
	startGatedTask(t, holder, holderLLM, "task-lost-then-cancelled")
	other.handleCancelRequest(cancelRequestFor("task-lost-then-cancelled"))

	holder.handleCancelRequest(cancelRequestFor("task-lost-then-cancelled"))

	if diff := cmp.Diff(map[string]int{"holder": 0, "other": 1}, map[string]int{"holder": len(holderAudit.events), "other": len(otherAudit.events)}); diff != "" {
		t.Errorf("approval audit events per replica (-want +got):\n%s", diff)
	}
}
