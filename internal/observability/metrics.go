// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
)

type DistributionMetricConfig struct {
	Buckets []float64

	ExcludeLabels []string
}

type ValueMetricConfig struct {
	ExcludeLabels []string
}

type Config struct {
	Enabled bool

	MetricPrefix string

	Path string

	DistributionMetrics map[string]DistributionMetricConfig

	ValueMetrics map[string]ValueMetricConfig
}

func DefaultDistributionMetrics() map[string]DistributionMetricConfig {
	return map[string]DistributionMetricConfig{
		"outbound.request.duration": {
			Buckets:       []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
			ExcludeLabels: nil,
		},
		"gen_ai.client.operation.duration": {
			Buckets:       []float64{0.5, 1.0, 2.0, 5.0, 10.0, 20.0, 30.0, 60.0, 120.0},
			ExcludeLabels: []string{"tokens"},
		},
		"gen_ai.client.operation.ttft.duration": {
			Buckets:       []float64{0.1, 0.25, 0.5, 1.0, 3.0, 5.0, 10.0, 20.0, 30.0},
			ExcludeLabels: nil,
		},
		"entrypoint.duration": {
			Buckets:       []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0},
			ExcludeLabels: nil,
		},
		"entrypoint.ttfb.duration": {
			Buckets:       []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
			ExcludeLabels: nil,
		},
		"operation.duration": {
			Buckets:       []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0},
			ExcludeLabels: nil,
		},
	}
}

func DefaultValueMetrics() map[string]ValueMetricConfig {
	return map[string]ValueMetricConfig{
		"gen_ai.tokens.used": {
			ExcludeLabels: []string{"owner.id"},
		},
		"gen_ai.cost.total": {
			ExcludeLabels: []string{"owner.id"},
		},
	}
}

func ValidateConfig(cfg *Config) error {
	for name, dc := range cfg.DistributionMetrics {
		if isDisabled(dc.ExcludeLabels) {
			continue
		}
		if len(dc.Buckets) > 0 {
			if err := validateBuckets(dc.Buckets, name); err != nil {
				return err
			}
		} else {
			defaults := DefaultDistributionMetrics()
			if _, isBuiltin := defaults[name]; !isBuiltin {
				return fmt.Errorf("distribution_metrics.%s: 'buckets' is required for custom metrics; "+
					"use exclude_labels: [\"*\"] to disable this metric", name)
			}
		}
	}

	return nil
}

func validateBuckets(buckets []float64, metricName string) error {
	if len(buckets) == 0 {
		return fmt.Errorf("distribution_metrics.%s: buckets cannot be empty; "+
			"use exclude_labels: [\"*\"] to disable this metric", metricName)
	}
	for _, b := range buckets {
		if b <= 0 {
			return fmt.Errorf("distribution_metrics.%s: buckets must be positive", metricName)
		}
	}
	if !slices.IsSorted(buckets) {
		return fmt.Errorf("distribution_metrics.%s: buckets must be in ascending order", metricName)
	}
	return nil
}

func isDisabled(excludeLabels []string) bool {
	return len(excludeLabels) == 1 && excludeLabels[0] == "*"
}

func mergeWithDefaults(cfg *Config) {
	defaults := DefaultDistributionMetrics()
	if cfg.DistributionMetrics == nil {
		cfg.DistributionMetrics = defaults
	} else {
		for name, dc := range defaults {
			if _, userSet := cfg.DistributionMetrics[name]; !userSet {
				cfg.DistributionMetrics[name] = dc
			} else {
				user := cfg.DistributionMetrics[name]
				if len(user.Buckets) == 0 && !isDisabled(user.ExcludeLabels) {
					user.Buckets = dc.Buckets
				}
				cfg.DistributionMetrics[name] = user
			}
		}
	}

	valueDefaults := DefaultValueMetrics()
	if cfg.ValueMetrics == nil {
		cfg.ValueMetrics = valueDefaults
	} else {
		for name, vc := range valueDefaults {
			if _, userSet := cfg.ValueMetrics[name]; !userSet {
				cfg.ValueMetrics[name] = vc
			}
		}
	}
}

