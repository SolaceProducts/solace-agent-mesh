// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"testing"
)

func TestNewGatewayOutboundMonitor_Labels(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewGatewayOutboundMonitor("TeamsGateway", "send_message")
	m.Stop(nil)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}
	h := metrics.histograms[0]
	if h.name != "outbound.request.duration" {
		t.Errorf("metric = %q, want outbound.request.duration", h.name)
	}
	assertLabel(t, h.labels, "service.peer.name", "TeamsGateway")
	assertLabel(t, h.labels, "operation.name", "send_message")
	assertLabel(t, h.labels, "error.type", "")
}

func TestNewGatewayOutboundMonitor_PropagatesErrorType(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})
	m := NewGatewayOutboundMonitor("SlackGateway", "post_message")
	err := errors.New("rate limit exceeded")
	m.Stop(&err)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram")
	}
	assertLabel(t, metrics.histograms[0].labels, "service.peer.name", "SlackGateway")
	assertLabel(t, metrics.histograms[0].labels, "operation.name", "post_message")
	assertLabel(t, metrics.histograms[0].labels, "error.type", "rate_limit")
}
