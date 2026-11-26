// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type staticSource struct {
	state *DBState
	err   error
}

func (s *staticSource) Snapshot(_ context.Context) (*DBState, error) {
	return s.state, s.err
}

func TestComposer_YAMLOnly(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_admin": {Scopes: []string{"*"}},
			"sam_user":  {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: UsersConfig{Users: map[string]UserAssignment{
			"alice@example.org": {Roles: []string{"sam_admin"}},
		}},
		DefaultRoles: []string{"sam_user"},
	}
	c := NewComposer(yaml, nil, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, err := c.Lookup("alice@example.org", "webui", nil)
	if err != nil {
		t.Fatalf("lookup alice: %v", err)
	}
	if !slices.Equal(res.Roles, []string{"sam_admin"}) {
		t.Errorf("alice roles = %v, want [sam_admin]", res.Roles)
	}
	if !slices.Equal(res.Scopes, []string{"*"}) {
		t.Errorf("alice scopes = %v, want [sam:*]", res.Scopes)
	}

	res, err = c.Lookup("bob@example.org", "webui", nil)
	if err != nil {
		t.Fatalf("lookup bob: %v", err)
	}
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Errorf("bob roles (default) = %v, want [sam_user]", res.Roles)
	}
}

func TestComposer_MixedCaseEmailSeedResolvesViaFallback(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_admin": {Scopes: []string{"*"}},
			"sam_user":  {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: UsersConfig{Users: map[string]UserAssignment{
			"Mixed.Case@example.com": {Roles: []string{"sam_admin"}},
		}},
		DefaultRoles: []string{"sam_user"},
	}
	c := NewComposer(yaml, nil, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, err := c.Lookup("mixed.case@example.com", "webui", nil)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !slices.Equal(res.Roles, []string{"sam_admin"}) {
		t.Errorf("roles = %v, want [sam_admin] via case-insensitive fallback, not the default", res.Roles)
	}
}

func TestComposer_DefaultFallthroughLogsAtDebugByUsersIDNeverByLogin(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_user": {Scopes: []string{"agent_builder:*:read"}},
		}},
		DefaultRoles: []string{"sam_user"},
	}
	c := NewComposer(yaml, nil, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	const samUserID = "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f01"

	if _, err := c.Lookup("nobody@example.com", "webui", map[string]any{"sam_user_id": samUserID}); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	withRow := buf.String()
	if !strings.Contains(withRow, "applying default roles") || !strings.Contains(withRow, "samUserID="+samUserID) {
		t.Errorf("want a DEBUG line naming the defaulted user by users.id, got:\n%s", withRow)
	}

	buf.Reset()
	if _, err := c.Lookup("norow@example.com", "webui", nil); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	withoutRow := buf.String()
	if !strings.Contains(withoutRow, "applying default roles") || strings.Contains(withoutRow, "samUserID") {
		t.Errorf("want a DEBUG line with no identity for a user with no row, got:\n%s", withoutRow)
	}

	for _, login := range []string{"nobody@", "norow@"} {
		if strings.Contains(withRow+withoutRow, login) {
			t.Errorf("the default-roles line names the login %q:\n%s%s", login, withRow, withoutRow)
		}
	}
}

