// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"testing"
)

func TestNewArtifactMonitor_Labels(t *testing.T) {
	cases := []struct {
		op   string
		want string
	}{
		{ArtifactOpSave, "save"},
		{ArtifactOpLoad, "load"},
		{ArtifactOpDelete, "delete"},
		{ArtifactOpList, "list"},
	}

	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			metrics := installMetrics(t, &collectingMetrics{})
			m := NewArtifactMonitor(tc.op)
			m.Stop(nil)

			if len(metrics.histograms) != 1 {
				t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
			}
			h := metrics.histograms[0]
			if h.name != "outbound.request.duration" {
				t.Errorf("metric = %q, want outbound.request.duration", h.name)
			}
			assertLabel(t, h.labels, "service.peer.name", "artifact_service")
			assertLabel(t, h.labels, "operation.name", tc.want)
			assertLabel(t, h.labels, "error.type", "")
		})
	}
}

func TestNewArtifactMonitor_ErrorType(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})
	m := NewArtifactMonitor(ArtifactOpSave)
	err := errors.New("unauthorized")
	m.Stop(&err)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram")
	}
	assertLabel(t, metrics.histograms[0].labels, "error.type", "auth_error")
}
