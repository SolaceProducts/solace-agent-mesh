// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type fakeAuthn struct {
	identity *auth.Identity
	err      error
}

func (f *fakeAuthn) Authenticate(_ context.Context, _ string, _ string) (*auth.Identity, error) {
	return f.identity, f.err
}

type recordingAudit struct {
	auth []auth.AuthEvent
}

func (r *recordingAudit) LogAuth(_ context.Context, e auth.AuthEvent)                         { r.auth = append(r.auth, e) }
func (r *recordingAudit) LogToolAccess(_ context.Context, _ auth.ToolAccessEvent)             {}
func (r *recordingAudit) LogAgentAccess(_ context.Context, _ auth.AgentAccessEvent)           {}
func (r *recordingAudit) LogToolExecution(_ context.Context, _ auth.ToolExecutionEvent)       {}
func (r *recordingAudit) LogControlAccess(_ context.Context, _ auth.ControlAccessEvent)       {}
func (r *recordingAudit) LogShareGrant(_ context.Context, _ auth.ShareGrantEvent)             {}
func (r *recordingAudit) LogShareLevelChange(_ context.Context, _ auth.ShareLevelChangeEvent) {}
func (r *recordingAudit) LogShareRevoke(_ context.Context, _ auth.ShareRevokeEvent)           {}
func (r *recordingAudit) LogTemplateDeleted(_ context.Context, _ auth.TemplateDeletedEvent)   {}
func (r *recordingAudit) LogActivationChange(_ context.Context, _ auth.ActivationChangeEvent) {}
func (r *recordingAudit) LogConsentChange(_ context.Context, _ auth.ConsentChangeEvent)       {}

func newWorkflowInstanceForAuth(svc *stubServices) *WorkflowInstance {
	return &WorkflowInstance{
		cfg:    &WorkflowConfig{Name: "wf-test"},
		svc:    svc,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func TestAuthorizeWorkflowInvocation(t *testing.T) {
	logger := slog.Default()
	newInstance := func(authz auth.AgentAuthorizer) *WorkflowInstance {
		return &WorkflowInstance{
			cfg:    &WorkflowConfig{Name: "workflow_wf1"},
			svc:    &stubServices{agentAuth: authz},
			logger: logger,
		}
	}
	rbacAuthz := rbac.NewAgentAuth(nil, logger)

	t.Run("allowed with invoke scope", func(t *testing.T) {
		w := newInstance(rbacAuthz)
		id := &auth.Identity{UserID: "alice", Scopes: []string{"workflow:workflow_wf1:invoke"}}
		if resp := w.authorizeWorkflowInvocation(t.Context(), id, "task-1", false); resp != nil {
			t.Fatalf("expected allow, got error response %+v", resp)
		}
	})

	t.Run("denied with only agent delegate", func(t *testing.T) {
		w := newInstance(rbacAuthz)
		id := &auth.Identity{UserID: "alice", Scopes: []string{"agent:*:delegate"}}
		resp := w.authorizeWorkflowInvocation(t.Context(), id, "task-2", false)
		if resp == nil || resp.Error == nil {
			t.Fatalf("expected deny error response, got %+v", resp)
		}
	})

	t.Run("community noop allows nil identity", func(t *testing.T) {
		w := newInstance(auth.NoopAgentAuthorizer{})
		if resp := w.authorizeWorkflowInvocation(t.Context(), nil, "task-3", false); resp != nil {
			t.Fatalf("community/auth-off should allow, got %+v", resp)
		}
	})

	t.Run("peer-delegated call short-circuits to allow", func(t *testing.T) {
		w := newInstance(rbacAuthz)
		id := &auth.Identity{UserID: "alice", Scopes: []string{"agent:*:delegate"}}
		if resp := w.authorizeWorkflowInvocation(t.Context(), id, "task-4", true); resp != nil {
			t.Fatalf("peer-delegated call should skip the gate and allow, got %+v", resp)
		}
	})
}

func TestAuthenticateRequest_CommunityModeNoOp(t *testing.T) {
	rec := &recordingAudit{}
	w := newWorkflowInstanceForAuth(&stubServices{audit: rec})

	id, resp := w.authenticateRequest(t.Context(), a2a.UserProperties{UserID: "u-1"}, "task-1")
	if id != nil {
		t.Errorf("expected nil identity when auth not configured, got %+v", id)
	}
	if resp != nil {
		t.Errorf("expected nil response when auth not configured, got %+v", resp)
	}
	if len(rec.auth) != 0 {
		t.Errorf("expected 0 audit events when auth not configured, got %d", len(rec.auth))
	}
}

func TestAuthenticateRequest_EmptyTokenInEnterpriseRejects(t *testing.T) {
	rec := &recordingAudit{}
	w := newWorkflowInstanceForAuth(&stubServices{
		authn: &fakeAuthn{identity: &auth.Identity{UserID: "u-1"}},
		audit: rec,
	})

	id, resp := w.authenticateRequest(t.Context(), a2a.UserProperties{UserID: "u-1"}, "task-2")
	if id != nil {
		t.Errorf("expected nil identity, got %+v", id)
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("expected JSON-RPC error response, got %+v", resp)
	}
	if len(rec.auth) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(rec.auth))
	}
	ev := rec.auth[0]
	if ev.Success {
		t.Error("expected failure auth event")
	}
	if ev.Reason != "missing auth token" {
		t.Errorf("audit reason = %q, want %q", ev.Reason, "missing auth token")
	}
}

func TestAuthenticateRequest_EnterpriseSuccessAudits(t *testing.T) {
	rec := &recordingAudit{}
	w := newWorkflowInstanceForAuth(&stubServices{
		authn: &fakeAuthn{identity: &auth.Identity{UserID: "alice"}},
		audit: rec,
	})

	id, resp := w.authenticateRequest(t.Context(),
		a2a.UserProperties{UserID: "alice", AuthToken: "valid-jwt"}, "task-3")
	if id == nil || id.UserID != "alice" {
		t.Errorf("expected identity for alice, got %+v", id)
	}
	if resp != nil {
		t.Errorf("expected nil response on success, got %+v", resp)
	}
	if len(rec.auth) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(rec.auth))
	}
	if !rec.auth[0].Success {
		t.Error("expected success audit event")
	}
	if rec.auth[0].UserID != "alice" {
		t.Errorf("audit user_id = %q, want alice", rec.auth[0].UserID)
	}
}

func TestAuthenticateRequest_AuthFailureAudits(t *testing.T) {
	rec := &recordingAudit{}
	w := newWorkflowInstanceForAuth(&stubServices{
		authn: &fakeAuthn{err: errors.New("signature invalid")},
		audit: rec,
	})

	id, resp := w.authenticateRequest(t.Context(),
		a2a.UserProperties{UserID: "u-1", AuthToken: "tampered-jwt"}, "task-4")
	if id != nil {
		t.Errorf("expected nil identity on auth failure, got %+v", id)
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("expected error response, got %+v", resp)
	}
	if len(rec.auth) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(rec.auth))
	}
	if rec.auth[0].Success {
		t.Error("expected failure audit event")
	}
	if rec.auth[0].Reason != "signature invalid" {
		t.Errorf("audit reason = %q, want %q", rec.auth[0].Reason, "signature invalid")
	}
}
