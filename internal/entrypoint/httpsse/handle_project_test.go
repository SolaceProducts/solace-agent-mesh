// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestHandleProjectCreate(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	projectUserRepo := store.NewProjectUserRepo(core.Store())
	sessRepo := store.NewSessionRepo(core.Store())

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: projectUserRepo,
		sessRepo:        sessRepo,
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", ph.handleCreate)

	body := `{"name":"Test Project"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty id")
	}
	if resp.Name != "Test Project" {
		t.Errorf("expected name='Test Project', got %q", resp.Name)
	}
	if resp.UserID == "" {
		t.Error("expected non-empty userId")
	}
}

func TestHandleProjectList(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	projectUserRepo := store.NewProjectUserRepo(core.Store())

	ctx := context.Background()
	_ = projectRepo.Create(ctx, &store.Project{Name: "Project A", UserID: "sam_dev_user"})
	_ = projectRepo.Create(ctx, &store.Project{Name: "Project B", UserID: "sam_dev_user"})

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: projectUserRepo,
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", ph.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[ProjectResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Meta.Pagination.Count != 2 {
		t.Errorf("expected count=2, got %d", resp.Meta.Pagination.Count)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 projects, got %d", len(resp.Data))
	}
}

func TestHandleProjectGet(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	projectUserRepo := store.NewProjectUserRepo(core.Store())

	ctx := context.Background()
	project := &store.Project{Name: "My Project", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: projectUserRepo,
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}", ph.handleGet)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != project.ID {
		t.Errorf("expected id=%s, got %s", project.ID, resp.ID)
	}
	if resp.Name != "My Project" {
		t.Errorf("expected name='My Project', got %q", resp.Name)
	}
}

func TestHandleProjectUpdate(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	projectUserRepo := store.NewProjectUserRepo(core.Store())

	ctx := context.Background()
	project := &store.Project{Name: "Old Name", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: projectUserRepo,
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/projects/{id}", ph.handleUpdate)

	body := `{"name":"New Name","description":"A description"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/projects/"+project.ID, strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Name != "New Name" {
		t.Errorf("expected name='New Name', got %q", resp.Name)
	}
}

