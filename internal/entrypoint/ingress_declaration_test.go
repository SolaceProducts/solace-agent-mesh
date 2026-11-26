// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint_test

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/email"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/slack"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/teams"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/webhook"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

const httpEntrypoint = "httpsse"

func TestEveryIngressAdapterDeclaresWhoDrivesIt(t *testing.T) {
	for _, name := range entrypoint.RegisteredAdapters() {
		if name == httpEntrypoint {
			continue
		}
		t.Run(name, func(t *testing.T) {
			adapter, err := entrypoint.CreateAdapter(name, nil)
			if err != nil {
				t.Skipf("not constructible with nil config (%v)", err)
			}
			assertDeclaresWhoDrivesIt(t, adapter)
		})
	}
}

func TestEachIngressAdapterDeclaresItsOwnClient(t *testing.T) {
	cases := []struct {
		name    string
		adapter entrypoint.GatewayAdapter
		relayed string
	}{
		{"slack", &slack.SlackGatewayAdapter{}, httputil.SurfaceSlack},
		{"teams", &teams.TeamsAdapter{}, httputil.SurfaceTeams},
		{"email", &email.EmailAdapter{}, httputil.SurfaceEmail},
		{"whatsapp", &whatsapp.Adapter{}, httputil.SurfaceWhatsApp},
		{"google_chat", &googlechat.Adapter{}, httputil.SurfaceGoogleChat},
		{"webhook", &webhook.WebhookAdapter{}, ""},
		{"eventmesh", &eventmesh.EventMeshAdapter{}, ""},
		{"mcp", &mcp.Adapter{}, ""},
	}

	if registered := entrypoint.RegisteredAdapters(); len(registered) != len(cases)+1 {
		t.Fatalf("registered adapters %v, declarations pinned %d: say whether the new one is machine ingress or which client it relays for", registered, len(cases))
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertDeclaresWhoDrivesIt(t, c.adapter)
			if got := relayedSurfaceOf(c.adapter); got != c.relayed {
				t.Fatalf("RelayedSurface = %q, want %q", got, c.relayed)
			}
		})
	}
}

func assertDeclaresWhoDrivesIt(t *testing.T, adapter entrypoint.GatewayAdapter) {
	t.Helper()
	ingress, isMachine := adapter.(entrypoint.MachineIngressAdapter)
	machine := isMachine && ingress.MachineIngress()
	relayed := relayedSurfaceOf(adapter)

	if machine && relayed != "" {
		t.Fatalf("declares both machine ingress and relay %q: a run is driven by one or the other", relayed)
	}
	if !machine && relayed == "" {
		t.Fatal("declares neither entrypoint.MachineIngressAdapter nor entrypoint.RelayedSurfaceAdapter, so its runs would be counted with no submitter")
	}
	if relayed != "" && !slices.Contains(httputil.RelayedSurfaces, relayed) {
		t.Fatalf("RelayedSurface = %q, outside httputil.RelayedSurfaces, so the run would report no submitter at all", relayed)
	}
}

func relayedSurfaceOf(adapter entrypoint.GatewayAdapter) string {
	relay, ok := adapter.(entrypoint.RelayedSurfaceAdapter)
	if !ok {
		return ""
	}
	return relay.RelayedSurface()
}