func TestComposer_EmailShapedSubKeyExactMatchSurvives(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_admin": {Scopes: []string{"*"}},
			"sam_user":  {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: UsersConfig{Users: map[string]UserAssignment{
			"Weird@Sub-Value": {Roles: []string{"sam_admin"}},
		}},
		DefaultRoles: []string{"sam_user"},
	}
	c := NewComposer(yaml, nil, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, err := c.Lookup("Weird@Sub-Value", "webui", nil)
	if err != nil {
		t.Fatalf("lookup exact: %v", err)
	}
	if !slices.Equal(res.Roles, []string{"sam_admin"}) {
		t.Errorf("exact-case sub roles = %v, want [sam_admin]", res.Roles)
	}
}

func TestComposer_ResolveRoleAssignments_Provenance(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"yaml_admin":   {Scopes: []string{"*"}},
			"yaml_default": {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: UsersConfig{Users: map[string]UserAssignment{
			"alice@corp.com": {Roles: []string{"yaml_admin"}},
		}},
		DefaultRoles: []string{"yaml_default"},
	}
	db := &DBState{
		Roles:     []DBRole{{ID: "uuid-direct", Name: "db_direct", Scopes: []string{"agent:*:read"}}},
		UserRoles: []DBUserRole{{UserID: "user-1", RoleID: "uuid-direct"}},
	}
	c := NewComposer(yaml, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	got, err := c.ResolveRoleAssignments("alice@corp.com", "webui", nil)
	if err != nil {
		t.Fatalf("resolve alice: %v", err)
	}
	if len(got) != 1 || got[0] != (RoleAssignment{RoleID: "yaml_admin", Source: SourceYAML}) {
		t.Errorf("alice = %+v, want [{yaml_admin yaml}]", got)
	}

	got, _ = c.ResolveRoleAssignments("user-1", "webui", map[string]any{"sam_user_id": "user-1"})
	if len(got) != 1 || got[0] != (RoleAssignment{RoleID: "uuid-direct", Source: SourceDirect}) {
		t.Errorf("user-1 = %+v, want [{uuid-direct direct}]", got)
	}

	got, _ = c.ResolveRoleAssignments("stranger", "webui", nil)
	if len(got) != 1 || got[0] != (RoleAssignment{RoleID: "yaml_default", Source: SourceDefault}) {
		t.Errorf("stranger = %+v, want [{yaml_default default}]", got)
	}
}

func TestComposer_DBOnly(t *testing.T) {
	yaml := YAMLState{}
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"*"}},
			{ID: "uuid-user", Name: "sam_user", Scopes: []string{"agent_builder:*:read"}},
		},
		UserRoles: []DBUserRole{
			{UserID: "sub-alice", RoleID: "uuid-admin"},
		},
		DefaultRoleIDs: []string{"uuid-user"},
	}
	c := NewComposer(yaml, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("sub-alice", "webui", map[string]any{"sam_user_id": "sub-alice"})
	if !slices.Equal(res.Roles, []string{"sam_admin"}) {
		t.Errorf("alice roles = %v, want [sam_admin]", res.Roles)
	}

	res, _ = c.Lookup("sub-stranger", "webui", nil)
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Errorf("stranger roles (default) = %v, want [sam_user]", res.Roles)
	}
}

func TestComposer_IDKeyedGrantViaSamUserID(t *testing.T) {
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"*"}},
			{ID: "uuid-user", Name: "sam_user", Scopes: []string{"agent_builder:*:read"}},
		},
		UserRoles:      []DBUserRole{{UserID: "user-uuid-1", RoleID: "uuid-admin"}},
		DefaultRoleIDs: []string{"uuid-user"},
	}
	c := NewComposer(YAMLState{}, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("alice@example.com", "webui", map[string]any{"sam_user_id": "user-uuid-1"})
	if !slices.Equal(res.Roles, []string{"sam_admin"}) {
		t.Errorf("roles with sam_user_id = %v, want [sam_admin]", res.Roles)
	}

	res, _ = c.Lookup("alice@example.com", "webui", nil)
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Errorf("roles without sam_user_id = %v, want [sam_user] (default)", res.Roles)
	}

	res, _ = c.Lookup("alice@example.com", "webui", map[string]any{"sam_user_id": "someone-else"})
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Errorf("roles for a different user id = %v, want [sam_user] (default)", res.Roles)
	}
}

func TestComposer_DBDefaultRolesOverrideYAML(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_user": {Scopes: []string{"agent_builder:*:read"}},
		}},
		DefaultRoles: []string{"sam_user"},
	}
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-limited", Name: "limited", Scopes: []string{"agent:*:invoke"}},
		},
		DefaultRoleIDs: []string{"uuid-limited"},
	}
	c := NewComposer(yaml, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, err := c.Lookup("sub-stranger", "webui", nil)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !slices.Equal(res.Roles, []string{"limited"}) {
		t.Errorf("stranger roles = %v, want [limited] (DB default overrides YAML default)", res.Roles)
	}
	if !slices.Equal(res.Scopes, []string{"agent:*:invoke"}) {
		t.Errorf("stranger scopes = %v, want [agent:*:invoke]", res.Scopes)
	}
}

