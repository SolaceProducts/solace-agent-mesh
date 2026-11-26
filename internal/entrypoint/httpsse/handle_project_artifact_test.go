// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestProjectArtifactAdd_NonOwnerForbidden(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	projectID := idgen.New()
	if err := projectRepo.Create(ctx, &store.Project{ID: projectID, Name: "Owned Project", UserID: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}

	h := &projectArtifactHandler{
		core:        core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{id}/artifacts", h.handleAdd)

	rec := httptest.NewRecorder()
	req := withIdentity(
		httptest.NewRequest("POST", "/api/v1/projects/"+projectID+"/artifacts", nil),
		"alice@example.com",
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner upload, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestProjectListArtifacts_HidesWorkingTagged(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}

	projectRepo := store.NewProjectRepo(core.Store())
	ph := &projectHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		artSvc:      artSvc,
		logger:      adapter.logger,
	}

	ctx := t.Context()
	const userID = "sam_dev_user"
	project := &store.Project{Name: "Seasmoke", UserID: userID}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	storageSessionID := projectSessionPrefix + project.ID

	upLoc := artSvc.Resolve("", userID, storageSessionID, "cover-letter.pdf")
	if _, err := memStore.Save(ctx, upLoc, []byte("%PDF-1.4 ..."), "application/pdf"); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", userID, storageSessionID,
		"pdf_extract_text_result_1780925321706_f38a1b2c.json",
		[]byte(`{"big":"payload"}`), "application/json",
		artifact.MetadataOpts{
			Description: "Auto-spilled tool result (data exceeded max return size)",
			Tags:        []string{a2a.ArtifactTagWorking},
		}, logger); err != nil {
		t.Fatalf("seed spilled artifact: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/artifacts", ph.handleListArtifacts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/artifacts", nil)
	req.Header.Set("X-SAM-User-ID", userID)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[ArtifactInfoResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	names := make([]string, 0, len(resp.Data))
	for _, a := range resp.Data {
		names = append(names, a.Filename)
	}
	if len(resp.Data) != 1 || resp.Data[0].Filename != "cover-letter.pdf" {
		t.Fatalf("Knowledge listing should show only the user upload, got %v", names)
	}
}

func TestRebuildIndex_PreservesIndexWhenIndexableFilesRemain(t *testing.T) {
	store := memory.New()
	artSvc := &artifact.ScopedService{
		Inner:     store,
		Scope:     "namespace",
		Namespace: "test-ns",
	}

	const (
		userID    = "alice"
		projectID = "proj-2"
	)
	storageSessionID := "project-" + projectID

	srcLoc := artSvc.Resolve("", userID, storageSessionID, "notes.txt")
	if _, err := store.Save(context.Background(), srcLoc, []byte("hello world from the regression test"), "text/plain"); err != nil {
		t.Fatalf("seed source artifact: %v", err)
	}

	ix := &projectIndexer{
		artSvc: artSvc,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	ix.rebuildIndex(context.Background(), projectID, userID, "", "")

	indexLoc := artSvc.Resolve("", userID, storageSessionID, artifact.BM25IndexFilename)
	data, _, err := store.Load(context.Background(), indexLoc, nil)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected BM25 index to be written when indexable files exist")
	}
}

func TestCountProjectArtifacts_ExcludesInternalAndWorking(t *testing.T) {
	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	const userID = "sam_dev_user"
	const projectID = "proj-count"
	storageSessionID := projectSessionPrefix + projectID

	if _, err := artifact.SaveWithMetadata(t.Context(), artSvc, "", userID, storageSessionID,
		"cover-letter.pdf", []byte("%PDF-1.4 ..."), "application/pdf",
		artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagUserUploaded}}, logger); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	if _, err := artifact.SaveWithMetadata(t.Context(), artSvc, "", userID, storageSessionID,
		"pdf_extract_text_result_1780925321706_f38a1b2c.json",
		[]byte(`{"big":"payload"}`), "application/json",
		artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagWorking}}, logger); err != nil {
		t.Fatalf("seed spilled artifact: %v", err)
	}

	if got := countProjectArtifacts(t.Context(), artSvc, userID, userID, projectID); got != 1 {
		scope := artifact.KeyScope{AppName: artSvc.Namespace, UserID: userID, SessionID: storageSessionID}
		keys, _ := artSvc.Inner.ListKeys(t.Context(), scope)
		t.Fatalf("count = %d, want 1 (keys present: %v)", got, keys)
	}
}

func TestProjectArtifact_DeleteInvalidatesAllCache(t *testing.T) {
	_, core, sessRepo, _, _ := setupTestAdapterWithDB(t)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	projectRepo := store.NewProjectRepo(core.Store())
	allCache := newArtifactCache(30*time.Second, 200)
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	artifactH := &artifactHandler{core: core, artSvc: artSvc, sessRepo: sessRepo, projectRepo: projectRepo, allCache: allCache, logger: discard}
	projArtH := &projectArtifactHandler{
		core: core, projectRepo: projectRepo, artSvc: artSvc, logger: discard,
		allCache: allCache, indexer: &projectIndexer{artSvc: artSvc, logger: discard},
	}

	const userID = "sam_dev_user"
	ctx := context.Background()
	project := &store.Project{Name: "Docs", UserID: userID}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	storageSessionID := projectSessionPrefix + project.ID
	for _, name := range []string{"keep.png", "doomed.png"} {
		loc := artSvc.Resolve("", userID, storageSessionID, name)
		if _, err := memStore.Save(ctx, loc, []byte("img-"+name), "image/png"); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", artifactH.handleListAll)
	mux.HandleFunc("DELETE /api/v1/projects/{id}/artifacts/{filename}", projArtH.handleDelete)

	listAll := func() PaginatedResponse[ArtifactAllItemResponse] {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/all", nil))
		return decodeAllResponse(t, rec)
	}
	has := func(items []ArtifactAllItemResponse, name string) bool {
		for _, a := range items {
			if a.Filename == name {
				return true
			}
		}
		return false
	}

	if before := listAll(); !has(before.Data, "doomed.png") {
		t.Fatalf("setup: doomed.png should be listed before delete")
	}

	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, httptest.NewRequest("DELETE", "/api/v1/projects/"+project.ID+"/artifacts/doomed.png", nil))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d; body: %s", delRec.Code, delRec.Body.String())
	}

	after := listAll()
	if has(after.Data, "doomed.png") {
		t.Error("deleted project artifact still present on /artifacts/all (stale cache)")
	}
	if !has(after.Data, "keep.png") {
		t.Error("keep.png should still be present after deleting doomed.png")
	}
}
