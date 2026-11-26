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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func setupShareLinkTest(t *testing.T) (*shareLinkHandler, *http.ServeMux, *store.SessionRepo, *store.ChatTaskRepo) {
	t.Helper()
	adapter, _, sessRepo, chatRepo, _ := setupTestAdapterWithDB(t)

	db := adapter.core.Store()
	h := &shareLinkHandler{
		core:         adapter.core,
		linkRepo:     store.NewSharedLinkRepo(db),
		linkUserRepo: store.NewSharedLinkUserRepo(db),
		artifactRepo: store.NewSharedArtifactRepo(db),
		sessRepo:     sessRepo,
		chatRepo:     chatRepo,
		taskRepo:     store.NewTaskRepo(db),
		eventRepo:    store.NewTaskEventRepo(db),
		artSvc:       newTestArtSvc(),
		userNotifier: adapter.userNotifier,
		logger:       adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/share/", h.handleShareGetDispatch)
	mux.HandleFunc("POST /api/v1/share/{id}/fork", h.handleFork)
	mux.HandleFunc("POST /api/v1/share/{id}/updateSnapshot", h.handleUpdateSnapshot)
	mux.HandleFunc("POST /api/v1/share/{id}/users", h.handleAddUsers)
	mux.HandleFunc("POST /api/v1/share/{id}/viewed", h.handleMarkShareViewed)
	mux.HandleFunc("PATCH /api/v1/share/{id}", h.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/share/{id}/users", h.handleRemoveUsers)
	mux.HandleFunc("DELETE /api/v1/share/{id}", h.handleDelete)
	mux.HandleFunc("POST /api/v1/share/{id}", h.handleCreate)
	mux.HandleFunc("GET /api/v1/auth/me", handleAuthMe)

	return h, mux, sessRepo, chatRepo
}

func seedSession(t *testing.T, repo *store.SessionRepo, userID, sessionID, name string) {
	t.Helper()
	if name == "" {
		name = "Chat"
	}
	n := name
	if err := repo.Create(context.Background(), &store.Session{
		ID: sessionID, UserID: userID, Name: &n,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func reqWithUser(method, url string, body string, userID, email string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, url, nil)
	}
	ctx := auth.ContextWithSubject(r.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID:        userID,
		Email:         email,
		Authenticated: true,
		AuthMethod:    "test",
	}))
	return r.WithContext(ctx)
}

func createShareAs(t *testing.T, mux *http.ServeMux, sessionID, userID, email, body string) SharedLinkResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+sessionID, body, userID, email)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create share %s: got %d: %s", sessionID, rec.Code, rec.Body.String())
	}
	var resp SharedLinkResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode create share: %v", err)
	}
	return resp
}

func viewShareAs(t *testing.T, mux *http.ServeMux, shareID, userID, email string) SharedSessionView {
	t.Helper()
	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+shareID, "", userID, email)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("view share %s as %s: got %d: %s", shareID, email, rec.Code, rec.Body.String())
	}
	var view SharedSessionView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode view share: %v", err)
	}
	return view
}

func TestShareLink_CreateAndGet(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "My Chat")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+sess1, `{}`, "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var createResp SharedLinkResponse
	if err := json.NewDecoder(rec.Body).Decode(&createResp); err != nil {
		t.Fatal(err)
	}
	if createResp.SessionID != sess1 {
		t.Errorf("session_id = %q, want %q", createResp.SessionID, sess1)
	}
	if createResp.ID == "" {
		t.Error("expected non-empty share_id")
	}
	if createResp.Title != "My Chat" {
		t.Errorf("title = %q, want 'My Chat'", createResp.Title)
	}
	if createResp.AccessType != "authenticated" {
		t.Errorf("access_type = %q, want %q", "authenticated", createResp.AccessType)
	}
	if createResp.CreatedTime == "" {
		t.Error("expected non-empty createdTime ISO 8601 string")
	}
	if createResp.SnapshotTime != createResp.CreatedTime {
		t.Errorf("snapshotTime should equal createdAt at creation; snapshotTime=%q createdAt=%q", createResp.SnapshotTime, createResp.CreatedTime)
	}
	if createResp.AllowedDomains == nil {
		t.Error("expected allowed_domains to be a JSON array, not null")
	}
	shareID := createResp.ID

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/link/"+sess1, "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get by session: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var getResp SharedLinkResponse
	if err := json.NewDecoder(rec.Body).Decode(&getResp); err != nil {
		t.Fatal(err)
	}
	if getResp.ID != shareID {
		t.Errorf("share_id = %q, want %q", getResp.ID, shareID)
	}
	if getResp.SnapshotTime == "" {
		t.Error("get-by-session response missing snapshotTime")
	}
}

func TestShareLink_CreateIdempotent(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+sess1, "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d", rec.Code)
	}
	var resp1 SharedLinkResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp1)

	rec = httptest.NewRecorder()
	req = reqWithUser("POST", "/api/v1/share/"+sess1, "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second create: expected 200, got %d", rec.Code)
	}
	var resp2 SharedLinkResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp2)

	if resp2.ID != resp1.ID {
		t.Errorf("expected same share_id, got %q vs %q", resp2.ID, resp1.ID)
	}
}

func TestShareLink_Create_RequiresAuth(t *testing.T) {
	sess1 := idgen.New()
	_, mux, _, _ := setupShareLinkTest(t)

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+sess1, "", "", "")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated create, got %d", rec.Code)
	}
}

func TestShareLink_Create_SessionMustExist(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+idgen.New(), "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when session missing, got %d", rec.Code)
	}
}

func TestShareLink_Create_DomainArray(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com",
		`{"allowedDomains":["example.com","test.com"]}`)
	if resp.AccessType != "domain_restricted" {
		t.Errorf("access_type = %q, want %q", "domain_restricted", resp.AccessType)
	}
	if len(resp.AllowedDomains) != 2 || resp.AllowedDomains[0] != "example.com" {
		t.Errorf("allowed_domains = %v, want [example.com, test.com]", resp.AllowedDomains)
	}
}

func TestShareLink_List_Paginated(t *testing.T) {
	_, mux, sessRepo, _ := setupShareLinkTest(t)

	for i := range 2 {
		sess := idgen.New()
		seedSession(t, sessRepo, "user-1", sess, "chat")
		_ = i
		_ = createShareAs(t, mux, sess, "user-1", "user1@example.com", "")
	}

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[ShareLinkItem]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 shares, got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.PageNumber != 1 || resp.Meta.Pagination.PageSize != 20 {
		t.Errorf("default pagination = %+v", resp.Meta.Pagination)
	}
	for _, item := range resp.Data {
		if item.AccessType == "" {
			t.Error("list item missing access_type")
		}
	}
}

