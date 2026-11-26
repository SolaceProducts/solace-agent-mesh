// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func refusalAdapter(t *testing.T, defaultAgent string) *SlackGatewayAdapter {
	t.Helper()
	a, _, _ := newHandlerTestAdapter(t)
	a.cfg.DefaultAgentName = defaultAgent
	return a
}

func slackMessage() slackInbound {
	return slackInbound{channelID: "C123", threadTS: "1700000000.000001", userID: "U-alice", teamID: "T-team", text: "what is the capital of France?", isDM: true}
}

func TestSlackRefusalsReportTheirReason(t *testing.T) {
	t.Run("a misconfigured default agent is an agent that is not found", func(t *testing.T) {
		a := refusalAdapter(t, "does-not-exist")
		a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
		if got, _ := a.trySubmit(slackMessage()); got != taxonomy.RejectAgentNotFound {
			t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectAgentNotFound)
		}
	})

	t.Run("no agent to route to is an agent that is not found", func(t *testing.T) {
		a := refusalAdapter(t, "")
		if got, _ := a.trySubmit(slackMessage()); got != taxonomy.RejectAgentNotFound {
			t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectAgentNotFound)
		}
	})
}

func TestSlackRefusalReasonsAreInTheClosedSet(t *testing.T) {
	for _, defaultAgent := range []string{"", "does-not-exist"} {
		a := refusalAdapter(t, defaultAgent)
		reason, _ := a.trySubmit(slackMessage())
		if reason == "" {
			continue
		}
		if !slices.Contains(taxonomy.RejectReasons, reason) {
			t.Errorf("refusal reason %q is outside the closed set", reason)
		}
	}
}

func TestAnAcceptedSlackMessageReportsNoRefusal(t *testing.T) {
	a := refusalAdapter(t, "Orchestrator")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Orchestrator"})
	if got, _ := a.trySubmit(slackMessage()); got != "" {
		t.Fatalf("an accepted Slack message reported refusal %q", got)
	}
}

func TestASlackRefusalYieldsOneReason(t *testing.T) {
	a := refusalAdapter(t, "does-not-exist")
	in := slackMessage()
	in.text = ""
	if got, _ := a.trySubmit(in); got != taxonomy.RejectAgentNotFound {
		t.Errorf("refusal reason = %q, want the answering gate's %q alone", got, taxonomy.RejectAgentNotFound)
	}
}

func TestTheSlackAdapterDeclaresTheClientItsRefusalsReport(t *testing.T) {
	a := &SlackGatewayAdapter{}
	if got := a.RelayedSurface(); got != httputil.SurfaceSlack {
		t.Fatalf("RelayedSurface = %q, want %q", got, httputil.SurfaceSlack)
	}
	if !slices.Contains(httputil.SubmitterSurfaces, a.RelayedSurface()) {
		t.Errorf("submittedFrom does not accept %q, so a refused Slack message would name no client", a.RelayedSurface())
	}
}
