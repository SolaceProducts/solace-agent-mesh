// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func TestToolAuthAuthorizeTool(t *testing.T) {
	logger := slog.Default()
	ta := NewToolAuth(nil, logger)

	tests := []struct {
		name     string
		scopes   []string
		required []string
		wantErr  bool
	}{
		{"wildcard allows all", []string{"*"}, []string{"tool:file:read"}, false},
		{"exact match", []string{"tool:file:read"}, []string{"tool:file:read"}, false},
		{"prefix wildcard", []string{"tool:*:*"}, []string{"tool:file:read"}, false},
		{"no required scopes", []string{"tool:file:read"}, nil, false},
		{"missing scope", []string{"tool:file:read"}, []string{"tool:file:update"}, true},
		{"partial match", []string{"tool:file:read"}, []string{"tool:file:read", "tool:file:update"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := &auth.Identity{UserID: "test-user", Scopes: tt.scopes}
			err := ta.AuthorizeTool(context.Background(), identity, "test-tool", tt.required)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestToolAuthAuthorizeToolNilIdentity(t *testing.T) {
	ta := NewToolAuth(nil, slog.Default())
	err := ta.AuthorizeTool(context.Background(), nil, "tool", nil)
	if err == nil {
		t.Error("expected error for nil identity")
	}
}

func TestToolAuthFilterTools(t *testing.T) {
	ta := NewToolAuth(nil, slog.Default())

	identity := &auth.Identity{
		UserID: "test-user",
		Scopes: []string{"tool:file:read", "tool:artifact:*"},
	}

	tools := []auth.ToolInfo{
		{Name: "file-reader", RequiredScopes: []string{"tool:file:read"}},
		{Name: "file-writer", RequiredScopes: []string{"tool:file:update"}},
		{Name: "artifact-creator", RequiredScopes: []string{"tool:artifact:create"}},
		{Name: "admin-tool", RequiredScopes: []string{"admin:*"}},
		{Name: "no-scopes", RequiredScopes: nil},
	}

	got := ta.FilterTools(context.Background(), identity, tools)

	wantNames := map[string]bool{
		"file-reader":      true,
		"artifact-creator": true,
		"no-scopes":        true,
	}
	if len(got) != len(wantNames) {
		t.Fatalf("expected %d tools, got %d: %v", len(wantNames), len(got), got)
	}
	for _, tool := range got {
		if !wantNames[tool.Name] {
			t.Errorf("unexpected tool %q in filtered list", tool.Name)
		}
	}
}

func TestToolAuthFilterToolsNilIdentity(t *testing.T) {
	ta := NewToolAuth(nil, slog.Default())
	got := ta.FilterTools(context.Background(), nil, []auth.ToolInfo{{Name: "test"}})
	if got != nil {
		t.Errorf("expected nil for nil identity, got %v", got)
	}
}

func TestAgentAuthAuthorizeAgent(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())

	const wfCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const agentCard = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const dashedUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"

	tests := []struct {
		name      string
		scopes    []string
		agentName string
		wantErr   bool
	}{
		{"wildcard allows all", []string{"*"}, "my-agent", false},
		{"exact scope match", []string{"agent:my-agent:invoke"}, "my-agent", false},
		{"wildcard agent name", []string{"agent:*:invoke"}, "any-agent", false},
		{"wrong agent", []string{"agent:other-agent:invoke"}, "my-agent", true},
		{"wrong action", []string{"agent:my-agent:read"}, "my-agent", true},
		{"no scopes", nil, "my-agent", true},
		{"workflow card exact dashed-uuid scope", []string{"workflow:" + dashedUUID + ":invoke"}, wfCard, false},
		{"workflow card workflow wildcard", []string{"workflow:*:invoke"}, wfCard, false},
		{"workflow card denied to agent wildcard", []string{"agent:*:invoke"}, wfCard, true},
		{"workflow card denied to underscored name scope", []string{"agent:" + wfCard + ":invoke"}, wfCard, true},
		{"agent card exact dashed-uuid scope", []string{"agent:" + dashedUUID + ":invoke"}, agentCard, false},
		{"agent card agent wildcard", []string{"agent:*:invoke"}, agentCard, false},
		{"agent card denied to underscored name scope", []string{"agent:" + agentCard + ":invoke"}, agentCard, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := &auth.Identity{UserID: "test-user", Scopes: tt.scopes}
			err := aa.AuthorizeAgent(context.Background(), identity, tt.agentName)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestAgentAuthAuthorizeAgentNilIdentity(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())
	err := aa.AuthorizeAgent(context.Background(), nil, "agent")
	if err == nil {
		t.Error("expected error for nil identity")
	}
}

func TestAgentAuthFilterAgents(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())
	names := []string{"research", "support", "billing"}

	tests := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{"exact subset", []string{"agent:research:invoke"}, []string{"research"}},
		{"two of three", []string{"agent:research:invoke", "agent:billing:invoke"}, []string{"research", "billing"}},
		{"wildcard agent name allows all", []string{"agent:*:invoke"}, []string{"research", "support", "billing"}},
		{"global wildcard allows all", []string{"*"}, []string{"research", "support", "billing"}},
		{"wrong action grants none", []string{"agent:research:read"}, nil},
		{"no scopes grants none", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := &auth.Identity{UserID: "u", Scopes: tt.scopes}
			got := aa.FilterAgents(context.Background(), identity, names)
			assertNameSet(t, got, tt.want)
		})
	}
}

func TestAgentAuthFilterAgentsInstanceScopes(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())

	const wfCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const agentCard = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const dashedUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"
	names := []string{wfCard, agentCard, "plain"}

	tests := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{"agent wildcard hides workflow card", []string{"agent:*:invoke"}, []string{agentCard, "plain"}},
		{"workflow wildcard shows only workflow card", []string{"workflow:*:invoke"}, []string{wfCard}},
		{"both wildcards show everything", []string{"agent:*:invoke", "workflow:*:invoke"}, []string{wfCard, agentCard, "plain"}},
		{"exact dashed-uuid grants", []string{"workflow:" + dashedUUID + ":invoke", "agent:" + dashedUUID + ":invoke"}, []string{wfCard, agentCard}},
		{"plain name scope shows only plain", []string{"agent:plain:invoke"}, []string{"plain"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := &auth.Identity{UserID: "u", Scopes: tt.scopes}
			got := aa.FilterAgents(context.Background(), identity, names)
			assertNameSet(t, got, tt.want)
		})
	}
}

func assertNameSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}
	for _, w := range want {
		if !gotSet[w] {
			t.Errorf("missing %q in %v", w, got)
		}
	}
}

func TestAgentAuthFilterAgentsNilIdentity(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())
	if got := aa.FilterAgents(context.Background(), nil, []string{"research"}); got != nil {
		t.Errorf("expected nil for nil identity, got %v", got)
	}
}

func TestAgentAuthAuthorizeAgentScopes(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())

	tests := []struct {
		name           string
		scopes         []string
		requiredScopes []string
		wantErr        bool
	}{
		{"empty required allows all", []string{"anything"}, nil, false},
		{"user scope match", []string{"agent:alice__*:invoke"}, []string{"agent:alice__my_agent:invoke"}, false},
		{"user scope wrong user", []string{"agent:bob__*:invoke"}, []string{"agent:alice__my_agent:invoke"}, true},
		{"exact scope match", []string{"agent:alice__my_agent:invoke"}, []string{"agent:alice__my_agent:invoke"}, false},
		{"wildcard grants all", []string{"*"}, []string{"agent:alice__my_agent:invoke"}, false},
		{"no scopes denied", nil, []string{"agent:alice__my_agent:invoke"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := &auth.Identity{UserID: "test-user", Scopes: tt.scopes}
			err := aa.AuthorizeAgentScopes(context.Background(), identity, tt.requiredScopes)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestAgentAuthAuthorizeAgentScopesNilIdentity(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())
	err := aa.AuthorizeAgentScopes(context.Background(), nil, []string{"agent:alice__test:invoke"})
	if err == nil {
		t.Error("expected error for nil identity")
	}
}

func TestAgentAuthAuthorizeWorkflow(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())

	tests := []struct {
		name         string
		scopes       []string
		workflowName string
		wantErr      bool
	}{
		{"wildcard allows all", []string{"*"}, "workflow_abc", false},
		{"exact scope match", []string{"workflow:workflow_abc:invoke"}, "workflow_abc", false},
		{"wildcard workflow name", []string{"workflow:*:invoke"}, "workflow_any", false},
		{"wrong workflow", []string{"workflow:workflow_other:invoke"}, "workflow_abc", true},
		{"wrong action", []string{"workflow:workflow_abc:delegate"}, "workflow_abc", true},
		{"no scopes", nil, "workflow_abc", true},
		{"agent invoke does not grant workflow", []string{"agent:workflow_abc:invoke"}, "workflow_abc", true},
		{"agent wildcard does not grant workflow", []string{"agent:*:invoke"}, "workflow_abc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := &auth.Identity{UserID: "test-user", Scopes: tt.scopes}
			err := aa.AuthorizeWorkflow(context.Background(), identity, tt.workflowName)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestAgentAuthAuthorizeWorkflowNilIdentity(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())
	err := aa.AuthorizeWorkflow(context.Background(), nil, "workflow_abc")
	if err == nil {
		t.Error("expected error for nil identity")
	}
}

func TestAgentAuthWorkflowScopeIsolation(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())
	ctx := context.Background()

	workflowOnly := &auth.Identity{UserID: "u", Scopes: []string{"workflow:*:invoke"}}
	if err := aa.AuthorizeAgent(ctx, workflowOnly, "some_agent"); err == nil {
		t.Error("workflow:*:invoke must NOT grant agent delegation")
	}
	if err := aa.AuthorizeWorkflow(ctx, workflowOnly, "workflow_abc"); err != nil {
		t.Errorf("workflow:*:invoke should grant workflow invocation: %v", err)
	}

	agentOnly := &auth.Identity{UserID: "u", Scopes: []string{"agent:*:invoke"}}
	if err := aa.AuthorizeWorkflow(ctx, agentOnly, "workflow_abc"); err == nil {
		t.Error("agent:*:invoke must NOT grant workflow invocation")
	}
	if err := aa.AuthorizeAgent(ctx, agentOnly, "some_agent"); err != nil {
		t.Errorf("agent:*:invoke should grant agent delegation: %v", err)
	}
}

func TestAgentAuthFilterWorkflows(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())
	names := []string{"workflow_a", "workflow_b", "workflow_c"}

	tests := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{"exact subset", []string{"workflow:workflow_a:invoke"}, []string{"workflow_a"}},
		{"wildcard invoke allows all", []string{"workflow:*:invoke"}, []string{"workflow_a", "workflow_b", "workflow_c"}},
		{"global wildcard allows all", []string{"*"}, []string{"workflow_a", "workflow_b", "workflow_c"}},
		{"agent invoke grants none", []string{"agent:*:invoke"}, nil},
		{"no scopes grants none", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := &auth.Identity{UserID: "u", Scopes: tt.scopes}
			got := aa.FilterWorkflows(context.Background(), identity, names)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			gotSet := map[string]bool{}
			for _, n := range got {
				gotSet[n] = true
			}
			for _, w := range tt.want {
				if !gotSet[w] {
					t.Errorf("missing %q in %v", w, got)
				}
			}
		})
	}
}

