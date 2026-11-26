// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRegistryDisabledReturnsNil(t *testing.T) {
	reg, err := NewRegistry(Config{Enabled: false}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg != nil {
		t.Fatal("expected nil registry when disabled")
	}
}

func TestRegistryRecordsHistogram(t *testing.T) {
	reg := mustNewRegistry(t, Config{Enabled: true, MetricPrefix: "test"})

	reg.Histogram("operation.duration", 500*time.Millisecond,
		"type", "agent",
		"component.name", "TestAgent",
	)

	body := scrapeMetrics(t, reg)

	if !strings.Contains(body, "test_operation_duration") {
		t.Errorf("expected histogram metric in output, got:\n%s", body)
	}
	if !strings.Contains(body, `component_name="TestAgent"`) {
		t.Errorf("expected component_name label, got:\n%s", body)
	}
	if !strings.Contains(body, `type="agent"`) {
		t.Errorf("expected type label, got:\n%s", body)
	}
}

func TestRegistryRecordsCounter(t *testing.T) {
	reg := mustNewRegistry(t, Config{Enabled: true, MetricPrefix: "test"})

	reg.Counter("gen_ai.tokens.used", 150,
		"gen_ai.request.model", "claude-sonnet",
		"gen_ai.token.type", "input",
	)

	body := scrapeMetrics(t, reg)

	if !strings.Contains(body, "test_gen_ai_tokens_used") {
		t.Errorf("expected counter metric, got:\n%s", body)
	}
	if !strings.Contains(body, `gen_ai_token_type="input"`) {
		t.Errorf("expected token type label, got:\n%s", body)
	}
}

func TestRegistryRecordsGauge(t *testing.T) {
	reg := mustNewRegistry(t, Config{Enabled: true, MetricPrefix: "test"})

	reg.Gauge("active_connections", 42,
		"entrypoint.name", "main",
	)

	body := scrapeMetrics(t, reg)

	if !strings.Contains(body, "test_active_connections") {
		t.Errorf("expected gauge metric, got:\n%s", body)
	}
	if !strings.Contains(body, "42") {
		t.Errorf("expected value 42, got:\n%s", body)
	}
}

func TestRegistryGaugeUsesLastValueNotDelta(t *testing.T) {
	reg := mustNewRegistry(t, Config{Enabled: true, MetricPrefix: "test"})

	reg.Gauge("invocations.inflight", 5)
	reg.Gauge("invocations.inflight", 3)
	if got := gaugeValue(t, scrapeMetrics(t, reg), "test_invocations_inflight"); got != 3 {
		t.Fatalf("gauge = %v, want 3 (last value); 8 means the delta accumulation bug is back", got)
	}

	reg.Gauge("invocations.inflight", 1)
	if got := gaugeValue(t, scrapeMetrics(t, reg), "test_invocations_inflight"); got != 1 {
		t.Fatalf("gauge = %v, want 1 — the gauge must be able to fall, not just climb", got)
	}
}

func TestRegistryGaugeLastValuePerAttributeSet(t *testing.T) {
	reg := mustNewRegistry(t, Config{Enabled: true, MetricPrefix: "test"})

	reg.Gauge("channel.depth", 5, "queue", "a")
	reg.Gauge("channel.depth", 9, "queue", "b")
	reg.Gauge("channel.depth", 3, "queue", "a")

	body := scrapeMetrics(t, reg)
	if got := labeledGaugeValue(t, body, "test_channel_depth", `queue="a"`); got != 3 {
		t.Errorf("series queue=a = %v, want 3 (last value for that series)", got)
	}
	if got := labeledGaugeValue(t, body, "test_channel_depth", `queue="b"`); got != 9 {
		t.Errorf("series queue=b = %v, want 9 (unaffected by series a)", got)
	}
	if !strings.Contains(body, "# TYPE test_channel_depth gauge") {
		t.Errorf("expected Prometheus gauge type (not counter/_total), got:\n%s", body)
	}
}

func labeledGaugeValue(t *testing.T, body, name, label string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") || !strings.HasPrefix(line, name) || !strings.Contains(line, label) {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse gauge value from %q: %v", line, err)
		}
		return v
	}
	t.Fatalf("metric %q with label %q not found in:\n%s", name, label, body)
	return 0
}

func gaugeValue(t *testing.T, body, name string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") || !strings.HasPrefix(line, name) {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse gauge value from %q: %v", line, err)
		}
		return v
	}
	t.Fatalf("metric %q not found in:\n%s", name, body)
	return 0
}

