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
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func idorSetup(t *testing.T) (mux *http.ServeMux, sessRepo *store.SessionRepo, sessID string) {
	t.Helper()
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	sessID = idgen.New()
	if err := sessRepo.Create(context.Background(), &store.Session{ID: sessID, UserID: "alice"}); err != nil {
		t.Fatalf("create alice's session: %v", err)
	}

	sessH := &sessionHandler{
		core:     adapter.core,
		sessRepo: sessRepo,
		chatRepo: chatRepo,
		sseRepo:  sseRepo,
		logger:   adapter.logger,
	}
	chatH := &chatTaskHandler{
		core:     adapter.core,
		sessRepo: sessRepo,
		chatRepo: chatRepo,
		sseRepo:  sseRepo,
		logger:   adapter.logger,
	}

	mux = http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}", sessH.handleGet)
	mux.HandleFunc("PATCH /api/v1/sessions/{id}", sessH.handleRename)
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", sessH.handleDelete)
	mux.HandleFunc("PATCH /api/v1/sessions/{id}/project", sessH.handleAssignProject)
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)
	mux.HandleFunc("GET /api/v1/sessions/{id}/events/unconsumed", sessH.handleUnconsumedEvents)
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)
	mux.HandleFunc("GET /api/v1/sessions/{id}/chatTasks", chatH.handleList)
	return mux, sessRepo, sessID
}

func asUser(req *http.Request, userID string) *http.Request {
	id := auth.NewTestSubject(auth.SubjectFields{UserID: userID, Email: userID + "@example.com"})
	return req.WithContext(auth.ContextWithSubject(req.Context(), id))
}

func assertNotFoundWithMsg(t *testing.T, rec *httptest.ResponseRecorder, route string) {
	t.Helper()
	if rec.Code != http.StatusNotFound {
		t.Errorf("[%s] cross-user request: got %d, want 404", route, rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, sessionNotFoundMsg) {
		t.Errorf("[%s] body=%q, want it to contain %q", route, body, sessionNotFoundMsg)
	}
}

func TestSession_CrossUserIDOR_AllHandlers(t *testing.T) {
	mux, _, sessID := idorSetup(t)

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		bodyHeader string
	}{
		{"handleGet", "GET", "/api/v1/sessions/" + sessID, "", ""},
		{"handleRename", "PATCH", "/api/v1/sessions/" + sessID, `{"name":"hijacked"}`, "application/json"},
		{"handleDelete", "DELETE", "/api/v1/sessions/" + sessID, "", ""},
		{"handleAssignProject", "PATCH", "/api/v1/sessions/" + sessID + "/project", `{"projectId":"some-proj"}`, "application/json"},
		{"handleGenerateTitle", "POST", "/api/v1/sessions/" + sessID + "/generateTitle", `{"userMessage":"hi","agentResponse":"hi"}`, "application/json"},
		{"handleUnconsumedEvents", "GET", "/api/v1/sessions/" + sessID + "/events/unconsumed", "", ""},
		{"handleSave (chat-tasks)", "POST", "/api/v1/sessions/" + sessID + "/chatTasks",
			`{"id":"t1","messageBubbles":"[]"}`, "application/json"},
		{"handleList (chat-tasks)", "GET", "/api/v1/sessions/" + sessID + "/chatTasks", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body *strings.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			if tc.bodyHeader != "" {
				req.Header.Set("Content-Type", tc.bodyHeader)
			}
			req = asUser(req, "bob")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			assertNotFoundWithMsg(t, rec, tc.name)
		})
	}
}

func TestSession_OwnerCanGet(t *testing.T) {
	mux, _, sessID := idorSetup(t)
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sessID, nil)
	req = asUser(req, "alice")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("alice GET own session: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSession_SoftDelete_DefenceInDepth(t *testing.T) {
	_, sessRepo, sessID := idorSetup(t)

	if _, err := sessRepo.SoftDelete(context.Background(), sessID, "bob"); err == nil {
		t.Error("repo SoftDelete with non-owner userID should error (zero rows affected)")
	}

	got, err := sessRepo.GetByID(context.Background(), sessID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("alice's session should still be live after rejected cross-user delete")
	}
	if got.DeletedAt != nil {
		t.Errorf("deleted_at should be nil after rejected cross-user delete, got %v", *got.DeletedAt)
	}
}

