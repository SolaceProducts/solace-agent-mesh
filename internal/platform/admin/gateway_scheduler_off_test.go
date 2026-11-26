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

func TestBuildGatewayConfig_TurnsTheSchedulerOff(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	for _, typ := range []string{"httpsse", "teams", "slack", "email", "whatsapp", "event_mesh", "mcp", "webhook", "google_chat"} {
		built, err := svc.BuildGatewayConfig(&store.Gateway{ID: "gw-" + typ, Name: "gw-" + typ, Type: typ, Values: new(`{}`)})
		if err != nil {
			t.Fatalf("%s: BuildGatewayConfig: %v", typ, err)
		}
		sched, ok := appConfigFromBuilt(t, built)["scheduler_service"].(map[string]any)
		if !ok || sched["enabled"] != false {
			t.Errorf("%s: scheduler_service = %v, want enabled false", typ, sched)
		}
	}
}
