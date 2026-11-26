// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type toolAuditRecorder struct {
	auth.NoopAuditLogger
	mu     sync.Mutex
	events []auth.ToolExecutionEvent
}

func (r *toolAuditRecorder) LogToolExecution(_ context.Context, e auth.ToolExecutionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *toolAuditRecorder) only(tb testing.TB) auth.ToolExecutionEvent {
	tb.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) != 1 {
		tb.Fatalf("want exactly one tool-execution audit event, got %d (%+v)", len(r.events), r.events)
	}
	return r.events[0]
}

type gatedAgentHarness struct {
	*toolStatusHarness
	audit     *toolAuditRecorder
	ran       *bool
	tCtx      *tool.Context
	setOnExec func(func(*tool.Context))
}

func newGatedAgentHarness(t *testing.T, taskID string, canPrompt bool) *gatedAgentHarness {
	t.Helper()
	h := newToolStatusHarness(t, taskID)

	ran := false
	var onExec func(*tool.Context)
	h.registerTool(t, "web_request", func(_ context.Context, _ map[string]any, tc *tool.Context) (*tool.Result, error) {
		ran = true
		if onExec != nil {
			onExec(tc)
		}
		return tool.OK("fetched"), nil
	})
	h.agent.SetHILToolConfigs(map[string]HILToolConfig{
		"web_request": {RequireApproval: true, ShowArgs: true, Timeout: 30 * time.Second},
	})

	rec := &toolAuditRecorder{}
	h.agent.svc.(*mockServices).auditLog = rec

	tCtx := &tool.Context{
		AgentName:       h.agent.cfg.Name,
		UserID:          "user-1",
		SessionID:       "ctx-1",
		ArtifactService: &artifact.ScopedService{Inner: memory.New()},
	}
	if canPrompt {
		tCtx.GatewayCapabilities = map[string]bool{tool.CapInteractiveUserInput: true}
	}
	gh := &gatedAgentHarness{toolStatusHarness: h, audit: rec, ran: &ran, tCtx: tCtx}
	gh.setOnExec = func(f func(*tool.Context)) { onExec = f }
	return gh
}

func gatedCall() []llm.ToolCall {
	return []llm.ToolCall{{ID: "call-A", Name: "web_request", Arguments: `{"url":"https://api.example.test/v1"}`}}
}

func (h *gatedAgentHarness) answerWith(t *testing.T, status string, data map[string]any) {
	t.Helper()
	resCh := make(chan []*tool.Result, 1)
	go func() { resCh <- h.agent.dispatchToolCallsWithStatus(t.Context(), gatedCall(), h.tCtx, h.sp, "") }()

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	requestID, _ := signals[1]["request_id"].(string)
	if !strings.HasPrefix(requestID, "hil-approval-") {
		t.Fatalf("request_id = %q, want a hil-approval- id", requestID)
	}
	if h.agent.deliverHILResponse(&a2a.UserInputResponseData{
		Type: "user_input_response", RequestID: requestID, Status: status, Data: data,
	}, hilSenderUser(h.tCtx.UserID)) != hilDelivered {
		t.Fatal("deliverHILResponse found no pending request for the published id")
	}
	select {
	case <-resCh:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch did not return after the response was delivered")
	}
}

func TestAgentAudit_RecordsRefusalWhenNobodyCanBeAsked(t *testing.T) {
	h := newGatedAgentHarness(t, "task-hil-audit-noprompt", false)

	h.agent.dispatchToolCallsWithStatus(t.Context(), gatedCall(), h.tCtx, h.sp, "")

	if *h.ran {
		t.Fatal("a gated tool ran on a channel that cannot ask anyone")
	}
	ev := h.audit.only(t)
	if ev.ApprovalRefusal != string(tool.RefusalNoApprovalRoute) {
		t.Errorf("approvalRefusal = %q, want no_approval_route", ev.ApprovalRefusal)
	}
	if ev.Error != tool.AuditToolErrorCode {
		t.Errorf("error = %q, want %q", ev.Error, tool.AuditToolErrorCode)
	}
	if ev.Success {
		t.Error("a refused call recorded as a success")
	}
	if ev.ApprovalReason != "" {
		t.Errorf("approvalReason = %q on a call that never ran, want empty", ev.ApprovalReason)
	}
	if ev.ToolName != "web_request" || ev.UserID != "user-1" {
		t.Errorf("a refusal must be as attributable as a dispatch: %+v", ev)
	}
}

