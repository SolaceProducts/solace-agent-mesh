// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "testing"

func TestConfigNeedsSolaceBroker(t *testing.T) {
	eventMesh := func(emBroker map[string]any) map[string]any {
		return map[string]any{
			"apps": []map[string]any{{
				"name": "gw",
				"app_config": map[string]any{
					"gateway_type":             "event_mesh",
					"event_mesh_broker_config": emBroker,
				},
			}},
		}
	}

	tests := []struct {
		name   string
		config map[string]any
		want   bool
	}{
		{"event mesh with real broker url", eventMesh(map[string]any{"broker_url": "tcps://broker.example.com:55443"}), true},
		{"event mesh via []any envelope (round-tripped shape)", map[string]any{
			"apps": []any{
				map[string]any{"app_config": map[string]any{
					"gateway_type":             "event_mesh",
					"event_mesh_broker_config": map[string]any{"broker_url": "tcps://broker.example.com:55443"},
				}},
			},
		}, true},
		{"event mesh in dev mode (no broker url)", eventMesh(map[string]any{"broker_vpn": "default"}), false},
		{"event mesh dev_mode explicit", eventMesh(map[string]any{"dev_mode": true, "broker_url": "tcps://x:55443"}), false},
		{"non event mesh gateway", map[string]any{"apps": []map[string]any{{"app_config": map[string]any{"gateway_type": "http_sse"}}}}, false},
		{"no apps", map[string]any{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := configNeedsSolaceBroker(tt.config); got != tt.want {
				t.Errorf("configNeedsSolaceBroker = %v, want %v", got, tt.want)
			}
		})
	}
}