func TestShareLink_Update(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	shareID := resp.ID

	rec := httptest.NewRecorder()
	req := reqWithUser("PATCH", "/api/v1/share/"+shareID,
		`{"allowedDomains":["example.com"]}`, "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updateResp SharedLinkResponse
	_ = json.NewDecoder(rec.Body).Decode(&updateResp)
	if len(updateResp.AllowedDomains) != 1 || updateResp.AllowedDomains[0] != "example.com" {
		t.Errorf("allowed_domains = %v, want [example.com]", updateResp.AllowedDomains)
	}
	if updateResp.AccessType != "domain_restricted" {
		t.Errorf("access_type = %q, want %q", "domain_restricted", updateResp.AccessType)
	}
}

func TestShareLink_Update_NonOwnerForbidden(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("PATCH", "/api/v1/share/"+resp.ID, `{"allowedDomains":["evil.com"]}`, "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-owner update, got %d", rec.Code)
	}
}

func TestShareLink_SoftDelete(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("DELETE", "/api/v1/share/"+resp.ID, "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var delResp SuccessResponse
	_ = json.NewDecoder(rec.Body).Decode(&delResp)
	if !delResp.Success || delResp.Message == "" {
		t.Errorf("delete response missing success/message: %+v", delResp)
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/link/"+sess1, "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rec.Code)
	}
}

func TestShareLink_Delete_NonOwnerForbidden(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("DELETE", "/api/v1/share/"+resp.ID, "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-owner delete, got %d", rec.Code)
	}
}

func TestShareLink_View_OwnerAccess(t *testing.T) {
	sess1 := idgen.New()
	h, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original")
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`hello`), MessageBubbles: `[]`, CreatedTime: 1000,
	})
	loc := h.artSvc.Resolve("", "user-1", sess1, "report.txt")
	_, _ = h.artSvc.Inner.Save(context.Background(), loc, []byte("hello"), "text/plain")

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+resp.ID, "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("view: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var view SharedSessionView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view.ID != resp.ID {
		t.Errorf("share_id = %q", view.ID)
	}
	if !view.IsOwner {
		t.Error("expected is_owner=true for owner view")
	}
	if view.SessionID == nil || *view.SessionID != sess1 {
		t.Errorf("session_id = %v, want %q", view.SessionID, sess1)
	}
	if view.SnapshotTime != nil {
		t.Error("owner view should not include snapshot_time")
	}
	if len(view.Tasks) != 1 {
		t.Errorf("expected 1 anonymized task, got %d", len(view.Tasks))
	}
	if view.AccessType != "authenticated" {
		t.Errorf("access_type = %q", view.AccessType)
	}
	if view.Artifacts == nil {
		t.Error("artifacts must be [] not null")
	}
}

func TestShareLink_View_ViewerHasSnapshotTime(t *testing.T) {
	sess1 := idgen.New()
	h, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original")
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`hello`), MessageBubbles: `[]`, CreatedTime: 1000,
	})

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/users",
		`{"shares":[{"userEmail":"user2@example.com","accessLevel":"resource_viewer"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add user: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+resp.ID, "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("view as viewer: %d %s", rec.Code, rec.Body.String())
	}
	var view SharedSessionView
	_ = json.NewDecoder(rec.Body).Decode(&view)
	if view.IsOwner {
		t.Error("expected is_owner=false for viewer")
	}
	if view.SnapshotTime == nil {
		t.Error("expected snapshot_time for resource_viewer")
	}
	if view.SessionID != nil {
		t.Error("viewer view must not expose original session_id")
	}
	if view.AccessType != "user_specific" {
		t.Errorf("access_type = %q, want %q", "user_specific", view.AccessType)
	}

	_, _ = time.Parse(time.RFC3339Nano, *view.SnapshotTime)
	time.Sleep(2 * time.Millisecond)
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`after snapshot`), MessageBubbles: `[]`,
	})
	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+resp.ID, "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	_ = json.NewDecoder(rec.Body).Decode(&view)
	if len(view.Tasks) != 1 {
		t.Errorf("expected 1 task after snapshot filter, got %d", len(view.Tasks))
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("POST", "/api/v1/share/"+resp.ID+"/updateSnapshot", `{}`, "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update-snapshot: %d %s", rec.Code, rec.Body.String())
	}
	var snapResp UpdateSnapshotResponse
	_ = json.NewDecoder(rec.Body).Decode(&snapResp)
	if snapResp.SnapshotTime == "" {
		t.Error("update-snapshot response missing snapshot_time")
	}
	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+resp.ID, "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	_ = json.NewDecoder(rec.Body).Decode(&view)
	if len(view.Tasks) != 2 {
		t.Errorf("expected 2 tasks after snapshot advance, got %d", len(view.Tasks))
	}
	_ = h
}

func TestShareLink_View_LinkViewerSnapshotFrozenAtCreation(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original")
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`before share`), MessageBubbles: `[]`, CreatedTime: 1000,
	})

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	if resp.SnapshotTime == "" {
		t.Error("create response missing snapshotTime")
	}

	time.Sleep(2 * time.Millisecond)
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`after share`), MessageBubbles: `[]`,
	})

	view := viewShareAs(t, mux, resp.ID, "user-2", "user2@example.com")
	if view.IsOwner {
		t.Error("expected is_owner=false for link viewer")
	}
	if view.SnapshotTime == nil {
		t.Error("expected snapshot_time for link viewer")
	}
	if len(view.Tasks) != 1 {
		t.Errorf("link viewer should see only the pre-share task, got %d", len(view.Tasks))
	}

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/updateSnapshot", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner refresh link snapshot: %d %s", rec.Code, rec.Body.String())
	}

	view = viewShareAs(t, mux, resp.ID, "user-2", "user2@example.com")
	if len(view.Tasks) != 2 {
		t.Errorf("link viewer should see both tasks after owner refresh, got %d", len(view.Tasks))
	}
}

func TestShareLink_View_DomainViewerSnapshotFrozen(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original")
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`before share`), MessageBubbles: `[]`, CreatedTime: 1000,
	})

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", `{"allowedDomains":["example.com"]}`)

	time.Sleep(2 * time.Millisecond)
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`after share`), MessageBubbles: `[]`,
	})

	view := viewShareAs(t, mux, resp.ID, "user-2", "alice@example.com")
	if view.SnapshotTime == nil {
		t.Error("expected snapshot_time for domain viewer")
	}
	if len(view.Tasks) != 1 {
		t.Errorf("domain viewer should see only the pre-share task, got %d", len(view.Tasks))
	}
}

func TestShareLink_View_EditorSeesLivingChat(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original")
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`before share`), MessageBubbles: `[]`, CreatedTime: 1000,
	})

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/users",
		`{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_editor"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add editor: %d %s", rec.Code, rec.Body.String())
	}

	time.Sleep(2 * time.Millisecond)
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`after share`), MessageBubbles: `[]`,
	})

	view := viewShareAs(t, mux, resp.ID, "user-2", "editor@example.com")
	if view.SnapshotTime != nil {
		t.Error("editor view should not be frozen at a snapshot")
	}
	if len(view.Tasks) != 2 {
		t.Errorf("editor should see the living chat (2 tasks), got %d", len(view.Tasks))
	}
}

