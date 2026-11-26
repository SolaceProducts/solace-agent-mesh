// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func setupArtifactHandler(t *testing.T) (*artifactHandler, *artifact.ScopedService, *store.SessionRepo) {
	t.Helper()
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}

	h := &artifactHandler{
		core:     core,
		artSvc:   artSvc,
		sessRepo: sessRepo,
		logger:   adapter.logger,
	}
	return h, artSvc, sessRepo
}

func multipartUpload(t *testing.T, filename, content, sessionID string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("uploadFile", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(content))
	if sessionID != "" {
		_ = w.WriteField("sessionId", sessionID)
	}
	_ = w.WriteField("filename", filename)
	_ = w.Close()

	req := httptest.NewRequest("POST", "/api/v1/artifacts/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestArtifactUpload(t *testing.T) {
	sess1 := idgen.New()
	h, _, sessRepo := setupArtifactHandler(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: sess1, UserID: "sam_dev_user"})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/artifacts/upload", h.handleUpload)

	req := multipartUpload(t, "hello.txt", "Hello, World!", sess1)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ArtifactUploadResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Filename != "hello.txt" {
		t.Errorf("expected filename=hello.txt, got %s", resp.Filename)
	}
	if resp.Size != 13 {
		t.Errorf("expected size=13, got %d", resp.Size)
	}
	if resp.SessionID != sess1 {
		t.Errorf("expected sessionId=sess1, got %s", resp.SessionID)
	}
	if resp.URI == "" {
		t.Error("expected non-empty URI")
	}
	if resp.CreatedTime == "" {
		t.Error("expected non-empty createdTime")
	}
}

func TestArtifactUpload_AutoCreateSession(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/artifacts/upload", h.handleUpload)

	req := multipartUpload(t, "auto.txt", "content", "")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ArtifactUploadResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.SessionID == "" {
		t.Error("expected auto-generated sessionId")
	}
}

func TestArtifactUpload_RejectsInternalArtifactNames(t *testing.T) {
	cases := []string{
		"report.metadata.json",
		"data.converted.txt",
		"project_bm25_index.zip",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			h, artSvc, sessRepo := setupArtifactHandler(t)
			sess := idgen.New()
			_ = sessRepo.Create(context.Background(), &store.Session{ID: sess, UserID: "sam_dev_user"})

			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/v1/artifacts/upload", h.handleUpload)

			req := multipartUpload(t, name, `{"poisoned":"yes"}`, sess)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 rejecting %q, got %d; body: %s", name, rec.Code, rec.Body.String())
			}

			scope := artifact.KeyScope{
				AppName:   artSvc.Namespace,
				UserID:    "sam_dev_user",
				SessionID: sess,
			}
			keys, _ := artSvc.Inner.ListKeys(context.Background(), scope)
			for _, k := range keys {
				if k == name {
					t.Errorf("artifact %q was persisted despite 400", name)
				}
			}
		})
	}
}

func TestArtifactUpload_RejectsUnsafeFilenames(t *testing.T) {
	cases := []string{
		"../../etc/passwd",
		"../secret.txt",
		"foo/bar.txt",
		`..\windows\system32`,
		"..",
		"   ",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			h, artSvc, sessRepo := setupArtifactHandler(t)
			sess := idgen.New()
			_ = sessRepo.Create(context.Background(), &store.Session{ID: sess, UserID: "sam_dev_user"})

			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/v1/artifacts/upload", h.handleUpload)

			req := multipartUpload(t, name, "data", sess)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 rejecting unsafe filename %q, got %d; body: %s", name, rec.Code, rec.Body.String())
			}

			scope := artifact.KeyScope{
				AppName:   artSvc.Namespace,
				UserID:    "sam_dev_user",
				SessionID: sess,
			}
			keys, _ := artSvc.Inner.ListKeys(context.Background(), scope)
			if len(keys) != 0 {
				t.Errorf("unsafe filename %q was persisted (keys=%v) despite 400", name, keys)
			}
		})
	}
}

func TestArtifactUpload_MissingFile(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/artifacts/upload", h.handleUpload)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/artifacts/upload", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestArtifactUpload_StructuredGetsSchema(t *testing.T) {
	h, artSvc, sessRepo := setupArtifactHandler(t)
	sess := idgen.New()
	_ = sessRepo.Create(context.Background(), &store.Session{ID: sess, UserID: "sam_dev_user"})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/artifacts/upload", h.handleUpload)

	stimYAML := "invocation_details:\n    task_id: abc\n    status: completed\n"
	req := multipartUpload(t, "events.stim", stimYAML, sess)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp ArtifactUploadResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.MIMEType != "application/x-yaml" {
		t.Errorf(".stim MIME = %q, want application/x-yaml", resp.MIMEType)
	}

	meta := artifact.LoadCompanionMetadata(context.Background(), artSvc, "", "sam_dev_user", sess, "events.stim", nil)
	if meta == nil {
		t.Fatal("expected companion metadata for .stim upload")
	}
	schema, ok := meta["schema"].(map[string]any)
	if !ok {
		t.Fatalf("expected inferred schema in metadata, got %#v", meta["schema"])
	}
	if schema["type"] != "application/x-yaml" {
		t.Errorf("schema type = %v, want application/x-yaml", schema["type"])
	}
	if inferred, _ := schema["inferred"].(bool); !inferred {
		t.Errorf("expected schema.inferred = true, got %v", schema["inferred"])
	}

	req2 := multipartUpload(t, "notes.txt", "just prose", sess)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("expected 201 for notes.txt, got %d", rec2.Code)
	}
	txtMeta := artifact.LoadCompanionMetadata(context.Background(), artSvc, "", "sam_dev_user", sess, "notes.txt", nil)
	if txtMeta != nil {
		if _, hasSchema := txtMeta["schema"]; hasSchema {
			t.Errorf("plain text should not carry a schema, got %#v", txtMeta["schema"])
		}
	}
}

