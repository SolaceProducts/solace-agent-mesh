// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"sync"
	"testing"
	"time"
)

func TestRecordingMetrics_GaugeRecorded(t *testing.T) {
	r := NewRecordingMetrics()
	r.Gauge("queue.depth", 42, "queue", "tasks")
	r.Gauge("queue.depth", 11, "queue", "tasks")

	if got := len(r.gauges); got != 2 {
		t.Fatalf("expected 2 gauges, got %d", got)
	}
	if r.gauges[0].Name != "queue.depth" || r.gauges[0].Value != 42 {
		t.Errorf("gauge[0] = %+v, want name=queue.depth value=42", r.gauges[0])
	}
	if r.gauges[1].Value != 11 {
		t.Errorf("gauge[1].Value = %v, want 11", r.gauges[1].Value)
	}
	if r.gauges[0].Labels["queue"] != "tasks" {
		t.Errorf("gauge[0].Labels[queue] = %q, want tasks", r.gauges[0].Labels["queue"])
	}
}

func TestRecordingMetrics_HasCounter(t *testing.T) {
	r := NewRecordingMetrics()
	r.Counter("requests", 1, "method", "GET", "status", "200")
	r.Counter("requests", 1, "method", "POST", "status", "500")

	if !r.HasCounter("requests", "method", "GET") {
		t.Error("HasCounter should find GET counter")
	}
	if !r.HasCounter("requests", "method", "GET", "status", "200") {
		t.Error("HasCounter should match both labels")
	}
	if r.HasCounter("requests", "method", "DELETE") {
		t.Error("HasCounter must not match non-recorded labels")
	}
	if r.HasCounter("unknown.metric") {
		t.Error("HasCounter must return false for unknown metric")
	}
}

func TestRecordingMetrics_HistogramCount(t *testing.T) {
	r := NewRecordingMetrics()
	r.Histogram("operation.duration", 10*time.Millisecond, "type", "agent")
	r.Histogram("operation.duration", 20*time.Millisecond, "type", "agent")
	r.Histogram("operation.duration", 5*time.Millisecond, "type", "tool")

	if got := r.HistogramCount("operation.duration"); got != 3 {
		t.Errorf("HistogramCount(no labels) = %d, want 3", got)
	}
	if got := r.HistogramCount("operation.duration", "type", "agent"); got != 2 {
		t.Errorf("HistogramCount(type=agent) = %d, want 2", got)
	}
	if got := r.HistogramCount("operation.duration", "type", "tool"); got != 1 {
		t.Errorf("HistogramCount(type=tool) = %d, want 1", got)
	}
	if got := r.HistogramCount("operation.duration", "type", "missing"); got != 0 {
		t.Errorf("HistogramCount(missing) = %d, want 0", got)
	}
}

func TestRecordingMetrics_CountersByName(t *testing.T) {
	r := NewRecordingMetrics()
	r.Counter("gen_ai.tokens.used", 100, "type", "input")
	r.Counter("gen_ai.tokens.used", 50, "type", "output")
	r.Counter("other.counter", 1)

	tokens := r.Counters("gen_ai.tokens.used")
	if len(tokens) != 2 {
		t.Fatalf("expected 2 token counters, got %d", len(tokens))
	}
	if tokens[0].Delta != 100 || tokens[1].Delta != 50 {
		t.Errorf("token deltas = %d, %d; want 100, 50", tokens[0].Delta, tokens[1].Delta)
	}
	if got := len(r.Counters("missing")); got != 0 {
		t.Errorf("Counters(missing) = %d, want 0", got)
	}
}

func TestRecordingMetrics_ResetClearsAll(t *testing.T) {
	r := NewRecordingMetrics()
	r.Counter("c", 1)
	r.Gauge("g", 1.0)
	r.Histogram("h", time.Second)
	r.Reset()

	if got := len(r.Counters("c")); got != 0 {
		t.Errorf("after Reset, counters not cleared: %d", got)
	}
	if got := len(r.gauges); got != 0 {
		t.Errorf("after Reset, gauges not cleared: %d", got)
	}
	if got := r.HistogramCount("h"); got != 0 {
		t.Errorf("after Reset, histograms not cleared: %d", got)
	}
}

func TestRecordingMetrics_ConcurrentSafe(t *testing.T) {
	r := NewRecordingMetrics()

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 64 {
				r.Counter("c", 1, "k", "v")
				r.Gauge("g", 1.0, "k", "v")
				r.Histogram("h", time.Millisecond, "k", "v")
			}
		})
	}
	wg.Wait()

	if got := r.CounterSum("c", "k", "v"); got != 16*64 {
		t.Errorf("CounterSum = %d, want %d", got, 16*64)
	}
	if got := r.HistogramCount("h", "k", "v"); got != 16*64 {
		t.Errorf("HistogramCount = %d, want %d", got, 16*64)
	}
}
