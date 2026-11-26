// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestCreateMetricsRecorder_NoExporters(t *testing.T) {
	apps := []map[string]any{
		{
			"management_server": map[string]any{
				"observability": map[string]any{
					"enabled":       true,
					"metric_prefix": "sam_test",
				},
			},
		},
	}
	logger := slog.Default()
	rec := createMetricsRecorder(nil, apps, logger, "sam-test")
	if rec == nil {
		t.Fatal("expected non-nil metrics recorder")
	}
}

func TestCreateMetricsRecorder_WithExporter(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	apps := []map[string]any{
		{
			"management_server": map[string]any{
				"observability": map[string]any{
					"enabled":       true,
					"metric_prefix": "sam_test",
				},
				"exporters": []any{
					map[string]any{
						"type":     "otlp",
						"endpoint": "http://localhost:4318",
						"protocol": "http",
						"metrics":  true,
					},
					map[string]any{
						"type":     "otlp",
						"endpoint": "localhost:4317",
						"protocol": "grpc",
						"metrics":  true,
						"insecure": true,
					},
				},
			},
		},
	}
	rec := createMetricsRecorder(nil, apps, logger, "sam-test")
	if rec == nil {
		t.Fatal("expected non-nil metrics recorder")
	}

	out := buf.String()
	if !strings.Contains(out, "registered OTLP metric exporter") {
		t.Errorf("expected exporter registration log, got: %s", out)
	}
	count := strings.Count(out, "registered OTLP metric exporter")
	if count != 2 {
		t.Errorf("expected 2 OTLP exporter registrations, got %d. log: %s", count, out)
	}
}

func TestCreateMetricsRecorder_InvalidExporterIsSkipped(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	apps := []map[string]any{
		{
			"management_server": map[string]any{
				"observability": map[string]any{
					"enabled": true,
				},
				"exporters": []any{
					map[string]any{
						"type":     "otlp",
						"endpoint": "http://localhost:4318",
						"protocol": "http",
						"metrics":  true,
					},
					map[string]any{
						"type": "otlp",
					},
				},
			},
		},
	}
	rec := createMetricsRecorder(nil, apps, logger, "sam-test")
	if rec == nil {
		t.Fatal("expected non-nil metrics recorder")
	}
	out := buf.String()
	if !strings.Contains(out, "invalid exporter config") {
		t.Errorf("expected invalid-exporter error, got: %s", out)
	}
	if !strings.Contains(out, "registered OTLP metric exporter") {
		t.Errorf("expected the valid exporter to register, got: %s", out)
	}
}

func TestCreateMetricsRecorder_LogsOnlyExporterDoesNotRegister(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	apps := []map[string]any{
		{
			"management_server": map[string]any{
				"observability": map[string]any{
					"enabled": true,
				},
				"exporters": []any{
					map[string]any{
						"type":     "otlp",
						"endpoint": "http://localhost:4318",
						"protocol": "http",
						"logs":     true,
					},
				},
			},
		},
	}
	rec := createMetricsRecorder(nil, apps, logger, "sam-test")
	if rec == nil {
		t.Fatal("expected non-nil metrics recorder")
	}
	if strings.Contains(buf.String(), "registered OTLP metric exporter") {
		t.Errorf("logs-only exporter must not register as a metric exporter; got: %s", buf.String())
	}
}

