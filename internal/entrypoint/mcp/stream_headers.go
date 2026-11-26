// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"net/http"
	"strings"
)

func passStreamsThroughProxies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marked := &lateStreamHeaderWriter{ResponseWriter: w}
		if _, canFlush := w.(http.Flusher); canFlush {
			next.ServeHTTP(flushingStreamHeaderWriter{marked}, r)
			return
		}
		next.ServeHTTP(marked, r)
	})
}

type lateStreamHeaderWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *lateStreamHeaderWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
			w.Header().Set("Cache-Control", "no-cache, no-transform")
			w.Header().Set("X-Accel-Buffering", "no")
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *lateStreamHeaderWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *lateStreamHeaderWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type flushingStreamHeaderWriter struct {
	*lateStreamHeaderWriter
}

func (w flushingStreamHeaderWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	w.ResponseWriter.(http.Flusher).Flush()
}
