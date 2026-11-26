// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samembed"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestValidateGatewayID_Empty(t *testing.T) {
	appConfigs := []samapp.AppEntry{{Name: "gw0"}}
	err := validateGatewayID("", 0, appConfigs, map[string]int{})
	if err == nil || !strings.Contains(err.Error(), "gateway_id is required") {
		t.Errorf("err = %v, want 'gateway_id is required' error", err)
	}
}

func TestValidateGatewayID_Duplicate(t *testing.T) {
	appConfigs := []samapp.AppEntry{{Name: "gw0"}, {Name: "gw1"}}
	seen := map[string]int{"go-mcp-gw": 0}
	err := validateGatewayID("go-mcp-gw", 1, appConfigs, seen)
	if err == nil || !strings.Contains(err.Error(), "duplicate gateway_id") {
		t.Errorf("err = %v, want 'duplicate gateway_id' error", err)
	}
}

func TestValidateGatewayID_Unique(t *testing.T) {
	appConfigs := []samapp.AppEntry{{Name: "gw0"}, {Name: "gw1"}}
	seen := map[string]int{"go-webui-gw": 0}
	if err := validateGatewayID("go-mcp-gw", 1, appConfigs, seen); err != nil {
		t.Errorf("err = %v, want nil for unique gateway_id", err)
	}
}

func TestGatewayKeyPersistenceDir_DefaultsToKeys(t *testing.T) {
	t.Setenv("SAM_DATA_DIR", "")
	if got := gatewayKeyPersistenceDir(); got != "keys" {
		t.Errorf("gatewayKeyPersistenceDir() = %q, want %q", got, "keys")
	}
}

func TestGatewayKeyPersistenceDir_UsesSAMDataDir(t *testing.T) {
	const dataDir = "sam-data"
	t.Setenv("SAM_DATA_DIR", dataDir)
	if got, want := gatewayKeyPersistenceDir(), filepath.Join(dataDir, "keys"); got != want {
		t.Errorf("gatewayKeyPersistenceDir() = %q, want %q", got, want)
	}
}

func TestApplyAuthDefaults_CatalogPresent_AutoInjectsEnterprise(t *testing.T) {
	appCfgs := []samapp.AppEntry{
		{AppConfig: map[string]any{"frontend_use_authorization": true}},
	}
	applyEnterpriseAuthDefaults(appCfgs, true)
	if got := appCfgs[0].AppConfig["external_auth_provider"]; got != "enterprise" {
		t.Errorf("want 'enterprise' auto-injected even with catalog present; got %v", got)
	}
}

func TestApplyAuthDefaults_SingleEntryCatalog_AutoInjectsEnterprise_PreventsNoopBypass(t *testing.T) {
	appCfgs := []samapp.AppEntry{
		{AppConfig: map[string]any{"frontend_use_authorization": true}},
	}
	applyEnterpriseAuthDefaults(appCfgs, true)
	got := appCfgs[0].AppConfig["external_auth_provider"]
	if got != "enterprise" {
		t.Errorf("want 'enterprise' auto-injected (factory-lookup key for catalog path); got %v", got)
	}
}

func TestApplyAuthDefaults_NoCatalog_AutoInjectsEnterprise(t *testing.T) {
	appCfgs := []samapp.AppEntry{
		{AppConfig: map[string]any{"frontend_use_authorization": true}},
	}
	applyEnterpriseAuthDefaults(appCfgs, false)
	if appCfgs[0].AppConfig["external_auth_provider"] != "enterprise" {
		t.Errorf("want legacy auto-inject; got %v", appCfgs[0].AppConfig["external_auth_provider"])
	}
}

func TestApplyAuthDefaults_ExplicitProvider_UnchangedByCatalog(t *testing.T) {
	appCfgs := []samapp.AppEntry{
		{AppConfig: map[string]any{
			"frontend_use_authorization": true,
			"external_auth_provider":     "azure",
		}},
	}
	applyEnterpriseAuthDefaults(appCfgs, false)
	if appCfgs[0].AppConfig["external_auth_provider"] != "azure" {
		t.Errorf("explicit value must not be overwritten; got %v", appCfgs[0].AppConfig["external_auth_provider"])
	}
}