func TestComposer_YAMLDefaultRolesWhenDBEmpty(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_user": {Scopes: []string{"agent_builder:*:read"}},
		}},
		DefaultRoles: []string{"sam_user"},
	}
	db := &DBState{
		Roles:          []DBRole{{ID: "uuid-limited", Name: "limited", Scopes: []string{"agent:*:invoke"}}},
		DefaultRoleIDs: nil,
	}
	c := NewComposer(yaml, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("sub-stranger", "webui", nil)
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Errorf("stranger roles = %v, want [sam_user] (YAML default when DB set empty)", res.Roles)
	}
}

func TestComposer_DBInheritanceResolvesWithinDB(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_user": {Scopes: []string{"agent_builder:*:read"}},
		}},
	}
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-user", Name: "sam_user", Scopes: []string{"agent_builder:*:test"}},
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"agent_builder:_:create"}, Parents: []string{"uuid-user"}},
		},
		UserRoles: []DBUserRole{
			{UserID: "sub-alice", RoleID: "uuid-admin"},
		},
	}
	c := NewComposer(yaml, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, err := c.Lookup("sub-alice", "webui", map[string]any{"sam_user_id": "sub-alice"})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	slices.Sort(res.Scopes)
	want := []string{"agent_builder:*:test", "agent_builder:_:create"}
	if !slices.Equal(res.Scopes, want) {
		t.Errorf("admin scopes = %v, want %v (DB inheritance only, YAML sam_user must NOT leak in)", res.Scopes, want)
	}
	if !slices.Equal(res.Roles, []string{"sam_admin"}) {
		t.Errorf("admin roles = %v, want [sam_admin]", res.Roles)
	}
}

func TestComposer_CollisionRolesStayDistinct(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_admin": {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: UsersConfig{Users: map[string]UserAssignment{
			"yaml-user": {Roles: []string{"sam_admin"}},
		}},
	}
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"agent:hr-bot:invoke"}},
		},
		UserRoles: []DBUserRole{
			{UserID: "db-user", RoleID: "uuid-admin"},
		},
	}
	c := NewComposer(yaml, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	dbRes, _ := c.Lookup("db-user", "webui", map[string]any{"sam_user_id": "db-user"})
	if !slices.Equal(dbRes.Scopes, []string{"agent:hr-bot:invoke"}) {
		t.Errorf("db-user scopes = %v, want only the DB role's scopes", dbRes.Scopes)
	}
	if !slices.Equal(dbRes.Roles, []string{"sam_admin"}) {
		t.Errorf("db-user roles = %v, want [sam_admin] (display name)", dbRes.Roles)
	}

	yamlRes, _ := c.Lookup("yaml-user", "webui", nil)
	if !slices.Equal(yamlRes.Scopes, []string{"agent_builder:*:read"}) {
		t.Errorf("yaml-user scopes = %v, want only the YAML role's scopes", yamlRes.Scopes)
	}
	if !slices.Equal(yamlRes.Roles, []string{"sam_admin"}) {
		t.Errorf("yaml-user roles = %v, want [sam_admin] (display name)", yamlRes.Roles)
	}
}

