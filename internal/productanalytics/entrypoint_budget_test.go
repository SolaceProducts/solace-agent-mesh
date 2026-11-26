// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func bufferedNamed(s *Service, name EventName) int {
	count := 0
	for _, ev := range buffered(s) {
		if ev.Name == name {
			count++
		}
	}
	return count
}

func entrypointEvent() RawEvent {
	return RawEvent{
		Name: EventEntrypointUsed,
		Properties: map[string]any{
			PropEntrypointType: "webhook",
			PropOutcome:        OutcomeSuccess,
		},
	}
}

func TestEntrypointUsedCannotSpendTheServerSurfacesRateBudget(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 10000, BatchSize: 1_000_000, RateCapPerMin: 600}})

	flood := make([]RawEvent, 5000)
	for i := range flood {
		flood[i] = entrypointEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, flood)

	runs := make([]RawEvent, 600)
	for i := range runs {
		runs[i] = serverRunEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, runs)

	if got := bufferedNamed(s, EventAgentRunCompleted); got != 600 {
		t.Fatalf("server surface buffered %d run reports after an entrypoint flood, want all 600", got)
	}
	if got := droppedOn(s, SurfaceServer, EventAgentRunCompleted); got != 0 {
		t.Errorf("server surface dropped %d run reports, want 0: the flood was on the entrypoint bucket", got)
	}
}

func TestServerRunsCannotSpendTheEntrypointBudget(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 10000, BatchSize: 1_000_000, RateCapPerMin: 600}})

	runs := make([]RawEvent, 5000)
	for i := range runs {
		runs[i] = serverRunEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, runs)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{entrypointEvent()})

	if got := bufferedNamed(s, EventEntrypointUsed); got != 1 {
		t.Fatalf("the entrypoint bucket buffered %d events after a run flood, want 1", got)
	}
	if got := droppedOn(s, SurfaceServer, EventEntrypointUsed); got != 0 {
		t.Errorf("the entrypoint bucket dropped %d, want 0: the flood was on the surface bucket", got)
	}
}

func TestEntrypointBudgetIsBoundedIndependentlyOfTheConfiguredCap(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 100_000, BatchSize: 1_000_000, RateCapPerMin: 100_000}})

	flood := make([]RawEvent, 5000)
	for i := range flood {
		flood[i] = entrypointEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, flood)

	if got := bufferedNamed(s, EventEntrypointUsed); got != entrypointRateCapPerMin {
		t.Fatalf("the entrypoint bucket admitted %d of a 5000-event flood, want its own cap of %d", got, entrypointRateCapPerMin)
	}
}

func machineRefusalEvent() RawEvent {
	return RawEvent{
		Name: EventTaskSubmitRejected,
		Properties: map[string]any{
			PropRejectReason:   RejectForbidden,
			PropEntrypointType: "webhook",
		},
	}
}

func relayedRefusalEvent() RawEvent {
	return RawEvent{
		Name: EventTaskSubmitRejected,
		Properties: map[string]any{
			PropRejectReason:  RejectForbidden,
			PropSubmittedFrom: httputil.SurfaceSlack,
		},
	}
}

func TestAMachineIngressRefusalCannotSpendTheServerSurfacesRateBudget(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 10000, BatchSize: 1_000_000, RateCapPerMin: 600}})

	flood := make([]RawEvent, 5000)
	for i := range flood {
		flood[i] = machineRefusalEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, flood)

	runs := make([]RawEvent, 600)
	for i := range runs {
		runs[i] = serverRunEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, runs)

	if got := bufferedNamed(s, EventAgentRunCompleted); got != 600 {
		t.Fatalf("server surface buffered %d run reports after a machine-refusal flood, want all 600", got)
	}
	if got := bufferedNamed(s, EventTaskSubmitRejected); got != entrypointRateCapPerMin {
		t.Errorf("the entrypoint bucket admitted %d refusals of a 5000-event flood, want its own cap of %d", got, entrypointRateCapPerMin)
	}
}

func TestARelayedRefusalStaysOnTheSurfaceBudget(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 10000, BatchSize: 1_000_000, RateCapPerMin: 600}})

	flood := make([]RawEvent, 5000)
	for i := range flood {
		flood[i] = machineRefusalEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, flood)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{relayedRefusalEvent()})

	relayed := 0
	for _, ev := range buffered(s) {
		if ev.Name == EventTaskSubmitRejected && ev.Properties[PropSubmittedFrom] == httputil.SurfaceSlack {
			relayed++
		}
	}
	if relayed != 1 {
		t.Fatalf("the surface bucket buffered %d relayed refusals after a machine-refusal flood, want 1", relayed)
	}
}

func TestReportDropsNamesTheEntrypointBudget(t *testing.T) {
	capture := &warnCapture{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Consent:  allowConsent,
		Logger:   slog.New(capture),
		Config:   ServiceConfig{BufferSize: 1000, BatchSize: 1_000_000, RateCapPerMin: 1},
	})
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{serverRunEvent(), serverRunEvent()})
	flood := make([]RawEvent, entrypointRateCapPerMin+3)
	for i := range flood {
		flood[i] = entrypointEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, flood)

	s.reportDrops()
	drops := capture.drops()
	if len(drops) != 2 {
		t.Fatalf("emitted %d drop lines, want one per bucket that lost events", len(drops))
	}
	want := map[string]int64{SurfaceServer: 1, SurfaceServer + entrypointBudgetSuffix: 3}
	for _, record := range drops {
		budget, ok := attrString(record, "budget")
		if !ok {
			t.Fatalf("drop line carries no budget, so an operator cannot tell which cap ran out: %v", record)
		}
		if surface, _ := attrString(record, "surface"); surface != SurfaceServer {
			t.Errorf("budget %q reported under surface %q, want %q", budget, surface, SurfaceServer)
		}
		delta, _ := attrInt64(record, "dropped")
		if delta != want[budget] {
			t.Errorf("budget %q: dropped delta = %d, want %d", budget, delta, want[budget])
		}
		delete(want, budget)
	}
	if len(want) != 0 {
		t.Errorf("no drop line for %v", want)
	}
}