func TestApplyAuthDefaults_EmptyExternalAuthProvider_Injects(t *testing.T) {
	appCfgs := []samapp.AppEntry{
		{AppConfig: map[string]any{
			"frontend_use_authorization": true,
			"external_auth_provider":     "",
		}},
	}
	applyEnterpriseAuthDefaults(appCfgs, true)
	if got := appCfgs[0].AppConfig["external_auth_provider"]; got != "enterprise" {
		t.Errorf("want 'enterprise' injected over present-but-empty value; got %v", got)
	}
}

func TestSetupPerGatewaySigners_OnlyRegistersAuthAdapters(t *testing.T) {
	srv := newMockOIDCServer(t)

	mcpAdapter, err := mcp.NewAdapter(map[string]any{
		"namespace":              "test",
		"gateway_id":             "mcp-gw-h1",
		"enable_auth":            true,
		"external_auth_provider": "test-idp",
	})
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}
	webAdapter := httpsse.NewHTTPSSEAdapter()
	skipAdapter := &fakeAdapter{}

	exe, appConfigs := buildExecutorWithAdapters(t, []struct {
		name    string
		adapter entrypoint.GatewayAdapter
	}{
		{name: "mcp-gw-h1", adapter: mcpAdapter},
		{name: "webui-gw-h1", adapter: webAdapter},
		{name: "slack-gw-h1", adapter: skipAdapter},
	}, "test-idp")

	ec := newLocalTrustManager(t)
	catalog := map[string]*oidcauth.OIDCConfig{
		"test-idp": {Issuer: srv.URL, ClientID: "id", ClientSecret: "secret"},
	}

	if _, err := setupPerGatewaySigners(t.Context(), exe, appConfigs, catalog, ec, nil, "test", discardLogger()); err != nil {
		t.Fatalf("setupPerGatewaySigners: %v", err)
	}

	cfg := map[string]any{
		"external_auth_provider": "test-idp",
		"session_secret_key":     "k",
	}

	if _, err := auth.CreateValidator("mcp-gw-h1:test-idp", cfg); err != nil {
		t.Errorf("MCP validator must be registered: %v", err)
	}
	if _, err := auth.CreateValidator("webui-gw-h1:test-idp", cfg); err != nil {
		t.Errorf("WebUI validator must be registered: %v", err)
	}

	if _, err := auth.CreateValidator("slack-gw-h1:test-idp", cfg); err == nil {
		t.Error("slack-gw-h1 validator must not be registered (skip branch broken)")
	}
}

func TestSetupPerGatewaySigners_InjectsAuthDepsForMCP(t *testing.T) {
	srv := newMockOIDCServer(t)

	mcpAdapter, err := mcp.NewAdapter(map[string]any{
		"namespace":              "test",
		"gateway_id":             "mcp-gw-h2",
		"enable_auth":            true,
		"external_auth_provider": "test-idp",
	})
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}

	exe, appConfigs := buildExecutorWithAdapters(t, []struct {
		name    string
		adapter entrypoint.GatewayAdapter
	}{
		{name: "mcp-gw-h2", adapter: mcpAdapter},
	}, "test-idp")

	ec := newLocalTrustManager(t)
	catalog := map[string]*oidcauth.OIDCConfig{
		"test-idp": {Issuer: srv.URL, ClientID: "id", ClientSecret: "secret"},
	}

	if _, err := setupPerGatewaySigners(t.Context(), exe, appConfigs, catalog, ec, nil, "test", discardLogger()); err != nil {
		t.Fatalf("setupPerGatewaySigners: %v", err)
	}

	deps := mcpAdapter.AuthDeps()
	if deps == nil {
		t.Fatal("AuthDeps must be non-nil after setup for an MCP gateway")
	}
	if deps.IDPClient == nil {
		t.Error("IDPClient must be wired")
	}
	if deps.SAMMinter == nil {
		t.Error("SAMMinter must be wired")
	}
	if deps.AuthzResolver == nil {
		t.Error("AuthzResolver must be wired")
	}
}

