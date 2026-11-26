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
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func systemUserGrantXRef() rbacRoleXRefAdapter {
	return rbacRoleXRefAdapter{
		nameToID: map[string]string{"sam_user": "uuid-user"},
		idToName: map[string]string{"uuid-user": "sam_user"},
		desired:  map[string]struct{}{},
		yaml:     map[string]struct{}{},
	}
}

func TestPlanRBACGrants_SystemUserGrantsAreNeverPruned(t *testing.T) {
	actual := []wire.GrantResponse{{
		ID: "grant-system",
		Subject: &apiv2.GrantSubject{
			Type:   "sub",
			Value:  rbac.SystemUserPrincipal("orders-bot"),
			Issuer: new(rbac.SystemUserIssuer),
		},
		RoleID: "uuid-user",
		State:  new(entplatform.GrantStateConcrete),
		Origin: entplatform.RBACOriginCustom,
	}}

	plan, err := planRBACGrants(nil, actual, nil, systemUserGrantXRef())
	if err != nil {
		t.Fatalf("planRBACGrants: %v", err)
	}
	if len(plan.Deletes) != 0 {
		t.Fatalf("planned %d delete(s) for a system user's grants: %+v", len(plan.Deletes), plan.Deletes)
	}
}

func TestPlanRBACGrants_HumanGrantsStillPrune(t *testing.T) {
	actual := []wire.GrantResponse{{
		ID:      "grant-human",
		Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"},
		RoleID:  "uuid-user",
		Origin:  entplatform.RBACOriginCustom,
	}}

	plan, err := planRBACGrants(nil, actual, nil, systemUserGrantXRef())
	if err != nil {
		t.Fatalf("planRBACGrants: %v", err)
	}
	if len(plan.Deletes) != 1 {
		t.Fatalf("planned %d delete(s) for an undeclared human grant, want 1", len(plan.Deletes))
	}
}

func TestReadRoleUsers_RejectsSystemPrincipal(t *testing.T) {
	for name, entry := range map[string]any{
		"bare string": rbac.SystemUserPrincipal("orders-bot"),
		"typed sub": map[string]any{
			"type": "sub", "value": rbac.SystemUserPrincipal("orders-bot"), "issuer": rbac.SystemUserIssuer,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := readRoleUsers(resolve.ResolvedResource{
				Kind:       "rbacRole",
				Name:       "some_role",
				SourceFile: "rbac/roles/some_role.yaml",
				Spec:       map[string]any{roleUsersKey: []any{entry}},
			})
			if err == nil {
				t.Fatalf("expected a rejection for a system principal in spec.users")
			}
			if !strings.Contains(err.Error(), "kind: serviceAccount") {
				t.Errorf("error %q should point the author at kind: serviceAccount", err)
			}
		})
	}
}
