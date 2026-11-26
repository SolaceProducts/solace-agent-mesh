// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type stubAuthenticator struct {
	identity     *auth.Identity
	jwtTaskID    string
	authnCalls   []string
	peerCalls    []string
	rejectErr    error
	failNoBinder bool
}

func (s *stubAuthenticator) Authenticate(_ context.Context, token, reqTaskID string) (*auth.Identity, error) {
	s.authnCalls = append(s.authnCalls, reqTaskID)
	if token == "" {
		return nil, nil
	}
	if s.rejectErr != nil {
		return nil, s.rejectErr
	}
	if s.failNoBinder && s.jwtTaskID != reqTaskID {
		return nil, errors.New("authentication failed: task_id mismatch")
	}
	return s.cloneIdentity(token), nil
}

func (s *stubAuthenticator) AuthenticateWithoutTaskBinding(_ context.Context, token string) (*auth.Identity, error) {
	s.peerCalls = append(s.peerCalls, token)
	if token == "" {
		return nil, nil
	}
	if s.rejectErr != nil {
		return nil, s.rejectErr
	}
	return s.cloneIdentity(token), nil
}

func (s *stubAuthenticator) AuthenticateWithSoftTaskBinding(_ context.Context, token string, _ string) (*auth.Identity, error) {
	s.peerCalls = append(s.peerCalls, token)
	if token == "" {
		return nil, nil
	}
	if s.rejectErr != nil {
		return nil, s.rejectErr
	}
	return s.cloneIdentity(token), nil
}

func (s *stubAuthenticator) cloneIdentity(token string) *auth.Identity {
	if s.identity == nil {
		return nil
	}
	scopes := append([]string(nil), s.identity.Scopes...)
	roles := append([]string(nil), s.identity.Roles...)
	return &auth.Identity{
		UserID:    s.identity.UserID,
		Roles:     roles,
		Scopes:    scopes,
		Claims:    s.identity.Claims,
		AuthToken: token,
	}
}

var _ auth.Authenticator = (*stubAuthenticator)(nil)
var _ auth.PeerAuthenticator = (*stubAuthenticator)(nil)

func makeUserConfigWithCapabilities(scopes []string) string {
	b, _ := json.Marshal(map[string]any{"_enterprise_capabilities": scopes})
	return string(b)
}

func runSetupTaskCapturingState(t *testing.T, agent *Agent, taskID string, msg a2a.Message, up a2a.UserProperties) *TaskExecutionState {
	t.Helper()
	state, _, earlyResp, err := agent.setupTask(t.Context(), taskID, msg, up, taskDelivery{})
	if err != nil {
		t.Fatalf("setupTask err: %v", err)
	}
	if earlyResp != nil {
		t.Fatalf("setupTask returned earlyResp: %+v", earlyResp)
	}
	if state == nil {
		t.Fatal("setupTask returned nil state")
	}
	return state
}

func TestSetupTaskCapabilityInflationRejected(t *testing.T) {
	jwtScopes := []string{"tool:foo"}
	inflated := []string{"tool:foo", "agent:Stripe:invoke"}

	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	svc.authn = &stubAuthenticator{
		identity:  &auth.Identity{UserID: "u-1", Scopes: jwtScopes},
		jwtTaskID: "task-001",
	}

	state := runSetupTaskCapturingState(t, agent, "task-001", newUserMessage("hi"), a2a.UserProperties{
		ClientID:      "gw",
		UserID:        "u-1",
		AuthToken:     "fake-jwt",
		A2AUserConfig: makeUserConfigWithCapabilities(inflated),
	})

	if state.Identity == nil {
		t.Fatal("expected non-nil Identity after authenticated request")
	}
	if !reflect.DeepEqual(state.Identity.Scopes, jwtScopes) {
		t.Errorf("identity.Scopes = %v, want %v (must NOT be inflated by unsigned body)",
			state.Identity.Scopes, jwtScopes)
	}
}

