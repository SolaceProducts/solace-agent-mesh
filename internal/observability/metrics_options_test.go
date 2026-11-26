// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type captureReader struct {
	sdkmetric.Reader
	collected []metricdata.ResourceMetrics
}

func newCaptureReader() *captureReader {
	r := sdkmetric.NewManualReader()
	return &captureReader{Reader: r}
}

func (c *captureReader) snapshot(ctx context.Context) (metricdata.ResourceMetrics, error) {
	var rm metricdata.ResourceMetrics
	if err := c.Collect(ctx, &rm); err != nil {
		return rm, err
	}
	c.collected = append(c.collected, rm)
	return rm, nil
}

func TestWithAdditionalReader_FlowsToBothReaders(t *testing.T) {
	extra := newCaptureReader()

	reg, err := NewRegistry(
		Config{Enabled: true, MetricPrefix: "test"},
		nil,
		WithAdditionalReader(extra.Reader),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}

	reg.Counter("gen_ai.tokens.used", 42, "gen_ai.token.type", "input")
	reg.Histogram("operation.duration", 250*time.Millisecond, "type", "agent")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rm, err := extra.snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	var foundCounter, foundHist bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "test.gen_ai.tokens.used":
				foundCounter = true
			case "test.operation.duration":
				foundHist = true
			}
		}
	}
	if !foundCounter {
		t.Error("counter not exported to additional reader")
	}
	if !foundHist {
		t.Error("histogram not exported to additional reader")
	}

	body := scrapeMetrics(t, reg)
	if body == "" {
		t.Error("Prometheus handler returned empty body")
	}
}

func TestWithAdditionalReader_NilIsIgnored(t *testing.T) {
	reg, err := NewRegistry(
		Config{Enabled: true, MetricPrefix: "test"},
		nil,
		WithAdditionalReader(nil),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	reg.Counter("gen_ai.tokens.used", 1)
}

func TestRegistryShutdown_FlushesAdditionalReader(t *testing.T) {
	extra := sdkmetric.NewManualReader()
	reg, err := NewRegistry(
		Config{Enabled: true, MetricPrefix: "test"},
		nil,
		WithAdditionalReader(extra),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := reg.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
