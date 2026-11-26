// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/webhook"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"gopkg.in/yaml.v3"
)

func webhookYAMLRoundTrip(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	b, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("yaml marshal: %v", err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	return out
}

func webhookAdapterConfig(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	rt := webhookYAMLRoundTrip(t, cfg)
	apps, ok := rt["apps"].([]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("apps[] missing or wrong shape: %T", rt["apps"])
	}
	appCfg, ok := apps[0].(map[string]any)["app_config"].(map[string]any)
	if !ok {
		t.Fatalf("app_config missing or wrong shape")
	}
	adapter, ok := appCfg["gateway_adapter"].(map[string]any)
	if !ok {
		t.Fatalf("gateway_adapter missing or wrong shape")
	}
	adapterCfg, ok := adapter["adapter_config"].(map[string]any)
	if !ok {
		t.Fatalf("adapter_config missing or wrong shape")
	}
	return adapterCfg
}

func webhookGateway(t *testing.T, valuesJSON string) *store.Gateway {
	t.Helper()
	desc := "declarative webhook fixture"
	return &store.Gateway{
		ID:          "019f8acf-ba30-703a-92ef-a443c16f9ac3",
		Slug:        "webhook-decl-test",
		Name:        "Hooks",
		Description: &desc,
		Type:        "webhook",
		Values:      &valuesJSON,
	}
}

func TestBuildWebhookGatewayConfig_TokenRoundTrips(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	gw := webhookGateway(t, `{
		"routePath":"/decl-test","method":"POST","authMode":"token",
		"authToken":"dev-webhook-decl-token","targetAgentName":"Orchestrator",
		"inputExpression":"input.payload:message"
	}`)

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}
	ac := webhookAdapterConfig(t, cfg)
	if _, err := webhook.ParseConfig(ac); err != nil {
		t.Fatalf("runtime ParseConfig rejected the emitted token config: %v", err)
	}

	routes, ok := ac["routes"].([]any)
	if !ok || len(routes) == 0 {
		t.Fatalf("routes[] missing or wrong shape")
	}
	route, ok := routes[0].(map[string]any)
	if !ok {
		t.Fatalf("route[0] wrong shape")
	}
	if route["target_agent_name"] != "Orchestrator" {
		t.Errorf("target_agent_name = %v, want Orchestrator", route["target_agent_name"])
	}
	matchMap, ok := route["match"].(map[string]any)
	if !ok {
		t.Fatalf("match block missing or wrong shape: %v", route["match"])
	}
	if matchMap["path"] != "/decl-test" {
		t.Errorf("match.path = %v, want /decl-test", matchMap["path"])
	}
	if matchMap["method"] != "POST" {
		t.Errorf("match.method = %v, want POST", matchMap["method"])
	}
	authMap, ok := route["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth block missing or wrong shape: %v", route["auth"])
	}
	tokenBlock, ok := authMap["token"].(map[string]any)
	if !ok {
		t.Fatalf("expected a token auth block, got %v", route["auth"])
	}
	if tokenBlock["value"] != "dev-webhook-decl-token" {
		t.Errorf("token value = %v, want dev-webhook-decl-token", tokenBlock["value"])
	}
}

