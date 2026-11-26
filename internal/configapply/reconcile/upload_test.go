// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type httpRecorder struct {
	initCalls     atomic.Int32
	chunkCalls    atomic.Int32
	completeCalls atomic.Int32
	singleCalls   atomic.Int32
	receivedBytes atomic.Int64
	singleQuery   atomic.Pointer[string]
	completeQuery atomic.Pointer[string]
}

const testChunkSize = 5 << 20

func newRecorderServer(t *testing.T) (*httptest.Server, *httpRecorder) {
	t.Helper()
	rec := &httpRecorder{}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v2/platform/toolsetUploads", func(w http.ResponseWriter, r *http.Request) {
		rec.initCalls.Add(1)
		_ = r.Body.Close()
		writeV2Single(w, http.StatusCreated, apiv2.ToolsetUpload{ID: "upload-stub", Type: "toolsetUpload", ChunkSize: testChunkSize})
	})
	mux.HandleFunc("PUT /api/v2/platform/toolsetUploads/{toolsetUploadId}/chunks/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.chunkCalls.Add(1)
		body, _ := io.ReadAll(r.Body)
		rec.receivedBytes.Add(int64(len(body)))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /api/v2/platform/toolsetUploads/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		rec.completeCalls.Add(1)
		q := r.URL.RawQuery
		rec.completeQuery.Store(&q)
		_ = r.Body.Close()
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "abc", Name: "x"})
	})
	mux.HandleFunc("PUT /api/v2/platform/toolsets/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
		rec.singleCalls.Add(1)
		q := r.URL.RawQuery
		rec.singleQuery.Store(&q)
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "abc", Name: "x"})
	})

	return newV2Platform(t, mux), rec
}

func newClientForTest(t *testing.T, url string) *Client {
	t.Helper()
	resolved, err := target.Target{URL: url}.Resolve(t.Context(), nil)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	return NewClient(resolved)
}

func TestUploadToolset_SmallPayloadUsesSingleShot(t *testing.T) {
	srv, rec := newRecorderServer(t)
	c := newClientForTest(t, srv.URL)

	if err := c.UploadToolset(context.Background(), "demo", "demo.zip", bytes.Repeat([]byte("x"), 1024)); err != nil {
		t.Fatalf("UploadToolset: %v", err)
	}
	if got := rec.singleCalls.Load(); got != 1 {
		t.Errorf("single-shot calls = %d; want 1", got)
	}
	if got := rec.initCalls.Load() + rec.chunkCalls.Load() + rec.completeCalls.Load(); got != 0 {
		t.Errorf("expected no chunked traffic; init=%d chunks=%d complete=%d",
			rec.initCalls.Load(), rec.chunkCalls.Load(), rec.completeCalls.Load())
	}
}

func TestUploadToolset_LargePayloadUsesChunkedPath(t *testing.T) {
	srv, rec := newRecorderServer(t)
	c := newClientForTest(t, srv.URL)

	payload := bytes.Repeat([]byte("y"), chunkedUploadThreshold+1)
	if err := c.UploadToolset(context.Background(), "demo", "demo.zip", payload); err != nil {
		t.Fatalf("UploadToolset: %v", err)
	}

	if got := rec.singleCalls.Load(); got != 0 {
		t.Errorf("single-shot calls = %d; want 0", got)
	}
	if got := rec.initCalls.Load(); got != 1 {
		t.Errorf("init calls = %d; want 1", got)
	}
	if got := rec.completeCalls.Load(); got != 1 {
		t.Errorf("complete calls = %d; want 1", got)
	}
	wantChunks := int32((len(payload) + testChunkSize - 1) / testChunkSize)
	if got := rec.chunkCalls.Load(); got != wantChunks {
		t.Errorf("chunk calls = %d; want %d", got, wantChunks)
	}
	if got := rec.receivedBytes.Load(); got != int64(len(payload)) {
		t.Errorf("received bytes = %d; want %d", got, len(payload))
	}
}

func TestUploadToolset_RetriesOnTransient503(t *testing.T) {
	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/toolsets/{id}/bundle", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if attempts.Add(1) == 1 {
			writeV2Error(w, http.StatusServiceUnavailable, "busy", nil)
			return
		}
		writeV2Single(w, http.StatusOK, wire.ToolsetResponse{ID: "abc", Name: "x"})
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	if err := c.UploadToolset(context.Background(), "demo", "demo.zip", []byte("hi")); err != nil {
		t.Fatalf("UploadToolset: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d; want 2 (initial + 1 retry)", got)
	}
}

func TestUploadToolset_SingleShotURLHasNoQuery(t *testing.T) {
	srv, rec := newRecorderServer(t)
	c := newClientForTest(t, srv.URL)

	if err := c.UploadToolset(context.Background(), "demo", "demo.zip", []byte("hi")); err != nil {
		t.Fatalf("UploadToolset: %v", err)
	}
	if got := rec.singleCalls.Load(); got != 1 {
		t.Fatalf("single-shot calls = %d; want 1", got)
	}
	if q := rec.singleQuery.Load(); q != nil && *q != "" {
		t.Errorf("single-shot query = %q; want empty", strDeref(q))
	}
}

func TestUploadToolset_ChunkedCompleteURLHasNoQuery(t *testing.T) {
	srv, rec := newRecorderServer(t)
	c := newClientForTest(t, srv.URL)

	payload := bytes.Repeat([]byte("z"), chunkedUploadThreshold+1)
	if err := c.UploadToolset(context.Background(), "demo", "demo.zip", payload); err != nil {
		t.Fatalf("UploadToolset(chunked): %v", err)
	}
	if got := rec.completeCalls.Load(); got != 1 {
		t.Fatalf("complete calls = %d; want 1", got)
	}
	if q := rec.completeQuery.Load(); q != nil && *q != "" {
		t.Errorf("complete query = %q; want empty", strDeref(q))
	}
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func TestRetryHTTP_NonTransientHTTPErrorNotRetried(t *testing.T) {
	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/toolsetUploads", func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		writeV2Error(w, http.StatusBadRequest, "bad", nil)
	})
	srv := newV2Platform(t, mux)

	c := newClientForTest(t, srv.URL)
	_, _, err := c.initToolsetUpload(context.Background(), "demo", "demo.zip", 1024)
	if err == nil {
		t.Fatal("expected error from initToolsetUpload")
	}
	if attempts.Load() != 1 {
		t.Errorf("expected exactly 1 attempt for non-transient 400, got %d", attempts.Load())
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 in error message, got %v", err)
	}
}
