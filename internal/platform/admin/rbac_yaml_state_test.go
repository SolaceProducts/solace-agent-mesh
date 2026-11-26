// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestBuildRBACYAMLState_NoOperatorConfigHasOnlyEmbeddedDefault(t *testing.T) {
	wantRoles, wantUsers, err := rbac.EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}
	state, err := BuildRBACYAMLState(oidcauth.AuthorizationServiceConfig{}, nil)
	if err != nil {
		t.Fatalf("BuildRBACYAMLState: %v", err)
	}
	if !maps.Equal(roleNames(state.Roles.Roles), roleNames(wantRoles.Roles)) {
		t.Errorf("roles = %+v, want exactly the embedded roles %+v", state.Roles.Roles, wantRoles.Roles)
	}
	for _, user := range rbac.ReservedSystemUserSubs() {
		if _, ok := state.Users.Users[user]; !ok {
			t.Errorf("users = %+v, want the embedded user %q", state.Users.Users, user)
		}
	}
	if len(state.Users.Users) != len(wantUsers.Users) {
		t.Errorf("users = %+v, want exactly the embedded users %+v", state.Users.Users, wantUsers.Users)
	}
	if len(state.ClaimMappings) != 0 {
		t.Errorf("claim mappings = %+v, want none", state.ClaimMappings)
	}
}

func roleNames(in map[string]rbac.RoleDefinition) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for name := range in {
		out[name] = struct{}{}
	}
	return out
}

