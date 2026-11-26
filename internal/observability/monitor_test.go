// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"testing"
	"time"
)

func TestMonitorRecordsDuration(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := newMonitor(metrics, "operation.duration", classifyError,
		"type", "tool",
		"component.name", "web_search",
	)
	time.Sleep(5 * time.Millisecond)
	m.Stop(nil)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}

	h := metrics.histograms[0]
	if h.name != "operation.duration" {
		t.Errorf("name = %q, want %q", h.name, "operation.duration")
	}
	assertLabel(t, h.labels, "type", "tool")
	assertLabel(t, h.labels, "component.name", "web_search")
	assertLabel(t, h.labels, "error.type", "")

	d := h.value.(time.Duration)
	if d < 5*time.Millisecond {
		t.Errorf("duration %v is too short", d)
	}
}

func TestMonitorRecordsError(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := newMonitor(metrics, "operation.duration", classifyError, "type", "agent")
	err := errors.New("timeout: context deadline exceeded")
	m.Stop(&err)

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}

	assertLabel(t, metrics.histograms[0].labels, "error.type", "timeout")
}

func TestMonitorStopWithErrorType(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	m := newMonitor(metrics, "outbound.request.duration", classifyError,
		"service.peer.name", "oauth",
	)
	m.StopWithErrorType("auth_error")

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}

	assertLabel(t, metrics.histograms[0].labels, "error.type", "auth_error")
}

func TestMonitorCapturesPanic(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	rethrown := func() (r any) {
		defer func() { r = recover() }()
		var err error
		defer newMonitor(metrics, "operation.duration", classifyError, "type", "tool").Stop(&err)
		panic("boom")
	}()

	if rethrown != "boom" {
		t.Fatalf("panic was not re-raised; recover() got %v, want \"boom\"", rethrown)
	}
	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}
	assertLabel(t, metrics.histograms[0].labels, "error.type", "panic")
}

func TestMonitorPanicWithErrorClassifies(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	func() {
		defer func() { _ = recover() }()
		var err error
		defer newMonitor(metrics, "operation.duration", classifyError, "type", "agent").Stop(&err)
		panic(errors.New("rate limit exceeded"))
	}()

	if len(metrics.histograms) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
	}
	assertLabel(t, metrics.histograms[0].labels, "error.type", "rate_limit")
}

func TestMonitorElapsed(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})
	m := newMonitor(metrics, "test", classifyError)
	time.Sleep(5 * time.Millisecond)
	elapsed := m.Elapsed()

	if elapsed < 5*time.Millisecond {
		t.Errorf("elapsed %v is too short", elapsed)
	}
}
