// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestSaveChatTask_Create(t *testing.T) {
	s1 := idgen.New()
	t1 := idgen.New()
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	body := fmt.Sprintf(`{"id":%q,"messageBubbles":"[]","userMessage":"hello"}`, t1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/chatTasks", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, hasData := raw["data"]; hasData {
		t.Error("POST chat-tasks response should NOT be wrapped in DataResponse")
	}
	if raw["id"] != t1 {
		t.Errorf("expected id=t1, got %v", raw["id"])
	}
	if raw["sessionId"] != s1 {
		t.Errorf("expected sessionId=s1, got %v", raw["sessionId"])
	}
}

func TestSaveChatTask_Update(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	t1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	_ = chatRepo.Create(ctx, &store.ChatTask{
		ID: t1, SessionID: s1, UserID: "sam_dev_user",
		MessageBubbles: "[]",
	})

	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	body := fmt.Sprintf(`{"id":%q,"messageBubbles":"[{\"type\":\"text\"}]"}`, t1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/chatTasks", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSaveChatTask_BumpsSessionUpdatedTime(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	t1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	before, err := sessRepo.GetByID(ctx, s1)
	if err != nil || before == nil {
		t.Fatalf("session not found before save: %v", err)
	}

	time.Sleep(2 * time.Millisecond)

	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	body := fmt.Sprintf(`{"id":%q,"messageBubbles":"[]","userMessage":"hello"}`, t1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/chatTasks", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	after, err := sessRepo.GetByID(ctx, s1)
	if err != nil || after == nil {
		t.Fatalf("session not found after save: %v", err)
	}
	if after.UpdatedTime <= before.UpdatedTime {
		t.Errorf("session UpdatedTime not bumped on chat task save: before=%d after=%d", before.UpdatedTime, after.UpdatedTime)
	}
}

func TestSaveChatTask_InvalidBubbles(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	body := fmt.Sprintf(`{"id":%q,"messageBubbles":"not valid json"}`, idgen.New())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/chatTasks", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}

	var raw map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&raw)
	if _, ok := raw["message"]; !ok {
		t.Error("missing 'message' key in error")
	}
}

func TestSaveChatTask_SessionNotFound(t *testing.T) {
	nonexistent := idgen.New()
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	body := fmt.Sprintf(`{"id":%q,"messageBubbles":"[]"}`, idgen.New())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+nonexistent+"/chatTasks", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestListChatTasks_Empty(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/chatTasks", chatH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/chatTasks", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[TaskResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data == nil {
		t.Error("expected data to be an empty slice, not nil")
	}
}

func TestListChatTasks_Ordered(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	t1 := idgen.New()
	t2 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	_ = chatRepo.Create(ctx, &store.ChatTask{
		ID: t1, SessionID: s1, UserID: "sam_dev_user", MessageBubbles: "[]",
	})
	_ = chatRepo.Create(ctx, &store.ChatTask{
		ID: t2, SessionID: s1, UserID: "sam_dev_user", MessageBubbles: "[{}]",
	})

	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/chatTasks", chatH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/chatTasks", nil)
	mux.ServeHTTP(rec, req)

	var resp PaginatedResponse[TaskResponse]
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != t1 {
		t.Errorf("expected first task t1, got %s", resp.Data[0].ID)
	}
	if resp.Data[1].ID != t2 {
		t.Errorf("expected second task t2, got %s", resp.Data[1].ID)
	}
}
