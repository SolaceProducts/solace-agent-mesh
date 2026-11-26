// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint_test

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/email"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/slack"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/teams"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/webhook"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestMachineIngressAdapters(t *testing.T) {
	cases := []struct {
		name    string
		adapter entrypoint.GatewayAdapter
		machine bool
	}{
		{"a broker topic rule firing", &eventmesh.EventMeshAdapter{}, true},
		{"an inbound HTTP webhook", &webhook.WebhookAdapter{}, true},
		{"an external MCP client", &mcp.Adapter{}, true},
		{"a person in the web UI", httpsse.NewHTTPSSEAdapter(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ingress, ok := c.adapter.(entrypoint.MachineIngressAdapter)
			if !c.machine {
				if ok && ingress.MachineIngress() {
					t.Fatal("interactive traffic was declared machine ingress")
				}
				return
			}
			if !ok {
				t.Fatal("adapter does not declare entrypoint.MachineIngressAdapter")
			}
			if !ingress.MachineIngress() {
				t.Fatal("adapter declares MachineIngressAdapter but reports false")
			}
		})
	}
}

func TestMachineIngressAdaptersDeclareACatalogType(t *testing.T) {
	cases := []struct {
		adapter        entrypoint.MachineIngressAdapter
		entrypointType string
	}{
		{&eventmesh.EventMeshAdapter{}, "event_mesh"},
		{&webhook.WebhookAdapter{}, "webhook"},
		{&mcp.Adapter{}, "mcp"},
	}
	for _, c := range cases {
		t.Run(c.entrypointType, func(t *testing.T) {
			if got := c.adapter.EntrypointType(); got != c.entrypointType {
				t.Fatalf("EntrypointType = %q, want %q", got, c.entrypointType)
			}
			if !slices.Contains(taxonomy.GatewayTypes, c.entrypointType) {
				t.Errorf("%q is not in taxonomy.GatewayTypes, so entrypoint:used would report no type at all", c.entrypointType)
			}
		})
	}
}

func TestEveryRegisteredAdapterIsClassified(t *testing.T) {
	classified := map[string]bool{
		"webhook":     true,
		"eventmesh":   true,
		"mcp":         true,
		"httpsse":     false,
		"slack":       false,
		"teams":       false,
		"email":       false,
		"whatsapp":    false,
		"google_chat": false,
	}

	registered := entrypoint.RegisteredAdapters()
	if len(registered) != len(classified) {
		t.Fatalf("registered adapters %v, classified %d: classify the new one before it ships", registered, len(classified))
	}
	for _, name := range registered {
		machine, ok := classified[name]
		if !ok {
			t.Errorf("adapter %q is registered but not classified as machine ingress or not", name)
			continue
		}
		adapter, err := entrypoint.CreateAdapter(name, nil)
		if err != nil {
			t.Logf("%s: not constructible with nil config (%v), name classification still pinned", name, err)
			continue
		}
		ingress, declares := adapter.(entrypoint.MachineIngressAdapter)
		got := declares && ingress.MachineIngress()
		if got != machine {
			t.Errorf("%s: machine ingress = %v, want %v", name, got, machine)
		}
	}
}
