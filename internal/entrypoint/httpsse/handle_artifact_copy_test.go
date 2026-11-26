// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type copyRequest struct {
	SourceSessionID string   `json:"sourceSessionId"`
	Filenames       []string `json:"filenames"`
}

type copyResponse struct {
	Copied []string `json:"copied"`
	Failed []string `json:"failed"`
}

func makeCopyRequest(t *testing.T, destSessionID string, body copyRequest) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+destSessionID+"/artifacts/copy", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestArtifactCopy_Success(t *testing.T) {
	h, artSvc, sessRepo := setupArtifactHandler(t)
	ctx := context.Background()
	srcSess, dstSess := idgen.New(), idgen.New()

	_ = sessRepo.Create(ctx, &store.Session{ID: srcSess, UserID: "sam_dev_user"})
	srcLoc := artSvc.Resolve("", "sam_dev_user", srcSess, "report.csv")
	_, _ = artSvc.Inner.Save(ctx, srcLoc, []byte("a,b,c\n1,2,3"), "text/csv")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, dstSess, copyRequest{
		SourceSessionID: srcSess,
		Filenames:       []string{"report.csv"},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp copyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Copied) != 1 || resp.Copied[0] != "report.csv" {
		t.Errorf("expected copied=[report.csv], got %v", resp.Copied)
	}
	if len(resp.Failed) != 0 {
		t.Errorf("expected no failures, got %v", resp.Failed)
	}

	dstLoc := artSvc.Resolve("", "sam_dev_user", dstSess, "report.csv")
	content, mimeType, err := artSvc.Inner.Load(ctx, dstLoc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "a,b,c\n1,2,3" {
		t.Errorf("expected copied content, got %q", string(content))
	}
	if mimeType != "text/csv" {
		t.Errorf("expected text/csv, got %s", mimeType)
	}
}

func TestArtifactCopy_MultipleFiles(t *testing.T) {
	h, artSvc, sessRepo := setupArtifactHandler(t)
	ctx := context.Background()
	src, dst := idgen.New(), idgen.New()

	_ = sessRepo.Create(ctx, &store.Session{ID: src, UserID: "sam_dev_user"})
	loc1 := artSvc.Resolve("", "sam_dev_user", src, "file1.txt")
	_, _ = artSvc.Inner.Save(ctx, loc1, []byte("content1"), "text/plain")
	loc2 := artSvc.Resolve("", "sam_dev_user", src, "file2.txt")
	_, _ = artSvc.Inner.Save(ctx, loc2, []byte("content2"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, dst, copyRequest{
		SourceSessionID: src,
		Filenames:       []string{"file1.txt", "file2.txt"},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp copyResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Copied) != 2 {
		t.Errorf("expected 2 copied, got %d", len(resp.Copied))
	}
	if len(resp.Failed) != 0 {
		t.Errorf("expected no failures, got %v", resp.Failed)
	}
}

func TestArtifactCopy_PartialFailure(t *testing.T) {
	h, artSvc, sessRepo := setupArtifactHandler(t)
	ctx := context.Background()
	src, dst := idgen.New(), idgen.New()

	_ = sessRepo.Create(ctx, &store.Session{ID: src, UserID: "sam_dev_user"})
	loc := artSvc.Resolve("", "sam_dev_user", src, "exists.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("data"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, dst, copyRequest{
		SourceSessionID: src,
		Filenames:       []string{"exists.txt", "missing.txt"},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp copyResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Copied) != 1 || resp.Copied[0] != "exists.txt" {
		t.Errorf("expected copied=[exists.txt], got %v", resp.Copied)
	}
	if len(resp.Failed) != 1 || resp.Failed[0] != "missing.txt" {
		t.Errorf("expected failed=[missing.txt], got %v", resp.Failed)
	}
}

func TestArtifactCopy_UserPrefixedSkipped(t *testing.T) {
	h, _, sessRepo := setupArtifactHandler(t)
	ctx := context.Background()
	src, dst := idgen.New(), idgen.New()

	_ = sessRepo.Create(ctx, &store.Session{ID: src, UserID: "sam_dev_user"})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, dst, copyRequest{
		SourceSessionID: src,
		Filenames:       []string{"user:shared-doc.pdf"},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp copyResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Copied) != 1 || resp.Copied[0] != "user:shared-doc.pdf" {
		t.Errorf("expected user-prefixed file in copied, got %v", resp.Copied)
	}
}

func TestArtifactCopy_SourceSessionNotFound(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, idgen.New(), copyRequest{
		SourceSessionID: idgen.New(),
		Filenames:       []string{"file.txt"},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactCopy_SourceSessionWrongOwner(t *testing.T) {
	h, _, sessRepo := setupArtifactHandler(t)
	ctx := context.Background()
	otherSess := idgen.New()

	_ = sessRepo.Create(ctx, &store.Session{ID: otherSess, UserID: "other_user"})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, idgen.New(), copyRequest{
		SourceSessionID: otherSess,
		Filenames:       []string{"secret.txt"},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactCopy_MissingSourceSessionID(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, "dst", copyRequest{
		SourceSessionID: "",
		Filenames:       []string{"file.txt"},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactCopy_EmptyFilenames(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, "dst", copyRequest{
		SourceSessionID: "src",
		Filenames:       []string{},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactCopy_InvalidJSON(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/dst/artifacts/copy",
		bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactCopy_AutoCreatesDestSession(t *testing.T) {
	h, artSvc, sessRepo := setupArtifactHandler(t)
	ctx := context.Background()
	src, newDest := idgen.New(), idgen.New()

	_ = sessRepo.Create(ctx, &store.Session{ID: src, UserID: "sam_dev_user"})
	loc := artSvc.Resolve("", "sam_dev_user", src, "file.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("data"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, newDest, copyRequest{
		SourceSessionID: src,
		Filenames:       []string{"file.txt"},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	sess, err := sessRepo.GetByID(ctx, newDest)
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("expected destination session to be auto-created")
	}
	if sess.UserID != "sam_dev_user" {
		t.Errorf("expected auto-created session user=sam_dev_user, got %s", sess.UserID)
	}
}

func TestArtifactCopy_NilSessRepo(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	memStore := memory.New()
	artSvc := &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}

	h := &artifactHandler{
		core:     core,
		artSvc:   artSvc,
		sessRepo: nil,
		logger:   adapter.logger,
	}

	ctx := context.Background()
	loc := artSvc.Resolve("", "sam_dev_user", "src", "file.txt")
	_, _ = memStore.Save(ctx, loc, []byte("data"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	rec := httptest.NewRecorder()
	req := makeCopyRequest(t, "dst", copyRequest{
		SourceSessionID: "src",
		Filenames:       []string{"file.txt"},
	})
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp copyResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Copied) != 1 {
		t.Errorf("expected 1 copied, got %d", len(resp.Copied))
	}
}
