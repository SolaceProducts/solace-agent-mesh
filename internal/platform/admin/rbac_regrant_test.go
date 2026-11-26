// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func createRoleWithScopes(ctx context.Context, t *testing.T, svc *RBACService, name string, scopes, inherits []string) string {
	t.Helper()
	resp, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: name, Scopes: scopes, Inherits: inherits}, "seed", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole %q: %v", name, err)
	}
	return resp.ID
}

func assertForbidden(t *testing.T, err error, wantScopeSubstr string) {
	t.Helper()
	var fe *ForbiddenError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want *ForbiddenError", err)
	}
	if wantScopeSubstr != "" && !strings.Contains(fe.Message, wantScopeSubstr) {
		t.Errorf("forbidden message = %q, want it to name missing scope %q", fe.Message, wantScopeSubstr)
	}
}

func TestRegrant_Grant_AllowedWhenActorHoldsConferredScopes(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "reader", []string{"agent:*:read"}, nil)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-1"},
		RoleID:  roleID,
	}, "admin", []string{"agent:*:read"})
	if err != nil {
		t.Fatalf("CreateGrant with sufficient scopes: %v", err)
	}
}

func TestRegrant_Grant_DeniedWhenActorMissingAConferredScope(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "power", []string{"agent:*:read", "tool:_:read"}, nil)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-1"},
		RoleID:  roleID,
	}, "admin", []string{"agent:*:read"})
	assertForbidden(t, err, "tool:_:read")
}

func TestRegrant_Grant_WildcardActorMayGrantAnything(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "super", []string{"agent:*:read", "rbac:_:create", "tool:_:read"}, nil)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-1"},
		RoleID:  roleID,
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("wildcard actor denied: %v", err)
	}
}

func TestRegrant_Grant_HeldWildcardSatisfiesInstanceScope(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "invoker", []string{"agent:11111111-1111-1111-1111-111111111111:invoke"}, nil)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-1"},
		RoleID:  roleID,
	}, "admin", []string{"agent:*:invoke"})
	if err != nil {
		t.Fatalf("held agent:*:invoke should satisfy a concrete instance scope: %v", err)
	}
}

func TestRegrant_Grant_HeldCollectionSentinelSatisfiesWildcardScope(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "connector_reader", []string{"connector:*:read"}, nil)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-1"},
		RoleID:  roleID,
	}, "admin", []string{"connector:_:*"})
	if err != nil {
		t.Fatalf("held connector:_:* should satisfy connector:*:read: %v", err)
	}
}

func TestRegrant_Grant_ChecksInheritedScopes(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	baseID := createRoleWithScopes(ctx, t, svc, "base", []string{"tool:_:read"}, nil)
	childID := createRoleWithScopes(ctx, t, svc, "child", []string{"agent:*:read"}, []string{baseID})
	seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-1"},
		RoleID:  childID,
	}, "admin", []string{"agent:*:read"})
	assertForbidden(t, err, "tool:_:read")
}

func TestRegrant_Grant_DeniedWhenActorHoldsNoScopes(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "reader", []string{"agent:*:read"}, nil)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-1"},
		RoleID:  roleID,
	}, "admin", nil)
	assertForbidden(t, err, "agent:*:read")
}

func TestRegrant_ClaimMapping_EnforcesHeldScopes(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "reader", []string{"agent:*:read"}, nil)

	req := CreateRBACClaimMappingRequest{
		Name:         "Engineers",
		OIDCProvider: "azure",
		ClaimKey:     "groups",
		ClaimValue:   "eng",
		RoleIDs:      []string{roleID},
	}

	if _, err := svc.CreateClaimMapping(ctx, req, "admin", nil); err == nil {
		t.Fatal("claim mapping created despite actor holding no scopes")
	} else {
		assertForbidden(t, err, "agent:*:read")
	}

	if _, err := svc.CreateClaimMapping(ctx, req, "admin", []string{"agent:*:read"}); err != nil {
		t.Fatalf("claim mapping denied despite sufficient scopes: %v", err)
	}
}