func TestShareLink_UpdateSnapshot_NonOwnerDoesNotAdvanceLinkSnapshot(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	before := resp.SnapshotTime

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/updateSnapshot", "", "user-2", "stranger@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner refresh: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/link/"+sess1, "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	var after SharedLinkResponse
	_ = json.NewDecoder(rec.Body).Decode(&after)
	if after.SnapshotTime != before {
		t.Errorf("snapshot advanced by non-owner: before=%q after=%q", before, after.SnapshotTime)
	}
}

func TestLinkSnapshotTime(t *testing.T) {
	if got := linkSnapshotTime(&store.SharedLink{SnapshotTime: 0, CreatedTime: 1234}); got != 1234 {
		t.Errorf("zero snapshot should fall back to created_time; got %d want 1234", got)
	}
	if got := linkSnapshotTime(&store.SharedLink{SnapshotTime: 5000, CreatedTime: 1234}); got != 5000 {
		t.Errorf("non-zero snapshot should be used as-is; got %d want 5000", got)
	}
}

func TestShareLink_View_UnauthenticatedReturns401(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+resp.ID, "", "", "")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated view, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header on 401")
	}
}

func TestShareLink_View_DomainAccess(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com",
		`{"allowedDomains":["example.com","test.com"]}`)

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+resp.ID, "", "user-2", "alice@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for domain match, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+resp.ID, "", "user-3", "bob@other.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-matching domain, got %d", rec.Code)
	}
}

