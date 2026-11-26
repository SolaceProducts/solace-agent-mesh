// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type recordedByTheStore struct {
	PendingCount int
	Results      []string
}

type deliveredAfterHandoff struct {
	Stored          recordedByTheStore
	AuditEvents     []auth.ToolExecutionEvent
	MetricResults   []string
	MetricDurations []time.Duration
}

const (
	handoffUser      = "user-handoff"
	handoffSession   = "session-handoff"
	handoffTrace     = "trace-handoff"
	dispatchedAgo    = time.Hour
	durationSlackMax = time.Minute
)

func deliverAfter(t *testing.T, kind PendingCallKind, endTask func(session.Store, *TaskExecutionState)) deliveredAfterHandoff {
	t.Helper()
	const taskID = "task-reply-after-handoff"
	store := session.NewMemoryStore()
	holder, _, _ := replicaOf(t, store, "holder")
	audit := &toolAuditRecorder{}
	metrics := &lockedToolMetrics{}
	holderServices := holder.svc.(*mockServices)
	holderServices.auditLog, holderServices.metrics = audit, metrics
	state := pausedOnRemoteCallLostOwner(t, store, holder, taskID, idgen.New())
	state.PendingCalls["tc-1"].Kind = kind
	var deliver func(*tool.Result)
	armed := armedPendingCall{
		pc:     state.PendingCalls["tc-1"],
		result: &tool.Result{OnDelivery: func(d func(*tool.Result)) { deliver = d }},
	}
	toolCtx := &tool.Context{UserID: handoffUser, SessionID: handoffSession, TraceID: handoffTrace}
	holder.wireDelivery(t.Context(), state, armed, toolCtx, time.Now().Add(-dispatchedAgo), holder.logger)
	endTask(store, state)

	deliver(tool.OK("the tool finished"))

	cp, err := store.GetCheckpoint(t.Context(), taskID)
	if err != nil || cp == nil {
		t.Fatalf("GetCheckpoint() = %v, %v, want the checkpoint", cp, err)
	}
	got := deliveredAfterHandoff{
		Stored:        recordedByTheStore{PendingCount: cp.PendingCount},
		AuditEvents:   audit.eventsFor(remoteReportTool),
		MetricResults: metrics.errorTypesFor(remoteReportTool),

		MetricDurations: metrics.durationsFor(remoteReportTool),
	}
	for _, r := range cp.ToolResults {
		got.Stored.Results = append(got.Stored.Results, r.ToolCallID+"="+r.Content)
	}
	return got
}

func fenceTheTask(_ session.Store, state *TaskExecutionState) { state.Fenced.Store(true) }

func cancelTheTask(_ session.Store, state *TaskExecutionState) { state.Cancelled.Store(true) }

func ignoreAuditDuration() cmp.Option {
	return cmpopts.IgnoreFields(auth.ToolExecutionEvent{}, "DurationMs")
}

func TestAReplyThatArrivesAfterTheTaskWasHandedOffIsRecordedAndAuditedOnce(t *testing.T) {
	got := deliverAfter(t, PendingCallRemoteTool, fenceTheTask)

	want := deliveredAfterHandoff{
		Stored: recordedByTheStore{PendingCount: 0, Results: []string{"tc-1=the tool finished"}},
		AuditEvents: []auth.ToolExecutionEvent{
			{UserID: handoffUser, AgentName: "test-agent", ToolName: remoteReportTool, SessionID: handoffSession, TraceID: handoffTrace, Success: true},
		},
		MetricResults: []string{""},
	}
	ranFor := func(d time.Duration) bool { return d >= dispatchedAgo && d < dispatchedAgo+durationSlackMax }
	ignoreDurations := cmpopts.IgnoreFields(deliveredAfterHandoff{}, "MetricDurations")
	if diff := cmp.Diff(want, got, ignoreAuditDuration(), ignoreDurations); diff != "" {
		t.Errorf("reply after a handoff (-want +got):\n%s", diff)
	}
	if len(got.AuditEvents) == 1 && !ranFor(time.Duration(got.AuditEvents[0].DurationMs*float64(time.Millisecond))) {
		t.Errorf("audit duration = %vms, want the hour since the call was dispatched, within %v", got.AuditEvents[0].DurationMs, durationSlackMax)
	}
	if len(got.MetricDurations) != 1 || !ranFor(got.MetricDurations[0]) {
		t.Errorf("metric durations = %v, want one of about the hour since the call was dispatched, within %v", got.MetricDurations, durationSlackMax)
	}
}

func TestAReplyThatArrivesAfterAnotherReplicaResolvedTheCallIsNotAuditedAgain(t *testing.T) {
	resolveElsewhere := func(store session.Store, state *TaskExecutionState) {
		fenceTheTask(store, state)
		pc := state.PendingCalls["tc-1"]
		_, err := store.RecordToolResult(t.Context(), state.TaskID, pc.SubTaskID, &session.ToolCallResult{
			ToolCallID: pc.ToolCallID, ToolName: pc.ToolName, Content: "the adopter's reply",
		})
		if err != nil {
			t.Fatalf("RecordToolResult() = %v, want the other replica's result recorded", err)
		}
	}

	got := deliverAfter(t, PendingCallRemoteTool, resolveElsewhere)

	want := deliveredAfterHandoff{
		Stored: recordedByTheStore{PendingCount: 0, Results: []string{"tc-1=the adopter's reply"}},
	}
	if diff := cmp.Diff(want, got, ignoreAuditDuration(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("reply after another replica resolved the call (-want +got):\n%s", diff)
	}
}

func TestAReplyThatArrivesAfterTheTaskWasCancelledIsNotRecordedOrAudited(t *testing.T) {
	got := deliverAfter(t, PendingCallRemoteTool, cancelTheTask)

	want := deliveredAfterHandoff{Stored: recordedByTheStore{PendingCount: 1}}
	if diff := cmp.Diff(want, got, ignoreAuditDuration(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("reply after a cancel (-want +got):\n%s", diff)
	}
}

func TestAReplyThatArrivesAfterTheTaskWasCancelledAndHandedOffIsDropped(t *testing.T) {
	got := deliverAfter(t, PendingCallRemoteTool, func(store session.Store, state *TaskExecutionState) {
		fenceTheTask(store, state)
		cancelTheTask(store, state)
	})

	want := deliveredAfterHandoff{Stored: recordedByTheStore{PendingCount: 1}}
	if diff := cmp.Diff(want, got, ignoreAuditDuration(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("reply after a cancel and a handoff (-want +got):\n%s", diff)
	}
}

func TestAPeerReplyThatArrivesAfterTheTaskWasHandedOffIsNotRecordedOrAudited(t *testing.T) {
	got := deliverAfter(t, PendingCallPeer, fenceTheTask)

	want := deliveredAfterHandoff{Stored: recordedByTheStore{PendingCount: 1}}
	if diff := cmp.Diff(want, got, ignoreAuditDuration(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("peer reply after a handoff (-want +got):\n%s", diff)
	}
}
