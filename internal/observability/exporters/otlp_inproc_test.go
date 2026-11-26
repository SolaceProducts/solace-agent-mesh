// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters_test

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability/exporters"
)

type otlpHTTPRecorder struct {
	mu       sync.Mutex
	requests [][]byte
	headers  []http.Header
	server   *httptest.Server
}

func newOTLPHTTPRecorder() *otlpHTTPRecorder {
	r := &otlpHTTPRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, req *http.Request) {
		body, err := readBody(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.requests = append(r.requests, body)
		r.headers = append(r.headers, req.Header.Clone())
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	})
	r.server = httptest.NewServer(mux)
	return r
}

func readBody(req *http.Request) ([]byte, error) {
	defer func() { _ = req.Body.Close() }()
	if req.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(req.Body)
		if err != nil {
			return nil, err
		}
		defer func() { _ = gz.Close() }()
		return io.ReadAll(gz)
	}
	return io.ReadAll(req.Body)
}

func (r *otlpHTTPRecorder) Close() {
	r.server.Close()
}

func (r *otlpHTTPRecorder) Snapshot() ([][]byte, []http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reqs := append([][]byte{}, r.requests...)
	hdrs := append([]http.Header{}, r.headers...)
	return reqs, hdrs
}

func (r *otlpHTTPRecorder) URL() string {
	return r.server.URL
}

func TestOTLPHTTP_EndToEnd(t *testing.T) {
	rec := newOTLPHTTPRecorder()
	defer rec.Close()

	cfg := exporters.ExporterConfig{
		Type:     "otlp",
		Endpoint: rec.URL(),
		Protocol: "http",
		Timeout:  5,
	}
	exp, err := exporters.CreateOTLPMetricExporter(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("create exporter: %v", err)
	}
	reader := exporters.NewPeriodicReader(exp)

	reg, err := observability.NewRegistry(
		observability.Config{Enabled: true, MetricPrefix: "test"},
		nil,
		observability.WithAdditionalReader(reader),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}

	reg.Counter("gen_ai.tokens.used", 7, "gen_ai.token.type", "input")
	reg.Histogram("operation.duration", 100*time.Millisecond, "type", "agent")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := reg.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	requests, _ := rec.Snapshot()
	if len(requests) == 0 {
		t.Fatal("expected at least one OTLP HTTP request, got 0")
	}
}

func TestOTLPHTTP_GzipCompression(t *testing.T) {
	rec := newOTLPHTTPRecorder()
	defer rec.Close()

	cfg := exporters.ExporterConfig{
		Type:        "otlp",
		Endpoint:    rec.URL(),
		Protocol:    "http",
		Timeout:     5,
		Compression: exporters.CompressionGzip,
	}
	exp, err := exporters.CreateOTLPMetricExporter(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("create exporter: %v", err)
	}
	reader := exporters.NewPeriodicReader(exp)

	reg, err := observability.NewRegistry(
		observability.Config{Enabled: true, MetricPrefix: "test"},
		nil,
		observability.WithAdditionalReader(reader),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	reg.Counter("gen_ai.tokens.used", 1)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := reg.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	_, headers := rec.Snapshot()
	if len(headers) == 0 {
		t.Fatal("expected at least one request")
	}
	if headers[0].Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %q", headers[0].Get("Content-Encoding"))
	}
}

func TestOTLPHTTP_HeadersPropagate(t *testing.T) {
	rec := newOTLPHTTPRecorder()
	defer rec.Close()

	cfg := exporters.ExporterConfig{
		Type:     "otlp",
		Endpoint: rec.URL(),
		Protocol: "http",
		Timeout:  5,
		Headers: map[string]string{
			"X-API-Key": "secret-token",
		},
	}
	exp, err := exporters.CreateOTLPMetricExporter(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("create exporter: %v", err)
	}
	reader := exporters.NewPeriodicReader(exp)

	reg, err := observability.NewRegistry(
		observability.Config{Enabled: true, MetricPrefix: "test"},
		nil,
		observability.WithAdditionalReader(reader),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	reg.Counter("gen_ai.tokens.used", 1)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := reg.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	_, headers := rec.Snapshot()
	if len(headers) == 0 {
		t.Fatal("expected at least one request")
	}
	if got := headers[0].Get("X-API-Key"); got != "secret-token" {
		t.Errorf("expected X-API-Key header, got %q", got)
	}
}

func mkOTLPReader(t *testing.T, url string) sdkmetric.Reader {
	t.Helper()
	cfg := exporters.ExporterConfig{
		Type:     "otlp",
		Endpoint: url,
		Protocol: "http",
		Timeout:  5,
	}
	exp, err := exporters.CreateOTLPMetricExporter(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("create exporter: %v", err)
	}
	return exporters.NewPeriodicReader(exp)
}

func TestOTLPHTTP_MultipleExporters(t *testing.T) {
	rec1 := newOTLPHTTPRecorder()
	defer rec1.Close()
	rec2 := newOTLPHTTPRecorder()
	defer rec2.Close()

	r1 := mkOTLPReader(t, rec1.URL())
	r2 := mkOTLPReader(t, rec2.URL())

	reg, err := observability.NewRegistry(
		observability.Config{Enabled: true, MetricPrefix: "test"},
		nil,
		observability.WithAdditionalReader(r1),
		observability.WithAdditionalReader(r2),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	reg.Counter("gen_ai.tokens.used", 3)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := reg.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if reqs, _ := rec1.Snapshot(); len(reqs) == 0 {
		t.Error("recorder 1 received no requests")
	}
	if reqs, _ := rec2.Snapshot(); len(reqs) == 0 {
		t.Error("recorder 2 received no requests")
	}
}