func TestShareLink_View_SharedUser(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/users",
		`{"shares":[{"userEmail":"user2@example.com","accessLevel":"resource_viewer"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add user: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+resp.ID, "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("shared user view: expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+resp.ID, "", "user-3", "user3@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-shared user, got %d", rec.Code)
	}
}

func TestShareLink_Users_CRUD(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	shareID := resp.ID

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+shareID+"/users",
		`{"shares":[{"userEmail":"alice@example.com","accessLevel":"resource_viewer"},{"userEmail":"bob@example.com","accessLevel":"resource_editor"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add users: %d %s", rec.Code, rec.Body.String())
	}
	var addResp BatchAddShareUsersResponse
	_ = json.NewDecoder(rec.Body).Decode(&addResp)
	if addResp.AddedCount != 2 || len(addResp.Users) != 2 {
		t.Errorf("add response = %+v, want added_count=2", addResp)
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+shareID+"/users", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list users: %d %s", rec.Code, rec.Body.String())
	}
	var listResp ShareUsersResponse
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if listResp.ID != shareID {
		t.Errorf("share_id = %q", listResp.ID)
	}
	if listResp.OwnerEmail == "" {
		t.Error("owner_email missing")
	}
	if len(listResp.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(listResp.Users))
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("DELETE", "/api/v1/share/"+shareID+"/users",
		`{"userEmails":["alice@example.com"]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove user: %d %s", rec.Code, rec.Body.String())
	}
	var delResp BatchDeleteShareUsersResponse
	_ = json.NewDecoder(rec.Body).Decode(&delResp)
	if delResp.DeletedCount != 1 {
		t.Errorf("deleted_count = %d, want 1", delResp.DeletedCount)
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+shareID+"/users", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	_ = json.NewDecoder(rec.Body).Decode(&listResp)
	if len(listResp.Users) != 1 {
		t.Errorf("expected 1 user after remove, got %d", len(listResp.Users))
	}
}

func TestShareLink_Users_UpdateAccessLevelPreservesOriginals(t *testing.T) {
	sess1 := idgen.New()
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	shareID := resp.ID

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+shareID+"/users",
		`{"shares":[{"userEmail":"alice@example.com","accessLevel":"resource_viewer"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("POST", "/api/v1/share/"+shareID+"/users",
		`{"shares":[{"userEmail":"alice@example.com","accessLevel":"resource_editor"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}

	got, err := h.linkUserRepo.GetByShareAndEmail(context.Background(), shareID, "alice@example.com")
	if err != nil || got == nil {
		t.Fatalf("GetByShareAndEmail: got=%v err=%v", got, err)
	}
	if got.AccessLevel != "resource_editor" {
		t.Errorf("AccessLevel = %q, want resource_editor", got.AccessLevel)
	}
	if got.OriginalAccessLevel == nil || *got.OriginalAccessLevel != "resource_viewer" {
		t.Errorf("OriginalAccessLevel = %v, want resource_viewer", got.OriginalAccessLevel)
	}
}

func TestShareLink_Users_NonOwnerForbidden(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+resp.ID+"/users", "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-owner list users: expected 403, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("POST", "/api/v1/share/"+resp.ID+"/users",
		`{"shares":[{"userEmail":"x@y.com"}]}`, "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-owner add users: expected 403, got %d", rec.Code)
	}
}

func TestShareLink_Fork(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original Chat")
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`hello`), MessageBubbles: `[]`, CreatedTime: 1000,
	})
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr(`world`), MessageBubbles: `[]`, CreatedTime: 2000,
	})

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/users",
		`{"shares":[{"userEmail":"user2@example.com","accessLevel":"resource_viewer"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)

	rec = httptest.NewRecorder()
	req = reqWithUser("POST", "/api/v1/share/"+resp.ID+"/fork", "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fork: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var forkResp ForkSharedChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&forkResp); err != nil {
		t.Fatal(err)
	}
	if forkResp.SessionID == "" {
		t.Error("expected non-empty forked session_id")
	}
	if forkResp.SessionName != "Original Chat (forked)" {
		t.Errorf("session_name = %q, want 'Original Chat (forked)'", forkResp.SessionName)
	}
	if !strings.Contains(forkResp.Message, "2 messages") {
		t.Errorf("message should reference forked count, got %q", forkResp.Message)
	}

	tasks, err := chatRepo.ListBySession(context.Background(), forkResp.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 forked tasks, got %d", len(tasks))
	}
}

func TestShareLink_Fork_UnauthenticatedRejected(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/fork", "", "", "")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestShareLink_SharedWithMe(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Group Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/users",
		`{"shares":[{"userEmail":"user2@example.com","accessLevel":"resource_viewer"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add user: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/sharedWithMe", "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared-with-me: %d %s", rec.Code, rec.Body.String())
	}

	var listResp PaginatedResponse[SharedWithMeItem]
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	items := listResp.Data
	if len(items) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(items))
	}
	it := items[0]
	if it.Title != "Group Chat" {
		t.Errorf("title = %q, want 'Group Chat'", it.Title)
	}
	if it.AccessLevel != "resource_viewer" {
		t.Errorf("access_level = %q", it.AccessLevel)
	}
	if it.SharedAt == "" {
		t.Error("sharedAt must be an ISO 8601 string")
	}
	if it.ShareURL == "" {
		t.Error("share_url missing")
	}
	if it.SessionID != nil {
		t.Error("viewers should not receive session_id")
	}
}

func TestShareLink_UpdateSnapshot_OwnerTargetsUser(t *testing.T) {
	sess1 := idgen.New()
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	shareID := resp.ID

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+shareID+"/users",
		`{"shares":[{"userEmail":"viewer@example.com","accessLevel":"resource_viewer"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)

	rec = httptest.NewRecorder()
	req = reqWithUser("POST", "/api/v1/share/"+shareID+"/updateSnapshot",
		`{"userEmail":"viewer@example.com"}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner update snapshot: %d %s", rec.Code, rec.Body.String())
	}
	var snapResp UpdateSnapshotResponse
	_ = json.NewDecoder(rec.Body).Decode(&snapResp)
	got, _ := h.linkUserRepo.GetByShareAndEmail(context.Background(), shareID, "viewer@example.com")
	snapTs, _ := time.Parse(time.RFC3339Nano, snapResp.SnapshotTime)
	if got == nil || got.AddedAt != snapTs.UnixMilli() {
		t.Errorf("snapshot not persisted; got=%+v resp=%+v", got, snapResp)
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("POST", "/api/v1/share/"+shareID+"/updateSnapshot",
		`{"userEmail":"viewer@example.com"}`,
		"user-3", "user3@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-owner target update: expected 403, got %d", rec.Code)
	}
}

func TestShareLink_AuthMe(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/auth/me", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("auth/me: %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["id"] != "user-1" || resp["email"] != "user1@example.com" {
		t.Errorf("identity = %+v", resp)
	}
}

func TestShareLink_AuthMe_EnrichedFields(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	r := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	r = r.WithContext(auth.ContextWithSubject(r.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID:           "user-1",
		Email:            "user1@example.com",
		Name:             "User One",
		Authenticated:    true,
		AuthMethod:       "test",
		EnrichmentStatus: identity.EnrichmentOK,
		EnrichmentReason: "",
		Profile: &identity.Profile{
			ID:          "user-1",
			DisplayName: "User One",
			WorkEmail:   "user1@example.com",
			JobTitle:    "Engineer",
			Department:  "Platform",
		},
	})))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("auth/me: %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["name"] != "User One" {
		t.Errorf("name = %v, want 'User One'", resp["name"])
	}
	if resp["enrichmentStatus"] != string(identity.EnrichmentOK) {
		t.Errorf("enrichmentStatus = %v", resp["enrichmentStatus"])
	}
	prof, ok := resp["profile"].(map[string]any)
	if !ok {
		t.Fatalf("profile missing or wrong shape: %+v", resp["profile"])
	}
	if prof["jobTitle"] != "Engineer" || prof["department"] != "Platform" {
		t.Errorf("profile fields = %+v", prof)
	}
}

func TestShareLink_AuthMe_UnavailableSurfacesReason(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	r := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	r = r.WithContext(auth.ContextWithSubject(r.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID:           "user-1",
		Email:            "user1@example.com",
		Authenticated:    true,
		AuthMethod:       "test",
		EnrichmentStatus: identity.EnrichmentUnavailable,
		EnrichmentReason: "cache miss",
	})))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["enrichmentStatus"] != string(identity.EnrichmentUnavailable) {
		t.Errorf("enrichmentStatus = %v", resp["enrichmentStatus"])
	}
	if resp["enrichmentReason"] != "cache miss" {
		t.Errorf("enrichmentReason = %v", resp["enrichmentReason"])
	}
	if _, hasProfile := resp["profile"]; hasProfile {
		t.Errorf("profile should be omitted when nil, got %+v", resp["profile"])
	}
}

func TestShareLink_NotFound(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)
	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+idgen.New(), "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestBaseURL_PlainHTTP(t *testing.T) {
	r := httptest.NewRequest("GET", "http://app.example.com/api/v1/share/", nil)
	r.Host = "app.example.com"
	if got := baseURL(r); got != "http://app.example.com" {
		t.Errorf("baseURL = %q", got)
	}
}

func TestBaseURL_XForwardedProto(t *testing.T) {
	r := httptest.NewRequest("GET", "http://app.example.com/api/v1/share/", nil)
	r.Host = "app.example.com"
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := baseURL(r); got != "https://app.example.com" {
		t.Errorf("baseURL = %q", got)
	}
}

func TestBaseURL_XForwardedHost(t *testing.T) {
	r := httptest.NewRequest("GET", "http://internal:8080/", nil)
	r.Host = "internal:8080"
	r.Header.Set("X-Forwarded-Host", "app.example.com")
	if got := baseURL(r); got != "http://app.example.com" {
		t.Errorf("baseURL = %q", got)
	}
}

func TestBaseURL_BothForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "http://internal:8080/", nil)
	r.Host = "internal:8080"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "app.example.com")
	if got := baseURL(r); got != "https://app.example.com" {
		t.Errorf("baseURL = %q", got)
	}
}

func TestBuildShareURL(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/", nil)
	r.Host = "example.com"
	got := buildShareURL("abc123XYZ_-defghijk", r)
	want := "http://example.com/#/share/abc123XYZ_-defghijk"
	if got != want {
		t.Errorf("buildShareURL = %q, want %q", got, want)
	}
}

func TestShareLink_CreateReturnsShareURL(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	if resp.ShareURL == "" {
		t.Error("expected non-empty share_url")
	}
	if !strings.Contains(resp.ShareURL, "/#/share/"+resp.ID) {
		t.Errorf("share_url %q must contain /#/share/%s", resp.ShareURL, resp.ID)
	}
}

func TestShareLink_Create_RejectsInvalidDomains(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+sess1,
		`{"allowedDomains":["localhost"]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid domain, got %d", rec.Code)
	}
}

func TestShareLink_Update_ClearsAllowedDomains(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com",
		`{"allowedDomains":["example.com"]}`)

	rec := httptest.NewRecorder()
	req := reqWithUser("PATCH", "/api/v1/share/"+resp.ID,
		`{"allowedDomains":[]}`, "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var updated SharedLinkResponse
	_ = json.NewDecoder(rec.Body).Decode(&updated)
	if len(updated.AllowedDomains) != 0 {
		t.Errorf("allowed_domains should be cleared, got %v", updated.AllowedDomains)
	}
	if updated.AccessType != "authenticated" {
		t.Errorf("access_type = %q, want %q", "authenticated", updated.AccessType)
	}
}

func TestShareLink_Update_RejectsDomainsWithoutAuth(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("PATCH", "/api/v1/share/"+resp.ID,
		`{"requireAuthentication":false,"allowedDomains":["example.com"]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("domains+no-auth must be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestShareLink_Delete_MissingShareIs404(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	rec := httptest.NewRecorder()
	req := reqWithUser("DELETE", "/api/v1/share/bogus-share-id", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestShareLink_UpdateSnapshot_ViewerSelfSuccess(t *testing.T) {
	sess1 := idgen.New()
	h, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	shareID := resp.ID

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+shareID+"/users",
		`{"shares":[{"userEmail":"viewer@example.com","accessLevel":"resource_viewer"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)

	rec = httptest.NewRecorder()
	req = reqWithUser("POST", "/api/v1/share/"+shareID+"/updateSnapshot", "", "user-2", "viewer@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer update-snapshot: %d %s", rec.Code, rec.Body.String())
	}
	var snap UpdateSnapshotResponse
	_ = json.NewDecoder(rec.Body).Decode(&snap)
	got, _ := h.linkUserRepo.GetByShareAndEmail(context.Background(), shareID, "viewer@example.com")
	snapTs2, _ := time.Parse(time.RFC3339Nano, snap.SnapshotTime)
	if got == nil || got.AddedAt != snapTs2.UnixMilli() {
		t.Errorf("snapshot not persisted; got=%+v resp=%+v", got, snap)
	}
}

func TestShareLink_UpdateSnapshot_UnknownShareIs404(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/bogus/updateSnapshot", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing share, got %d", rec.Code)
	}
}

func TestShareLink_UpdateSnapshot_NonMemberIs403(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/updateSnapshot", "", "user-9", "stranger@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-member update, got %d", rec.Code)
	}
}

func TestShareLink_ListSharedWithMe_EmptyForUnknownUser(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/sharedWithMe", "", "user-nobody", "nobody@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared-with-me for unknown user: %d %s", rec.Code, rec.Body.String())
	}
	var listResp PaginatedResponse[SharedWithMeItem]
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Data) != 0 {
		t.Errorf("expected empty array, got %d items", len(listResp.Data))
	}
}

func TestShareLink_Fork_NonMemberIs403(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original Chat")
	_ = chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr("hi"), MessageBubbles: `[]`, CreatedTime: 1,
	})

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com",
		`{"allowedDomains":["example.com"]}`)

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/fork", "", "user-2", "stranger@other.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-member fork, got %d", rec.Code)
	}
}

func TestShareLink_Fork_NoMessagesIs404(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Empty Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/fork", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("fork with no tasks: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestShareLink_AddUsers_DuplicateSkippedSilently(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	add := func() BatchAddShareUsersResponse {
		rec := httptest.NewRecorder()
		req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/users",
			`{"shares":[{"userEmail":"alice@example.com","accessLevel":"resource_viewer"}]}`,
			"user-1", "user1@example.com")
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("add users: %d %s", rec.Code, rec.Body.String())
		}
		var out BatchAddShareUsersResponse
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return out
	}

	first := add()
	if first.AddedCount != 1 {
		t.Errorf("first add: added_count = %d, want 1", first.AddedCount)
	}

	second := add()
	if second.AddedCount != 0 {
		t.Errorf("duplicate add: added_count = %d, want 0", second.AddedCount)
	}
	if len(second.Users) != 0 {
		t.Errorf("duplicate add: users = %d, want 0", len(second.Users))
	}
}

func TestShareLink_AddUsers_SkipsBlankEmail(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/users",
		`{"shares":[{"userEmail":"","accessLevel":"resource_viewer"},{"userEmail":"alice@example.com"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add users: %d %s", rec.Code, rec.Body.String())
	}
	var out BatchAddShareUsersResponse
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out.AddedCount != 1 {
		t.Errorf("added_count = %d, want 1 (blank email skipped)", out.AddedCount)
	}
	if out.Users[0].AccessLevel != "resource_viewer" {
		t.Errorf("default access_level = %q, want resource_viewer", out.Users[0].AccessLevel)
	}
}

func TestShareLink_AddUsers_InvalidBodyIs400(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+resp.ID+"/users", `{not json`, "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}

func TestShareLink_RemoveUsers_EmptyListDeletesZero(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("DELETE", "/api/v1/share/"+resp.ID+"/users", `{"userEmails":[]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty remove: %d %s", rec.Code, rec.Body.String())
	}
	var del BatchDeleteShareUsersResponse
	_ = json.NewDecoder(rec.Body).Decode(&del)
	if del.DeletedCount != 0 {
		t.Errorf("deleted_count = %d, want 0", del.DeletedCount)
	}
}

func TestShareLink_RemoveUsers_NotFoundIs404(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	rec := httptest.NewRecorder()
	req := reqWithUser("DELETE", "/api/v1/share/bogus/users", `{"userEmails":["x@y.com"]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("remove users for missing share: expected 404, got %d", rec.Code)
	}
}

func TestShareLink_RemoveUsers_NonOwnerIs403(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("DELETE", "/api/v1/share/"+resp.ID+"/users",
		`{"userEmails":["x@y.com"]}`, "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestShareLink_RemoveUsers_InvalidBodyIs400(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("DELETE", "/api/v1/share/"+resp.ID+"/users", `{bad json`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON: expected 400, got %d", rec.Code)
	}
}

func TestShareLink_Update_InvalidBodyIs400(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("PATCH", "/api/v1/share/"+resp.ID, `not json`, "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}

func TestShareLink_Update_RejectsInvalidDomain(t *testing.T) {
	sess1 := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Chat")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("PATCH", "/api/v1/share/"+resp.ID,
		`{"allowedDomains":["invalid domain"]}`, "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid domain, got %d", rec.Code)
	}
}

func TestShareLink_ListUsers_NotFoundIs404(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/bogus/users", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing share, got %d", rec.Code)
	}
}

func TestCountMessageBubbles(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty string", "", 0},
		{"empty array", `[]`, 0},
		{"two bubbles", `[{"role":"user"},{"role":"assistant"}]`, 2},
		{"malformed JSON returns 0", `{not json`, 0},
		{"object (not array) returns 0", `{"role":"user"}`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := countMessageBubbles(tc.in); got != tc.want {
				t.Errorf("countMessageBubbles(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestAnonymizeChatTask_StripsUserIdentity(t *testing.T) {
	bubbles := `[{"role":"user","metadata":{"userId":"u1","sessionId":"s1","other":"keep"}},` +
		`{"role":"assistant","metadata":{"agentId":"a1"}}]`
	meta := `{"taskId":"a2a-123","status":"running"}`
	task := &store.ChatTask{
		ID:             "task-1",
		SessionID:      "sess-abcdef1234567890",
		UserID:         "alice",
		UserMessage:    strPtr("hello"),
		MessageBubbles: bubbles,
		TaskMetadata:   &meta,
		CreatedTime:    1000,
	}

	got := anonymizeChatTask(task)

	if got["userId"] != "anonymous" {
		t.Errorf("user_id = %v, want 'anonymous'", got["userId"])
	}
	if got["sessionId"] == "sess-abcdef1234567890" {
		t.Error("session_id must be anonymized")
	}
	if got["workflowTaskId"] != "a2a-123" {
		t.Errorf("workflow_task_id = %v, want 'a2a-123'", got["workflowTaskId"])
	}
	arr, ok := got["messageBubbles"].([]map[string]any)
	if !ok {
		t.Fatalf("message_bubbles has wrong type: %T", got["messageBubbles"])
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 bubbles, got %d", len(arr))
	}
	b0meta, _ := arr[0]["metadata"].(map[string]any)
	if _, present := b0meta["userId"]; present {
		t.Error("userId must be stripped from bubble metadata")
	}
	if _, present := b0meta["sessionId"]; present {
		t.Error("sessionId must be stripped from bubble metadata")
	}
	if b0meta["other"] != "keep" {
		t.Error("non-PII metadata keys must be preserved")
	}
}

func TestAnonymizeChatTask_FallsBackToTaskIDWhenNoMetadata(t *testing.T) {
	task := &store.ChatTask{
		ID:             "task-xyz",
		SessionID:      "short",
		UserID:         "alice",
		MessageBubbles: "",
	}
	got := anonymizeChatTask(task)
	if got["workflowTaskId"] != "task-xyz" {
		t.Errorf("workflow_task_id = %v, want 'task-xyz'", got["workflowTaskId"])
	}
	if got["sessionId"] != "session_f9b0078b5df596d2" {
		t.Errorf("session_id hash mismatch, got %v", got["sessionId"])
	}
}

func TestAnonymizeChatTask_MalformedMetadataKeptAsString(t *testing.T) {
	bad := `{not json`
	task := &store.ChatTask{
		ID:           "t1",
		SessionID:    "s1",
		TaskMetadata: &bad,
	}
	got := anonymizeChatTask(task)
	if s, ok := got["taskMetadata"].(string); !ok || s != bad {
		t.Errorf("malformed task_metadata should be kept as raw string, got %T: %v", got["taskMetadata"], got["taskMetadata"])
	}
}

func TestForkedTaskMetadata_MergesExisting(t *testing.T) {
	meta := `{"taskId":"orig-task","status":"done","custom":"keep"}`
	src := &store.ChatTask{
		ID:           "orig-id",
		TaskMetadata: &meta,
	}
	got := forkedTaskMetadata(src, "orig-session", "orig-owner")
	if got == nil {
		t.Fatal("forkedTaskMetadata returned nil")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(*got), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["forkedFrom"] != "orig-id" {
		t.Errorf("forked_from = %v", m["forkedFrom"])
	}
	if m["forkedFromSessionId"] != "orig-session" {
		t.Errorf("forked_from_session_id = %v", m["forkedFromSessionId"])
	}
	if m["forkedFromOwnerId"] != "orig-owner" {
		t.Errorf("forked_from_owner_id = %v", m["forkedFromOwnerId"])
	}
	if _, present := m["status"]; present {
		t.Error("status must NOT be carried over into the forked task")
	}
	if m["taskId"] == "orig-task" {
		t.Error("task_id must be regenerated for the forked task")
	}
	if m["custom"] != "keep" {
		t.Errorf("custom key dropped: %v", m)
	}
}

func TestForkedTaskMetadata_NilSource(t *testing.T) {
	src := &store.ChatTask{ID: "orig-id"}
	got := forkedTaskMetadata(src, "orig-session", "orig-owner")
	if got == nil {
		t.Fatal("forkedTaskMetadata returned nil for no-metadata source")
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(*got), &m)
	if m["forkedFromSessionId"] != "orig-session" {
		t.Errorf("missing forked_from_session_id: %v", m)
	}
}

func TestValidateDomainsList(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		wantErr bool
	}{
		{"empty slice", []string{}, false},
		{"valid single", []string{"example.com"}, false},
		{"valid multi", []string{"example.com", "test.io"}, false},
		{"blank entries skipped", []string{"", "  ", "example.com"}, false},
		{"whitespace rejected", []string{"example .com"}, true},
		{"no dot rejected", []string{"localhost"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDomainsList(tc.in)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateDomainsList(%v) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
			}
		})
	}
}

func TestEmailToDomain(t *testing.T) {
	if got := emailToDomain("Alice@Example.COM"); got != "example.com" {
		t.Errorf("emailToDomain = %q, want 'example.com'", got)
	}
	if got := emailToDomain("no-at-sign"); got != "" {
		t.Errorf("expected empty for malformed, got %q", got)
	}
}

func TestTitleOrUntitled(t *testing.T) {
	empty := ""
	ws := "   "
	real := "Real Title"
	tests := []struct {
		in   *string
		want string
	}{
		{nil, "Untitled"},
		{&empty, "Untitled"},
		{&ws, "Untitled"},
		{&real, "Real Title"},
	}
	for _, tc := range tests {
		if got := titleOrUntitled(tc.in); got != tc.want {
			t.Errorf("titleOrUntitled(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAnonymizeSessionID_PythonParity(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"known vector abc", "abc", "session_ba7816bf8f01cfea"},
		{"known vector test-session-id", "test-session-id", "session_08001f8fa6f5dbb9"},
		{"empty string", "", "session_e3b0c44298fc1c14"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := anonymizeSessionID(tc.in)
			if got != tc.want {
				t.Errorf("anonymizeSessionID(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !strings.HasPrefix(got, "session_") {
				t.Errorf("separator must be underscore, got %q", got)
			}
			if len(got) != len("session_")+16 {
				t.Errorf("length = %d, want %d", len(got), len("session_")+16)
			}
		})
	}
}

func TestAnonymizeSessionID_DistinctInputsDistinctOutputs(t *testing.T) {
	a := anonymizeSessionID("session-abc")
	b := anonymizeSessionID("session-abd")
	if a == b {
		t.Errorf("expected distinct outputs, both were %q", a)
	}
}

func TestAnonymizeSessionID_Deterministic(t *testing.T) {
	id := "019dbbd4-ab12-cdef-3456-789012345678"
	first := anonymizeSessionID(id)
	second := anonymizeSessionID(id)
	if first != second {
		t.Errorf("anonymizeSessionID should be deterministic: %q vs %q", first, second)
	}
}

func seedSharedArtifact(t *testing.T, h *shareLinkHandler, mux *http.ServeMux, sessionID, ownerID, ownerEmail, filename string, body []byte, mime string) string {
	t.Helper()
	seedSession(t, h.sessRepo, ownerID, sessionID, "Chat")
	loc := h.artSvc.Resolve("", ownerID, sessionID, filename)
	if _, err := h.artSvc.Inner.Save(context.Background(), loc, body, mime); err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	resp := createShareAs(t, mux, sessionID, ownerID, ownerEmail, "")
	return resp.ID
}

func TestShareLink_ArtifactContent_OwnerSuccess(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	body := []byte("hello world")
	shareID := seedSharedArtifact(t, h, mux, sess1, "user-1", "user1@example.com", "report.txt", body, "text/plain")

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+shareID+"/artifacts/report.txt", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != string(body) {
		t.Errorf("body = %q, want %q", got, string(body))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if cl := rec.Header().Get("Content-Length"); cl != "11" {
		t.Errorf("Content-Length = %q, want 11", cl)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `inline; filename="report.txt"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("Content-Security-Policy = %q, want none on an inline-safe type", csp)
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", nosniff)
	}
}

func TestShareLink_ArtifactContent_AnonymousUnauthorized(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	shareID := seedSharedArtifact(t, h, mux, sess1, "user-1", "user1@example.com", "report.txt", []byte("x"), "text/plain")

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+shareID+"/artifacts/report.txt", "", "", "")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("expected WWW-Authenticate: Bearer, got %q", rec.Header().Get("WWW-Authenticate"))
	}
}

func TestShareLink_ArtifactContent_DomainMismatchForbidden(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	seedSession(t, h.sessRepo, "user-1", sess1, "Chat")
	loc := h.artSvc.Resolve("", "user-1", sess1, "report.txt")
	_, _ = h.artSvc.Inner.Save(context.Background(), loc, []byte("hi"), "text/plain")
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com",
		`{"allowedDomains":["example.com"]}`)

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+resp.ID+"/artifacts/report.txt", "", "user-2", "bob@other.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "authorized domains") {
		t.Errorf("body = %q, want 'authorized domains' message", rec.Body.String())
	}
}

func TestShareLink_ArtifactContent_SoftDeletedNotFound(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	shareID := seedSharedArtifact(t, h, mux, sess1, "user-1", "user1@example.com", "report.txt", []byte("x"), "text/plain")

	if err := h.linkRepo.SoftDelete(context.Background(), shareID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+shareID+"/artifacts/report.txt", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for soft-deleted share, got %d", rec.Code)
	}
}

func TestShareLink_ArtifactContent_PathTraversalBadRequest(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	shareID := seedSharedArtifact(t, h, mux, sess1, "user-1", "user1@example.com", "report.txt", []byte("x"), "text/plain")

	cases := []struct {
		name     string
		filename string
	}{
		{"dotdot", "../../etc/passwd"},
		{"backslash_dotdot", `..\windows\sys32`},
		{"leading_slash", "/etc/passwd"},
		{"embedded_dotdot", "subdir/../../etc/passwd"},
		{"nul_byte", "report.txt\x00.png"},
		{"dot_segment", "./report.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := reqWithUser("GET", "/api/v1/share/"+shareID, "", "user-1", "user1@example.com")
			req.SetPathValue("shareId", shareID)
			req.SetPathValue("filename", tc.filename)
			h.handleSharedArtifactContent(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("filename %q: expected 400, got %d (%s)", tc.filename, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestShareLink_ArtifactContent_SharedUserSuccess(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	body := []byte("hello viewer")
	shareID := seedSharedArtifact(t, h, mux, sess1, "user-1", "user1@example.com", "report.txt", body, "text/plain")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+shareID+"/users",
		`{"shares":[{"userEmail":"user2@example.com","accessLevel":"resource_viewer"}]}`,
		"user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add user: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+shareID+"/artifacts/report.txt", "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared-user fetch: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(body) {
		t.Errorf("body = %q, want %q", rec.Body.String(), string(body))
	}

	rec = httptest.NewRecorder()
	req = reqWithUser("GET", "/api/v1/share/"+shareID+"/artifacts/report.txt", "", "user-3", "user3@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-shared user: expected 403, got %d", rec.Code)
	}
}

func TestShareLink_ArtifactContent_FilenameNotInSession(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	shareID := seedSharedArtifact(t, h, mux, sess1, "user-1", "user1@example.com", "report.txt", []byte("x"), "text/plain")

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+shareID+"/artifacts/missing.txt", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing artifact, got %d", rec.Code)
	}
}

func TestShareLink_ArtifactContent_SQLInjectionProbe(t *testing.T) {
	_, mux, _, _ := setupShareLinkTest(t)

	probeID := "%27OR%271%27=%271%27-X"
	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+probeID+"/artifacts/report.txt", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for SQL-injection probe id, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestShareLink_ArtifactContent_OwnerSuccessPNGContentType(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	body := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	shareID := seedSharedArtifact(t, h, mux, sess1, "user-1", "user1@example.com", "image.png", body, "image/png")

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+shareID+"/artifacts/image.png", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Content-Length") != "8" {
		t.Errorf("Content-Length = %q, want 8", rec.Header().Get("Content-Length"))
	}
	if got := rec.Body.Bytes(); string(got) != string(body) {
		t.Errorf("body bytes mismatch")
	}
}

func TestShareLink_ArtifactContent_ScriptableTypesAreNeverInline(t *testing.T) {
	cases := map[string]string{
		"page.html":  "text/html",
		"page.xhtml": "application/xhtml+xml",
		"logo.svg":   "image/svg+xml",
		"feed.xml":   "application/xml",
		"blob.bin":   "application/octet-stream",
		"notes.txt":  "text/plain; x=1,text/html",
	}
	for filename, mime := range cases {
		t.Run(filename, func(t *testing.T) {
			h, mux, _, _ := setupShareLinkTest(t)
			shareID := seedSharedArtifact(t, h, mux, idgen.New(), "user-1", "user1@example.com", filename,
				[]byte(`<script>fetch("/api/v1/sessions")</script>`), mime)

			rec := httptest.NewRecorder()
			req := reqWithUser("GET", "/api/v1/share/"+shareID+"/artifacts/"+filename, "", "user-1", "user1@example.com")
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
				t.Errorf("Content-Disposition = %q, want attachment", cd)
			}
			if csp := rec.Header().Get("Content-Security-Policy"); csp != "sandbox" {
				t.Errorf("Content-Security-Policy = %q, want sandbox", csp)
			}
		})
	}
}

func TestShareLink_ArtifactContent_NonASCIIFilename(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	body := []byte("contenu")
	shareID := seedSharedArtifact(t, h, mux, sess1, "user-1", "user1@example.com", "rapport-é.txt", body, "text/plain")

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+shareID+"/artifacts/rapport-é.txt", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "filename*=UTF-8''") {
		t.Errorf("Content-Disposition missing filename*=UTF-8 for non-ASCII name: %q", cd)
	}
}

func TestShareLink_ArtifactContent_DefaultMimeType(t *testing.T) {
	sess1 := idgen.New()
	h, mux, _, _ := setupShareLinkTest(t)
	seedSession(t, h.sessRepo, "user-1", sess1, "Chat")
	loc := h.artSvc.Resolve("", "user-1", sess1, "blob")
	if _, err := h.artSvc.Inner.Save(context.Background(), loc, []byte{0xDE, 0xAD, 0xBE, 0xEF}, ""); err != nil {
		t.Fatalf("save: %v", err)
	}
	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("GET", "/api/v1/share/"+resp.ID+"/artifacts/blob", "", "user-1", "user1@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}
}

func TestGenerateShareID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for range 100 {
		id := generateShareID()
		if len(id) != 21 {
			t.Errorf("id length = %d, want 21", len(id))
		}
		if ids[id] {
			t.Errorf("duplicate id: %s", id)
		}
		ids[id] = true
	}
}

func TestShareLink_View_PopulatesTaskEvents(t *testing.T) {
	ctx := context.Background()
	sess1 := idgen.New()
	taskID := idgen.New()
	h, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original")

	if err := h.taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "user-1", SessionID: &sess1, StartTime: 1000,
		ExecutionMode: "foreground", InitialRequestText: strPtr("do the thing"),
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := h.eventRepo.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: taskID, CreatedTime: 1000,
		Topic: "default/a2a/v1/agent/request/Orchestrator", Direction: "request",
		Payload: `{"id":"msg-1","method":"message/send"}`,
	}); err != nil {
		t.Fatalf("seed task event: %v", err)
	}
	if err := chatRepo.Create(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr("hi"), MessageBubbles: `[]`, CreatedTime: 1000,
		TaskMetadata: strPtr(`{"taskId":"` + taskID + `"}`),
	}); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}

	resp := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	view := viewShareAs(t, mux, resp.ID, "user-1", "user1@example.com")

	if view.TaskEvents == nil {
		t.Fatal("taskEvents must be populated, got nil")
	}
	entry, ok := view.TaskEvents[taskID].(map[string]any)
	if !ok {
		t.Fatalf("taskEvents missing key %q; keys=%v", taskID, view.TaskEvents)
	}
	events, ok := entry["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("expected 1 event for task, got %v", entry["events"])
	}
	if _, leaked := entry["sessionId"]; leaked {
		t.Error("shared task-event entry must not leak the owner's sessionId")
	}
}

func TestShareLink_Fork_CopiesArtifacts(t *testing.T) {
	ctx := context.Background()
	sess1 := idgen.New()
	h, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original")
	if err := chatRepo.Create(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr("make a report"), MessageBubbles: `[]`, CreatedTime: 1000,
	}); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}
	loc := h.artSvc.Resolve("", "user-1", sess1, "report.txt")
	if _, err := h.artSvc.Inner.Save(ctx, loc, []byte("hello"), "text/plain"); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	share := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	req := reqWithUser("POST", "/api/v1/share/"+share.ID+"/fork", "", "user-2", "user2@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fork: got %d: %s", rec.Code, rec.Body.String())
	}
	var forkResp ForkSharedChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&forkResp); err != nil {
		t.Fatalf("decode fork: %v", err)
	}
	if forkResp.SessionID == "" || forkResp.SessionID == sess1 {
		t.Fatalf("fork must return a new session id, got %q", forkResp.SessionID)
	}

	forkLink := &store.SharedLink{UserID: "user-2", SessionID: forkResp.SessionID}
	copied := h.listSharedArtifacts(ctx, forkLink, h.scopeFromLoadedTasks("user-2", forkLink, nil))
	if len(copied) != 1 || copied[0].Filename != "report.txt" {
		t.Fatalf("forked session must contain report.txt under the viewer's scope, got %+v", copied)
	}
}

