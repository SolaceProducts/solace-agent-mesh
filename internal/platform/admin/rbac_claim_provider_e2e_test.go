// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newClaimMappingStack(t *testing.T, yaml rbac.YAMLState) (*RBACService, *rbac.Composer) {
	t.Helper()
	return newClaimMappingStackWithLogger(t, yaml, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newClaimMappingStackWithLogger(t *testing.T, yaml rbac.YAMLState, logger *slog.Logger) (*RBACService, *rbac.Composer) {
	t.Helper()
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	composer := rbac.NewComposer(yaml, newRBACDBSource(repo), logger)
	if err := composer.Rebuild(context.Background()); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	svc := NewRBACService(repo, composer, nil, nil, logger)
	svc.SetUsersRepo(store.NewUsersRepo(db))
	return svc, composer
}

func TestClaimMapping_CreatedMappingActuallyGrants(t *testing.T) {
	yaml := rbac.YAMLState{
		Roles:             rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{"sam_admin": {Scopes: []string{"*"}}}},
		ClaimOIDCProvider: "azure",
		ClaimKey:          "groups",
	}
	svc, composer := newClaimMappingStack(t, yaml)
	ctx := context.Background()

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "responder", Scopes: []string{"agent:bot:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	_, err = svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Engineering", ClaimValue: "eng-group", RoleIDs: []string{role.ID},
	}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	res, err := composer.Lookup("sub-alice", "webui", map[string]any{
		"oidc_provider": "azure",
		"idp_claims":    map[string]any{"groups": []any{"eng-group"}},
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(res.Roles) != 1 || res.Roles[0] != "responder" {
		t.Errorf("roles = %v, want [responder]; a mapping the API accepted must grant", res.Roles)
	}
	if len(res.Scopes) != 1 || res.Scopes[0] != "agent:bot:invoke" {
		t.Errorf("scopes = %v, want [agent:bot:invoke]", res.Scopes)
	}
}

func TestClaimMapping_WarnsWhenDeploymentProviderIsAPlaceholder(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	yaml := rbac.YAMLState{
		Roles:             rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{"sam_admin": {Scopes: []string{"*"}}}},
		ClaimOIDCProvider: rbac.ProviderSentinelGeneric,
		ClaimKey:          "groups",
	}
	svc, _ := newClaimMappingStackWithLogger(t, yaml, logger)
	ctx := context.Background()

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "responder", Scopes: []string{"agent:bot:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	created, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
		Name: "Engineering", ClaimValue: "eng", RoleIDs: []string{role.ID},
	}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("create must be accepted — the platform cannot tell this from a real provider name: %v", err)
	}
	if created.OIDCProvider != rbac.ProviderSentinelGeneric {
		t.Errorf("oidcProvider = %q, want the configured value stored as-is", created.OIDCProvider)
	}
	if !strings.Contains(buf.String(), msgPlaceholderProviderStored) {
		t.Errorf("no warning for an inert mapping; log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "claimMappingName=Engineering") {
		t.Errorf("warning must name the mapping; log:\n%s", buf.String())
	}
}

func TestClaimMapping_WriteThatStoresNothingDoesNotWarn(t *testing.T) {
	yaml := rbac.YAMLState{
		Roles:             rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{"sam_admin": {Scopes: []string{"*"}}}},
		ClaimOIDCProvider: "azure",
		ClaimKey:          "groups",
	}
	ctx := context.Background()

	newStack := func(t *testing.T) (*RBACService, *bytes.Buffer, string) {
		t.Helper()
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		svc, _ := newClaimMappingStackWithLogger(t, yaml, logger)
		role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "responder", Scopes: []string{"agent:bot:invoke"}}, "tester", []string{"*"})
		if err != nil {
			t.Fatalf("create role: %v", err)
		}
		buf.Reset()
		return svc, &buf, role.ID
	}

	t.Run("a rejected create", func(t *testing.T) {
		svc, buf, roleID := newStack(t)
		if _, err := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{
			Name: "Rejected", OIDCProvider: rbac.ProviderSentinelEnterprise, ClaimValue: "eng", RoleIDs: []string{roleID},
		}, "tester", []string{"*"}); err == nil {
			t.Fatal("create must be rejected")
		}
		if strings.Contains(buf.String(), msgPlaceholderProviderStored) {
			t.Errorf("rejected create claimed the mapping was stored; log:\n%s", buf.String())
		}
	})

	t.Run("an update against an unknown id", func(t *testing.T) {
		svc, buf, roleID := newStack(t)
		_, err := svc.UpdateClaimMapping(ctx, "6f1a6a3e-0000-7000-8000-000000000000", UpdateRBACClaimMappingRequest{
			Name: "Ghost", OIDCProvider: rbac.ProviderSentinelEnterprise, ClaimKey: "groups",
			ClaimValue: "eng", RoleIDs: []string{roleID},
		}, "tester", []string{"*"})
		var nferr *NotFoundError
		if !errors.As(err, &nferr) {
			t.Fatalf("err = %v, want NotFoundError", err)
		}
		if strings.Contains(buf.String(), msgPlaceholderProviderStored) {
			t.Errorf("not-found update claimed a mapping was stored; log:\n%s", buf.String())
		}
	})
}