func TestSetupPerGatewaySigners_StateRedirectionEnabled_PopulatesSignerAndVerifier(t *testing.T) {
	srv := newMockOIDCServer(t)

	mcpAdapter, err := mcp.NewAdapter(map[string]any{
		"namespace":              "test",
		"gateway_id":             "mcp-gw-sr1",
		"enable_auth":            true,
		"external_auth_provider": "test-idp",
	})
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}

	exe, appConfigs := buildExecutorWithAdapters(t, []struct {
		name    string
		adapter entrypoint.GatewayAdapter
	}{
		{name: "mcp-gw-sr1", adapter: mcpAdapter},
	}, "test-idp")

	appConfigs[0].AppConfig["gateway_adapter"] = map[string]any{
		"adapter_config": map[string]any{
			"state_redirection_enabled": true,
		},
	}

	ec := newLocalTrustManager(t)
	catalog := map[string]*oidcauth.OIDCConfig{
		"test-idp": {Issuer: srv.URL, ClientID: "id", ClientSecret: "secret"},
	}

	if _, err := setupPerGatewaySigners(t.Context(), exe, appConfigs, catalog, ec, nil, "test", discardLogger()); err != nil {
		t.Fatalf("setupPerGatewaySigners: %v", err)
	}

	deps := mcpAdapter.AuthDeps()
	if deps == nil {
		t.Fatal("AuthDeps must be non-nil after setup for an MCP gateway")
	}
	if deps.StateSigner == nil {
		t.Error("StateSigner must be wired when state_redirection_enabled=true (pre-Init)")
	}
	if deps.StateVerifier == nil {
		t.Error("StateVerifier must be wired when state_redirection_enabled=true (pre-Init)")
	}
}

func TestInjectMCPAuthDeps_WrapsParseConfigError(t *testing.T) {
	mcpAdapter, err := mcp.NewAdapter(map[string]any{
		"namespace":  "test",
		"gateway_id": "mcp-bad-1",
	})
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}
	signer := newTestTrustSigner(t, "mcp-bad-1")

	entry := samapp.AppEntry{
		Name:      "x",
		AppConfig: map[string]any{},
	}
	err = injectMCPAuthDeps(mcpAdapter, "mcp-bad-1", signer, entry, nil, nil, discardLogger())
	if err == nil {
		t.Fatal("expected error when catalog is empty and provider not configured")
	}
	if !strings.Contains(err.Error(), "mcp-bad-1") {
		t.Errorf("error must include gateway ID for diagnostics; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "parse enterprise auth") {
		t.Errorf("error must wrap with parse-enterprise-auth context; got %q", err.Error())
	}
}

func TestRegisterMCPMetadataRoutes_OnlyForMCPGateways(t *testing.T) {
	mcpAdapter, err := mcp.NewAdapter(map[string]any{
		"namespace":              "test",
		"gateway_id":             "mcp-gw-h3",
		"enable_auth":            true,
		"external_auth_provider": "test-idp",
	})
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}
	skipAdapter := &fakeAdapter{}

	exe, _ := buildExecutorWithAdapters(t, []struct {
		name    string
		adapter entrypoint.GatewayAdapter
	}{
		{name: "mcp-gw-h3", adapter: mcpAdapter},
		{name: "slack-gw-h3", adapter: skipAdapter},
	}, "test-idp")

	proxy := entrypoint.NewGatewayProxy(":0", discardLogger())
	registerMCPMetadataRoutes(proxy, exe, discardLogger())

	if !proxy.HasMCPMetadataRoute("mcp-gw-h3") {
		t.Error("MCP gateway must have metadata route registered")
	}
	if proxy.HasMCPMetadataRoute("slack-gw-h3") {
		t.Error("non-MCP gateway must not have metadata route registered (skip branch broken)")
	}
}

