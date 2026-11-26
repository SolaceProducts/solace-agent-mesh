// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const waDeniedWireName = "agent_019fe224_dd07_764b_ae4c_125a04fb3ab2"

func TestWhatsAppDenial_TargetedAgentIsIndistinguishableFromAnUnknownOne(t *testing.T) {
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Helper")

	card := a2a.AgentCard{Name: waDeniedWireName}
	card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.AgentExtension{
		URI:    a2a.ExtensionURIDisplayName,
		Params: map[string]any{"display_name": "Orchestrator"},
	})
	a.core.AgentRegistry().AddOrUpdate(card)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Helper"})
	a.core.SetScopeResolver(waScopeResolver{scopes: []string{rbac.InvokeScopeForInstance("SomethingElse")}})

	send := func(wamid, text string) (reply string, deliveries int) {
		t.Helper()
		before := len(mock.calls())
		value, msg := textMessage(wamid, "15551230000", text)
		deliveries = driveToSettlement(t, a, value, msg)
		calls := mock.calls()
		if len(calls) <= before {
			t.Fatalf("no reply sent for %q", text)
		}
		return calls[len(calls)-1].Text.Body, deliveries
	}

	denied, deniedDeliveries := send("wamid.denied", "@Orchestrator do a thing")
	unknown, unknownDeliveries := send("wamid.unknown", "@Orchestrator2 do a thing")

	if denied != unknown {
		t.Errorf("a denied agent and a nonexistent one must be indistinguishable:\n denied:  %q\n unknown: %q", denied, unknown)
	}
	if deniedDeliveries != unknownDeliveries {
		t.Errorf("the two refusals must also take the same number of deliveries, or the pair is distinguishable by timing: denied=%d unknown=%d", deniedDeliveries, unknownDeliveries)
	}
	if strings.Contains(denied, waDeniedWireName) {
		t.Errorf("reply leaks the agent's broker wire name: %q", denied)
	}
	if strings.Contains(strings.ToLower(denied), "sign in") {
		t.Errorf("a WhatsApp identity keys on a verified phone claim, so a WebUI sign-in does not link it: %q", denied)
	}
	if denied == entrypoint.SubmitFailedText {
		t.Errorf("a permission refusal must not reuse the retryable failure copy: %q", denied)
	}
	if !strings.Contains(denied, "don't have access") {
		t.Errorf("refusal should name the permission boundary, got %q", denied)
	}
}
