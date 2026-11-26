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
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestListChatTasks_Paginated(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	ctx := context.Background()
	s1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})
	for range 3 {
		_ = chatRepo.Create(ctx, &store.ChatTask{
			ID: idgen.New(), SessionID: s1, UserID: "sam_dev_user", MessageBubbles: "[]",
		})
	}

	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/chatTasks", chatH.handleList)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/chatTasks?pageNumber=1&pageSize=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("page 1: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var page1 PaginatedResponse[TaskResponse]
	if err := json.NewDecoder(rec.Body).Decode(&page1); err != nil {
		t.Fatal(err)
	}
	if len(page1.Data) != 2 {
		t.Errorf("page 1: expected 2 tasks, got %d", len(page1.Data))
	}
	if page1.Meta.Pagination.Count != 3 {
		t.Errorf("page 1: expected count=3, got %d", page1.Meta.Pagination.Count)
	}
	if page1.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 1: expected totalPages=2, got %d", page1.Meta.Pagination.TotalPages)
	}
	if page1.Meta.Pagination.NextPage == nil || *page1.Meta.Pagination.NextPage != 2 {
		t.Errorf("page 1: expected nextPage=2, got %v", page1.Meta.Pagination.NextPage)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/chatTasks?pageNumber=2&pageSize=2", nil))
	var page2 PaginatedResponse[TaskResponse]
	if err := json.NewDecoder(rec.Body).Decode(&page2); err != nil {
		t.Fatal(err)
	}
	if len(page2.Data) != 1 {
		t.Errorf("page 2: expected 1 task, got %d", len(page2.Data))
	}
	if page2.Meta.Pagination.NextPage != nil {
		t.Errorf("page 2: expected nextPage=null, got %v", *page2.Meta.Pagination.NextPage)
	}
}

func TestListChatTasks_PaginatedInvalidPageSize(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	s1 := idgen.New()
	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/chatTasks", chatH.handleList)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/chatTasks?pageSize=0", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for pageSize=0, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSessionMessages_Paginated(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	ctx := context.Background()
	s1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})
	for range 2 {
		_ = chatRepo.Create(ctx, &store.ChatTask{
			ID: idgen.New(), SessionID: s1, UserID: "sam_dev_user",
			MessageBubbles: `[{"type":"text","content":"hi"}]`,
		})
	}

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/messages", sessH.handleMessages)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/messages?pageNumber=1&pageSize=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[sessionMessageResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Meta.Pagination.Count != 2 {
		t.Errorf("expected count=2 chat tasks, got %d", resp.Meta.Pagination.Count)
	}
	if resp.Meta.Pagination.NextPage == nil || *resp.Meta.Pagination.NextPage != 2 {
		t.Errorf("expected nextPage=2, got %v", resp.Meta.Pagination.NextPage)
	}
}

func TestSessionMessages_PaginatedInvalidPageSize(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	s1 := idgen.New()
	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/messages", sessH.handleMessages)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/messages?pageSize=200", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for pageSize=200, got %d: %s", rec.Code, rec.Body.String())
	}
}
