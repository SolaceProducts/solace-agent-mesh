// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const labelEntrypointName = "entrypoint.name"

func MetricsMiddleware(metrics runtime.Metrics, entrypointName string, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" || r.URL.Path == "/metrics" || r.URL.Path == "/ready" {
				next.ServeHTTP(w, r)
				return
			}

			operation := classifyRoute(r.URL.Path)
			isStreaming := isStreamingEndpoint(r.URL.Path)

			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			if isStreaming {
				rw.onFirstWrite = func() {
					ttfb := time.Since(start)
					metrics.Histogram("entrypoint.ttfb.duration", ttfb,
						labelEntrypointName, entrypointName,
						"operation.name", operation,
					)
				}
			}

			next.ServeHTTP(rw, r)

			if !isSSESubscribe(r.URL.Path) {
				duration := time.Since(start)
				errorType := ""
				if rw.statusCode >= 400 {
					errorType = classifyHTTPStatus(rw.statusCode)
				}
				metrics.Histogram("entrypoint.duration", duration,
					labelEntrypointName, entrypointName,
					"operation.name", operation,
					"error.type", errorType,
				)
			}

			routeTemplate := r.Pattern
			if idx := strings.IndexByte(routeTemplate, ' '); idx > 0 {
				routeTemplate = routeTemplate[idx+1:]
			}
			if routeTemplate == "" {
				routeTemplate = "unmatched"
				logger.Warn("gateway: request matched no registered route",
					"method", r.Method,
					"path", r.URL.Path,
					"httpStatus", rw.statusCode,
					"gatewayName", entrypointName,
					"userAgent", r.UserAgent(),
				)
			}
			errorType := ""
			if rw.statusCode >= 400 {
				errorType = classifyHTTPStatus(rw.statusCode)
			}
			metrics.Counter("entrypoint.requests", 1,
				labelEntrypointName, entrypointName,
				"route.template", routeTemplate,
				"http.method", r.Method,
				"error.type", errorType,
			)
		})
	}
}

const (
	RejectRouteAuth     = "rejected_auth"
	RejectRouteMaxBytes = "rejected_maxbytes"
)

func RecordGatewayReject(entrypointName, routeTemplate, method string, status int) {
	runtime.MetricProvider().Counter("entrypoint.requests", 1,
		labelEntrypointName, entrypointName,
		"route.template", routeTemplate,
		"http.method", method,
		"error.type", classifyHTTPStatus(status),
	)
}

type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	wroteHeader  bool
	onFirstWrite func()
	firstWritten bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.statusCode = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.firstWritten && rw.onFirstWrite != nil {
		rw.firstWritten = true
		rw.onFirstWrite()
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func classifyRoute(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/v1/tasks"):
		return "/tasks"
	case strings.HasPrefix(path, "/api/v1/message"):
		return "/message"
	case strings.HasPrefix(path, "/api/v1/sessions"):
		return "/sessions"
	case strings.HasPrefix(path, "/api/v1/sse"), isChatV2EventStream(path):
		return "/sse"
	case strings.HasPrefix(path, "/api/v1/artifacts"):
		return "/artifacts"
	case strings.HasPrefix(path, "/api/v1/projects"):
		return "/projects"
	case strings.HasPrefix(path, "/api/v1/agents"):
		return "/agents"
	default:
		return "other"
	}
}

func isStreamingEndpoint(path string) bool {
	return strings.HasSuffix(path, ":stream") || isSSESubscribe(path)
}

const chatV2TasksPrefix = "/api/v2/chat/tasks/"

func isChatV2EventStream(path string) bool {
	return strings.HasPrefix(path, chatV2TasksPrefix) && strings.HasSuffix(path, "/events")
}

func isSSESubscribe(path string) bool {
	return strings.HasPrefix(path, "/api/v1/sse/subscribe") || isChatV2EventStream(path)
}
