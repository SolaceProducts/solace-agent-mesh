// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

type relayAdapter struct {
	recordingAdapter
	surface string
}

func (a *relayAdapter) RelayedSurface() string { return a.surface }

func TestARelayedRunIsCountedAndNamesItsClient(t *testing.T) {
	for _, surface := range httputil.RelayedSurfaces {
		t.Run(surface, func(t *testing.T) {
			h := newRunAnalyticsHarness(t, withAdapter(&relayAdapter{surface: surface}))
			h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
			taskID := h.submit(t, "my-agent", "task-"+surface, nil)
			h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

			events := h.drain(t)
			runs := eventsNamed(events, productanalytics.EventAgentRunCompleted)
			if len(runs) != 1 {
				t.Fatalf("agent:run_completed emitted %d times, want 1: %+v", len(runs), events)
			}
			if got := runs[0].Properties[productanalytics.PropSubmittedFrom]; got != surface {
				t.Errorf("submittedFrom = %v, want %q", got, surface)
			}
			if got := len(eventsNamed(events, productanalytics.EventEntrypointUsed)); got != 0 {
				t.Errorf("a person in %s reported entrypoint:used %d times: relayed traffic is not machine ingress", surface, got)
			}
		})
	}
}

func TestTheEmitterKeepsEveryRelayedSurface(t *testing.T) {
	for _, surface := range httputil.RelayedSurfaces {
		info := &TaskInfo{TaskOrigin: TaskOrigin{Surface: surface, AgentType: productanalytics.AgentTypeUserCreated}}
		props := runReportProperties(info, completedTask("task-"+surface), nil, usageFacts{})
		if got := props[productanalytics.PropSubmittedFrom]; got != surface {
			t.Errorf("submittedFrom = %v, want %q kept where the event is built", got, surface)
		}
	}
}

func TestAnUnknownRelayedSurfaceIsRefused(t *testing.T) {
	for _, surface := range []string{"", "discord", "acme-chat", httputil.SurfaceWebUI} {
		core := &GatewayCore{adapter: &relayAdapter{surface: surface}}
		if got := core.relayedSurface(); got != "" {
			t.Errorf("an adapter declaring %q was allowed to report %q", surface, got)
		}
	}
}

func TestARelayIsNotMachineIngress(t *testing.T) {
	core := &GatewayCore{adapter: &relayAdapter{surface: httputil.SurfaceSlack}}
	if core.machineIngress() {
		t.Error("a relay adapter read as machine ingress, which would drop its runs from the run count")
	}
	if core.entrypointType() != "" {
		t.Error("a relay adapter named an entrypoint type, which would report entrypoint:used instead of the run")
	}
}

func TestNoRequestCanClaimARelayedSurface(t *testing.T) {
	for _, surface := range httputil.RelayedSurfaces {
		r := httptest.NewRequest("POST", "/api/v1/message:stream", nil)
		r.Header.Set(httputil.HeaderClientSurface, surface)
		if got := httputil.ClientSurface(r); got != httputil.SurfaceAPI {
			t.Errorf("a request declaring %q read as %q, want %q", surface, got, httputil.SurfaceAPI)
		}
	}
}

func TestRelayedSurfacesAreThePlatformsEntrypointNames(t *testing.T) {
	for _, surface := range httputil.RelayedSurfaces {
		if !slices.Contains(productanalytics.GatewayTypes, surface) {
			t.Errorf("%q is not a productanalytics.GatewayTypes entrypoint name", surface)
		}
	}
}
