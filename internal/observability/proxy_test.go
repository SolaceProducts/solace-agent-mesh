// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"syscall"
	"testing"
)

func TestNewRemoteAgentProxyMonitor_LabelsAndMetric(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewRemoteAgentProxyMonitor("my-agent")
	m.Stop(nil)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}
	h := metrics.histograms[0]
	if h.name != "operation.duration" {
		t.Errorf("metric = %q, want operation.duration", h.name)
	}
	assertLabel(t, h.labels, "type", "a2a_agent")
	assertLabel(t, h.labels, "component.name", "my-agent")
	assertLabel(t, h.labels, "operation.name", "forward_request")
	assertLabel(t, h.labels, "error.type", "")
}

func TestNewRemoteAgentProxyMonitor_UsesA2AClassifier(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{"http 401 text", errors.New("HTTP 401 unauthorized"), "auth_error"},
		{"http 503 text", errors.New("HTTP 503 bad gateway"), "5xx_error"},
		{"jsonrpc text", errors.New("downstream error -32602"), "jsonrpc_error"},
		{"connection refused", syscall.ECONNREFUSED, "connection_error"},
		{"timeout fallback", errors.New("context deadline exceeded: timeout"), "timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := installMetrics(t, &collectingMetrics{})
			m := NewRemoteAgentProxyMonitor("a")
			err := tt.err
			m.Stop(&err)
			if len(metrics.histograms) != 1 {
				t.Fatalf("expected 1 histogram")
			}
			assertLabel(t, metrics.histograms[0].labels, "error.type", tt.wantErr)
		})
	}
}
