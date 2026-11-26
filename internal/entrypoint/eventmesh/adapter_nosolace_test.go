// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosolace

package eventmesh

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestEventMeshAdapter_Init_NoSolaceReturnsError(t *testing.T) {
	adapter := NewEventMeshAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":    "test-ns",
		"gateway_id":   "gw-1",
		"gateway_type": "event_mesh",
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcps://broker.example.com:55443",
			"broker_vpn": "default",
		},
		"event_handlers": []any{
			map[string]any{
				"name":              "h1",
				"subscriptions":     []any{map[string]any{"topic": "in/>"}},
				"input_expression":  "input.payload:msg",
				"target_agent_name": "TestAgent",
			},
		},
	})

	err := core.Init(t.Context(), nil)
	if err == nil {
		t.Fatal("expected Init to return an error on a nosolace build, got nil")
	}
	if !strings.Contains(err.Error(), "nosolace") {
		t.Errorf("expected error to mention nosolace, got: %v", err)
	}
}
