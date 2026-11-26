// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth/authtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sharing"
)

func setupPromptHandler(t *testing.T) (*promptHandler, *promptGroupHandler, *store.PromptGroupRepo, *store.PromptRepo) {
	t.Helper()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())

	ph := &promptHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		logger:     adapter.logger,
	}
	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		logger:     adapter.logger,
	}
	return ph, pgh, groupRepo, promptRepo
}

func createTestGroupWithPrompt(t *testing.T, groupRepo *store.PromptGroupRepo, promptRepo *store.PromptRepo) (*store.PromptGroup, *store.Prompt) {
	t.Helper()
	ctx := context.Background()

	group := &store.PromptGroup{Name: "Test Group", UserID: "sam_dev_user"}
	if err := groupRepo.Create(ctx, group); err != nil {
		t.Fatal(err)
	}

	prompt := &store.Prompt{
		PromptText: "initial prompt text",
		GroupID:    group.ID,
		UserID:     "sam_dev_user",
		Version:    1,
		Name:       &group.Name,
	}
	if err := promptRepo.Create(ctx, prompt); err != nil {
		t.Fatal(err)
	}

	if err := groupRepo.SetProductionPrompt(ctx, group.ID, prompt.ID); err != nil {
		t.Fatal(err)
	}
	group.ProductionPromptID = &prompt.ID

	return group, prompt
}

func TestPromptListVersions(t *testing.T) {
	ph, _, groupRepo, promptRepo := setupPromptHandler(t)
	group, _ := createTestGroupWithPrompt(t, groupRepo, promptRepo)

	p2 := &store.Prompt{PromptText: "v2 text", GroupID: group.ID, UserID: "sam_dev_user", Version: 2}
	_ = promptRepo.Create(context.Background(), p2)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prompts/groups/{id}/prompts", ph.handleListVersions)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/prompts/groups/"+group.ID+"/prompts", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[PromptResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 prompt versions, got %d", len(resp.Data))
	}
}

