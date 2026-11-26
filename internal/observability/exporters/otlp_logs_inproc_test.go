// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability/exporters"
)

type otlpHTTPLogRecorder struct {
	mu       sync.Mutex
	requests [][]byte
	headers  []http.Header
	server   *httptest.Server
}

func newOTLPHTTPLogRecorder() *otlpHTTPLogRecorder {
	r := &otlpHTTPLogRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", func(w http.ResponseWriter, req *http.Request) {
		body, err := readBodyLogs(req)
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

func readBodyLogs(req *http.Request) ([]byte, error) {
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

func (r *otlpHTTPLogRecorder) Close() {
	r.server.Close()
}

func (r *otlpHTTPLogRecorder) Snapshot() ([][]byte, []http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reqs := append([][]byte{}, r.requests...)
	hdrs := append([]http.Header{}, r.headers...)
	return reqs, hdrs
}

func (r *otlpHTTPLogRecorder) URL() string {
	return r.server.URL
}

func (r *otlpHTTPLogRecorder) allBodies() []byte {
	reqs, _ := r.Snapshot()
	var buf bytes.Buffer
	for _, b := range reqs {
		buf.Write(b)
	}
	return buf.Bytes()
}

func TestOTLPLogs_EndToEnd(t *testing.T) {
	rec := newOTLPHTTPLogRecorder()
	defer rec.Close()

	cfgs := []exporters.ExporterConfig{
		{
			Type:     "otlp",
			Endpoint: rec.URL(),
			Protocol: "http",
			Logs:     true,
			LogLevel: "INFO",
			Timeout:  5,
		},
	}
	lp, err := exporters.NewLogProvider(t.Context(), cfgs, nil, nil)
	if err != nil {
		t.Fatalf("NewLogProvider: %v", err)
	}
	defer func() { _ = lp.Stop(context.Background()) }()

	logger := slog.New(samlog.NewMultiHandler(lp.Handlers()...))
	logger.Info("hello-otlp", "key", "value")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := lp.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	body := rec.allBodies()
	if len(body) == 0 {
		t.Fatal("expected at least one OTLP HTTP log request, got 0")
	}
	if !bytes.Contains(body, []byte("hello-otlp")) {
		t.Errorf("expected log body to contain 'hello-otlp'; got bytes: %x", body)
	}
}

func TestOTLPLogs_PerExporterLevelFiltering(t *testing.T) {
	rec1 := newOTLPHTTPLogRecorder()
	defer rec1.Close()
	rec2 := newOTLPHTTPLogRecorder()
	defer rec2.Close()

	cfgs := []exporters.ExporterConfig{
		{
			Type: "otlp", Endpoint: rec1.URL(), Protocol: "http",
			Logs: true, LogLevel: "WARNING", Timeout: 5,
		},
		{
			Type: "otlp", Endpoint: rec2.URL(), Protocol: "http",
			Logs: true, LogLevel: "ERROR", Timeout: 5,
		},
	}
	lp, err := exporters.NewLogProvider(t.Context(), cfgs, nil, nil)
	if err != nil {
		t.Fatalf("NewLogProvider: %v", err)
	}

	logger := slog.New(samlog.NewMultiHandler(lp.Handlers()...))
	logger.Info("info-line")
	logger.Warn("warn-line")
	logger.Error("error-line")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := lp.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	body1 := string(rec1.allBodies())
	body2 := string(rec2.allBodies())

	if strings.Contains(body1, "info-line") {
		t.Errorf("WARNING exporter must filter INFO records; got: %s", body1)
	}
	if !strings.Contains(body1, "warn-line") || !strings.Contains(body1, "error-line") {
		t.Errorf("WARNING exporter missing WARN/ERROR records; got: %s", body1)
	}
	if strings.Contains(body2, "info-line") || strings.Contains(body2, "warn-line") {
		t.Errorf("ERROR exporter must filter INFO/WARN records; got: %s", body2)
	}
	if !strings.Contains(body2, "error-line") {
		t.Errorf("ERROR exporter missing ERROR record; got: %s", body2)
	}
}

func TestOTLPLogs_AdditiveWithStderr(t *testing.T) {
	rec := newOTLPHTTPLogRecorder()
	defer rec.Close()

	cfgs := []exporters.ExporterConfig{
		{
			Type: "otlp", Endpoint: rec.URL(), Protocol: "http",
			Logs: true, LogLevel: "INFO", Timeout: 5,
		},
	}
	lp, err := exporters.NewLogProvider(t.Context(), cfgs, nil, nil)
	if err != nil {
		t.Fatalf("NewLogProvider: %v", err)
	}

	var stderrBuf bytes.Buffer
	stderrHandler := slog.NewTextHandler(&stderrBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	base := slog.New(stderrHandler)
	merged := samlog.AttachHandlers(base, lp.Handlers()...)
	merged.Info("dual-write-record")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := lp.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if !strings.Contains(stderrBuf.String(), "dual-write-record") {
		t.Errorf("stderr did not receive record (additive integration broken): %s", stderrBuf.String())
	}
	if !bytes.Contains(rec.allBodies(), []byte("dual-write-record")) {
		t.Error("OTLP exporter did not receive record")
	}
}

func TestOTLPLogs_GzipCompression(t *testing.T) {
	rec := newOTLPHTTPLogRecorder()
	defer rec.Close()

	cfgs := []exporters.ExporterConfig{
		{
			Type: "otlp", Endpoint: rec.URL(), Protocol: "http",
			Logs: true, LogLevel: "INFO", Timeout: 5,
			Compression: exporters.CompressionGzip,
		},
	}
	lp, err := exporters.NewLogProvider(t.Context(), cfgs, nil, nil)
	if err != nil {
		t.Fatalf("NewLogProvider: %v", err)
	}

	logger := slog.New(samlog.NewMultiHandler(lp.Handlers()...))
	logger.Info("compressed-record")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := lp.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	_, headers := rec.Snapshot()
	if len(headers) == 0 {
		t.Fatal("expected at least one request")
	}
	if got := headers[0].Get("Content-Encoding"); got != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %q", got)
	}
}

func TestOTLPLogs_HeadersPropagate(t *testing.T) {
	rec := newOTLPHTTPLogRecorder()
	defer rec.Close()

	cfgs := []exporters.ExporterConfig{
		{
			Type: "otlp", Endpoint: rec.URL(), Protocol: "http",
			Logs: true, LogLevel: "INFO", Timeout: 5,
			Headers: map[string]string{"X-Api-Key": "secret-token"},
		},
	}
	lp, err := exporters.NewLogProvider(t.Context(), cfgs, nil, nil)
	if err != nil {
		t.Fatalf("NewLogProvider: %v", err)
	}

	logger := slog.New(samlog.NewMultiHandler(lp.Handlers()...))
	logger.Info("headered-record")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := lp.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	_, headers := rec.Snapshot()
	if len(headers) == 0 {
		t.Fatal("expected at least one request")
	}
	if got := headers[0].Get("X-Api-Key"); got != "secret-token" {
		t.Errorf("expected X-Api-Key header on request, got %q", got)
	}
}

func TestOTLPLogs_ResourceTagsOnWire(t *testing.T) {
	rec := newOTLPHTTPLogRecorder()
	defer rec.Close()

	res := resource.NewSchemaless(
		semconv.ServiceNameKey.String("sam-awe-test"),
		semconv.DeploymentEnvironmentKey.String("staging-inproc"),
		semconv.ServiceVersionKey.String("v9.9.9-test"),
	)

	cfgs := []exporters.ExporterConfig{
		{
			Type: "otlp", Endpoint: rec.URL(), Protocol: "http",
			Logs: true, LogLevel: "INFO", Timeout: 5,
		},
	}
	lp, err := exporters.NewLogProvider(t.Context(), cfgs, res, nil)
	if err != nil {
		t.Fatalf("NewLogProvider: %v", err)
	}

	logger := slog.New(samlog.NewMultiHandler(lp.Handlers()...))
	logger.Info("tagged-record")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := lp.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	body := rec.allBodies()
	for _, want := range []string{
		"service.name", "sam-awe-test",
		"deployment.environment", "staging-inproc",
		"service.version", "v9.9.9-test",
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("OTLP log resource missing %q on the wire; got bytes: %x", want, body)
		}
	}
}
