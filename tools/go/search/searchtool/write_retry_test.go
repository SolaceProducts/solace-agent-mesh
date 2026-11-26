// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type retryCase struct {
	name   string
	params QueryParams
}

func retryStrPtr(s string) *string { return &s }

func writeRetryCases() []retryCase {
	return []retryCase{
		{"index without id", QueryParams{Index: "docs", Operation: retryStrPtr("index"), Query: `{"title":"a"}`}},
		{"index with id", QueryParams{Index: "docs", Operation: retryStrPtr("index"), ID: retryStrPtr("1"), Query: `{"title":"a"}`}},
		{"update", QueryParams{Index: "docs", Operation: retryStrPtr("update"), ID: retryStrPtr("1"), Query: `{"title":"b"}`}},
		{"delete by id", QueryParams{Index: "docs", Operation: retryStrPtr("delete"), ID: retryStrPtr("1")}},
		{"delete by query", QueryParams{Index: "docs", Operation: retryStrPtr("delete"), Query: `{"query":{"match_all":{}}}`}},
	}
}

func readRetryCases() []retryCase {
	return []retryCase{
		{"search", QueryParams{Index: "docs", Operation: retryStrPtr("search"), Query: `{"query":{"match_all":{}}}`}},
		{"count", QueryParams{Index: "docs", Operation: retryStrPtr("count"), Query: `{}`}},
	}
}

func newGatewayTimeoutServer(hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":{"number":"8.0.0"}}`))
			return
		}
		hits.Add(1)
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
}

func newRetryBackends(t *testing.T, url string) map[string]Backend {
	t.Helper()
	backends := map[string]Backend{
		"opensearch":    newOpenSearchBackend(map[string]any{"connection_string": url}),
		"elasticsearch": newElasticsearchBackend(map[string]any{"connection_string": url}),
	}
	for name, b := range backends {
		if err := b.Connect(t.Context()); err != nil {
			t.Fatalf("%s Connect: %v", name, err)
		}
	}
	return backends
}

func TestWriteOperations_NotRetriedOn504(t *testing.T) {
	for _, tc := range writeRetryCases() {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := newGatewayTimeoutServer(&hits)
			defer srv.Close()

			for name, b := range newRetryBackends(t, srv.URL) {
				hits.Store(0)
				if _, err := b.ExecuteQuery(t.Context(), tc.params); err == nil {
					t.Errorf("%s: expected an error from the 504 response", name)
				}
				if got := hits.Load(); got != 1 {
					t.Errorf("%s: server received %d request(s), want exactly 1", name, got)
				}
			}
		})
	}
}

func TestReadOperations_RetriedOn504(t *testing.T) {
	for _, tc := range readRetryCases() {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := newGatewayTimeoutServer(&hits)
			defer srv.Close()

			for name, b := range newRetryBackends(t, srv.URL) {
				hits.Store(0)
				if _, err := b.ExecuteQuery(t.Context(), tc.params); err == nil {
					t.Errorf("%s: expected an error from the 504 response", name)
				}
				if got := hits.Load(); got < 2 {
					t.Errorf("%s: server received %d request(s), want the read to be retried", name, got)
				}
			}
		})
	}
}

func TestWriteOperations_NotRetriedOnDroppedConnection(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			_, _ = w.Write([]byte(`{"version":{"number":"8.0.0"}}`))
			return
		}
		hits.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	params := writeRetryCases()[0].params
	for name, b := range newRetryBackends(t, srv.URL) {
		hits.Store(0)
		if _, err := b.ExecuteQuery(t.Context(), params); err == nil {
			t.Errorf("%s: expected an error from the dropped connection", name)
		}
		if got := hits.Load(); got != 1 {
			t.Errorf("%s: server received %d request(s), want exactly 1", name, got)
		}
	}
}