func TestHandleProjectUpdate_NotFound(t *testing.T) {
	nonexistent := idgen.New()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/projects/{id}", ph.handleUpdate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/projects/"+nonexistent, strings.NewReader(`{"name":"x"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleProjectUpdate_Forbidden(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Other User Project", UserID: "other_user"}
	_ = projectRepo.Create(ctx, project)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/projects/{id}", ph.handleUpdate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/projects/"+project.ID, strings.NewReader(`{"name":"x"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleProjectUpdate_InvalidBody(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "My Project", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/projects/{id}", ph.handleUpdate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/projects/"+project.ID, strings.NewReader("not json"))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleProjectPin(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Pin Me", UserID: "sam_dev_user"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/projects/{id}/pin", ph.handlePin)

	pin := func() ProjectResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PATCH", "/api/v1/projects/"+project.ID+"/pin", nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
		}
		var resp ProjectResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	if resp := pin(); !resp.IsPinned {
		t.Error("expected isPinned=true after first toggle")
	}
	if got, _ := projectRepo.GetByID(ctx, project.ID); got == nil || !got.IsPinned {
		t.Error("expected project row to be pinned after first toggle")
	}

	if resp := pin(); resp.IsPinned {
		t.Error("expected isPinned=false after second toggle")
	}
	if got, _ := projectRepo.GetByID(ctx, project.ID); got == nil || got.IsPinned {
		t.Error("expected project row to be unpinned after second toggle")
	}
}

func TestHandleProjectPin_NotFound(t *testing.T) {
	nonexistent := idgen.New()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/projects/{id}/pin", ph.handlePin)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/projects/"+nonexistent+"/pin", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleProjectPin_Forbidden(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Other Project", UserID: "other_user"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/projects/{id}/pin", ph.handlePin)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/projects/"+project.ID+"/pin", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleProjectPin_InvalidUUID(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/projects/{id}/pin", ph.handlePin)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/projects/not-a-uuid/pin", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleProjectGet_NotFound(t *testing.T) {
	nonexistent := idgen.New()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}", ph.handleGet)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+nonexistent, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleProjectGet_OtherUserNoAccess(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Private Project", UserID: "other_user"}
	_ = projectRepo.Create(ctx, project)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}", ph.handleGet)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleProjectDelete_NotFound(t *testing.T) {
	nonexistent := idgen.New()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/projects/{id}", ph.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/projects/"+nonexistent, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleProjectDelete_Forbidden(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Other Project", UserID: "other_user"}
	_ = projectRepo.Create(ctx, project)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/projects/{id}", ph.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/projects/"+project.ID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func withIdentity(req *http.Request, userID string, scopes ...string) *http.Request {
	id := auth.NewTestSubject(auth.SubjectFields{UserID: userID, Email: userID, Authenticated: true, Scopes: scopes})
	return req.WithContext(auth.ContextWithSubject(req.Context(), id))
}

func TestHandleProjectGet_SharedUserHasAccess(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Shared Project", UserID: "owner@example.com"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	mockSvc := &mockProjectSharingService{hasAccess: true}
	core.SetProjectSharingService(mockSvc)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}", ph.handleGet)

	rec := httptest.NewRecorder()
	req := withIdentity(
		httptest.NewRequest("GET", "/api/v1/projects/"+project.ID, nil),
		"alice@example.com",
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("shared user should see project (200); got %d; body: %s", rec.Code, rec.Body.String())
	}
	if mockSvc.lastViewAccessProjectID != project.ID {
		t.Errorf("HasViewAccess called with projectID=%s, want %s", mockSvc.lastViewAccessProjectID, project.ID)
	}
	if mockSvc.lastViewAccessUserID != "alice@example.com" {
		t.Errorf("HasViewAccess called with userID=%q, want %q", mockSvc.lastViewAccessUserID, "alice@example.com")
	}
}