func TestComposer_YAMLRemovalLeavesDBRoleUnchanged(t *testing.T) {
	db := func() *DBState {
		return &DBState{
			Roles: []DBRole{
				{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"agent:hr-bot:invoke"}},
			},
			UserRoles: []DBUserRole{
				{UserID: "db-user", RoleID: "uuid-admin"},
			},
		}
	}

	withYAML := NewComposer(YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_admin": {Scopes: []string{"*"}},
		}},
	}, &staticSource{state: db()}, newTestLogger())
	if err := withYAML.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild withYAML: %v", err)
	}
	withoutYAML := NewComposer(YAMLState{}, &staticSource{state: db()}, newTestLogger())
	if err := withoutYAML.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild withoutYAML: %v", err)
	}

	a, _ := withYAML.Lookup("db-user", "webui", map[string]any{"sam_user_id": "db-user"})
	b, _ := withoutYAML.Lookup("db-user", "webui", map[string]any{"sam_user_id": "db-user"})
	slices.Sort(a.Scopes)
	slices.Sort(b.Scopes)
	if !slices.Equal(a.Scopes, b.Scopes) {
		t.Errorf("DB role scopes changed with YAML present=%v vs removed=%v", a.Scopes, b.Scopes)
	}
	if !slices.Equal(a.Scopes, []string{"agent:hr-bot:invoke"}) {
		t.Errorf("DB role scopes = %v, want only the DB role's own scopes regardless of YAML", a.Scopes)
	}
}

func TestComposer_CollisionWarningEmittedOnce(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_admin": {Scopes: []string{"*"}},
			"sam_user":  {Scopes: []string{"agent_builder:*:read"}},
		}},
	}
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"agent:hr-bot:invoke"}},
			{ID: "uuid-other", Name: "incident_responder", Scopes: []string{"agent:inc:invoke"}},
		},
	}
	c := NewComposer(yaml, &staticSource{state: db}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	got := strings.Count(buf.String(), msgRoleNameCollision)
	if got != 1 {
		t.Fatalf("collision warnings = %d, want 1 (only sam_admin collides); log:\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "role=sam_admin") {
		t.Errorf("warning missing role=sam_admin field; log:\n%s", buf.String())
	}
}

func TestComposer_CollisionSingleUserGetsBothRoles(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_admin": {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: UsersConfig{Users: map[string]UserAssignment{
			"sub-alice": {Roles: []string{"sam_admin"}},
		}},
	}
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"agent:hr-bot:invoke"}},
		},
		UserRoles: []DBUserRole{
			{UserID: "sub-alice", RoleID: "uuid-admin"},
		},
	}
	c := NewComposer(yaml, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, err := c.Lookup("sub-alice", "webui", map[string]any{"sam_user_id": "sub-alice"})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !slices.Equal(res.Roles, []string{"sam_admin"}) {
		t.Errorf("roles = %v, want a single deduped [sam_admin] (both origins collapse to one display name)", res.Roles)
	}
	slices.Sort(res.Scopes)
	want := []string{"agent:hr-bot:invoke", "agent_builder:*:read"}
	if !slices.Equal(res.Scopes, want) {
		t.Errorf("scopes = %v, want the union of both origins %v", res.Scopes, want)
	}
}

func TestComposer_UnresolvedYAMLRefWarns(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_user": {Scopes: []string{"agent_builder:*:read"}},
		}},
		DefaultRoles: []string{"sam_user"},
		Users: UsersConfig{Users: map[string]UserAssignment{
			"typo@example.org": {Roles: []string{"sam_amdin"}},
		}},
		ClaimMappings: []DBClaimMapping{
			{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "admins", RoleID: "sam_claimtypo"},
		},
	}
	c := NewComposer(yaml, &staticSource{state: &DBState{}}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if got := strings.Count(buf.String(), msgUnresolvedRoleRef); got != 2 {
		t.Fatalf("unresolved-ref warnings = %d, want 2; log:\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "role=sam_amdin") {
		t.Errorf("warning missing role=sam_amdin field (user-assignment branch); log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "role=sam_claimtypo") {
		t.Errorf("warning missing role=sam_claimtypo field (claim-mapping branch); log:\n%s", buf.String())
	}

	res, _ := c.Lookup("typo@example.org", "webui", nil)
	if len(res.Scopes) != 0 {
		t.Errorf("typo user scopes = %v, want empty (fail-closed, no default-role fallback)", res.Scopes)
	}
}

func TestComposer_DBAssignmentsAreGlobal(t *testing.T) {
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"*"}},
			{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}},
		},
		UserRoles: []DBUserRole{
			{UserID: "sub-alice", RoleID: "uuid-admin"},
			{UserID: "sub-alice", RoleID: "uuid-user"},
		},
	}
	c := NewComposer(YAMLState{}, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	for _, gatewayID := range []string{"webui", "slack", ""} {
		res, _ := c.Lookup("sub-alice", gatewayID, map[string]any{"sam_user_id": "sub-alice"})
		slices.Sort(res.Roles)
		if !slices.Equal(res.Roles, []string{"sam_admin", "sam_user"}) {
			t.Errorf("alice@%s roles = %v, want both assignments to apply globally", gatewayID, res.Roles)
		}
	}
}

