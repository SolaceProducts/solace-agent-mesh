// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	undecodableCheckpointWarn = "could not decode the checkpoint of an expired call; giving it the generic timeout result"
	timedOutQuestionJSON      = `{"error_code":"DISPATCH_USER_INPUT_UNANSWERED","message":"User did not respond within 60 minutes.","status":"timed_out"}`
)

func checkpointHoldingCall(t *testing.T, kind PendingCallKind) *session.TaskCheckpoint {
	t.Helper()
	state := fullTaskState()
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {
			ToolCallID: "tc-1", ToolName: "ask_user_question", Kind: kind, HILRequestID: askRequestID, Timeout: time.Hour,
			OriginalArgs: map[string]any{"questions": []any{map[string]any{"question": "Which region?"}}},
		},
	}
	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal task state: %v", err)
	}
	return &session.TaskCheckpoint{TaskID: "task-ask-expired", State: blob}
}

func TestExpiredQuestionResult(t *testing.T) {
	tests := []struct {
		name      string
		pending   *session.PendingCall
		cp        *session.TaskCheckpoint
		want      *tool.Result
		wantWarns int
	}{
		{
			name:    "an expired question gets the question timeout result",
			pending: &session.PendingCall{Kind: session.PendingKindHILApproval, ToolCallID: "tc-1"},
			cp:      checkpointHoldingCall(t, PendingCallHILAsk),
			want: tool.OK(timedOutQuestionJSON, tool.WithData(map[string]any{
				"error_code": "DISPATCH_USER_INPUT_UNANSWERED", "message": "User did not respond within 60 minutes.", "status": "timed_out",
			})),
		},
		{
			name:    "an expired approval gets no question-style result",
			pending: &session.PendingCall{Kind: session.PendingKindHILApproval, ToolCallID: "tc-1"},
			cp:      checkpointHoldingCall(t, PendingCallHILApproval),
		},
		{
			name:    "an expired call of another kind gets no question result",
			pending: &session.PendingCall{Kind: session.PendingKindPeer, ToolCallID: "tc-1"},
			cp:      checkpointHoldingCall(t, PendingCallHILAsk),
		},
		{
			name:    "an expired call the checkpoint does not hold gets no question result",
			pending: &session.PendingCall{Kind: session.PendingKindHILApproval, ToolCallID: "tc-gone"},
			cp:      checkpointHoldingCall(t, PendingCallHILAsk),
		},
		{
			name:      "an undecodable checkpoint gets no result and a warning",
			pending:   &session.PendingCall{Kind: session.PendingKindHILApproval, ToolCallID: "tc-1"},
			cp:        &session.TaskCheckpoint{TaskID: "task-ask-expired", State: []byte("{not a task state")},
			wantWarns: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := &syncLogBuffer{}
			logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

			got := expiredQuestionResult(tt.pending, tt.cp, logger)

			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreUnexported(tool.Result{})); diff != "" {
				t.Errorf("expiredQuestionResult() mismatch (-want +got):\n%s", diff)
			}
			warns := logs.records(undecodableCheckpointWarn)
			if diff := cmp.Diff(tt.wantWarns, len(warns)); diff != "" {
				t.Errorf("decode warnings mismatch (-want +got):\n%s", diff)
			}
			for _, w := range warns {
				if w["level"] != "WARN" || w["error"] == nil {
					t.Errorf("decode warning = %v, want level WARN carrying the decode error", w)
				}
			}
		})
	}
}

func TestAbandonedQuestionExpiresWithTheQuestionTimeoutResult(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnQuestion(t, store, owner, "task-ask-expired")
	owner.pausedTasks.Delete("task-ask-expired")
	loseOwner(t, store, state, 1)
	survivorLLM.responses = []*llm.ChatResponse{{Content: "No answer came."}}

	survivor.reapExpiredPendingCalls(t.Context(), time.Now().Add(2*time.Hour))

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, "task-ask-expired", "User did not respond within 60 minutes.")
}
