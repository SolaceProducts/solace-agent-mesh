// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestAuthorizationService_DenyAll(t *testing.T) {
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{
		Type: "deny_all",
	}, "gw-1", testLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	scopes := svc.ResolveScopes("alice", nil, nil)
	if len(scopes) != 0 {
		t.Errorf("deny_all should return no scopes, got %v", scopes)
	}
}

func TestAuthorizationService_DenyAllDefault(t *testing.T) {
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{}, "gw-1", testLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	scopes := svc.ResolveScopes("alice", nil, nil)
	if len(scopes) != 0 {
		t.Errorf("default (deny_all) should return no scopes, got %v", scopes)
	}
}

func TestAuthorizationService_None(t *testing.T) {
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{
		Type: "none",
	}, "gw-1", testLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	scopes := svc.ResolveScopes("alice", nil, nil)
	if len(scopes) != 1 || scopes[0] != "*" {
		t.Errorf("none should return [*], got %v", scopes)
	}
}

func TestAuthorizationService_UnknownType(t *testing.T) {
	_, err := NewAuthorizationService(AuthorizationServiceConfig{
		Type: "invalid",
	}, "gw-1", testLogger())
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestAuthorizationService_ResolveRoles_Defaults(t *testing.T) {
	svc := &AuthorizationService{
		serviceType:  "default_rbac",
		defaultRoles: []string{"viewer"},
		yamlUsers:    rbac.UsersConfig{Users: map[string]rbac.UserAssignment{}},
		gatewayID:    "gw-1",
		logger:       testLogger(),
	}

	roles := svc.ResolveRoles("unknown-user", nil)
	if len(roles) != 1 || roles[0] != "viewer" {
		t.Errorf("expected default roles [viewer], got %v", roles)
	}
}

func TestAuthorizationService_ResolveRoles_DefaultFallthroughLogsAtDebug(t *testing.T) {
	var buf bytes.Buffer
	svc := &AuthorizationService{
		serviceType:  "default_rbac",
		defaultRoles: []string{"viewer"},
		yamlUsers:    rbac.UsersConfig{Users: map[string]rbac.UserAssignment{}},
		emailFold:    rbac.BuildEmailFoldIndex(nil),
		gatewayID:    "gw-1",
		logger:       slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	svc.ResolveRoles("nobody@example.com", nil)

	out := buf.String()
	if !strings.Contains(out, "applying default roles") || !strings.Contains(out, "nobody@example.com") {
		t.Errorf("expected a DEBUG log naming the defaulted identity, got:\n%s", out)
	}
}

func TestAuthorizationService_ResolveRoles_WithProvider(t *testing.T) {
	provider := &mockRoleProvider{
		roles: map[string][]string{
			"alice": {"admin", "editor"},
		},
	}

	svc := &AuthorizationService{
		serviceType:  "default_rbac",
		roleProvider: provider,
		yamlUsers:    rbac.UsersConfig{Users: map[string]rbac.UserAssignment{}},
		defaultRoles: []string{"viewer"},
		gatewayID:    "gw-1",
		logger:       testLogger(),
	}

	roles := svc.ResolveRoles("alice", map[string]any{"key": "val"})
	if len(roles) != 2 {
		t.Fatalf("expected 2 roles, got %v", roles)
	}
	found := map[string]bool{}
	for _, r := range roles {
		found[r] = true
	}
	if !found["admin"] || !found["editor"] {
		t.Errorf("expected admin and editor, got %v", roles)
	}
}

func TestAuthorizationService_ResolveRoles_MergesYAMLAndProvider(t *testing.T) {
	provider := &mockRoleProvider{
		roles: map[string][]string{
			"alice": {"editor"},
		},
	}

	svc := &AuthorizationService{
		serviceType:  "default_rbac",
		roleProvider: provider,
		yamlUsers: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
			"alice": {Roles: []string{"viewer"}},
		}},
		defaultRoles: []string{"default"},
		gatewayID:    "gw-1",
		logger:       testLogger(),
	}

	roles := svc.ResolveRoles("alice", map[string]any{})
	found := map[string]bool{}
	for _, r := range roles {
		found[r] = true
	}
	if !found["viewer"] || !found["editor"] {
		t.Errorf("expected merged roles [viewer, editor], got %v", roles)
	}
	if found["default"] {
		t.Error("default roles should NOT be applied when user has roles")
	}
}

