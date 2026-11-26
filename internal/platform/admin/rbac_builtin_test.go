// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func seedBuiltinRole(t *testing.T, svc *RBACService, name string, scopes []string) *store.RBACRole {
	t.Helper()
	role := &store.RBACRole{Name: name, CreatedBy: "system", UpdatedBy: "system"}
	if err := svc.repo.CreateRole(context.Background(), role, scopes, nil); err != nil {
		t.Fatalf("seed builtin role %q: %v", name, err)
	}
	if _, err := svc.repo.DB().ExecContext(context.Background(),
		`UPDATE rbac_roles SET builtin = 1 WHERE id = ?`, role.ID); err != nil {
		t.Fatalf("flag builtin role %q: %v", name, err)
	}
	svc.rebuildAndInvalidate(context.Background())
	return role
}

func TestRBACBuiltinRole_AssignedScopesResolve(t *testing.T) {
	svc, usersRepo, ctx := newGrantTestService(t)
	builtin := seedBuiltinRole(t, svc, "sam_auditor", []string{"analytics:_:read"})

	if _, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectLegacy, Value: "dana@example.org"},
		RoleID:  builtin.ID,
	}, "admin", []string{"analytics:_:read"}); err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}

	u := seedGrantUser(ctx, t, usersRepo, "https://idp", "sub-dana", "dana@example.org")
	granted, err := svc.repo.MaterializePendingForUser(ctx, u, "login")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(granted) != 1 || granted[0] != builtin.ID {
		t.Fatalf("built-in grant did not materialize for the user: %v", granted)
	}

	if err = svc.composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	res, err := svc.composer.Lookup("dana@example.org", "webui", map[string]any{"sam_user_id": u.ID})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !slices.Contains(res.Roles, "sam_auditor") {
		t.Errorf("resolved roles = %v, want to include built-in sam_auditor", res.Roles)
	}
	if !slices.Contains(res.Scopes, "analytics:_:read") {
		t.Errorf("resolved scopes = %v, want the built-in's analytics:_:read", res.Scopes)
	}
}
