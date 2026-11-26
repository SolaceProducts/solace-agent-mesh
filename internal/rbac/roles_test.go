// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveUserScopes(t *testing.T) {
	roles := RolesConfig{
		Roles: map[string]RoleDefinition{
			"viewer": {Scopes: []string{"tool:artifact:read"}},
			"editor": {Scopes: []string{"tool:artifact:update"}, Inherits: []string{"viewer"}},
			"admin":  {Scopes: []string{"*"}, Inherits: []string{"editor"}},
		},
	}
	users := UsersConfig{
		Users: map[string]UserAssignment{
			"alice": {Roles: []string{"editor"}},
			"bob":   {Roles: []string{"admin"}},
		},
	}

	engine, err := NewRoleEngine(roles, users, []string{"viewer"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		userID string
		want   []string
	}{
		{"editor inherits viewer", "alice", []string{"tool:artifact:update", "tool:artifact:read"}},
		{"admin inherits all", "bob", []string{"*", "tool:artifact:update", "tool:artifact:read"}},
		{"unknown user gets default", "charlie", []string{"tool:artifact:read"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.ResolveUserScopes(tt.userID)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d; got %v", len(got), len(tt.want), got)
			}
			for i, s := range got {
				if s != tt.want[i] {
					t.Errorf("scope[%d] = %q, want %q", i, s, tt.want[i])
				}
			}
		})
	}
}

func TestCircularInheritanceRejected(t *testing.T) {
	roles := RolesConfig{
		Roles: map[string]RoleDefinition{
			"a": {Scopes: []string{"scope:a"}, Inherits: []string{"b"}},
			"b": {Scopes: []string{"scope:b"}, Inherits: []string{"a"}},
		},
	}
	users := UsersConfig{
		Users: map[string]UserAssignment{
			"user1": {Roles: []string{"a"}},
		},
	}

	_, err := NewRoleEngine(roles, users, nil)
	if err == nil {
		t.Fatal("expected error for cyclic inheritance, got nil")
	}
	if !containsSubstring(err.Error(), "cycle") {
		t.Errorf("error %q should mention cycle", err.Error())
	}
}

func TestDetectCycles(t *testing.T) {
	tests := []struct {
		name      string
		graph     map[string][]string
		wantCycle bool
	}{
		{
			name:      "empty graph",
			graph:     map[string][]string{},
			wantCycle: false,
		},
		{
			name: "clean linear chain",
			graph: map[string][]string{
				"viewer": {},
				"editor": {"viewer"},
				"admin":  {"editor"},
			},
			wantCycle: false,
		},
		{
			name: "diamond no cycle",
			graph: map[string][]string{
				"base":    {},
				"left":    {"base"},
				"right":   {"base"},
				"diamond": {"left", "right"},
			},
			wantCycle: false,
		},
		{
			name: "simple two-node cycle A->B->A",
			graph: map[string][]string{
				"a": {"b"},
				"b": {"a"},
			},
			wantCycle: true,
		},
		{
			name: "three-node cycle A->B->C->A",
			graph: map[string][]string{
				"a": {"b"},
				"b": {"c"},
				"c": {"a"},
			},
			wantCycle: true,
		},
		{
			name: "self-loop",
			graph: map[string][]string{
				"a": {"a"},
			},
			wantCycle: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cycle := detectCycles(tt.graph)
			if tt.wantCycle && cycle == nil {
				t.Error("expected a cycle to be detected, got nil")
			}
			if !tt.wantCycle && cycle != nil {
				t.Errorf("expected no cycle, got %v", cycle)
			}
			if cycle != nil {
				if cycle[0] != cycle[len(cycle)-1] {
					t.Errorf("cycle path should start and end with the same node; got %v", cycle)
				}
			}
		})
	}
}

func TestNewRoleEngineCycleViaYAMLDBMerge(t *testing.T) {
	roles := RolesConfig{
		Roles: map[string]RoleDefinition{
			"yaml_role": {Scopes: []string{"scope:yaml"}, Inherits: []string{"db_role"}},
			"db_role":   {Scopes: []string{"scope:db"}, Inherits: []string{"yaml_role"}},
		},
	}

	_, err := NewRoleEngine(roles, UsersConfig{}, nil)
	if err == nil {
		t.Fatal("expected error for cyclic YAML+DB merged graph, got nil")
	}
	if !containsSubstring(err.Error(), "cycle") {
		t.Errorf("error %q should mention cycle", err.Error())
	}
}

func TestScopeDeduplication(t *testing.T) {
	roles := RolesConfig{
		Roles: map[string]RoleDefinition{
			"base":   {Scopes: []string{"tool:_:read", "tool:_:update"}},
			"role_a": {Scopes: []string{"tool:_:read", "extra:a"}, Inherits: []string{"base"}},
			"role_b": {Scopes: []string{"tool:_:read", "extra:b"}, Inherits: []string{"base"}},
		},
	}
	users := UsersConfig{
		Users: map[string]UserAssignment{
			"user1": {Roles: []string{"role_a", "role_b"}},
		},
	}

	engine, err := NewRoleEngine(roles, users, nil)
	if err != nil {
		t.Fatal(err)
	}

	scopes := engine.ResolveUserScopes("user1")
	count := 0
	for _, s := range scopes {
		if s == "tool:_:read" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("tool:_:read appears %d times, want 1; scopes: %v", count, scopes)
	}
}

