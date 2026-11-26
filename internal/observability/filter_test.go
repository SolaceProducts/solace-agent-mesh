// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestExcludeLabelsStar_BlocksAllReaders(t *testing.T) {
	extra := sdkmetric.NewManualReader()

	reg, err := NewRegistry(
		Config{
			Enabled:      true,
			MetricPrefix: "test",
			DistributionMetrics: map[string]DistributionMetricConfig{
				"entrypoint.duration": {ExcludeLabels: []string{"*"}},
			},
		},
		nil,
		WithAdditionalReader(extra),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	reg.Histogram("entrypoint.duration", 100*time.Millisecond, "entrypoint.name", "main")
	reg.Histogram("operation.duration", 50*time.Millisecond, "type", "agent")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var rm metricdata.ResourceMetrics
	if err := extra.Collect(ctx, &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var sawDisabled, sawAllowed bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "test.entrypoint.duration":
				sawDisabled = true
			case "test.operation.duration":
				sawAllowed = true
			}
		}
	}
	if sawDisabled {
		t.Error("disabled metric leaked to additional (OTLP) reader — exclude_labels: [*] must block all readers")
	}
	if !sawAllowed {
		t.Error("non-disabled metric did not flow to additional reader (isolation broken)")
	}

	body := scrapeMetrics(t, reg)
	if strings.Contains(body, "test_entrypoint_duration") {
		t.Errorf("disabled metric leaked to Prometheus scrape:\n%s", body)
	}
	if !strings.Contains(body, "test_operation_duration") {
		t.Errorf("non-disabled metric missing from Prometheus scrape:\n%s", body)
	}
}

func TestExcludeLabelsStar_AppliesToCounterAndGauge(t *testing.T) {
	reg := mustNewRegistry(t, Config{
		Enabled:      true,
		MetricPrefix: "test",
		ValueMetrics: map[string]ValueMetricConfig{
			"errors.total":   {ExcludeLabels: []string{"*"}},
			"queue.depth":    {ExcludeLabels: []string{"*"}},
			"requests.count": {},
		},
	})

	reg.Counter("errors.total", 1, "service", "x")
	reg.Gauge("queue.depth", 99, "queue", "tasks")
	reg.Counter("requests.count", 1, "method", "GET")

	body := scrapeMetrics(t, reg)
	if strings.Contains(body, "test_errors_total") {
		t.Errorf("disabled counter leaked: %s", body)
	}
	if strings.Contains(body, "test_queue_depth") {
		t.Errorf("disabled gauge leaked: %s", body)
	}
	if !strings.Contains(body, "test_requests_count") {
		t.Errorf("non-disabled counter missing: %s", body)
	}
}

func TestExcludeLabels_MultipleNamedLabelsStripped(t *testing.T) {
	reg := mustNewRegistry(t, Config{
		Enabled:      true,
		MetricPrefix: "test",
		DistributionMetrics: map[string]DistributionMetricConfig{
			"entrypoint.duration": {ExcludeLabels: []string{"tokens", "owner.id"}},
		},
	})

	reg.Histogram("entrypoint.duration", 50*time.Millisecond,
		"entrypoint.name", "main",
		"tokens", "9999",
		"owner.id", "user-1",
		"route", "/api/v1/message",
	)

	body := scrapeMetrics(t, reg)

	if strings.Contains(body, `tokens=`) {
		t.Errorf("tokens label should be excluded: %s", body)
	}
	if strings.Contains(body, `owner_id=`) {
		t.Errorf("owner.id label should be excluded: %s", body)
	}
	if !strings.Contains(body, `entrypoint_name="main"`) {
		t.Errorf("entrypoint.name label should be present: %s", body)
	}
	if !strings.Contains(body, `route="/api/v1/message"`) {
		t.Errorf("route label should be present: %s", body)
	}
}

func TestExcludeLabels_OnlyAffectsTargetMetric(t *testing.T) {
	reg := mustNewRegistry(t, Config{
		Enabled:      true,
		MetricPrefix: "test",
		DistributionMetrics: map[string]DistributionMetricConfig{
			"entrypoint.duration": {ExcludeLabels: []string{"tokens"}},
		},
	})

	reg.Histogram("entrypoint.duration", 50*time.Millisecond,
		"entrypoint.name", "main",
		"tokens", "9999",
	)
	reg.Histogram("operation.duration", 50*time.Millisecond,
		"type", "agent",
		"tokens", "1234",
	)

	body := scrapeMetrics(t, reg)

	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "test_entrypoint_duration") && strings.Contains(line, "tokens=") {
			t.Errorf("tokens leaked into entrypoint.duration line: %s", line)
		}
	}
	foundOpWithTokens := false
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "test_operation_duration") && strings.Contains(line, `tokens="1234"`) {
			foundOpWithTokens = true
			break
		}
	}
	if !foundOpWithTokens {
		t.Errorf("operation.duration lost its tokens label (filter bled across metrics):\n%s", body)
	}
}
