// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hilsurface

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestAskUserQuestionResultReportsHowTheQuestionEnded(t *testing.T) {
	questions := []any{map[string]any{"question": "Which region?"}}
	tests := []struct {
		name        string
		resp        *a2a.UserInputResponseData
		wantData    map[string]any
		wantMessage string
	}{
		{
			name: "an answer is keyed by the question text",
			resp: &a2a.UserInputResponseData{
				Status: "answered", Data: map[string]any{"answers": map[string]any{"q0": "EMEA"}},
			},
			wantData:    map[string]any{"status": "answered", "answers": map[string]string{"Which region?": "EMEA"}},
			wantMessage: `{"answers":{"Which region?":"EMEA"},"status":"answered"}`,
		},
		{
			name:        "a cancelled question says the user cancelled it",
			resp:        &a2a.UserInputResponseData{Status: "cancelled"},
			wantData:    map[string]any{"status": "cancelled", "message": "User cancelled the request."},
			wantMessage: `{"message":"User cancelled the request.","status":"cancelled"}`,
		},
		{
			name: "a timed out question names the wait in whole minutes and carries the unanswered error code",
			resp: &a2a.UserInputResponseData{Status: "timed_out"},
			wantData: map[string]any{
				"status":     "timed_out",
				"message":    "User did not respond within 90 minutes.",
				"error_code": "DISPATCH_USER_INPUT_UNANSWERED",
			},
			wantMessage: `{"error_code":"DISPATCH_USER_INPUT_UNANSWERED","message":"User did not respond within 90 minutes.","status":"timed_out"}`,
		},
		{
			name:        "any other status is reported as unexpected with that status kept",
			resp:        &a2a.UserInputResponseData{Status: "snoozed"},
			wantData:    map[string]any{"status": "snoozed", "message": "Unexpected response status."},
			wantMessage: `{"message":"Unexpected response status.","status":"snoozed"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AskUserQuestionResult(questions, tt.resp, 90*time.Minute)

			if diff := cmp.Diff(tt.wantData, got.Data); diff != "" {
				t.Errorf("AskUserQuestionResult() data mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantMessage, got.Message); diff != "" {
				t.Errorf("AskUserQuestionResult() message mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
