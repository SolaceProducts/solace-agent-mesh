// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestClaimMapping_ClaimKeyMirrorsProvider(t *testing.T) {
	yaml := rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			"sam_admin": {Scopes: []string{"*"}},
		}},
		ClaimOIDCProvider: "azure",
		ClaimKey:          "groups",
	}
	svc, _ := newClaimMappingStack(t, yaml)
	ctx := context.Background()

	dbRole, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "responder", Scopes: []string{"agent:bot:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	t.Run("supplied claim key is stored", func(t *testing.T) {
		resp, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
			Name: "Supplied", OIDCProvider: "okta", ClaimKey: "roles", ClaimValue: "eng", RoleIDs: []string{dbRole.ID},
		}, "tester", []string{"*"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if resp.ClaimKey != "roles" {
			t.Errorf("claimKey = %q, want roles", resp.ClaimKey)
		}
	})

	t.Run("omitted claim key defaults to the deployment key", func(t *testing.T) {
		resp, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
			Name: "Defaulted", OIDCProvider: "azure", ClaimValue: "ops", RoleIDs: []string{dbRole.ID},
		}, "tester", []string{"*"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if resp.ClaimKey != "groups" {
			t.Errorf("claimKey = %q, want groups (yaml default)", resp.ClaimKey)
		}

		updated, err := svc.UpdateClaimMapping(ctx, resp.ID, UpdateRBACClaimMappingRequest{
			Name: "Defaulted", OIDCProvider: "azure", ClaimKey: "departments", ClaimValue: "ops", RoleIDs: []string{dbRole.ID},
		}, "tester", []string{"*"})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.ClaimKey != "departments" {
			t.Errorf("updated claimKey = %q, want departments", updated.ClaimKey)
		}
	})
}
