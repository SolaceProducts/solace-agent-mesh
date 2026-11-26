// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func TestSubmitRejectionIsReported(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.ReportSubmitRejected(SubmitRejection{TraceID: "019a1c2d-3e4f-7abc-8def-0123456789ab", Surface: httputil.SurfaceCLI, Reason: productanalytics.RejectAgentNotFound})

	events := eventsNamed(h.drain(t), productanalytics.EventTaskSubmitRejected)
	if len(events) != 1 {
		t.Fatalf("task:submit_rejected emitted %d times, want 1", len(events))
	}
	if got := events[0].Properties[productanalytics.PropRejectReason]; got != productanalytics.RejectAgentNotFound {
		t.Errorf("rejectReason = %v, want %q", got, productanalytics.RejectAgentNotFound)
	}
	if got := events[0].Properties[productanalytics.PropSubmittedFrom]; got != httputil.SurfaceCLI {
		t.Errorf("submittedFrom = %v, want %q", got, httputil.SurfaceCLI)
	}
	if _, present := events[0].Properties[productanalytics.PropTraceID]; !present {
		t.Error("the rejection carries no trace, so a browser reporting the same failure joins to nothing")
	}
}

func TestATraceLessRefusalIsStillReported(t *testing.T) {
	props := submitRejectionProperties(productanalytics.RejectForbidden, httputil.SurfaceAPI, "")
	if got := props[productanalytics.PropRejectReason]; got != productanalytics.RejectForbidden {
		t.Errorf("rejectReason = %v, want %q in the built event", got, productanalytics.RejectForbidden)
	}
	if got, present := props[productanalytics.PropTraceID]; present {
		t.Errorf("traceId = %v, want absent when the refusal was decided before one was minted", got)
	}

	h := newRunAnalyticsHarness(t)
	h.core.ReportSubmitRejected(SubmitRejection{Surface: httputil.SurfaceAPI, Reason: productanalytics.RejectForbidden})

	events := eventsNamed(h.drain(t), productanalytics.EventTaskSubmitRejected)
	if len(events) != 1 {
		t.Fatalf("task:submit_rejected emitted %d times, want 1", len(events))
	}
	if got := events[0].Properties[productanalytics.PropRejectReason]; got != productanalytics.RejectForbidden {
		t.Errorf("rejectReason = %v, want %q delivered", got, productanalytics.RejectForbidden)
	}
	if got := events[0].Properties[productanalytics.PropSubmittedFrom]; got != httputil.SurfaceAPI {
		t.Errorf("submittedFrom = %v, want %q", got, httputil.SurfaceAPI)
	}
}

func TestAnUnlistedRejectReasonIsDroppedByTheEmitter(t *testing.T) {
	props := submitRejectionProperties("agent acme-payroll-oracle not found", httputil.SurfaceWebUI, "")
	if got, present := props[productanalytics.PropRejectReason]; present {
		t.Errorf("rejectReason = %#v, want an unlisted reason absent from the built event", got)
	}

	props = submitRejectionProperties(productanalytics.RejectAgentNotFound, httputil.SurfaceWebUI, "")
	if got := props[productanalytics.PropRejectReason]; got != productanalytics.RejectAgentNotFound {
		t.Errorf("rejectReason = %v, want the approved reason kept", got)
	}
}