func TestRegrant_DefaultRoles_EnforcesHeldScopes(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "reader", []string{"agent:*:read"}, nil)

	if err := svc.SetDefaultRoles(ctx, []string{roleID}, "admin", nil); err == nil {
		t.Fatal("default role set despite actor holding no scopes")
	} else {
		assertForbidden(t, err, "agent:*:read")
	}

	if err := svc.SetDefaultRoles(ctx, []string{roleID}, "admin", []string{"agent:*:read"}); err != nil {
		t.Fatalf("default role denied despite sufficient scopes: %v", err)
	}

	if err := svc.SetDefaultRoles(ctx, nil, "admin", nil); err == nil {
		t.Fatal("cleared the default set while holding none of the scopes it removed")
	} else {
		assertForbidden(t, err, "agent:*:read")
	}

	if err := svc.SetDefaultRoles(ctx, nil, "admin", []string{"agent:*:read"}); err != nil {
		t.Fatalf("clearing while holding the removed scope was refused: %v", err)
	}
}

func TestRegrant_SuperadminMayConferEitherUniversalSpelling(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)

	if _, err := svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name: "legacy_admin", Scopes: []string{"*"},
	}, "root", []string{"*:*:*"}); err != nil {
		t.Fatalf("actor holding *:*:* refused a role authored as bare *: %v", err)
	}
	if _, err := svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name: "canonical_admin", Scopes: []string{"*:*:*"},
	}, "root", []string{"*"}); err != nil {
		t.Fatalf("actor holding bare * refused a role authored as *:*:*: %v", err)
	}

	_, err := svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name: "escalation", Scopes: []string{"*"},
	}, "mallory", []string{"agent:*:*"})
	assertForbidden(t, err, "*")
}

func TestRegrant_RefusalNamesTheDirectionOfTheChange(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)

	granting := createRoleWithScopes(ctx, t, svc, "granting", []string{"tool:_:read"}, nil)
	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "grantee@example.com"},
		RoleID:  granting,
	}, "mallory", nil)
	assertForbiddenVerb(t, err, "cannot grant")

	removing := createRoleWithScopes(ctx, t, svc, "removing", []string{"tool:_:read"}, nil)
	assertForbiddenVerb(t, svc.DeleteRole(ctx, removing, "mallory", nil), "cannot remove")

	both := createRoleWithScopes(ctx, t, svc, "both", []string{"tool:_:read"}, nil)
	_, err = svc.UpdateRole(ctx, both, UpdateRBACRoleRequest{
		Name: "both", Scopes: []string{"agent_builder:*:update"},
	}, "mallory", nil)
	assertForbiddenVerb(t, err, "cannot change")
}

func assertForbiddenVerb(t *testing.T, err error, wantVerb string) {
	t.Helper()
	var fe *ForbiddenError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want *ForbiddenError", err)
	}
	if !strings.Contains(strings.ToLower(fe.Message), wantVerb) {
		t.Errorf("message = %q, want it to say %q", fe.Message, wantVerb)
	}
}

func TestRegrant_UpdateClaimMapping_EnforcesTheDelta(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	readerID := createRoleWithScopes(ctx, t, svc, "reader", []string{"agent:*:read"}, nil)
	privID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)

	mapping, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "eng", RoleIDs: []string{readerID},
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("seed claim mapping: %v", err)
	}

	rename := UpdateRBACClaimMappingRequest{
		Name: "Engineering", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "eng", RoleIDs: []string{readerID},
	}
	if _, err = svc.UpdateClaimMapping(ctx, mapping.ID, rename, "admin", nil); err != nil {
		t.Fatalf("rename with an unchanged role set was refused: %v", err)
	}

	addPriv := UpdateRBACClaimMappingRequest{
		Name: "Engineering", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "eng",
		RoleIDs: []string{readerID, privID},
	}
	_, err = svc.UpdateClaimMapping(ctx, mapping.ID, addPriv, "admin", []string{"agent:*:read"})
	assertForbidden(t, err, "tool:_:read")

	if _, err := svc.UpdateClaimMapping(ctx, mapping.ID, addPriv, "admin", []string{"agent:*:read", "tool:_:read"}); err != nil {
		t.Fatalf("adding a held role was refused: %v", err)
	}
}