func TestRegisterGatewaySigner_ReusesPrimaryWhenGwIDMatchesComponentID(t *testing.T) {
	ec := newLocalTrustManager(t)
	primary := ec.TrustManager.Signer()
	if primary == nil {
		t.Fatal("primary signer must be non-nil for a gateway-typed trust manager")
	}
	sgnr, err := registerGatewaySigner(t.Context(), "test-host", "", ec)
	if err != nil {
		t.Fatalf("registerGatewaySigner: %v", err)
	}
	if sgnr != primary {
		t.Errorf("expected primary signer to be reused when gwID matches componentID; got distinct signer")
	}
}

func TestRegisterGatewaySigner_NewSignerWhenGwIDDiffers(t *testing.T) {
	ec := newLocalTrustManager(t)
	primary := ec.TrustManager.Signer()
	sgnr, err := registerGatewaySigner(t.Context(), "other-gw", "", ec)
	if err != nil {
		t.Fatalf("registerGatewaySigner: %v", err)
	}
	if sgnr == nil {
		t.Fatal("expected non-nil signer for distinct gwID")
	}
	if sgnr == primary {
		t.Errorf("expected distinct signer when gwID differs from componentID; got primary")
	}
}

func TestResolveGatewayPublicURL_ExplicitOverrideWins(t *testing.T) {
	entry := samapp.AppEntry{AppConfig: map[string]any{
		"mcp_oauth_dispatcher": map[string]any{
			"enabled":      true,
			"external_url": "https://override.example.com",
		},
		"gateway_adapter": map[string]any{
			"adapter_config": map[string]any{"issuer": "https://gw-mcp.example.com/"},
		},
	}}
	got := resolveGatewayPublicURL(entry, false, true)
	if got != "https://override.example.com" {
		t.Errorf("got %q, want override URL", got)
	}
}

func TestResolveGatewayPublicURL_MCPDefaultsToIssuer(t *testing.T) {
	entry := samapp.AppEntry{AppConfig: map[string]any{
		"gateway_adapter": map[string]any{
			"adapter_config": map[string]any{"issuer": "https://gw-mcp.example.com/"},
		},
	}}
	got := resolveGatewayPublicURL(entry, false, true)
	if got != "https://gw-mcp.example.com" {
		t.Errorf("got %q, want issuer without trailing slash", got)
	}
}

func TestResolveGatewayPublicURL_WebUIDefaultsToFrontendServerURL(t *testing.T) {
	entry := samapp.AppEntry{AppConfig: map[string]any{
		"frontend_server_url": "https://webui.example.com",
	}}
	got := resolveGatewayPublicURL(entry, true, false)
	if got != "https://webui.example.com" {
		t.Errorf("got %q, want webui frontend URL", got)
	}
}

