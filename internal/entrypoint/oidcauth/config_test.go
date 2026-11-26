// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"strings"
	"testing"
)

func TestParseConfig_Required(t *testing.T) {
	_, err := ParseConfig(map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error when no providers catalog is configured")
	}
}

func TestParseConfig_MissingSessionSecretKey_WithOIDC(t *testing.T) {
	t.Setenv("SESSION_SECRET_KEY", "")

	catalog := map[string]*OIDCConfig{
		"only": {
			Issuer:       "https://idp.example.com",
			ClientID:     "c",
			ClientSecret: "s",
		},
	}
	_, err := ParseConfig(map[string]any{}, catalog)
	if err == nil {
		t.Fatal("expected error for missing session_secret_key when OIDC is enabled")
	}
}

func TestParseConfig_Minimal(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://idp.example.com",
			ClientID:     "c",
			ClientSecret: "s",
		},
	}
	cfg, err := ParseConfig(map[string]any{
		"external_auth_provider": "azure",
		"session_secret_key":     "test-secret-key",
	}, catalog)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.ExternalAuthProvider != "azure" {
		t.Errorf("ExternalAuthProvider = %q, want azure", cfg.ExternalAuthProvider)
	}
	if cfg.AuthorizationService.Type != "deny_all" {
		t.Errorf("AuthorizationService.Type = %q, want deny_all", cfg.AuthorizationService.Type)
	}
	if cfg.SAMAccessToken.TTLSeconds != 3600 {
		t.Errorf("SAMAccessToken.TTLSeconds = %d, want 3600 (1 hour default)", cfg.SAMAccessToken.TTLSeconds)
	}
}

func TestParseConfig_Full(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://idp.example.com",
			ClientID:     "c",
			ClientSecret: "s",
		},
	}
	cfg, err := ParseConfig(map[string]any{
		"external_auth_provider":     "azure",
		"external_auth_callback_uri": "https://gw.example.com/api/v1/auth/callback",
		"frontend_auth_login_url":    "https://auth.example.com/login",
		"frontend_redirect_url":      "https://app.example.com",
		"frontend_server_url":        "https://gw.example.com",
		"session_secret_key":         "test-secret-key",
		"authorization_service": map[string]any{
			"type":                           "default_rbac",
			"role_to_scope_definitions_path": "/etc/sam/roles.yaml",
			"user_to_role_assignments_path":  "/etc/sam/users.yaml",
			"user_to_role_provider":          "idp_claims",
			"default_roles":                  []any{"viewer"},
			"idp_claims_config": map[string]any{
				"oidc_provider": "azure",
				"claim_key":     "groups",
				"mappings": map[string]any{
					"group-uuid-1": []any{"admin"},
				},
			},
		},
		"sam_access_token": map[string]any{
			"ttl_seconds": 7200,
		},
	}, catalog)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if cfg.ExternalAuthProvider != "azure" {
		t.Errorf("ExternalAuthProvider = %q, want azure", cfg.ExternalAuthProvider)
	}
	if cfg.FrontendRedirectURL != "https://app.example.com" {
		t.Errorf("FrontendRedirectURL = %q", cfg.FrontendRedirectURL)
	}

	authz := cfg.AuthorizationService
	if authz.Type != "default_rbac" {
		t.Errorf("AuthorizationService.Type = %q", authz.Type)
	}
	if authz.UserToRoleProvider != "idp_claims" {
		t.Errorf("UserToRoleProvider = %q", authz.UserToRoleProvider)
	}
	if len(authz.DefaultRoles) != 1 || authz.DefaultRoles[0] != "viewer" {
		t.Errorf("DefaultRoles = %v", authz.DefaultRoles)
	}

	if authz.IDPClaimsConfig == nil {
		t.Fatal("IDPClaimsConfig should not be nil")
	}
	if authz.IDPClaimsConfig.OIDCProvider != "azure" {
		t.Errorf("IDPClaimsConfig.OIDCProvider = %q", authz.IDPClaimsConfig.OIDCProvider)
	}
	if authz.IDPClaimsConfig.ClaimKey != "groups" {
		t.Errorf("IDPClaimsConfig.ClaimKey = %q", authz.IDPClaimsConfig.ClaimKey)
	}
	if len(authz.IDPClaimsConfig.Mappings) != 1 {
		t.Errorf("IDPClaimsConfig.Mappings = %v", authz.IDPClaimsConfig.Mappings)
	}

	if cfg.SAMAccessToken.TTLSeconds != 7200 {
		t.Errorf("SAMAccessToken.TTLSeconds = %d, want 7200", cfg.SAMAccessToken.TTLSeconds)
	}
}