func TestRegrant_UpdateClaimMapping_SubjectChangeGuardsWholeSet(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)

	mapping, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "engineering", RoleIDs: []string{roleID},
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("seed claim mapping: %v", err)
	}

	repoint := UpdateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "everyone",
		RoleIDs: []string{roleID},
	}
	_, err = svc.UpdateClaimMapping(ctx, mapping.ID, repoint, "mallory", nil)
	assertForbidden(t, err, "tool:_:read")

	if _, err := svc.UpdateClaimMapping(ctx, mapping.ID, repoint, "admin", []string{"tool:_:read"}); err != nil {
		t.Fatalf("repointing while holding the conferred scope was refused: %v", err)
	}
}

func TestRegrant_UpdateClaimMapping_ProviderChangeAloneGuardsWholeSet(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)

	mapping, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "engineering", RoleIDs: []string{roleID},
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("seed claim mapping: %v", err)
	}

	repoint := UpdateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "okta", ClaimKey: "groups", ClaimValue: "engineering",
		RoleIDs: []string{roleID},
	}
	_, err = svc.UpdateClaimMapping(ctx, mapping.ID, repoint, "mallory", nil)
	assertForbidden(t, err, "tool:_:read")

	if _, err := svc.UpdateClaimMapping(ctx, mapping.ID, repoint, "admin", []string{"tool:_:read"}); err != nil {
		t.Fatalf("provider repoint while holding the conferred scope was refused: %v", err)
	}
}

func TestRegrant_UpdateClaimMapping_ClaimKeyChangeAloneGuardsWholeSet(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)

	mapping, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "engineering", RoleIDs: []string{roleID},
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("seed claim mapping: %v", err)
	}

	repoint := UpdateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "roles", ClaimValue: "engineering",
		RoleIDs: []string{roleID},
	}
	_, err = svc.UpdateClaimMapping(ctx, mapping.ID, repoint, "mallory", nil)
	assertForbidden(t, err, "tool:_:read")

	if _, err := svc.UpdateClaimMapping(ctx, mapping.ID, repoint, "admin", []string{"tool:_:read"}); err != nil {
		t.Fatalf("claim-key repoint while holding the conferred scope was refused: %v", err)
	}
}

func TestRegrant_UpdateClaimMapping_SubjectChangeGuardsTheRemovalToo(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	privID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)
	viewerID := createRoleWithScopes(ctx, t, svc, "viewer", []string{"agent:*:read"}, nil)

	mapping, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "engineering", RoleIDs: []string{privID},
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("seed claim mapping: %v", err)
	}

	repoint := UpdateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "nobody",
		RoleIDs: []string{viewerID},
	}
	mallory := []string{"rbac:_:update", "agent:*:read"}
	_, err = svc.UpdateClaimMapping(ctx, mapping.ID, repoint, "mallory", mallory)
	assertForbidden(t, err, "tool:_:read")

	assertForbidden(t, svc.DeleteClaimMapping(ctx, mapping.ID, "mallory", mallory), "tool:_:read")

	if _, err := svc.UpdateClaimMapping(ctx, mapping.ID, repoint, "admin", append(mallory, "tool:_:read")); err != nil {
		t.Fatalf("repoint while holding both sides was refused: %v", err)
	}
}