func TestShareLink_View_ViewerGetsTaskEvents(t *testing.T) {
	ctx := context.Background()
	sess1 := idgen.New()
	taskID := idgen.New()
	h, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original")

	if err := h.taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "user-1", SessionID: &sess1, StartTime: 1000,
		ExecutionMode: "foreground", InitialRequestText: strPtr("do the thing"),
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := h.eventRepo.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: taskID, CreatedTime: 1000,
		Topic: "default/a2a/v1/agent/request/Orchestrator", Direction: "request",
		Payload: `{"id":"msg-1","method":"message/send"}`,
	}); err != nil {
		t.Fatalf("seed task event: %v", err)
	}
	if err := chatRepo.Create(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr("hi"), MessageBubbles: `[]`, CreatedTime: 1000,
		TaskMetadata: strPtr(`{"taskId":"` + taskID + `"}`),
	}); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}

	share := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	view := viewShareAs(t, mux, share.ID, "user-2", "user2@example.com")

	if view.IsOwner {
		t.Fatal("expected non-owner viewer")
	}
	if _, ok := view.TaskEvents[taskID].(map[string]any); !ok {
		t.Fatalf("viewer must receive owner's task events; got %v", view.TaskEvents)
	}
}

func TestShareLink_View_NoEvents_TaskEventsNil(t *testing.T) {
	ctx := context.Background()
	sess1 := idgen.New()
	_, mux, sessRepo, chatRepo := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess1, "Original")
	if err := chatRepo.Create(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sess1, UserID: "user-1",
		UserMessage: strPtr("hi"), MessageBubbles: `[]`, CreatedTime: 1000,
	}); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}

	share := createShareAs(t, mux, sess1, "user-1", "user1@example.com", "")
	view := viewShareAs(t, mux, share.ID, "user-1", "user1@example.com")

	if view.TaskEvents != nil {
		t.Fatalf("taskEvents must be nil when no events exist, got %v", view.TaskEvents)
	}
}