func TestSetupTaskJWTOnlyScopes(t *testing.T) {
	jwtScopes := []string{"tool:foo", "tool:bar"}

	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	svc.authn = &stubAuthenticator{
		identity:  &auth.Identity{UserID: "u-1", Scopes: jwtScopes},
		jwtTaskID: "task-002",
	}

	state := runSetupTaskCapturingState(t, agent, "task-002", newUserMessage("hi"), a2a.UserProperties{
		ClientID:  "gw",
		UserID:    "u-1",
		AuthToken: "fake-jwt",
	})

	if !reflect.DeepEqual(state.Identity.Scopes, jwtScopes) {
		t.Errorf("identity.Scopes = %v, want %v", state.Identity.Scopes, jwtScopes)
	}
}

func TestSetupTaskBodyMatchesJWT_NoRegression(t *testing.T) {
	jwtScopes := []string{"tool:foo", "tool:bar"}

	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	svc.authn = &stubAuthenticator{
		identity:  &auth.Identity{UserID: "u-1", Scopes: jwtScopes},
		jwtTaskID: "task-003",
	}

	state := runSetupTaskCapturingState(t, agent, "task-003", newUserMessage("hi"), a2a.UserProperties{
		ClientID:      "gw",
		UserID:        "u-1",
		AuthToken:     "fake-jwt",
		A2AUserConfig: makeUserConfigWithCapabilities(jwtScopes),
	})

	if !reflect.DeepEqual(state.Identity.Scopes, jwtScopes) {
		t.Errorf("identity.Scopes = %v, want %v", state.Identity.Scopes, jwtScopes)
	}
}

func TestSetupTaskEvalWildcardDroppedAgainstBoundedJWT(t *testing.T) {
	jwtScopes := []string{"agent:*:invoke", "tool:*:*"}

	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	svc.authn = &stubAuthenticator{
		identity:  &auth.Identity{UserID: "system:eval", Scopes: jwtScopes},
		jwtTaskID: "eval-task-001",
	}

	state := runSetupTaskCapturingState(t, agent, "eval-task-001", newUserMessage("hi"), a2a.UserProperties{
		ClientID:      "evals",
		UserID:        "system:eval",
		AuthToken:     "fake-jwt",
		A2AUserConfig: makeUserConfigWithCapabilities([]string{"*"}),
	})

	if !reflect.DeepEqual(state.Identity.Scopes, jwtScopes) {
		t.Errorf("identity.Scopes = %v, want %v (bare \"*\" body must NOT widen bounded JWT scopes)",
			state.Identity.Scopes, jwtScopes)
	}
}

func TestSetupTaskTamperedJWTRejected(t *testing.T) {
	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	svc.authn = &stubAuthenticator{
		identity:  &auth.Identity{UserID: "u-1", Scopes: []string{"tool:foo"}},
		rejectErr: errors.New("signature invalid"),
	}

	_, _, earlyResp, err := agent.setupTask(t.Context(), "task-004", newUserMessage("hi"), a2a.UserProperties{
		ClientID:  "gw",
		UserID:    "u-1",
		AuthToken: "tampered-jwt",
	}, taskDelivery{})
	if err != nil {
		t.Fatalf("setupTask returned err: %v (want early response, no err)", err)
	}
	if earlyResp == nil || earlyResp.Error == nil {
		t.Fatalf("expected early Authentication failed response, got %+v", earlyResp)
	}
}

type recordingAuditLog struct {
	auth []auth.AuthEvent
}

func (r *recordingAuditLog) LogAuth(_ context.Context, e auth.AuthEvent)                         { r.auth = append(r.auth, e) }
func (r *recordingAuditLog) LogToolAccess(_ context.Context, _ auth.ToolAccessEvent)             {}
func (r *recordingAuditLog) LogAgentAccess(_ context.Context, _ auth.AgentAccessEvent)           {}
func (r *recordingAuditLog) LogToolExecution(_ context.Context, _ auth.ToolExecutionEvent)       {}
func (r *recordingAuditLog) LogControlAccess(_ context.Context, _ auth.ControlAccessEvent)       {}
func (r *recordingAuditLog) LogShareGrant(_ context.Context, _ auth.ShareGrantEvent)             {}
func (r *recordingAuditLog) LogShareLevelChange(_ context.Context, _ auth.ShareLevelChangeEvent) {}
func (r *recordingAuditLog) LogShareRevoke(_ context.Context, _ auth.ShareRevokeEvent)           {}
func (r *recordingAuditLog) LogTemplateDeleted(_ context.Context, _ auth.TemplateDeletedEvent)   {}
func (r *recordingAuditLog) LogActivationChange(_ context.Context, _ auth.ActivationChangeEvent) {}
func (r *recordingAuditLog) LogConsentChange(_ context.Context, _ auth.ConsentChangeEvent)       {}

