// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type submitRefusalReporter interface {
	ReportSubmitRejected(rejection entrypoint.SubmitRejection)
}

var taskSubmitPaths = map[string]bool{
	"/api/v1/message:stream": true,
	"/api/v1/message:send":   true,
}

func isTaskSubmitRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := r.URL.Path
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	return taskSubmitPaths[path]
}

func isRefusalStatus(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

type refusalRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *refusalRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *refusalRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rec *refusalRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

func reportRefusedSubmits(reporter submitRefusalReporter, logger *slog.Logger, guarded func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		accepted := guarded(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isTaskSubmitRequest(r) {
				accepted.ServeHTTP(w, r)
				return
			}
			passedOn := false
			observed := &refusalRecorder{ResponseWriter: w}
			guarded(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				passedOn = true
				next.ServeHTTP(w, r)
			})).ServeHTTP(observed, r)
			if passedOn || !isRefusalStatus(observed.status) {
				return
			}
			reportSubmitRefusal(reporter, logger, r, observed.status)
		})
	}
}

func reportSubmitRefusal(reporter submitRefusalReporter, logger *slog.Logger, r *http.Request, status int) {
	surface := httputil.ClientSurface(r)
	traceID := idgen.New()
	logger.Warn("task submit refused before the handler", "httpStatus", status, "surface", surface, "traceID", traceID)
	reporter.ReportSubmitRejected(entrypoint.SubmitRejection{TraceID: traceID, Surface: surface, Reason: taxonomy.RejectForbidden, SessionID: httputil.ClientSession(r)})
}
