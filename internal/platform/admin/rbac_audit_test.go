// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newRBACTestServiceWithAuditLog(t *testing.T) (*RBACService, *bytes.Buffer) {
	t.Helper()
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	if err := composer.Rebuild(context.Background()); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	svc := NewRBACService(repo, composer, nil, nil, logger)
	svc.SetUsersRepo(store.NewUsersRepo(db))
	return svc, buf
}

func findAuditLine(t *testing.T, buf *bytes.Buffer, action, resource string) map[string]any {
	t.Helper()
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["event"] == "rbac_mutation" && rec["action"] == action && rec["resource"] == resource {
			return rec
		}
	}
	t.Fatalf("no audit line for action=%s resource=%s; buffer:\n%s", action, resource, buf.String())
	return nil
}

func TestRBACAudit_RoleLifecycle(t *testing.T) {
	svc, buf := newRBACTestServiceWithAuditLog(t)
	ctx := context.Background()

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name:   "auditor",
		Scopes: []string{"rbac:_:read"},
	}, "admin@example.org", []string{"*"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	rec := findAuditLine(t, buf, auditActionCreated, auditResourceRole)
	if rec["resourceId"] != role.ID {
		t.Errorf("create audit resourceId = %v, want %s", rec["resourceId"], role.ID)
	}
	if rec["actor"] != "admin@example.org" {
		t.Errorf("create audit actor = %v, want admin@example.org", rec["actor"])
	}

	buf.Reset()
	_, err = svc.UpdateRole(ctx, role.ID, UpdateRBACRoleRequest{
		Name:   "auditor",
		Scopes: []string{"rbac:_:read", "rbac:_:update"},
	}, "editor@example.org", []string{"*"})
	if err != nil {
		t.Fatalf("update role: %v", err)
	}
	rec = findAuditLine(t, buf, auditActionUpdated, auditResourceRole)
	if rec["actor"] != "editor@example.org" {
		t.Errorf("update audit actor = %v", rec["actor"])
	}

	buf.Reset()
	if err := svc.DeleteRole(ctx, role.ID, "deleter@example.org", []string{"*"}); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	rec = findAuditLine(t, buf, auditActionDeleted, auditResourceRole)
	if rec["actor"] != "deleter@example.org" {
		t.Errorf("delete audit actor = %v", rec["actor"])
	}
}

func TestRBACAudit_GrantLifecycle(t *testing.T) {
	svc, buf := newRBACTestServiceWithAuditLog(t)
	ctx := context.Background()

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name:   "viewer",
		Scopes: []string{"rbac:_:read"},
	}, "admin@example.org", []string{"*"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	buf.Reset()
	grant, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectLegacy, Value: "alice@example.org"},
		RoleID:  role.ID,
	}, "admin@example.org", []string{"*"})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	rec := findAuditLine(t, buf, auditActionCreated, auditResourceGrant)
	if rec["resourceId"] != grant.ID {
		t.Errorf("create grant audit resourceId = %v, want %s", rec["resourceId"], grant.ID)
	}

	buf.Reset()
	if err := svc.DeleteGrant(ctx, grant.ID, "admin@example.org", []string{"*"}); err != nil {
		t.Fatalf("delete grant: %v", err)
	}
	findAuditLine(t, buf, auditActionDeleted, auditResourceGrant)
}

func TestRBACAudit_ClaimMappingLifecycle(t *testing.T) {
	svc, buf := newRBACTestServiceWithAuditLog(t)
	ctx := context.Background()

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name:   "claim_holder",
		Scopes: []string{"rbac:_:read"},
	}, "admin@example.org", []string{"*"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	buf.Reset()
	mapping, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name:         "Viewers",
		OIDCProvider: "okta",
		ClaimValue:   "viewers",
		RoleIDs:      []string{role.ID},
	}, "admin@example.org", []string{"*"})
	if err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	findAuditLine(t, buf, auditActionCreated, auditResourceClaimMapping)

	buf.Reset()
	_, err = svc.UpdateClaimMapping(ctx, mapping.ID, UpdateRBACClaimMappingRequest{
		Name:         "Viewers",
		OIDCProvider: "okta",
		ClaimValue:   "viewers",
		RoleIDs:      []string{role.ID},
	}, "admin@example.org", []string{"*"})
	if err != nil {
		t.Fatalf("update mapping: %v", err)
	}
	findAuditLine(t, buf, auditActionUpdated, auditResourceClaimMapping)

	buf.Reset()
	if err := svc.DeleteClaimMapping(ctx, mapping.ID, "admin@example.org", []string{"*"}); err != nil {
		t.Fatalf("delete mapping: %v", err)
	}
	findAuditLine(t, buf, auditActionDeleted, auditResourceClaimMapping)
}

func TestRBACAudit_NoEntryOnFailure(t *testing.T) {
	svc, buf := newRBACTestServiceWithAuditLog(t)
	ctx := context.Background()

	_, err := svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name:   "dupe",
		Scopes: []string{"rbac:_:read"},
	}, "admin@example.org", []string{"*"})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	buf.Reset()
	_, err = svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name:   "dupe",
		Scopes: []string{"rbac:_:read"},
	}, "admin@example.org", []string{"*"})
	if err == nil {
		t.Fatal("expected duplicate-name conflict")
	}
	if strings.Contains(buf.String(), "rbac_mutation") {
		t.Fatalf("audit line emitted for failed create:\n%s", buf.String())
	}
}
