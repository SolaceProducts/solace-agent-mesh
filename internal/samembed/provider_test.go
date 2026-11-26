// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samembed

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	samauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "samembed-test-data-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("SAM_DATA_DIR", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, "samembed TestMain: remove temp dir:", err)
	}
	os.Exit(code)
}

func setupBootstrappedProvider(t *testing.T, fullCfg map[string]any) *provider {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	p := &provider{}
	opts, comp, err := p.Bootstrap(context.Background(), fullCfg, nil, brk, discardLogger())
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(opts) == 0 {
		t.Error("expected runtime options from Bootstrap")
	}
	if comp == nil {
		t.Error("expected runtime component from Bootstrap")
	}
	return p
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEmbeddedKeyPersistenceDir_DefaultsToKeys(t *testing.T) {
	t.Setenv("SAM_DATA_DIR", "")
	if got := embeddedKeyPersistenceDir(); got != "keys" {
		t.Errorf("embeddedKeyPersistenceDir() = %q, want %q", got, "keys")
	}
}

func TestEmbeddedKeyPersistenceDir_UsesSAMDataDir(t *testing.T) {
	const dataDir = "sam-data"
	t.Setenv("SAM_DATA_DIR", dataDir)
	if got, want := embeddedKeyPersistenceDir(), filepath.Join(dataDir, "keys"); got != want {
		t.Errorf("embeddedKeyPersistenceDir() = %q, want %q", got, want)
	}
}

func TestBootstrapPersistsSigningKey(t *testing.T) {
	setupBootstrappedProvider(t, map[string]any{})

	want := filepath.Join(os.Getenv("SAM_DATA_DIR"), "keys", "trust_signing_embedded-gateway.pem")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected persisted signing key at %s, got stat error: %v", want, err)
	}
}

func TestParseGatewayAuthzConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      map[string]any
		wantType string
	}{
		{
			name:     "empty config defaults to none",
			cfg:      map[string]any{},
			wantType: "none",
		},
		{
			name:     "frontend_use_authorization enabled defaults to deny_all",
			cfg:      map[string]any{"frontend_use_authorization": true},
			wantType: "deny_all",
		},
		{
			name: "explicit type overrides default",
			cfg: map[string]any{
				"frontend_use_authorization": true,
				"authorization_service":      map[string]any{"type": "none"},
			},
			wantType: "none",
		},
		{
			name: "empty type string falls back to default",
			cfg: map[string]any{
				"authorization_service": map[string]any{"type": ""},
			},
			wantType: "none",
		},
		{
			name: "non-map authorization_service ignored",
			cfg: map[string]any{
				"authorization_service": "not-a-map",
			},
			wantType: "none",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseGatewayAuthzConfig(tc.cfg)
			if got.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tc.wantType)
			}
		})
	}
}

func TestParseGatewayAuthzConfig_IDPClaims(t *testing.T) {
	t.Parallel()

	cfg := ParseGatewayAuthzConfig(map[string]any{
		"authorization_service": map[string]any{
			"type":                           "default_rbac",
			"role_to_scope_definitions_path": "/etc/roles.yaml",
			"user_to_role_assignments_path":  "/etc/users.yaml",
			"user_to_role_provider":          "idp_claims",
			"default_roles":                  []any{"viewer", "editor"},
			"idp_claims_config": map[string]any{
				"oidc_provider": "azure",
				"claim_key":     "groups",
				"mappings": map[string]any{
					"group-uuid-1": []any{"admin"},
				},
			},
		},
	})

	if cfg.Type != "default_rbac" {
		t.Errorf("Type = %q, want default_rbac", cfg.Type)
	}
	if cfg.RoleToScopeDefinitionsPath != "/etc/roles.yaml" {
		t.Errorf("RoleToScopeDefinitionsPath = %q", cfg.RoleToScopeDefinitionsPath)
	}
	if cfg.UserToRoleAssignmentsPath != "/etc/users.yaml" {
		t.Errorf("UserToRoleAssignmentsPath = %q", cfg.UserToRoleAssignmentsPath)
	}
	if cfg.UserToRoleProvider != "idp_claims" {
		t.Errorf("UserToRoleProvider = %q", cfg.UserToRoleProvider)
	}
	if len(cfg.DefaultRoles) != 2 || cfg.DefaultRoles[0] != "viewer" || cfg.DefaultRoles[1] != "editor" {
		t.Errorf("DefaultRoles = %v, want [viewer editor]", cfg.DefaultRoles)
	}
	if cfg.IDPClaimsConfig == nil {
		t.Fatal("IDPClaimsConfig is nil; provider.go wrapper dropped idp_claims_config — " +
			"NewAuthorizationService will reject user_to_role_provider: idp_claims and hooks stay nil")
	}
	if cfg.IDPClaimsConfig.OIDCProvider != "azure" {
		t.Errorf("IDPClaimsConfig.OIDCProvider = %q", cfg.IDPClaimsConfig.OIDCProvider)
	}
	if cfg.IDPClaimsConfig.ClaimKey != "groups" {
		t.Errorf("IDPClaimsConfig.ClaimKey = %q", cfg.IDPClaimsConfig.ClaimKey)
	}
	if got := cfg.IDPClaimsConfig.Mappings["group-uuid-1"]; len(got) != 1 || got[0] != "admin" {
		t.Errorf("Mappings[group-uuid-1] = %v, want [admin]", got)
	}
}

