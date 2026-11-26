// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewLogProvider_NoLogsReturnsNil(t *testing.T) {
	logger, _ := newTestLogger()
	cfgs := []ExporterConfig{
		{Type: "otlp", Endpoint: "http://localhost:4318", Protocol: "http", Metrics: true, Timeout: 1},
	}
	lp, err := NewLogProvider(t.Context(), cfgs, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lp != nil {
		t.Errorf("expected nil LogProvider when no exporter has logs:true, got %+v", lp)
	}
}

func TestNewLogProvider_BuildsHandlers(t *testing.T) {
	logger, _ := newTestLogger()
	cfgs := []ExporterConfig{
		{Type: "otlp", Endpoint: "http://localhost:4318", Protocol: "http", Logs: true, LogLevel: "WARNING", Timeout: 1},
		{Type: "otlp", Endpoint: "http://localhost:4318", Protocol: "http", Logs: true, LogLevel: "ERROR", Timeout: 1},
	}
	lp, err := NewLogProvider(t.Context(), cfgs, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lp == nil {
		t.Fatal("expected LogProvider, got nil")
	}
	defer func() {
		_ = lp.Stop(context.Background())
	}()

	handlers := lp.Handlers()
	if len(handlers) != 2 {
		t.Fatalf("expected 2 handlers (one per exporter), got %d", len(handlers))
	}
	if lp.ExporterCount() != 2 {
		t.Errorf("ExporterCount: got %d, want 2", lp.ExporterCount())
	}

	ctx := context.Background()
	if h, ok := handlers[0].(*levelFilteringHandler); ok {
		if h.Enabled(ctx, slog.LevelInfo) {
			t.Error("handler[0] (WARNING) should not be enabled at INFO")
		}
		if !h.Enabled(ctx, slog.LevelWarn) {
			t.Error("handler[0] (WARNING) should be enabled at WARN")
		}
	} else {
		t.Errorf("expected *levelFilteringHandler, got %T", handlers[0])
	}
	if h, ok := handlers[1].(*levelFilteringHandler); ok {
		if h.Enabled(ctx, slog.LevelWarn) {
			t.Error("handler[1] (ERROR) should not be enabled at WARN")
		}
		if !h.Enabled(ctx, slog.LevelError) {
			t.Error("handler[1] (ERROR) should be enabled at ERROR")
		}
	} else {
		t.Errorf("expected *levelFilteringHandler, got %T", handlers[1])
	}
}

func TestLogProvider_StopIsIdempotent(t *testing.T) {
	logger, _ := newTestLogger()
	cfgs := []ExporterConfig{
		{Type: "otlp", Endpoint: "http://localhost:4318", Protocol: "http", Logs: true, Timeout: 1},
	}
	lp, err := NewLogProvider(t.Context(), cfgs, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := lp.Stop(context.Background()); err != nil {
		t.Errorf("first Stop returned: %v", err)
	}
	if err := lp.Stop(context.Background()); err != nil {
		t.Errorf("second Stop returned: %v", err)
	}
	if err := lp.Health(); err != nil {
		t.Errorf("Health after clean Stop: got %v, want nil", err)
	}
}

func TestLogProvider_LifecycleMethods(t *testing.T) {
	logger, _ := newTestLogger()
	cfgs := []ExporterConfig{
		{Type: "otlp", Endpoint: "http://localhost:4318", Protocol: "http", Logs: true, Timeout: 1},
	}
	lp, err := NewLogProvider(t.Context(), cfgs, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		_ = lp.Stop(context.Background())
	}()

	if err := lp.Init(context.Background(), nil); err != nil {
		t.Errorf("Init: got %v, want nil", err)
	}
	if err := lp.Start(context.Background()); err != nil {
		t.Errorf("Start: got %v, want nil", err)
	}
}

func TestLogProvider_NilSafety(t *testing.T) {
	var lp *LogProvider
	if lp.Handlers() != nil {
		t.Error("nil LogProvider Handlers() should return nil")
	}
	if lp.ExporterCount() != 0 {
		t.Error("nil LogProvider ExporterCount() should return 0")
	}
}

func TestNewLogProvider_SkipsBadEntriesAndKeepsValid(t *testing.T) {
	logger, _ := newTestLogger()
	cfgs := []ExporterConfig{
		{Type: "otlp", Endpoint: "localhost:4317", Protocol: "grpc", Logs: true, CertificateFile: "/no/such/cert.pem", Timeout: 1},
		{Type: "otlp", Endpoint: "http://localhost:4318", Protocol: "http", Logs: true, Timeout: 1},
	}
	lp, err := NewLogProvider(t.Context(), cfgs, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lp == nil {
		t.Fatal("expected non-nil LogProvider when at least one entry succeeds")
	}
	defer func() { _ = lp.Stop(context.Background()) }()
	if got := lp.ExporterCount(); got != 1 {
		t.Errorf("ExporterCount: got %d, want 1 (bad entry should be skipped)", got)
	}
	if got := len(lp.Handlers()); got != 1 {
		t.Errorf("Handlers(): got %d, want 1", got)
	}
}

func TestNewLogProvider_AllEntriesFailReturnsError(t *testing.T) {
	logger, _ := newTestLogger()
	cfgs := []ExporterConfig{
		{Type: "otlp", Endpoint: "localhost:4317", Protocol: "grpc", Logs: true, CertificateFile: "/no/such/cert.pem", Timeout: 1},
	}
	lp, err := NewLogProvider(t.Context(), cfgs, nil, logger)
	if err == nil {
		t.Errorf("expected error when all entries fail; got nil and lp=%v", lp)
	}
	if lp != nil {
		t.Error("expected nil LogProvider when all entries fail")
	}
}
