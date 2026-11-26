// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

const superAdminRefusalMessage = "RBAC management requires a super administrator"

func refusingRBACMux(t *testing.T, writes *int) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.RoleResponse{})
	})
	mux.HandleFunc("GET "+serverCapabilitiesPath, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, map[string]bool{"manageRbac": true})
	})
	refuse := func(w http.ResponseWriter, _ *http.Request) {
		*writes++
		writeV2Error(w, http.StatusForbidden, superAdminRefusalMessage, nil)
	}
	mux.HandleFunc("POST /api/v2/platform/rbac/roles", refuse)
	mux.HandleFunc("PUT /api/v2/platform/rbac/roles/{id}", refuse)
	mux.HandleFunc("DELETE /api/v2/platform/rbac/roles/{id}", refuse)
	return mux
}

func TestApplyRBACRoles_NonSuperAdminIsRefusedAndWritesNothing(t *testing.T) {
	var writes int
	c, _ := newRBACTestClient(t, refusingRBACMux(t, &writes))

	plan := &rbacRolePlan{
		Creates: []rbacRoleOp{{Name: "analyst", Create: wire.CreateRoleRequest{Name: "analyst"}}},
		Updates: []rbacRoleOp{{Name: "existing", ID: "uuid-existing", Patch: wire.ReplaceRoleRequest{Name: "existing"}}},
		Deletes: []rbacRoleOp{{Name: "old", ID: "uuid-old"}},
	}
	outcome := applyRBACRoles(t.Context(), c, plan, ApplyOptions{Prune: true})
	applyRBACRoleDeletes(t.Context(), c, plan, ApplyOptions{Prune: true}, outcome)

	if len(outcome.Created)+len(outcome.Updated)+len(outcome.Deleted) != 0 {
		t.Errorf("a refused apply reported successful writes: created=%d updated=%d deleted=%d",
			len(outcome.Created), len(outcome.Updated), len(outcome.Deleted))
	}
	if len(outcome.Failures) != 3 {
		t.Fatalf("expected all 3 ops to fail, got %d failures: %+v", len(outcome.Failures), outcome.Failures)
	}
	for _, f := range outcome.Failures {
		if f.Err == nil {
			t.Errorf("failure for %q carries no error", f.Op.Name)
			continue
		}
		if !strings.Contains(f.Err.Error(), superAdminRefusalMessage) {
			t.Errorf("failure for %q lost the refusal reason: %v", f.Op.Name, f.Err)
		}
	}
	if len(outcome.Skipped) != 0 {
		t.Errorf("expected 0 skipped (the manage probe reports true), got %d", len(outcome.Skipped))
	}
	if writes != 3 {
		t.Errorf("expected 3 attempted writes, got %d", writes)
	}
}

func TestPlanAllRBAC_SuperAdminGateIsNotPreflighted(t *testing.T) {
	var writes int
	c, _ := newRBACTestClient(t, refusingRBACMux(t, &writes))

	allowed, err := rbacManageProbe(t.Context(), c)
	if err != nil {
		t.Fatalf("rbacManageProbe: %v", err)
	}
	if !allowed {
		t.Fatal("probe reported blocked; the super-admin gate is now pre-flighted — replace this test with one asserting RBACWritesBlocked")
	}
	if writes != 0 {
		t.Errorf("the probe must not attempt writes, got %d", writes)
	}
}