func TestArtifactList(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "file1.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("content1"), "text/plain")
	loc2 := artSvc.Resolve("", "sam_dev_user", sess1, "file2.csv")
	_, _ = artSvc.Inner.Save(ctx, loc2, []byte("a,b,c"), "text/csv")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[ArtifactInfoResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(resp.Data))
	}

	for _, a := range resp.Data {
		if a.Filename == "" {
			t.Error("expected non-empty filename")
		}
		if a.URI == nil || *a.URI == "" {
			t.Error("expected non-empty URI")
		}
	}
}

func TestArtifactList_Empty(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+idgen.New(), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[ArtifactInfoResponse]
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 0 {
		t.Errorf("expected empty list, got %d items", len(resp.Data))
	}
}

func TestArtifactList_FiltersInternalFiles(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "report.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("report"), "text/plain")
	internal := artSvc.Resolve("", "sam_dev_user", sess1, "report.converted.txt")
	_, _ = artSvc.Inner.Save(ctx, internal, []byte("converted"), "text/plain")
	index := artSvc.Resolve("", "sam_dev_user", sess1, "project_bm25_index.zip")
	_, _ = artSvc.Inner.Save(ctx, index, []byte("index"), "application/zip")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1, nil)
	mux.ServeHTTP(rec, req)

	var resp PaginatedResponse[ArtifactInfoResponse]
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 artifact (internal files filtered), got %d", len(resp.Data))
	}
	if len(resp.Data) > 0 && resp.Data[0].Filename != "report.txt" {
		t.Errorf("expected report.txt, got %s", resp.Data[0].Filename)
	}
}

func TestArtifactList_FiltersWorkingTagged(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sess1 := idgen.New()

	loc := artSvc.Resolve("", "sam_dev_user", sess1, "report.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("report"), "text/plain")
	_, err := artifact.SaveWithMetadata(ctx, artSvc, "", "sam_dev_user", sess1,
		"pdf_extract_text_result_1780925321706_f38a1b2c.json",
		[]byte(`{"big":"payload"}`), "application/json",
		artifact.MetadataOpts{
			Description: "Auto-spilled tool result (data exceeded max return size)",
			Tags:        []string{a2a.ArtifactTagWorking},
		}, logger)
	if err != nil {
		t.Fatalf("save working-tagged artifact: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1, nil)
	mux.ServeHTTP(rec, req)

	var resp PaginatedResponse[ArtifactInfoResponse]
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 1 {
		names := make([]string, 0, len(resp.Data))
		for _, a := range resp.Data {
			names = append(names, a.Filename)
		}
		t.Fatalf("expected 1 artifact (__working filtered), got %d: %v", len(resp.Data), names)
	}
	if resp.Data[0].Filename != "report.txt" {
		t.Errorf("expected report.txt, got %s", resp.Data[0].Filename)
	}
}

func TestArtifactList_VisibilityFilter(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	ctx := t.Context()
	sess := idgen.New()

	upload := artSvc.Resolve("", "sam_dev_user", sess, "report.txt")
	if _, err := artSvc.Inner.Save(ctx, upload, []byte("report"), "text/plain"); err != nil {
		t.Fatalf("save upload: %v", err)
	}
	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", "sam_dev_user", sess, "deck-spec.yaml",
		[]byte("- layout: title\n"), "application/yaml",
		artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagWorking}}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("save working-tagged artifact: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)
	list := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/"+sess+query, nil))
		return rec
	}
	filenames := func(t *testing.T, rec *httptest.ResponseRecorder) map[string][]string {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
		}
		var resp PaginatedResponse[ArtifactInfoResponse]
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		tagsByName := make(map[string][]string, len(resp.Data))
		for _, a := range resp.Data {
			tagsByName[a.Filename] = a.Tags
		}
		return tagsByName
	}

	for _, query := range []string{"", "?visibility=visible"} {
		t.Run("default hides working"+query, func(t *testing.T) {
			got := filenames(t, list(query))
			if _, ok := got["deck-spec.yaml"]; ok || len(got) != 1 {
				t.Errorf("listing = %v, want only report.txt", got)
			}
		})
	}

	t.Run("all includes working with its tag", func(t *testing.T) {
		got := filenames(t, list("?visibility=all"))
		if len(got) != 2 || !slices.Contains(got["deck-spec.yaml"], a2a.ArtifactTagWorking) {
			t.Errorf("listing = %v, want report.txt plus deck-spec.yaml tagged __working", got)
		}
	})

	t.Run("unknown value is rejected", func(t *testing.T) {
		rec := list("?visibility=everything")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		var resp ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.ValidationDetails["visibility"]) == 0 || resp.ErrorID == "" {
			t.Errorf("error = %+v, want a visibility validation detail and an errorId", resp)
		}
	})
}

