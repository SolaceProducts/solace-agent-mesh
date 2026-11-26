// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"

	otelmetric "go.opentelemetry.io/otel/metric"
)

type Observation struct {
	Value  float64
	Labels []string
}

func NewObservation(value float64, labels ...string) Observation {
	return Observation{Value: value, Labels: labels}
}

type GaugeFunc func(context.Context) []Observation

func (r *Registry) ObservableGauge(name string, fn GaugeFunc) {
	if fn == nil || r.disabledMetrics[name] {
		return
	}

	fullName := metricName(r.cfg.MetricPrefix, name)

	r.mu.Lock()
	if _, exists := r.observables[name]; exists {
		r.mu.Unlock()
		return
	}
	r.observables[name] = struct{}{}
	r.mu.Unlock()

	unreserve := func() {
		r.mu.Lock()
		delete(r.observables, name)
		r.mu.Unlock()
	}

	inst, err := r.meter.Float64ObservableGauge(fullName,
		otelmetric.WithDescription(name+" gauge"),
	)
	if err != nil {
		r.logger.Error("failed to create observable gauge", "name", fullName, "error", err)
		unreserve()
		return
	}

	_, err = r.meter.RegisterCallback(func(ctx context.Context, o otelmetric.Observer) error {
		for _, ob := range fn(ctx) {
			attrs := r.filterAttrs(name, labelsToAttrs(ob.Labels))
			o.ObserveFloat64(inst, ob.Value, otelmetric.WithAttributeSet(attrs))
		}
		return nil
	}, inst)
	if err != nil {
		r.logger.Error("failed to register observable gauge callback", "name", fullName, "error", err)
		unreserve()
	}
}

func (r *Registry) ObservableConst(name string, value float64, labels ...string) {
	r.ObservableGauge(name, func(context.Context) []Observation {
		return []Observation{{Value: value, Labels: labels}}
	})
}

func (r *Registry) ObservableValue(name string, fn func() float64, labels ...string) {
	if fn == nil {
		return
	}
	r.ObservableGauge(name, func(context.Context) []Observation {
		return []Observation{{Value: fn(), Labels: labels}}
	})
}