func TestResolveGatewayPublicURL_NoneConfigured(t *testing.T) {
	entry := samapp.AppEntry{AppConfig: map[string]any{}}
	got := resolveGatewayPublicURL(entry, false, true)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestResolveDispatcher_WebUIDefault(t *testing.T) {
	gws := []gatewayDescriptor{
		{GwID: "webui-gw", IsHTTPSSE: true, AppConfig: map[string]any{
			"frontend_server_url": "https://webui.example.com",
		}},
		{GwID: "mcp-gw", IsMCP: true, AppConfig: map[string]any{
			"gateway_adapter": map[string]any{
				"adapter_config": map[string]any{"issuer": "https://mcp.example.com/"},
			},
		}},
	}
	chosen, source := resolveDispatcher(gws)
	if chosen != "webui-gw" {
		t.Errorf("chosen = %q, want webui-gw", chosen)
	}
	if source != "webui_default" {
		t.Errorf("source = %q, want webui_default", source)
	}
}

func TestResolveDispatcher_ExplicitOverrideWinsOverWebUI(t *testing.T) {
	gws := []gatewayDescriptor{
		{GwID: "webui-gw", IsHTTPSSE: true, AppConfig: map[string]any{
			"frontend_server_url": "https://webui.example.com",
		}},
		{GwID: "mcp-override", IsMCP: true, AppConfig: map[string]any{
			"mcp_oauth_dispatcher": map[string]any{"enabled": true, "external_url": "https://override.example.com"},
			"gateway_adapter": map[string]any{
				"adapter_config": map[string]any{"issuer": "https://mcp.example.com/"},
			},
		}},
	}
	chosen, source := resolveDispatcher(gws)
	if chosen != "mcp-override" {
		t.Errorf("chosen = %q, want mcp-override", chosen)
	}
	if source != "explicit_override" {
		t.Errorf("source = %q, want explicit_override", source)
	}
}

func TestResolveDispatcher_NoWebUINoOverride_None(t *testing.T) {
	gws := []gatewayDescriptor{
		{GwID: "mcp-gw", IsMCP: true, AppConfig: map[string]any{
			"gateway_adapter": map[string]any{
				"adapter_config": map[string]any{"issuer": "https://mcp.example.com/"},
			},
		}},
	}
	chosen, source := resolveDispatcher(gws)
	if chosen != "" {
		t.Errorf("chosen = %q, want empty", chosen)
	}
	if source != "none" {
		t.Errorf("source = %q, want none", source)
	}
}

func TestResolveDispatcher_MultipleExplicit_FirstByLexOrder(t *testing.T) {
	gws := []gatewayDescriptor{
		{GwID: "z-gw", IsMCP: true, AppConfig: map[string]any{
			"mcp_oauth_dispatcher": map[string]any{"enabled": true},
		}},
		{GwID: "a-gw", IsMCP: true, AppConfig: map[string]any{
			"mcp_oauth_dispatcher": map[string]any{"enabled": true},
		}},
	}
	chosen, source := resolveDispatcher(gws)
	if chosen != "a-gw" {
		t.Errorf("chosen = %q, want a-gw (first by lex order)", chosen)
	}
	if source != "explicit_override" {
		t.Errorf("source = %q, want explicit_override", source)
	}
}

func TestDispatcherSelfCheck_MultipleExplicit_Warns(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gws := []gatewayDescriptor{
		{GwID: "a-gw", IsMCP: true, AppConfig: map[string]any{
			"mcp_oauth_dispatcher": map[string]any{"enabled": true},
		}},
		{GwID: "z-gw", IsMCP: true, AppConfig: map[string]any{
			"mcp_oauth_dispatcher": map[string]any{"enabled": true},
		}},
	}
	runDispatcherSelfChecks(gws, "a-gw", logger)
	out := buf.String()
	if !strings.Contains(out, "multiple gateways") {
		t.Errorf("expected warning about multiple explicit dispatchers, got: %s", out)
	}
	if !strings.Contains(out, "a-gw") || !strings.Contains(out, "z-gw") {
		t.Errorf("expected warning to list both gateway IDs, got: %s", out)
	}
}

func TestDispatcherSelfCheck_NoneButMCPNeedsIt_Warns(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gws := []gatewayDescriptor{
		{GwID: "mcp-gw", IsMCP: true, AppConfig: map[string]any{
			"gateway_adapter": map[string]any{
				"adapter_config": map[string]any{"state_redirection_enabled": true},
			},
		}},
	}
	runDispatcherSelfChecks(gws, "", logger)
	out := buf.String()
	if !strings.Contains(out, "state_redirection_enabled") {
		t.Errorf("expected warning about state_redirection_enabled without dispatcher, got: %s", out)
	}
}

func TestDispatcherSelfCheck_NoneAndNoMCPNeedsIt_Silent(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gws := []gatewayDescriptor{
		{GwID: "mcp-gw", IsMCP: true, AppConfig: map[string]any{
			"gateway_adapter": map[string]any{
				"adapter_config": map[string]any{},
			},
		}},
	}
	runDispatcherSelfChecks(gws, "", logger)
	if buf.Len() != 0 {
		t.Errorf("expected no warnings, got: %s", buf.String())
	}
}

func TestCreatePlatformService_InheritsIDPClaimsFromGateway(t *testing.T) {
	t.Parallel()

	gatewayAppConfig := map[string]any{
		"authorization_service": map[string]any{
			"type":                  "default_rbac",
			"user_to_role_provider": "idp_claims",
			"idp_claims_config": map[string]any{
				"oidc_provider": "azure",
				"claim_key":     "groups",
				"mappings": map[string]any{
					"grp-admins": []any{"admin"},
				},
			},
		},
	}
	platformAppConfigWithoutIdpClaims := map[string]any{}

	fullAuthzCfg := samembed.ParsePlatformAuthzConfig(platformAppConfigWithoutIdpClaims)
	gatewayAuthzCfg := samembed.ParseGatewayAuthzConfig(gatewayAppConfig)
	fullAuthzCfg = samembed.MergePlatformAuthzFromGateway(fullAuthzCfg, gatewayAuthzCfg)

	rbacYAML, err := entplatform.BuildRBACYAMLState(fullAuthzCfg, nil)
	if err != nil {
		t.Fatalf("BuildRBACYAMLState: %v", err)
	}
	if len(rbacYAML.ClaimMappings) == 0 {
		t.Fatal("ClaimMappings is empty — SSO users will fall back to default_roles (regression)")
	}
	found := false
	for _, m := range rbacYAML.ClaimMappings {
		if m.ClaimValue == "grp-admins" && m.RoleID == "admin" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected grp-admins→admin mapping; got %+v", rbacYAML.ClaimMappings)
	}
	if fullAuthzCfg.UserToRoleProvider != "idp_claims" {
		t.Errorf("UserToRoleProvider = %q, want idp_claims", fullAuthzCfg.UserToRoleProvider)
	}
}

func TestPlatformAuthzConfig_SeedsClaimProviderFromExternalAuth(t *testing.T) {
	t.Parallel()

	gatewayAppConfig := func() map[string]any {
		return map[string]any{
			"external_auth_provider":  "azure",
			"external_auth_claim_key": "groups",
			"authorization_service": map[string]any{
				"type":                  "default_rbac",
				"user_to_role_provider": "idp_claims",
				"idp_claims_config":     map[string]any{"mappings": map[string]any{"grp-admins": []any{"admin"}}},
			},
		}
	}

	t.Run("the gateway's external_auth_provider reaches the inherited block", func(t *testing.T) {
		t.Parallel()
		cfg := platformAuthzConfig(map[string]any{}, gatewayAppConfig())
		if cfg.IDPClaimsConfig == nil {
			t.Fatal("IDPClaimsConfig is nil; DB claim mappings have no provider to inherit")
		}
		if cfg.IDPClaimsConfig.OIDCProvider != "azure" {
			t.Errorf("OIDCProvider = %q, want azure; a mapping stamped otherwise never matches a token", cfg.IDPClaimsConfig.OIDCProvider)
		}
		if cfg.IDPClaimsConfig.ClaimKey != "groups" {
			t.Errorf("ClaimKey = %q, want groups", cfg.IDPClaimsConfig.ClaimKey)
		}
		if got := cfg.IDPClaimsConfig.Mappings["grp-admins"]; len(got) != 1 || got[0] != "admin" {
			t.Errorf("Mappings[grp-admins] = %v, want [admin]; seeding must not drop the gateway's mappings", got)
		}
	})

	t.Run("the platform's own external_auth_provider wins over the gateway's", func(t *testing.T) {
		t.Parallel()
		cfg := platformAuthzConfig(map[string]any{"external_auth_provider": "okta"}, gatewayAppConfig())
		if cfg.IDPClaimsConfig.OIDCProvider != "okta" {
			t.Errorf("OIDCProvider = %q, want okta; the platform's own value must win over the gateway fallback", cfg.IDPClaimsConfig.OIDCProvider)
		}
	})
}
