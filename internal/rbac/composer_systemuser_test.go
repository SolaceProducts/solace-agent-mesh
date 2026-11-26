// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

func systemUserDBState() *DBState {
	return &DBState{
		Roles: []DBRole{
			{ID: "uuid-sys", Name: RoleSystemInvokeAll, Scopes: []string{ScopeAgentInvokeAny}},
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"*"}},
		},
		UserRoles: []DBUserRole{
			{UserID: DefaultSystemUserID, RoleID: "uuid-sys", SubjectType: "sub", SubjectValue: DefaultSystemUserSub, Issuer: SystemUserIssuer},
			{UserID: "user-uuid-alice", RoleID: "uuid-admin", SubjectType: "sub", SubjectValue: "alice@example.com", Issuer: "okta"},
		},
	}
}

func TestComposer_SystemUserResolvesBySubject(t *testing.T) {
	c := NewComposer(YAMLState{}, &staticSource{state: systemUserDBState()}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup(DefaultSystemUserSub, "webhook", nil)
	if !slices.Equal(res.Roles, []string{RoleSystemInvokeAll}) {
		t.Errorf("system user roles = %v, want [%s]", res.Roles, RoleSystemInvokeAll)
	}
	if !slices.Contains(res.Scopes, ScopeAgentInvokeAny) {
		t.Errorf("system user scopes = %v, want to contain %s", res.Scopes, ScopeAgentInvokeAny)
	}
}

func TestComposer_NonSystemSubjectDoesNotLeak(t *testing.T) {
	c := NewComposer(YAMLState{}, &staticSource{state: systemUserDBState()}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("alice@example.com", "webui", nil)
	if len(res.Roles) != 0 {
		t.Errorf("human subject resolved by string = %v, want none (no leak into subject bucket)", res.Roles)
	}
}

func TestComposer_UnseededSystemUserFailsClosed(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"default_role": {Scopes: []string{"agent:Orchestrator:invoke"}},
		}},
		DefaultRoles: []string{"default_role"},
	}
	c := NewComposer(yaml, &staticSource{state: systemUserDBState()}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("system:unknown", "webhook", nil)
	if len(res.Roles) != 0 {
		t.Errorf("unseeded system user roles = %v, want none (fail closed, no default fallback)", res.Roles)
	}

	human, _ := c.Lookup("alice@example.com", "webhook", nil)
	if len(human.Roles) == 0 {
		t.Errorf("unseeded human user roles = %v, want the configured default role", human.Roles)
	}
}

func TestComposer_SystemUserAssignmentProvenance(t *testing.T) {
	c := NewComposer(YAMLState{}, &staticSource{state: systemUserDBState()}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	assigns, _ := c.ResolveRoleAssignments(DefaultSystemUserSub, "webhook", nil)
	found := false
	for _, a := range assigns {
		if a.RoleID == "uuid-sys" {
			if a.Source != SourceDirect {
				t.Errorf("system grant source = %q, want %q", a.Source, SourceDirect)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("system user assignment not resolved: %+v", assigns)
	}
}

func TestComposer_WarnsWhenDBRowShadowsReservedSub(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db := &DBState{
		Roles: []DBRole{{ID: "uuid-narrow", Name: "operator_narrow_eval", Scopes: []string{"agent:Orchestrator:invoke"}}},
		UserRoles: []DBUserRole{
			{UserID: "user-uuid-eval", RoleID: "uuid-narrow", SubjectType: "sub", SubjectValue: EvalSystemUserSub, Issuer: SystemUserIssuer},
			{UserID: "user-uuid-custom", RoleID: "uuid-narrow", SubjectType: "sub", SubjectValue: "system:my-own-eval", Issuer: SystemUserIssuer},
		},
	}
	c := NewComposer(YAMLState{}, &staticSource{state: db}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if got := strings.Count(buf.String(), msgShadowedReservedSub); got != 1 {
		t.Fatalf("shadow warnings = %d, want 1 (only %s is reserved); log:\n%s", got, EvalSystemUserSub, buf.String())
	}
	if !strings.Contains(buf.String(), "systemUserSub="+EvalSystemUserSub) {
		t.Errorf("warning missing systemUserSub=%s field; log:\n%s", EvalSystemUserSub, buf.String())
	}
}
