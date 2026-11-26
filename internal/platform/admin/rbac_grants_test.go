// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestRBACService_CreateSystemUser(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	svc.SetUsersRepo(usersRepo)
	ctx := context.Background()
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "orchestrator_only", Scopes: []string{"agent:Orchestrator:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	resp, err := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "helpdesk", RoleIDs: []string{role.ID}}, "tester")
	if err != nil {
		t.Fatalf("CreateSystemUser: %v", err)
	}
	if resp.Sub != "system:helpdesk" {
		t.Errorf("sub = %q, want system:helpdesk", resp.Sub)
	}

	res, err := composer.Lookup("system:helpdesk", "webhook", nil)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !slices.Contains(res.Scopes, "agent:Orchestrator:invoke") {
		t.Errorf("system:helpdesk scopes = %v, want to contain agent:Orchestrator:invoke (concrete grant resolved)", res.Scopes)
	}

	if _, err = svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "helpdesk", RoleIDs: []string{role.ID}}, "tester"); err != nil {
		t.Fatalf("re-apply CreateSystemUser should be idempotent: %v", err)
	}

	role2, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "reporter_only", Scopes: []string{"agent:Reporter:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole role2: %v", err)
	}
	if _, err = svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "helpdesk", RoleIDs: []string{role.ID, role2.ID}}, "tester"); err != nil {
		t.Fatalf("widen re-apply: %v", err)
	}
	narrowed, err := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "helpdesk", RoleIDs: []string{role2.ID}}, "tester")
	if err != nil {
		t.Fatalf("narrow re-apply: %v", err)
	}
	if !slices.Contains(narrowed.RoleIDs, role.ID) || !slices.Contains(narrowed.RoleIDs, role2.ID) {
		t.Errorf("narrow re-apply reported RoleIDs = %v, want the effective union of %q and %q (grants are never revoked)", narrowed.RoleIDs, role.ID, role2.ID)
	}
}

func TestRBACService_ListSystemUsers(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)

	got, err := svc.ListSystemUsers(ctx)
	if err != nil {
		t.Fatalf("ListSystemUsers: %v", err)
	}
	if len(got) != 1 || got[0].Sub != rbac.DefaultSystemUserSub || got[0].Name != "default" {
		t.Fatalf("baseline = %+v, want only the seeded default (name=default)", got)
	}
	if len(got[0].RoleIDs) == 0 || !slices.Contains(got[0].RoleIDs, rbac.RoleSystemInvokeAll) {
		t.Errorf("default roleIds = %v, want to contain %q from embedded defaults", got[0].RoleIDs, rbac.RoleSystemInvokeAll)
	}

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "orchestrator_only", Scopes: []string{"agent:Orchestrator:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, createErr := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "helpdesk", DisplayName: "Helpdesk", RoleIDs: []string{role.ID}}, "tester"); createErr != nil {
		t.Fatalf("CreateSystemUser: %v", createErr)
	}

	got, err = svc.ListSystemUsers(ctx)
	if err != nil {
		t.Fatalf("ListSystemUsers after create: %v", err)
	}
	helpdesk, ok := findSystemUser(got, "helpdesk")
	if !ok {
		t.Fatalf("custom system user not listed: %+v", got)
	}
	if helpdesk.Sub != "system:helpdesk" || helpdesk.DisplayName != "Helpdesk" || !slices.Contains(helpdesk.RoleIDs, role.ID) {
		t.Errorf("helpdesk = %+v, want sub system:helpdesk, display Helpdesk, role %s", helpdesk, role.ID)
	}
	if _, ok := findSystemUser(got, "default"); !ok {
		t.Error("default system user dropped from the union")
	}
}

func findSystemUser(users []SystemUserResponse, name string) (SystemUserResponse, bool) {
	for _, u := range users {
		if u.Name == name {
			return u, true
		}
	}
	return SystemUserResponse{}, false
}

func newGrantTestService(t *testing.T) (*RBACService, *store.UsersRepo, context.Context) {
	t.Helper()
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	svc.SetUsersRepo(usersRepo)
	ctx := context.Background()
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	return svc, usersRepo, ctx
}

func seedGrantUser(ctx context.Context, t *testing.T, ur *store.UsersRepo, issuer, sub, email string) *store.User {
	t.Helper()
	u := &store.User{Issuer: issuer, Sub: sub, CreatedBy: "x", UpdatedBy: "x"}
	if email != "" {
		u.Email = &email
	}
	if err := ur.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("seed user %s: %v", sub, err)
	}
	return u
}

func mustGrantRole(ctx context.Context, t *testing.T, svc *RBACService) string {
	t.Helper()
	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "admin", Scopes: []string{"*"}}, "t", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	return role.ID
}

