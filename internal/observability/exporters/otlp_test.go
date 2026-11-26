// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestBuildMetricsEndpoint(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"http://localhost:4318", "http://localhost:4318/v1/metrics"},
		{"http://localhost:4318/", "http://localhost:4318/v1/metrics"},
		{"http://localhost:4318/v1/metrics", "http://localhost:4318/v1/metrics"},
		{"https://api.datadoghq.com/api/v2/otlp/v1/metrics", "https://api.datadoghq.com/api/v2/otlp/v1/metrics"},
		{"https://otlp.eu01.nr-data.net/otlp/v1/metrics", "https://otlp.eu01.nr-data.net/otlp/v1/metrics"},
		{"https://otlp.example.com:4318/", "https://otlp.example.com:4318/v1/metrics"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := buildMetricsEndpoint(tt.in)
			if got != tt.want {
				t.Errorf("buildMetricsEndpoint(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRequiresDeltaTemporality(t *testing.T) {
	tests := []struct {
		endpoint string
		want     bool
	}{
		{"https://api.datadoghq.com", true},
		{"https://api.datadoghq.eu", true},
		{"https://API.DATADOGHQ.COM", true},
		{"https://otlp.nr-data.net:4317", false},
		{"http://localhost:4318", false},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			if got := requiresDeltaTemporality(tt.endpoint); got != tt.want {
				t.Errorf("requiresDeltaTemporality(%q): got %v, want %v", tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestDeltaTemporalitySelector(t *testing.T) {
	tests := []struct {
		kind metric.InstrumentKind
		want metricdata.Temporality
	}{
		{metric.InstrumentKindCounter, metricdata.DeltaTemporality},
		{metric.InstrumentKindHistogram, metricdata.DeltaTemporality},
		{metric.InstrumentKindObservableCounter, metricdata.DeltaTemporality},
		{metric.InstrumentKindUpDownCounter, metricdata.CumulativeTemporality},
		{metric.InstrumentKindObservableUpDownCounter, metricdata.CumulativeTemporality},
		{metric.InstrumentKindObservableGauge, metricdata.CumulativeTemporality},
		{metric.InstrumentKindGauge, metricdata.CumulativeTemporality},
	}
	for _, tt := range tests {
		t.Run(tt.kind.String(), func(t *testing.T) {
			if got := deltaTemporalitySelector(tt.kind); got != tt.want {
				t.Errorf("kind=%v: got %v, want %v", tt.kind, got, tt.want)
			}
		})
	}
}

func TestMapHTTPCompression(t *testing.T) {
	logger, buf := newTestLogger()
	tests := []struct {
		in   string
		want otlpmetrichttp.Compression
		warn string
	}{
		{"none", otlpmetrichttp.NoCompression, ""},
		{"", otlpmetrichttp.NoCompression, ""},
		{"gzip", otlpmetrichttp.GzipCompression, ""},
		{"deflate", otlpmetrichttp.NoCompression, "not supported"},
		{"snappy", otlpmetrichttp.NoCompression, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			buf.Reset()
			got := mapHTTPCompression(tt.in, logger)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if tt.warn != "" && !strings.Contains(buf.String(), tt.warn) {
				t.Errorf("expected warning containing %q, got: %s", tt.warn, buf.String())
			}
		})
	}
}

func TestMapGRPCCompression(t *testing.T) {
	logger, buf := newTestLogger()
	tests := []struct {
		in   string
		want string
		warn string
	}{
		{"none", "", ""},
		{"", "", ""},
		{"gzip", "gzip", ""},
		{"deflate", "", "deflate"},
		{"snappy", "", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			buf.Reset()
			got := mapGRPCCompression(tt.in, logger)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if tt.warn != "" && !strings.Contains(buf.String(), tt.warn) {
				t.Errorf("expected warning containing %q, got: %s", tt.warn, buf.String())
			}
		})
	}
}

func TestStripScheme(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"http://localhost:4317", "localhost:4317"},
		{"https://otlp.example.com:4317/", "otlp.example.com:4317"},
		{"localhost:4317", "localhost:4317"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := stripScheme(tt.in)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateOTLPMetricExporter_HTTPSucceeds(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg := ExporterConfig{
		Type:     "otlp",
		Endpoint: "http://localhost:4318",
		Protocol: "http",
		Timeout:  5,
	}
	exp, err := CreateOTLPMetricExporter(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter")
	}
	_ = exp.Shutdown(context.Background())
}

func TestCreateOTLPMetricExporter_GRPCSucceeds(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg := ExporterConfig{
		Type:     "otlp",
		Endpoint: "localhost:4317",
		Protocol: "grpc",
		Timeout:  5,
		Insecure: true,
	}
	exp, err := CreateOTLPMetricExporter(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter")
	}
	_ = exp.Shutdown(context.Background())
}

func TestCreateOTLPMetricExporter_BadProtocol(t *testing.T) {
	cfg := ExporterConfig{Protocol: "tcp"}
	if _, err := CreateOTLPMetricExporter(context.Background(), cfg, nil); err == nil {
		t.Error("expected error for unsupported protocol")
	}
}

func TestCreateOTLPMetricExporter_GRPCBadCertFile(t *testing.T) {
	logger, _ := newTestLogger()
	cfg := ExporterConfig{
		Type:            "otlp",
		Endpoint:        "localhost:4317",
		Protocol:        "grpc",
		Timeout:         5,
		CertificateFile: "/nonexistent/path/ca.pem",
	}
	if _, err := CreateOTLPMetricExporter(context.Background(), cfg, logger); err == nil {
		t.Error("expected error for missing certificate file")
	}
}

func TestNewPeriodicReader(t *testing.T) {
	cfg := ExporterConfig{
		Type:     "otlp",
		Endpoint: "http://localhost:4318",
		Protocol: "http",
		Timeout:  1,
	}
	logger, _ := newTestLogger()
	exp, err := CreateOTLPMetricExporter(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	r := NewPeriodicReader(exp)
	if r == nil {
		t.Fatal("expected non-nil reader")
	}
	_ = r.Shutdown(context.Background())
}