func TestParsePlatformAuthzConfig(t *testing.T) {
	t.Parallel()

	t.Run("defaults to deny_all", func(t *testing.T) {
		t.Parallel()
		cfg := ParsePlatformAuthzConfig(map[string]any{})
		if cfg.Type != "deny_all" {
			t.Errorf("Type = %q, want deny_all", cfg.Type)
		}
		if len(cfg.DefaultRoles) != 0 {
			t.Errorf("DefaultRoles = %v, want empty", cfg.DefaultRoles)
		}
	})

	t.Run("populates all optional fields", func(t *testing.T) {
		t.Parallel()
		cfg := ParsePlatformAuthzConfig(map[string]any{
			"authorization_service": map[string]any{
				"type":                           "default_rbac",
				"role_to_scope_definitions_path": "/etc/roles.yaml",
				"user_to_role_assignments_path":  "/etc/users.yaml",
				"user_to_role_provider":          "bamboohr",
				"default_roles":                  []any{"viewer", "editor"},
			},
		})
		if cfg.Type != "default_rbac" {
			t.Errorf("Type = %q, want default_rbac", cfg.Type)
		}
		if cfg.RoleToScopeDefinitionsPath != "/etc/roles.yaml" {
			t.Errorf("RoleToScopeDefinitionsPath = %q", cfg.RoleToScopeDefinitionsPath)
		}
		if cfg.UserToRoleAssignmentsPath != "/etc/users.yaml" {
			t.Errorf("UserToRoleAssignmentsPath = %q", cfg.UserToRoleAssignmentsPath)
		}
		if cfg.UserToRoleProvider != "bamboohr" {
			t.Errorf("UserToRoleProvider = %q", cfg.UserToRoleProvider)
		}
		if len(cfg.DefaultRoles) != 2 || cfg.DefaultRoles[0] != "viewer" || cfg.DefaultRoles[1] != "editor" {
			t.Errorf("DefaultRoles = %v, want [viewer editor]", cfg.DefaultRoles)
		}
	})

	t.Run("ignores non-string entries in default_roles", func(t *testing.T) {
		t.Parallel()
		cfg := ParsePlatformAuthzConfig(map[string]any{
			"authorization_service": map[string]any{
				"default_roles": []any{"admin", 42, "user", nil},
			},
		})
		if len(cfg.DefaultRoles) != 2 || cfg.DefaultRoles[0] != "admin" || cfg.DefaultRoles[1] != "user" {
			t.Errorf("DefaultRoles = %v, want [admin user]", cfg.DefaultRoles)
		}
	})

	t.Run("empty type string keeps default", func(t *testing.T) {
		t.Parallel()
		cfg := ParsePlatformAuthzConfig(map[string]any{
			"authorization_service": map[string]any{"type": ""},
		})
		if cfg.Type != "deny_all" {
			t.Errorf("Type = %q, want deny_all", cfg.Type)
		}
	})
}