func TestComposer_UnknownYAMLRoleFailsClosed(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_user": {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: UsersConfig{Users: map[string]UserAssignment{
			"typo@example.org": {Roles: []string{"sam_amdin"}},
		}},
		DefaultRoles: []string{"sam_user"},
	}
	c := NewComposer(yaml, nil, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, err := c.Lookup("typo@example.org", "webui", nil)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(res.Scopes) != 0 {
		t.Errorf("scopes = %v, want empty (unknown role must grant nothing)", res.Scopes)
	}
	if slices.Contains(res.Scopes, "agent_builder:*:read") {
		t.Errorf("scopes = %v, must NOT include the default role's scopes", res.Scopes)
	}

	def, _ := c.Lookup("stranger@example.org", "webui", nil)
	if !slices.Equal(def.Roles, []string{"sam_user"}) {
		t.Errorf("stranger default roles = %v, want [sam_user]", def.Roles)
	}
}

func TestComposer_IdpClaimMappings(t *testing.T) {
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"*"}},
			{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}},
		},
		ClaimMappings: []DBClaimMappingEntity{
			{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "sam-admins", RoleIDs: []string{"uuid-admin"}},
		},
	}
	yaml := YAMLState{
		ClaimMappings: []DBClaimMapping{
			{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "sam-users", RoleID: "sam_user"},
		},
	}
	c := NewComposer(yaml, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	ctx := map[string]any{
		"oidc_provider": "azure",
		"idp_claims": map[string]any{
			"groups": []any{"sam-admins", "sam-users"},
		},
	}
	res, _ := c.Lookup("sub-alice", "webui", ctx)
	slices.Sort(res.Roles)
	if !slices.Equal(res.Roles, []string{"sam_admin", "sam_user"}) {
		t.Errorf("claim-mapped roles = %v, want both YAML and DB mappings to fire", res.Roles)
	}

	res, _ = c.Lookup("sub-alice", "webui", map[string]any{
		"oidc_provider": "google",
		"idp_claims":    map[string]any{"groups": []any{"sam-admins"}},
	})
	if len(res.Roles) != 0 {
		t.Errorf("google claims fired = %v, want empty (provider mismatch)", res.Roles)
	}
}