func TestPromptListVersions_GroupNotFound(t *testing.T) {
	ph, _, _, _ := setupPromptHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prompts/groups/{id}/prompts", ph.handleListVersions)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/prompts/groups/"+idgen.New()+"/prompts", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestPromptCreateVersion(t *testing.T) {
	ph, _, groupRepo, promptRepo := setupPromptHandler(t)
	group, _ := createTestGroupWithPrompt(t, groupRepo, promptRepo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/groups/{id}/prompts", ph.handleCreateVersion)

	body := `{"promptText":"new version text"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/groups/"+group.ID+"/prompts", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PromptResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.PromptText != "new version text" {
		t.Errorf("expected promptText='new version text', got %q", resp.PromptText)
	}
	if resp.Version != 2 {
		t.Errorf("expected version=2, got %d", resp.Version)
	}
	if resp.GroupID != group.ID {
		t.Errorf("expected groupId=%s, got %s", group.ID, resp.GroupID)
	}
}

func TestPromptCreateVersion_EmptyText(t *testing.T) {
	ph, _, groupRepo, promptRepo := setupPromptHandler(t)
	group, _ := createTestGroupWithPrompt(t, groupRepo, promptRepo)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/groups/{id}/prompts", ph.handleCreateVersion)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/groups/"+group.ID+"/prompts", strings.NewReader(`{"promptText":""}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty promptText, got %d", rec.Code)
	}
}

func TestPromptCreateVersion_GroupNotFound(t *testing.T) {
	ph, _, _, _ := setupPromptHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/groups/{id}/prompts", ph.handleCreateVersion)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/groups/"+idgen.New()+"/prompts", strings.NewReader(`{"promptText":"new version text"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing group, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestPromptUpdate(t *testing.T) {
	ph, _, groupRepo, promptRepo := setupPromptHandler(t)
	_, prompt := createTestGroupWithPrompt(t, groupRepo, promptRepo)

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/prompts/{id}", ph.handleUpdate)

	body := `{"promptText":"updated text"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/prompts/"+prompt.ID, strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PromptResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.PromptText != "updated text" {
		t.Errorf("expected promptText='updated text', got %q", resp.PromptText)
	}
}

func TestPromptUpdate_NotFound(t *testing.T) {
	ph, _, _, _ := setupPromptHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/prompts/{id}", ph.handleUpdate)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/prompts/"+idgen.New()+"", strings.NewReader(`{"promptText":"x"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestPromptMakeProduction(t *testing.T) {
	ph, _, groupRepo, promptRepo := setupPromptHandler(t)
	group, _ := createTestGroupWithPrompt(t, groupRepo, promptRepo)

	p2 := &store.Prompt{PromptText: "v2", GroupID: group.ID, UserID: "sam_dev_user", Version: 2}
	_ = promptRepo.Create(context.Background(), p2)

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/prompts/{id}/makeProduction", ph.handleMakeProduction)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/prompts/"+p2.ID+"/makeProduction", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	updatedGroup, _ := groupRepo.GetByID(context.Background(), group.ID)
	if updatedGroup.ProductionPromptID == nil || *updatedGroup.ProductionPromptID != p2.ID {
		t.Error("expected production prompt to be updated to p2")
	}
}

func TestPromptDelete_LastInGroup_CascadesToShares(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	ctx := t.Context()
	group := &store.PromptGroup{Name: "With shares", UserID: "sam_dev_user"}
	if err := groupRepo.Create(ctx, group); err != nil {
		t.Fatal(err)
	}
	prompt := &store.Prompt{
		PromptText: "v1", GroupID: group.ID, UserID: "sam_dev_user", Version: 1, Name: &group.Name,
	}
	if err := promptRepo.Create(ctx, prompt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := shareRepo.BatchShare(ctx, group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{
			{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
			{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"},
		}, "sam_dev_user"); err != nil {
		t.Fatal(err)
	}

	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	ph := &promptHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		shareRepo:  shareRepo,
		logger:     adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/prompts/{id}", ph.handleDelete)

	rec := httptest.NewRecorder()
	req := authtest.With(
		httptest.NewRequest("DELETE", "/api/v1/prompts/"+prompt.ID, nil),
		auth.NewTestSubject(auth.SubjectFields{UserID: "sam_dev_user", Email: "sam_dev_user@dev.local", Authenticated: true}),
	)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}

	if got, _ := groupRepo.GetByID(ctx, group.ID); got != nil {
		t.Error("expected group to be deleted when last prompt is removed")
	}

	remaining, err := shareRepo.GetCollaborators(ctx, group.ID, store.ResourceTypePromptGroup)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 share rows after cascade, got %d: %+v", len(remaining), remaining)
	}
}

func TestPromptDelete_LastInGroup(t *testing.T) {
	ph, _, groupRepo, promptRepo := setupPromptHandler(t)
	group, prompt := createTestGroupWithPrompt(t, groupRepo, promptRepo)

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/prompts/{id}", ph.handleDelete)

	rec := httptest.NewRecorder()
	req := authtest.With(
		httptest.NewRequest("DELETE", "/api/v1/prompts/"+prompt.ID, nil),
		auth.NewTestSubject(auth.SubjectFields{UserID: "sam_dev_user", Email: "sam_dev_user@dev.local", Authenticated: true}),
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}

	got, _ := groupRepo.GetByID(context.Background(), group.ID)
	if got != nil {
		t.Error("expected group to be deleted when last prompt is removed")
	}
}

func TestPromptDelete_NotLast(t *testing.T) {
	ph, _, groupRepo, promptRepo := setupPromptHandler(t)
	group, prompt := createTestGroupWithPrompt(t, groupRepo, promptRepo)

	p2 := &store.Prompt{PromptText: "v2", GroupID: group.ID, UserID: "sam_dev_user", Version: 2}
	_ = promptRepo.Create(context.Background(), p2)

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/prompts/{id}", ph.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/prompts/"+prompt.ID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}

	got, _ := groupRepo.GetByID(context.Background(), group.ID)
	if got == nil {
		t.Error("expected group to still exist after deleting non-last prompt")
	}
}

func TestPromptDelete_NotFound(t *testing.T) {
	ph, _, _, _ := setupPromptHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/prompts/{id}", ph.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/prompts/"+idgen.New()+"", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestPromptPatchRouter(t *testing.T) {
	ph, _, groupRepo, promptRepo := setupPromptHandler(t)
	_, prompt := createTestGroupWithPrompt(t, groupRepo, promptRepo)

	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/prompts/", ph.handlePromptPatch)

	body := `{"promptText":"via router"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/prompts/"+prompt.ID, strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for update via router, got %d; body: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("PATCH", "/api/v1/prompts/"+prompt.ID+"/unknown-action", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown suffix, got %d", rec.Code)
	}
}