func TestAgentAudit_RecordsEachRefusalCause(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  string
		data    map[string]any
		refusal tool.ApprovalRefusal
	}{
		{"a person said no", "answered", map[string]any{"approved": false}, tool.RefusalDenied},
		{"the answer could not be read", "answered", map[string]any{"approved": "yes please"}, tool.RefusalUnreadable},
		{"the prompt was withdrawn", "cancelled", nil, tool.RefusalCancelled},
		{"the status is not one we know", "acknowledged", map[string]any{"approved": true}, tool.RefusalUnexpected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newGatedAgentHarness(t, "task-hil-audit-"+string(tc.refusal), true)
			h.answerWith(t, tc.status, tc.data)

			if *h.ran {
				t.Fatal("a refused call was dispatched")
			}
			ev := h.audit.only(t)
			if ev.ApprovalRefusal != string(tc.refusal) {
				t.Errorf("approvalRefusal = %q, want %q", ev.ApprovalRefusal, tc.refusal)
			}
			if ev.Success || ev.ApprovalReason != "" {
				t.Errorf("a refused call must record neither success nor a reason: %+v", ev)
			}
		})
	}
}

func TestAgentAudit_ApprovedDispatchRecordsHuman(t *testing.T) {
	h := newGatedAgentHarness(t, "task-hil-audit-approved", true)
	h.answerWith(t, "answered", map[string]any{"approved": true})

	if !*h.ran {
		t.Fatal("precondition: the approved call should have dispatched")
	}
	ev := h.audit.only(t)
	if ev.ApprovalReason != string(tool.ApprovalHuman) {
		t.Errorf("approvalReason = %q, want human — the record cannot say a person approved this", ev.ApprovalReason)
	}
	if !ev.Success {
		t.Errorf("success = false for a call that ran and returned ok (error %q)", ev.Error)
	}
	if ev.ApprovalRefusal != "" {
		t.Errorf("approvalRefusal = %q on a call that ran, want empty", ev.ApprovalRefusal)
	}
}

func TestAgentAudit_ToolCannotRewriteItsOwnAuthorisation(t *testing.T) {
	h := newGatedAgentHarness(t, "task-hil-audit-rewrite", true)
	h.setOnExec(func(tc *tool.Context) { tc.ApprovalReason = tool.ApprovalNone })

	h.answerWith(t, "answered", map[string]any{"approved": true})

	if got := h.audit.only(t).ApprovalReason; got != string(tool.ApprovalHuman) {
		t.Errorf("approvalReason = %q — the tool erased the record of how it came to run", got)
	}
}

func TestAgentAudit_BackstopRefusalReachesTheRecord(t *testing.T) {
	h := newGatedAgentHarness(t, "task-hil-audit-backstop", true)
	h.agent.cfg.HILToolConfigs = nil

	h.agent.dispatchToolCallsWithStatus(t.Context(), gatedCall(), h.tCtx, h.sp, "")

	if *h.ran {
		t.Fatal("the backstop let a gated call through")
	}
	ev := h.audit.only(t)
	if ev.ApprovalRefusal != string(tool.RefusalBackstop) {
		t.Errorf("approvalRefusal = %q, want backstop — the record cannot say a gate refused this, only that something failed", ev.ApprovalRefusal)
	}
	if ev.Success {
		t.Error("a refused call recorded as a success")
	}
}

func TestAgentAudit_ToolRunUnderAKeyNamesTheKey(t *testing.T) {
	h := newGatedAgentHarness(t, "task-hil-audit-key", true)
	h.tCtx.UserID = "someone-else"
	h.tCtx.Identity = &auth.Identity{UserID: "system:ci-deployer", AuthMethod: "api_key", CredentialID: "key-1"}
	h.answerWith(t, "answered", map[string]any{"approved": true})

	ev := h.audit.only(t)
	if ev.CredentialID != "key-1" {
		t.Errorf("credentialID = %q, want key-1", ev.CredentialID)
	}
	if ev.UserID != "system:ci-deployer" {
		t.Errorf("userID = %q, want the verified system:ci-deployer, not the broker user property", ev.UserID)
	}
}

func TestAgentAudit_RefusalUnderAKeyNamesTheKey(t *testing.T) {
	h := newGatedAgentHarness(t, "task-hil-audit-key-refused", false)
	h.tCtx.UserID = "someone-else"
	h.tCtx.Identity = &auth.Identity{UserID: "system:ci-deployer", AuthMethod: "api_key", CredentialID: "key-1"}

	h.agent.dispatchToolCallsWithStatus(t.Context(), gatedCall(), h.tCtx, h.sp, "")

	ev := h.audit.only(t)
	if ev.CredentialID != "key-1" || ev.UserID != "system:ci-deployer" {
		t.Errorf("record = %q/%q, want system:ci-deployer/key-1, both from the verified identity", ev.UserID, ev.CredentialID)
	}
}
