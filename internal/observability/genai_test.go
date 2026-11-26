// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"testing"
)

func TestNewGenAIMonitor_Labels(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewGenAIMonitor(metrics, "claude-sonnet")
	m.Stop(nil)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}
	h := metrics.histograms[0]
	if h.name != metricGenAIOperationDuration {
		t.Errorf("metric = %q, want %q", h.name, metricGenAIOperationDuration)
	}
	assertLabel(t, h.labels, "gen_ai.request.model", "claude-sonnet")
	assertLabel(t, h.labels, "error.type", "")
}

func TestNewGenAIMonitor_ErrorClassified(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewGenAIMonitor(metrics, "gpt-4")
	err := errors.New("context_length_exceeded")
	m.Stop(&err)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram")
	}
	assertLabel(t, metrics.histograms[0].labels, "gen_ai.request.model", "gpt-4")
	assertLabel(t, metrics.histograms[0].labels, "error.type", "context_length_exceeded")
}

func TestNewGenAITTFTMonitor_Labels(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewGenAITTFTMonitor(metrics, "claude-sonnet")
	m.Stop(nil)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}
	h := metrics.histograms[0]
	if h.name != metricGenAITTFTDuration {
		t.Errorf("metric = %q, want %q", h.name, metricGenAITTFTDuration)
	}
	assertLabel(t, h.labels, "gen_ai.request.model", "claude-sonnet")
	assertLabel(t, h.labels, "error.type", "")
}

func TestNewGenAITTFTMonitor_NeverEmitsTwiceOnDoubleStop(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewGenAITTFTMonitor(metrics, "m")
	m.Stop(nil)
	m.Stop(nil)

	if len(metrics.histograms) != 2 {
		t.Fatalf("expected 2 histograms after double Stop, got %d", len(metrics.histograms))
	}
	for _, h := range metrics.histograms {
		if h.name != metricGenAITTFTDuration {
			t.Errorf("metric name drift: %q", h.name)
		}
	}
}