func TestSeedPlatformAuthzDefaults(t *testing.T) {
	t.Parallel()

	devPlatformAppConfig := func() map[string]any {
		return map[string]any{
			"external_auth_provider":  "azure",
			"external_auth_claim_key": "groups",
			"authorization_service":   map[string]any{"type": "default_rbac"},
		}
	}

	t.Run("creates the block and stamps both defaults", func(t *testing.T) {
		t.Parallel()
		cfg := ParsePlatformAuthzConfig(devPlatformAppConfig())
		if cfg.IDPClaimsConfig != nil {
			t.Fatal("parse must leave the block nil so MergePlatformAuthzFromGateway can inherit one")
		}
		SeedPlatformAuthzDefaults(&cfg, devPlatformAppConfig())
		if cfg.IDPClaimsConfig == nil {
			t.Fatal("IDPClaimsConfig is nil; DB claim mappings have no provider to inherit")
		}
		if cfg.IDPClaimsConfig.OIDCProvider != "azure" {
			t.Errorf("OIDCProvider = %q, want azure; a mapping stamped otherwise never matches a token", cfg.IDPClaimsConfig.OIDCProvider)
		}
		if cfg.IDPClaimsConfig.ClaimKey != "groups" {
			t.Errorf("ClaimKey = %q, want groups", cfg.IDPClaimsConfig.ClaimKey)
		}
	})

	t.Run("an explicitly authored provider wins", func(t *testing.T) {
		t.Parallel()
		appConfig := devPlatformAppConfig()
		appConfig["authorization_service"] = map[string]any{
			"type":              "default_rbac",
			"idp_claims_config": map[string]any{"oidc_provider": "okta", "claim_key": "roles"},
		}
		cfg := ParsePlatformAuthzConfig(appConfig)
		SeedPlatformAuthzDefaults(&cfg, appConfig)
		if cfg.IDPClaimsConfig.OIDCProvider != "okta" || cfg.IDPClaimsConfig.ClaimKey != "roles" {
			t.Errorf("authored values overwritten: provider=%q claimKey=%q",
				cfg.IDPClaimsConfig.OIDCProvider, cfg.IDPClaimsConfig.ClaimKey)
		}
	})

	t.Run("a sentinel provider is rewritten to the configured one", func(t *testing.T) {
		t.Parallel()
		appConfig := devPlatformAppConfig()
		appConfig["authorization_service"] = map[string]any{
			"type":              "default_rbac",
			"idp_claims_config": map[string]any{"oidc_provider": "enterprise"},
		}
		cfg := ParsePlatformAuthzConfig(appConfig)
		SeedPlatformAuthzDefaults(&cfg, appConfig)
		if cfg.IDPClaimsConfig.OIDCProvider != "azure" {
			t.Errorf("OIDCProvider = %q, want azure; the sentinel is not a provider tokens carry", cfg.IDPClaimsConfig.OIDCProvider)
		}
	})

	t.Run("embedded PlatformExtensions seeds the composer's claim provider", func(t *testing.T) {
		t.Parallel()
		p := &provider{}
		p.PlatformExtensions(devPlatformAppConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		got := p.routeDep.RBACYAMLState.ClaimOIDCProvider
		if got != "azure" {
			t.Errorf("composer ClaimOIDCProvider = %q, want azure; a claim mapping created in the UI is stamped with this and must match the provider the gateway emits at login", got)
		}
		if key := p.routeDep.RBACYAMLState.ClaimKey; key != "groups" {
			t.Errorf("composer ClaimKey = %q, want groups", key)
		}
	})

	t.Run("ConfigurePlatformAuth seeds the platform API's claim provider", func(t *testing.T) {
		t.Parallel()

		rolesPath := filepath.Join(t.TempDir(), "roles.yaml")
		if err := os.WriteFile(rolesPath, []byte(
			"roles:\n  sam_user:\n    description: \"dev role\"\n    scopes:\n      - \"tool:*:*\"\n",
		), 0o600); err != nil {
			t.Fatalf("write roles file: %v", err)
		}

		appConfig := devPlatformAppConfig()
		appConfig["authorization_service"] = map[string]any{
			"type":                           "default_rbac",
			"role_to_scope_definitions_path": rolesPath,
			"user_to_role_provider":          "idp_claims",
		}

		var buf bytes.Buffer
		p := setupBootstrappedProvider(t, map[string]any{})
		p.useAuth = true
		p.ConfigurePlatformAuth(appConfig, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

		logs := buf.String()
		if strings.Contains(logs, "falling back to dev middleware") {
			t.Fatalf("platform API auth downgraded to dev identity: idp_claims had no config block to read\n%s", logs)
		}
		if !strings.Contains(logs, "oidcProvider=azure") || !strings.Contains(logs, "claimKey=groups") {
			t.Errorf("authorization service did not receive the seeded claim provider; want oidcProvider=azure claimKey=groups\n%s", logs)
		}
	})

	t.Run("seeding after the merge leaves an inherited block intact", func(t *testing.T) {
		t.Parallel()
		platformCfg := ParsePlatformAuthzConfig(map[string]any{})
		gatewayCfg := ParseGatewayAuthzConfig(map[string]any{
			"authorization_service": map[string]any{
				"user_to_role_provider": "idp_claims",
				"idp_claims_config": map[string]any{
					"oidc_provider": "azure", "claim_key": "groups",
					"mappings": map[string]any{"grp-admins": []any{"admin"}},
				},
			},
		})
		merged := MergePlatformAuthzFromGateway(platformCfg, gatewayCfg)
		SeedPlatformAuthzDefaults(&merged, map[string]any{"external_auth_provider": "azure"})
		if merged.IDPClaimsConfig == nil {
			t.Fatal("inherited block lost")
		}
		if got := merged.IDPClaimsConfig.Mappings["grp-admins"]; len(got) != 1 || got[0] != "admin" {
			t.Errorf("Mappings[grp-admins] = %v, want [admin]; the gateway's mappings must survive seeding", got)
		}
	})
}

func TestTrustTaskSignerSignTask(t *testing.T) {
	t.Parallel()

	km, err := trust.NewKeyManager("gw-test")
	if err != nil {
		t.Fatal(err)
	}
	signer := trust.NewSigner(km, "gw-test", time.Hour, 24*time.Hour)
	registry := trust.NewRegistry()
	registry.AddOrUpdate(&trust.TrustCard{
		ComponentType: "gateway",
		ComponentID:   "gw-test",
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
	})
	verifier := trust.NewVerifier(registry, 5*time.Second, discardLogger())
	tts := &TrustTaskSigner{Signer: signer}

	signAndVerify := func(t *testing.T, identity *auth.Subject) *samauth.Identity {
		t.Helper()
		token, err := tts.SignTask(identity, "task-42")
		if err != nil {
			t.Fatal(err)
		}
		verified, err := verifier.Authenticate(t.Context(), token, "task-42")
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		return verified
	}

	t.Run("a key's id reaches the agent", func(t *testing.T) {
		t.Parallel()
		verified := signAndVerify(t, auth.NewTestSubject(auth.SubjectFields{
			UserID:       "system:ci-deployer",
			Name:         "system:ci-deployer",
			AuthMethod:   auth.APIKeyAuthMethod,
			CredentialID: "key-1",
			Scopes:       []string{"agent:*:invoke"},
		}))
		if verified.AuthMethod != auth.APIKeyAuthMethod || verified.CredentialID != "key-1" {
			t.Errorf("verified AuthMethod, CredentialID = %q, %q, want %q, key-1",
				verified.AuthMethod, verified.CredentialID, auth.APIKeyAuthMethod)
		}
		if got := samauth.CallerFromIdentity(verified).CredentialID; got != "key-1" {
			t.Errorf("Caller().CredentialID = %q, want key-1: tools and audit read the caller view, not the claims", got)
		}
	})

	t.Run("a person's token carries no credential id", func(t *testing.T) {
		t.Parallel()
		verified := signAndVerify(t, auth.NewTestSubject(auth.SubjectFields{
			UserID:     "alice",
			Email:      "alice@example.com",
			AuthMethod: "oidc",
			Scopes:     []string{"tool:*:*"},
		}))
		if verified.UserID != "alice" || verified.AuthMethod != "oidc" {
			t.Errorf("verified UserID, AuthMethod = %q, %q, want alice, oidc", verified.UserID, verified.AuthMethod)
		}
		if _, present := verified.Claims["credential_id"]; present {
			t.Errorf("credential_id claim present on a person's token: %v", verified.Claims["credential_id"])
		}
	})
}

func TestAuthzScopeResolverResolveScopes(t *testing.T) {
	t.Parallel()

	t.Run("none grants wildcard", func(t *testing.T) {
		t.Parallel()
		svc, err := oidcauth.NewAuthorizationService(
			oidcauth.AuthorizationServiceConfig{Type: "none"},
			"gw", discardLogger(),
		)
		if err != nil {
			t.Fatal(err)
		}
		r := &AuthzScopeResolver{Svc: svc}
		scopes := r.ResolveScopes("alice", []string{"editor"})
		if len(scopes) != 1 || scopes[0] != "*" {
			t.Errorf("ResolveScopes = %v, want [*]", scopes)
		}
	})

	t.Run("deny_all grants nothing", func(t *testing.T) {
		t.Parallel()
		svc, err := oidcauth.NewAuthorizationService(
			oidcauth.AuthorizationServiceConfig{Type: "deny_all"},
			"gw", discardLogger(),
		)
		if err != nil {
			t.Fatal(err)
		}
		r := &AuthzScopeResolver{Svc: svc}
		scopes := r.ResolveScopes("alice", []string{"editor"})
		if len(scopes) != 0 {
			t.Errorf("ResolveScopes = %v, want empty", scopes)
		}
	})
}

func TestExtensionComponentNoopMethods(t *testing.T) {
	t.Parallel()
	c := &ExtensionComponent{EC: nil}
	if err := c.Init(context.Background(), nil); err != nil {
		t.Errorf("Init returned %v", err)
	}
	if err := c.Health(); err != nil {
		t.Errorf("Health returned %v", err)
	}
}

func TestExtensionComponentStartStop(t *testing.T) {
	t.Parallel()
	p := setupBootstrappedProvider(t, map[string]any{})
	c := &ExtensionComponent{EC: p.ec}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Errorf("Start: %v", err)
	}
	if err := c.Stop(ctx); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestProviderStopNilEC(t *testing.T) {
	t.Parallel()
	p := &provider{}
	if err := p.Stop(context.Background()); err != nil {
		t.Errorf("Stop with nil ec returned %v", err)
	}
}

func TestProviderStopCallsEC(t *testing.T) {
	t.Parallel()
	p := setupBootstrappedProvider(t, map[string]any{})
	if err := p.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestProviderPlatformExtensionsAfterBootstrap(t *testing.T) {
	t.Parallel()
	p := setupBootstrappedProvider(t, map[string]any{})
	ext := p.PlatformExtensions(map[string]any{}, discardLogger())
	if p.routeDep == nil || p.routeDep.FeatureRegistry == nil {
		t.Error("PlatformExtensions should populate routeDep.FeatureRegistry from ec")
	}
	if ext.RegisterRoutes == nil || ext.BackgroundServices == nil || ext.LeaderJobs == nil || ext.AuthMiddleware == nil {
		t.Error("expected all extension points populated")
	}
}

func TestProviderPlatformExtensionsSignsGenerationsOnlyWithAuth(t *testing.T) {
	t.Parallel()
	for _, useAuth := range []bool{false, true} {
		p := setupBootstrappedProvider(t, map[string]any{})
		p.useAuth = useAuth
		p.PlatformExtensions(map[string]any{}, discardLogger())
		if !p.routeDep.EvalGenerationDispatch {
			t.Errorf("useAuth=%v: generations should be dispatched", useAuth)
		}
		if signed := p.routeDep.EvalGenerationSigner != nil; signed != useAuth {
			t.Errorf("useAuth=%v: generation signer set = %v", useAuth, signed)
		}
	}
}

func TestProviderConfigureGatewayNilEC(t *testing.T) {
	t.Parallel()
	p := &provider{}
	if err := p.ConfigureGateway(nil, map[string]any{}, discardLogger()); err != nil {
		t.Errorf("ConfigureGateway returned %v", err)
	}
}

func TestProviderConfigureGateway_InjectsMCPAuthDeps(t *testing.T) {
	t.Parallel()
	p := setupBootstrappedProvider(t, map[string]any{
		"providers": map[string]any{
			"azure": map[string]any{
				"issuer":    "https://azure.example.com/",
				"client_id": "az-id",
			},
		},
	})

	mcpAdapter, err := mcp.NewAdapter(map[string]any{
		"transport": "sse",
	})
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}
	core := entrypoint.NewGatewayCore(mcpAdapter, map[string]any{"gateway_id": "mcp-gw"})

	appCfg := map[string]any{
		"gateway_id":             "mcp-gw",
		"enable_auth":            true,
		"external_auth_provider": "azure",
		"session_secret_key":     "test-session-secret-not-used-in-mcp-flow",
	}
	if err := p.ConfigureGateway(core, appCfg, discardLogger()); err != nil {
		t.Fatalf("ConfigureGateway: %v", err)
	}
	deps := mcpAdapter.AuthDeps()
	if deps == nil {
		t.Fatal("AuthDeps should be populated for MCP gateway with enable_auth: true")
	}
	if deps.IDPClient == nil {
		t.Error("AuthDeps.IDPClient missing")
	}
	if deps.SAMMinter == nil {
		t.Error("AuthDeps.SAMMinter missing")
	}
	if deps.AuthzResolver == nil {
		t.Error("AuthDeps.AuthzResolver missing")
	}
}

func TestProviderConfigureGateway_SkipsMCPAuthDepsWhenAuthDisabled(t *testing.T) {
	t.Parallel()
	p := setupBootstrappedProvider(t, map[string]any{})

	mcpAdapter, err := mcp.NewAdapter(map[string]any{"transport": "sse"})
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}
	core := entrypoint.NewGatewayCore(mcpAdapter, map[string]any{"gateway_id": "mcp-gw"})

	appCfg := map[string]any{
		"gateway_id":  "mcp-gw",
		"enable_auth": false,
	}
	if err := p.ConfigureGateway(core, appCfg, discardLogger()); err != nil {
		t.Fatalf("ConfigureGateway: %v", err)
	}
	if mcpAdapter.AuthDeps() != nil {
		t.Error("AuthDeps must remain nil when enable_auth is false")
	}
}

func TestProviderConfigureGateway_NonMCPAdapterIsNoOp(t *testing.T) {
	t.Parallel()
	p := setupBootstrappedProvider(t, map[string]any{})

	core := entrypoint.NewGatewayCore(entrypoint.NewLoggingAdapter(discardLogger()), map[string]any{"gateway_id": "webui-gw"})
	appCfg := map[string]any{
		"gateway_id":  "webui-gw",
		"enable_auth": true,
	}
	if err := p.ConfigureGateway(core, appCfg, discardLogger()); err != nil {
		t.Fatalf("ConfigureGateway: %v", err)
	}
}

func TestProviderConfigureGateway_MCPAuthMissingCatalogErrors(t *testing.T) {
	t.Parallel()
	p := setupBootstrappedProvider(t, map[string]any{})

	mcpAdapter, err := mcp.NewAdapter(map[string]any{"transport": "sse"})
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}
	core := entrypoint.NewGatewayCore(mcpAdapter, map[string]any{"gateway_id": "mcp-gw"})
	appCfg := map[string]any{
		"gateway_id":             "mcp-gw",
		"enable_auth":            true,
		"external_auth_provider": "azure",
	}
	err = p.ConfigureGateway(core, appCfg, discardLogger())
	if err == nil {
		t.Fatal("expected ConfigureGateway to error when enable_auth=true but no providers catalog is present")
	}
	if !strings.Contains(err.Error(), "wire mcp auth deps") {
		t.Errorf("error missing wrap context: %v", err)
	}
	if mcpAdapter.AuthDeps() != nil {
		t.Error("AuthDeps must remain nil on error path")
	}
}

func TestProviderConfigurePlatformAuthDevFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		p    *provider
	}{
		{"nil ec", &provider{}},
		{"useAuth false", &provider{useAuth: false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mw := tc.p.ConfigurePlatformAuth(map[string]any{}, discardLogger())
			if mw == nil {
				t.Fatal("expected middleware, got nil")
			}
			var gotIdentity bool
			handler := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if id, ok := auth.SubjectFromContext(r.Context()); ok && id != nil {
					gotIdentity = true
				}
			}))
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			handler.ServeHTTP(w, req)
			if !gotIdentity {
				t.Error("dev middleware should attach an Identity to the request context")
			}
		})
	}
}

func TestProviderPlatformExtensions(t *testing.T) {
	t.Parallel()
	p := &provider{}
	ext := p.PlatformExtensions(nil, discardLogger())
	if ext.RegisterRoutes == nil {
		t.Error("RegisterRoutes is nil")
	}
	if ext.BackgroundServices == nil {
		t.Error("BackgroundServices is nil")
	}
	if ext.AuthMiddleware == nil {
		t.Error("AuthMiddleware is nil")
	}
	if svcs := ext.BackgroundServices(nil); len(svcs) != 0 {
		t.Errorf("BackgroundServices = %v, want empty", svcs)
	}
	if jobs := ext.LeaderJobs(nil); len(jobs) != 0 {
		t.Errorf("LeaderJobs = %v, want empty", jobs)
	}
	if p.routeDep == nil {
		t.Fatal("routeDep not populated for a nil appConfig")
	}
	for _, sub := range []string{rbac.EvalSystemUserSub, rbac.DefaultSystemUserSub} {
		if _, ok := p.routeDep.RBACYAMLState.Users.Users[sub]; !ok {
			t.Errorf("RBACYAMLState.Users missing %q with a nil appConfig; got %v",
				sub, p.routeDep.RBACYAMLState.Users.Users)
		}
	}
}

