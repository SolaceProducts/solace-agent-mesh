// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestClaimMapping_ProviderResolution(t *testing.T) {
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

	create := func(t *testing.T, name, provider, claimValue string) (*RBACClaimMappingResponse, error) {
		t.Helper()
		return svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
			Name: name, OIDCProvider: provider, ClaimValue: claimValue, RoleIDs: []string{dbRole.ID},
		}, "tester", []string{"*"})
	}

	t.Run("omitted provider defaults to the deployment provider", func(t *testing.T) {
		resp, err := create(t, "Defaulted", "", "eng")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if resp.OIDCProvider != "azure" {
			t.Errorf("oidcProvider = %q, want azure (deployment default)", resp.OIDCProvider)
		}
	})

	t.Run("a real per-mapping provider is preserved", func(t *testing.T) {
		resp, err := create(t, "Okta", "okta", "ops")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if resp.OIDCProvider != "okta" {
			t.Errorf("oidcProvider = %q, want okta; multi-gateway deployments vary it per mapping", resp.OIDCProvider)
		}
	})

	t.Run("a placeholder provider is rejected with an actionable 422", func(t *testing.T) {
		for _, placeholder := range []string{rbac.ProviderSentinelEnterprise, rbac.ProviderSentinelGeneric} {
			_, err := create(t, "Placeholder-"+placeholder, placeholder, "grp-"+placeholder)
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Errorf("create with %q: err = %v, want ValidationError (422)", placeholder, err)
				continue
			}
			detail, ok := verr.Details["oidcProvider"]
			if !ok {
				t.Fatalf("details = %v, want an oidcProvider entry", verr.Details)
			}
			if !strings.Contains(strings.Join(detail, " "), "azure") {
				t.Errorf("detail must name the configured provider so it is actionable; got %v", detail)
			}
		}
	})

	t.Run("with no configured provider there is nothing to check against", func(t *testing.T) {
		yamlNoProvider := rbac.YAMLState{
			Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{"sam_admin": {Scopes: []string{"*"}}}},
		}
		svcNoProvider, _ := newClaimMappingStack(t, yamlNoProvider)
		role, err := svcNoProvider.CreateRole(ctx, CreateRBACRoleRequest{Name: "r", Scopes: []string{"agent:b:invoke"}}, "tester", []string{"*"})
		if err != nil {
			t.Fatalf("create role: %v", err)
		}
		resp, err := svcNoProvider.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
			Name: "Unchecked", OIDCProvider: rbac.ProviderSentinelEnterprise, ClaimValue: "eng", RoleIDs: []string{role.ID},
		}, "tester", []string{"*"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if resp.OIDCProvider != rbac.ProviderSentinelEnterprise {
			t.Errorf("oidcProvider = %q, want it stored as sent", resp.OIDCProvider)
		}
	})

	t.Run("update rejects a placeholder it introduces onto a working mapping", func(t *testing.T) {
		resp, err := create(t, "Working", "", "sales")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		_, err = svc.UpdateClaimMapping(ctx, resp.ID, UpdateRBACClaimMappingRequest{
			Name: "Working", OIDCProvider: rbac.ProviderSentinelEnterprise, ClaimKey: "groups",
			ClaimValue: "sales", RoleIDs: []string{dbRole.ID},
		}, "tester", []string{"*"})
		var verr *ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("err = %v, want ValidationError; this update silently stops the mapping granting", err)
		}
	})

	t.Run("update accepts a placeholder it is only carrying", func(t *testing.T) {
		legacy := &store.RBACIdpClaimMapping{
			OIDCProvider: rbac.ProviderSentinelEnterprise,
			ClaimKey:     "groups", ClaimValue: "support", Name: "Editable",
			CreatedBy: "old", UpdatedBy: "old",
		}
		if err := svc.repo.CreateClaimMapping(ctx, legacy, []string{dbRole.ID}); err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
		updated, err := svc.UpdateClaimMapping(ctx, legacy.ID, UpdateRBACClaimMappingRequest{
			Name: "Renamed", OIDCProvider: rbac.ProviderSentinelEnterprise, ClaimKey: "groups",
			ClaimValue: "support", RoleIDs: []string{dbRole.ID},
		}, "tester", []string{"*"})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.Name != "Renamed" {
			t.Errorf("name = %q, want Renamed", updated.Name)
		}
	})

	t.Run("update refuses to repoint a carried placeholder onto a new claim value", func(t *testing.T) {
		legacy := &store.RBACIdpClaimMapping{
			OIDCProvider: rbac.ProviderSentinelEnterprise,
			ClaimKey:     "groups", ClaimValue: "legacy-value", Name: "Shim",
			CreatedBy: "old", UpdatedBy: "old",
		}
		if err := svc.repo.CreateClaimMapping(ctx, legacy, []string{dbRole.ID}); err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
		_, err := svc.UpdateClaimMapping(ctx, legacy.ID, UpdateRBACClaimMappingRequest{
			Name: "Shim", OIDCProvider: rbac.ProviderSentinelEnterprise, ClaimKey: "groups",
			ClaimValue: "minted-value", RoleIDs: []string{dbRole.ID},
		}, "tester", []string{"*"})
		var verr *ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("err = %v, want ValidationError; a legacy row must not mint new unmatchable subjects", err)
		}
	})

	t.Run("update omitting the provider keeps the stored one", func(t *testing.T) {
		legacy := &store.RBACIdpClaimMapping{
			OIDCProvider: rbac.ProviderSentinelEnterprise,
			ClaimKey:     "groups", ClaimValue: "carried", Name: "Untouched",
			CreatedBy: "old", UpdatedBy: "old",
		}
		if err := svc.repo.CreateClaimMapping(ctx, legacy, []string{dbRole.ID}); err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
		updated, err := svc.UpdateClaimMapping(ctx, legacy.ID, UpdateRBACClaimMappingRequest{
			Name: "RenamedOnly", OIDCProvider: "", ClaimKey: "groups",
			ClaimValue: "carried", RoleIDs: []string{dbRole.ID},
		}, "tester", []string{"*"})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.OIDCProvider != rbac.ProviderSentinelEnterprise {
			t.Errorf("oidcProvider = %q, want it left at the stored value; the deployment default would repoint the mapping", updated.OIDCProvider)
		}
	})
}

func TestClaimMapping_PlaceholderProviderAllowedWhenItIsTheDeploymentProvider(t *testing.T) {
	yaml := rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			"sam_admin": {Scopes: []string{"*"}},
		}},
		ClaimOIDCProvider: rbac.ProviderSentinelEnterprise,
		ClaimKey:          "groups",
	}
	svc, _ := newClaimMappingStack(t, yaml)
	ctx := context.Background()

	dbRole, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "responder", Scopes: []string{"agent:bot:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	resp, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Enterprise", OIDCProvider: rbac.ProviderSentinelEnterprise, ClaimValue: "eng", RoleIDs: []string{dbRole.ID},
	}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.OIDCProvider != rbac.ProviderSentinelEnterprise {
		t.Errorf("oidcProvider = %q, want it stored as the deployment's own provider name", resp.OIDCProvider)
	}
}