type Registry struct {
	cfg Config

	meterProvider *metric.MeterProvider
	meter         otelmetric.Meter

	mu         sync.RWMutex
	histograms map[string]otelmetric.Float64Histogram
	counters   map[string]otelmetric.Float64Counter
	gauges     map[string]otelmetric.Float64Gauge

	observables map[string]struct{}

	disabledMetrics map[string]bool

	excludedLabels map[string]map[string]bool

	promHandler http.Handler

	logger *slog.Logger
}

type RegistryOption func(*registryOptions)

type registryOptions struct {
	additionalReaders []metric.Reader
	resource          *resource.Resource
}

func WithAdditionalReader(r metric.Reader) RegistryOption {
	return func(o *registryOptions) {
		if r != nil {
			o.additionalReaders = append(o.additionalReaders, r)
		}
	}
}

func WithResource(r *resource.Resource) RegistryOption {
	return func(o *registryOptions) {
		if r != nil {
			o.resource = r
		}
	}
}

func NewRegistry(cfg Config, logger *slog.Logger, opts ...RegistryOption) (*Registry, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.MetricPrefix == "" {
		cfg.MetricPrefix = "sam"
	}
	if cfg.Path == "" {
		cfg.Path = "/metrics"
	}

	mergeWithDefaults(&cfg)

	if err := ValidateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("observability config: %w", err)
	}

	disabled := make(map[string]bool)
	excluded := make(map[string]map[string]bool)

	for name, dc := range cfg.DistributionMetrics {
		if isDisabled(dc.ExcludeLabels) {
			disabled[name] = true
			continue
		}
		if len(dc.ExcludeLabels) > 0 {
			s := make(map[string]bool, len(dc.ExcludeLabels))
			for _, l := range dc.ExcludeLabels {
				s[l] = true
			}
			excluded[name] = s
		}
	}
	for name, vc := range cfg.ValueMetrics {
		if isDisabled(vc.ExcludeLabels) {
			disabled[name] = true
			continue
		}
		if len(vc.ExcludeLabels) > 0 {
			s := make(map[string]bool, len(vc.ExcludeLabels))
			for _, l := range vc.ExcludeLabels {
				s[l] = true
			}
			excluded[name] = s
		}
	}

	var views []metric.View
	for name, dc := range cfg.DistributionMetrics {
		if isDisabled(dc.ExcludeLabels) || len(dc.Buckets) == 0 {
			continue
		}
		fullName := metricName(cfg.MetricPrefix, name)
		v := metric.NewView(
			metric.Instrument{Name: fullName},
			metric.Stream{
				Aggregation: metric.AggregationExplicitBucketHistogram{
					Boundaries: dc.Buckets,
				},
			},
		)
		views = append(views, v)
	}

	promReg := prometheus.NewRegistry()

	promExporter, err := otelprom.New(
		otelprom.WithRegisterer(promReg),
	)
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	regOpts := registryOptions{}
	for _, opt := range opts {
		opt(&regOpts)
	}

	mpOpts := []metric.Option{
		metric.WithReader(promExporter),
	}
	if regOpts.resource != nil {
		mpOpts = append(mpOpts, metric.WithResource(regOpts.resource))
	}
	for _, r := range regOpts.additionalReaders {
		mpOpts = append(mpOpts, metric.WithReader(r))
	}
	for _, v := range views {
		mpOpts = append(mpOpts, metric.WithView(v))
	}

	mp := metric.NewMeterProvider(mpOpts...)
	meterName := cfg.MetricPrefix
	m := mp.Meter(meterName)

	return &Registry{
		cfg:             cfg,
		meterProvider:   mp,
		meter:           m,
		histograms:      make(map[string]otelmetric.Float64Histogram),
		counters:        make(map[string]otelmetric.Float64Counter),
		gauges:          make(map[string]otelmetric.Float64Gauge),
		observables:     make(map[string]struct{}),
		disabledMetrics: disabled,
		excludedLabels:  excluded,
		promHandler:     promhttp.HandlerFor(promReg, promhttp.HandlerOpts{}),
		logger:          logger,
	}, nil
}

func (r *Registry) Shutdown(ctx context.Context) error {
	return r.meterProvider.Shutdown(ctx)
}

func (r *Registry) Handler() http.Handler {
	return r.promHandler
}