func TestArtifactList_VisibilityFilter_ProjectScope(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	artSvc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "test-ns"}
	projectRepo := store.NewProjectRepo(core.Store())
	h := &artifactHandler{
		core:        core,
		artSvc:      artSvc,
		sessRepo:    sessRepo,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	ctx := t.Context()
	const owner = "sam_dev_user"
	projectID := idgen.New()
	if err := projectRepo.Create(ctx, &store.Project{ID: projectID, Name: "Visibility", UserID: owner}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	storageSessionID := projectSessionPrefix + projectID
	upload := artSvc.Resolve("", owner, storageSessionID, "report.txt")
	if _, err := artSvc.Inner.Save(ctx, upload, []byte("report"), "text/plain"); err != nil {
		t.Fatalf("save upload: %v", err)
	}
	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", owner, storageSessionID, "deck-spec.yaml",
		[]byte("- layout: title\n"), "application/yaml",
		artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagWorking}}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("save working-tagged artifact: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)
	list := func(userID, query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/artifacts/null?projectId="+projectID+query, nil)
		mux.ServeHTTP(rec, withIdentity(req, userID))
		return rec
	}
	decode := func(t *testing.T, rec *httptest.ResponseRecorder) PaginatedResponse[ArtifactInfoResponse] {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
		}
		var resp PaginatedResponse[ArtifactInfoResponse]
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	t.Run("default hides working", func(t *testing.T) {
		resp := decode(t, list(owner, ""))
		if len(resp.Data) != 1 || resp.Data[0].Filename != "report.txt" {
			t.Errorf("listing = %+v, want only report.txt", resp.Data)
		}
	})

	t.Run("all includes working with its tag", func(t *testing.T) {
		resp := decode(t, list(owner, "&visibility=all"))
		tagsByName := make(map[string][]string, len(resp.Data))
		for _, a := range resp.Data {
			tagsByName[a.Filename] = a.Tags
		}
		if _, ok := tagsByName["report.txt"]; !ok || len(tagsByName) != 2 ||
			!slices.Contains(tagsByName["deck-spec.yaml"], a2a.ArtifactTagWorking) {
			t.Errorf("listing = %v, want report.txt plus deck-spec.yaml tagged __working", tagsByName)
		}
		if resp.Meta.Pagination.Count != 2 {
			t.Errorf("meta.pagination.count = %d, want 2", resp.Meta.Pagination.Count)
		}
	})

	t.Run("all does not bypass project access", func(t *testing.T) {
		rec := list("stranger@example.com", "&visibility=all")
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403: %s", rec.Code, rec.Body)
		}
	})

	t.Run("a shared viewer lists the owner's working files", func(t *testing.T) {
		core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true})
		resp := decode(t, list("viewer@example.com", "&visibility=all"))
		names := make([]string, 0, len(resp.Data))
		for _, a := range resp.Data {
			names = append(names, a.Filename)
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{"deck-spec.yaml", "report.txt"}) {
			t.Errorf("listing = %v, want the owner's report.txt and deck-spec.yaml", names)
		}
	})
}

func TestArtifactList_ClientGoneIsNotAnError(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	var logs bytes.Buffer
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sessionID := idgen.New()
	if _, err := artSvc.Inner.Save(t.Context(), artSvc.Resolve("", "sam_dev_user", sessionID, "report.txt"), []byte("report"), "text/plain"); err != nil {
		t.Fatalf("save: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/artifacts/"+sessionID+"?visibility=all", nil)
	mux.ServeHTTP(rec, withIdentity(req, "sam_dev_user"))

	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("canceled list request logged at ERROR:\n%s", logs.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("canceled list request wrote a body nobody reads: %s", rec.Body)
	}
}

func TestArtifactGetLatest(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "hello.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("v0"), "text/plain")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("v1-latest"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/hello.txt", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	body, _ := io.ReadAll(rec.Body)
	if string(body) != "v1-latest" {
		t.Errorf("expected latest version content 'v1-latest', got %q", string(body))
	}
	if rec.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("expected Content-Type=text/plain, got %s", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Content-Disposition") == "" {
		t.Error("expected Content-Disposition header")
	}
}

func TestArtifactGetLatest_NotFound(t *testing.T) {
	sess1 := idgen.New()
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/nonexistent.txt", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestArtifactGetLatest_ProjectOverride(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	projectID := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", "project-"+projectID, "profileimg.jpg")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("img-bytes"), "image/jpeg")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/null/profileimg.jpg?projectId="+projectID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "img-bytes" {
		t.Errorf("expected project artifact content, got %q", string(body))
	}
}

