// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestPreAuthShortCircuit(t *testing.T) {
	if r := PreAuthShortCircuit(&tool.Context{UserID: rbac.DefaultSystemUserSub}, "jira", nil); r == nil {
		t.Error("system-user principal should short-circuit auth")
	} else if r.Status != tool.StatusError || IsPendingAuthResult(r) {
		t.Errorf("short-circuit result should be terminal error, got status=%v pending=%v", r.Status, IsPendingAuthResult(r))
	}

	normal := &tool.Context{UserID: "alice@example.com", Identity: &auth.Identity{Roles: []string{"sam_user"}}}
	if PreAuthShortCircuit(normal, "jira", nil) != nil {
		t.Error("a regular user must not be short-circuited")
	}
	if PreAuthShortCircuit(nil, "jira", nil) != nil {
		t.Error("nil toolCtx must not short-circuit")
	}
}

func TestPreAuthShortCircuit_EvalExemption(t *testing.T) {
	evalCtx := &tool.Context{UserID: rbac.EvalSystemUserSub, Trigger: a2a.TriggerEval}
	if r := PreAuthShortCircuit(evalCtx, "jira", nil); r != nil {
		t.Errorf("eval-triggered system-user task must fall through to the normal flow, got %v", r)
	}
	if r := PreAuthShortCircuit(&tool.Context{UserID: "system:qa-bot", Trigger: a2a.TriggerEval}, "jira", nil); r != nil {
		t.Errorf("eval-triggered custom run_as principal must fall through, got %v", r)
	}
	for _, trigger := range []string{"", a2a.TriggerInteractive, a2a.TriggerScheduled, a2a.TriggerUnknown} {
		entrypoint := &tool.Context{UserID: rbac.EvalSystemUserSub, Trigger: trigger}
		if r := PreAuthShortCircuit(entrypoint, "jira", nil); r == nil {
			t.Errorf("trigger %q under a system principal must keep the terminal no-delegation result", trigger)
		}
	}
}

func TestNewAuthGaveUpResult(t *testing.T) {
	r := NewAuthGaveUpResult("salesforce_schema", "")
	if r == nil {
		t.Fatal("result is nil")
	}
	if r.Status != tool.StatusError {
		t.Errorf("Status = %q, want %q (must be terminal, not pending)", r.Status, tool.StatusError)
	}
	if IsPendingAuthResult(r) {
		t.Error("give-up result must NOT be classified as pending-auth")
	}
	if !strings.Contains(r.Message, "salesforce_schema") {
		t.Errorf("message %q should name the tool", r.Message)
	}

	r2 := NewAuthGaveUpResult("jira", "missing scope offline_access")
	if !strings.Contains(r2.Message, "missing scope offline_access") {
		t.Errorf("message %q should carry the underlying tool message", r2.Message)
	}
}

func TestNewPendingAuthResult_RoundTrip(t *testing.T) {
	args := map[string]any{"q": "hello", "n": 42}
	r := NewPendingAuthResult("call-42", args, "")

	if r == nil {
		t.Fatal("result is nil")
	}
	if r.Status != tool.StatusPending {
		t.Errorf("Status = %q, want %q", r.Status, tool.StatusPending)
	}
	if r.Message != "Pending user authorization." {
		t.Errorf("Message = %q, want %q", r.Message, "Pending user authorization.")
	}
	if !IsPendingAuthResult(r) {
		t.Error("IsPendingAuthResult = false, want true")
	}
	if got := PendingAuthFunctionCallID(r); got != "call-42" {
		t.Errorf("FunctionCallID = %q, want %q", got, "call-42")
	}
	gotArgs := PendingAuthOriginalArgs(r)
	if gotArgs["q"] != "hello" || gotArgs["n"] != 42 {
		t.Errorf("OriginalArgs = %+v, want %+v", gotArgs, args)
	}
}

func TestNewPendingAuthResult_NilArgs(t *testing.T) {
	r := NewPendingAuthResult("call-1", nil, "")
	if !IsPendingAuthResult(r) {
		t.Fatal("IsPendingAuthResult = false")
	}
	if got := PendingAuthOriginalArgs(r); got == nil || len(got) != 0 {
		t.Errorf("OriginalArgs for nil input = %+v, want empty map", got)
	}
}

func TestIsPendingAuthResult_Negative(t *testing.T) {
	cases := []struct {
		name string
		r    *tool.Result
	}{
		{"nil", nil},
		{"plain error", tool.Error("boom")},
		{"success", tool.OK("done")},
		{"pending but not auth", &tool.Result{Status: tool.StatusPending, Data: map[string]any{"sub_task_id": "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if IsPendingAuthResult(tc.r) {
				t.Errorf("IsPendingAuthResult = true, want false")
			}
			if got := PendingAuthFunctionCallID(tc.r); got != "" {
				t.Errorf("FunctionCallID = %q, want empty", got)
			}
		})
	}
}

func TestNewPendingAuthResult_NoSubTaskID(t *testing.T) {
	r := NewPendingAuthResult("call-1", nil, "")
	if _, ok := r.Data["sub_task_id"]; ok {
		t.Error("pending-auth result must not set sub_task_id")
	}
	if r.OnDelivery != nil {
		t.Error("pending-auth result must have nil OnDelivery")
	}
}

func TestPendingAuthResult_CarriesCredentialKeyAndFollowerFlag(t *testing.T) {
	leader := NewPendingAuthResult("call-1", map[string]any{"q": "x"}, "cred-key")
	if got := PendingAuthCredentialKey(leader); got != "cred-key" {
		t.Errorf("leader credentialKey = %q, want cred-key", got)
	}
	if IsPendingAuthFollower(leader) {
		t.Error("leader must not be flagged as follower")
	}

	follower := NewPendingAuthFollowerResult("call-2", map[string]any{"q": "y"}, "cred-key")
	if !IsPendingAuthFollower(follower) {
		t.Error("follower must be flagged as follower")
	}
	if got := PendingAuthCredentialKey(follower); got != "cred-key" {
		t.Errorf("follower credentialKey = %q, want cred-key", got)
	}
	if !IsPendingAuthResult(follower) {
		t.Error("follower must still classify as a pending-auth result")
	}
}

func TestAuthFlowClassifyOnly(t *testing.T) {
	if !AuthFlowClassifyOnly(rbac.DefaultSystemUserSub, a2a.TriggerEval) {
		t.Error("eval on a system user must be classify-only, not a real auth flow")
	}
	if !AuthFlowClassifyOnly("system:qa-bot", a2a.TriggerEval) {
		t.Error("eval on a custom run_as system user must be classify-only")
	}
	if AuthFlowClassifyOnly("alice@example.com", a2a.TriggerEval) {
		t.Error("a human principal must keep the real auth flow")
	}
	for _, trigger := range []string{"", a2a.TriggerInteractive, a2a.TriggerScheduled} {
		if AuthFlowClassifyOnly(rbac.DefaultSystemUserSub, trigger) {
			t.Errorf("trigger %q must be refused by DelegationRefused, not classified", trigger)
		}
		if !DelegationRefused(rbac.DefaultSystemUserSub, trigger) {
			t.Errorf("trigger %q on a system user must be refused outright", trigger)
		}
	}
}