func TestAgentAuthFilterWorkflowsNilIdentity(t *testing.T) {
	aa := NewAgentAuth(nil, slog.Default())
	if got := aa.FilterWorkflows(context.Background(), nil, []string{"workflow_a"}); got != nil {
		t.Errorf("expected nil for nil identity, got %v", got)
	}
}

func TestAgentAuthAuthorizeAgent_NilIdentity_EmitsAudit(t *testing.T) {
	rec := &recordingAuditLogger{}
	aa := NewAgentAuth(rec, slog.Default())

	err := aa.AuthorizeAgent(context.Background(), nil, "TestAgent")
	if err == nil {
		t.Fatal("expected error for nil identity")
	}
	if !strings.Contains(err.Error(), "no identity provided") {
		t.Errorf("err = %q, want contains 'no identity provided'", err.Error())
	}

	if len(rec.agentAccessEvents) != 1 {
		t.Fatalf("LogAgentAccess called %d times, want 1", len(rec.agentAccessEvents))
	}
	ev := rec.agentAccessEvents[0]
	if ev.UserID != "" {
		t.Errorf("UserID=%q, want \"\"", ev.UserID)
	}
	if ev.AgentName != "TestAgent" {
		t.Errorf("AgentName=%q, want TestAgent", ev.AgentName)
	}
	if ev.Allowed {
		t.Error("Allowed=true, want false")
	}
}

func TestAgentAuthAuthorizeAgentScopes_Deny_EmitsAudit(t *testing.T) {
	rec := &recordingAuditLogger{}
	aa := NewAgentAuth(rec, slog.Default())

	identity := &auth.Identity{UserID: "alice", Scopes: []string{"agent:bob__*:invoke"}}
	err := aa.AuthorizeAgentScopes(context.Background(), identity, []string{"agent:alice__my_agent:invoke"})
	if err == nil {
		t.Fatal("expected error for insufficient scopes")
	}

	if len(rec.agentAccessEvents) != 1 {
		t.Fatalf("LogAgentAccess called %d times, want 1", len(rec.agentAccessEvents))
	}
	ev := rec.agentAccessEvents[0]
	if ev.UserID != "alice" {
		t.Errorf("UserID=%q, want alice", ev.UserID)
	}
	if ev.AgentName != "" {
		t.Errorf("AgentName=%q, want \"\" (scope check is not tied to a named peer)", ev.AgentName)
	}
	if ev.Allowed {
		t.Error("Allowed=true, want false")
	}
}