func TestRegrant_RepointDenialAuditsTheRefusedRoleSet(t *testing.T) {
	svc, _, logs := newRegrantServiceWithLogs(t, true)
	ctx := t.Context()
	privID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)
	viewerID := createRoleWithScopes(ctx, t, svc, "viewer", []string{"agent:*:read"}, nil)

	mapping, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups",
		ClaimValue: "engineering", RoleIDs: []string{privID},
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("seed claim mapping: %v", err)
	}
	logs.Reset()

	_, err = svc.UpdateClaimMapping(ctx, mapping.ID, UpdateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups",
		ClaimValue: "nobody", RoleIDs: []string{viewerID},
	}, "mallory", []string{"rbac:_:update", "agent:*:read"})
	assertForbidden(t, err, "tool:_:read")

	rec := findAuditRecord(t, logs, auditActionRejected)
	got, _ := rec["roleIds"].(string)
	if !strings.Contains(got, privID) {
		t.Errorf("denial audit roleIds = %q, want the refused set %q", got, privID)
	}
	if strings.Contains(got, viewerID) {
		t.Errorf("denial audit roleIds = %q, must not name the proposed set %q", got, viewerID)
	}
}

func TestRegrant_SetUserRoleGrants_EnforcesHeldScopes(t *testing.T) {
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()
	role := &store.RBACRole{Name: "reader", CreatedBy: "seed", UpdatedBy: "seed"}
	if err := rbacRepo.CreateRole(ctx, role, []string{"agent:*:read"}, nil); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	uid := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "{}", nil)

	if _, err := svc.SetUserRoleGrants(ctx, uid, []string{role.ID}, "admin", nil); err == nil {
		t.Fatal("SetUserRoleGrants allowed conferring a scope the actor lacks")
	} else {
		assertForbidden(t, err, "agent:*:read")
	}

	if _, err := svc.SetUserRoleGrants(ctx, uid, []string{role.ID}, "admin", []string{"agent:*:read"}); err != nil {
		t.Fatalf("SetUserRoleGrants denied despite sufficient scopes: %v", err)
	}
}

func newRegrantServiceWithLogs(t *testing.T, rebuild bool) (*RBACService, *store.RBACRepo, *bytes.Buffer) {
	t.Helper()
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	if rebuild {
		if err := composer.Rebuild(t.Context()); err != nil {
			t.Fatalf("rebuild: %v", err)
		}
	}
	svc := NewRBACService(repo, composer, nil, nil, logger)
	svc.SetUsersRepo(store.NewUsersRepo(db))
	return svc, repo, &logs
}

func seedRoleUnseenByComposer(ctx context.Context, t *testing.T, repo *store.RBACRepo, name string, scopes []string) string {
	t.Helper()
	role := &store.RBACRole{Name: name, CreatedBy: "seed", UpdatedBy: "seed"}
	if err := repo.CreateRole(ctx, role, scopes, nil); err != nil {
		t.Fatalf("seed role %q: %v", name, err)
	}
	return role.ID
}

func findAuditRecord(t *testing.T, logs *bytes.Buffer, action string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	for {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("no rbac_mutation record with action=%q in logs:\n%s", action, logs.String())
		}
		if rec["event"] == "rbac_mutation" && rec["action"] == action {
			return rec
		}
	}
}

func findLogRecord(t *testing.T, logs *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	for {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("no record with msg=%q in logs:\n%s", msg, logs.String())
		}
		if rec["msg"] == msg {
			return rec
		}
	}
}

