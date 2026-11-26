// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestPlatformDiscoveryFanOutAcrossPods(t *testing.T) {
	t.Parallel()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	const namespace = "test-ns"
	logger := slog.Default()

	agentsA := NewAgentRegistry()
	gatewaysA := NewGatewayRegistry()
	dmA := newDiscoveryManager(agentsA, gatewaysA, brk, namespace, 3600, 7200, logger)

	agentsB := NewAgentRegistry()
	gatewaysB := NewGatewayRegistry()
	dmB := newDiscoveryManager(agentsB, gatewaysB, brk, namespace, 3600, 7200, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := dmA.Start(ctx); err != nil {
		t.Fatalf("start dmA: %v", err)
	}
	t.Cleanup(func() { _ = dmA.Stop(ctx) })

	if err := dmB.Start(ctx); err != nil {
		t.Fatalf("start dmB: %v", err)
	}
	t.Cleanup(func() { _ = dmB.Stop(ctx) })

	card := a2a.AgentCard{
		Name:            "agent-fanout",
		Description:     "test agent for fan-out",
		ProtocolVersion: "0.3.0",
	}
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	topic := a2a.DiscoveryAgentCardsTopic(namespace)
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: data}); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(5 * time.Second)
	for {
		gotA := agentsA.Get(card.Name) != nil
		gotB := agentsB.Get(card.Name) != nil
		if gotA && gotB {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("discovery card fan-out failed: dmA received=%v, dmB received=%v "+
				"(both must be true for fan-out across platform replicas)",
				gotA, gotB)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestHandleDiscovery_ReplicaStopKeepsComponent(t *testing.T) {
	agents := NewAgentRegistry()
	gateways := NewGatewayRegistry()
	dm := newDiscoveryManager(agents, gateways, nil, "test-ns", 3600, 7200, slog.Default())

	cases := []struct {
		kind string
		card a2a.AgentCard
		get  func(string) *a2a.AgentCard
	}{
		{"agent", a2a.AgentCard{Name: "agent-r"}, agents.Get},
		{"gateway", a2a.AgentCard{
			Name:         "gw-r",
			Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{URI: ExtensionURIGatewayRole}}},
		}, gateways.Get},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			data, err := json.Marshal(tc.card)
			if err != nil {
				t.Fatal(err)
			}
			deliver := func(props map[string]string) {
				dm.handleDiscovery(&broker.Message{Payload: data, UserProperties: props})
			}

			deliver(map[string]string{"instance_id": "B"})
			deliver(map[string]string{"instance_id": "A"})
			deliver(map[string]string{"instance_id": "A", "removed": "true"})
			if tc.get(tc.card.Name) == nil {
				t.Fatalf("replica A stopping removed %s although replica B still publishes it", tc.card.Name)
			}

			deliver(map[string]string{"instance_id": "B", "removed": "true"})
			if tc.get(tc.card.Name) != nil {
				t.Fatalf("%s should be gone after its last replica's removal", tc.card.Name)
			}
		})
	}
}