func TestRegistryDefaultMetricPrefix(t *testing.T) {
	reg := mustNewRegistry(t, Config{Enabled: true})

	if reg.MetricPrefix() != "sam" {
		t.Errorf("expected default metric prefix 'sam', got %q", reg.MetricPrefix())
	}

	reg.Counter("requests", 1, "method", "GET")
	body := scrapeMetrics(t, reg)

	if !strings.Contains(body, "sam_requests") {
		t.Errorf("expected 'sam_' prefix, got:\n%s", body)
	}
}

func TestRegistryDefaultPath(t *testing.T) {
	reg := mustNewRegistry(t, Config{Enabled: true})
	if reg.Path() != "/metrics" {
		t.Errorf("expected default path '/metrics', got %q", reg.Path())
	}
}

func TestRegistryCustomPath(t *testing.T) {
	reg := mustNewRegistry(t, Config{Enabled: true, Path: "/prometheus/metrics"})
	if reg.Path() != "/prometheus/metrics" {
		t.Errorf("expected custom path, got %q", reg.Path())
	}
}

func TestRegistryReusesExistingMetrics(t *testing.T) {
	reg := mustNewRegistry(t, Config{Enabled: true, MetricPrefix: "test"})

	reg.Counter("requests", 1, "method", "GET")
	reg.Counter("requests", 1, "method", "GET")

	body := scrapeMetrics(t, reg)

	if !strings.Contains(body, "2") {
		t.Errorf("expected counter value 2 after two increments, got:\n%s", body)
	}
}

func TestDefaultDistributionMetrics(t *testing.T) {
	defaults := DefaultDistributionMetrics()

	tests := []struct {
		name        string
		expectedLen int
	}{
		{"entrypoint.ttfb.duration", 10},
		{"gen_ai.client.operation.duration", 9},
		{"entrypoint.duration", 11},
		{"outbound.request.duration", 10},
		{"operation.duration", 12},
		{"gen_ai.client.operation.ttft.duration", 9},
	}

	for _, tt := range tests {
		dc, ok := defaults[tt.name]
		if !ok {
			t.Errorf("missing default config for %q", tt.name)
			continue
		}
		if len(dc.Buckets) != tt.expectedLen {
			t.Errorf("DefaultDistributionMetrics[%q]: got %d buckets, want %d", tt.name, len(dc.Buckets), tt.expectedLen)
		}
	}
}

func TestCustomBucketsInPrometheusOutput(t *testing.T) {
	reg := mustNewRegistry(t, Config{
		Enabled:      true,
		MetricPrefix: "test",
		DistributionMetrics: map[string]DistributionMetricConfig{
			"entrypoint.duration": {Buckets: []float64{0.1, 0.5, 1.0, 5.0}},
		},
	})

	reg.Histogram("entrypoint.duration", 200*time.Millisecond,
		"entrypoint.name", "main",
	)

	body := scrapeMetrics(t, reg)

	if !strings.Contains(body, `le="0.1"`) {
		t.Errorf("expected custom bucket le=0.1 in output, got:\n%s", body)
	}
	if !strings.Contains(body, `le="5"`) {
		t.Errorf("expected custom bucket le=5 in output, got:\n%s", body)
	}
}

func TestLabelExclusion(t *testing.T) {
	reg := mustNewRegistry(t, Config{
		Enabled:      true,
		MetricPrefix: "test",
		DistributionMetrics: map[string]DistributionMetricConfig{
			"operation.duration": {ExcludeLabels: []string{"tokens"}},
		},
	})

	reg.Histogram("operation.duration", 100*time.Millisecond,
		"type", "agent",
		"tokens", "5000",
	)

	body := scrapeMetrics(t, reg)

	if strings.Contains(body, "tokens") {
		t.Errorf("expected 'tokens' label to be excluded, got:\n%s", body)
	}
	if !strings.Contains(body, `type="agent"`) {
		t.Errorf("expected 'type' label to be present, got:\n%s", body)
	}
}

func TestDisabledMetric(t *testing.T) {
	reg := mustNewRegistry(t, Config{
		Enabled:      true,
		MetricPrefix: "test",
		DistributionMetrics: map[string]DistributionMetricConfig{
			"entrypoint.duration": {ExcludeLabels: []string{"*"}},
		},
	})

	reg.Histogram("entrypoint.duration", 100*time.Millisecond,
		"entrypoint.name", "main",
	)

	body := scrapeMetrics(t, reg)

	if strings.Contains(body, "entrypoint_duration") {
		t.Errorf("expected disabled metric to not appear, got:\n%s", body)
	}
}

