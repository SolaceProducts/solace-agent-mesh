// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type collectingMetrics struct {
	histograms []metricCall
	counters   []metricCall
}

type metricCall struct {
	name   string
	value  any
	labels []string
}

func (m *collectingMetrics) Counter(name string, delta int64, labels ...string) {
	m.counters = append(m.counters, metricCall{name: name, value: delta, labels: labels})
}

func (m *collectingMetrics) Gauge(string, float64, ...string) {}

func (m *collectingMetrics) Histogram(name string, value time.Duration, labels ...string) {
	m.histograms = append(m.histograms, metricCall{name: name, value: value, labels: labels})
}

func TestMetricsMiddlewareRecordsRequestMetrics(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	handler := MetricsMiddleware(metrics, "test-gateway", slog.Default())(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/message:send", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	found := false
	for _, h := range metrics.histograms {
		if h.name == "entrypoint.duration" {
			found = true
			assertLabel(t, h.labels, "entrypoint.name", "test-gateway")
			assertLabel(t, h.labels, "operation.name", "/message")
			assertLabel(t, h.labels, "error.type", "")
			break
		}
	}
	if !found {
		t.Error("expected entrypoint.duration histogram to be recorded")
	}

	foundCounter := false
	for _, c := range metrics.counters {
		if c.name == "entrypoint.requests" {
			foundCounter = true
			assertLabel(t, c.labels, "entrypoint.name", "test-gateway")
			assertLabel(t, c.labels, "route.template", "unmatched")
			assertLabel(t, c.labels, "http.method", "POST")
			break
		}
	}
	if !foundCounter {
		t.Error("expected entrypoint.requests counter to be recorded")
	}
}

func TestMetricsMiddlewareCounterUsesRoutePattern(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /api/v1/sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	handler := MetricsMiddleware(metrics, "test-gateway", slog.Default())(mux)

	cases := []struct {
		method     string
		path       string
		wantRoute  string
		wantStatus int
	}{
		{"GET", "/api/v1/sessions/abc-123", "/api/v1/sessions/{id}", 200},
		{"POST", "/api/v1/sessions", "/api/v1/sessions", 201},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	if got := len(metrics.counters); got != 2 {
		t.Fatalf("expected 2 counter increments, got %d", got)
	}
	for i, c := range metrics.counters {
		if c.name != "entrypoint.requests" {
			t.Errorf("counter[%d].name = %q, want entrypoint.requests", i, c.name)
		}
		assertLabel(t, c.labels, "route.template", cases[i].wantRoute)
		assertLabel(t, c.labels, "http.method", cases[i].method)
	}
}

func TestMetricsMiddlewareReadsPatternBelowRequestClone(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrappedMux := MetricsMiddleware(metrics, "test-gateway", slog.Default())(mux)

	stripClone := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := *r
		u := *r.URL
		r2.URL = &u
		wrappedMux.ServeHTTP(w, &r2)
	})
	authClone := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stripClone.ServeHTTP(w, r.WithContext(r.Context()))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/abc-123", nil)
	w := httptest.NewRecorder()
	authClone.ServeHTTP(w, req)

	if len(metrics.counters) != 1 {
		t.Fatalf("expected 1 counter, got %d", len(metrics.counters))
	}
	if got, _ := labelValueFromPairs(metrics.counters[0].labels, "route.template"); got != "/api/v1/sessions/{id}" {
		t.Errorf("route.template = %q, want /api/v1/sessions/{id}; "+
			"if this drops to \"unmatched\", the metrics middleware has been "+
			"hoisted above a request-cloning layer again", got)
	}
}

func labelValueFromPairs(labels []string, key string) (string, bool) {
	for i := 0; i+1 < len(labels); i += 2 {
		if labels[i] == key {
			return labels[i+1], true
		}
	}
	return "", false
}

func TestMetricsMiddlewareCounterUnmatchedFor404(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/known", func(w http.ResponseWriter, _ *http.Request) {})
	handler := MetricsMiddleware(metrics, "test-gateway", slog.Default())(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/totally/unknown/path", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if len(metrics.counters) != 1 {
		t.Fatalf("expected 1 counter, got %d", len(metrics.counters))
	}
	assertLabel(t, metrics.counters[0].labels, "route.template", "unmatched")
	assertLabel(t, metrics.counters[0].labels, "error.type", "4xx_error")
}

