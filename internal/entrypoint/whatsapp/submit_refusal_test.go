// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"errors"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func refusalTrySubmit(a *Adapter, wamid, body string) string {
	value, msg := textMessage(wamid, "15551230000", body)
	_, reason := a.trySubmit(testEnvelope(a, value, msg), body, []a2a.Part{a2a.NewTextPart(body)}, testSessionEpoch)
	return reason
}

func TestWhatsAppRefusalsReportTheirReason(t *testing.T) {
	mock := newWAMock(t)

	t.Run("a misconfigured default agent is an agent that is not found", func(t *testing.T) {
		a := newAdapterRig(t, mock, "does-not-exist")
		a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
		if got := refusalTrySubmit(a, "wamid.refusal1", "hello"); got != taxonomy.RejectAgentNotFound {
			t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectAgentNotFound)
		}
	})

	t.Run("no agent to route to is an agent that is not found", func(t *testing.T) {
		a := newAdapterRig(t, mock, "")
		if got := refusalTrySubmit(a, "wamid.refusal2", "hello"); got != taxonomy.RejectAgentNotFound {
			t.Errorf("refusal reason = %q, want %q", got, taxonomy.RejectAgentNotFound)
		}
	})
}

func TestWhatsAppRefusalReasonsAreInTheClosedSet(t *testing.T) {
	mock := newWAMock(t)
	for i, defaultAgent := range []string{"", "does-not-exist"} {
		a := newAdapterRig(t, mock, defaultAgent)
		reason := refusalTrySubmit(a, "wamid.closed"+string(rune('a'+i)), "hello")
		if reason == "" {
			continue
		}
		if !slices.Contains(taxonomy.RejectReasons, reason) {
			t.Errorf("refusal reason %q is outside the closed set", reason)
		}
	}
}

type registerRefusingRegistry struct{ entrypoint.TaskRegistry }

func (registerRefusingRegistry) Register(string, string, string, entrypoint.TaskIdentity) error {
	return errors.New("database unreachable")
}

func TestAMessageWhoseRecordFailsIsRetriedNotAnswered(t *testing.T) {
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Orchestrator")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Orchestrator"})
	a.coreTasksOverride = registerRefusingRegistry{a.core.Tasks()}

	value, msg := textMessage("wamid.norecord", "15551230000", "hello")
	outcome, reason := a.trySubmit(testEnvelope(a, value, msg), "hello", []a2a.Part{a2a.NewTextPart("hello")}, testSessionEpoch)

	if outcome != outcomeTransient {
		t.Errorf("outcome = %v, want %v", outcome, outcomeTransient)
	}
	if reason != taxonomy.RejectInternal {
		t.Errorf("refusal reason = %q, want %q", reason, taxonomy.RejectInternal)
	}
	if ids := a.core.Tasks().AllByAgent("Orchestrator"); len(ids) != 0 {
		t.Errorf("SubmitTask must not run for a task that was not recorded, yet the core tracks %v", ids)
	}
}

func TestAnAcceptedWhatsAppMessageReportsNoRefusal(t *testing.T) {
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Orchestrator")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Orchestrator"})
	if got := refusalTrySubmit(a, "wamid.accepted", "hello"); got != "" {
		t.Fatalf("an accepted WhatsApp message reported refusal %q", got)
	}
}

func TestTheWhatsAppAdapterDeclaresTheClientItsRefusalsReport(t *testing.T) {
	a := &Adapter{}
	if got := a.RelayedSurface(); got != httputil.SurfaceWhatsApp {
		t.Fatalf("RelayedSurface = %q, want %q", got, httputil.SurfaceWhatsApp)
	}
	if !slices.Contains(httputil.SubmitterSurfaces, a.RelayedSurface()) {
		t.Errorf("submittedFrom does not accept %q, so a refused WhatsApp message would name no client", a.RelayedSurface())
	}
}
