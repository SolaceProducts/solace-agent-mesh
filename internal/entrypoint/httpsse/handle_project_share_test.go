// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sharing"
)

type mockProjectSharingService struct {
	sharedIDs     []string
	sharedIDsErr  error
	sharesResp    *entrypoint.ProjectSharesResponse
	batchResp     *entrypoint.BatchShareResponse
	unshareResp   *entrypoint.BatchDeleteResponse
	hasAccess     bool
	viewAccessErr error
	deleteAllErr  error

	lastViewAccessProjectID string
	lastViewAccessUserID    string
}

func (m *mockProjectSharingService) GetSharedProjectIDs(_ context.Context, _ string) ([]string, error) {
	return m.sharedIDs, m.sharedIDsErr
}

func (m *mockProjectSharingService) GetProjectShares(_ context.Context, _ string) (*entrypoint.ProjectSharesResponse, error) {
	return m.sharesResp, nil
}

func (m *mockProjectSharingService) BatchShare(_ context.Context, _ string, _ []entrypoint.ShareRequest, _ string) (*entrypoint.BatchShareResponse, error) {
	return m.batchResp, nil
}

func (m *mockProjectSharingService) BatchUnshare(_ context.Context, _ string, _ []string) (*entrypoint.BatchDeleteResponse, error) {
	return m.unshareResp, nil
}

func (m *mockProjectSharingService) DeleteAllShares(_ context.Context, _ string) error {
	return m.deleteAllErr
}

func (m *mockProjectSharingService) HasViewAccess(_ context.Context, projectID, userID string) (bool, error) {
	m.lastViewAccessProjectID = projectID
	m.lastViewAccessUserID = userID
	if m.viewAccessErr != nil {
		return false, m.viewAccessErr
	}
	return m.hasAccess, nil
}

func TestHandleGetShares_NoService_OwnerGetsEmptyShares(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "My Project", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/shares", h.handleGetShares)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/shares", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp entrypoint.ProjectSharesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ProjectID != project.ID {
		t.Errorf("expected projectId=%s, got %s", project.ID, resp.ProjectID)
	}
	if len(resp.Shares) != 0 {
		t.Errorf("expected 0 shares, got %d", len(resp.Shares))
	}
}