func TestAuthorizationService_GatewaySpecificRoles(t *testing.T) {
	svc := &AuthorizationService{
		serviceType: "default_rbac",
		yamlUsers: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
			"gateway_specific_identities:gw-1:alice": {Roles: []string{"admin"}},
		}},
		defaultRoles: []string{"viewer"},
		gatewayID:    "gw-1",
		logger:       testLogger(),
	}

	roles := svc.ResolveRoles("alice", nil)
	if len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("expected [admin] from gateway-specific identity, got %v", roles)
	}
}

func TestAuthorizationService_GatewaySpecificRolesMixedCase(t *testing.T) {
	users := map[string]rbac.UserAssignment{
		"gateway_specific_identities:gw-1:Bob@example.org": {Roles: []string{"admin"}},
	}
	svc := &AuthorizationService{
		serviceType:  "default_rbac",
		yamlUsers:    rbac.UsersConfig{Users: users},
		emailFold:    rbac.BuildEmailFoldIndex(users),
		defaultRoles: []string{"viewer"},
		gatewayID:    "gw-1",
		logger:       testLogger(),
	}

	roles := svc.ResolveRoles("bob@example.org", nil)
	if len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("expected [admin] via case-insensitive gateway-specific fallback, got %v", roles)
	}
}

func TestAuthorizationService_UnknownRoleProvider(t *testing.T) {
	rolesPath := writeTempRolesYAML(t)

	t.Run("ms_graph rejected with supported-values hint", func(t *testing.T) {
		_, err := NewAuthorizationService(AuthorizationServiceConfig{
			Type:                       "default_rbac",
			RoleToScopeDefinitionsPath: rolesPath,
			UserToRoleProvider:         "ms_graph",
		}, "gw-1", testLogger())
		if err == nil {
			t.Fatal("expected error for unsupported role provider ms_graph")
		}
		if !strings.Contains(err.Error(), "ms_graph") {
			t.Errorf("error should name the rejected provider: %v", err)
		}
		if !strings.Contains(err.Error(), "idp_claims") {
			t.Errorf("error should mention the supported providers: %v", err)
		}
	})

	t.Run("random unknown provider rejected", func(t *testing.T) {
		_, err := NewAuthorizationService(AuthorizationServiceConfig{
			Type:                       "default_rbac",
			RoleToScopeDefinitionsPath: rolesPath,
			UserToRoleProvider:         "not_a_real_provider",
		}, "gw-1", testLogger())
		if err == nil {
			t.Fatal("expected error for unknown role provider")
		}
	})

	t.Run("idp_claims without idp_claims_config rejected", func(t *testing.T) {
		_, err := NewAuthorizationService(AuthorizationServiceConfig{
			Type:                       "default_rbac",
			RoleToScopeDefinitionsPath: rolesPath,
			UserToRoleProvider:         "idp_claims",
		}, "gw-1", testLogger())
		if err == nil {
			t.Fatal("expected error when idp_claims provider is missing its config")
		}
		if !strings.Contains(err.Error(), "idp_claims_config") {
			t.Errorf("error should name the missing config section: %v", err)
		}
	})

	t.Run("empty provider is accepted", func(t *testing.T) {
		_, err := NewAuthorizationService(AuthorizationServiceConfig{
			Type:                       "default_rbac",
			RoleToScopeDefinitionsPath: rolesPath,
			UserToRoleProvider:         "",
		}, "gw-1", testLogger())
		if err != nil {
			t.Fatalf("empty provider should be accepted (YAML + defaults only): %v", err)
		}
	})
}

func writeTempRolesYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "roles.yaml")
	const minimal = `roles:
  viewer:
    scopes:
      - "*"
`
	if err := os.WriteFile(path, []byte(minimal), 0o600); err != nil {
		t.Fatalf("write roles file: %v", err)
	}
	return path
}

func TestInitRBAC_EmptyPath_BrokerOnlyMode(t *testing.T) {
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{
		Type:                       "default_rbac",
		RoleToScopeDefinitionsPath: "",
	}, "gw-1", testLogger())
	if err != nil {
		t.Fatalf("broker-only mode should not error on empty path, got: %v", err)
	}
	if svc.serviceType != "default_rbac" {
		t.Errorf("serviceType should stay default_rbac in broker-only mode, got %q", svc.serviceType)
	}
	if svc.roleEngine != nil {
		t.Error("roleEngine should be nil in broker-only mode (no YAML loaded)")
	}
	if svc.client != nil {
		t.Error("client should be nil before SetAuthorizationClient wires it")
	}
}

func TestLookup_FailsClosed_WhenNoClientAndNoEngine(t *testing.T) {
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{
		Type:                       "default_rbac",
		RoleToScopeDefinitionsPath: "",
	}, "gw-1", testLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	_, err = svc.Lookup(context.Background(), "alice", nil)
	if err == nil {
		t.Fatal("Lookup should fail closed when client is nil and no YAML engine is loaded")
	}
	if !strings.Contains(err.Error(), "AuthorizationClient not wired") {
		t.Errorf("error should explain the missing client, got: %v", err)
	}
}

func TestLookup_UsesClient_OnceWired(t *testing.T) {
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{
		Type:                       "default_rbac",
		RoleToScopeDefinitionsPath: "",
	}, "gw-1", testLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	svc.SetAuthorizationClient(&stubAuthzClient{resolution: auth.AuthorizationResolution{
		Roles:  []string{"sam_admin"},
		Scopes: []string{"*"},
	}})

	res, err := svc.Lookup(context.Background(), "alice", nil)
	if err != nil {
		t.Fatalf("Lookup after SetAuthorizationClient should succeed, got: %v", err)
	}
	if len(res.Scopes) != 1 || res.Scopes[0] != "*" {
		t.Errorf("Lookup should return scopes from the client, got %v", res.Scopes)
	}
	if len(res.Roles) != 1 || res.Roles[0] != "sam_admin" {
		t.Errorf("Lookup should return roles from the client, got %v", res.Roles)
	}
}

type mockRoleProvider struct {
	roles map[string][]string
}

func (m *mockRoleProvider) GetUserRoles(userID string, _ map[string]any) ([]string, error) {
	return m.roles[userID], nil
}

type stubAuthzClient struct {
	resolution auth.AuthorizationResolution
}

func (s *stubAuthzClient) Lookup(_ context.Context, _ string, _ map[string]any) (auth.AuthorizationResolution, error) {
	return s.resolution, nil
}

func TestResolveRoles_SystemPrincipalNeverTakesDefaultRoles(t *testing.T) {
	svc := &AuthorizationService{
		defaultRoles: []string{"sam_user"},
		emailFold:    rbac.BuildEmailFoldIndex(nil),
		logger:       testLogger(),
	}

	for _, tt := range []struct {
		name   string
		userID string
		want   []string
	}{
		{
			name:   "unseeded system principal gets no roles",
			userID: "system:opps",
			want:   nil,
		},
		{
			name:   "custom system user absent from this gateway's YAML gets no roles",
			userID: "system:ops",
			want:   nil,
		},
		{
			name:   "a human identity still takes the defaults",
			userID: "someone@example.com",
			want:   []string{"sam_user"},
		},
		{
			name:   "a chat identity still takes the defaults",
			userID: "whatsapp:15551230000",
			want:   []string{"sam_user"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.ResolveRoles(tt.userID, nil)
			if len(got) != len(tt.want) {
				t.Fatalf("ResolveRoles(%q) = %v, want %v", tt.userID, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ResolveRoles(%q) = %v, want %v", tt.userID, got, tt.want)
				}
			}
		})
	}
}