func TestRegrant_DeniedAttemptIsAudited(t *testing.T) {
	svc, _, logs := newRegrantServiceWithLogs(t, true)
	ctx := t.Context()
	roleID := createRoleWithScopes(ctx, t, svc, "power", []string{"tool:_:read"}, nil)

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "grantee@example.com"},
		RoleID:  roleID,
	}, "mallory@example.com", []string{"agent:*:read"})
	assertForbidden(t, err, "tool:_:read")

	rec := findAuditRecord(t, logs, auditActionRejected)
	if rec["level"] != "WARN" {
		t.Errorf("denial audit level = %v, want WARN (security rejection)", rec["level"])
	}
	if rec["actor"] != "mallory@example.com" {
		t.Errorf("denial audit actor = %v, want mallory@example.com", rec["actor"])
	}
	if rec["resource"] != auditResourceGrant {
		t.Errorf("denial audit resource = %v, want %q", rec["resource"], auditResourceGrant)
	}
	if got, ok := rec["roleIds"].(string); !ok || !strings.Contains(got, roleID) {
		t.Errorf("denial audit roleIds = %v, want it to name %q", rec["roleIds"], roleID)
	}
	if got, ok := rec["missingScopes"].(string); !ok || !strings.Contains(got, "tool:_:read") {
		t.Errorf("denial audit missingScopes = %v, want it to name tool:_:read", rec["missingScopes"])
	}
}

func TestRegrant_UnresolvableConferredSetIsRefused(t *testing.T) {
	svc, repo, logs := newRegrantServiceWithLogs(t, false)
	ctx := t.Context()
	roleID := seedRoleUnseenByComposer(ctx, t, repo, "reader", []string{"agent:*:read"})

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "grantee@example.com"},
		RoleID:  roleID,
	}, "admin", []string{"*"})

	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %v, want *UnavailableError", err)
	}
	if unavailable.Unwrap() == nil {
		t.Error("UnavailableError carries no cause; the operator loses the reason")
	}
	rec := findLogRecord(t, logs, "re-grant guard cannot resolve conferred scopes; refusing the write")
	if rec["level"] != "ERROR" {
		t.Errorf("unresolvable-refusal level = %v, want ERROR (it does not self-heal)", rec["level"])
	}
	if strings.Contains(unavailable.Message, "retry") {
		t.Errorf("message promises a retry that may never succeed: %q", unavailable.Message)
	}
}

func TestRegrant_RoleAbsentFromSnapshotIsNotAdmitted(t *testing.T) {
	svc, repo, _ := newRegrantServiceWithLogs(t, true)
	ctx := t.Context()
	roleID := seedRoleUnseenByComposer(ctx, t, repo, "power", []string{"tool:_:read", "rbac:_:create"})

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "grantee@example.com"},
		RoleID:  roleID,
	}, "mallory@example.com", nil)

	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %v, want *UnavailableError (a stale snapshot must not confer)", err)
	}
}

func TestRegrant_UpdateRole_StaleSnapshotCannotRestoreADroppedScope(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	ctx := t.Context()

	newReplica := func() *RBACService {
		t.Helper()
		composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
		if err := composer.Rebuild(ctx); err != nil {
			t.Fatalf("rebuild: %v", err)
		}
		return NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	}

	applier := newReplica()
	roleID := createRoleWithScopes(ctx, t, applier, "team", []string{"agent:*:read", "tool:_:read"}, nil)

	stale := newReplica()

	if _, err := applier.UpdateRole(ctx, roleID, UpdateRBACRoleRequest{
		Name: "team", Scopes: []string{"agent:*:read"},
	}, "owner", []string{"*"}); err != nil {
		t.Fatalf("legitimate removal: %v", err)
	}

	_, err := stale.UpdateRole(ctx, roleID, UpdateRBACRoleRequest{
		Name: "team", Scopes: []string{"agent:*:read", "tool:_:read"},
	}, "mallory", []string{"rbac:_:update", "agent:*:read"})
	assertForbidden(t, err, "tool:_:read")
}

func TestRegrant_NoOpWriteSurvivesUnreadyResolver(t *testing.T) {
	svc, _, _ := newRegrantServiceWithLogs(t, false)

	if err := svc.SetDefaultRoles(t.Context(), nil, "admin", nil); err != nil {
		t.Fatalf("no-op default-roles write with an unready resolver: %v", err)
	}
}

