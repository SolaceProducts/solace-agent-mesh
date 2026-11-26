// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"testing"
)

func machineFactEvent() RawEvent {
	return RawEvent{
		Name: EventToolUsed,
		Properties: map[string]any{
			PropToolCategory: ToolCategoryBuiltin,
			PropRunOrigin:    RunOriginMachineIngress,
		},
	}
}

func scheduledFactEvent() RawEvent {
	return RawEvent{
		Name: EventToolUsed,
		Properties: map[string]any{
			PropToolCategory: ToolCategoryBuiltin,
			PropRunOrigin:    RunOriginScheduled,
		},
	}
}

func TestMachineFactsCannotSpendTheServerSurfacesRateBudget(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 10000, BatchSize: 1_000_000, RateCapPerMin: 600}})

	flood := make([]RawEvent, 5000)
	for i := range flood {
		flood[i] = machineFactEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, flood)

	runs := make([]RawEvent, 600)
	for i := range runs {
		runs[i] = serverRunEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, runs)

	if got := bufferedNamed(s, EventAgentRunCompleted); got != 600 {
		t.Fatalf("server surface buffered %d run reports after a machine-fact flood, want all 600", got)
	}
	if got := droppedOn(s, SurfaceServer, EventAgentRunCompleted); got != 0 {
		t.Errorf("server surface dropped %d run reports, want 0: the flood was on the machine-fact bucket", got)
	}
}

func TestMachineFactsCannotSpendTheEntrypointBudget(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 10000, BatchSize: 1_000_000, RateCapPerMin: 600}})

	flood := make([]RawEvent, 5000)
	for i := range flood {
		flood[i] = machineFactEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, flood)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{entrypointEvent()})

	if got := bufferedNamed(s, EventEntrypointUsed); got != 1 {
		t.Fatalf("the entrypoint bucket buffered %d events after a machine-fact flood, want 1", got)
	}
	if got := droppedOn(s, SurfaceServer, EventEntrypointUsed); got != 0 {
		t.Errorf("the entrypoint bucket dropped %d, want 0: the flood was on the machine-fact bucket", got)
	}
}

func TestMachineFactBudgetIsBoundedIndependentlyOfTheConfiguredCap(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 100_000, BatchSize: 1_000_000, RateCapPerMin: 100_000}})

	flood := make([]RawEvent, 5000)
	for i := range flood {
		flood[i] = machineFactEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, flood)

	if got := bufferedNamed(s, EventToolUsed); got != machineFactRateCapPerMin {
		t.Fatalf("the machine-fact bucket admitted %d of a 5000-event flood, want its own cap of %d", got, machineFactRateCapPerMin)
	}
}

func TestScheduledFactsRideTheGeneralServerBudget(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 10000, BatchSize: 1_000_000, RateCapPerMin: 600}})

	facts := make([]RawEvent, machineFactRateCapPerMin+40)
	for i := range facts {
		facts[i] = scheduledFactEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, facts)

	if got := bufferedNamed(s, EventToolUsed); got != len(facts) {
		t.Fatalf("buffered %d scheduled usage facts of %d, want all: they belong on the general bucket, not the outside-caller one", got, len(facts))
	}
	if got := len(machineFactBudget(s, SurfaceServer).buf); got != 0 {
		t.Errorf("the machine-fact bucket holds %d scheduled facts, want 0", got)
	}
}

func TestServerRunsCannotSpendTheMachineFactBudget(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 10000, BatchSize: 1_000_000, RateCapPerMin: 600}})

	runs := make([]RawEvent, 5000)
	for i := range runs {
		runs[i] = serverRunEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, runs)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{machineFactEvent()})

	if got := bufferedNamed(s, EventToolUsed); got != 1 {
		t.Fatalf("the machine-fact bucket buffered %d events after a run flood, want 1", got)
	}
	if got := machineFactBudget(s, SurfaceServer).dropped; got != 0 {
		t.Errorf("the machine-fact bucket dropped %d, want 0: the flood was on the surface bucket", got)
	}
}
