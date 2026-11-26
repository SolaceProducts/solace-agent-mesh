// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"sync"
	"time"
)

type RecordingMetrics struct {
	mu         sync.Mutex
	counters   []RecordedCounter
	gauges     []RecordedGauge
	histograms []RecordedHistogram
}

type RecordedCounter struct {
	Name   string
	Delta  int64
	Labels map[string]string
}

type RecordedGauge struct {
	Name   string
	Value  float64
	Labels map[string]string
}

type RecordedHistogram struct {
	Name     string
	Duration time.Duration
	Labels   map[string]string
}

func NewRecordingMetrics() *RecordingMetrics {
	return &RecordingMetrics{}
}

func (r *RecordingMetrics) Counter(name string, delta int64, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = append(r.counters, RecordedCounter{
		Name:   name,
		Delta:  delta,
		Labels: pairsToMap(labels),
	})
}

func (r *RecordingMetrics) Gauge(name string, value float64, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges = append(r.gauges, RecordedGauge{
		Name:   name,
		Value:  value,
		Labels: pairsToMap(labels),
	})
}

func (r *RecordingMetrics) Histogram(name string, value time.Duration, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms = append(r.histograms, RecordedHistogram{
		Name:     name,
		Duration: value,
		Labels:   pairsToMap(labels),
	})
}

func (r *RecordingMetrics) HasHistogram(name string, labelPairs ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := pairsToMap(labelPairs)
	for _, h := range r.histograms {
		if h.Name == name && containsLabels(h.Labels, want) {
			return true
		}
	}
	return false
}

func (r *RecordingMetrics) HasCounter(name string, labelPairs ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := pairsToMap(labelPairs)
	for _, c := range r.counters {
		if c.Name == name && containsLabels(c.Labels, want) {
			return true
		}
	}
	return false
}

func (r *RecordingMetrics) CounterSum(name string, labelPairs ...string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := pairsToMap(labelPairs)
	var sum int64
	for _, c := range r.counters {
		if c.Name == name && containsLabels(c.Labels, want) {
			sum += c.Delta
		}
	}
	return sum
}

func (r *RecordingMetrics) HistogramCount(name string, labelPairs ...string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := pairsToMap(labelPairs)
	count := 0
	for _, h := range r.histograms {
		if h.Name == name && containsLabels(h.Labels, want) {
			count++
		}
	}
	return count
}

func (r *RecordingMetrics) Histograms(name string) []RecordedHistogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []RecordedHistogram
	for _, h := range r.histograms {
		if h.Name == name {
			result = append(result, h)
		}
	}
	return result
}

func (r *RecordingMetrics) Counters(name string) []RecordedCounter {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []RecordedCounter
	for _, c := range r.counters {
		if c.Name == name {
			result = append(result, c)
		}
	}
	return result
}

func (r *RecordingMetrics) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = nil
	r.gauges = nil
	r.histograms = nil
}

func pairsToMap(pairs []string) map[string]string {
	m := make(map[string]string, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func containsLabels(got, want map[string]string) bool {
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