func TestBuildRBACYAMLState_LoadsRolesUsersClaims(t *testing.T) {
	dir := t.TempDir()
	rolesPath := filepath.Join(dir, "roles.yaml")
	usersPath := filepath.Join(dir, "users.yaml")

	if err := os.WriteFile(rolesPath, []byte(`
roles:
  sam_admin:
    description: full access
    scopes: ["*"]
  sam_user:
    description: read only
    scopes: ["agent_builder:*:read"]
    inherits: []
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usersPath, []byte(`
users:
  alice@example.org:
    roles: ["sam_admin"]
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := oidcauth.AuthorizationServiceConfig{
		RoleToScopeDefinitionsPath: rolesPath,
		UserToRoleAssignmentsPath:  usersPath,
		DefaultRoles:               []string{"sam_user"},
		IDPClaimsConfig: &oidcauth.IDPClaimsConfig{
			OIDCProvider: "azure",
			ClaimKey:     "groups",
			Mappings: map[string][]string{
				"admins-id": {"sam_admin"},
				"users-id":  {"sam_user"},
			},
		},
	}

	state, err := BuildRBACYAMLState(cfg, nil)
	if err != nil {
		t.Fatalf("BuildRBACYAMLState: %v", err)
	}

	if got := state.Roles.Roles["sam_admin"].Scopes; !slices.Equal(got, []string{"*"}) {
		t.Errorf("sam_admin scopes = %v, want [*]", got)
	}
	if got := state.Users.Users["alice@example.org"].Roles; !slices.Equal(got, []string{"sam_admin"}) {
		t.Errorf("alice roles = %v, want [sam_admin]", got)
	}
	if !slices.Equal(state.DefaultRoles, []string{"sam_user"}) {
		t.Errorf("default roles = %v, want [sam_user]", state.DefaultRoles)
	}
	if state.ClaimOIDCProvider != "azure" || state.ClaimKey != "groups" {
		t.Errorf("claim header = (%q, %q), want (azure, groups)", state.ClaimOIDCProvider, state.ClaimKey)
	}
	if len(state.ClaimMappings) != 2 {
		t.Fatalf("claim mappings = %d, want 2", len(state.ClaimMappings))
	}
	for _, m := range state.ClaimMappings {
		if m.OIDCProvider != "azure" || m.ClaimKey != "groups" {
			t.Errorf("mapping header mismatch: %+v", m)
		}
		switch m.ClaimValue {
		case "admins-id":
			if m.RoleID != "sam_admin" {
				t.Errorf("admins-id mapped to %q, want sam_admin", m.RoleID)
			}
		case "users-id":
			if m.RoleID != "sam_user" {
				t.Errorf("users-id mapped to %q, want sam_user", m.RoleID)
			}
		default:
			t.Errorf("unexpected claim value: %q", m.ClaimValue)
		}
	}
}

func TestBuildRBACYAMLState_MissingRolesFile(t *testing.T) {
	cfg := oidcauth.AuthorizationServiceConfig{
		RoleToScopeDefinitionsPath: "/nonexistent/path/roles.yaml",
	}
	state, err := BuildRBACYAMLState(cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing roles file")
	}
	assertEmbeddedSystemUsersSurvive(t, state)
}

func assertEmbeddedSystemUsersSurvive(t *testing.T, state rbac.YAMLState) {
	t.Helper()
	for _, sub := range []string{rbac.EvalSystemUserSub, rbac.DefaultSystemUserSub} {
		if _, ok := state.Users.Users[sub]; !ok {
			t.Errorf("state.Users missing %q after a load failure; got %v", sub, state.Users.Users)
		}
	}
	if _, ok := state.Roles.Roles[rbac.RoleSystemEvalInvoke]; !ok {
		t.Errorf("state.Roles missing %q after a load failure; got %v", rbac.RoleSystemEvalInvoke, state.Roles.Roles)
	}
}

func TestBuildRBACYAMLState_MissingUsersFile(t *testing.T) {
	dir := t.TempDir()
	rolesPath := filepath.Join(dir, "roles.yaml")
	if err := os.WriteFile(rolesPath, []byte("roles: {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := oidcauth.AuthorizationServiceConfig{
		RoleToScopeDefinitionsPath: rolesPath,
		UserToRoleAssignmentsPath:  "/nonexistent/users.yaml",
	}
	state, err := BuildRBACYAMLState(cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing users file")
	}
	assertEmbeddedSystemUsersSurvive(t, state)
}

func TestBuildRBACYAMLState_PerScopeSkip(t *testing.T) {
	dir := t.TempDir()
	rolesPath := filepath.Join(dir, "roles.yaml")
	if err := os.WriteFile(rolesPath, []byte(`
roles:
  sam_admin:
    description: full + one broken scope
    scopes: ["*", "sam:agent_builder:_:read"]
  sam_user:
    description: well-formed siblings of a broken role load normally
    scopes: ["agent:Orchestrator:invoke"]
  empty_after_filter:
    description: only invalid scopes; role survives but ends up empty
    scopes: ["::bad", "monitor/legacy"]
`), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	state, err := BuildRBACYAMLState(oidcauth.AuthorizationServiceConfig{RoleToScopeDefinitionsPath: rolesPath}, logger)
	if err != nil {
		t.Fatalf("BuildRBACYAMLState: %v", err)
	}

	if got := state.Roles.Roles["sam_admin"].Scopes; !slices.Equal(got, []string{"*"}) {
		t.Errorf("sam_admin scopes after filter = %v, want [*] (the bad scope dropped, the wildcard kept)", got)
	}
	if got := state.Roles.Roles["sam_user"].Scopes; !slices.Equal(got, []string{"agent:Orchestrator:invoke"}) {
		t.Errorf("sam_user scopes = %v, want [agent:Orchestrator:invoke] — well-formed siblings must not be affected by a broken role", got)
	}
	if got := state.Roles.Roles["empty_after_filter"].Scopes; len(got) != 0 {
		t.Errorf("empty_after_filter scopes = %v, want empty — every scope was invalid, role survives with no grants (deny-by-default)", got)
	}

	out := buf.String()
	if !strings.Contains(out, `level=WARN`) {
		t.Errorf("per-scope skip must surface at WARN; got:\n%s", out)
	}
	for _, want := range []string{"sam_admin", "sam:agent_builder:_:read", "::bad", "monitor/legacy"} {
		if !strings.Contains(out, want) {
			t.Errorf("WARN log missing %q for actionability; got:\n%s", want, out)
		}
	}
}
