// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func appConfigFromBuilt(t *testing.T, built map[string]any) map[string]any {
	t.Helper()
	apps, ok := built["apps"].([]map[string]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("apps missing or wrong shape: %T", built["apps"])
	}
	ac, ok := apps[0]["app_config"].(map[string]any)
	if !ok {
		t.Fatal("app_config missing")
	}
	return ac
}

func TestBuildGatewayConfig_InheritsClusterAuth(t *testing.T) {
	allTypes := []string{"httpsse", "teams", "slack", "email", "whatsapp", "event_mesh", "mcp", "google_chat"}

	t.Run("cluster RBAC on: every entrypoint type enforces", func(t *testing.T) {
		t.Setenv("FRONTEND_USE_AUTHORIZATION", "true")
		svc := NewYAMLService("test-ns", slog.Default())
		for _, typ := range allTypes {
			gw := &store.Gateway{ID: "gw-" + typ, Name: "gw-" + typ, Type: typ, Values: new(`{}`)}
			built, err := svc.BuildGatewayConfig(gw)
			if err != nil {
				t.Fatalf("%s: BuildGatewayConfig: %v", typ, err)
			}
			ac := appConfigFromBuilt(t, built)
			if ac["frontend_use_authorization"] != true {
				t.Errorf("%s: frontend_use_authorization = %v, want true", typ, ac["frontend_use_authorization"])
			}
			authSvc, ok := ac["authorization_service"].(map[string]any)
			if !ok {
				t.Errorf("%s: authorization_service block missing — entrypoint would run ungated", typ)
				continue
			}
			if authSvc["type"] != "default_rbac" {
				t.Errorf("%s: authorization_service.type = %v, want default_rbac (a non-enforcing type would defeat the point)", typ, authSvc["type"])
			}
		}
	})

	t.Run("explicit enableAuth:false opts out of authn but NOT authz", func(t *testing.T) {
		t.Setenv("FRONTEND_USE_AUTHORIZATION", "true")
		svc := NewYAMLService("test-ns", slog.Default())
		gw := &store.Gateway{ID: "gw-slack", Name: "gw-slack", Type: "slack", Values: new(`{"enableAuth": false}`)}
		built, err := svc.BuildGatewayConfig(gw)
		if err != nil {
			t.Fatalf("BuildGatewayConfig: %v", err)
		}
		ac := appConfigFromBuilt(t, built)
		if _, ok := ac["frontend_use_authorization"]; ok {
			t.Error("enableAuth:false should opt out of authentication (no frontend_use_authorization)")
		}
		if _, ok := ac["external_auth_provider"]; ok {
			t.Error("enableAuth:false should opt out of authentication: external_auth_provider selects the IdP for Bearer validation, which this entrypoint does not do")
		}
		authSvc, ok := ac["authorization_service"].(map[string]any)
		if !ok {
			t.Fatal("enableAuth:false must still stamp authorization_service — without it the scope resolver is unwired and every caller reaches every agent")
		}
		if authSvc["type"] != "default_rbac" {
			t.Errorf("authorization_service.type = %v, want default_rbac", authSvc["type"])
		}
	})

	t.Run("cluster RBAC off: entrypoint stays ungated", func(t *testing.T) {
		t.Setenv("FRONTEND_USE_AUTHORIZATION", "false")
		svc := NewYAMLService("test-ns", slog.Default())
		gw := &store.Gateway{ID: "gw-teams", Name: "gw-teams", Type: "teams", Values: new(`{}`)}
		built, err := svc.BuildGatewayConfig(gw)
		if err != nil {
			t.Fatalf("BuildGatewayConfig: %v", err)
		}
		ac := appConfigFromBuilt(t, built)
		if _, ok := ac["frontend_use_authorization"]; ok {
			t.Error("cluster auth off should not stamp frontend_use_authorization")
		}
		if _, ok := ac["authorization_service"]; ok {
			t.Error("cluster auth off must stay fully ungated: stamping authz here would make a community deployment deny every caller")
		}
	})

	t.Run("explicit enableAuth:true opts in even when the cluster runs no authz", func(t *testing.T) {
		t.Setenv("FRONTEND_USE_AUTHORIZATION", "false")
		svc := NewYAMLService("test-ns", slog.Default())
		gw := &store.Gateway{ID: "gw-mcp", Name: "gw-mcp", Type: "mcp", Values: new(`{"enableAuth": true}`)}
		built, err := svc.BuildGatewayConfig(gw)
		if err != nil {
			t.Fatalf("BuildGatewayConfig: %v", err)
		}
		ac := appConfigFromBuilt(t, built)
		if ac["frontend_use_authorization"] != true {
			t.Errorf("frontend_use_authorization = %v, want true", ac["frontend_use_authorization"])
		}
		if _, ok := ac["authorization_service"].(map[string]any); !ok {
			t.Error("authorization_service block missing for an entrypoint that opted into auth")
		}
	})
}

func TestBoolFromValue(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		wantVal bool
		wantOK  bool
	}{
		{"bool true", true, true, true},
		{"bool false", false, false, true},
		{"string true", "true", true, true},
		{"string false", "false", false, true},
		{"string TRUE case-insensitive", "TRUE", true, true},
		{"string padded false", " false ", false, true},
		{"absent nil", nil, false, false},
		{"empty string", "", false, false},
		{"unrecognized yes", "yes", false, false},
		{"unrecognized 1", "1", false, false},
		{"typo ture", "ture", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok := boolFromValue(tt.in)
			if val != tt.wantVal || ok != tt.wantOK {
				t.Fatalf("boolFromValue(%#v) = (%v, %v), want (%v, %v)", tt.in, val, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}
