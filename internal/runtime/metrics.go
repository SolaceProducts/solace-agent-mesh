// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"sync/atomic"
	"time"
)

type Metrics interface {
	Counter(name string, delta int64, labels ...string)

	Gauge(name string, value float64, labels ...string)

	Histogram(name string, value time.Duration, labels ...string)
}

type NoopMetrics struct{}

func (NoopMetrics) Counter(string, int64, ...string) {}

func (NoopMetrics) Gauge(string, float64, ...string) {}

func (NoopMetrics) Histogram(string, time.Duration, ...string) {}

var _ Metrics = NoopMetrics{}

func MetricsOrNoop(m Metrics) Metrics {
	if m == nil {
		return NoopMetrics{}
	}
	return m
}

type metricsHolder struct{ m Metrics }

var globalMetrics atomic.Pointer[metricsHolder]

func Init(m Metrics) {
	if m == nil {
		m = NoopMetrics{}
	}
	globalMetrics.Store(&metricsHolder{m: m})
}

func MetricProvider() Metrics {
	if h := globalMetrics.Load(); h != nil {
		return h.m
	}
	return NoopMetrics{}
}

type Tracer interface {
	StartSpan(name string) Span
}

type Span interface {
	SetAttribute(key, value string)

	SetError(err error)

	End()
}

type NoopTracer struct{}

func (NoopTracer) StartSpan(string) Span { return NoopSpan{} }

var _ Tracer = NoopTracer{}

type NoopSpan struct{}

func (NoopSpan) SetAttribute(string, string) {}

func (NoopSpan) SetError(error) {}

func (NoopSpan) End() {}

var _ Span = NoopSpan{}