func TestComposer_PlaceholderProviderClaimMappingIsReported(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}},
			{ID: "uuid-ops", Name: "sam_ops", Scopes: []string{"agent:ops:invoke"}},
		},
		ClaimMappings: []DBClaimMappingEntity{
			{OIDCProvider: ProviderSentinelEnterprise, ClaimKey: "groups", ClaimValue: "sam-users", RoleIDs: []string{"uuid-user"}},
			{OIDCProvider: "oidc", ClaimKey: "groups", ClaimValue: "sam-ops", RoleIDs: []string{"uuid-ops"}},
		},
	}
	yaml := YAMLState{ClaimOIDCProvider: "oidc", ClaimKey: "groups"}
	c := NewComposer(yaml, &staticSource{state: db}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("sub-alice", "webui", map[string]any{
		"oidc_provider": "oidc",
		"idp_claims":    map[string]any{"groups": []any{"sam-users", "sam-ops"}},
	})
	if !slices.Equal(res.Roles, []string{"sam_ops"}) {
		t.Errorf("roles = %v, want only [sam_ops]; the placeholder-provider mapping cannot match a token", res.Roles)
	}

	if got := strings.Count(buf.String(), msgInertClaimMapping); got != 1 {
		t.Fatalf("inert-mapping warnings = %d, want 1 (only the placeholder row); log:\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "claimValue=sam-users") {
		t.Errorf("warning must name the affected claim value; log:\n%s", buf.String())
	}
}

func TestComposer_SentinelConfiguredProviderIsReported(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db := &DBState{
		Roles: []DBRole{{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}}},
		ClaimMappings: []DBClaimMappingEntity{
			{OIDCProvider: ProviderSentinelEnterprise, ClaimKey: "groups", ClaimValue: "sam-users", RoleIDs: []string{"uuid-user"}},
		},
	}
	yaml := YAMLState{ClaimOIDCProvider: ProviderSentinelEnterprise, ClaimKey: "groups"}
	c := NewComposer(yaml, &staticSource{state: db}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("sub-alice", "webui", map[string]any{
		"oidc_provider": "oidc",
		"idp_claims":    map[string]any{"groups": []any{"sam-users"}},
	})
	if len(res.Roles) != 0 {
		t.Fatalf("roles = %v, want none; the sentinel cannot match a real token", res.Roles)
	}
	if got := strings.Count(buf.String(), msgSentinelConfiguredProvider); got != 1 {
		t.Errorf("deployment-level warnings = %d, want 1; log:\n%s", got, buf.String())
	}
}

func TestComposer_YAMLMappingsInheritSentinelConfig(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	yaml := YAMLState{
		Roles:             RolesConfig{Roles: map[string]RoleDefinition{"sam_admin": {Scopes: []string{"*"}}}},
		ClaimOIDCProvider: ProviderSentinelEnterprise,
		ClaimKey:          "groups",
		ClaimMappings: []DBClaimMapping{
			{OIDCProvider: ProviderSentinelEnterprise, ClaimKey: "groups", ClaimValue: "admins", RoleID: "sam_admin"},
			{OIDCProvider: ProviderSentinelEnterprise, ClaimKey: "groups", ClaimValue: "ops", RoleID: "sam_admin"},
		},
	}
	c := NewComposer(yaml, &staticSource{state: &DBState{}}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := strings.Count(buf.String(), msgSentinelConfiguredProvider); got != 1 {
		t.Fatalf("warnings = %d, want 1; log:\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "affectedClaimMappings=2") {
		t.Errorf("count must include YAML-declared mappings; log:\n%s", buf.String())
	}
}

func TestComposer_UnsetProviderWithWorkingMappingsIsQuiet(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db := &DBState{
		Roles: []DBRole{{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}}},
		ClaimMappings: []DBClaimMappingEntity{
			{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "g1", RoleIDs: []string{"uuid-user"}},
			{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "g2", RoleIDs: []string{"uuid-user"}},
		},
	}
	c := NewComposer(YAMLState{ClaimKey: "groups"}, &staticSource{state: db}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("sub-alice", "webui", map[string]any{
		"oidc_provider": "azure",
		"idp_claims":    map[string]any{"groups": []any{"g1"}},
	})
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Fatalf("roles = %v, want [sam_user]; these mappings resolve", res.Roles)
	}
	for _, msg := range []string{msgSentinelConfiguredProvider, msgInertClaimMapping} {
		if strings.Contains(buf.String(), msg) {
			t.Errorf("must not report working mappings; log:\n%s", buf.String())
		}
	}
}

func TestComposer_UnsetProviderWithUnsetMappingsResolves(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db := &DBState{
		Roles: []DBRole{{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}}},
		ClaimMappings: []DBClaimMappingEntity{
			{OIDCProvider: "", ClaimKey: "groups", ClaimValue: "g1", RoleIDs: []string{"uuid-user"}},
		},
	}
	c := NewComposer(YAMLState{ClaimKey: "groups"}, &staticSource{state: db}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("sub-alice", "webui", map[string]any{
		"idp_claims": map[string]any{"groups": []any{"g1"}},
	})
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Fatalf("roles = %v, want [sam_user]; this mapping resolves", res.Roles)
	}
	for _, msg := range []string{msgSentinelConfiguredProvider, msgInertClaimMapping} {
		if strings.Contains(buf.String(), msg) {
			t.Errorf("reported a mapping that grants; log:\n%s", buf.String())
		}
	}
}

func TestComposer_UnsetProviderWithoutClaimMappingsIsQuiet(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db := &DBState{Roles: []DBRole{{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}}}}
	c := NewComposer(YAMLState{}, &staticSource{state: db}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if strings.Contains(buf.String(), msgSentinelConfiguredProvider) {
		t.Errorf("must stay quiet with no claim mappings; log:\n%s", buf.String())
	}
}

func TestComposer_PlaceholderProviderMatchingDeploymentIsNotReported(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db := &DBState{
		Roles: []DBRole{{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}}},
		ClaimMappings: []DBClaimMappingEntity{
			{OIDCProvider: ProviderSentinelEnterprise, ClaimKey: "groups", ClaimValue: "sam-users", RoleIDs: []string{"uuid-user"}},
		},
	}
	yaml := YAMLState{ClaimOIDCProvider: ProviderSentinelEnterprise, ClaimKey: "groups"}
	c := NewComposer(yaml, &staticSource{state: db}, logger)
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("sub-alice", "webui", map[string]any{
		"oidc_provider": ProviderSentinelEnterprise,
		"idp_claims":    map[string]any{"groups": []any{"sam-users"}},
	})
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Errorf("roles = %v, want [sam_user]", res.Roles)
	}
	if strings.Contains(buf.String(), msgInertClaimMapping) {
		t.Errorf("must not report a provider that matches the deployment; log:\n%s", buf.String())
	}
	if got := strings.Count(buf.String(), msgSentinelConfiguredProvider); got != 1 {
		t.Errorf("deployment-level warnings = %d, want 1; log:\n%s", got, buf.String())
	}
}