func TestArtifactGetLatest_ProjectOverride_InvalidUUID(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/null/file.txt?projectId=not-a-uuid", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed projectId, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactGetLatest_ProjectOverride_NotFound(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	h := &artifactHandler{
		core:        core,
		artSvc:      artSvc,
		sessRepo:    sessRepo,
		projectRepo: store.NewProjectRepo(core.Store()),
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/null/file.txt?projectId="+idgen.New(), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing project, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactGetLatest_ProjectOverride_Forbidden(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	projectRepo := store.NewProjectRepo(core.Store())
	h := &artifactHandler{
		core:        core,
		artSvc:      artSvc,
		sessRepo:    sessRepo,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	ctx := context.Background()
	projectID := idgen.New()
	if err := projectRepo.Create(ctx, &store.Project{ID: projectID, Name: "Other's Project", UserID: "other_user"}); err != nil {
		t.Fatal(err)
	}
	loc := artSvc.Resolve("", "other_user", "project-"+projectID, "secret.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("secret-bytes"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/null/secret.txt?projectId="+projectID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for another user's project, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactGetLatest_ProjectOverride_Owned(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	projectRepo := store.NewProjectRepo(core.Store())
	h := &artifactHandler{
		core:        core,
		artSvc:      artSvc,
		sessRepo:    sessRepo,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	ctx := context.Background()
	projectID := idgen.New()
	if err := projectRepo.Create(ctx, &store.Project{ID: projectID, Name: "Mine", UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	loc := artSvc.Resolve("", "sam_dev_user", "project-"+projectID, "mine.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("mine-bytes"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/null/mine.txt?projectId="+projectID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "mine-bytes" {
		t.Errorf("expected 'mine-bytes', got %q", string(body))
	}
}

func TestArtifactGetLatest_ProjectOverride_SharedUser(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	projectRepo := store.NewProjectRepo(core.Store())
	h := &artifactHandler{
		core:        core,
		artSvc:      artSvc,
		sessRepo:    sessRepo,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	ctx := context.Background()
	projectID := idgen.New()
	if err := projectRepo.Create(ctx, &store.Project{ID: projectID, Name: "Shared Project", UserID: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	loc := artSvc.Resolve("", "owner@example.com", "project-"+projectID, "doc.txt")
	if _, err := artSvc.Inner.Save(ctx, loc, []byte("shared-content"), "text/plain"); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := withIdentity(
		httptest.NewRequest("GET", "/api/v1/artifacts/null/doc.txt?projectId="+projectID, nil),
		"alice@example.com",
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("shared user should download artifact (200); got %d; body: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "shared-content" {
		t.Errorf("expected 'shared-content', got %q", string(body))
	}
}

func TestArtifactList_ProjectOverride_SharedUser(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	projectRepo := store.NewProjectRepo(core.Store())
	h := &artifactHandler{
		core:        core,
		artSvc:      artSvc,
		sessRepo:    sessRepo,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	ctx := context.Background()
	projectID := idgen.New()
	if err := projectRepo.Create(ctx, &store.Project{ID: projectID, Name: "Shared Project", UserID: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	loc := artSvc.Resolve("", "owner@example.com", "project-"+projectID, "doc.txt")
	if _, err := artSvc.Inner.Save(ctx, loc, []byte("shared-content"), "text/plain"); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)

	rec := httptest.NewRecorder()
	req := withIdentity(
		httptest.NewRequest("GET", "/api/v1/artifacts/null?projectId="+projectID, nil),
		"alice@example.com",
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[ArtifactInfoResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Filename != "doc.txt" {
		t.Errorf("shared user should see owner's artifact; got %+v", resp.Data)
	}
}

func TestArtifactList_ProjectOverride_ViewAccessError(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	projectRepo := store.NewProjectRepo(core.Store())
	h := &artifactHandler{
		core:        core,
		artSvc:      artSvc,
		sessRepo:    sessRepo,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	ctx := context.Background()
	projectID := idgen.New()
	if err := projectRepo.Create(ctx, &store.Project{ID: projectID, Name: "Shared Project", UserID: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{viewAccessErr: errors.New("boom")})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)

	rec := httptest.NewRecorder()
	req := withIdentity(
		httptest.NewRequest("GET", "/api/v1/artifacts/null?projectId="+projectID, nil),
		"alice@example.com",
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on view-access error, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactListVersions(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "multi.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("v0"), "text/plain")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("v1"), "text/plain")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("v2"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions", h.handleListVersions)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/multi.txt/versions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var versionsResp PaginatedResponse[int]
	_ = json.NewDecoder(rec.Body).Decode(&versionsResp)
	if len(versionsResp.Data) != 3 {
		t.Errorf("expected 3 versions, got %d", len(versionsResp.Data))
	}
}

func TestArtifactGetVersion(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "versioned.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("version-zero"), "text/plain")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("version-one"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions/{version}", h.handleGetVersion)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/versioned.txt/versions/0", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "version-zero" {
		t.Errorf("expected 'version-zero', got %q", string(body))
	}
}

func TestArtifactGetVersion_Latest(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "latest.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("old"), "text/plain")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("new"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions/{version}", h.handleGetVersion)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/latest.txt/versions/latest", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "new" {
		t.Errorf("expected 'new', got %q", string(body))
	}
}

func TestArtifactGetVersion_InvalidVersion(t *testing.T) {
	sess1 := idgen.New()
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions/{version}", h.handleGetVersion)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/file.txt/versions/abc", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestArtifactByURI(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	loc := artSvc.Resolve("", "sam_dev_user", idgen.New(), "byuri.txt")
	ver, _ := artSvc.Inner.Save(ctx, loc, []byte("found-by-uri"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	uri := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		loc.AppName, loc.UserID, loc.SessionID, loc.Filename, ver)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/byUri?uri="+uri, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "found-by-uri" {
		t.Errorf("expected 'found-by-uri', got %q", string(body))
	}
}

func TestArtifactByURI_MissingParam(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/byUri", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestArtifactByURI_InvalidScheme(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/byUri?uri=http://example.com", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestArtifactByURI_PercentEncodedFilename(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	rawFilename := "Financial Sample.xlsx"
	loc := artSvc.Resolve("", "sam_dev_user", idgen.New(), rawFilename)
	ver, _ := artSvc.Inner.Save(ctx, loc, []byte("xlsx-bytes"), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	encodedURI := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		loc.AppName, loc.UserID, loc.SessionID, "Financial%20Sample.xlsx", ver)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/byUri?uri="+url.QueryEscape(encodedURI), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "xlsx-bytes" {
		t.Errorf("expected 'xlsx-bytes', got %q", string(body))
	}
}

func TestArtifactByURI_MissingVersion(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/byUri?uri=artifact://app/user/sess/file.txt", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (version required), got %d", rec.Code)
	}
}

func TestArtifactDelete(t *testing.T) {
	h, artSvc, sessRepo := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "delete-me.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("to be deleted"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/artifacts/{id}/{filename}", h.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/artifacts/"+sess1+"/delete-me.txt", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}

	content, _, _ := artSvc.Inner.Load(ctx, loc, nil)
	if content != nil {
		t.Error("expected artifact to be deleted")
	}
}

func TestArtifactDelete_WrongOwner(t *testing.T) {
	h, artSvc, sessRepo := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "other_user"})
	loc := artSvc.Resolve("", "other_user", sess1, "protected.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("protected"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/artifacts/{id}/{filename}", h.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/artifacts/"+sess1+"/protected.txt", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for wrong owner, got %d", rec.Code)
	}
}

func TestBuildArtifactURI(t *testing.T) {
	sess1 := idgen.New()
	loc := artifact.Locator{
		AppName:   "myapp",
		UserID:    "user1",
		SessionID: sess1,
		Filename:  "file.txt",
	}
	uri := buildArtifactURI(loc, 3)
	expected := "artifact://myapp/user1/" + sess1 + "/file.txt?version=3"
	if uri != expected {
		t.Errorf("expected %q, got %q", expected, uri)
	}
}

func TestGetArtifactInfoFromService(t *testing.T) {
	memStore := memory.New()
	artSvc := &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}

	ctx := context.Background()
	loc := artSvc.Resolve("", "user1", idgen.New(), "info.txt")
	ver, _ := memStore.Save(ctx, loc, []byte("hello"), "text/plain")
	_ = memStore.SaveMetadata(ctx, loc, ver, &artifact.VersionMetadata{
		MIMEType:    "text/plain",
		SizeBytes:   5,
		Description: "test file",
	})

	info := getArtifactInfoFromService(ctx, artSvc, loc)
	if info.Filename != "info.txt" {
		t.Errorf("expected filename=info.txt, got %s", info.Filename)
	}
	if info.MIMEType == nil || *info.MIMEType != "text/plain" {
		t.Error("expected mime_type=text/plain")
	}
	if info.Size != 5 {
		t.Errorf("expected size=5, got %d", info.Size)
	}
	if info.Description == nil || *info.Description != "test file" {
		t.Error("expected description='test file'")
	}
	if info.URI == nil || *info.URI == "" {
		t.Error("expected non-empty URI")
	}
}

func TestArtifactUpload_UserUploadedTag(t *testing.T) {
	h, artSvc, sessRepo := setupArtifactHandler(t)
	sess1 := idgen.New()
	_ = sessRepo.Create(context.Background(), &store.Session{ID: sess1, UserID: "sam_dev_user"})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/artifacts/upload", h.handleUpload)

	req := multipartUpload(t, "photo.png", "fake-png-data", sess1)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	metaLoc := artSvc.Resolve("", "sam_dev_user", sess1, "photo.png.metadata.json")
	metaContent, _, err := artSvc.Inner.Load(ctx, metaLoc, nil)
	if err != nil {
		t.Fatalf("failed to load metadata: %v", err)
	}
	if metaContent == nil {
		t.Fatal("expected companion metadata to exist")
	}

	var meta map[string]any
	if err := json.Unmarshal(metaContent, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	rawTags, ok := meta["tags"].([]any)
	if !ok {
		t.Fatalf("expected tags to be []any, got %T (%v)", meta["tags"], meta["tags"])
	}
	if len(rawTags) != 1 || rawTags[0] != "__user_uploaded" {
		t.Errorf("tags = %v, want [__user_uploaded]", rawTags)
	}
}

func TestArtifactList_IncludesTags(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	ctx := context.Background()
	sess1 := idgen.New()

	_, err := artifact.SaveWithMetadata(ctx, artSvc,
		"", "sam_dev_user", sess1, "report.csv",
		[]byte("a,b\n1,2"), "text/csv",
		artifact.MetadataOpts{
			Description:    "Test report",
			SuppressSchema: true,
			Tags:           []string{"__user_uploaded", "custom"},
		}, slog.Default())
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[ArtifactInfoResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(resp.Data))
	}
	if len(resp.Data[0].Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d: %v", len(resp.Data[0].Tags), resp.Data[0].Tags)
	}
	if resp.Data[0].Tags[0] != "__user_uploaded" || resp.Data[0].Tags[1] != "custom" {
		t.Errorf("tags = %v, want [__user_uploaded, custom]", resp.Data[0].Tags)
	}
}

func TestArtifactList_NoTagsWhenNone(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	ctx := context.Background()
	sess1 := idgen.New()

	loc := artSvc.Resolve("", "sam_dev_user", sess1, "plain.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("hello"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1, nil)
	mux.ServeHTTP(rec, req)

	var resp PaginatedResponse[ArtifactInfoResponse]
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(resp.Data))
	}
	if len(resp.Data[0].Tags) != 0 {
		t.Errorf("expected no tags, got %v", resp.Data[0].Tags)
	}
}

func TestExtractStringSlice(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want []string
	}{
		{"[]any with strings", map[string]any{"tags": []any{"a", "b"}}, "tags", []string{"a", "b"}},
		{"[]string", map[string]any{"tags": []string{"x", "y"}}, "tags", []string{"x", "y"}},
		{"nil value", map[string]any{"tags": nil}, "tags", nil},
		{"missing key", map[string]any{}, "tags", nil},
		{"wrong type", map[string]any{"tags": "not-a-slice"}, "tags", nil},
		{"empty []any", map[string]any{"tags": []any{}}, "tags", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractStringSlice(tt.m, tt.key)
			if len(tt.want) == 0 {
				if len(got) != 0 {
					t.Errorf("got %v, want nil/empty", got)
				}
			} else {
				if len(got) != len(tt.want) {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
				for i := range tt.want {
					if got[i] != tt.want[i] {
						t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestArtifactGetLatest_MaxBytes_NoTruncation(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "small.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("Hello"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/small.txt?maxBytes=100", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "Hello" {
		t.Errorf("expected full content 'Hello', got %q", string(body))
	}
	if rec.Header().Get("X-Truncated") != "" {
		t.Error("expected no X-Truncated header when content fits")
	}
	if rec.Header().Get("X-Original-Size") != "" {
		t.Error("expected no X-Original-Size header when content fits")
	}
}

func TestArtifactGetLatest_MaxBytes_Truncated(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	content := bytes.Repeat([]byte("A"), 100)
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "big.bin")
	_, _ = artSvc.Inner.Save(ctx, loc, content, "application/octet-stream")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/big.bin?maxBytes=50", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if len(body) != 50 {
		t.Errorf("expected 50 bytes, got %d", len(body))
	}
	if rec.Header().Get("X-Truncated") != "true" {
		t.Errorf("expected X-Truncated=true, got %q", rec.Header().Get("X-Truncated"))
	}
	if rec.Header().Get("X-Original-Size") != "100" {
		t.Errorf("expected X-Original-Size=100, got %q", rec.Header().Get("X-Original-Size"))
	}
}

func TestArtifactGetLatest_MaxBytes_NotProvided(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "full.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("full content here"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/full.txt", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "full content here" {
		t.Errorf("expected full content, got %q", string(body))
	}
	if rec.Header().Get("X-Truncated") != "" {
		t.Error("expected no truncation headers when max_bytes not set")
	}
}

func TestArtifactGetLatest_MaxBytes_InvalidValues(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "file.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("data"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	tests := []struct {
		name     string
		maxBytes string
	}{
		{"zero", "0"},
		{"negative", "-1"},
		{"too large", "2000000"},
		{"non-numeric", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET",
				fmt.Sprintf("/api/v1/artifacts/"+sess1+"/file.txt?maxBytes=%s", tt.maxBytes), nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("maxBytes=%s: expected 400, got %d", tt.maxBytes, rec.Code)
			}
		})
	}
}

func TestArtifactGetLatest_MaxBytes_PreservesUTF8(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	content := []byte("😀😀😀")
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "emoji.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, content, "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/emoji.txt?maxBytes=5", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)

	if string(body) != "😀" {
		t.Errorf("expected single emoji '😀' (4 bytes), got %q (%d bytes)", string(body), len(body))
	}
	if len(body) > 5 {
		t.Errorf("truncated content exceeds maxBytes=5: got %d bytes", len(body))
	}
	if rec.Header().Get("X-Truncated") != "true" {
		t.Error("expected X-Truncated=true")
	}
}

func TestArtifactGetLatest_MaxBytes_BinaryNotUTF8Sanitized(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	content := []byte{0xFF, 0xFE, 0xFD, 0xFC, 0xFB, 0xFA, 0xF9, 0xF8, 0xF7, 0xF6}
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "binary.bin")
	_, _ = artSvc.Inner.Save(ctx, loc, content, "application/octet-stream")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/binary.bin?maxBytes=5", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if len(body) != 5 {
		t.Errorf("expected exactly 5 bytes for binary, got %d", len(body))
	}
	if !bytes.Equal(body, content[:5]) {
		t.Errorf("expected first 5 bytes of binary content")
	}
}

func TestArtifactByURI_MaxBytes(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	loc := artSvc.Resolve("", "sam_dev_user", idgen.New(), "byuri.txt")
	ver, _ := artSvc.Inner.Save(ctx, loc, []byte("Hello, World! This is a long text."), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/byUri", h.handleByURI)

	uri := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		loc.AppName, loc.UserID, loc.SessionID, loc.Filename, ver)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET",
		fmt.Sprintf("/api/v1/artifacts/byUri?uri=%s&maxBytes=5", uri), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "Hello" {
		t.Errorf("expected 'Hello', got %q", string(body))
	}
	if rec.Header().Get("X-Truncated") != "true" {
		t.Error("expected X-Truncated=true")
	}
}

func TestArtifactGetVersion_MaxBytes(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	loc := artSvc.Resolve("", "sam_dev_user", sess1, "versioned.txt")
	_, _ = artSvc.Inner.Save(ctx, loc, []byte("version zero content here"), "text/plain")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions/{version}", h.handleGetVersion)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/versioned.txt/versions/0?maxBytes=7", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "version" {
		t.Errorf("expected 'version', got %q", string(body))
	}
	if rec.Header().Get("X-Truncated") != "true" {
		t.Error("expected X-Truncated=true")
	}
}

func TestSanitizeUTF8Truncation(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{
			"complete ASCII",
			[]byte("hello"),
			"hello",
		},
		{
			"complete 2-byte char",
			[]byte("café"),
			"café",
		},
		{
			"split 2-byte char (é = 0xC3 0xA9)",
			[]byte{'c', 'a', 'f', 0xC3},
			"caf",
		},
		{
			"complete 3-byte char (€ = 0xE2 0x82 0xAC)",
			[]byte("a€"),
			"a€",
		},
		{
			"split 3-byte char after 1 byte",
			[]byte{'a', 0xE2},
			"a",
		},
		{
			"split 3-byte char after 2 bytes",
			[]byte{'a', 0xE2, 0x82},
			"a",
		},
		{
			"complete 4-byte char (😀 = 0xF0 0x9F 0x98 0x80)",
			[]byte("😀"),
			"😀",
		},
		{
			"split 4-byte emoji after 1 byte",
			[]byte{0xF0},
			"",
		},
		{
			"split 4-byte emoji after 2 bytes",
			[]byte{0xF0, 0x9F},
			"",
		},
		{
			"split 4-byte emoji after 3 bytes",
			[]byte{0xF0, 0x9F, 0x98},
			"",
		},
		{
			"mixed: ASCII + split emoji",
			[]byte{'H', 'i', 0xF0, 0x9F},
			"Hi",
		},
		{
			"empty input",
			[]byte{},
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeUTF8Truncation(tt.input)
			if string(got) != tt.want {
				t.Errorf("sanitizeUTF8Truncation(%x) = %q (%x), want %q",
					tt.input, string(got), got, tt.want)
			}
		})
	}
}

func TestArtifactList_ProjectOverride_InvalidUUID(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/null?projectId=bad", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestArtifactListVersions_ProjectOverride_InvalidUUID(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions", h.handleListVersions)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET",
		"/api/v1/artifacts/null/file.txt/versions?projectId=bad", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestArtifactGetVersion_ProjectOverride_InvalidUUID(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}/versions/{version}", h.handleGetVersion)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET",
		"/api/v1/artifacts/null/file.txt/versions/0?projectId=bad", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestArtifactGetLatest_ProjectOverride_DBError(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	projectRepo := store.NewProjectRepo(core.Store())
	h := &artifactHandler{
		core: core, artSvc: artSvc, sessRepo: sessRepo,
		projectRepo: projectRepo, logger: adapter.logger,
	}
	_ = core.Store().Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET",
		"/api/v1/artifacts/null/file.txt?projectId="+idgen.New(), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when DB GetByID errors, got %d; body: %s",
			rec.Code, rec.Body.String())
	}
}

func TestArtifactDelete_SessionLookupError(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	_ = h.core.Store().Close()

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/artifacts/{id}/{filename}", h.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/artifacts/"+idgen.New()+"/file.txt", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when sessRepo errors, got %d; body: %s",
			rec.Code, rec.Body.String())
	}
}

