// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"sync"
	"testing"
	"time"
)

type recordingMetrics struct {
	mu         sync.Mutex
	counters   []recordedSample
	gauges     []recordedSample
	histograms []recordedSample
}

type recordedSample struct {
	name   string
	value  float64
	labels []string
}

func (r *recordingMetrics) Counter(name string, delta int64, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = append(r.counters, recordedSample{name: name, value: float64(delta), labels: labels})
}

func (r *recordingMetrics) Gauge(name string, value float64, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges = append(r.gauges, recordedSample{name: name, value: value, labels: labels})
}

func (r *recordingMetrics) Histogram(name string, value time.Duration, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms = append(r.histograms, recordedSample{name: name, value: value.Seconds(), labels: labels})
}

func TestSTRMetrics_NilSafe(t *testing.T) {
	var m *strMetrics
	m.recordSemaphoreWait(time.Millisecond, "subprocess")
	m.recordSetupDuration(time.Millisecond, "tool")
	m.recordExecDuration(time.Millisecond, "tool", "ok")
	m.recordError("tool", "code")
	m.recordCancellation("tool")
	done := m.invocationStarted("tool")
	done("ok")
}

func TestSTRMetrics_InvocationStarted_GaugeAndHistogram(t *testing.T) {
	rec := &recordingMetrics{}
	m := newSTRMetrics(rec)

	done1 := m.invocationStarted("toolA")
	done2 := m.invocationStarted("toolB")

	rec.mu.Lock()
	if len(rec.gauges) != 2 {
		t.Errorf("expected 2 gauge emissions on start, got %d", len(rec.gauges))
	}
	if rec.gauges[1].value != 2 {
		t.Errorf("inflight gauge after second start = %v, want 2", rec.gauges[1].value)
	}
	rec.mu.Unlock()

	done1("ok")
	done2("error")

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if len(rec.histograms) != 2 {
		t.Errorf("expected 2 dispatch histograms, got %d", len(rec.histograms))
	}
	for _, h := range rec.histograms {
		if h.name != metricDispatchDuration {
			t.Errorf("histogram name = %q, want %q", h.name, metricDispatchDuration)
		}
	}

	if len(rec.gauges) != 4 {
		t.Errorf("expected 4 gauge emissions, got %d", len(rec.gauges))
	}
	if rec.gauges[3].value != 0 {
		t.Errorf("final inflight gauge = %v, want 0", rec.gauges[3].value)
	}
}

func TestSTRMetrics_ErrorAndCancellation(t *testing.T) {
	rec := &recordingMetrics{}
	m := newSTRMetrics(rec)

	m.recordError("toolA", "timeout")
	m.recordCancellation("toolA")

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if len(rec.counters) != 2 {
		t.Fatalf("expected 2 counter emissions, got %d", len(rec.counters))
	}
	if rec.counters[0].name != metricErrors {
		t.Errorf("first counter = %q, want %q", rec.counters[0].name, metricErrors)
	}
	if rec.counters[1].name != metricCancellations {
		t.Errorf("second counter = %q, want %q", rec.counters[1].name, metricCancellations)
	}
}