func TestProviderWrapPlatformCore(t *testing.T) {
	t.Parallel()
	p := &provider{}
	core := platform.NewPlatformCore(map[string]any{}, platform.ExtensionPoints{})
	comp := p.WrapPlatformCore(core, discardLogger())
	if comp == nil {
		t.Fatal("WrapPlatformCore returned nil")
	}
	_ = p.WrapPlatformCore(core, discardLogger())
}

func TestProviderBootstrapParsesNamespaceAndGatewayID(t *testing.T) {
	t.Parallel()

	fullCfg := map[string]any{
		"apps": []any{
			"not-a-map",
			map[string]any{
				"app_config": map[string]any{
					"namespace":  "prod-ns",
					"gateway_id": "gw-alpha",
				},
			},
			map[string]any{"name": "other"},
			map[string]any{
				"app_config": map[string]any{
					"gateway_id": "gw-beta",
				},
			},
		},
	}

	p := setupBootstrappedProvider(t, fullCfg)
	if p.ec == nil {
		t.Fatal("provider.ec not populated by Bootstrap")
	}
	if p.ec.TrustManager == nil {
		t.Error("TrustManager missing")
	}
	if p.ec.FeatureRegistry == nil {
		t.Error("FeatureRegistry missing")
	}
}

func TestProviderBootstrapDefaultsWhenNoApps(t *testing.T) {
	t.Parallel()

	p := setupBootstrappedProvider(t, map[string]any{})
	if p.ec == nil {
		t.Fatal("provider.ec not populated")
	}
}

func TestProviderConfigureGatewayWiresGatewayHooks(t *testing.T) {
	t.Parallel()

	p := setupBootstrappedProvider(t, map[string]any{})

	adapter := entrypoint.NewLoggingAdapter(discardLogger())
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":                  "test-ns",
		"gateway_id":                 "gw-test",
		"frontend_use_authorization": true,
	})

	appCfg := map[string]any{
		"namespace":                  "test-ns",
		"gateway_id":                 "gw-test",
		"frontend_use_authorization": true,
	}
	if err := p.ConfigureGateway(core, appCfg, discardLogger()); err != nil {
		t.Fatalf("ConfigureGateway: %v", err)
	}
	if !p.useAuth {
		t.Error("useAuth should be set when frontend_use_authorization is true")
	}
	if got := appCfg["external_auth_provider"]; got != "enterprise" {
		t.Errorf("external_auth_provider = %v, want enterprise", got)
	}
}

func TestProviderConfigureGateway_AuthzBuildFails_EmitsWarn(t *testing.T) {
	t.Parallel()

	p := setupBootstrappedProvider(t, map[string]any{})

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	adapter := entrypoint.NewLoggingAdapter(discardLogger())
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "gw-test",
	})

	appCfg := map[string]any{
		"namespace":                  "test-ns",
		"gateway_id":                 "gw-test",
		"frontend_use_authorization": true,
		"authorization_service": map[string]any{
			"type":                           "default_rbac",
			"role_to_scope_definitions_path": "/nonexistent/roles.yaml",
		},
	}
	if err := p.ConfigureGateway(core, appCfg, logger); err != nil {
		t.Fatalf("ConfigureGateway should swallow authz build errors, got %v", err)
	}
	if core.IdentityResolver() != nil {
		t.Error("IdentityResolver should be nil when NewAuthorizationService fails")
	}
	out := buf.String()
	if !strings.Contains(out, "failed to build authorization service for gateway hooks") {
		t.Errorf("expected Warn log for failed authz service build; got:\n%s", out)
	}
	if !strings.Contains(out, "gatewayID=gw-test") {
		t.Errorf("Warn log missing gatewayID attribute; got:\n%s", out)
	}
}

func TestProviderBootstrapLoadsProvidersCatalog(t *testing.T) {
	t.Parallel()

	p := setupBootstrappedProvider(t, map[string]any{
		"providers": map[string]any{
			"azure": map[string]any{
				"issuer":        "https://azure.example.com/",
				"client_id":     "az-id",
				"client_secret": "az-secret",
			},
		},
	})
	if p.providersCatalog == nil {
		t.Fatal("providersCatalog should be populated from fullConfig")
	}
	if _, ok := p.providersCatalog["azure"]; !ok {
		t.Errorf("catalog missing 'azure'; got keys: %v", p.providersCatalog)
	}
}

