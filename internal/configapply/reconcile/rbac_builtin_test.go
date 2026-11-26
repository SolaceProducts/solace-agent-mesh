// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestPlanRBACRoles_BuiltinRolesNotPruned(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("incident_responder", []string{"agent:incident-bot:invoke"}, nil)}
	actual := []wire.RoleResponse{{
		ID:     "uuid-builtin",
		Name:   "sam_manager",
		Origin: entplatform.RBACOriginBuiltin,
	}}
	plan, err := planRBACRoles(desired, actual)
	if err != nil {
		t.Fatalf("planRBACRoles: %v", err)
	}
	if len(plan.Deletes) != 0 {
		t.Errorf("built-in role must never be pruned, got Deletes = %+v", plan.Deletes)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "incident_responder" {
		t.Errorf("Creates = %+v", plan.Creates)
	}
}

func TestPlanRBACRoles_RejectsBuiltinNameCollision(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("sam_manager", nil, nil)}
	actual := []wire.RoleResponse{{
		ID:     "uuid-builtin",
		Name:   "sam_manager",
		Origin: entplatform.RBACOriginBuiltin,
	}}
	_, err := planRBACRoles(desired, actual)
	if err == nil {
		t.Fatalf("expected error for built-in name collision, got nil")
	}
	if !strings.Contains(err.Error(), "built-in") {
		t.Errorf("error missing built-in context: %v", err)
	}
}

func TestPlanRBACRoles_InheritFromBuiltinResolves(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("incident_responder", []string{"agent:incident-bot:invoke"}, []string{"sam_manager"})}
	actual := []wire.RoleResponse{{
		ID:     "uuid-builtin",
		Name:   "sam_manager",
		Origin: entplatform.RBACOriginBuiltin,
	}}
	plan, err := planRBACRoles(desired, actual)
	if err != nil {
		t.Fatalf("inheriting from a built-in role must resolve: %v", err)
	}
	if len(plan.Deletes) != 0 {
		t.Errorf("built-in parent must not be pruned, got Deletes = %+v", plan.Deletes)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("Creates = %+v", plan.Creates)
	}
	if got := plan.Creates[0].Create.Inherits; len(got) != 1 || got[0] != "uuid-builtin" {
		t.Errorf("inherits resolved to %v, want [uuid-builtin]", got)
	}
}

func TestPlanRBACGrants_GrantFileResolvesBuiltinRole(t *testing.T) {
	actual := []wire.RoleResponse{
		{ID: "uuid-builtin", Name: "sam_manager", Origin: entplatform.RBACOriginBuiltin},
	}
	xref := loadRBACRoleRegistry(actual, nil)
	desired, err := extractGrantResources([]resolve.ResolvedResource{
		grantFile("managers", "sam_manager", []any{"ops@corp.com"}),
	})
	if err != nil {
		t.Fatalf("extractGrantResources: %v", err)
	}
	plan, err := planRBACGrants(desired, nil, nil, xref)
	if err != nil {
		t.Fatalf("planRBACGrants: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].RoleID != "uuid-builtin" {
		t.Fatalf("plan = %+v, want one create on uuid-builtin", plan)
	}
}