func TestAgentAuthAuthorizeAgentScopes_Allow_EmitsAudit(t *testing.T) {
	rec := &recordingAuditLogger{}
	aa := NewAgentAuth(rec, slog.Default())

	identity := &auth.Identity{UserID: "alice", Scopes: []string{"agent:alice__*:invoke"}}
	if err := aa.AuthorizeAgentScopes(context.Background(), identity, []string{"agent:alice__my_agent:invoke"}); err != nil {
		t.Fatalf("expected allow, got err = %v", err)
	}

	if len(rec.agentAccessEvents) != 1 {
		t.Fatalf("LogAgentAccess called %d times, want 1", len(rec.agentAccessEvents))
	}
	ev := rec.agentAccessEvents[0]
	if !ev.Allowed {
		t.Error("Allowed=false, want true")
	}
	if ev.UserID != "alice" {
		t.Errorf("UserID=%q, want alice", ev.UserID)
	}
}

type recordingAuditLogger struct {
	agentAccessEvents []auth.AgentAccessEvent
	toolAccessEvents  []auth.ToolAccessEvent
}

func (r *recordingAuditLogger) LogAuth(_ context.Context, _ auth.AuthEvent) {}
func (r *recordingAuditLogger) LogToolAccess(_ context.Context, ev auth.ToolAccessEvent) {
	r.toolAccessEvents = append(r.toolAccessEvents, ev)
}
func (r *recordingAuditLogger) LogAgentAccess(_ context.Context, ev auth.AgentAccessEvent) {
	r.agentAccessEvents = append(r.agentAccessEvents, ev)
}
func (r *recordingAuditLogger) LogToolExecution(_ context.Context, _ auth.ToolExecutionEvent)       {}
func (r *recordingAuditLogger) LogControlAccess(_ context.Context, _ auth.ControlAccessEvent)       {}
func (r *recordingAuditLogger) LogShareGrant(_ context.Context, _ auth.ShareGrantEvent)             {}
func (r *recordingAuditLogger) LogShareLevelChange(_ context.Context, _ auth.ShareLevelChangeEvent) {}
func (r *recordingAuditLogger) LogShareRevoke(_ context.Context, _ auth.ShareRevokeEvent)           {}
func (r *recordingAuditLogger) LogTemplateDeleted(_ context.Context, _ auth.TemplateDeletedEvent)   {}
func (r *recordingAuditLogger) LogActivationChange(_ context.Context, _ auth.ActivationChangeEvent) {}
func (r *recordingAuditLogger) LogConsentChange(_ context.Context, _ auth.ConsentChangeEvent)       {}

func TestAuthorizers_AccessDecisionsNameTheKey(t *testing.T) {
	key := &auth.Identity{UserID: "system:ci-deployer", CredentialID: "key-1", Scopes: []string{"agent:allowed:invoke"}}
	rec := &recordingAuditLogger{}
	aa := NewAgentAuth(rec, slog.Default())
	ta := NewToolAuth(rec, slog.Default())

	_ = aa.AuthorizeAgent(t.Context(), key, "forbidden")
	_ = aa.AuthorizeWorkflow(t.Context(), key, "forbidden-flow")
	_ = aa.AuthorizeAgentScopes(t.Context(), key, []string{"agent:forbidden:invoke"})
	_ = ta.AuthorizeTool(t.Context(), key, "forbidden_tool", []string{"tool:forbidden:invoke"})

	if len(rec.agentAccessEvents) != 3 || len(rec.toolAccessEvents) != 1 {
		t.Fatalf("events = %d agent, %d tool; want 3 and 1", len(rec.agentAccessEvents), len(rec.toolAccessEvents))
	}
	for i, ev := range rec.agentAccessEvents {
		if ev.Allowed || ev.CredentialID != "key-1" {
			t.Errorf("agent event %d = %+v, want a denial naming key-1", i, ev)
		}
	}
	if ev := rec.toolAccessEvents[0]; ev.Allowed || ev.CredentialID != "key-1" {
		t.Errorf("tool event = %+v, want a denial naming key-1", ev)
	}
}