func TestProviderConfigureGatewayAutoInjectsEnterpriseEvenWithCatalog(t *testing.T) {
	t.Parallel()

	p := setupBootstrappedProvider(t, map[string]any{
		"providers": map[string]any{
			"azure": map[string]any{
				"issuer":    "https://azure.example.com/",
				"client_id": "az-id",
			},
		},
	})

	adapter := entrypoint.NewLoggingAdapter(discardLogger())
	core := entrypoint.NewGatewayCore(adapter, map[string]any{})

	appCfg := map[string]any{
		"gateway_id":                 "gw-test",
		"frontend_use_authorization": true,
	}
	if err := p.ConfigureGateway(core, appCfg, discardLogger()); err != nil {
		t.Fatalf("ConfigureGateway: %v", err)
	}
	if !p.useAuth {
		t.Error("useAuth should be set when frontend_use_authorization is true")
	}
	if got := appCfg["external_auth_provider"]; got != "enterprise" {
		t.Errorf("external_auth_provider = %v, want 'enterprise' auto-injected even with catalog", got)
	}
}

func TestProviderConfigureGatewayEmptyExternalAuthProviderInjects(t *testing.T) {
	t.Parallel()

	p := setupBootstrappedProvider(t, map[string]any{})

	adapter := entrypoint.NewLoggingAdapter(discardLogger())
	core := entrypoint.NewGatewayCore(adapter, map[string]any{})

	appCfg := map[string]any{
		"gateway_id":                 "gw-test",
		"frontend_use_authorization": true,
		"external_auth_provider":     "",
	}
	if err := p.ConfigureGateway(core, appCfg, discardLogger()); err != nil {
		t.Fatalf("ConfigureGateway: %v", err)
	}
	if got := appCfg["external_auth_provider"]; got != "enterprise" {
		t.Errorf("want 'enterprise' injected over present-but-empty value; got %v", got)
	}
}

func TestProviderConfigureGatewayPreservesExternalAuthProvider(t *testing.T) {
	t.Parallel()

	p := setupBootstrappedProvider(t, map[string]any{})

	adapter := entrypoint.NewLoggingAdapter(discardLogger())
	core := entrypoint.NewGatewayCore(adapter, map[string]any{})

	appCfg := map[string]any{
		"gateway_id":                 "gw-test",
		"frontend_use_authorization": true,
		"external_auth_provider":     "custom",
	}
	if err := p.ConfigureGateway(core, appCfg, discardLogger()); err != nil {
		t.Fatal(err)
	}
	if got := appCfg["external_auth_provider"]; got != "custom" {
		t.Errorf("external_auth_provider = %v, want preserved value 'custom'", got)
	}
}

func TestProviderConfigurePlatformAuthEnabled(t *testing.T) {
	t.Parallel()

	p := setupBootstrappedProvider(t, map[string]any{})
	p.useAuth = true

	t.Run("falls back to dev middleware when authz service creation fails", func(t *testing.T) {
		mw := p.ConfigurePlatformAuth(map[string]any{
			"authorization_service": map[string]any{
				"type":                           "default_rbac",
				"role_to_scope_definitions_path": "/nonexistent/roles.yaml",
				"user_to_role_assignments_path":  "/nonexistent/users.yaml",
			},
		}, discardLogger())
		if mw == nil {
			t.Fatal("expected non-nil middleware")
		}
	})

	t.Run("returns enterprise auth middleware when authz service builds", func(t *testing.T) {
		mw := p.ConfigurePlatformAuth(map[string]any{
			"authorization_service": map[string]any{"type": "none"},
		}, discardLogger())
		if mw == nil {
			t.Fatal("expected non-nil middleware")
		}
	})
}

func TestMergePlatformAuthzFromGateway(t *testing.T) {
	t.Parallel()

	gwClaims := &oidcauth.IDPClaimsConfig{
		OIDCProvider: "azure",
		ClaimKey:     "groups",
		Mappings:     map[string][]string{"grp-1": {"admin"}},
	}

	t.Run("platform nil + gateway set → platform inherits", func(t *testing.T) {
		t.Parallel()
		platform := oidcauth.AuthorizationServiceConfig{UserToRoleProvider: ""}
		gateway := oidcauth.AuthorizationServiceConfig{
			IDPClaimsConfig:    gwClaims,
			UserToRoleProvider: "idp_claims",
		}
		got := MergePlatformAuthzFromGateway(platform, gateway)
		if got.IDPClaimsConfig == nil {
			t.Fatal("IDPClaimsConfig should be inherited from gateway when platform is nil")
		}
		if got.IDPClaimsConfig.OIDCProvider != "azure" {
			t.Errorf("OIDCProvider = %q, want azure", got.IDPClaimsConfig.OIDCProvider)
		}
		if got.UserToRoleProvider != "idp_claims" {
			t.Errorf("UserToRoleProvider = %q, want idp_claims", got.UserToRoleProvider)
		}
	})

	t.Run("platform set + gateway set → platform wins, no overwrite", func(t *testing.T) {
		t.Parallel()
		ownClaims := &oidcauth.IDPClaimsConfig{OIDCProvider: "okta", ClaimKey: "roles"}
		platform := oidcauth.AuthorizationServiceConfig{IDPClaimsConfig: ownClaims}
		gateway := oidcauth.AuthorizationServiceConfig{IDPClaimsConfig: gwClaims}
		got := MergePlatformAuthzFromGateway(platform, gateway)
		if got.IDPClaimsConfig.OIDCProvider != "okta" {
			t.Errorf("platform IDPClaimsConfig should not be overwritten; got OIDCProvider=%q", got.IDPClaimsConfig.OIDCProvider)
		}
	})

	t.Run("both nil → result IDPClaimsConfig still nil", func(t *testing.T) {
		t.Parallel()
		got := MergePlatformAuthzFromGateway(
			oidcauth.AuthorizationServiceConfig{},
			oidcauth.AuthorizationServiceConfig{},
		)
		if got.IDPClaimsConfig != nil {
			t.Errorf("expected nil IDPClaimsConfig, got %+v", got.IDPClaimsConfig)
		}
	})

	t.Run("platform already has UserToRoleProvider → not overwritten", func(t *testing.T) {
		t.Parallel()
		platform := oidcauth.AuthorizationServiceConfig{UserToRoleProvider: "bamboohr"}
		gateway := oidcauth.AuthorizationServiceConfig{
			IDPClaimsConfig:    gwClaims,
			UserToRoleProvider: "idp_claims",
		}
		got := MergePlatformAuthzFromGateway(platform, gateway)
		if got.UserToRoleProvider != "bamboohr" {
			t.Errorf("UserToRoleProvider = %q, want preserved value bamboohr", got.UserToRoleProvider)
		}
	})

	t.Run("inherited pointer is cloned, not shared", func(t *testing.T) {
		t.Parallel()
		gw := oidcauth.AuthorizationServiceConfig{
			IDPClaimsConfig: &oidcauth.IDPClaimsConfig{
				OIDCProvider: "azure",
				ClaimKey:     "groups",
				Mappings:     map[string][]string{"grp-1": {"admin"}},
			},
		}
		platform := oidcauth.AuthorizationServiceConfig{}
		got := MergePlatformAuthzFromGateway(platform, gw)
		if got.IDPClaimsConfig == gw.IDPClaimsConfig {
			t.Error("IDPClaimsConfig must be a clone, not the same pointer as the gateway input")
		}
		got.IDPClaimsConfig.OIDCProvider = "mutated"
		if gw.IDPClaimsConfig.OIDCProvider != "azure" {
			t.Errorf("mutating result affected gateway input; gateway OIDCProvider = %q", gw.IDPClaimsConfig.OIDCProvider)
		}
	})
}

