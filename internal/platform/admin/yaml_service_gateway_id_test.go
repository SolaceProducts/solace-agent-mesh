// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"maps"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func extractGatewayID(t *testing.T, cfg map[string]any) string {
	t.Helper()
	apps, ok := cfg["apps"].([]map[string]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("apps[] missing or wrong shape: %T", cfg["apps"])
	}
	appCfg, ok := apps[0]["app_config"].(map[string]any)
	if !ok {
		t.Fatalf("app_config missing or wrong shape: %T", apps[0]["app_config"])
	}
	id, _ := appCfg["gateway_id"].(string)
	return id
}

func extractMCPIssuer(t *testing.T, cfg map[string]any) string {
	t.Helper()
	apps, ok := cfg["apps"].([]map[string]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("apps[] missing or wrong shape: %T", cfg["apps"])
	}
	appCfg, ok := apps[0]["app_config"].(map[string]any)
	if !ok {
		t.Fatalf("app_config missing or wrong shape: %T", apps[0]["app_config"])
	}
	adapter, ok := appCfg["gateway_adapter"].(map[string]any)
	if !ok {
		t.Fatalf("gateway_adapter missing or wrong shape: %T", appCfg["gateway_adapter"])
	}
	adapterCfg, ok := adapter["adapter_config"].(map[string]any)
	if !ok {
		t.Fatalf("adapter_config missing or wrong shape: %T", adapter["adapter_config"])
	}
	issuer, _ := adapterCfg["issuer"].(string)
	return issuer
}

