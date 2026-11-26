// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestAConfiguredDisplayNameReplacesTheOneTheAgentDescribes(t *testing.T) {
	r := newRig(t, map[string]any{"display_name": "Claims Desk"}, nil)
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	described := describedClaims()
	described["displayName"] = "Orchestrator"
	c.send(map[string]any{"type": typeDescribe, "agent": described})
	var card a2a.AgentCard
	if err := json.Unmarshal(recv(t, cards).Payload, &card); err != nil {
		t.Fatal(err)
	}
	if got := a2a.DisplayNameFromCard(card); got != "Claims Desk" {
		t.Fatalf("the card shows %q", got)
	}
	if got := r.adapter.agent(agentName).displayName(); got != "Claims Desk" {
		t.Fatalf("the link calls the agent %q", got)
	}
}

func TestWithoutAConfiguredDisplayNameTheCardShowsTheAgentsNameNotTheOneItDescribes(t *testing.T) {
	r := newRig(t, nil, nil)
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	described := describedClaims()
	described["displayName"] = "Support"
	c.send(map[string]any{"type": typeDescribe, "agent": described})
	var card a2a.AgentCard
	if err := json.Unmarshal(recv(t, cards).Payload, &card); err != nil {
		t.Fatal(err)
	}
	if got := a2a.DisplayNameFromCard(card); got != agentName {
		t.Fatalf("the card shows %q", got)
	}
}