func TestNewProviderReturnsUsableInstance(t *testing.T) {
	t.Parallel()
	prov := NewProvider()
	if prov == nil {
		t.Fatal("NewProvider returned nil")
	}
	if err := prov.Stop(context.Background()); err != nil {
		t.Errorf("Stop on fresh provider returned %v", err)
	}
}

func TestProviderGatewayHooks_BuildResolversWiresRuntimeDeployedGateways(t *testing.T) {
	t.Parallel()

	p := setupBootstrappedProvider(t, map[string]any{})
	hooks := p.GatewayHooks()
	if hooks == nil {
		t.Fatal("GatewayHooks returned nil")
	}
	if hooks.BuildResolvers == nil {
		t.Fatal("BuildResolvers is nil — runtime-deployed gateways will mint JWTs with empty scopes (issue #1329)")
	}

	appCfg := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "gw-runtime",
	}
	scopeRes, identityRes, err := hooks.BuildResolvers("gw-runtime", appCfg)
	if err != nil {
		t.Fatalf("BuildResolvers returned error: %v", err)
	}
	if scopeRes == nil {
		t.Fatal("ScopeResolver is nil; workflow agent delegation will fail closed")
	}
	if identityRes == nil {
		t.Error("IdentityResolver is nil; scheduled-task identity resolution will fail")
	}
	if got := scopeRes.ResolveScopes("alice", []string{"editor"}); len(got) != 1 || got[0] != "*" {
		t.Errorf("default 'none' authz should grant wildcard scope; got %v", got)
	}
}

func TestProviderPlatformExtensions_LoadsRBACYAMLState(t *testing.T) {
	t.Parallel()

	rolesPath := filepath.Join(t.TempDir(), "roles.yaml")
	if err := os.WriteFile(rolesPath, []byte(
		"roles:\n  sam_user:\n    description: \"dev role\"\n    scopes:\n      - \"tool:*:*\"\n",
	), 0o600); err != nil {
		t.Fatalf("write roles file: %v", err)
	}

	p := &provider{}
	p.PlatformExtensions(map[string]any{
		"authorization_service": map[string]any{
			"type":                           "default_rbac",
			"role_to_scope_definitions_path": rolesPath,
			"default_roles":                  []any{"sam_user"},
		},
	}, discardLogger())

	if p.routeDep == nil {
		t.Fatal("routeDep not populated")
	}
	if _, ok := p.routeDep.RBACYAMLState.Roles.Roles["sam_user"]; !ok {
		t.Errorf("RBACYAMLState.Roles missing sam_user; got %v — composer would see yaml_roles=0",
			p.routeDep.RBACYAMLState.Roles.Roles)
	}
	if len(p.routeDep.RBACYAMLState.DefaultRoles) != 1 || p.routeDep.RBACYAMLState.DefaultRoles[0] != "sam_user" {
		t.Errorf("RBACYAMLState.DefaultRoles = %v, want [sam_user]", p.routeDep.RBACYAMLState.DefaultRoles)
	}
}

func TestProviderPlatformExtensions_SetsMiniIDPMode(t *testing.T) {
	t.Parallel()

	p := &provider{}
	p.PlatformExtensions(map[string]any{
		"authorization_service": map[string]any{
			"type":          "default_rbac",
			"mini_idp_mode": true,
		},
	}, discardLogger())

	if p.routeDep == nil {
		t.Fatal("routeDep not populated")
	}
	if !p.routeDep.MiniIDPMode {
		t.Error("routeDep.MiniIDPMode = false, want true")
	}
}

func TestProviderPlatformExtensions_RBACYAMLLoadFailure_LogsErrorAndContinues(t *testing.T) {
	t.Parallel()

	missingPath := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	p := &provider{}
	p.PlatformExtensions(map[string]any{
		"authorization_service": map[string]any{
			"type":                           "default_rbac",
			"role_to_scope_definitions_path": missingPath,
		},
	}, logger)

	if p.routeDep == nil {
		t.Fatal("routeDep not populated — load failure must not abort PlatformExtensions")
	}
	for _, sub := range []string{rbac.EvalSystemUserSub, rbac.DefaultSystemUserSub} {
		if _, ok := p.routeDep.RBACYAMLState.Users.Users[sub]; !ok {
			t.Errorf("RBACYAMLState.Users missing %q after a roles-file load failure; got %v — eval and run_as entrypoints fail closed without it",
				sub, p.routeDep.RBACYAMLState.Users.Users)
		}
	}
	for role := range p.routeDep.RBACYAMLState.Roles.Roles {
		if !strings.HasPrefix(role, "system_") {
			t.Errorf("RBACYAMLState.Roles carries %q after a load failure; only embedded system_* roles may survive", role)
		}
	}

	out := buf.String()
	if !strings.Contains(out, "level=ERROR") {
		t.Errorf("RBAC YAML load failure must surface at ERROR (not a swallowed Warn); got:\n%s", out)
	}
	if !strings.Contains(out, "rbac yaml state load failed") {
		t.Errorf("missing the rbac-load-failure message; got:\n%s", out)
	}
	if !strings.Contains(out, missingPath) {
		t.Errorf("Error log must include the configured rolesPath %q for actionability; got:\n%s", missingPath, out)
	}
}

