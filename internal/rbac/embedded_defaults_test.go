// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestEmbeddedDefaults_ChannelUserKeepsOldReach(t *testing.T) {
	roles, users, err := EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}

	role, ok := roles.Roles[RoleSystemChannelInvoke]
	if !ok {
		t.Fatalf("embedded roles missing %q; have %v", RoleSystemChannelInvoke, roles.Roles)
	}
	if want := []string{AgentInvokeScope("Orchestrator")}; !slices.Equal(role.Scopes, want) {
		t.Errorf("role %q scopes = %v, want exactly %v", RoleSystemChannelInvoke, role.Scopes, want)
	}
	if slices.Contains(role.Scopes, ScopeAgentInvokeAny) {
		t.Errorf("role %q must not carry the wildcard invoke scope", RoleSystemChannelInvoke)
	}

	assignment, ok := users.Users[ChannelSystemUserSub]
	if !ok {
		t.Fatalf("embedded users missing %q; have %v", ChannelSystemUserSub, users.Users)
	}
	if !slices.Contains(assignment.Roles, RoleSystemChannelInvoke) {
		t.Errorf("user %q roles = %v, want to contain %q", ChannelSystemUserSub, assignment.Roles, RoleSystemChannelInvoke)
	}
	if SystemUserPrincipal(ChannelSystemUserRunAs) != ChannelSystemUserSub {
		t.Errorf("run_as %q resolves to %q, want %q", ChannelSystemUserRunAs, SystemUserPrincipal(ChannelSystemUserRunAs), ChannelSystemUserSub)
	}
}

func TestEmbeddedDefaults_MatchConstants(t *testing.T) {
	roles, users, err := EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}

	role, ok := roles.Roles[RoleSystemInvokeAll]
	if !ok {
		t.Fatalf("embedded roles missing %q; have %v", RoleSystemInvokeAll, roles.Roles)
	}
	for _, want := range []string{ScopeAgentInvokeAny, ScopeWorkflowInvokeAny} {
		if !slices.Contains(role.Scopes, want) {
			t.Errorf("embedded role %q scopes = %v, want to contain %q", RoleSystemInvokeAll, role.Scopes, want)
		}
	}

	assignment, ok := users.Users[DefaultSystemUserSub]
	if !ok {
		t.Fatalf("embedded users missing %q; have %v", DefaultSystemUserSub, users.Users)
	}
	if !slices.Contains(assignment.Roles, RoleSystemInvokeAll) {
		t.Errorf("embedded assignment for %q = %v, want to contain %q", DefaultSystemUserSub, assignment.Roles, RoleSystemInvokeAll)
	}
}

func TestReservedSystemUserSubsMatchEmbedded(t *testing.T) {
	_, users, err := EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}

	embedded := slices.Sorted(maps.Keys(users.Users))
	reserved := slices.Sorted(slices.Values(ReservedSystemUserSubs()))
	if !slices.Equal(embedded, reserved) {
		t.Errorf("ReservedSystemUserSubs() = %v, want the embedded user subs %v", reserved, embedded)
	}
}

func TestReservedSystemUsersHaveSeedIdentity(t *testing.T) {
	seen := map[string]string{}
	for _, u := range ReservedSystemUsers() {
		if _, err := uuid.Parse(u.ID); err != nil {
			t.Errorf("%s: id %q is not a UUID: %v", u.Sub, u.ID, err)
		}
		if other, dup := seen[u.ID]; dup {
			t.Errorf("%s and %s share id %q", u.Sub, other, u.ID)
		}
		seen[u.ID] = u.Sub
		if u.DisplayName == "" {
			t.Errorf("%s: empty display name", u.Sub)
		}
	}
	if u, ok := LookupReservedSystemUser(DefaultSystemUserSub); !ok || u.ID != DefaultSystemUserID || u.HiddenFromRunAsPicker {
		t.Errorf("LookupReservedSystemUser(%q) = %+v, %v; want id %q, offered in the run_as picker", DefaultSystemUserSub, u, ok, DefaultSystemUserID)
	}
}

func TestEmbeddedDefaults_ResolveViaComposer(t *testing.T) {
	roles, users, err := EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}
	c := NewComposer(YAMLState{Roles: roles, Users: users}, nil, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup(DefaultSystemUserSub, "webhook", nil)
	for _, want := range []string{ScopeAgentInvokeAny, ScopeWorkflowInvokeAny} {
		if !slices.Contains(res.Scopes, want) {
			t.Errorf("default system user scopes = %v, want to contain %q", res.Scopes, want)
		}
	}

	res, _ = c.Lookup("alice@example.com", "webhook", nil)
	if slices.Contains(res.Scopes, ScopeAgentInvokeAny) {
		t.Errorf("human user unexpectedly resolved system scope: %v", res.Scopes)
	}
}

func TestEmbeddedDefaults_EvalSystemUser(t *testing.T) {
	roles, users, err := EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}

	role, ok := roles.Roles[RoleSystemEvalInvoke]
	if !ok {
		t.Fatalf("embedded roles missing %q; have %v", RoleSystemEvalInvoke, roles.Roles)
	}
	if want := []string{RoleSystemInvokeAll}; !slices.Equal(role.Inherits, want) {
		t.Errorf("role %q inherits = %v, want %v", RoleSystemEvalInvoke, role.Inherits, want)
	}

	assignment, ok := users.Users[EvalSystemUserSub]
	if !ok {
		t.Fatalf("embedded users missing %q; have %v", EvalSystemUserSub, users.Users)
	}
	if !slices.Contains(assignment.Roles, RoleSystemEvalInvoke) {
		t.Errorf("user %q roles = %v, want to contain %q", EvalSystemUserSub, assignment.Roles, RoleSystemEvalInvoke)
	}
	if SystemUserPrincipal(EvalSystemUserRunAs) != EvalSystemUserSub {
		t.Errorf("run_as %q resolves to %q, want %q", EvalSystemUserRunAs, SystemUserPrincipal(EvalSystemUserRunAs), EvalSystemUserSub)
	}

	c := NewComposer(YAMLState{Roles: roles, Users: users}, nil, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	res, _ := c.Lookup(EvalSystemUserSub, "eval", nil)
	want := []string{ScopeAgentInvokeAny, ScopeToolAny, ScopeWorkflowInvokeAny}
	if got := slices.Sorted(slices.Values(res.Scopes)); !slices.Equal(got, want) {
		t.Errorf("eval system user scopes = %v, want exactly %v", got, want)
	}
}