func TestRBACService_ListGrants_InteractiveEmailReverseMapsToTypedEmail(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)
	u := seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")
	usersSvc := NewUsersService(ur, svc.repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	usersSvc.SetGrantRefresh(svc.composer, nil, nil)
	if _, err := usersSvc.SetUserRoleGrants(ctx, u.ID, []string{roleID}, "admin", []string{"*"}); err != nil {
		t.Fatalf("SetUserRoleGrants: %v", err)
	}

	grants, err := svc.ListGrants(ctx)
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 1 || grants[0].Subject == nil {
		t.Fatalf("grants = %+v, want 1 with a subject", grants)
	}
	if got := grants[0].Subject; got.Type != store.PendingSubjectEmail || got.Value != "alice@example.com" {
		t.Errorf("interactive email grant reverse-mapped to %+v, want typed email/alice@example.com", got)
	}
}

func TestRBACService_CreateGrant_SubConcrete(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)
	u := seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	resp, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-1"},
		RoleID:  roleID,
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	if resp.State != GrantStateConcrete || resp.UserID != u.ID {
		t.Fatalf("state=%q userID=%q, want concrete + %s", resp.State, resp.UserID, u.ID)
	}
	if resp.ID == "" {
		t.Errorf("concrete grant response has empty ID; want the grant id populated like the pending path")
	}
	grants, _ := svc.ListGrants(ctx)
	if len(grants) != 1 || grants[0].State != GrantStateConcrete || grants[0].RoleID != roleID {
		t.Fatalf("grants = %+v", grants)
	}
	if grants[0].ID != resp.ID {
		t.Errorf("create returned id %q but list shows %q", resp.ID, grants[0].ID)
	}
}

func TestRBACService_CreateGrant_EmailPending(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)

	resp, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "Ghost@Example.com"},
		RoleID:  roleID,
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	if resp.State != GrantStatePending {
		t.Fatalf("state = %q, want pending", resp.State)
	}
	if resp.Subject == nil || resp.Subject.Value != "ghost@example.com" {
		t.Errorf("subject value = %+v, want normalized ghost@example.com", resp.Subject)
	}
	grants, _ := svc.ListGrants(ctx)
	if len(grants) != 1 || grants[0].State != GrantStatePending || grants[0].Subject == nil || grants[0].Subject.Value != "ghost@example.com" {
		t.Fatalf("grants = %+v", grants)
	}
}

func TestRBACService_CreateGrant_EmailAmbiguous(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-a", "dup@example.com")
	seedGrantUser(ctx, t, ur, "https://idp", "sub-b", "dup@example.com")

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "dup@example.com"},
		RoleID:  roleID,
	}, "admin", []string{"*"})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError for ambiguous email, got %v", err)
	}
}

func TestRBACService_CreateGrant_TypedSubRecordsProvenance(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)
	u := seedGrantUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	if _, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-1"}, RoleID: roleID,
	}, "admin", []string{"*"}); err != nil {
		t.Fatalf("CreateGrant(sub): %v", err)
	}

	grants, err := svc.ListGrants(ctx)
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	var concrete *GrantResponse
	for i := range grants {
		if grants[i].State == GrantStateConcrete {
			concrete = &grants[i]
		}
	}
	if concrete == nil || concrete.UserID != u.ID {
		t.Fatalf("want a concrete grant on %s, got %+v", u.ID, grants)
	}
	if concrete.Subject == nil || concrete.Subject.Type != store.PendingSubjectSub || concrete.Subject.Issuer != "https://idp" {
		t.Errorf("provenance = %+v, want sub/https://idp", concrete.Subject)
	}
}

func TestRBACService_CreateGrant_LegacyEmailNormalized(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)

	emailResp, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectLegacy, Value: "Alice@Example.com"},
		RoleID:  roleID,
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("CreateGrant(legacy email): %v", err)
	}
	if emailResp.Subject == nil || emailResp.Subject.Value != "alice@example.com" {
		t.Errorf("legacy email not normalized: %+v", emailResp.Subject)
	}

	subResp, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectLegacy, Value: "AbC-123"},
		RoleID:  roleID,
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("CreateGrant(legacy sub): %v", err)
	}
	if subResp.Subject == nil || subResp.Subject.Value != "AbC-123" {
		t.Errorf("sub-shaped legacy value should keep case: %+v", subResp.Subject)
	}
}

func TestRBACService_CreateGrant_LegacyEmailMaterializesAfterProvision(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	svc.SetUsersRepo(usersRepo)
	ctx := context.Background()
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	roleID := mustGrantRole(ctx, t, svc)
	if _, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectLegacy, Value: "Alice@Example.com"},
		RoleID:  roleID,
	}, "admin", []string{"*"}); err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}

	email := "Alice@Example.com"
	u := &store.User{Issuer: "https://idp", Sub: "s1", Email: &email, CreatedBy: "x", UpdatedBy: "x"}
	if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("provision user: %v", err)
	}
	granted, err := repo.MaterializePendingForUser(ctx, u, "login")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(granted) != 1 || granted[0] != roleID {
		t.Fatalf("mixed-case legacy email grant did not materialize: %v", granted)
	}
}

func TestRBACService_CreateGrant_SubRequiresIssuer(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)

	_, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectSub, Value: "sub-x"},
		RoleID:  roleID,
	}, "admin", []string{"*"})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError for sub without issuer, got %v", err)
	}
}