func TestArtifactCopy_SourceSessionLookupError(t *testing.T) {
	h, _, _ := setupArtifactHandler(t)
	_ = h.core.Store().Close()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/artifacts/copy", h.handleCopy)

	body := fmt.Sprintf(`{"sourceSessionId":%q,"filenames":["x.txt"]}`, idgen.New())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST",
		"/api/v1/sessions/"+idgen.New()+"/artifacts/copy", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when source session lookup errors, got %d; body: %s",
			rec.Code, rec.Body.String())
	}
}

var pngBytes = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01, 0x02, 0x03}

func TestServeArtifact_ResolvesRecursiveDataURIEmbed(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()

	chartLoc := artSvc.Resolve("", "sam_dev_user", sess1, "status_distribution_chart.png")
	if _, err := artSvc.Inner.Save(ctx, chartLoc, pngBytes, "image/png"); err != nil {
		t.Fatalf("save chart: %v", err)
	}

	html := `<html><body><img src="«artifact_content:status_distribution_chart.png >>> format:datauri»"></body></html>`
	htmlLoc := artSvc.Resolve("", "sam_dev_user", sess1, "report.html")
	if _, err := artSvc.Inner.Save(ctx, htmlLoc, []byte(html), "text/html"); err != nil {
		t.Fatalf("save html: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/report.html", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	got := string(body)

	if !strings.Contains(got, "data:image/png;base64,") {
		t.Errorf("expected resolved data URI in body, got: %q", got)
	}
	if strings.Contains(got, "«artifact_content:") {
		t.Errorf("literal embed should be resolved away, got: %q", got)
	}
}

func TestServeArtifact_DataURIEmbed_SkippedWhenTruncated(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	chartLoc := artSvc.Resolve("", "sam_dev_user", sess1, "status_distribution_chart.png")
	_, _ = artSvc.Inner.Save(ctx, chartLoc, pngBytes, "image/png")

	imgTag := `<img src="«artifact_content:status_distribution_chart.png >>> format:datauri»">`
	html := imgTag + strings.Repeat("X", 2000)
	htmlLoc := artSvc.Resolve("", "sam_dev_user", sess1, "report.html")
	_, _ = artSvc.Inner.Save(ctx, htmlLoc, []byte(html), "text/html")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET",
		"/api/v1/artifacts/"+sess1+"/report.html?maxBytes="+strconv.Itoa(len(imgTag)+50), nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Truncated") != "true" {
		t.Fatalf("expected truncation to fire (X-Truncated=true), got %q", rec.Header().Get("X-Truncated"))
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "«artifact_content:") {
		t.Errorf("with maxBytes set, embed resolution is skipped; literal should survive, got: %q", string(body))
	}
}

func TestServeArtifact_DataURIEmbed_SkippedWhenResolutionDisabled(t *testing.T) {
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	core.Config().EnableEmbedResolution = false

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	h := &artifactHandler{core: core, artSvc: artSvc, sessRepo: sessRepo, logger: adapter.logger}

	ctx := context.Background()
	sess1 := idgen.New()
	chartLoc := artSvc.Resolve("", "sam_dev_user", sess1, "status_distribution_chart.png")
	_, _ = artSvc.Inner.Save(ctx, chartLoc, pngBytes, "image/png")
	html := `<img src="«artifact_content:status_distribution_chart.png >>> format:datauri»">`
	htmlLoc := artSvc.Resolve("", "sam_dev_user", sess1, "report.html")
	_, _ = artSvc.Inner.Save(ctx, htmlLoc, []byte(html), "text/html")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/report.html", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "«artifact_content:") {
		t.Errorf("with resolution disabled, literal embed should survive, got: %q", string(body))
	}
}