func TestSession_GetAfterDelete_Returns404(t *testing.T) {
	mux, _, sessID := idorSetup(t)

	delReq := asUser(httptest.NewRequest("DELETE", "/api/v1/sessions/"+sessID, nil), "alice")
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("alice delete: got %d, want 204; body=%s", delRec.Code, delRec.Body.String())
	}

	getReq := asUser(httptest.NewRequest("GET", "/api/v1/sessions/"+sessID, nil), "alice")
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	assertNotFoundWithMsg(t, getRec, "GET after delete")
}

func TestSession_CrossUser_GetSoftDeleted_Returns404(t *testing.T) {
	mux, sessRepo, sessID := idorSetup(t)
	if _, err := sessRepo.SoftDelete(context.Background(), sessID, "alice"); err != nil {
		t.Fatalf("alice softdelete: %v", err)
	}

	req := asUser(httptest.NewRequest("GET", "/api/v1/sessions/"+sessID, nil), "bob")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assertNotFoundWithMsg(t, rec, "GET cross-user × deleted")
}

func TestSession_IDOR_404DoesNotLeakOwner(t *testing.T) {
	mux, _, sessID := idorSetup(t)
	req := asUser(httptest.NewRequest("GET", "/api/v1/sessions/"+sessID, nil), "bob")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, leak := range []string{"alice", "alice@", `"userId"`, `"userID"`} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(leak)) {
			t.Errorf("404 body leaks %q: %s", leak, body)
		}
	}
}

func TestChatTaskList_CrossUser_Returns404Empty(t *testing.T) {
	mux, _, sessID := idorSetup(t)
	req := asUser(httptest.NewRequest("GET", "/api/v1/sessions/"+sessID+"/chatTasks", nil), "bob")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assertNotFoundWithMsg(t, rec, "chat-tasks list cross-user")

	if rec.Code == http.StatusOK {
		var resp map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		t.Errorf("got 200 — handler must not even reach the repo: %v", resp)
	}
}

func TestSession_Rename_CrossUser_LeavesNameIntact(t *testing.T) {
	mux, sessRepo, sessID := idorSetup(t)
	priorName := "alice-secret-name"
	if err := sessRepo.Rename(context.Background(), sessID, priorName); err != nil {
		t.Fatalf("rename: %v", err)
	}

	body := strings.NewReader(`{"name":"hijacked"}`)
	req := asUser(httptest.NewRequest("PATCH", "/api/v1/sessions/"+sessID, body), "bob")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assertNotFoundWithMsg(t, rec, "rename cross-user")

	got, err := sessRepo.GetByID(context.Background(), sessID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("session vanished")
	}
	if got.Name == nil || *got.Name != priorName {
		t.Errorf("name was changed by cross-user rename: got %v, want %q", got.Name, priorName)
	}
}

func TestSession_CrossUserIDOR_ChatTaskIDCollision(t *testing.T) {
	mux, sessRepo, aliceSess := idorSetup(t)
	bobSess := idgen.New()
	if err := sessRepo.Create(context.Background(), &store.Session{ID: bobSess, UserID: "bob"}); err != nil {
		t.Fatalf("create bob's session: %v", err)
	}
	taskID := idgen.New()
	save := func(user, sess, bubbles string) *httptest.ResponseRecorder {
		body := strings.NewReader(`{"id":"` + taskID + `","messageBubbles":` + bubbles + `}`)
		req := httptest.NewRequest("POST", "/api/v1/sessions/"+sess+"/chatTasks", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, asUser(req, user))
		return rec
	}

	if rec := save("alice", aliceSess, `"[]"`); rec.Code != http.StatusOK {
		t.Fatalf("alice saves her task: %d %s", rec.Code, rec.Body.String())
	}
	rec := save("bob", bobSess, `"[{\"text\":\"bob\"}]"`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), taskNotFoundMsg) {
		t.Errorf("bob saves alice's task id: %d %s, want 404 %q", rec.Code, rec.Body.String(), taskNotFoundMsg)
	}

	list := asUser(httptest.NewRequest("GET", "/api/v1/sessions/"+aliceSess+"/chatTasks", nil), "alice")
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, list)
	var listed PaginatedResponse[TaskResponse]
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode alice's chat tasks %q: %v", listRec.Body.String(), err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != taskID || listed.Data[0].MessageBubbles != "[]" {
		t.Errorf("alice's chat tasks after bob's attempt: %+v", listed.Data)
	}
}
