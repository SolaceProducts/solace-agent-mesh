// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestSessionSearch_MissingQueryParam_ErrorNamesParam(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/search", sessH.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/search?q=typo", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var errResp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errResp.Message, `"query"`) {
		t.Errorf("error detail does not mention the literal parameter name \"query\": %s", errResp.Message)
	}
}

func TestSessionSearch_ValidQueryParam_Returns200(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/search", sessH.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/search?query=hello", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSessionGet_ValidUUID_NotFound_Returns404(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}", sessH.handleGet)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+idgen.New(), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestProjectGet_ValidUUID_NotFound_Returns404(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	db := adapter.core.Store()

	projH := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(db),
		projectUserRepo: store.NewProjectUserRepo(db),
		sessRepo:        store.NewSessionRepo(db),
		logger:          slog.Default(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}", projH.handleGet)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+idgen.New(), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestArtifactList_ValidUUID_Returns200(t *testing.T) {
	adapter, _, sessRepo, _, _ := setupTestAdapterWithDB(t)

	artSvc := &artifact.ScopedService{
		Inner:     memory.New(),
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	artH := &artifactHandler{
		core:     adapter.core,
		artSvc:   artSvc,
		sessRepo: sessRepo,
		logger:   slog.Default(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", artH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+idgen.New(), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestArtifactInfo_LastModified_Populated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	inner := memory.New()
	artSvc := &artifact.ScopedService{
		Inner:     inner,
		Scope:     "namespace",
		Namespace: "test-ns",
	}

	loc := artSvc.Resolve("", "user1", "session1", "file.txt")
	if _, err := inner.Save(ctx, loc, []byte("hello"), "text/plain"); err != nil {
		t.Fatal(err)
	}

	h := &artifactHandler{artSvc: artSvc, logger: slog.Default()}
	info := h.getArtifactInfo(ctx, loc)

	if info.LastModified == nil {
		t.Fatal("LastModified should not be nil after saving an artifact")
	}
	if *info.LastModified == "" {
		t.Error("LastModified should not be empty string")
	}
}