func TestProviderGatewayHooks_OnGatewayBeforeInit_MCPAuth(t *testing.T) {
	t.Parallel()
	p := setupBootstrappedProvider(t, map[string]any{
		"providers": map[string]any{
			"azure": map[string]any{
				"issuer":    "https://azure.example.com/",
				"client_id": "az-id",
			},
		},
	})

	hooks := p.GatewayHooks()
	if hooks == nil {
		t.Fatal("GatewayHooks returned nil")
	}
	if hooks.OnGatewayBeforeInit == nil {
		t.Fatal("OnGatewayBeforeInit is nil; runtime-deployed MCP gateways fail Init with no validator factory")
	}

	mcpAdapter, err := mcp.NewAdapter(map[string]any{"transport": "sse"})
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}
	appCfg := map[string]any{
		"gateway_id": "mcp",
		"gateway_adapter": map[string]any{
			"type": "mcp",
			"adapter_config": map[string]any{
				"enable_auth":            true,
				"external_auth_provider": "azure",
				"session_secret_key":     "test-session-secret-not-used-in-mcp-flow",
			},
		},
	}
	core := entrypoint.NewGatewayCore(mcpAdapter, appCfg)
	if err := hooks.OnGatewayBeforeInit(context.Background(), core, appCfg); err != nil {
		t.Fatalf("OnGatewayBeforeInit: %v", err)
	}
	deps := mcpAdapter.AuthDeps()
	if deps == nil {
		t.Fatal("AuthDeps should be populated for runtime-deployed MCP gateway with enable_auth: true")
	}
	if deps.IDPClient == nil || deps.SAMMinter == nil || deps.AuthzResolver == nil {
		t.Errorf("AuthDeps incomplete: %+v", deps)
	}
}

func TestPlatformExtensionsPassesTheAuthorizationModeToTheRoutes(t *testing.T) {
	for _, mode := range []string{"default_rbac", "deny_all", "none"} {
		t.Run(mode, func(t *testing.T) {
			appConfig := map[string]any{"authorization_service": map[string]any{"type": mode}}
			p := &provider{}
			p.PlatformExtensions(appConfig, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if got := p.routeDep.AuthorizationMode; got != mode {
				t.Errorf("routeDep.AuthorizationMode = %q, want %q", got, mode)
			}
		})
	}
}

type resolvingKeyAuthority struct{}

func (resolvingKeyAuthority) Validate(context.Context, string) (apikey.Resolution, error) {
	return apikey.Resolution{KeyID: "key-1", OwnerSub: "system:ci-deployer", Scopes: []string{"agent:*:invoke"}}, nil
}

func TestProviderPlatformRoutesAndMiddlewareShareOneKeyHolder(t *testing.T) {
	t.Parallel()

	rolesPath := filepath.Join(t.TempDir(), "roles.yaml")
	if err := os.WriteFile(rolesPath, []byte(
		"roles:\n  sam_user:\n    description: \"dev role\"\n    scopes:\n      - \"tool:*:*\"\n",
	), 0o600); err != nil {
		t.Fatalf("write roles file: %v", err)
	}
	appConfig := map[string]any{
		"external_auth_provider":  "azure",
		"external_auth_claim_key": "groups",
		"authorization_service": map[string]any{
			"type":                           "default_rbac",
			"role_to_scope_definitions_path": rolesPath,
		},
	}

	p := setupBootstrappedProvider(t, map[string]any{})
	p.useAuth = true
	if p.ec.KeyAuthority == nil {
		t.Fatal("Bootstrap left no shared key holder")
	}
	p.PlatformExtensions(appConfig, discardLogger())
	middleware := p.ConfigurePlatformAuth(appConfig, discardLogger())

	if p.routeDep.KeyAuthority != p.ec.KeyAuthority {
		t.Fatal("the platform routes were given a different holder from the shared one")
	}

	p.routeDep.KeyAuthority.SetAuthority(resolvingKeyAuthority{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil)
	req.Header.Set("Authorization", "Bearer samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
	resp := httptest.NewRecorder()
	middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: the middleware does not validate against the holder the routes bind; body=%s",
			resp.Code, resp.Body.String())
	}
}

func TestProviderConfigureGateway_WiresTheKeyHolder(t *testing.T) {
	t.Parallel()
	p := setupBootstrappedProvider(t, map[string]any{
		"providers": map[string]any{
			"azure": map[string]any{"issuer": "https://azure.example.com/", "client_id": "az-id"},
		},
	})
	p.ec.KeyAuthority.SetAuthority(resolvingKeyAuthority{})

	core := entrypoint.NewGatewayCore(httpsse.NewHTTPSSEAdapter(), map[string]any{"gateway_id": "webui-embedded-keys"})
	cfg := map[string]any{
		"gateway_id":             "webui-embedded-keys",
		"external_auth_provider": "azure",
		"session_secret_key":     "test-session-secret",
	}
	if err := p.ConfigureGateway(core, cfg, discardLogger()); err != nil {
		t.Fatalf("ConfigureGateway: %v", err)
	}
	t.Cleanup(func() { oidcauth.UnregisterFactoryForGateway("webui-embedded-keys") })

	built, err := auth.CreateProvider("webui-embedded-keys", cfg)
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	var got *auth.Subject
	handler := built.(*oidcauth.Provider).Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if got == nil || got.UserID() != "system:ci-deployer" {
		t.Errorf("identity = %+v, want the key owner", got)
	}
}
