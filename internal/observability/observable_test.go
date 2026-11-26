// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func collectGauge(t *testing.T, r sdkmetric.Reader, name string) []metricdata.DataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			g, ok := m.Data.(metricdata.Gauge[float64])
			if !ok {
				t.Fatalf("metric %s is %T, want metricdata.Gauge[float64]", name, m.Data)
			}
			return g.DataPoints
		}
	}
	return nil
}

func attrString(dp metricdata.DataPoint[float64], key string) (string, bool) {
	v, ok := dp.Attributes.Value(attribute.Key(key))
	if !ok {
		return "", false
	}
	return v.AsString(), true
}

func newTestRegistry(t *testing.T) (*Registry, sdkmetric.Reader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	reg, err := NewRegistry(Config{Enabled: true, MetricPrefix: "test"}, nil, WithAdditionalReader(reader))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg, reader
}

func TestObservableGauge_EmitsCallbackValueWithLabels(t *testing.T) {
	reg, reader := newTestRegistry(t)

	reg.ObservableGauge("instance.up", func(context.Context) []Observation {
		return []Observation{NewObservation(1, "componentType", "AWE")}
	})

	dps := collectGauge(t, reader, "test.instance.up")
	if len(dps) != 1 {
		t.Fatalf("got %d data points, want 1", len(dps))
	}
	if dps[0].Value != 1 {
		t.Errorf("value = %v, want 1", dps[0].Value)
	}
	if v, ok := attrString(dps[0], "componentType"); !ok || v != "AWE" {
		t.Errorf("componentType = %q (present=%v), want \"AWE\"", v, ok)
	}
}

func TestObservableGauge_RecomputesEachCollect(t *testing.T) {
	reg, reader := newTestRegistry(t)

	var state atomic.Int64
	reg.ObservableGauge("broker.connection.state", func(context.Context) []Observation {
		return []Observation{{Value: float64(state.Load())}}
	})

	state.Store(1)
	if dps := collectGauge(t, reader, "test.broker.connection.state"); len(dps) != 1 || dps[0].Value != 1 {
		t.Fatalf("first collect: %+v, want value 1", dps)
	}

	state.Store(2)
	if dps := collectGauge(t, reader, "test.broker.connection.state"); len(dps) != 1 || dps[0].Value != 2 {
		t.Fatalf("second collect: %+v, want value 2", dps)
	}
}

func TestObservableGauge_MultiObservation(t *testing.T) {
	reg, reader := newTestRegistry(t)

	reg.ObservableGauge("component.count", func(context.Context) []Observation {
		return []Observation{
			NewObservation(1, "type", "agent", "name", "alpha"),
			NewObservation(1, "type", "agent", "name", "beta"),
		}
	})

	dps := collectGauge(t, reader, "test.component.count")
	if len(dps) != 2 {
		t.Fatalf("got %d data points, want 2", len(dps))
	}
	names := map[string]bool{}
	for _, dp := range dps {
		if dp.Value != 1 {
			t.Errorf("value = %v, want 1", dp.Value)
		}
		if n, ok := attrString(dp, "name"); ok {
			names[n] = true
		}
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("names = %v, want alpha+beta", names)
	}
}

func TestObservableConst(t *testing.T) {
	reg, reader := newTestRegistry(t)
	reg.ObservableConst("instance.up", 1, "componentType", "STR")

	dps := collectGauge(t, reader, "test.instance.up")
	if len(dps) != 1 || dps[0].Value != 1 {
		t.Fatalf("got %+v, want single value 1", dps)
	}
	if v, _ := attrString(dps[0], "componentType"); v != "STR" {
		t.Errorf("componentType = %q, want STR", v)
	}
}

func TestObservableValue_NilFn(t *testing.T) {
	reg, reader := newTestRegistry(t)
	reg.ObservableValue("broker.connection.state", nil)
	if dps := collectGauge(t, reader, "test.broker.connection.state"); dps != nil {
		t.Errorf("nil-fn ObservableValue emitted %d series, want 0", len(dps))
	}
}

func TestObservableValue(t *testing.T) {
	reg, reader := newTestRegistry(t)
	var n atomic.Int64
	reg.ObservableValue("broker.connection.state", func() float64 { return float64(n.Load()) })

	n.Store(2)
	dps := collectGauge(t, reader, "test.broker.connection.state")
	if len(dps) != 1 || dps[0].Value != 2 {
		t.Fatalf("got %+v, want single value 2", dps)
	}
}

func TestObservableGauge_IdempotentRegistration(t *testing.T) {
	reg, reader := newTestRegistry(t)
	fn := func(context.Context) []Observation { return []Observation{{Value: 1}} }
	reg.ObservableGauge("instance.up", fn)
	reg.ObservableGauge("instance.up", fn)

	dps := collectGauge(t, reader, "test.instance.up")
	if len(dps) != 1 {
		t.Fatalf("got %d data points, want 1 (duplicate registration must be a no-op)", len(dps))
	}
}

func TestObservableGauge_Disabled(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	reg, err := NewRegistry(
		Config{
			Enabled:      true,
			MetricPrefix: "test",
			ValueMetrics: map[string]ValueMetricConfig{
				"instance.up": {ExcludeLabels: []string{"*"}},
			},
		},
		nil,
		WithAdditionalReader(reader),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	reg.ObservableConst("instance.up", 1)

	if dps := collectGauge(t, reader, "test.instance.up"); dps != nil {
		t.Errorf("disabled observable gauge emitted %d points, want none", len(dps))
	}
}

func TestObservableGauge_ExcludesLabels(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	reg, err := NewRegistry(
		Config{
			Enabled:      true,
			MetricPrefix: "test",
			ValueMetrics: map[string]ValueMetricConfig{
				"component.count": {ExcludeLabels: []string{"secret"}},
			},
		},
		nil,
		WithAdditionalReader(reader),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	reg.ObservableGauge("component.count", func(context.Context) []Observation {
		return []Observation{NewObservation(1, "name", "alpha", "secret", "shh")}
	})

	dps := collectGauge(t, reader, "test.component.count")
	if len(dps) != 1 {
		t.Fatalf("got %d data points, want 1", len(dps))
	}
	if _, ok := attrString(dps[0], "secret"); ok {
		t.Error("excluded label \"secret\" leaked into emission")
	}
	if v, _ := attrString(dps[0], "name"); v != "alpha" {
		t.Errorf("name = %q, want alpha (non-excluded label dropped)", v)
	}
}

func TestObservableGauge_NilCallback(t *testing.T) {
	reg, reader := newTestRegistry(t)
	reg.ObservableGauge("instance.up", nil)
	if dps := collectGauge(t, reader, "test.instance.up"); dps != nil {
		t.Errorf("nil-callback gauge emitted %d points, want none", len(dps))
	}
}
