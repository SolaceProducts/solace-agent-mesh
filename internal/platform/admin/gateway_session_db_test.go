// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestGatewayBuilders_SessionDBUsesGatewayNotAgentStore(t *testing.T) {
	const agentDB = "postgres://u:pw@host:5432/rc_orchestrator"
	const gwDB = "postgres://u:pw@host:5432/rc_webui"
	t.Setenv("DATABASE_URL", agentDB)
	t.Setenv("WEB_UI_GATEWAY_DATABASE_URL", gwDB)

	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{ID: "gw-abc-123", Name: "test", Description: strPtr("test gateway fixture for session-db routing")}

	builders := map[string]func() map[string]any{
		"slack":    func() map[string]any { return svc.buildSlackGatewayConfig(gw, map[string]any{}) },
		"teams":    func() map[string]any { return svc.buildTeamsGatewayConfig(gw, map[string]any{}) },
		"email":    func() map[string]any { return svc.buildEmailGatewayConfig(gw, map[string]any{}) },
		"http_sse": func() map[string]any { return svc.buildHTTPSSEGatewayConfig(gw, map[string]any{}) },
		"event_mesh": func() map[string]any {
			return svc.buildEventMeshGatewayConfig(gw, map[string]any{"broker_connection_type": "default", "event_rules": []any{}})
		},
		"mcp": func() map[string]any { return svc.buildMCPGatewayConfig(gw, map[string]any{}) },
	}

	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			apps, _ := build()["apps"].([]map[string]any)
			if len(apps) == 0 {
				t.Fatalf("%s: config has no apps", name)
			}
			appConfig, _ := apps[0]["app_config"].(map[string]any)
			ss, _ := appConfig["session_service"].(map[string]any)
			got, _ := ss["database_url"].(string)
			if got != gwDB {
				t.Errorf("%s session_service.database_url = %q; want the gateway DB %q. It MUST NOT resolve the shared agent DATABASE_URL %q (gateway store schema differs and its migration would fail).", name, got, gwDB, agentDB)
			}
		})
	}
}