func TestRegrant_UpdateRole_CannotWidenBeyondHeldScopes(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "team_lead",
		[]string{"rbac:_:update", "agent:*:invoke"}, nil)

	carol := []string{"rbac:_:update", "agent:*:invoke"}
	_, err := svc.UpdateRole(ctx, roleID, UpdateRBACRoleRequest{
		Name:   "team_lead",
		Scopes: []string{"*"},
	}, "carol@example.com", carol)
	assertForbidden(t, err, "*")
}

func TestRegrant_UpdateRole_CannotInheritBeyondHeldScopes(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	powerful := createRoleWithScopes(ctx, t, svc, "powerful", []string{"agent_builder:*:*"}, nil)
	roleID := createRoleWithScopes(ctx, t, svc, "team_lead", []string{"rbac:_:update"}, nil)

	_, err := svc.UpdateRole(ctx, roleID, UpdateRBACRoleRequest{
		Name:     "team_lead",
		Scopes:   []string{"rbac:_:update"},
		Inherits: []string{powerful},
	}, "carol@example.com", []string{"rbac:_:update"})
	assertForbidden(t, err, "agent_builder:*:*")
}

func TestRegrant_UpdateRole_CannotRemoveUnheldScopes(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "mixed",
		[]string{"rbac:_:update", "tool:_:read"}, nil)

	_, err := svc.UpdateRole(ctx, roleID, UpdateRBACRoleRequest{
		Name:   "mixed",
		Scopes: []string{"rbac:_:update"},
	}, "carol@example.com", []string{"rbac:_:update"})
	assertForbidden(t, err, "tool:_:read")
}

func TestRegrant_UpdateRole_MayReshapeHeldScopesAlongsideUnheldOnes(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "mixed",
		[]string{"agent:*:invoke", "tool:_:read"}, nil)

	if _, err := svc.UpdateRole(ctx, roleID, UpdateRBACRoleRequest{
		Name:   "mixed",
		Scopes: []string{"agent:*:read", "tool:_:read"},
	}, "carol@example.com", []string{"agent:*:invoke", "agent:*:read"}); err != nil {
		t.Fatalf("reshaping held scopes around an untouched one was refused: %v", err)
	}
}

func TestRegrant_CreateRole_CannotSeedUnheldScopes(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	_, err := svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name:   "godmode",
		Scopes: []string{"*"},
	}, "carol@example.com", []string{"rbac:_:create"})
	assertForbidden(t, err, "*")
}