func (r *Registry) MetricPrefix() string {
	return r.cfg.MetricPrefix
}

func (r *Registry) Path() string {
	return r.cfg.Path
}

func (r *Registry) Counter(name string, delta int64, labels ...string) {
	if r.disabledMetrics[name] {
		return
	}
	attrs := r.filterAttrs(name, labelsToAttrs(labels))
	c := r.getOrCreateCounter(name)
	c.Add(context.Background(), float64(delta), otelmetric.WithAttributeSet(attrs))
}

func (r *Registry) Gauge(name string, value float64, labels ...string) {
	if r.disabledMetrics[name] {
		return
	}
	attrs := r.filterAttrs(name, labelsToAttrs(labels))
	g := r.getOrCreateGauge(name)
	g.Record(context.Background(), value, otelmetric.WithAttributeSet(attrs))
}

func (r *Registry) Histogram(name string, value time.Duration, labels ...string) {
	if r.disabledMetrics[name] {
		return
	}
	attrs := r.filterAttrs(name, labelsToAttrs(labels))
	h := r.getOrCreateHistogram(name)
	h.Record(context.Background(), value.Seconds(), otelmetric.WithAttributeSet(attrs))
}

func (r *Registry) filterAttrs(name string, attrs attribute.Set) attribute.Set {
	excl := r.excludedLabels[name]
	if len(excl) == 0 {
		return attrs
	}
	var filtered []attribute.KeyValue
	iter := attrs.Iter()
	for iter.Next() {
		kv := iter.Attribute()
		if !excl[string(kv.Key)] {
			filtered = append(filtered, kv)
		}
	}
	return attribute.NewSet(filtered...)
}

func metricName(prefix, name string) string {
	return prefix + "." + name
}

func (r *Registry) getOrCreateHistogram(name string) otelmetric.Float64Histogram {
	fullName := metricName(r.cfg.MetricPrefix, name)

	r.mu.RLock()
	h, ok := r.histograms[fullName]
	r.mu.RUnlock()
	if ok {
		return h
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if h, ok = r.histograms[fullName]; ok {
		return h
	}

	h, err := r.meter.Float64Histogram(fullName,
		otelmetric.WithUnit("s"),
		otelmetric.WithDescription(name+" duration"),
	)
	if err != nil {
		r.logger.Warn("failed to create histogram", "name", fullName, "error", err)
		h, _ = r.meter.Float64Histogram("noop_" + fullName)
	}

	r.histograms[fullName] = h
	return h
}

func (r *Registry) getOrCreateCounter(name string) otelmetric.Float64Counter {
	fullName := metricName(r.cfg.MetricPrefix, name)

	r.mu.RLock()
	c, ok := r.counters[fullName]
	r.mu.RUnlock()
	if ok {
		return c
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if c, ok = r.counters[fullName]; ok {
		return c
	}

	c, err := r.meter.Float64Counter(fullName,
		otelmetric.WithUnit("1"),
		otelmetric.WithDescription(name+" counter"),
	)
	if err != nil {
		r.logger.Warn("failed to create counter", "name", fullName, "error", err)
		c, _ = r.meter.Float64Counter("noop_" + fullName)
	}

	r.counters[fullName] = c
	return c
}

func (r *Registry) getOrCreateGauge(name string) otelmetric.Float64Gauge {
	fullName := metricName(r.cfg.MetricPrefix, name)

	r.mu.RLock()
	g, ok := r.gauges[fullName]
	r.mu.RUnlock()
	if ok {
		return g
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if g, ok = r.gauges[fullName]; ok {
		return g
	}

	g, err := r.meter.Float64Gauge(fullName,
		otelmetric.WithDescription(name+" gauge"),
	)
	if err != nil {
		r.logger.Warn("failed to create gauge", "name", fullName, "error", err)
		g, _ = r.meter.Float64Gauge("noop_" + fullName)
	}

	r.gauges[fullName] = g
	return g
}

func labelsToAttrs(labels []string) attribute.Set {
	kvs := make([]attribute.KeyValue, 0, len(labels)/2)
	for i := 0; i+1 < len(labels); i += 2 {
		kvs = append(kvs, attribute.String(labels[i], labels[i+1]))
	}
	return attribute.NewSet(kvs...)
}