func TestParseConfig_CatalogMatch(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://azure/",
			ClientID:     "az",
			ClientSecret: "az-sec",
		},
	}
	app := map[string]any{
		"external_auth_provider":     "azure",
		"external_auth_callback_uri": "https://gw/cb",
		"session_secret_key":         "k",
	}
	cfg, err := ParseConfig(app, catalog)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.OIDC == nil {
		t.Fatalf("cfg.OIDC should be populated from catalog")
	}
	if cfg.OIDC.Issuer != "https://azure/" {
		t.Errorf("issuer=%q want https://azure/", cfg.OIDC.Issuer)
	}
}

func TestParseConfig_SingleEntryAutoSelect(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"only": {Issuer: "https://i/", ClientID: "c", ClientSecret: "s"},
	}
	app := map[string]any{
		"session_secret_key": "k",
	}
	cfg, err := ParseConfig(app, catalog)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.OIDC == nil {
		t.Fatalf("single-entry catalog should auto-select even when provider is unset")
	}
	if cfg.ExternalAuthProvider != "only" {
		t.Errorf("cfg.ExternalAuthProvider=%q want auto-assigned 'only'", cfg.ExternalAuthProvider)
	}
}

func TestParseConfig_UnknownProvider_NoLegacy(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {Issuer: "https://a/", ClientID: "c", ClientSecret: "s"},
	}
	app := map[string]any{
		"external_auth_provider": "okta",
		"session_secret_key":     "k",
	}
	_, err := ParseConfig(app, catalog)
	if err == nil {
		t.Fatalf("want error for unknown provider with no legacy URL")
	}
	if !strings.Contains(err.Error(), "azure") {
		t.Errorf("error should list available providers; got: %v", err)
	}
}

func TestParseConfig_MultiEntry_UnsetProvider(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"a": {Issuer: "https://a/", ClientID: "ca", ClientSecret: "sa"},
		"b": {Issuer: "https://b/", ClientID: "cb", ClientSecret: "sb"},
	}
	app := map[string]any{
		"session_secret_key": "k",
	}
	_, err := ParseConfig(app, catalog)
	if err == nil {
		t.Fatalf("want error for unset provider with multi-entry catalog")
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Errorf("error should list available providers; got: %v", err)
	}
}

func TestParseConfig_EnvVarsAlone_FailStartup(t *testing.T) {
	t.Setenv("OIDC_ISSUER", "https://env-only/")
	t.Setenv("OIDC_CLIENT_ID", "env-id")
	t.Setenv("OIDC_CLIENT_SECRET", "env-sec")

	app := map[string]any{
		"session_secret_key": "k",
	}
	_, err := ParseConfig(app, nil)
	if err == nil {
		t.Fatalf("want startup-fail when env vars are set but no catalog or legacy URL")
	}
	if !strings.Contains(err.Error(), "providers") {
		t.Errorf("error should point at providers catalog; got: %v", err)
	}
}

func TestParseConfig_CatalogPresent_NoGenericDefault(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"a": {Issuer: "i", ClientID: "c", ClientSecret: "s"},
		"b": {Issuer: "i2", ClientID: "c2", ClientSecret: "s2"},
	}
	app := map[string]any{"session_secret_key": "k"}
	_, err := ParseConfig(app, catalog)
	if err == nil {
		t.Fatalf("want error; 'generic' should NOT default to a real value when catalog is present")
	}
	if strings.Contains(err.Error(), `"generic"`) {
		t.Errorf("error should not mention 'generic'; it's an internal default: %v", err)
	}
}

func TestParseConfig_IdpClaimsProviderDefault(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {Issuer: "i", ClientID: "c", ClientSecret: "s"},
	}
	app := map[string]any{
		"external_auth_provider": "azure",
		"session_secret_key":     "k",
		"authorization_service": map[string]any{
			"type":                  "default_rbac",
			"user_to_role_provider": "idp_claims",
			"idp_claims_config": map[string]any{
				"claim_key": "groups",
			},
		},
	}
	cfg, err := ParseConfig(app, catalog)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.AuthorizationService.IDPClaimsConfig == nil {
		t.Fatalf("IDPClaimsConfig should be populated")
	}
	if cfg.AuthorizationService.IDPClaimsConfig.OIDCProvider != "azure" {
		t.Errorf("want defaulted to 'azure'; got %q", cfg.AuthorizationService.IDPClaimsConfig.OIDCProvider)
	}
}

