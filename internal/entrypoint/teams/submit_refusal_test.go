// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func (a *TeamsAdapter) trySubmitReason(t *testing.T, act *Activity, files []Attachment) string {
	t.Helper()
	reason, _ := a.trySubmit(t.Context(), act, act.Text, files, nil)
	return reason
}

func TestTeamsRefusalsReportTheirReason(t *testing.T) {
	t.Run("a misconfigured default agent is an agent that is not found", func(t *testing.T) {
		a, _, _ := newRunAsTeamsFixture(t, "TestAgent")
		a.cfg.DefaultAgentName = "does-not-exist"
		act := teamsActivity(ConversationTypePersonal)
		if got := a.trySubmitReason(t, act, nil); got != taxonomy.RejectAgentNotFound {
			t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectAgentNotFound)
		}
	})

	t.Run("no agent to route to is an agent that is not found", func(t *testing.T) {
		a, _, _ := newRunAsTeamsFixture(t, "TestAgent")
		a.cfg.DefaultAgentName = ""
		a.core.AgentRegistry().Remove("TestAgent")
		act := teamsActivity(ConversationTypePersonal)
		if got := a.trySubmitReason(t, act, nil); got != taxonomy.RejectAgentNotFound {
			t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectAgentNotFound)
		}
	})

	t.Run("an attachment the adapter cannot take is malformed caller data", func(t *testing.T) {
		a, _, _ := newRunAsTeamsFixture(t, "TestAgent")
		act := teamsActivity(ConversationTypePersonal)
		attachments := []Attachment{{Name: "big.bin", ContentURL: "https://127.0.0.1:1/big.bin"}}
		if got := a.trySubmitReason(t, act, attachments); got != taxonomy.RejectMalformed {
			t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectMalformed)
		}
	})
}

func TestTeamsRefusalReasonsAreInTheClosedSet(t *testing.T) {
	for _, defaultAgent := range []string{"", "does-not-exist", "TestAgent"} {
		a, _, _ := newRunAsTeamsFixture(t, "TestAgent")
		a.cfg.DefaultAgentName = defaultAgent
		act := teamsActivity(ConversationTypePersonal)
		reason := a.trySubmitReason(t, act, nil)
		if reason == "" {
			continue
		}
		if !slices.Contains(taxonomy.RejectReasons, reason) {
			t.Errorf("refusal reason %q is outside the closed set", reason)
		}
	}
}

func TestAnAcceptedTeamsMessageReportsNoRefusal(t *testing.T) {
	a, brk, q := newRunAsTeamsFixture(t, "TestAgent")
	act := teamsActivity(ConversationTypePersonal)
	if got := a.trySubmitReason(t, act, nil); got != "" {
		t.Fatalf("an accepted Teams message reported refusal %q", got)
	}
	recvTeamsMessage(t, brk, q)
}

func TestATeamsRefusalYieldsOneReason(t *testing.T) {
	a, _, _ := newRunAsTeamsFixture(t, "TestAgent")
	a.cfg.DefaultAgentName = "does-not-exist"
	act := teamsActivity(ConversationTypePersonal)
	oversized := []Attachment{{Name: "big.bin", ContentURL: "https://example.invalid/big.bin"}}
	if got := a.trySubmitReason(t, act, oversized); got != taxonomy.RejectAgentNotFound {
		t.Errorf("refusal reason = %q, want the answering gate's %q alone", got, taxonomy.RejectAgentNotFound)
	}
}

func TestTheTeamsAdapterDeclaresTheClientItsRefusalsReport(t *testing.T) {
	a := &TeamsAdapter{}
	if got := a.RelayedSurface(); got != httputil.SurfaceTeams {
		t.Fatalf("RelayedSurface = %q, want %q", got, httputil.SurfaceTeams)
	}
	if !slices.Contains(httputil.SubmitterSurfaces, a.RelayedSurface()) {
		t.Errorf("submittedFrom does not accept %q, so a refused Teams message would name no client", a.RelayedSurface())
	}
}