func TestHandleProjectGet_ViewAccessError_500(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Shared Project", UserID: "owner@example.com"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	core.SetProjectSharingService(&mockProjectSharingService{viewAccessErr: errors.New("boom")})

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}", ph.handleGet)

	rec := httptest.NewRecorder()
	req := withIdentity(
		httptest.NewRequest("GET", "/api/v1/projects/"+project.ID, nil),
		"alice@example.com",
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on view-access error, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleListArtifacts_SharedUserSeesOwnersArtifacts(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Shared Project", UserID: "owner@example.com"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	artStore := memory.New()
	artSvc := &artifact.ScopedService{
		Inner:     artStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	storageSessionID := projectSessionPrefix + project.ID
	const artifactKey = "knowledge-doc.txt"
	ownerLoc := artSvc.Resolve("", project.UserID, storageSessionID, artifactKey)
	if _, err := artStore.Save(ctx, ownerLoc, []byte("knowledge content"), "text/plain"); err != nil {
		t.Fatalf("seed owner artifact: %v", err)
	}

	mockSvcArt := &mockProjectSharingService{hasAccess: true}
	core.SetProjectSharingService(mockSvcArt)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		artSvc:          artSvc,
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/artifacts", ph.handleListArtifacts)

	rec := httptest.NewRecorder()
	req := withIdentity(
		httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/artifacts", nil),
		"alice@example.com",
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("shared user should list artifacts (200); got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[ArtifactInfoResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 artifact under owner's namespace to be visible to shared user; got %d", len(resp.Data))
	}
	if resp.Data[0].Filename != artifactKey {
		t.Errorf("expected filename=%q, got %q", artifactKey, resp.Data[0].Filename)
	}
	if mockSvcArt.lastViewAccessProjectID != project.ID {
		t.Errorf("HasViewAccess called with projectID=%s, want %s", mockSvcArt.lastViewAccessProjectID, project.ID)
	}
	if mockSvcArt.lastViewAccessUserID != "alice@example.com" {
		t.Errorf("HasViewAccess called with userID=%q, want %q", mockSvcArt.lastViewAccessUserID, "alice@example.com")
	}
}

func TestHandleProjectDelete(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	projectUserRepo := store.NewProjectUserRepo(core.Store())

	ctx := context.Background()
	project := &store.Project{Name: "Delete Me", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: projectUserRepo,
		logger:          adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/projects/{id}", ph.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/projects/"+project.ID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Error("expected empty body for 204")
	}

	got, err := projectRepo.GetByID(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected project to be soft-deleted (nil from GetByID)")
	}
}

func TestHandleProjectCreate_MissingName(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", ph.handleCreate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(`{}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleProjectCreate_InvalidJSON(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", ph.handleCreate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader("not json"))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleProjectCreate_MultipartForm(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", ph.handleCreate)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "Multipart Project")
	_ = mw.WriteField("description", "from form")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Name != "Multipart Project" {
		t.Errorf("expected name='Multipart Project', got %q", resp.Name)
	}
}

func TestHandleProjectGet_InvalidUUID(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}", ph.handleGet)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/not-a-uuid", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleProjectUpdate_InvalidUUID(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/projects/{id}", ph.handleUpdate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/projects/not-a-uuid", strings.NewReader(`{"name":"x"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleProjectDelete_InvalidUUID(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/projects/{id}", ph.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/projects/not-a-uuid", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleListArtifacts_Owner(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Owner Project", UserID: "sam_dev_user"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	artStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: artStore, Scope: "namespace", Namespace: "test-ns"}
	storageSessionID := projectSessionPrefix + project.ID
	ownerLoc := artSvc.Resolve("", project.UserID, storageSessionID, "doc.txt")
	if _, err := artStore.Save(ctx, ownerLoc, []byte("content"), "text/plain"); err != nil {
		t.Fatal(err)
	}

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		artSvc:          artSvc,
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/artifacts", ph.handleListArtifacts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/artifacts", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[ArtifactInfoResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 artifact, got %d", len(resp.Data))
	}
}

func TestHandleListArtifacts_NoArtSvc(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Project", UserID: "sam_dev_user"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		artSvc:          nil,
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/artifacts", ph.handleListArtifacts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/artifacts", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp PaginatedResponse[ArtifactInfoResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected 0 artifacts, got %d", len(resp.Data))
	}
}

func TestHandleListArtifacts_NotFound(t *testing.T) {
	nonexistent := idgen.New()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/artifacts", ph.handleListArtifacts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+nonexistent+"/artifacts", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleListArtifacts_NoAccess(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Private Project", UserID: "other_user"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/artifacts", ph.handleListArtifacts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/artifacts", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleListArtifacts_InvalidUUID(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/artifacts", ph.handleListArtifacts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/not-a-uuid/artifacts", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleProjectList_IncludesShared(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	_ = projectRepo.Create(ctx, &store.Project{Name: "Mine", UserID: "sam_dev_user"})
	shared := &store.Project{Name: "Shared With Me", UserID: "other_user"}
	_ = projectRepo.Create(ctx, shared)

	mockSvc := &mockProjectSharingService{sharedIDs: []string{shared.ID}}
	core.SetProjectSharingService(mockSvc)

	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", ph.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp PaginatedResponse[ProjectResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 projects (owned + shared), got %d", len(resp.Data))
	}
}

func newProjectHandlerWithChatRepo(t *testing.T) (*projectHandler, *store.ProjectRepo) {
	t.Helper()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	projectRepo := store.NewProjectRepo(core.Store())
	return &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		sessRepo:        store.NewSessionRepo(core.Store()),
		logger:          adapter.logger,
	}, projectRepo
}

func TestProjectExport_ExcludesInternalAndWorking(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	projectRepo := store.NewProjectRepo(core.Store())
	ph := &projectHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		sessRepo:    store.NewSessionRepo(core.Store()),
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

	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", userID, storageSessionID,
		"cover-letter.pdf", []byte("%PDF-1.4 ..."), "application/pdf",
		artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagUserUploaded}}, logger); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", userID, storageSessionID,
		"pdf_extract_text_result_1780925321706_f38a1b2c.json",
		[]byte(`{"big":"payload"}`), "application/json",
		artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagWorking}}, logger); err != nil {
		t.Fatalf("seed spilled artifact: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/export", ph.handleExport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/export", nil)
	req.Header.Set("X-SAM-User-ID", userID)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("read export zip: %v", err)
	}
	var artifactEntries []string
	for _, f := range zr.File {
		if name, ok := strings.CutPrefix(f.Name, "artifacts/"); ok {
			artifactEntries = append(artifactEntries, name)
		}
	}

	if len(artifactEntries) != 1 || artifactEntries[0] != "cover-letter.pdf" {
		t.Fatalf("export should bundle only the user upload, got %v", artifactEntries)
	}
}

func readExportedProjectJSON(t *testing.T, zipBytes []byte) map[string]any {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("read export zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != "project.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open project.json: %v", err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("decode project.json: %v", err)
		}
		return raw
	}
	t.Fatalf("export zip missing project.json")
	return nil
}

func TestProjectExport_OmitsSessionsAddsArtifactManifest(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	projectRepo := store.NewProjectRepo(core.Store())
	ph := &projectHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		sessRepo:    store.NewSessionRepo(core.Store()),
		artSvc:      artSvc,
		logger:      adapter.logger,
	}

	ctx := t.Context()
	const userID = "sam_dev_user"
	project := &store.Project{Name: "animals proj", UserID: userID}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	storageSessionID := projectSessionPrefix + project.ID

	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", userID, storageSessionID,
		"clubs.txt", []byte("hello clubs"), "text/plain",
		artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagUserUploaded}}, logger); err != nil {
		t.Fatalf("seed upload: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/export", ph.handleExport)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/export", nil)
	req.Header.Set("X-SAM-User-ID", userID)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	raw := readExportedProjectJSON(t, rec.Body.Bytes())

	if _, ok := raw["sessions"]; ok {
		t.Errorf("project.json must not contain a \"sessions\" key (1.x parity); got %v", raw["sessions"])
	}
	artifacts, ok := raw["artifacts"].([]any)
	if !ok {
		t.Fatalf("project.json must contain an \"artifacts\" array; got %T (%v)", raw["artifacts"], raw["artifacts"])
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact in manifest, got %d", len(artifacts))
	}
	first, _ := artifacts[0].(map[string]any)
	if first["filename"] != "clubs.txt" {
		t.Errorf("manifest filename = %v, want clubs.txt", first["filename"])
	}
}

func TestHandleProjectExport(t *testing.T) {
	ph, projectRepo := newProjectHandlerWithChatRepo(t)
	ctx := context.Background()
	project := &store.Project{Name: "Export Me", UserID: "sam_dev_user"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/export", ph.handleExport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/export", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("expected Content-Type application/zip, got %q", ct)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("response is not a valid ZIP: %v", err)
	}
	var hasProjectJSON bool
	for _, f := range zr.File {
		if f.Name == "project.json" {
			hasProjectJSON = true
		}
	}
	if !hasProjectJSON {
		t.Error("exported ZIP missing project.json")
	}
}

func TestHandleProjectExport_NotFound(t *testing.T) {
	ph, _ := newProjectHandlerWithChatRepo(t)
	nonexistent := idgen.New()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/export", ph.handleExport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+nonexistent+"/export", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleProjectExport_Forbidden(t *testing.T) {
	ph, projectRepo := newProjectHandlerWithChatRepo(t)
	ctx := context.Background()
	project := &store.Project{Name: "Other Project", UserID: "other_user"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/export", ph.handleExport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/export", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestProjectExport_ManifestEntryFields(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	projectRepo := store.NewProjectRepo(core.Store())
	ph := &projectHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		sessRepo:    store.NewSessionRepo(core.Store()),
		artSvc:      artSvc,
		logger:      adapter.logger,
	}

	ctx := t.Context()
	const userID = "sam_dev_user"
	project := &store.Project{Name: "animals proj", UserID: userID}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	storageSessionID := projectSessionPrefix + project.ID

	content := []byte("dog brad pitt facts")
	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", userID, storageSessionID,
		"dog-bradPitt.docx", content,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		artifact.MetadataOpts{
			Description: "a very good dog",
			Source:      "project",
			Tags:        []string{a2a.ArtifactTagUserUploaded},
		}, logger); err != nil {
		t.Fatalf("seed upload: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/export", ph.handleExport)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/export", nil)
	req.Header.Set("X-SAM-User-ID", userID)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	raw := readExportedProjectJSON(t, rec.Body.Bytes())
	artifacts := raw["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 manifest entry, got %d", len(artifacts))
	}
	entry := artifacts[0].(map[string]any)

	if entry["filename"] != "dog-bradPitt.docx" {
		t.Errorf("filename = %v", entry["filename"])
	}
	if entry["mimeType"] != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Errorf("mimeType = %v", entry["mimeType"])
	}
	if size, _ := entry["size"].(float64); int(size) != len(content) {
		t.Errorf("size = %v, want %d", entry["size"], len(content))
	}
	meta, ok := entry["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata must be an object, got %T", entry["metadata"])
	}
	if meta["description"] != "a very good dog" {
		t.Errorf("metadata.description = %v", meta["description"])
	}
	if meta["source"] != "project" {
		t.Errorf("metadata.source = %v", meta["source"])
	}
}

func TestProjectExport_ProjectMetadataAndTimestamps(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	projectRepo := store.NewProjectRepo(core.Store())
	ph := &projectHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		sessRepo:    store.NewSessionRepo(core.Store()),
		artSvc:      artSvc,
		logger:      adapter.logger,
	}

	ctx := t.Context()
	const userID = "sam_dev_user"
	project := &store.Project{Name: "animals proj", UserID: userID}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	storageSessionID := projectSessionPrefix + project.ID

	clubs := []byte("clubs content")
	hearts := []byte("hearts content longer")
	for name, body := range map[string][]byte{"clubs.txt": clubs, "hearts.txt": hearts} {
		if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", userID, storageSessionID,
			name, body, "text/plain",
			artifact.MetadataOpts{Source: "project", Tags: []string{a2a.ArtifactTagUserUploaded}}, logger); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/export", ph.handleExport)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/export", nil)
	req.Header.Set("X-SAM-User-ID", userID)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	raw := readExportedProjectJSON(t, rec.Body.Bytes())

	if _, isString := raw["exportedAt"].(string); isString {
		t.Errorf("exportedAt must be epoch-ms number, got string %v", raw["exportedAt"])
	}
	if exportedAt, ok := raw["exportedAt"].(float64); !ok || exportedAt <= 0 {
		t.Errorf("exportedAt = %v (%T), want positive epoch-ms number", raw["exportedAt"], raw["exportedAt"])
	}

	proj := raw["project"].(map[string]any)
	meta, ok := proj["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("project.metadata must be an object, got %T", proj["metadata"])
	}
	if got, _ := meta["artifactCount"].(float64); int(got) != 2 {
		t.Errorf("artifactCount = %v, want 2", meta["artifactCount"])
	}
	if got, _ := meta["totalSizeBytes"].(float64); int64(got) != int64(len(clubs)+len(hearts)) {
		t.Errorf("totalSizeBytes = %v, want %d", meta["totalSizeBytes"], len(clubs)+len(hearts))
	}
	if got, _ := meta["originalCreatedAt"].(float64); int64(got) != project.CreatedAt {
		t.Errorf("originalCreatedAt = %v, want %d (project.CreatedAt epoch-ms)", meta["originalCreatedAt"], project.CreatedAt)
	}
}

func TestProjectExport_ManifestExcludesInternalAndWorking(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	projectRepo := store.NewProjectRepo(core.Store())
	ph := &projectHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		sessRepo:    store.NewSessionRepo(core.Store()),
		artSvc:      artSvc,
		logger:      adapter.logger,
	}

	ctx := t.Context()
	const userID = "sam_dev_user"
	project := &store.Project{Name: "animals proj", UserID: userID}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	storageSessionID := projectSessionPrefix + project.ID

	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", userID, storageSessionID,
		"spades.txt", []byte("spades"), "text/plain",
		artifact.MetadataOpts{Source: "project", Tags: []string{a2a.ArtifactTagUserUploaded}}, logger); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", userID, storageSessionID,
		"pdf_extract_text_result_1780925321706_f38a1b2c.json", []byte(`{"big":"payload"}`), "application/json",
		artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagWorking}}, logger); err != nil {
		t.Fatalf("seed working spill: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/export", ph.handleExport)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/export", nil)
	req.Header.Set("X-SAM-User-ID", userID)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	raw := readExportedProjectJSON(t, rec.Body.Bytes())
	artifacts := raw["artifacts"].([]any)
	if len(artifacts) != 1 {
		var names []string
		for _, a := range artifacts {
			names = append(names, a.(map[string]any)["filename"].(string))
		}
		t.Fatalf("manifest should list only the user upload, got %v", names)
	}
	if artifacts[0].(map[string]any)["filename"] != "spades.txt" {
		t.Errorf("manifest entry = %v, want spades.txt", artifacts[0])
	}
	meta := raw["project"].(map[string]any)["metadata"].(map[string]any)
	if got, _ := meta["artifactCount"].(float64); int(got) != 1 {
		t.Errorf("artifactCount = %v, want 1 (working spill excluded)", meta["artifactCount"])
	}
}

func buildProjectZIP(t *testing.T, projectJSON string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	pw, err := zw.Create("project.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write([]byte(projectJSON)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildImportRequest(t *testing.T, zipBytes []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "project.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(zipBytes); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/projects/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestHandleProjectImport(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		sessRepo:        store.NewSessionRepo(core.Store()),
		logger:          adapter.logger,
	}

	const projectJSON = `{"version":"1.0","exportedAt":1640995200000,"project":{"name":"Imported"},"artifacts":[]}`
	zipBytes := buildProjectZIP(t, projectJSON)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/import", ph.handleImport)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, buildImportRequest(t, zipBytes))

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["projectId"] == "" {
		t.Error("expected non-empty projectId in response")
	}
	if result["name"] != "Imported" {
		t.Errorf("expected name='Imported', got %v", result["name"])
	}
}

func TestHandleProjectImport_InvalidZIP(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		sessRepo:        store.NewSessionRepo(core.Store()),
		logger:          adapter.logger,
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "bad.zip")
	_, _ = fw.Write([]byte("not a zip"))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/import", ph.handleImport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleProjectImport_MissingProjectJSON(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     store.NewProjectRepo(core.Store()),
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		sessRepo:        store.NewSessionRepo(core.Store()),
		logger:          adapter.logger,
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_ = zw.Close()

	var reqBuf bytes.Buffer
	mw := multipart.NewWriter(&reqBuf)
	fw, _ := mw.CreateFormFile("file", "empty.zip")
	_, _ = fw.Write(buf.Bytes())
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/import", ph.handleImport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects/import", &reqBuf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestProjectImport_RestoresArtifactsFromManifest(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	projectRepo := store.NewProjectRepo(core.Store())
	ph := &projectHandler{
		core:            adapter.core,
		projectRepo:     projectRepo,
		projectUserRepo: store.NewProjectUserRepo(core.Store()),
		sessRepo:        store.NewSessionRepo(core.Store()),
		artSvc:          artSvc,
		logger:          adapter.logger,
	}

	ctx := t.Context()
	const userID = "sam_dev_user"
	sysPrompt := "you are a zoologist"
	defaultAgent := "agent-zoo"
	p1 := &store.Project{Name: "animals proj", UserID: userID, SystemPrompt: &sysPrompt, DefaultAgentID: &defaultAgent}
	if err := projectRepo.Create(ctx, p1); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", userID, projectSessionPrefix+p1.ID,
		"notes.dat", []byte("col1,col2\n1,2\n"), "text/csv",
		artifact.MetadataOpts{Description: "spreadsheet", Source: "project", Tags: []string{a2a.ArtifactTagUserUploaded}}, logger); err != nil {
		t.Fatalf("seed upload: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/export", ph.handleExport)
	mux.HandleFunc("POST /api/v1/projects/import", ph.handleImport)

	exportRec := httptest.NewRecorder()
	exportReq := httptest.NewRequest("GET", "/api/v1/projects/"+p1.ID+"/export", nil)
	exportReq.Header.Set("X-SAM-User-ID", userID)
	mux.ServeHTTP(exportRec, exportReq)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export: expected 200, got %d; body: %s", exportRec.Code, exportRec.Body.String())
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "project_animals.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(exportRec.Body.Bytes()); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	_ = mw.Close()

	importRec := httptest.NewRecorder()
	importReq := httptest.NewRequest("POST", "/api/v1/projects/import", &body)
	importReq.Header.Set("Content-Type", mw.FormDataContentType())
	importReq.Header.Set("X-SAM-User-ID", userID)
	mux.ServeHTTP(importRec, importReq)
	if importRec.Code != http.StatusCreated && importRec.Code != http.StatusAccepted {
		t.Fatalf("import: expected 201/202, got %d; body: %s", importRec.Code, importRec.Body.String())
	}

	var importResp map[string]any
	if err := json.Unmarshal(importRec.Body.Bytes(), &importResp); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	if _, ok := importResp["sessionsImported"]; ok {
		t.Errorf("import response must not carry sessionsImported (1.x parity); got %v", importResp["sessionsImported"])
	}
	if got, _ := importResp["artifactsImported"].(float64); int(got) != 1 {
		t.Errorf("artifactsImported = %v, want 1", importResp["artifactsImported"])
	}
	newProjectID, _ := importResp["projectId"].(string)
	if newProjectID == "" {
		t.Fatalf("import response missing projectId: %v", importResp)
	}

	reRec := httptest.NewRecorder()
	reReq := httptest.NewRequest("GET", "/api/v1/projects/"+newProjectID+"/export", nil)
	reReq.Header.Set("X-SAM-User-ID", userID)
	mux.ServeHTTP(reRec, reReq)
	if reRec.Code != http.StatusOK {
		t.Fatalf("re-export: expected 200, got %d; body: %s", reRec.Code, reRec.Body.String())
	}

	raw := readExportedProjectJSON(t, reRec.Body.Bytes())
	proj, _ := raw["project"].(map[string]any)
	if proj["systemPrompt"] != sysPrompt {
		t.Errorf("project.systemPrompt = %v, want %q (lost on round-trip)", proj["systemPrompt"], sysPrompt)
	}
	if proj["defaultAgentId"] != defaultAgent {
		t.Errorf("project.defaultAgentId = %v, want %q (lost on round-trip)", proj["defaultAgentId"], defaultAgent)
	}
	arts := raw["artifacts"].([]any)
	if len(arts) != 1 {
		t.Fatalf("re-export manifest should have 1 entry, got %d", len(arts))
	}
	entry := arts[0].(map[string]any)
	if entry["filename"] != "notes.dat" {
		t.Errorf("filename = %v, want notes.dat", entry["filename"])
	}
	if entry["mimeType"] != "text/csv" {
		t.Errorf("mimeType = %v, want text/csv (must come from manifest, not re-sniffed)", entry["mimeType"])
	}
	m, _ := entry["metadata"].(map[string]any)
	if m["description"] != "spreadsheet" {
		t.Errorf("metadata.description = %v, want \"spreadsheet\" (lost on import)", m["description"])
	}
	if m["source"] != "project" {
		t.Errorf("metadata.source = %v, want \"project\"", m["source"])
	}
	tags, _ := entry["tags"].([]any)
	if len(tags) != 1 || tags[0] != a2a.ArtifactTagUserUploaded {
		t.Errorf("tags = %v, want [%q] (lost on round-trip)", entry["tags"], a2a.ArtifactTagUserUploaded)
	}
}