func TestSeedIDPClaimsConfigDefaults(t *testing.T) {
	t.Run("creates block from top-level when absent", func(t *testing.T) {
		cfg := &AuthorizationServiceConfig{}
		SeedIDPClaimsConfigDefaults(cfg, "azure", "groups")
		if cfg.IDPClaimsConfig == nil {
			t.Fatal("IDPClaimsConfig should be created")
		}
		if cfg.IDPClaimsConfig.OIDCProvider != "azure" || cfg.IDPClaimsConfig.ClaimKey != "groups" {
			t.Errorf("got (%q, %q), want (azure, groups)", cfg.IDPClaimsConfig.OIDCProvider, cfg.IDPClaimsConfig.ClaimKey)
		}
	})

	t.Run("explicit nested values win", func(t *testing.T) {
		cfg := &AuthorizationServiceConfig{IDPClaimsConfig: &IDPClaimsConfig{OIDCProvider: "okta", ClaimKey: "roles"}}
		SeedIDPClaimsConfigDefaults(cfg, "azure", "groups")
		if cfg.IDPClaimsConfig.OIDCProvider != "okta" || cfg.IDPClaimsConfig.ClaimKey != "roles" {
			t.Errorf("got (%q, %q), want (okta, roles)", cfg.IDPClaimsConfig.OIDCProvider, cfg.IDPClaimsConfig.ClaimKey)
		}
	})

	t.Run("rewrites legacy provider sentinel", func(t *testing.T) {
		cfg := &AuthorizationServiceConfig{IDPClaimsConfig: &IDPClaimsConfig{OIDCProvider: "enterprise"}}
		SeedIDPClaimsConfigDefaults(cfg, "azure", "groups")
		if cfg.IDPClaimsConfig.OIDCProvider != "azure" {
			t.Errorf("provider = %q, want rewritten to azure", cfg.IDPClaimsConfig.OIDCProvider)
		}
	})

	t.Run("no top-level values leaves block nil", func(t *testing.T) {
		cfg := &AuthorizationServiceConfig{}
		SeedIDPClaimsConfigDefaults(cfg, "", "")
		if cfg.IDPClaimsConfig != nil {
			t.Errorf("IDPClaimsConfig = %+v, want nil", cfg.IDPClaimsConfig)
		}
	})
}

func TestParseConfig_ClaimKeyBackfill(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {Issuer: "i", ClientID: "c", ClientSecret: "s"},
	}
	base := func(nestedClaimKey string) map[string]any {
		idp := map[string]any{"oidc_provider": "azure"}
		if nestedClaimKey != "" {
			idp["claim_key"] = nestedClaimKey
		}
		return map[string]any{
			"external_auth_provider":  "azure",
			"external_auth_claim_key": "groups",
			"session_secret_key":      "k",
			"authorization_service": map[string]any{
				"type":                  "default_rbac",
				"user_to_role_provider": "idp_claims",
				"idp_claims_config":     idp,
			},
		}
	}

	t.Run("nested omitted defaults from top-level", func(t *testing.T) {
		cfg, err := ParseConfig(base(""), catalog)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := cfg.AuthorizationService.IDPClaimsConfig.ClaimKey; got != "groups" {
			t.Errorf("claim_key = %q, want defaulted to groups", got)
		}
	})

	t.Run("explicit nested claim_key wins", func(t *testing.T) {
		cfg, err := ParseConfig(base("roles"), catalog)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := cfg.AuthorizationService.IDPClaimsConfig.ClaimKey; got != "roles" {
			t.Errorf("claim_key = %q, want explicit roles", got)
		}
	})
}

func TestParseConfig_IdpClaimsProviderSentinelRewrite(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {Issuer: "i", ClientID: "c", ClientSecret: "s"},
	}
	for _, stale := range []string{"enterprise", "generic"} {
		t.Run(stale, func(t *testing.T) {
			app := map[string]any{
				"external_auth_provider": "azure",
				"session_secret_key":     "k",
				"authorization_service": map[string]any{
					"type":                  "default_rbac",
					"user_to_role_provider": "idp_claims",
					"idp_claims_config": map[string]any{
						"oidc_provider": stale,
						"claim_key":     "groups",
					},
				},
			}
			cfg, err := ParseConfig(app, catalog)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := cfg.AuthorizationService.IDPClaimsConfig.OIDCProvider; got != "azure" {
				t.Errorf("stale %q should rewrite to 'azure'; got %q", stale, got)
			}
		})
	}
}

func TestParseConfig_RedirectURI_CatalogOnly_MirroredToCallbackURI(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://azure/",
			ClientID:     "az",
			ClientSecret: "s",
			RedirectURI:  "https://gw.example.com/api/v1/auth/callback",
		},
	}
	app := map[string]any{
		"external_auth_provider": "azure",
		"session_secret_key":     "k",
	}
	cfg, err := ParseConfig(app, catalog)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.ExternalAuthCallbackURI != "https://gw.example.com/api/v1/auth/callback" {
		t.Errorf("ExternalAuthCallbackURI=%q want mirrored from catalog redirect_uri", cfg.ExternalAuthCallbackURI)
	}
	if cfg.OIDC.RedirectURI != "https://gw.example.com/api/v1/auth/callback" {
		t.Errorf("OIDC.RedirectURI=%q want preserved from catalog", cfg.OIDC.RedirectURI)
	}
}