func TestBuildWebhookGatewayConfig_HMACRoundTrips(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	gw := webhookGateway(t, `{
		"routePath":"/hmac","method":"POST","authMode":"hmac",
		"hmacSecret":"sixteen-char-secret-value","targetWorkflowName":"TestWorkflow",
		"inputExpression":"input.payload:message"
	}`)

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}
	ac := webhookAdapterConfig(t, cfg)
	if _, err := webhook.ParseConfig(ac); err != nil {
		t.Fatalf("runtime ParseConfig rejected the emitted hmac config: %v", err)
	}

	routes, ok := ac["routes"].([]any)
	if !ok || len(routes) == 0 {
		t.Fatalf("routes[] missing or wrong shape")
	}
	route, ok := routes[0].(map[string]any)
	if !ok {
		t.Fatalf("route[0] wrong shape")
	}
	if route["target_workflow_name"] != "TestWorkflow" {
		t.Errorf("target_workflow_name = %v, want TestWorkflow", route["target_workflow_name"])
	}
	if route["target_agent_name"] != nil {
		t.Errorf("workflow route should not carry target_agent_name, got %v", route["target_agent_name"])
	}
	matchMap, ok := route["match"].(map[string]any)
	if !ok {
		t.Fatalf("match block missing or wrong shape: %v", route["match"])
	}
	if matchMap["path"] != "/hmac" {
		t.Errorf("match.path = %v, want /hmac", matchMap["path"])
	}
	if matchMap["method"] != "POST" {
		t.Errorf("match.method = %v, want POST", matchMap["method"])
	}
	authMap, ok := route["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth block missing or wrong shape: %v", route["auth"])
	}
	hmacBlock, ok := authMap["hmac"].(map[string]any)
	if !ok {
		t.Fatalf("expected an hmac auth block, got %v", route["auth"])
	}
	if hmacBlock["secret"] != "sixteen-char-secret-value" {
		t.Errorf("hmac secret = %v, want sixteen-char-secret-value", hmacBlock["secret"])
	}
	if hmacBlock["prefix"] != "sha256=" {
		t.Errorf("hmac prefix = %v, want sha256=", hmacBlock["prefix"])
	}
	if hmacBlock["algo"] != "sha256" {
		t.Errorf("hmac algo = %v, want sha256", hmacBlock["algo"])
	}
	if hmacBlock["encoding"] != "hex" {
		t.Errorf("hmac encoding = %v, want hex", hmacBlock["encoding"])
	}
	if hmacBlock["header"] != "X-Hub-Signature-256" {
		t.Errorf("hmac header = %v, want X-Hub-Signature-256", hmacBlock["header"])
	}
}

func TestBuildWebhookGatewayConfig_HMACEncodingPropagates(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	gw := webhookGateway(t, `{
		"routePath":"/hmac-b64","method":"POST","authMode":"hmac",
		"hmacSecret":"sixteen-char-secret-value","targetAgentName":"Agent",
		"inputExpression":"input.payload:message","hmacEncoding":"base64url"
	}`)

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}
	ac := webhookAdapterConfig(t, cfg)
	if _, err := webhook.ParseConfig(ac); err != nil {
		t.Fatalf("runtime ParseConfig rejected the emitted config: %v", err)
	}

	routes, ok := ac["routes"].([]any)
	if !ok || len(routes) == 0 {
		t.Fatalf("routes[] missing or wrong shape")
	}
	route, ok := routes[0].(map[string]any)
	if !ok {
		t.Fatalf("route[0] wrong shape")
	}
	authMap, ok := route["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth block missing: %v", route["auth"])
	}
	hmacBlock, ok := authMap["hmac"].(map[string]any)
	if !ok {
		t.Fatalf("hmac block missing: %v", route["auth"])
	}
	if hmacBlock["encoding"] != "base64url" {
		t.Errorf("hmac encoding = %v, want base64url", hmacBlock["encoding"])
	}
}