func TestMetricsMiddlewareSkipsHealthEndpoint(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	handler := MetricsMiddleware(metrics, "gw", slog.Default())(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if len(metrics.histograms) > 0 || len(metrics.counters) > 0 {
		t.Error("expected no metrics for /health endpoint")
	}
}

func TestMetricsMiddlewareRecordsErrorType(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	handler := MetricsMiddleware(metrics, "gw", slog.Default())(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	for _, h := range metrics.histograms {
		if h.name == "entrypoint.duration" {
			assertLabel(t, h.labels, "error.type", "5xx_error")
			return
		}
	}
	t.Error("expected entrypoint.duration with error_type")
}

func TestMetricsMiddlewareTracksStreamingTTFB(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	handler := MetricsMiddleware(metrics, "gw", slog.Default())(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("first chunk"))
		}),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/message:stream", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	found := false
	for _, h := range metrics.histograms {
		if h.name == "entrypoint.ttfb.duration" {
			found = true
			assertLabel(t, h.labels, "operation.name", "/message")
			break
		}
	}
	if !found {
		t.Error("expected entrypoint.ttfb.duration for streaming endpoint")
	}
}

func TestMetricsMiddlewareSkipsDurationForSSESubscribe(t *testing.T) {
	metrics := installMetrics(t, &collectingMetrics{})

	handler := MetricsMiddleware(metrics, "gw", slog.Default())(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: first event\n\n"))
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sse/subscribe/task123", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	for _, h := range metrics.histograms {
		if h.name == "entrypoint.duration" {
			t.Error("should not record entrypoint.duration for SSE subscribe (indefinite connection)")
		}
	}

	foundTTFB := false
	for _, h := range metrics.histograms {
		if h.name == "entrypoint.ttfb.duration" {
			foundTTFB = true
			break
		}
	}
	if !foundTTFB {
		t.Error("expected TTFB for SSE subscribe endpoint")
	}
}

func TestClassifyRoute(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/api/v1/tasks/123", "/tasks"},
		{"/api/v1/message:stream", "/message"},
		{"/api/v1/sessions", "/sessions"},
		{"/api/v1/sse/subscribe/abc", "/sse"},
		{"/api/v2/chat/tasks/abc/events", "/sse"},
		{"/api/v1/artifacts/xyz", "/artifacts"},
		{"/api/v1/projects", "/projects"},
		{"/api/v1/agents", "/agents"},
		{"/api/v1/unknown", "other"},
		{"/something/else", "other"},
	}

	for _, tt := range tests {
		got := classifyRoute(tt.path)
		if got != tt.expected {
			t.Errorf("classifyRoute(%q) = %q, want %q", tt.path, got, tt.expected)
		}
	}
}

func assertLabel(t *testing.T, labels []string, key, want string) {
	t.Helper()
	for i := 0; i+1 < len(labels); i += 2 {
		if labels[i] == key {
			if labels[i+1] != want {
				t.Errorf("label %q = %q, want %q", key, labels[i+1], want)
			}
			return
		}
	}
	t.Errorf("label %q not found in %v", key, labels)
}

func TestClassifyHTTPStatusExported(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{401, "auth_error"},
		{403, "auth_error"},
		{404, "4xx_error"},
		{500, "5xx_error"},
		{200, "error"},
	}

	for _, tt := range tests {
		got := ClassifyHTTPStatus(tt.code)
		if got != tt.want {
			t.Errorf("ClassifyHTTPStatus(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestRecordGatewayReject(t *testing.T) {
	tests := []struct {
		name      string
		sentinel  string
		method    string
		status    int
		wantError string
	}{
		{"auth 401", RejectRouteAuth, http.MethodPost, http.StatusUnauthorized, "auth_error"},
		{"maxbytes 413", RejectRouteMaxBytes, http.MethodPut, http.StatusRequestEntityTooLarge, "4xx_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := installMetrics(t, &collectingMetrics{})

			RecordGatewayReject("WebUIGateway", tt.sentinel, tt.method, tt.status)

			if len(m.counters) != 1 {
				t.Fatalf("expected 1 counter, got %d", len(m.counters))
			}
			c := m.counters[0]
			if c.name != "entrypoint.requests" {
				t.Errorf("metric = %q, want entrypoint.requests", c.name)
			}
			assertLabel(t, c.labels, "entrypoint.name", "WebUIGateway")
			assertLabel(t, c.labels, "route.template", tt.sentinel)
			assertLabel(t, c.labels, "http.method", tt.method)
			assertLabel(t, c.labels, "error.type", tt.wantError)
		})
	}
}