func TestComposer_IdpClaimMappings_PerRowClaimKey(t *testing.T) {
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"*"}},
			{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}},
			{ID: "uuid-auditor", Name: "sam_auditor", Scopes: []string{"audit:_:read"}},
		},
		ClaimMappings: []DBClaimMappingEntity{
			{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "eng", RoleIDs: []string{"uuid-admin", "uuid-user"}},
			{OIDCProvider: "azure", ClaimKey: "departments", ClaimValue: "eng", RoleIDs: []string{"uuid-auditor"}},
		},
	}
	c := NewComposer(YAMLState{}, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("sub-alice", "webui", map[string]any{
		"oidc_provider": "azure",
		"idp_claims":    map[string]any{"groups": []any{"eng"}},
	})
	slices.Sort(res.Roles)
	if !slices.Equal(res.Roles, []string{"sam_admin", "sam_user"}) {
		t.Errorf("groups=eng roles = %v, want [sam_admin sam_user] (multi-role, groups key only)", res.Roles)
	}

	res, _ = c.Lookup("sub-bob", "webui", map[string]any{
		"oidc_provider": "azure",
		"idp_claims":    map[string]any{"roles": []any{"eng"}},
	})
	if len(res.Roles) != 0 {
		t.Errorf("roles=eng fired = %v, want empty (no mapping keyed on 'roles')", res.Roles)
	}

	res, _ = c.Lookup("sub-carol", "webui", map[string]any{
		"oidc_provider": "azure",
		"idp_claims":    map[string]any{"departments": []any{"eng"}},
	})
	if !slices.Equal(res.Roles, []string{"sam_auditor"}) {
		t.Errorf("departments=eng roles = %v, want [sam_auditor]", res.Roles)
	}
}