func TestBuildOTLPLogProvider_NoLogsReturnsNil(t *testing.T) {
	apps := []map[string]any{
		{
			"management_server": map[string]any{
				"exporters": []any{
					map[string]any{
						"type":     "otlp",
						"endpoint": "http://localhost:4318",
						"protocol": "http",
						"metrics":  true,
					},
				},
			},
		},
	}
	logger := slog.Default()
	lp, err := BuildOTLPLogProvider(context.Background(), apps, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lp != nil {
		t.Error("expected nil LogProvider when no exporter has logs:true")
	}
}

func TestBuildOTLPLogProvider_AggregatesAcrossApps(t *testing.T) {
	apps := []map[string]any{
		{
			"management_server": map[string]any{
				"exporters": []any{
					map[string]any{
						"type":      "otlp",
						"endpoint":  "http://localhost:4318",
						"protocol":  "http",
						"logs":      true,
						"log_level": "WARNING",
					},
				},
			},
		},
		{
			"management_server": map[string]any{
				"exporters": []any{
					map[string]any{
						"type":      "otlp",
						"endpoint":  "http://localhost:4318",
						"protocol":  "http",
						"logs":      true,
						"log_level": "ERROR",
					},
				},
			},
		},
	}
	logger := slog.Default()
	lp, err := BuildOTLPLogProvider(context.Background(), apps, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lp == nil {
		t.Fatal("expected non-nil LogProvider")
	}
	defer func() { _ = lp.Stop(context.Background()) }()

	if got := lp.ExporterCount(); got != 2 {
		t.Errorf("ExporterCount: got %d, want 2", got)
	}
}

func TestAttachOTLPLogExporters_NoExportersReturnsBaseLogger(t *testing.T) {
	apps := []samapp.AppEntry{
		{Name: "test", AppConfig: map[string]any{}},
	}
	base := slog.Default()
	merged, lp, err := AttachOTLPLogExporters(context.Background(), apps, nil, base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lp != nil {
		t.Error("expected nil LogProvider")
	}
	if merged != base {
		t.Error("expected base logger to be returned unchanged")
	}
}

func TestAttachOTLPLogExporters_WiresHandlers(t *testing.T) {
	apps := []samapp.AppEntry{
		{
			Name: "test",
			AppConfig: map[string]any{
				"management_server": map[string]any{
					"exporters": []any{
						map[string]any{
							"type":     "otlp",
							"endpoint": "http://localhost:4318",
							"protocol": "http",
							"logs":     true,
						},
					},
				},
			},
		},
	}
	base := slog.Default()
	merged, lp, err := AttachOTLPLogExporters(context.Background(), apps, nil, base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lp == nil {
		t.Fatal("expected non-nil LogProvider")
	}
	defer func() { _ = lp.Stop(context.Background()) }()
	if merged == base {
		t.Error("expected merged logger to differ from base")
	}
}

func TestBuildOTLPLogProvider_WorksWhenObservabilityDisabled(t *testing.T) {
	apps := []map[string]any{
		{
			"management_server": map[string]any{
				"observability": map[string]any{
					"enabled": false,
				},
				"exporters": []any{
					map[string]any{
						"type":     "otlp",
						"endpoint": "http://localhost:4318",
						"protocol": "http",
						"logs":     true,
					},
				},
			},
		},
	}
	logger := slog.Default()
	lp, err := BuildOTLPLogProvider(context.Background(), apps, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lp == nil {
		t.Fatal("expected log exporters to be built even when observability.enabled=false")
	}
	_ = lp.Stop(context.Background())
}

func TestCreateMetricsRecorder_PrefersTopLevelOverPerApp(t *testing.T) {
	rootCfg := map[string]any{
		"management_server": map[string]any{
			"observability": map[string]any{
				"enabled":       true,
				"metric_prefix": "from_root",
			},
		},
	}
	appCfgs := []map[string]any{
		{
			"management_server": map[string]any{
				"observability": map[string]any{
					"enabled":       true,
					"metric_prefix": "from_app",
				},
			},
		},
	}
	logger := slog.Default()
	rec := createMetricsRecorder(rootCfg, appCfgs, logger, "sam-test")
	if rec == nil {
		t.Fatal("expected non-nil recorder when root config has observability")
	}
}

func TestCreateMetricsRecorder_FallsBackToPerAppDeprecated(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	appCfgs := []map[string]any{
		{
			"management_server": map[string]any{
				"observability": map[string]any{
					"enabled": true,
				},
			},
		},
	}
	rec := createMetricsRecorder(nil, appCfgs, logger, "sam-test")
	if rec == nil {
		t.Fatal("expected non-nil recorder via per-app fallback")
	}
	if !strings.Contains(buf.String(), "deprecated") {
		t.Errorf("expected deprecation warning, got: %s", buf.String())
	}
}

func TestCreateMetricsRecorder_YAMLExcludeLabelsStarDisablesMetric(t *testing.T) {
	rootCfg := map[string]any{
		"management_server": map[string]any{
			"observability": map[string]any{
				"enabled":       true,
				"metric_prefix": "yaml_test",
				"distribution_metrics": map[string]any{
					"operation.duration": map[string]any{
						"exclude_labels": []any{"*"},
					},
				},
			},
		},
	}
	logger := slog.Default()
	rec := createMetricsRecorder(rootCfg, nil, logger, "sam-test")
	if rec == nil {
		t.Fatal("expected non-nil recorder")
	}

	reg, ok := rec.(*observability.Registry)
	if !ok {
		t.Fatalf("recorder is not *observability.Registry: %T", rec)
	}
	reg.Histogram("operation.duration", 100*time.Millisecond, "type", "agent")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	reg.Handler().ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Result().Body)

	if strings.Contains(string(body), "yaml_test_operation_duration") {
		t.Errorf("disabled metric appeared in scrape output:\n%s", body)
	}
}

func TestCreateMetricsRecorder_YAMLNamedLabelExclusion(t *testing.T) {
	rootCfg := map[string]any{
		"management_server": map[string]any{
			"observability": map[string]any{
				"enabled":       true,
				"metric_prefix": "yaml_test",
				"distribution_metrics": map[string]any{
					"operation.duration": map[string]any{
						"buckets":        []any{0.1, 1.0, 10.0},
						"exclude_labels": []any{"tokens"},
					},
				},
			},
		},
	}
	logger := slog.Default()
	rec := createMetricsRecorder(rootCfg, nil, logger, "sam-test")
	if rec == nil {
		t.Fatal("expected non-nil recorder")
	}
	reg := rec.(*observability.Registry)
	reg.Histogram("operation.duration", 50*time.Millisecond,
		"type", "agent",
		"tokens", "1234",
	)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	reg.Handler().ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Result().Body)
	s := string(body)

	if !strings.Contains(s, `type="agent"`) {
		t.Errorf("type label missing in scrape:\n%s", s)
	}
	if strings.Contains(s, "tokens=") {
		t.Errorf("tokens label should be excluded but appeared in scrape:\n%s", s)
	}
	if !strings.Contains(s, `le="1"`) {
		t.Errorf("expected custom bucket le=1 from YAML, got:\n%s", s)
	}
}

func TestCreateMetricsRecorder_YAMLValueMetricExclusionDisablesCounter(t *testing.T) {
	rootCfg := map[string]any{
		"management_server": map[string]any{
			"observability": map[string]any{
				"enabled":       true,
				"metric_prefix": "yaml_test",
				"value_metrics": map[string]any{
					"gen_ai.tokens.used": map[string]any{
						"exclude_labels": []any{"*"},
					},
				},
			},
		},
	}
	logger := slog.Default()
	rec := createMetricsRecorder(rootCfg, nil, logger, "sam-test")
	if rec == nil {
		t.Fatal("expected non-nil recorder")
	}
	reg := rec.(*observability.Registry)
	reg.Counter("gen_ai.tokens.used", 42, "gen_ai.token.type", "input")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	reg.Handler().ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Result().Body)

	if strings.Contains(string(body), "yaml_test_gen_ai_tokens_used") {
		t.Errorf("disabled counter appeared in scrape:\n%s", body)
	}
}
