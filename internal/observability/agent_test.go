// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"testing"
)

func TestNewAgentMonitor_Labels(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewAgentMonitor(metrics, "MyAgent")
	m.Stop(nil)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}
	h := metrics.histograms[0]
	if h.name != "operation.duration" {
		t.Errorf("metric = %q, want operation.duration", h.name)
	}
	assertLabel(t, h.labels, "type", "agent")
	assertLabel(t, h.labels, "component.name", "MyAgent")
	assertLabel(t, h.labels, "operation.name", "execute")
	assertLabel(t, h.labels, "error.type", "")
}

func TestNewAgentMonitor_ErrorClassification(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{"timeout", errors.New("context deadline exceeded: timeout"), "timeout"},
		{"unauthorized", errors.New("permission denied"), "auth_error"},
		{"rate limit", errors.New("rate limit exceeded"), "rate_limit"},
		{"context length", errors.New("maximum context length exceeded"), "context_length_exceeded"},
		{"generic", errors.New("something went wrong"), "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := installMetrics(t, &collectingMetrics{})
			m := NewAgentMonitor(metrics, "MyAgent")
			err := tt.err
			m.Stop(&err)

			if len(metrics.histograms) != 1 {
				t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
			}
			assertLabel(t, metrics.histograms[0].labels, "error.type", tt.wantErr)
		})
	}
}