func TestComposer_RebuildSwapsAtomically(t *testing.T) {
	source := &staticSource{state: &DBState{
		Roles: []DBRole{{ID: "uuid-user", Name: "sam_user", Scopes: []string{"tool:_:read"}}},
		UserRoles: []DBUserRole{
			{UserID: "sub-alice", RoleID: "uuid-user"},
		},
	}}
	c := NewComposer(YAMLState{}, source, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	res, _ := c.Lookup("sub-alice", "webui", map[string]any{"sam_user_id": "sub-alice"})
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Errorf("before swap = %v", res.Roles)
	}

	source.state = &DBState{
		Roles: []DBRole{{ID: "uuid-admin", Name: "sam_admin", Scopes: []string{"*"}}},
		UserRoles: []DBUserRole{
			{UserID: "sub-alice", RoleID: "uuid-admin"},
		},
	}
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild after swap: %v", err)
	}
	res, _ = c.Lookup("sub-alice", "webui", map[string]any{"sam_user_id": "sub-alice"})
	if !slices.Equal(res.Roles, []string{"sam_admin"}) {
		t.Errorf("after swap = %v, want [sam_admin]", res.Roles)
	}
}

func TestComposer_NotInitialized(t *testing.T) {
	c := NewComposer(YAMLState{}, nil, newTestLogger())
	_, err := c.Lookup("sub-alice", "webui", nil)
	if err == nil {
		t.Fatal("expected error when Rebuild was never called")
	}
}

func TestComposer_DBRoleMissingNameRejected(t *testing.T) {
	db := &DBState{Roles: []DBRole{{ID: "uuid-x", Name: ""}}}
	c := NewComposer(YAMLState{}, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err == nil {
		t.Fatal("expected rebuild to reject a DB role with empty name")
	}
}

func TestComposer_ScopesForRoleIDs(t *testing.T) {
	db := &DBState{
		Roles: []DBRole{
			{ID: "uuid-base", Name: "base", Scopes: []string{"tool:_:read"}},
			{ID: "uuid-child", Name: "child", Scopes: []string{"agent:*:read"}, Parents: []string{"uuid-base"}},
		},
	}
	c := NewComposer(YAMLState{}, &staticSource{state: db}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	got, err := c.ScopesForRoleIDs([]string{"uuid-child"})
	if err != nil {
		t.Fatalf("ScopesForRoleIDs: %v", err)
	}
	slices.Sort(got)
	if want := []string{"agent:*:read", "tool:_:read"}; !slices.Equal(got, want) {
		t.Errorf("child effective scopes = %v, want %v (direct + inherited)", got, want)
	}

	if _, err := c.ScopesForRoleIDs([]string{"uuid-unknown"}); err == nil {
		t.Error("unknown role id returned nil error, want fail-closed error")
	}

	if _, err := c.ScopesForRoleIDs([]string{"uuid-child", "uuid-unknown"}); err == nil {
		t.Error("set containing an unknown role id returned nil error, want fail-closed error")
	}

	fresh := NewComposer(YAMLState{}, &staticSource{state: db}, newTestLogger())
	if _, err := fresh.ScopesForRoleIDs([]string{"uuid-child"}); err == nil {
		t.Error("pre-rebuild ScopesForRoleIDs returned nil error, want fail-closed error")
	}
}

func TestComposer_ResolutionSaysWhetherTheRolesAreTheDeployedDefaults(t *testing.T) {
	yaml := YAMLState{
		Roles: RolesConfig{Roles: map[string]RoleDefinition{
			"sam_admin": {Scopes: []string{"*"}},
			"sam_user":  {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: UsersConfig{Users: map[string]UserAssignment{
			"alice@example.org": {Roles: []string{"sam_admin"}},
		}},
		DefaultRoles: []string{"sam_user"},
	}
	c := NewComposer(yaml, &staticSource{state: systemUserDBState()}, newTestLogger())
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	for _, tc := range []struct {
		name      string
		principal string
		want      bool
	}{
		{"a user the YAML assigns a role", "alice@example.org", false},
		{"a user nothing matches", "bob@example.org", true},
		{"a service account with a grant", DefaultSystemUserSub, false},
		{"a service account with no grant", "system:unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := c.Lookup(tc.principal, "webui", nil)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if res.DefaultRoles != tc.want {
				t.Errorf("DefaultRoles = %v, want %v (roles %v)", res.DefaultRoles, tc.want, res.Roles)
			}
		})
	}
}
