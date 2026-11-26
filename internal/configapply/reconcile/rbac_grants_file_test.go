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
)

func grantFile(name, roleName string, users []any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:       "rbacGrant",
		Name:       name,
		SourceFile: "rbac/grants/" + name + ".yaml",
		Spec:       map[string]any{"roleName": roleName, "users": users},
	}
}

func TestExtractGrantResources_FanOut(t *testing.T) {
	got, err := extractGrantResources([]resolve.ResolvedResource{
		grantFile("platform-admins", "PlatformAdmin", []any{
			" ops@x ",
			map[string]any{"type": "sub", "issuer": "https://idp", "value": "u-1"},
		}),
	})
	if err != nil {
		t.Fatalf("extractGrantResources: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 synthetic rows, got %d: %+v", len(got), got)
	}
	if got[0].Kind != "rbacGrant" || got[0].Name != "platform-admins/ops@x" {
		t.Errorf("row0 kind/name = %q/%q", got[0].Kind, got[0].Name)
	}
	if got[0].Spec["identity"] != "ops@x" || got[0].Spec["roleName"] != "PlatformAdmin" || got[0].Spec["subjectType"] != nil {
		t.Errorf("row0 spec = %+v, want trimmed identity, roleName, no subjectType", got[0].Spec)
	}
	if got[1].Spec["subjectType"] != "sub" || got[1].Spec["subjectIssuer"] != "https://idp" || got[1].Spec["identity"] != "u-1" {
		t.Errorf("row1 spec = %+v", got[1].Spec)
	}
	if got[1].Name != "platform-admins/sub:https://idp:u-1" {
		t.Errorf("row1 name = %q, want a distinct typed key", got[1].Name)
	}
	if got[1].SourceFile != "rbac/grants/platform-admins.yaml" {
		t.Errorf("source file = %q, want the grant file", got[1].SourceFile)
	}
}

func TestExtractGrantResources_Validation(t *testing.T) {
	cases := map[string]resolve.ResolvedResource{
		"blank roleName": grantFile("g", "  ", []any{"a@x"}),
		"empty users":    grantFile("g", "R", []any{}),
		"missing users": {
			Kind: "rbacGrant", Name: "g", SourceFile: "rbac/grants/g.yaml",
			Spec: map[string]any{"roleName": "R"},
		},
		"unknown key": {
			Kind: "rbacGrant", Name: "g", SourceFile: "rbac/grants/g.yaml",
			Spec: map[string]any{"roleName": "R", "users": []any{"a@x"}, "bogus": 1},
		},
		"sub without issuer":         grantFile("g", "R", []any{map[string]any{"type": "sub", "value": "x"}}),
		"bare built-in system user":  grantFile("g", "R", []any{"system:default"}),
		"typed built-in system user": grantFile("g", "R", []any{map[string]any{"type": "legacy", "value": "system:eval"}}),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := extractGrantResources([]resolve.ResolvedResource{r}); err == nil {
				t.Fatalf("expected error for %q", name)
			}
		})
	}
}

func TestExtractGrantResources_ErrorNamesGrantKind(t *testing.T) {
	_, err := extractGrantResources([]resolve.ResolvedResource{
		grantFile("g", "R", []any{map[string]any{"type": "sub", "value": "x"}}),
	})
	if err == nil || !strings.Contains(err.Error(), "rbacGrant") {
		t.Fatalf("error should name the rbacGrant kind, got: %v", err)
	}
}

func TestConfigRoleGrantAdvisories(t *testing.T) {
	fileGrants, err := extractGrantResources([]resolve.ResolvedResource{
		grantFile("g1", "ConfigRole", []any{"a@x"}),
		grantFile("g2", "ManagedRole", []any{"b@x"}),
	})
	if err != nil {
		t.Fatalf("extractGrantResources: %v", err)
	}
	warns := configRoleGrantAdvisories(fileGrants, []string{"ConfigRole"})
	if len(warns) != 1 {
		t.Fatalf("want 1 advisory, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0], "ConfigRole") {
		t.Errorf("advisory should name the config-defined role: %q", warns[0])
	}
	if strings.Contains(warns[0], "ManagedRole") {
		t.Errorf("advisory must not name the managed role: %q", warns[0])
	}
	if w := configRoleGrantAdvisories(fileGrants, nil); w != nil {
		t.Errorf("no config roles → no advisory, got %v", w)
	}
}

func TestPlanRBACGrants_GrantFileResolvesExistingRole(t *testing.T) {
	actual := []wire.RoleResponse{
		{ID: "uuid-pa", Name: "PlatformAdmin", Origin: entplatform.RBACOriginCustom},
	}
	xref := loadRBACRoleRegistry(actual, nil)
	desired, err := extractGrantResources([]resolve.ResolvedResource{
		grantFile("pa", "PlatformAdmin", []any{"ops@x"}),
	})
	if err != nil {
		t.Fatalf("extractGrantResources: %v", err)
	}
	plan, err := planRBACGrants(desired, nil, nil, xref)
	if err != nil {
		t.Fatalf("planRBACGrants: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].RoleID != "uuid-pa" {
		t.Fatalf("plan = %+v, want one create on uuid-pa", plan)
	}
}

func TestPlanRBACGrants_MixedCaseEmailNoChurn(t *testing.T) {
	desired := []resolve.ResolvedResource{{
		Kind: "rbacGrant", Name: "g", SourceFile: "rbac/grants/g.yaml",
		Spec: map[string]any{"identity": "Alice@Example.com", "roleName": "r", "subjectType": "email"},
	}}
	actual := []wire.GrantResponse{{
		ID:      "gid",
		Subject: &apiv2.GrantSubject{Type: "email", Value: "alice@example.com"},
		RoleID:  "uuid-r",
		Origin:  entplatform.RBACOriginCustom,
	}}
	xref := loadRBACRoleRegistry([]wire.RoleResponse{
		{ID: "uuid-r", Name: "r", Origin: entplatform.RBACOriginCustom},
	}, nil)
	plan, err := planRBACGrants(desired, actual, nil, xref)
	if err != nil {
		t.Fatalf("planRBACGrants: %v", err)
	}
	if len(plan.Creates) != 0 || len(plan.Deletes) != 0 {
		t.Fatalf("mixed-case email churned: creates=%d deletes=%d (want 0/0)", len(plan.Creates), len(plan.Deletes))
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("want 1 unchanged grant, got %d", len(plan.Unchanged))
	}
}

func TestPlanRBACGrants_DuplicateAcrossSourcesErrors(t *testing.T) {
	roleGrants, err := extractRoleUsers([]resolve.ResolvedResource{roleWithUsers("ops", []any{"ops@x"})})
	if err != nil {
		t.Fatalf("extractRoleUsers: %v", err)
	}
	fileGrants, err := extractGrantResources([]resolve.ResolvedResource{grantFile("dup", "ops", []any{"ops@x"})})
	if err != nil {
		t.Fatalf("extractGrantResources: %v", err)
	}
	xref := loadRBACRoleRegistry(nil, []string{"ops"})
	if _, err := planRBACGrants(append(roleGrants, fileGrants...), nil, nil, xref); err == nil {
		t.Fatalf("expected a duplicate (subject, role) error across the two grant sources")
	}
}