func TestParseConfig_RedirectURI_DerivedFromFrontendServerURL(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://azure/",
			ClientID:     "az",
			ClientSecret: "s",
		},
	}
	app := map[string]any{
		"external_auth_provider": "azure",
		"session_secret_key":     "k",
		"frontend_server_url":    "https://gw.example.com",
	}
	cfg, err := ParseConfig(app, catalog)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	want := "https://gw.example.com/api/v1/auth/callback"
	if cfg.OIDC.RedirectURI != want {
		t.Errorf("OIDC.RedirectURI=%q want %q", cfg.OIDC.RedirectURI, want)
	}
	if cfg.ExternalAuthCallbackURI != want {
		t.Errorf("ExternalAuthCallbackURI=%q want %q (mirrored)", cfg.ExternalAuthCallbackURI, want)
	}
}

func TestParseConfig_EnterpriseSentinel_SingleEntryAutoSelect(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {Issuer: "https://a/", ClientID: "c", ClientSecret: "s"},
	}
	app := map[string]any{
		"external_auth_provider": "enterprise",
		"session_secret_key":     "k",
	}
	cfg, err := ParseConfig(app, catalog)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.OIDC == nil {
		t.Fatalf("cfg.OIDC should be populated via single-entry auto-select despite 'enterprise' sentinel")
	}
	if cfg.ExternalAuthProvider != "azure" {
		t.Errorf("cfg.ExternalAuthProvider=%q want auto-selected 'azure'", cfg.ExternalAuthProvider)
	}
}

func TestParseConfig_EnterpriseSentinel_MultiEntry_ActionableError(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"a": {Issuer: "https://a/", ClientID: "ca", ClientSecret: "sa"},
		"b": {Issuer: "https://b/", ClientID: "cb", ClientSecret: "sb"},
	}
	app := map[string]any{
		"external_auth_provider": "enterprise",
		"session_secret_key":     "k",
	}
	_, err := ParseConfig(app, catalog)
	if err == nil {
		t.Fatalf("want error for multi-entry catalog with sentinel 'enterprise'")
	}
	if strings.Contains(err.Error(), `"enterprise"`) {
		t.Errorf("error must not mention internal sentinel 'enterprise'; got: %v", err)
	}
	if !strings.Contains(err.Error(), "is required") {
		t.Errorf("want 'is required' variant of error (cleaner for injected sentinel); got: %v", err)
	}
}

func TestParseConfig_RedirectURI_ExplicitCallbackWinsOverCatalog(t *testing.T) {
	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://azure/",
			ClientID:     "az",
			ClientSecret: "s",
			RedirectURI:  "https://catalog-default/cb",
		},
	}
	app := map[string]any{
		"external_auth_provider":     "azure",
		"external_auth_callback_uri": "https://per-app-override/cb",
		"session_secret_key":         "k",
	}
	cfg, err := ParseConfig(app, catalog)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.OIDC.RedirectURI != "https://per-app-override/cb" {
		t.Errorf("OIDC.RedirectURI=%q want per-app override", cfg.OIDC.RedirectURI)
	}
	if cfg.ExternalAuthCallbackURI != "https://per-app-override/cb" {
		t.Errorf("ExternalAuthCallbackURI=%q want per-app override preserved", cfg.ExternalAuthCallbackURI)
	}
}

func TestParseAuthorizationServiceConfig_DefaultRolesCSV(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want []string
	}{
		{"yaml list", []any{"sam_user", "viewer"}, []string{"sam_user", "viewer"}},
		{"csv scalar", "sam_user,admin", []string{"sam_user", "admin"}},
		{"csv with spaces", " sam_user , admin ", []string{"sam_user", "admin"}},
		{"single scalar", "sam_user", []string{"sam_user"}},
		{"trailing comma", "sam_user,", []string{"sam_user"}},
		{"leading and internal blanks", ",sam_user,,admin,", []string{"sam_user", "admin"}},
		{"empty default", "", nil},
		{"whitespace only", "   ", nil},
		{"commas only", ",,", nil},
		{"wrong type ignored", 42, nil},
		{"absent", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]any{}
			if tc.raw != nil {
				m["default_roles"] = tc.raw
			}
			var cfg AuthorizationServiceConfig
			ParseAuthorizationServiceConfig(m, &cfg)
			if len(cfg.DefaultRoles) != len(tc.want) {
				t.Fatalf("DefaultRoles = %v, want %v", cfg.DefaultRoles, tc.want)
			}
			for i := range tc.want {
				if cfg.DefaultRoles[i] != tc.want[i] {
					t.Errorf("DefaultRoles[%d] = %q, want %q", i, cfg.DefaultRoles[i], tc.want[i])
				}
			}
		})
	}
}
