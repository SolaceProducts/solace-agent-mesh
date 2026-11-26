// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return l, &buf
}

func TestValidateExporterConfig_Defaults(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{
		"type":     "otlp",
		"endpoint": "http://localhost:4318",
		"protocol": "http",
		"metrics":  true,
	}
	cfg, err := ValidateExporterConfig(raw, logger)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Type != "otlp" {
		t.Errorf("type: got %q, want otlp", cfg.Type)
	}
	if cfg.Timeout != DefaultTimeoutSec {
		t.Errorf("timeout default: got %v, want %v", cfg.Timeout, DefaultTimeoutSec)
	}
	if cfg.Compression != DefaultCompression {
		t.Errorf("compression default: got %q, want %q", cfg.Compression, DefaultCompression)
	}
	if cfg.LogLevel != DefaultLogLevel {
		t.Errorf("log_level default: got %q, want %q", cfg.LogLevel, DefaultLogLevel)
	}
}

func TestValidateExporterConfig_MissingRequired(t *testing.T) {
	logger, _ := newTestLogger()
	tests := []struct {
		name string
		raw  map[string]any
	}{
		{"missing all", map[string]any{}},
		{"missing endpoint", map[string]any{"type": "otlp", "protocol": "http"}},
		{"missing type", map[string]any{"endpoint": "x", "protocol": "http"}},
		{"missing protocol", map[string]any{"type": "otlp", "endpoint": "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ValidateExporterConfig(tt.raw, logger); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestValidateExporterConfig_RejectsBadType(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{"type": "jaeger", "endpoint": "x", "protocol": "http"}
	if _, err := ValidateExporterConfig(raw, logger); err == nil {
		t.Error("expected unsupported type error")
	}
}

func TestValidateExporterConfig_RejectsBadProtocol(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{"type": "otlp", "endpoint": "x", "protocol": "tcp"}
	if _, err := ValidateExporterConfig(raw, logger); err == nil {
		t.Error("expected unsupported protocol error")
	}
}

func TestValidateExporterConfig_RejectsEmptyEndpoint(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{"type": "otlp", "endpoint": "   ", "protocol": "http"}
	if _, err := ValidateExporterConfig(raw, logger); err == nil {
		t.Error("expected empty endpoint error")
	}
}

func TestValidateExporterConfig_LogLevelCaseInsensitive(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{
		"type":      "otlp",
		"endpoint":  "x",
		"protocol":  "http",
		"log_level": "warning",
	}
	cfg, err := ValidateExporterConfig(raw, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != "WARNING" {
		t.Errorf("log_level: got %q, want WARNING", cfg.LogLevel)
	}
}

func TestValidateExporterConfig_RejectsBadLogLevel(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{
		"type":      "otlp",
		"endpoint":  "x",
		"protocol":  "http",
		"log_level": "TRACE",
	}
	if _, err := ValidateExporterConfig(raw, logger); err == nil {
		t.Error("expected invalid log_level error")
	}
}

func TestValidateExporterConfig_RejectsBadCompression(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{
		"type":        "otlp",
		"endpoint":    "x",
		"protocol":    "http",
		"compression": "snappy",
	}
	if _, err := ValidateExporterConfig(raw, logger); err == nil {
		t.Error("expected invalid compression error")
	}
}

func TestValidateExporterConfig_RejectsBadTimeout(t *testing.T) {
	logger, _ := newTestLogger()
	for _, bad := range []any{0, -5, "ten", 0.0} {
		raw := map[string]any{
			"type":     "otlp",
			"endpoint": "x",
			"protocol": "http",
			"timeout":  bad,
		}
		if _, err := ValidateExporterConfig(raw, logger); err == nil {
			t.Errorf("expected error for timeout=%v", bad)
		}
	}
}

func TestValidateExporterConfig_AcceptsTimeout(t *testing.T) {
	logger, _ := newTestLogger()
	for _, ok := range []any{1, 2.5, int64(15), uint(20)} {
		raw := map[string]any{
			"type":     "otlp",
			"endpoint": "x",
			"protocol": "http",
			"timeout":  ok,
		}
		if _, err := ValidateExporterConfig(raw, logger); err != nil {
			t.Errorf("unexpected error for timeout=%v: %v", ok, err)
		}
	}
}

func TestValidateExporterConfig_HeadersTypes(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{
		"type":     "otlp",
		"endpoint": "x",
		"protocol": "http",
		"headers": map[string]any{
			"api-key": "secret",
		},
	}
	cfg, err := ValidateExporterConfig(raw, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Headers["api-key"] != "secret" {
		t.Errorf("headers: got %v", cfg.Headers)
	}

	bad := map[string]any{
		"type":     "otlp",
		"endpoint": "x",
		"protocol": "http",
		"headers": map[string]any{
			"api-key": 123,
		},
	}
	if _, err := ValidateExporterConfig(bad, logger); err == nil {
		t.Error("expected error for non-string header value")
	}
}

func TestValidateExporterConfig_InsecureCertMutex(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{
		"type":             "otlp",
		"endpoint":         "x",
		"protocol":         "grpc",
		"insecure":         true,
		"certificate_file": "/tmp/ca.pem",
	}
	if _, err := ValidateExporterConfig(raw, logger); err == nil {
		t.Error("expected mutex error for insecure+certificate_file")
	}
}

func TestValidateExporterConfig_UnknownFieldsWarn(t *testing.T) {
	logger, buf := newTestLogger()
	raw := map[string]any{
		"type":      "otlp",
		"endpoint":  "x",
		"protocol":  "http",
		"future":    "value",
		"frobulate": true,
	}
	if _, err := ValidateExporterConfig(raw, logger); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "unknown exporter config fields") {
		t.Errorf("expected unknown fields warning, got: %s", out)
	}
	if !strings.Contains(out, "future") || !strings.Contains(out, "frobulate") {
		t.Errorf("expected unknown field names in warning, got: %s", out)
	}
}

func TestValidateExporterConfig_BothDisabledLogsInfo(t *testing.T) {
	logger, buf := newTestLogger()
	raw := map[string]any{
		"type":     "otlp",
		"endpoint": "x",
		"protocol": "http",
		"metrics":  false,
		"logs":     false,
	}
	if _, err := ValidateExporterConfig(raw, logger); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "no-op") {
		t.Errorf("expected no-op info log, got: %s", buf.String())
	}
}

func TestParseExporters_NotAList(t *testing.T) {
	logger, buf := newTestLogger()
	raw := map[string]any{
		"management_server": map[string]any{
			"exporters": map[string]any{"foo": "bar"},
		},
	}
	out := ParseExporters(raw, logger)
	if len(out) != 0 {
		t.Errorf("expected 0 exporters, got %d", len(out))
	}
	if !strings.Contains(buf.String(), "must be a list") {
		t.Errorf("expected error log, got: %s", buf.String())
	}
}

func TestParseExporters_SkipsInvalid(t *testing.T) {
	logger, _ := newTestLogger()
	raw := map[string]any{
		"management_server": map[string]any{
			"exporters": []any{
				map[string]any{"type": "otlp", "endpoint": "x", "protocol": "http", "metrics": true},
				map[string]any{"type": "bogus"},
				map[string]any{"type": "otlp", "endpoint": "y", "protocol": "grpc", "metrics": true},
			},
		},
	}
	out := ParseExporters(raw, logger)
	if len(out) != 2 {
		t.Errorf("expected 2 valid exporters, got %d", len(out))
	}
}

func TestParseExporters_NoConfig(t *testing.T) {
	logger, _ := newTestLogger()
	if out := ParseExporters(map[string]any{}, logger); out != nil {
		t.Errorf("expected nil, got %v", out)
	}
	if out := ParseExporters(map[string]any{"management_server": map[string]any{}}, logger); out != nil {
		t.Errorf("expected nil, got %v", out)
	}
}
