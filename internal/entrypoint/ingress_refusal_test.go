// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestARelayedRefusalNamesTheClientItCameFrom(t *testing.T) {
	for _, surface := range httputil.RelayedSurfaces {
		t.Run(surface, func(t *testing.T) {
			core := NewGatewayCore(&relayAdapter{surface: surface}, nil)
			props := core.rejectionProperties(SubmitRejection{Reason: productanalytics.RejectAgentNotFound})

			if got := props[productanalytics.PropSubmittedFrom]; got != surface {
				t.Errorf("submittedFrom = %v, want %q", got, surface)
			}
			if got := props[productanalytics.PropRejectReason]; got != productanalytics.RejectAgentNotFound {
				t.Errorf("rejectReason = %v, want %q", got, productanalytics.RejectAgentNotFound)
			}
			if got, present := props[productanalytics.PropEntrypointType]; present {
				t.Errorf("entrypointType = %v, want absent: a person in %s is not machine ingress", got, surface)
			}
		})
	}
}

func TestAMachineIngressRefusalNamesTheEntrypointKind(t *testing.T) {
	for _, entrypointType := range []string{"webhook", "event_mesh", "mcp"} {
		t.Run(entrypointType, func(t *testing.T) {
			core := NewGatewayCore(&machineIngressAdapter{entrypointType: entrypointType}, nil)
			props := core.rejectionProperties(SubmitRejection{Reason: productanalytics.RejectForbidden})

			if got := props[productanalytics.PropEntrypointType]; got != entrypointType {
				t.Errorf("entrypointType = %v, want %q", got, entrypointType)
			}
			if got, present := props[productanalytics.PropSubmittedFrom]; present {
				t.Errorf("submittedFrom = %v, want absent: a machine entrypoint has no client to name", got)
			}
		})
	}
}

func TestTheHTTPEntrypointsRefusalStaysHeaderAsserted(t *testing.T) {
	core := NewGatewayCore(&recordingAdapter{}, nil)
	for _, surface := range []string{httputil.SurfaceWebUI, httputil.SurfaceCLI, httputil.SurfaceAPI} {
		props := core.rejectionProperties(SubmitRejection{Reason: productanalytics.RejectMalformed, Surface: surface})
		if got := props[productanalytics.PropSubmittedFrom]; got != surface {
			t.Errorf("submittedFrom = %v, want the declared %q", got, surface)
		}
		if got, present := props[productanalytics.PropEntrypointType]; present {
			t.Errorf("entrypointType = %v, want absent on the HTTP entrypoint", got)
		}
	}
}

func TestARelayOverridesADeclaredSurfaceOnARefusal(t *testing.T) {
	core := NewGatewayCore(&relayAdapter{surface: httputil.SurfaceTeams}, nil)
	props := core.rejectionProperties(SubmitRejection{Reason: productanalytics.RejectForbidden, Surface: httputil.SurfaceWebUI})
	if got := props[productanalytics.PropSubmittedFrom]; got != httputil.SurfaceTeams {
		t.Errorf("submittedFrom = %v, want the adapter's %q to win over the declared %q", got, httputil.SurfaceTeams, httputil.SurfaceWebUI)
	}
}

func TestTheHeaderAssertedAndAdapterAssertedSurfacesAreDisjoint(t *testing.T) {
	for _, surface := range httputil.RelayedSurfaces {
		if slices.Contains([]string{httputil.SurfaceWebUI, httputil.SurfaceCLI, httputil.SurfaceAPI}, surface) {
			t.Errorf("%q is both header-asserted and adapter-asserted", surface)
		}
	}
}

func TestAnAcceptedSubmitReportsNoRefusal(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{}))
	h.core.ReportSubmitRefusal("")

	if events := eventsNamed(h.drain(t), productanalytics.EventTaskSubmitRejected); len(events) != 0 {
		t.Fatalf("task:submit_rejected emitted %d times for a submit nothing refused: %+v", len(events), events)
	}
}

func TestAnAdapterRefusalIsReportedOnce(t *testing.T) {
	for _, surface := range httputil.RelayedSurfaces {
		t.Run(surface, func(t *testing.T) {
			h := newRunAnalyticsHarness(t, withAdapter(&relayAdapter{surface: surface}))
			h.core.ReportSubmitRefusal(productanalytics.RejectAgentNotFound)

			events := eventsNamed(h.drain(t), productanalytics.EventTaskSubmitRejected)
			if len(events) != 1 {
				t.Fatalf("task:submit_rejected emitted %d times, want 1", len(events))
			}
			if got := events[0].Properties[productanalytics.PropSubmittedFrom]; got != surface {
				t.Errorf("submittedFrom = %v, want %q delivered", got, surface)
			}
		})
	}
	for _, entrypointType := range []string{"webhook", "event_mesh", "mcp"} {
		t.Run(entrypointType, func(t *testing.T) {
			h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: entrypointType}))
			h.core.ReportSubmitRefusal(productanalytics.RejectForbidden)

			events := eventsNamed(h.drain(t), productanalytics.EventTaskSubmitRejected)
			if len(events) != 1 {
				t.Fatalf("task:submit_rejected emitted %d times, want 1", len(events))
			}
			if got := events[0].Properties[productanalytics.PropEntrypointType]; got != entrypointType {
				t.Errorf("entrypointType = %v, want %q delivered", got, entrypointType)
			}
		})
	}
}

func TestReportingARefusalWithNoGatewayIsInert(t *testing.T) {
	var core *GatewayCore
	core.ReportSubmitRefusal(productanalytics.RejectInternal)
}

func TestASubmitFailureSeparatesAuthorizationFromFault(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"a denied delegation is an authorization refusal": {
			err:  ErrAgentDelegationDenied,
			want: productanalytics.RejectForbidden,
		},
		"a denial reached through a wrapper is still one": {
			err:  fmt.Errorf("submit task: %w", ErrAgentDelegationDenied),
			want: productanalytics.RejectForbidden,
		},
		"anything else is a fault on this side": {
			err:  errors.New("broker publish: connection reset"),
			want: productanalytics.RejectInternal,
		},
		"a spent budget is its own refusal": {
			err:  fmt.Errorf("submit task: %w", &budget.ExhaustedError{Tier: budget.TierMesh}),
			want: taxonomy.RejectBudgetExhausted,
		},
		"an unconfirmed budget state is its own refusal": {
			err:  fmt.Errorf("submit task: %w", &budget.UnconfirmedError{}),
			want: taxonomy.RejectBudgetUnconfirmed,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := SubmitFailureReason(c.err); got != c.want {
				t.Errorf("SubmitFailureReason(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}