func TestSetupTaskEmptyTokenInEnterpriseRejectsAndAudits(t *testing.T) {
	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	svc.authn = &stubAuthenticator{
		identity:  &auth.Identity{UserID: "u-1"},
		jwtTaskID: "task-040",
	}
	rec := &recordingAuditLog{}
	svc.auditLog = rec

	_, _, earlyResp, err := agent.setupTask(t.Context(), "task-040", newUserMessage("hi"), a2a.UserProperties{
		ClientID: "gw",
		UserID:   "u-1",
	}, taskDelivery{})
	if err != nil {
		t.Fatalf("setupTask returned err: %v (want early response)", err)
	}
	if earlyResp == nil || earlyResp.Error == nil {
		t.Fatalf("expected early Authentication failed response, got %+v", earlyResp)
	}

	if len(rec.auth) != 1 {
		t.Fatalf("expected 1 LogAuth event, got %d (%+v)", len(rec.auth), rec.auth)
	}
	ev := rec.auth[0]
	if ev.Success {
		t.Error("expected failure auth event, got success")
	}
	if ev.Reason != "missing auth token" {
		t.Errorf("audit reason = %q, want %q", ev.Reason, "missing auth token")
	}
	if ev.TaskID != "task-040" {
		t.Errorf("audit task_id = %q, want %q", ev.TaskID, "task-040")
	}
}

func TestSetupTaskEmptyTokenInCommunityIsBenign(t *testing.T) {
	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	rec := &recordingAuditLog{}
	svc.auditLog = rec

	state, _, earlyResp, err := agent.setupTask(t.Context(), "task-041", newUserMessage("hi"), a2a.UserProperties{
		ClientID: "gw",
		UserID:   "u-1",
	}, taskDelivery{})
	if err != nil {
		t.Fatalf("setupTask err: %v", err)
	}
	if earlyResp != nil {
		t.Fatalf("community-mode empty token must not produce an early response, got %+v", earlyResp)
	}
	if state == nil {
		t.Fatal("expected non-nil state for community-mode setup")
	}
	if len(rec.auth) != 0 {
		t.Errorf("expected 0 LogAuth events when auth not configured, got %d (%+v)", len(rec.auth), rec.auth)
	}
}

func TestSetupTaskGatewayCallEnforcesTaskBinding(t *testing.T) {
	stub := &stubAuthenticator{
		identity:  &auth.Identity{UserID: "u-1", Scopes: []string{"tool:foo"}},
		jwtTaskID: "task-005",
	}
	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	svc.authn = stub

	runSetupTaskCapturingState(t, agent, "task-005", newUserMessage("hi"), a2a.UserProperties{
		ClientID:  "gw",
		UserID:    "u-1",
		AuthToken: "fake-jwt",
	})

	if len(stub.authnCalls) != 1 || stub.authnCalls[0] != "task-005" {
		t.Errorf("expected one Authenticate call with task-005, got %v", stub.authnCalls)
	}
	if len(stub.peerCalls) != 0 {
		t.Errorf("gateway call must NOT use the peer authentication path; got %v",
			stub.peerCalls)
	}
}

func TestSetupTaskPeerCallUsesRelaxedAuth(t *testing.T) {
	stub := &stubAuthenticator{
		identity:  &auth.Identity{UserID: "u-1", Scopes: []string{"tool:foo"}},
		jwtTaskID: "original-gateway-task",
	}
	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	svc.authn = stub

	runSetupTaskCapturingState(t, agent, "sub-task-1", newUserMessage("hi"), a2a.UserProperties{
		ClientID:            "gw",
		UserID:              "u-1",
		AuthToken:           "fake-jwt",
		DelegatingAgentName: "agent-A",
	})

	if len(stub.peerCalls) != 1 {
		t.Errorf("expected exactly one peer authentication call, got %d (%v)",
			len(stub.peerCalls), stub.peerCalls)
	}
	if len(stub.authnCalls) != 0 {
		t.Errorf("peer call must NOT use strict Authenticate path; got %v", stub.authnCalls)
	}
}