func TestRegrant_SetUserRoleGrants_PartialRevokeIsAllowed(t *testing.T) {
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	adminHelper := &store.RBACRole{Name: "admin_helper", CreatedBy: "s", UpdatedBy: "s"}
	if err := rbacRepo.CreateRole(ctx, adminHelper, []string{"tool:_:read"}, nil); err != nil {
		t.Fatalf("seed admin_helper: %v", err)
	}
	tempContractor := &store.RBACRole{Name: "temp_contractor", CreatedBy: "s", UpdatedBy: "s"}
	if err := rbacRepo.CreateRole(ctx, tempContractor, []string{"agent:*:invoke"}, nil); err != nil {
		t.Fatalf("seed temp_contractor: %v", err)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	bob := seedUserRow(ctx, t, usersRepo, "s-bob", "Bob", "bob@example.com", "azure", "{}", nil)

	if _, err := svc.SetUserRoleGrants(ctx, bob, []string{adminHelper.ID, tempContractor.ID}, "setup", []string{"*"}); err != nil {
		t.Fatalf("seed Bob's roles: %v", err)
	}

	dave := []string{"agent:*:invoke"}
	if _, err := svc.SetUserRoleGrants(ctx, bob, []string{adminHelper.ID}, "dave", dave); err != nil {
		t.Fatalf("partial revoke of a held role was refused: %v", err)
	}

	_, err := svc.SetUserRoleGrants(ctx, bob, nil, "dave", dave)
	assertForbidden(t, err, "tool:_:read")
}

func TestRegrant_SetUserRoleGrants_OverlappingScopeIsNotADelta(t *testing.T) {
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleA := &store.RBACRole{Name: "a", CreatedBy: "s", UpdatedBy: "s"}
	if err := rbacRepo.CreateRole(ctx, roleA, []string{"tool:_:read"}, nil); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	roleB := &store.RBACRole{Name: "b", CreatedBy: "s", UpdatedBy: "s"}
	if err := rbacRepo.CreateRole(ctx, roleB, []string{"tool:_:read"}, nil); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	bob := seedUserRow(ctx, t, usersRepo, "s-bob", "Bob", "bob@example.com", "azure", "{}", nil)

	if _, err := svc.SetUserRoleGrants(ctx, bob, []string{roleA.ID, roleB.ID}, "setup", []string{"*"}); err != nil {
		t.Fatalf("seed Bob's roles: %v", err)
	}
	if _, err := svc.SetUserRoleGrants(ctx, bob, []string{roleA.ID}, "dave", nil); err != nil {
		t.Fatalf("dropping a redundant role should confer and revoke nothing: %v", err)
	}
}

func TestRegrant_DeleteRole_RequiresHoldingWhatItGrants(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "admin_helper",
		[]string{"tool:_:read", "agent:*:invoke"}, nil)

	err := svc.DeleteRole(ctx, roleID, "dave", []string{"agent:*:invoke"})
	assertForbidden(t, err, "tool:_:read")

	if err := svc.DeleteRole(ctx, roleID, "dave", []string{"tool:_:read", "agent:*:invoke"}); err != nil {
		t.Fatalf("delete while holding every conferred scope was refused: %v", err)
	}
}

func TestRegrant_DeleteGrant_MeasuredAgainstTheUsersRemainingRoles(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	privID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)
	tempID := createRoleWithScopes(ctx, t, svc, "temp", []string{"agent:*:invoke"}, nil)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-bob", "bob@example.com")

	subject := GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-bob"}
	privGrant, err := svc.CreateGrant(ctx, CreateGrantRequest{Subject: subject, RoleID: privID}, "setup", []string{"*"})
	if err != nil {
		t.Fatalf("seed priv grant: %v", err)
	}
	tempGrant, err := svc.CreateGrant(ctx, CreateGrantRequest{Subject: subject, RoleID: tempID}, "setup", []string{"*"})
	if err != nil {
		t.Fatalf("seed temp grant: %v", err)
	}

	dave := []string{"agent:*:invoke"}
	assertForbidden(t, svc.DeleteGrant(ctx, privGrant.ID, "dave", dave), "tool:_:read")

	if err := svc.DeleteGrant(ctx, tempGrant.ID, "dave", dave); err != nil {
		t.Fatalf("revoking a held role was refused: %v", err)
	}
}

func TestRegrant_DeleteClaimMapping_RequiresHoldingWhatItConferred(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)

	mapping, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "eng", RoleIDs: []string{roleID},
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	assertForbidden(t, svc.DeleteClaimMapping(ctx, mapping.ID, "dave", nil), "tool:_:read")

	if err := svc.DeleteClaimMapping(ctx, mapping.ID, "dave", []string{"tool:_:read"}); err != nil {
		t.Fatalf("delete while holding the conferred scope was refused: %v", err)
	}
}

func TestRegrant_DeleteGrant_PendingRequiresHoldingTheRole(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)

	pending, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "nobody@example.com"},
		RoleID:  roleID,
	}, "setup", []string{"*"})
	if err != nil {
		t.Fatalf("seed pending grant: %v", err)
	}

	assertForbidden(t, svc.DeleteGrant(ctx, pending.ID, "dave", nil), "tool:_:read")
}