func TestDisabledValueMetric(t *testing.T) {
	reg := mustNewRegistry(t, Config{
		Enabled:      true,
		MetricPrefix: "test",
		ValueMetrics: map[string]ValueMetricConfig{
			"gen_ai.tokens.used": {ExcludeLabels: []string{"*"}},
		},
	})

	reg.Counter("gen_ai.tokens.used", 100,
		"gen_ai.request.model", "gpt-4",
	)

	body := scrapeMetrics(t, reg)

	if strings.Contains(body, "gen_ai_tokens_used") {
		t.Errorf("expected disabled counter to not appear, got:\n%s", body)
	}
}

func TestValueMetric_DefaultExcludesOwnerID(t *testing.T) {
	reg := mustNewRegistry(t, Config{
		Enabled:      true,
		MetricPrefix: "test",
	})

	reg.Counter("gen_ai.tokens.used", 100,
		"gen_ai.request.model", "gpt-4",
		"owner.id", "user-123",
	)

	body := scrapeMetrics(t, reg)

	if strings.Contains(body, "user-123") || strings.Contains(body, "owner_id") {
		t.Errorf("owner.id must be excluded by default (PII); got:\n%s", body)
	}
	if !strings.Contains(body, `gen_ai_request_model="gpt-4"`) {
		t.Errorf("expected model label retained, got:\n%s", body)
	}
}

func TestValueMetric_OwnerIDEmittedWhenOverridden(t *testing.T) {
	reg := mustNewRegistry(t, Config{
		Enabled:      true,
		MetricPrefix: "test",
		ValueMetrics: map[string]ValueMetricConfig{
			"gen_ai.tokens.used": {ExcludeLabels: []string{}},
		},
	})

	reg.Counter("gen_ai.tokens.used", 100,
		"gen_ai.request.model", "gpt-4",
		"owner.id", "user-123",
	)

	body := scrapeMetrics(t, reg)

	if !strings.Contains(body, `owner_id="user-123"`) {
		t.Errorf("owner.id should be emitted when exclude_labels overridden to []; got:\n%s", body)
	}
}

func TestValidateConfigBadBuckets(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			"non-ascending",
			Config{
				Enabled: true,
				DistributionMetrics: map[string]DistributionMetricConfig{
					"test.metric": {Buckets: []float64{5.0, 1.0, 10.0}},
				},
			},
		},
		{
			"negative bucket",
			Config{
				Enabled: true,
				DistributionMetrics: map[string]DistributionMetricConfig{
					"test.metric": {Buckets: []float64{-1.0, 0.5}},
				},
			},
		},
		{
			"custom metric without buckets",
			Config{
				Enabled: true,
				DistributionMetrics: map[string]DistributionMetricConfig{
					"my.custom.metric": {},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateConfig(&tt.cfg); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestValidateConfigDisabledSkipsValidation(t *testing.T) {
	cfg := Config{
		Enabled: true,
		DistributionMetrics: map[string]DistributionMetricConfig{
			"my.custom.metric": {ExcludeLabels: []string{"*"}},
		},
	}
	if err := ValidateConfig(&cfg); err != nil {
		t.Errorf("disabled metric should skip validation: %v", err)
	}
}

func TestMergeWithDefaults(t *testing.T) {
	cfg := Config{
		Enabled: true,
		DistributionMetrics: map[string]DistributionMetricConfig{
			"entrypoint.duration": {Buckets: []float64{0.1, 0.5, 1.0}},
		},
	}

	mergeWithDefaults(&cfg)

	gw := cfg.DistributionMetrics["entrypoint.duration"]
	if len(gw.Buckets) != 3 {
		t.Errorf("expected 3 custom buckets, got %d", len(gw.Buckets))
	}

	op := cfg.DistributionMetrics["operation.duration"]
	if len(op.Buckets) != 12 {
		t.Errorf("expected 12 default buckets for operation.duration, got %d", len(op.Buckets))
	}

	tokenCfg, ok := cfg.ValueMetrics["gen_ai.tokens.used"]
	if !ok {
		t.Fatal("expected gen_ai.tokens.used in value metrics")
	}
	if len(tokenCfg.ExcludeLabels) != 1 || tokenCfg.ExcludeLabels[0] != "owner.id" {
		t.Errorf("expected default exclude_labels [owner.id], got %v", tokenCfg.ExcludeLabels)
	}
}

func mustNewRegistry(t *testing.T, cfg Config) *Registry {
	t.Helper()
	reg, err := NewRegistry(cfg, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	return reg
}

func scrapeMetrics(t *testing.T, reg *Registry) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	reg.Handler().ServeHTTP(w, req)

	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics body: %v", err)
	}
	return string(b)
}
