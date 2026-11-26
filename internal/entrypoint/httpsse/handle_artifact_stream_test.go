// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

type countingReader struct {
	remaining int64
	read      *int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > c.remaining {
		n = int(c.remaining)
	}
	c.remaining -= int64(n)
	*c.read += int64(n)
	return n, nil
}

func (c *countingReader) Close() error { return nil }

type countingStore struct {
	*memory.Store
	size      int64
	readBytes *int64
}

func (s *countingStore) OpenReader(_ context.Context, _ artifact.Locator, _ *int) (io.ReadCloser, string, int64, error) {
	return &countingReader{remaining: s.size, read: s.readBytes}, "application/octet-stream", s.size, nil
}

type discardResponseWriter struct {
	header  http.Header
	status  int
	written int64
}

func (d *discardResponseWriter) Header() http.Header {
	if d.header == nil {
		d.header = http.Header{}
	}
	return d.header
}
func (d *discardResponseWriter) WriteHeader(status int) { d.status = status }
func (d *discardResponseWriter) Write(p []byte) (int, error) {
	d.written += int64(len(p))
	return len(p), nil
}

func TestServeArtifact_PreviewReadsOnlyMaxBytes(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	var readBytes int64
	const size = 200 << 20
	cs := &countingStore{Store: memory.New(), size: size, readBytes: &readBytes}
	h.artSvc = &artifact.ScopedService{Inner: observability.NewInstrumentedArtifactService(cs), Scope: "namespace", Namespace: "test-ns"}
	loc := h.artSvc.Resolve("", "user", "sess", "big.bin")

	const maxBytes = 1024
	req := httptest.NewRequest("GET", "/x?maxBytes="+strconv.Itoa(maxBytes), nil)
	rec := httptest.NewRecorder()
	h.serveArtifact(rec, req, loc, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if readBytes != maxBytes {
		t.Fatalf("preview read %d bytes, expected exactly %d (full artifact must not be loaded)", readBytes, maxBytes)
	}
	if rec.Header().Get("X-Truncated") != "true" {
		t.Errorf("expected X-Truncated=true, got %q", rec.Header().Get("X-Truncated"))
	}
	if got := rec.Header().Get("X-Original-Size"); got != strconv.Itoa(size) {
		t.Errorf("expected X-Original-Size=%d, got %q", size, got)
	}
	if rec.Body.Len() != maxBytes {
		t.Errorf("expected body of %d bytes, got %d", maxBytes, rec.Body.Len())
	}
}

func TestServeArtifact_FullDownloadStreams(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	var readBytes int64
	const size = 200 << 20
	cs := &countingStore{Store: memory.New(), size: size, readBytes: &readBytes}
	h.artSvc = &artifact.ScopedService{Inner: observability.NewInstrumentedArtifactService(cs), Scope: "namespace", Namespace: "test-ns"}
	loc := h.artSvc.Resolve("", "user", "sess", "big.bin")

	req := httptest.NewRequest("GET", "/x", nil)
	w := &discardResponseWriter{}
	h.serveArtifact(w, req, loc, nil)

	if w.status != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.status)
	}
	if got := w.Header().Get("Content-Length"); got != strconv.Itoa(size) {
		t.Errorf("expected Content-Length=%d, got %q", size, got)
	}
	if w.written != size {
		t.Errorf("expected %d bytes streamed to client, got %d", size, w.written)
	}
	if readBytes != size {
		t.Errorf("expected %d bytes read from store, got %d", size, readBytes)
	}
}

func TestServeArtifact_FullDownloadContent(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	artSvc := &artifact.ScopedService{
		Inner:     observability.NewInstrumentedArtifactService(memory.New()),
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	h.artSvc = artSvc
	want := make([]byte, 1<<20)
	for i := range want {
		want[i] = byte(i)
	}
	loc := artSvc.Resolve("", "user", "sess", "data.bin")
	if _, err := artSvc.Inner.Save(context.Background(), loc, want, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/x", nil)
	rec := httptest.NewRecorder()
	h.serveArtifact(rec, req, loc, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(want)) {
		t.Errorf("expected Content-Length=%d, got %q", len(want), got)
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Errorf("downloaded content does not match stored content")
	}
}

func TestServeArtifact_NotFoundReturns404(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	h.artSvc = &artifact.ScopedService{
		Inner:     observability.NewInstrumentedArtifactService(memory.New()),
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	loc := h.artSvc.Resolve("", "user", "sess", "missing.bin")

	req := httptest.NewRequest("GET", "/x", nil)
	rec := httptest.NewRecorder()
	h.serveArtifact(rec, req, loc, nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

type errorStore struct {
	*memory.Store
}

func (errorStore) OpenReader(_ context.Context, _ artifact.Locator, _ *int) (io.ReadCloser, string, int64, error) {
	return nil, "", 0, errors.New("backend exploded")
}

func TestServeArtifact_StoreErrorReturns500(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	h.artSvc = &artifact.ScopedService{
		Inner:     observability.NewInstrumentedArtifactService(errorStore{Store: memory.New()}),
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	loc := h.artSvc.Resolve("", "user", "sess", "boom.bin")

	req := httptest.NewRequest("GET", "/x", nil)
	rec := httptest.NewRecorder()
	h.serveArtifact(rec, req, loc, nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