func TestBuildWebhookGatewayConfig_TokenLocationPropagates(t *testing.T) {
	for _, tc := range []struct {
		name         string
		extraValues  string
		wantLocation string
		wantName     any
	}{
		{name: "absent defaults to bearer", wantLocation: "bearer", wantName: nil},
		{
			name:         "header location carries its name",
			extraValues:  `,"authTokenLocation":"header","authTokenName":"X-Webhook-Token"`,
			wantLocation: "header",
			wantName:     "X-Webhook-Token",
		},
		{
			name:         "query location carries its name",
			extraValues:  `,"authTokenLocation":"query","authTokenName":"token"`,
			wantLocation: "query",
			wantName:     "token",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewYAMLService("test-ns", slog.Default())
			gw := webhookGateway(t, `{
				"routePath":"/tok","method":"POST","authMode":"token",
				"authToken":"dev-webhook-token","targetAgentName":"Agent",
				"inputExpression":"input.payload:message"`+tc.extraValues+`
			}`)

			cfg, err := svc.BuildGatewayConfig(gw)
			if err != nil {
				t.Fatalf("BuildGatewayConfig: %v", err)
			}
			ac := webhookAdapterConfig(t, cfg)
			if _, err := webhook.ParseConfig(ac); err != nil {
				t.Fatalf("runtime ParseConfig rejected the emitted config: %v", err)
			}

			routes, ok := ac["routes"].([]any)
			if !ok || len(routes) == 0 {
				t.Fatalf("routes[] missing or wrong shape")
			}
			route, ok := routes[0].(map[string]any)
			if !ok {
				t.Fatalf("route[0] wrong shape")
			}
			authMap, ok := route["auth"].(map[string]any)
			if !ok {
				t.Fatalf("auth block missing: %v", route["auth"])
			}
			tokenBlock, ok := authMap["token"].(map[string]any)
			if !ok {
				t.Fatalf("token block missing: %v", route["auth"])
			}
			if tokenBlock["location"] != tc.wantLocation {
				t.Errorf("token location = %v, want %v", tokenBlock["location"], tc.wantLocation)
			}
			if tokenBlock["name"] != tc.wantName {
				t.Errorf("token name = %v, want %v", tokenBlock["name"], tc.wantName)
			}
		})
	}
}

func TestBuildWebhookGatewayConfig_HMACEmptyPrefixIsPreserved(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	gw := webhookGateway(t, `{
		"routePath":"/hmac-bare","method":"POST","authMode":"hmac",
		"hmacSecret":"sixteen-char-secret-value","targetAgentName":"Agent",
		"inputExpression":"input.payload:message","hmacPrefix":"","hmacEncoding":"base64"
	}`)

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}
	ac := webhookAdapterConfig(t, cfg)
	if _, err := webhook.ParseConfig(ac); err != nil {
		t.Fatalf("runtime ParseConfig rejected the emitted config: %v", err)
	}

	routes, ok := ac["routes"].([]any)
	if !ok || len(routes) == 0 {
		t.Fatalf("routes[] missing or wrong shape")
	}
	route, ok := routes[0].(map[string]any)
	if !ok {
		t.Fatalf("route[0] wrong shape")
	}
	authMap, ok := route["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth block missing: %v", route["auth"])
	}
	hmacBlock, ok := authMap["hmac"].(map[string]any)
	if !ok {
		t.Fatalf("hmac block missing: %v", route["auth"])
	}
	if hmacBlock["prefix"] != "" {
		t.Errorf("hmac prefix = %q, want empty (an explicitly-cleared prefix must not be defaulted)", hmacBlock["prefix"])
	}
}

func TestBuildWebhookGatewayConfig_HMACAbsentPrefixDefaults(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	gw := webhookGateway(t, `{
		"routePath":"/hmac-default","method":"POST","authMode":"hmac",
		"hmacSecret":"sixteen-char-secret-value","targetAgentName":"Agent",
		"inputExpression":"input.payload:message"
	}`)

	cfg, err := svc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}
	ac := webhookAdapterConfig(t, cfg)
	routes, ok := ac["routes"].([]any)
	if !ok || len(routes) == 0 {
		t.Fatalf("routes[] missing or wrong shape")
	}
	route, ok := routes[0].(map[string]any)
	if !ok {
		t.Fatalf("route[0] wrong shape")
	}
	authMap, ok := route["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth block missing: %v", route["auth"])
	}
	hmacBlock, ok := authMap["hmac"].(map[string]any)
	if !ok {
		t.Fatalf("hmac block missing: %v", route["auth"])
	}
	if hmacBlock["prefix"] != "sha256=" {
		t.Errorf("hmac prefix = %v, want sha256=", hmacBlock["prefix"])
	}
}
