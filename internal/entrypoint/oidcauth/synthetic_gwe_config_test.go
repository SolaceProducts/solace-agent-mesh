// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func loadGweAppConfig(t *testing.T, env map[string]string) map[string]any {
	t.Helper()
	loader := &config.YAMLLoader{Lookup: func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}}
	cfg, err := loader.Load(t.Context(), filepath.Join("..", "..", "..", "configs", "gwe", "gwe.yaml"))
	if err != nil {
		t.Fatalf("loading gwe.yaml: %v", err)
	}
	apps, ok := cfg.GetRaw("apps").([]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("gwe.yaml: expected non-empty apps list, got %T", cfg.GetRaw("apps"))
	}
	app, ok := apps[0].(map[string]any)
	if !ok {
		t.Fatalf("gwe.yaml: apps[0] is %T, expected map", apps[0])
	}
	appConfig, ok := app["app_config"].(map[string]any)
	if !ok {
		t.Fatalf("gwe.yaml: apps[0].app_config is %T, expected map", app["app_config"])
	}
	return appConfig
}

func TestGweConfigSyntheticAuthDisabledByDefault(t *testing.T) {
	appConfig := loadGweAppConfig(t, nil)

	if SyntheticAuthEnabled(appConfig) {
		t.Fatal("synthetic auth must be disabled when SYNTHETIC_AUTH_ENABLED is unset")
	}
	cfg, err := ParseSyntheticAuthConfig(appConfig)
	if err != nil {
		t.Fatalf("disabled synthetic_auth block must parse cleanly: %v", err)
	}
	if cfg != nil {
		t.Fatal("disabled synthetic_auth block must yield a nil config")
	}
}

func TestGweConfigSyntheticAuthEnabledViaEnv(t *testing.T) {
	appConfig := loadGweAppConfig(t, map[string]string{
		"SYNTHETIC_AUTH_ENABLED":         "true",
		"SYNTHETIC_AUTH_TENANT_ID":       "11111111-2222-3333-4444-555555555555",
		"SYNTHETIC_AUTH_AUDIENCE":        "api://sam-gateway",
		"SYNTHETIC_AUTH_APPID_ALLOWLIST": `["aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"]`,
		"SYNTHETIC_AUTH_ROLES":           `["SyntheticMonitor"]`,
	})

	if !SyntheticAuthEnabled(appConfig) {
		t.Fatal("SYNTHETIC_AUTH_ENABLED=true must enable synthetic auth")
	}
	cfg, err := ParseSyntheticAuthConfig(appConfig)
	if err != nil {
		t.Fatalf("enabled synthetic_auth block must parse: %v", err)
	}
	if cfg == nil {
		t.Fatal("enabled synthetic_auth block must yield a non-nil config")
	}
	if cfg.RoleName != "Synthetics.Smoke" {
		t.Errorf("role_name default = %q, want Synthetics.Smoke", cfg.RoleName)
	}
	if _, ok := cfg.AppIDAllowlist["aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"]; !ok {
		t.Errorf("appid_allowlist missing env-substituted GUID: %v", cfg.AppIDAllowlist)
	}
	if len(cfg.PrincipalRoles) != 1 || cfg.PrincipalRoles[0] != "SyntheticMonitor" {
		t.Errorf("roles = %v, want [SyntheticMonitor]", cfg.PrincipalRoles)
	}
	if len(cfg.EndpointRules) != 2 {
		t.Fatalf("endpoint_allowlist rules = %d, want 2", len(cfg.EndpointRules))
	}
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/v1/sessions", true},
		{"POST", "/api/v1/message:stream", true},
		{"POST", "/api/v1/message:send", true},
		{"POST", "/api/v1/sessions", false},
		{"DELETE", "/api/v1/sessions", false},
		{"GET", "/foo/api/v1/sessions", false},
	} {
		if got := cfg.IsEndpointAllowed(tc.method, tc.path); got != tc.want {
			t.Errorf("IsEndpointAllowed(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
