// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"testing"
)

func TestNewToolMonitor_Labels(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewToolMonitor(metrics, "web_search")
	m.Stop(nil)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}
	h := metrics.histograms[0]
	if h.name != "operation.duration" {
		t.Errorf("metric = %q, want operation.duration", h.name)
	}
	assertLabel(t, h.labels, "type", "tool")
	assertLabel(t, h.labels, "component.name", "web_search")
	assertLabel(t, h.labels, "operation.name", "execute")
	assertLabel(t, h.labels, "error.type", "")
}

func TestNewRemoteToolMonitor_Labels(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewRemoteToolMonitor(metrics, "pptx_create_presentation")
	m.Stop(nil)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}
	h := metrics.histograms[0]
	if h.name != "operation.duration" {
		t.Errorf("metric = %q, want operation.duration", h.name)
	}
	assertLabel(t, h.labels, "type", "str_tool")
	assertLabel(t, h.labels, "component.name", "pptx_create_presentation")
	assertLabel(t, h.labels, "operation.name", "invoke")
	assertLabel(t, h.labels, "error.type", "")
}

func TestClassifyToolError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"invalid arg", errors.New("invalid argument"), "validation_error"},
		{"validation msg", errors.New("validation failed for field x"), "validation_error"},
		{"timeout", errors.New("context deadline exceeded: timeout"), "timeout"},
		{"unauthorized falls through", errors.New("unauthorized"), "auth_error"},
		{"rate limit falls through", errors.New("rate limit exceeded"), "rate_limit"},
		{"generic", errors.New("oops"), "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyToolError(tt.err)
			if got != tt.want {
				t.Errorf("classifyToolError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestNewToolMonitor_ValidationErrorLabel(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewToolMonitor(metrics, "pptx_create_presentation")
	err := errors.New("invalid argument: slide layout unknown")
	m.Stop(&err)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram")
	}
	assertLabel(t, metrics.histograms[0].labels, "error.type", "validation_error")
}

func TestNewToolMonitor_StopWithErrorTypePreservesCode(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := NewToolMonitor(metrics, "ffmpeg")
	m.StopWithErrorType("sandbox_failed")

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram")
	}
	assertLabel(t, metrics.histograms[0].labels, "error.type", "sandbox_failed")
}
