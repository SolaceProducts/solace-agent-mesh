// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestLogToolExecution_ApprovalReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		want   bool
	}{
		{"human approval is recorded", "human", true},
		{"self-certified dispatch is recorded", "enforcement_disabled", true},
		{"an ungated tool carries no reason", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, buf := testLoggerJSON()
			l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
				UserID: "u1", AgentName: "a1", ToolName: "danger", SessionID: "s1",
				Success: true, ApprovalReason: tc.reason,
			})
			got := buf.String()
			if strings.Contains(got, "approvalReason") != tc.want {
				t.Errorf("approvalReason present = %v, want %v: %s", !tc.want, tc.want, got)
			}
			if tc.want && !strings.Contains(got, tc.reason) {
				t.Errorf("record does not carry %q: %s", tc.reason, got)
			}
		})
	}
}

func TestLogToolExecution_ApprovalReasonOnFailure(t *testing.T) {
	l, buf := testLoggerJSON()
	l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
		UserID: "u1", AgentName: "a1", ToolName: "danger", SessionID: "s1",
		Success: false, Error: "tool_error", ApprovalReason: "enforcement_disabled",
	})
	got := buf.String()
	if !strings.Contains(got, "approvalReason") || !strings.Contains(got, "enforcement_disabled") {
		t.Errorf("failure record dropped the approval reason: %s", got)
	}
	if !strings.Contains(got, "tool_error") {
		t.Errorf("failure record dropped the error class: %s", got)
	}
}

func TestLogToolExecution_ApprovalRefusalReachesTheRecord(t *testing.T) {
	l, buf := testLoggerJSON()
	l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
		UserID: "u1", AgentName: "a1", ToolName: "danger", SessionID: "s1",
		Success: false, Error: tool.AuditToolErrorCode,
		ApprovalRefusal: string(tool.RefusalDenied),
	})
	got := buf.String()
	if !strings.Contains(got, "approvalRefusal") || !strings.Contains(got, "denied") {
		t.Errorf("a refusal is indistinguishable from an ordinary tool failure: %s", got)
	}
}

func TestLogToolExecution_RefusalLevels(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refusal tool.ApprovalRefusal
		want    string
	}{
		{"a person said no", tool.RefusalDenied, "WARN"},
		{"nobody answered in time", tool.RefusalTimedOut, "WARN"},
		{"the prompt was withdrawn", tool.RefusalCancelled, "WARN"},
		{"the answer could not be read", tool.RefusalUnreadable, "WARN"},
		{"the run had already ended", tool.RefusalRunEnded, "WARN"},
		{"there was nowhere to ask", tool.RefusalNoApprovalRoute, "ERROR"},
		{"the prompt was never asked", tool.RefusalPromptFailed, "ERROR"},
		{"the gate could not run in an exit handler", tool.RefusalExitHandler, "ERROR"},
		{"the response status was not one we know", tool.RefusalUnexpected, "ERROR"},
		{"the backstop caught a caller that skipped its gate", tool.RefusalBackstop, "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, buf := testLoggerJSON()
			l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
				UserID: "u1", AgentName: "a1", ToolName: "danger", SessionID: "s1",
				Success: false, Error: tool.AuditToolErrorCode,
				ApprovalRefusal: string(tc.refusal),
			})
			if got := buf.String(); !strings.Contains(got, `"level":"`+tc.want+`"`) {
				t.Errorf("want level %s: %s", tc.want, got)
			}
		})
	}
}

func TestLogToolExecution_OrdinaryToolErrorStaysWarn(t *testing.T) {
	l, buf := testLoggerJSON()
	l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
		UserID: "u1", AgentName: "a1", ToolName: "danger", SessionID: "s1",
		Success: false, Error: tool.AuditToolErrorCode,
	})
	if got := buf.String(); !strings.Contains(got, `"level":"WARN"`) {
		t.Errorf("an ordinary tool error changed level: %s", got)
	}
}

func TestLogToolExecution_UnclassifiedRefusalIsLoud(t *testing.T) {
	l, buf := testLoggerJSON()
	l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
		UserID: "u1", AgentName: "a1", ToolName: "danger", SessionID: "s1",
		Success: false, Error: tool.AuditToolErrorCode,
		ApprovalRefusal: "a_cause_nobody_has_classified_yet",
	})
	if got := buf.String(); !strings.Contains(got, `"level":"ERROR"`) {
		t.Errorf("an unclassified refusal logged quietly: %s", got)
	}
}

func TestRefusalClassification_CoversEveryConstant(t *testing.T) {
	faults := map[tool.ApprovalRefusal]bool{
		tool.RefusalNoApprovalRoute: true,
		tool.RefusalExitHandler:     true,
		tool.RefusalPromptFailed:    true,
		tool.RefusalUnexpected:      true,
		tool.RefusalBackstop:        true,
	}
	decisions := map[tool.ApprovalRefusal]bool{
		tool.RefusalDenied:             true,
		tool.RefusalTimedOut:           true,
		tool.RefusalCancelled:          true,
		tool.RefusalUnreadable:         true,
		tool.RefusalRunEnded:           true,
		tool.RefusalRunEndedUnanswered: true,
		tool.RefusalNoHumanInEvalRun:   true,
	}

	for _, r := range tool.AllApprovalRefusals() {
		classifiedHere := faults[r] || decisions[r]
		if !classifiedHere {
			t.Errorf("refusal %q is not classified in this test; decide whether it is a decision (WARN) or a fault (ERROR) and say so in both places", r)
			continue
		}
		if got := decisionRefusals[string(r)]; got != decisions[r] {
			t.Errorf("refusal %q: production says decision=%v, this test says decision=%v — one of them is wrong about whether it pages an operator",
				r, got, decisions[r])
		}
	}
}

func TestLogToolExecution_ApprovalRecipientOnlyWhenItDiffers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		caller    string
		recipient string
		want      bool
	}{
		{"somebody else was asked", "caller@test", "approver@test", true},
		{"the caller was asked", "caller@test", "caller@test", false},
		{"nobody was asked", "caller@test", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, buf := testLoggerJSON()
			l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
				UserID: tc.caller, AgentName: "a1", ToolName: "danger", SessionID: "s1",
				Success: false, Error: tool.AuditToolErrorCode,
				ApprovalRefusal:   string(tool.RefusalRunEndedUnanswered),
				ApprovalRecipient: tc.recipient,
			})
			if got := strings.Contains(buf.String(), "approvalRecipient"); got != tc.want {
				t.Errorf("approvalRecipient present = %v, want %v: %s", got, tc.want, buf.String())
			}
		})
	}
}

func TestLogHILApproval_DenialsAreWarnedAndTheRestIsInfo(t *testing.T) {
	cases := map[string]string{
		auth.HILApprovalAsked:         "INFO",
		auth.HILApprovalAccepted:      "INFO",
		auth.HILApprovalTimedOut:      "INFO",
		auth.HILApprovalWithdrawn:     "INFO",
		auth.HILApprovalRefused:       "WARN",
		auth.HILApprovalRequestDenied: "WARN",
		auth.HILApprovalUndeliverable: "WARN",
		auth.HILApprovalLate:          "WARN",
	}
	for outcome, level := range cases {
		t.Run(outcome, func(t *testing.T) {
			l, buf := testLoggerJSON()

			l.LogHILApproval(context.Background(), auth.HILApprovalEvent{RequestID: "r1", Outcome: outcome})

			if got := buf.String(); !strings.Contains(got, `"level":"`+level+`"`) {
				t.Errorf("%s logged as %s, want %s", outcome, got, level)
			}
		})
	}
}
