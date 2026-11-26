// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestAnApprovedCallThatIsNotRunWritesOneRefusalAuditEvent(t *testing.T) {
	approved := tool.OK("answered", tool.WithData(map[string]any{
		toolApprovalStatusKey: "answered",
		toolApprovalDataKey:   map[string]any{"approved": true},
	}))
	unreadable := tool.OK("no approval status in this result")
	tests := []struct {
		name     string
		toolName string
		args     map[string]any
		answer   *tool.Result
		bindings map[string]map[string]string
		want     string
	}{
		{
			name:     "the stored answer cannot be read",
			toolName: approvalTool,
			args:     map[string]any{"to": "Acme"},
			answer:   unreadable,
			want:     "unreadable",
		},
		{
			name:     "the tool was removed while the approval waited",
			toolName: "removed_tool",
			args:     map[string]any{"to": "Acme"},
			answer:   approved,
			want:     "unanswerable",
		},
		{
			name:     "the call cannot be prepared",
			toolName: approvalTool,
			args:     map[string]any{"to": "Acme"},
			answer:   approved,
			bindings: map[string]map[string]string{approvalTool: {"url": "undeclared-policy"}},
			want:     "unanswerable",
		},
		{
			name:     "the stored arguments cannot be encoded",
			toolName: approvalTool,
			args:     map[string]any{"to": math.NaN()},
			answer:   approved,
			want:     "unreadable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replica, _, _, runs := approvalReplica(t, session.NewMemoryStore(), "owner", approvalWaitInHarness)
			replica.SetToolNetworkBindings(tt.bindings)
			rec := &toolAuditRecorder{}
			replica.svc.(*mockServices).auditLog = rec
			state := &TaskExecutionState{TaskID: "task-approved-not-run"}
			call := pausedApprovalCall{requestID: approvalRequestID, args: tt.args, timeout: approvalWaitInHarness}
			toolCtx := &tool.Context{UserID: concurrentUser.UserID, SessionID: "sess-1", TraceID: "trace-1"}

			got := replica.settlePausedApproval(t.Context(), state, nil, toolCtx, pausedToolCall{toolCall: llm.ToolCall{ID: "call-send", Name: tt.toolName}, approval: call}, tt.answer, slog.Default())

			if got == nil || got.Status != tool.StatusError {
				t.Errorf("settled result = %+v, want an error telling the model the call was not run", got)
			}
			if n := runs.Load(); n != 0 {
				t.Errorf("the call ran %d times, want 0", n)
			}
			wantEvent := auth.ToolExecutionEvent{
				UserID:          concurrentUser.UserID,
				AgentName:       "test-agent",
				ToolName:        tt.toolName,
				SessionID:       "sess-1",
				TraceID:         "trace-1",
				Success:         false,
				Error:           tool.AuditToolErrorCode,
				ApprovalRefusal: tt.want,
			}
			if diff := cmp.Diff(wantEvent, rec.only(t)); diff != "" {
				t.Errorf("refusal audit event mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
