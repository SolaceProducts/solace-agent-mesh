// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"testing"
	"time"
)

func TestNoopMetrics(t *testing.T) {
	var m Metrics = NoopMetrics{}
	m.Counter("test.counter", 1)
	m.Counter("test.counter", 5, "component", "agent")
	m.Gauge("test.gauge", 42.0)
	m.Gauge("test.gauge", 0, "component", "agent")
	m.Histogram("test.histogram", 100*time.Millisecond)
	m.Histogram("test.histogram", time.Second, "op", "request")
}

func TestNoopTracer(t *testing.T) {
	var tr Tracer = NoopTracer{}
	span := tr.StartSpan("test-operation")
	span.SetAttribute("key", "value")
	span.SetError(nil)
	span.End()
}

func TestServicesMetricsTracer(t *testing.T) {
	svc := &runtimeServices{
		metrics: NoopMetrics{},
		tracer:  NoopTracer{},
	}
	if svc.Metrics() == nil {
		t.Error("expected non-nil metrics")
	}
	if svc.Tracer() == nil {
		t.Error("expected non-nil tracer")
	}
}