func TestHandleGetShares_NoService_NonOwnerGets404(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Other Project", UserID: "other_user"}
	_ = projectRepo.Create(ctx, project)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/shares", h.handleGetShares)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/shares", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleGetShares_WithService_DelegatesToService(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "My Project", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	mockSvc := &mockProjectSharingService{
		sharesResp: &entrypoint.ProjectSharesResponse{
			ProjectID:  project.ID,
			OwnerEmail: "sam_dev_user",
			Shares: []entrypoint.ShareResponse{
				{ID: idgen.New(), ProjectID: project.ID, UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
			},
		},
	}
	core.SetProjectSharingService(mockSvc)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/shares", h.handleGetShares)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/shares", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp entrypoint.ProjectSharesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Shares) != 1 {
		t.Errorf("expected 1 share, got %d", len(resp.Shares))
	}
}

func TestHandleCreateShares_NoService_Returns501(t *testing.T) {
	someID := idgen.New()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{id}/shares", h.handleCreateShares)

	body := `{"shares":[{"userEmail":"alice@example.com","accessLevel":"resource_viewer"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects/"+someID+"/shares", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", rec.Code)
	}
}

func TestHandleDeleteShares_NoService_Returns501(t *testing.T) {
	someID := idgen.New()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/projects/{id}/shares", h.handleDeleteShares)

	body := `{"userEmails":["alice@example.com"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/projects/"+someID+"/shares", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", rec.Code)
	}
}

func TestHandleCreateShares_NonOwner_Returns403(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Other Project", UserID: "other_user"}
	_ = projectRepo.Create(ctx, project)

	mockSvc := &mockProjectSharingService{}
	core.SetProjectSharingService(mockSvc)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{id}/shares", h.handleCreateShares)

	body := `{"shares":[{"userEmail":"alice@example.com","accessLevel":"resource_viewer"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects/"+project.ID+"/shares", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleCreateShares_SelfShare_Returns400(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "My Project", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	mockSvc := &mockProjectSharingService{}
	core.SetProjectSharingService(mockSvc)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{id}/shares", h.handleCreateShares)

	body := `{"shares":[{"userEmail":"sam_dev_user","accessLevel":"resource_viewer"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects/"+project.ID+"/shares", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateShares_EmptyShares_Returns400(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "My Project", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	mockSvc := &mockProjectSharingService{}
	core.SetProjectSharingService(mockSvc)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{id}/shares", h.handleCreateShares)

	body := `{"shares":[]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects/"+project.ID+"/shares", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleCreateShares_Owner_DelegatesToService(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "My Project", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	mockSvc := &mockProjectSharingService{
		batchResp: &entrypoint.BatchShareResponse{
			ProjectID:      project.ID,
			Created:        []entrypoint.ShareResponse{{ID: idgen.New(), UserEmail: "alice@example.com"}},
			Updated:        []entrypoint.ShareResponse{},
			TotalProcessed: 1,
		},
	}
	core.SetProjectSharingService(mockSvc)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{id}/shares", h.handleCreateShares)

	body := `{"shares":[{"userEmail":"alice@example.com","accessLevel":"resource_viewer"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects/"+project.ID+"/shares", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp entrypoint.BatchShareResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalProcessed != 1 {
		t.Errorf("expected totalProcessed=1, got %d", resp.TotalProcessed)
	}
}

func TestHandleDeleteShares_Owner_DelegatesToService(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "My Project", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	mockSvc := &mockProjectSharingService{
		unshareResp: &entrypoint.BatchDeleteResponse{
			ProjectID:     project.ID,
			DeletedCount:  1,
			DeletedEmails: []string{"alice@example.com"},
		},
	}
	core.SetProjectSharingService(mockSvc)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/projects/{id}/shares", h.handleDeleteShares)

	body := `{"userEmails":["alice@example.com"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/projects/"+project.ID+"/shares", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp entrypoint.BatchDeleteResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.DeletedCount != 1 {
		t.Errorf("expected deletedCount=1, got %d", resp.DeletedCount)
	}
}

func TestHandleGetShares_SharedUserCanAccess(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "Other Project", UserID: "other_user"}
	_ = projectRepo.Create(ctx, project)

	mockSvc := &mockProjectSharingService{
		hasAccess: true,
		sharesResp: &entrypoint.ProjectSharesResponse{
			ProjectID:  project.ID,
			OwnerEmail: "other_user",
			Shares:     []entrypoint.ShareResponse{},
		},
	}
	core.SetProjectSharingService(mockSvc)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/shares", h.handleGetShares)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/shares", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for shared user, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleShares_NoShareScope_Passes(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	projectRepo := store.NewProjectRepo(core.Store())
	ctx := context.Background()
	project := &store.Project{Name: "My Project", UserID: "sam_dev_user"}
	_ = projectRepo.Create(ctx, project)

	mockSvc := &mockProjectSharingService{
		sharesResp: &entrypoint.ProjectSharesResponse{
			ProjectID:  project.ID,
			OwnerEmail: "sam_dev_user",
			Shares:     []entrypoint.ShareResponse{},
		},
	}
	core.SetProjectSharingService(mockSvc)

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "sam_dev_user",
		Email:         "sam_dev_user",
		Authenticated: true,
		Scopes:        []string{"agent:*:invoke"},
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/shares", h.handleGetShares)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+project.ID+"/shares", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 without a share scope, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestShareIntegration_FullFlow(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	ctx := context.Background()
	sessAlice1 := idgen.New()

	svc := sharing.NewService(func() *store.DB { return db })
	core.SetProjectSharingService(svc)

	projectRepo := store.NewProjectRepo(db)
	sessRepo := store.NewSessionRepo(db)

	project := &store.Project{Name: "Shared Project", UserID: "owner@example.com"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	pid := project.ID
	if err := sessRepo.Create(ctx, &store.Session{
		ID: sessAlice1, UserID: "alice@example.com", ProjectID: &pid,
	}); err != nil {
		t.Fatal(err)
	}

	h := &projectShareHandler{
		core:        adapter.core,
		projectRepo: projectRepo,
		logger:      adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/shares", h.handleGetShares)
	mux.HandleFunc("POST /api/v1/projects/{id}/shares", h.handleCreateShares)
	mux.HandleFunc("DELETE /api/v1/projects/{id}/shares", h.handleDeleteShares)

	ownerID := auth.NewTestSubject(auth.SubjectFields{
		UserID: "owner@example.com", Email: "owner@example.com",
		Authenticated: true, Scopes: []string{"project:_:share"},
	})
	aliceID := auth.NewTestSubject(auth.SubjectFields{
		UserID: "alice@example.com", Email: "alice@example.com",
		Authenticated: true, Scopes: []string{"project:_:share"},
	})
	noScopeID := auth.NewTestSubject(auth.SubjectFields{
		UserID: "owner@example.com", Email: "owner@example.com",
		Authenticated: true, Scopes: []string{"tool:*"},
	})

	doReq := func(method, url, body string, id *auth.Subject) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, url, strings.NewReader(body))
		if id != nil {
			req = req.WithContext(auth.ContextWithSubject(req.Context(), id))
		}
		mux.ServeHTTP(rec, req)
		return rec
	}

	shareURL := "/api/v1/projects/" + project.ID + "/shares"

	t.Run("owner_without_share_scope_allowed", func(t *testing.T) {
		rec := doReq("GET", shareURL, "", noScopeID)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("initial_empty", func(t *testing.T) {
		rec := doReq("GET", shareURL, "", ownerID)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
		}
		var resp entrypoint.ProjectSharesResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if resp.OwnerEmail != "owner@example.com" {
			t.Errorf("expected owner=owner@example.com, got %s", resp.OwnerEmail)
		}
		if len(resp.Shares) != 0 {
			t.Errorf("expected 0 shares, got %d", len(resp.Shares))
		}
	})

	t.Run("non_owner_create_forbidden", func(t *testing.T) {
		body := `{"shares":[{"userEmail":"bob@example.com","accessLevel":"resource_viewer"}]}`
		rec := doReq("POST", shareURL, body, aliceID)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("owner_shares_with_alice", func(t *testing.T) {
		body := `{"shares":[{"userEmail":"alice@example.com","accessLevel":"resource_viewer"}]}`
		rec := doReq("POST", shareURL, body, ownerID)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
		}
		var resp entrypoint.BatchShareResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if resp.TotalProcessed != 1 {
			t.Errorf("expected totalProcessed=1, got %d", resp.TotalProcessed)
		}
		if len(resp.Created) != 1 {
			t.Errorf("expected 1 created, got %d", len(resp.Created))
		}
	})

	t.Run("shared_user_can_get", func(t *testing.T) {
		rec := doReq("GET", shareURL, "", aliceID)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
		}
		var resp entrypoint.ProjectSharesResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if len(resp.Shares) != 1 {
			t.Fatalf("expected 1 share, got %d", len(resp.Shares))
		}
		if resp.Shares[0].UserEmail != "alice@example.com" {
			t.Errorf("expected alice@example.com, got %s", resp.Shares[0].UserEmail)
		}
		if resp.Shares[0].AccessLevel != "resource_viewer" {
			t.Errorf("expected resource_viewer, got %s", resp.Shares[0].AccessLevel)
		}
	})

	t.Run("self_share_prevented", func(t *testing.T) {
		body := `{"shares":[{"userEmail":"owner@example.com","accessLevel":"resource_viewer"}]}`
		rec := doReq("POST", shareURL, body, ownerID)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unshare_with_cascade", func(t *testing.T) {
		body := `{"userEmails":["alice@example.com"]}`
		rec := doReq("DELETE", shareURL, body, ownerID)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
		}
		var resp entrypoint.BatchDeleteResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if resp.DeletedCount != 1 {
			t.Errorf("expected deletedCount=1, got %d", resp.DeletedCount)
		}

		var deletedAt sql.NullInt64
		row := db.QueryRowContext(ctx,
			`SELECT deleted_at FROM sessions WHERE id = ?`, sessAlice1)
		if err := row.Scan(&deletedAt); err != nil {
			t.Fatal(err)
		}
		if !deletedAt.Valid {
			t.Error("expected alice's session to be soft-deleted after unshare")
		}
	})

	t.Run("unshared_user_denied", func(t *testing.T) {
		rec := doReq("GET", shareURL, "", aliceID)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 after unshare, got %d; body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("shares_empty_after_unshare", func(t *testing.T) {
		rec := doReq("GET", shareURL, "", ownerID)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp entrypoint.ProjectSharesResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if len(resp.Shares) != 0 {
			t.Errorf("expected 0 shares after unshare, got %d", len(resp.Shares))
		}
	})
}