var _ = slog.Default

func TestServeArtifact_SAMConfigMIME_PreservesLateEmbed(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()

	const directive = `«artifact_content:{{fetch_image.output.artifact_filename}} >>> format:datauri»`
	cfg := "nodes:\n  - id: detect\n    input:\n      image: \"" + directive + "\"\n"
	cfgLoc := artSvc.Resolve("", "sam_dev_user", sess1, "traffic_camera_detection.yaml")
	_, _ = artSvc.Inner.Save(ctx, cfgLoc, []byte(cfg), "application/vnd.sam-workflow-config+yaml")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/traffic_camera_detection.yaml", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	got := rec.Body.String()
	if !strings.Contains(got, directive) {
		t.Errorf("config artifact must round-trip its late embed verbatim, got: %q", got)
	}
	if strings.Contains(got, "[Embed Error:") {
		t.Errorf("config artifact must not have embeds resolved on download, got: %q", got)
	}
}

func TestServeArtifact_NonConfigMIME_StillResolvesLateEmbed(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()

	chartLoc := artSvc.Resolve("", "sam_dev_user", sess1, "chart.png")
	_, _ = artSvc.Inner.Save(ctx, chartLoc, pngBytes, "image/png")

	html := `<img src="«artifact_content:chart.png >>> format:datauri»">`
	htmlLoc := artSvc.Resolve("", "sam_dev_user", sess1, "report.html")
	_, _ = artSvc.Inner.Save(ctx, htmlLoc, []byte(html), "text/html")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/report.html", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	got := rec.Body.String()
	if !strings.Contains(got, "data:image/png;base64,") {
		t.Errorf("ordinary text artifact should still resolve its embed, got: %q", got)
	}
}

func TestServeArtifact_SAMConfigMIME_PreservesResolvableLateEmbed(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()

	chartLoc := artSvc.Resolve("", "sam_dev_user", sess1, "chart.png")
	_, _ = artSvc.Inner.Save(ctx, chartLoc, pngBytes, "image/png")

	const directive = `«artifact_content:chart.png >>> format:datauri»`
	cfg := "nodes:\n  - id: detect\n    input:\n      image: \"" + directive + "\"\n"
	cfgLoc := artSvc.Resolve("", "sam_dev_user", sess1, "traffic_camera_detection.yaml")
	_, _ = artSvc.Inner.Save(ctx, cfgLoc, []byte(cfg), "application/vnd.sam-workflow-config+yaml")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess1+"/traffic_camera_detection.yaml", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	got := rec.Body.String()
	if !strings.Contains(got, directive) {
		t.Errorf("config artifact must round-trip a resolvable late embed verbatim, got: %q", got)
	}
	if strings.Contains(got, "data:image/png;base64,") {
		t.Errorf("config artifact must not resolve a late embed even when the target exists, got: %q", got)
	}
}
