// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

const evictionWindow = 1500 * time.Millisecond

func TestDevModeEventMeshGateways_DoNotEvictEachOther(t *testing.T) {
	server := broker.NewDevBrokerServer(nil, nil)
	addr, err := server.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start dev broker server: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split dev broker addr %q: %v", addr, err)
	}
	t.Setenv("SAM_DEV_BROKER_HOST", host)
	t.Setenv("SAM_DEV_BROKER_PORT", port)

	reconnected := make(chan string, 1)
	for _, gatewayID := range []string{"em-gw-a", "em-gw-b"} {
		adapter := startDevModeAdapter(t, gatewayID)
		dataPlane := unwrapNetworkDevBroker(t, adapter.dataBroker)
		dataPlane.OnReconnect(func() {
			select {
			case reconnected <- gatewayID:
			default:
			}
		})
	}

	select {
	case gatewayID := <-reconnected:
		t.Fatalf("gateway %s was evicted and reconnected; the two gateways share a dev broker client", gatewayID)
	case <-time.After(evictionWindow):
	}
}

func startDevModeAdapter(t *testing.T, gatewayID string) *EventMeshAdapter {
	t.Helper()
	controlPlane := broker.NewDevBroker()
	if err := controlPlane.Connect(t.Context()); err != nil {
		t.Fatalf("control plane connect: %v", err)
	}
	t.Cleanup(func() { _ = controlPlane.Disconnect(context.Background()) })

	adapter := NewEventMeshAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":                "test",
		"gateway_id":               gatewayID,
		"event_mesh_broker_config": map[string]any{"dev_mode": true},
		"event_handlers": []any{
			map[string]any{
				"name":              "h1",
				"subscriptions":     []any{map[string]any{"topic": "in/" + gatewayID + "/>"}},
				"input_expression":  "input.payload:msg",
				"target_agent_name": "TestAgent",
			},
		},
	})
	core.SetServices(&stubServicesAuth{brk: controlPlane})
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init gateway %s: %v", gatewayID, err)
	}
	if err := adapter.Start(t.Context()); err != nil {
		t.Fatalf("start gateway %s: %v", gatewayID, err)
	}
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })
	return adapter
}

func unwrapNetworkDevBroker(t *testing.T, brk broker.Broker) *broker.NetworkDevBroker {
	t.Helper()
	if instrumented, ok := brk.(*observability.InstrumentedBroker); ok {
		brk = instrumented.Broker
	}
	netBroker, ok := brk.(*broker.NetworkDevBroker)
	if !ok {
		t.Fatalf("data plane broker is %T, want *broker.NetworkDevBroker", brk)
	}
	return netBroker
}

func TestDevDataPlaneClientID_UniquePerInstance(t *testing.T) {
	first := devDataPlaneClientID("gw-1")
	second := devDataPlaneClientID("gw-1")
	if first == second {
		t.Fatalf("two adapter instances of one gateway got the same client id %q", first)
	}
	if other := devDataPlaneClientID("gw-2"); other == first {
		t.Fatalf("two gateways got the same client id %q", first)
	}
	if !strings.HasPrefix(first, "emgw-data-gw-1-") {
		t.Fatalf("client id %q does not name its gateway", first)
	}
	if a, b := devDataPlaneClientID(""), devDataPlaneClientID(""); a == b || !strings.HasPrefix(a, "emgw-data-") || strings.HasPrefix(a, "emgw-data--") {
		t.Fatalf("client ids without a gateway id = %q, %q; want two distinct emgw-data-<suffix> ids", a, b)
	}
}