func TestNewRoleEngineValidation(t *testing.T) {
	tests := []struct {
		name     string
		roles    RolesConfig
		users    UsersConfig
		defaults []string
		wantErr  string
	}{
		{
			name: "unknown inherited role",
			roles: RolesConfig{Roles: map[string]RoleDefinition{
				"a": {Inherits: []string{"nonexistent"}},
			}},
			wantErr: "inherits from unknown role",
		},
		{
			name:  "unknown user role",
			roles: RolesConfig{Roles: map[string]RoleDefinition{"a": {}}},
			users: UsersConfig{Users: map[string]UserAssignment{
				"user1": {Roles: []string{"nonexistent"}},
			}},
			wantErr: "references unknown role",
		},
		{
			name:     "unknown default role",
			roles:    RolesConfig{Roles: map[string]RoleDefinition{"a": {}}},
			defaults: []string{"nonexistent"},
			wantErr:  "default role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRoleEngine(tt.roles, tt.users, tt.defaults)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !containsSubstring(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestLoadRolesFromFile(t *testing.T) {
	content := `roles:
  viewer:
    description: "Read-only access"
    scopes:
      - "tool:artifact:read"
  editor:
    description: "Read-write access"
    scopes:
      - "tool:artifact:update"
    inherits:
      - viewer
`
	dir := t.TempDir()
	path := filepath.Join(dir, "roles.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadRolesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(cfg.Roles))
	}
	if cfg.Roles["editor"].Inherits[0] != "viewer" {
		t.Errorf("editor should inherit viewer")
	}
}

func TestLoadUsersFromFile(t *testing.T) {
	content := `users:
  alice:
    roles:
      - editor
  bob:
    roles:
      - admin
`
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadUsersFromFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(cfg.Users))
	}
	if cfg.Users["alice"].Roles[0] != "editor" {
		t.Errorf("alice should have editor role")
	}
}

func TestLoadUsersFromFile_NeverMutatesKeys(t *testing.T) {
	content := `users:
  "First.Last@example.org":
    roles:
      - sam_admin
  "system:default":
    roles:
      - system_agent_invoke
  "gateway_specific_identities:slack:Bob@example.org":
    roles:
      - sam_user
`
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadUsersFromFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"First.Last@example.org",
		"system:default",
		"gateway_specific_identities:slack:Bob@example.org",
	} {
		if _, ok := cfg.Users[key]; !ok {
			t.Errorf("expected key %q to round-trip verbatim, got keys: %v", key, keysOf(cfg.Users))
		}
	}
	if len(cfg.Users) != 3 {
		t.Errorf("expected no keys added or merged, got %d: %v", len(cfg.Users), keysOf(cfg.Users))
	}
}

func TestLoadUsersFromFile_WarnsOnNonCanonicalKeyAndCollision(t *testing.T) {
	content := `users:
  "Mixed.Case@example.org":
    roles:
      - sam_admin
  "Dup@example.org":
    roles:
      - sam_admin
  "dup@example.org":
    roles:
      - sam_user
`
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if _, err := LoadUsersFromFile(path, logger); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, msgUserIdentityNotCanonical) || !strings.Contains(out, "Mixed.Case@example.org") {
		t.Errorf("expected a non-canonical warning naming the mixed-case key, got log:\n%s", out)
	}
	if !strings.Contains(out, msgUserIdentityFoldCollision) {
		t.Errorf("expected a fold-collision warning for Dup@example.org/dup@example.org, got log:\n%s", out)
	}
}

func TestLoadUsersFromFile_AllLowercaseSeedStaysQuiet(t *testing.T) {
	content := `users:
  "alice@example.org":
    roles:
      - sam_admin
  "system:default":
    roles:
      - system_agent_invoke
`
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if _, err := LoadUsersFromFile(path, logger); err != nil {
		t.Fatal(err)
	}

	if out := buf.String(); out != "" {
		t.Errorf("expected no warnings for an already-canonical seed, got log:\n%s", out)
	}
}

func TestLoadUsersFromFile_PaddingOnlyKeyGetsTheRightMessage(t *testing.T) {
	content := "users:\n  \" admin@example.org\":\n    roles:\n      - sam_admin\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if _, err := LoadUsersFromFile(path, logger); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if strings.Contains(out, msgUserIdentityNotCanonical) {
		t.Errorf("a padding-only key is already lowercase and must not get the not-lowercase warning, got log:\n%s", out)
	}
	if !strings.Contains(out, msgUserIdentityHasWhitespace) {
		t.Errorf("expected the whitespace warning for a padded key, got log:\n%s", out)
	}
}

func keysOf(users map[string]UserAssignment) []string {
	keys := make([]string, 0, len(users))
	for k := range users {
		keys = append(keys, k)
	}
	return keys
}
