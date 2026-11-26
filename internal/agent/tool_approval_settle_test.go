// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func assertAnApprovedCallIsNotRunOnceItsTaskIsCancelled(ctx context.Context, t *testing.T, state *TaskExecutionState) {
	t.Helper()
	replica, _, _, runs := approvalReplica(t, session.NewMemoryStore(), "owner", approvalWaitInHarness)
	rec := &toolAuditRecorder{}
	replica.svc.(*mockServices).auditLog = rec
	call := pausedApprovalCall{requestID: approvalRequestID, args: map[string]any{"to": "Acme"}, timeout: approvalWaitInHarness}
	approved := toolApprovalAnswer(&a2a.UserInputResponseData{Status: "answered", Data: map[string]any{"approved": true}}, call)
	toolCtx := &tool.Context{UserID: concurrentUser.UserID, SessionID: "sess-1", TraceID: "trace-1"}

	got := replica.settlePausedApproval(ctx, state, nil, toolCtx, pausedToolCall{toolCall: llm.ToolCall{ID: "call-send", Name: approvalTool}, approval: call}, approved, slog.Default())

	if got == nil || got.Status != tool.StatusError {
		t.Errorf("settled result = %+v, want an error telling the model the call was not run", got)
	}
	if n := runs.Load(); n != 0 {
		t.Errorf("the approved call ran %d times after its task was cancelled", n)
	}
	want := auth.ToolExecutionEvent{
		UserID:          concurrentUser.UserID,
		AgentName:       "test-agent",
		ToolName:        approvalTool,
		SessionID:       "sess-1",
		TraceID:         "trace-1",
		Success:         false,
		Error:           tool.AuditToolErrorCode,
		ApprovalRefusal: string(tool.RefusalCancelled),
	}
	if diff := cmp.Diff(want, rec.only(t)); diff != "" {
		t.Errorf("refusal audit event mismatch (-want +got):\n%s", diff)
	}
}

func TestAnApprovedCallIsNotRunWhenItsTaskIsMarkedCancelled(t *testing.T) {
	state := &TaskExecutionState{TaskID: "task-approved-then-cancelled"}
	state.Cancelled.Store(true)

	assertAnApprovedCallIsNotRunOnceItsTaskIsCancelled(context.Background(), t, state)
}

func TestAnApprovedCallIsNotRunWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assertAnApprovedCallIsNotRunOnceItsTaskIsCancelled(ctx, t, &TaskExecutionState{TaskID: "task-approved-then-cancelled"})
}
