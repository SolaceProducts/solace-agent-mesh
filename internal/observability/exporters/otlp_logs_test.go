// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
)

func TestBuildLogsEndpoint(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"http://localhost:4318", "http://localhost:4318/v1/logs"},
		{"http://localhost:4318/", "http://localhost:4318/v1/logs"},
		{"http://localhost:4318/v1/logs", "http://localhost:4318/v1/logs"},
		{"https://api.datadoghq.com/api/v2/otlp/v1/logs", "https://api.datadoghq.com/api/v2/otlp/v1/logs"},
		{"https://otlp.eu01.nr-data.net/otlp/v1/logs", "https://otlp.eu01.nr-data.net/otlp/v1/logs"},
		{"https://otlp.example.com:4318/", "https://otlp.example.com:4318/v1/logs"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := buildLogsEndpoint(tt.in); got != tt.want {
				t.Errorf("buildLogsEndpoint(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMapHTTPLogCompression(t *testing.T) {
	logger, buf := newTestLogger()
	tests := []struct {
		in   string
		want otlploghttp.Compression
		warn string
	}{
		{"none", otlploghttp.NoCompression, ""},
		{"", otlploghttp.NoCompression, ""},
		{"gzip", otlploghttp.GzipCompression, ""},
		{"deflate", otlploghttp.NoCompression, "not supported"},
		{"snappy", otlploghttp.NoCompression, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			buf.Reset()
			got := mapHTTPLogCompression(tt.in, logger)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if tt.warn != "" && !strings.Contains(buf.String(), tt.warn) {
				t.Errorf("expected warning containing %q, got: %s", tt.warn, buf.String())
			}
		})
	}
}

func TestCreateOTLPLogExporter_HTTPSucceeds(t *testing.T) {
	logger, _ := newTestLogger()
	cfg := ExporterConfig{
		Type:     "otlp",
		Endpoint: "http://localhost:4318",
		Protocol: "http",
		Timeout:  5,
	}
	exp, err := CreateOTLPLogExporter(t.Context(), cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter")
	}
	_ = exp.Shutdown(context.Background())
}

func TestCreateOTLPLogExporter_GRPCSucceeds(t *testing.T) {
	logger, _ := newTestLogger()
	cfg := ExporterConfig{
		Type:     "otlp",
		Endpoint: "localhost:4317",
		Protocol: "grpc",
		Timeout:  5,
		Insecure: true,
	}
	exp, err := CreateOTLPLogExporter(t.Context(), cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter")
	}
	_ = exp.Shutdown(context.Background())
}

func TestCreateOTLPLogExporter_GRPCWithGzip(t *testing.T) {
	logger, _ := newTestLogger()
	cfg := ExporterConfig{
		Type:        "otlp",
		Endpoint:    "localhost:4317",
		Protocol:    "grpc",
		Timeout:     5,
		Insecure:    true,
		Compression: CompressionGzip,
	}
	exp, err := CreateOTLPLogExporter(t.Context(), cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = exp.Shutdown(context.Background())
}

func TestCreateOTLPLogExporter_HeadersAndCompression(t *testing.T) {
	logger, _ := newTestLogger()
	cfg := ExporterConfig{
		Type:        "otlp",
		Endpoint:    "http://localhost:4318",
		Protocol:    "http",
		Timeout:     5,
		Compression: CompressionGzip,
		Headers:     map[string]string{"X-Api-Key": "abc"},
	}
	exp, err := CreateOTLPLogExporter(t.Context(), cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = exp.Shutdown(context.Background())
}

func TestCreateOTLPLogExporter_BadProtocol(t *testing.T) {
	cfg := ExporterConfig{Protocol: "tcp"}
	if _, err := CreateOTLPLogExporter(t.Context(), cfg, nil); err == nil {
		t.Error("expected error for unsupported protocol")
	}
}

func TestCreateOTLPLogExporter_GRPCBadCertFile(t *testing.T) {
	logger, _ := newTestLogger()
	cfg := ExporterConfig{
		Type:            "otlp",
		Endpoint:        "localhost:4317",
		Protocol:        "grpc",
		Timeout:         5,
		CertificateFile: "/nonexistent/path/ca.pem",
	}
	if _, err := CreateOTLPLogExporter(t.Context(), cfg, logger); err == nil {
		t.Error("expected error for missing certificate file")
	}
}

func TestNewLogBatchProcessor(t *testing.T) {
	logger, _ := newTestLogger()
	cfg := ExporterConfig{
		Type:     "otlp",
		Endpoint: "http://localhost:4318",
		Protocol: "http",
		Timeout:  1,
	}
	exp, err := CreateOTLPLogExporter(t.Context(), cfg, logger)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	proc := NewLogBatchProcessor(exp)
	if proc == nil {
		t.Fatal("expected non-nil processor")
	}
	_ = proc.Shutdown(context.Background())
}
