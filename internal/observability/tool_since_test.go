// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"testing"
	"time"
)

func TestNewRemoteToolMonitorSince_MeasuresFromTheGivenStart(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewRemoteToolMonitorSince(metrics, "pptx_create_presentation", time.Now().Add(-10*time.Minute))
	m.Stop(nil)

	if len(metrics.histograms) != 1 {
		t.Fatalf("histograms recorded = %d, want 1", len(metrics.histograms))
	}
	h := metrics.histograms[0]
	got, _ := h.value.(time.Duration)
	if got < 10*time.Minute || got > 11*time.Minute {
		t.Errorf("duration recorded = %v, want between 10m and 11m since the given start", got)
	}
	assertLabel(t, h.labels, "type", "str_tool")
	assertLabel(t, h.labels, "component.name", "pptx_create_presentation")
	assertLabel(t, h.labels, "operation.name", "invoke")
}