func TestYAMLService_EmitsSlugAsGatewayID(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("SOLACE_BROKER_URL", "tcps://broker.example.com:55443")

	svc := NewYAMLService("test-ns", slog.Default())

	const (
		gwID   = "11111111-1111-1111-1111-111111111111"
		gwSlug = "operator-slug"
	)

	cases := []struct {
		gwType string
		values string
	}{
		{
			gwType: "httpsse",
			values: `{}`,
		},
		{
			gwType: "teams",
			values: `{"microsoft_app_id":"app-x","microsoft_app_password":"secret"}`,
		},
		{
			gwType: "slack",
			values: `{"bot_token":"xoxb-x","app_token":"xapp-x"}`,
		},
		{
			gwType: "email",
			values: `{"mailbox":"inbox@example.com","imap_host":"imap.example.com","auth_mode":"basic","imap_password":"pw"}`,
		},
		{
			gwType: "event_mesh",
			values: `{"broker_connection_type":"default","event_rules":[]}`,
		},
		{
			gwType: "mcp",
			values: `{"enableAuth":"false","defaultUserIdentity":"local"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.gwType, func(t *testing.T) {
			values := tc.values
			gw := &store.Gateway{
				ID:          gwID,
				Slug:        gwSlug,
				Name:        "Test Gateway",
				Description: strPtr("slug-emission fixture"),
				Type:        tc.gwType,
				Values:      &values,
			}
			cfg, err := svc.BuildGatewayConfig(gw)
			if err != nil {
				t.Fatalf("BuildGatewayConfig(%s): %v", tc.gwType, err)
			}
			got := extractGatewayID(t, cfg)
			if got != gwSlug {
				t.Errorf("gateway_id = %q, want %q (proof: emitted value must be the slug, NOT gw.ID=%q)",
					got, gwSlug, gwID)
			}
			if got == gwID {
				t.Errorf("gateway_id = gw.ID (%q); C5 was silently reverted — emitter must use gw.Slug", got)
			}
		})
	}
}

func TestYAMLService_MCPIssuerUsesSlug(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://api.example.com")
	t.Setenv("EXTERNAL_AUTH_PROVIDER", "azure")

	svc := NewYAMLService("test-ns", slog.Default())

	const (
		gwID     = "22222222-2222-2222-2222-222222222222"
		gwSlug   = "my-test-slug"
		wantIss  = "https://api.example.com/gw/my-test-slug"
		uuidIss  = "https://api.example.com/gw/22222222-2222-2222-2222-222222222222"
		emptyIss = "https://api.example.com/gw/"
	)

	values := `{"enableAuth":"true","allowedRedirectUris":[{"uri":"https://claude.ai/api/mcp/auth_callback"}]}`
	gw := &store.Gateway{
		ID:          gwID,
		Slug:        gwSlug,
		Name:        "MCP GW",
		Description: strPtr("issuer-slug fixture"),
		Type:        "mcp",
		Values:      &values,
	}

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}

	gotIss := extractMCPIssuer(t, cfg)
	if gotIss != wantIss {
		t.Errorf("issuer = %q, want %q", gotIss, wantIss)
	}
	if gotIss == uuidIss {
		t.Errorf("issuer composed from gw.ID (UUID); C5 was silently reverted — must use gw.Slug")
	}
	if gotIss == emptyIss {
		t.Errorf("issuer suffix is empty; the slug must be the URL-bound segment")
	}

	if got := extractGatewayID(t, cfg); got != gwSlug {
		t.Errorf("gateway_id = %q, want %q", got, gwSlug)
	}
}

func TestYAMLService_EmptySlugFallsBackToID(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("SOLACE_BROKER_URL", "tcps://broker.example.com:55443")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://api.example.com")
	t.Setenv("EXTERNAL_AUTH_PROVIDER", "azure")

	svc := NewYAMLService("test-ns", slog.Default())

	const gwID = "33333333-3333-3333-3333-333333333333"

	t.Run("gateway_id falls back to id", func(t *testing.T) {
		values := `{}`
		gw := &store.Gateway{
			ID:          gwID,
			Slug:        "",
			Name:        "No Slug Gateway",
			Description: strPtr("empty-slug fixture"),
			Type:        "httpsse",
			Values:      &values,
		}
		cfg, err := svc.BuildGatewayConfig(gw)
		if err != nil {
			t.Fatalf("BuildGatewayConfig: %v", err)
		}
		if got := extractGatewayID(t, cfg); got != gwID {
			t.Errorf("gateway_id = %q, want %q (empty slug must fall back to gw.ID)", got, gwID)
		}
	})

	t.Run("mcp issuer falls back to id", func(t *testing.T) {
		values := `{"enableAuth":"true","allowedRedirectUris":[{"uri":"https://claude.ai/api/mcp/auth_callback"}]}`
		gw := &store.Gateway{
			ID:          gwID,
			Slug:        "",
			Name:        "No Slug MCP",
			Description: strPtr("empty-slug issuer fixture"),
			Type:        "mcp",
			Values:      &values,
		}
		cfg, err := svc.BuildGatewayConfig(gw)
		if err != nil {
			t.Fatalf("BuildGatewayConfig: %v", err)
		}
		want := "https://api.example.com/gw/" + gwID
		if got := extractMCPIssuer(t, cfg); got != want {
			t.Errorf("issuer = %q, want %q (empty slug must fall back to gw.ID, never /gw/)", got, want)
		}
	})
}

func extractMCPAdapterConfig(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	apps, ok := cfg["apps"].([]map[string]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("apps[] missing or wrong shape: %T", cfg["apps"])
	}
	appCfg, ok := apps[0]["app_config"].(map[string]any)
	if !ok {
		t.Fatalf("app_config missing or wrong shape: %T", apps[0]["app_config"])
	}
	adapter, ok := appCfg["gateway_adapter"].(map[string]any)
	if !ok {
		t.Fatalf("gateway_adapter missing or wrong shape: %T", appCfg["gateway_adapter"])
	}
	ac, ok := adapter["adapter_config"].(map[string]any)
	if !ok {
		t.Fatalf("adapter_config missing or wrong shape: %T", adapter["adapter_config"])
	}
	return ac
}

func TestYAMLService_MCPStateRedirectionFlowsThrough(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://api.example.com")
	t.Setenv("EXTERNAL_AUTH_PROVIDER", "keycloak")

	svc := NewYAMLService("test-ns", slog.Default())

	values := `{"enableAuth":"true","stateRedirectionEnabled":true,"oauthDispatcherUrl":"https://api.example.com/api/v1/auth/callback","allowedRedirectUris":[]}`
	gw := &store.Gateway{
		ID:          "33333333-3333-3333-3333-333333333333",
		Slug:        "mcp-state-redir",
		Name:        "MCP GW (state redirection)",
		Description: strPtr("state-redirection fixture"),
		Type:        "mcp",
		Values:      &values,
	}

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}

	ac := extractMCPAdapterConfig(t, cfg)
	if got, ok := ac["state_redirection_enabled"].(bool); !ok || !got {
		t.Errorf("state_redirection_enabled = %v (type %T), want true", ac["state_redirection_enabled"], ac["state_redirection_enabled"])
	}
	if got, _ := ac["oauth_dispatcher_url"].(string); got != "https://api.example.com/api/v1/auth/callback" {
		t.Errorf("oauth_dispatcher_url = %q, want the dispatcher URL", got)
	}
}

func TestYAMLService_MCPStateRedirectionDefaultsOnWhenDerivable(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://api.example.com")
	t.Setenv("EXTERNAL_AUTH_PROVIDER", "keycloak")

	svc := NewYAMLService("test-ns", slog.Default())

	values := `{"enableAuth":"true","allowedRedirectUris":[]}`
	gw := &store.Gateway{
		ID:          "44444444-4444-4444-4444-444444444444",
		Slug:        "mcp-default-on",
		Name:        "MCP GW (default on)",
		Description: strPtr("default-on-when-derivable fixture"),
		Type:        "mcp",
		Values:      &values,
	}

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}

	ac := extractMCPAdapterConfig(t, cfg)
	if got, ok := ac["state_redirection_enabled"].(bool); !ok || !got {
		t.Errorf("state_redirection_enabled = %v (type %T), want true (auto-on when derivable)", ac["state_redirection_enabled"], ac["state_redirection_enabled"])
	}
	if got, _ := ac["oauth_dispatcher_url"].(string); got != "https://api.example.com/api/v1/auth/callback" {
		t.Errorf("oauth_dispatcher_url = %q, want the derived WebUI callback", got)
	}
}

func TestYAMLService_MCPStateRedirectionOmittedWithoutBase(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	t.Setenv("EXTERNAL_AUTH_PROVIDER", "keycloak")

	svc := NewYAMLService("test-ns", slog.Default())

	values := `{"enableAuth":"true","allowedRedirectUris":[]}`
	gw := &store.Gateway{
		ID:          "55555555-5555-5555-5555-555555555555",
		Slug:        "mcp-no-base",
		Name:        "MCP GW (no base)",
		Description: strPtr("no-derivable-dispatcher fixture"),
		Type:        "mcp",
		Values:      &values,
	}

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}

	ac := extractMCPAdapterConfig(t, cfg)
	if _, ok := ac["state_redirection_enabled"]; ok {
		t.Errorf("state_redirection_enabled should be absent with no derivable dispatcher; got %v", ac["state_redirection_enabled"])
	}
	if _, ok := ac["oauth_dispatcher_url"]; ok {
		t.Errorf("oauth_dispatcher_url should be absent with no derivable dispatcher; got %v", ac["oauth_dispatcher_url"])
	}
}

func TestYAMLService_MCPStateRedirectionExplicitOptOut(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://api.example.com")
	t.Setenv("EXTERNAL_AUTH_PROVIDER", "keycloak")

	svc := NewYAMLService("test-ns", slog.Default())

	values := `{"enableAuth":"true","stateRedirectionEnabled":"false","allowedRedirectUris":[]}`
	gw := &store.Gateway{
		ID:          "66666666-6666-6666-6666-666666666666",
		Slug:        "mcp-opt-out",
		Name:        "MCP GW (opt out)",
		Description: strPtr("explicit-opt-out fixture"),
		Type:        "mcp",
		Values:      &values,
	}

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}

	ac := extractMCPAdapterConfig(t, cfg)
	if _, ok := ac["state_redirection_enabled"]; ok {
		t.Errorf("state_redirection_enabled should be absent when operator opted out; got %v", ac["state_redirection_enabled"])
	}
	if _, ok := ac["oauth_dispatcher_url"]; ok {
		t.Errorf("oauth_dispatcher_url should be absent when operator opted out; got %v", ac["oauth_dispatcher_url"])
	}
}

func TestYAMLService_MCPStateRedirectionExplicitNoDispatcherEmitsNothing(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	t.Setenv("EXTERNAL_AUTH_PROVIDER", "keycloak")

	svc := NewYAMLService("test-ns", slog.Default())

	values := `{"enableAuth":"true","stateRedirectionEnabled":"true","allowedRedirectUris":[]}`
	gw := &store.Gateway{
		ID:          "77777777-7777-7777-7777-777777777777",
		Slug:        "mcp-explicit-no-base",
		Name:        "MCP GW (explicit, no base)",
		Description: strPtr("explicit-flag no-dispatcher safety-net fixture"),
		Type:        "mcp",
		Values:      &values,
	}

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}

	ac := extractMCPAdapterConfig(t, cfg)
	if _, ok := ac["state_redirection_enabled"]; ok {
		t.Errorf("state_redirection_enabled must be absent when no dispatcher URL is derivable; got %v", ac["state_redirection_enabled"])
	}
	if _, ok := ac["oauth_dispatcher_url"]; ok {
		t.Errorf("oauth_dispatcher_url must be absent when no dispatcher URL is derivable; got %v", ac["oauth_dispatcher_url"])
	}
}

func TestYAMLService_EmitsTheGatewayRowIDAsGatewayUUID(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	svc := NewYAMLService("test-ns", slog.Default())
	const gwID = "33333333-3333-4333-8333-333333333333"

	for _, gwType := range []string{"httpsse", "teams", "slack", "email", "whatsapp", "event_mesh", "mcp", "webhook", "google_chat"} {
		t.Run(gwType, func(t *testing.T) {
			values := `{}`
			cfg, err := svc.BuildGatewayConfig(&store.Gateway{ID: gwID, Slug: "operator-slug", Name: "GW", Type: gwType, Values: &values})
			if err != nil {
				t.Fatalf("BuildGatewayConfig(%s): %v", gwType, err)
			}
			apps := cfg["apps"].([]map[string]any)
			if got := apps[0]["app_config"].(map[string]any)["gateway_uuid"]; got != gwID {
				t.Errorf("gateway_uuid = %v, want the gateways.id %s", got, gwID)
			}
		})
	}
}

func TestYAMLService_RendersTheRunAsSystemUserRowOnlyWhenItExists(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	users := store.NewUsersRepo(testOpenDB(t))
	qaBot := &store.User{Issuer: rbac.SystemUserIssuer, Sub: "system:qa-bot", ProvisionedVia: rbac.ProvisionedViaSystem, CreatedBy: "test", UpdatedBy: "test"}
	if err := users.UpsertUser(t.Context(), qaBot, nil); err != nil {
		t.Fatalf("seed system user: %v", err)
	}
	qaBotID, err := users.SystemUserID(t.Context(), rbac.SystemUserIssuer, "system:qa-bot")
	if err != nil || qaBotID == "" {
		t.Fatalf("read seeded system user id = %q, %v", qaBotID, err)
	}
	svc := NewYAMLService("test-ns", slog.Default())
	svc.SetSystemUserLookup(users)

	for _, tc := range []struct {
		name, gwType, values string
		want                 map[string]any
	}{
		{"a slack run_as", "slack", `{"run_as":"qa-bot"}`, map[string]any{"system:qa-bot": qaBotID}},
		{"a webhook route run_as", "webhook", `{"runAs":"system:qa-bot","targetAgentName":"A"}`, map[string]any{"system:qa-bot": qaBotID}},
		{"an event mesh rule run_as", "event_mesh", `{"event_rules":[{"name":"r","runAs":"qa-bot"}]}`, map[string]any{"system:qa-bot": qaBotID}},
		{"a run_as no system user holds", "mcp", `{"runAs":"ghost"}`, nil},
		{"a baked-in system user", "teams", `{"run_as":"default"}`, nil},
		{"no run_as", "httpsse", `{}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := svc.BuildGatewayConfig(&store.Gateway{ID: "33333333-3333-4333-8333-333333333333", Slug: "gw", Name: "GW", Type: tc.gwType, Values: &tc.values})
			if err != nil {
				t.Fatalf("BuildGatewayConfig(%s): %v", tc.gwType, err)
			}
			got, _ := cfg["apps"].([]map[string]any)[0]["app_config"].(map[string]any)["run_as_user_uuids"].(map[string]any)
			if !maps.Equal(got, tc.want) {
				t.Errorf("run_as_user_uuids = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunAsPrincipals_NamesEachSystemUserOnce(t *testing.T) {
	appConfig := map[string]any{
		"run_as": " qa-bot ",
		"event_rules": []any{
			map[string]any{"name": "a", "run_as": "qa-bot"},
			map[string]any{"name": "b", "run_as": "ops-bot"},
		},
		"routes": []map[string]any{{"run_as": "ops-bot"}, {"run_as": "  "}},
	}
	got := runAsPrincipals(appConfig)
	want := []string{"system:ops-bot", "system:qa-bot"}
	if !slices.Equal(got, want) {
		t.Errorf("runAsPrincipals = %v, want %v", got, want)
	}
}
