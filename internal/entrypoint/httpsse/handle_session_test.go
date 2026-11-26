// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var testTemplate *dbtest.Template

func TestMain(m *testing.M) {
	dbURL := os.Getenv("SAM_TEST_GATEWAY_HTTPSSE_DB_URL")
	if dbURL == "" {
		dbURL = os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	}
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_httpsse", func(u string) error {
		db, openErr := store.Open(u, slog.Default())
		if openErr != nil {
			return openErr
		}
		defer func() { _ = db.Close() }()
		return db.Migrate(context.Background())
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest template: %v\n", err)
		os.Exit(1)
	}
	testTemplate = tmpl
	code := m.Run()
	testTemplate.Close()
	os.Exit(code)
}

func testDBURL(t *testing.T, _ string) string {
	t.Helper()
	t.Parallel()
	return testTemplate.Clone(t)
}

func setupTestAdapterWithDB(t *testing.T) (*HTTPSSEAdapter, *entrypoint.GatewayCore, *store.SessionRepo, *store.ChatTaskRepo, *store.SSEEventRepo) {
	t.Helper()
	return setupTestAdapterWithDBConfig(t, nil)
}

func setupTestAdapterWithDBConfig(t *testing.T, overrides map[string]any) (*HTTPSSEAdapter, *entrypoint.GatewayCore, *store.SessionRepo, *store.ChatTaskRepo, *store.SSEEventRepo) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()

	appConfig := map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": testDBURL(t, "test"),
		},
	}
	maps.Copy(appConfig, overrides)

	core := entrypoint.NewGatewayCore(adapter, appConfig)

	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		t.Fatalf("load community flags: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithFeatureRegistry(flagRegistry),
	)
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	db := core.Store()
	if db == nil {
		t.Fatal("expected DB to be initialized")
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })

	return adapter, core, store.NewSessionRepo(db), store.NewChatTaskRepo(db), store.NewSSEEventRepo(db)
}

func seedChatTask(t *testing.T, chatRepo *store.ChatTaskRepo, sessionID string) {
	t.Helper()
	um := "hello"
	if err := chatRepo.Create(context.Background(), &store.ChatTask{
		ID:             idgen.New(),
		SessionID:      sessionID,
		UserID:         "sam_dev_user",
		UserMessage:    &um,
		MessageBubbles: `[]`,
		CreatedTime:    time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}
}

func TestListSessions_Empty(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["data"]; !ok {
		t.Error("missing 'data' key")
	}
	if _, ok := raw["meta"]; !ok {
		t.Error("missing 'meta' key")
	}

	data, ok := raw["data"].([]any)
	if !ok {
		t.Fatalf("data is not an array: %T", raw["data"])
	}
	if len(data) != 0 {
		t.Errorf("expected empty data, got %d items", len(data))
	}

	meta, ok := raw["meta"].(map[string]any)
	if !ok {
		t.Fatal("meta is not an object")
	}
	pagination, ok := meta["pagination"].(map[string]any)
	if !ok {
		t.Fatal("meta.pagination is not an object")
	}
	for _, key := range []string{"pageNumber", "pageSize", "totalPages", "count", "nextPage"} {
		if _, ok := pagination[key]; !ok {
			t.Errorf("missing pagination key: %s", key)
		}
	}
}

func TestListSessions_WithData(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1, s2 := idgen.New(), idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})
	_ = sessRepo.Create(ctx, &store.Session{ID: s2, UserID: "sam_dev_user"})
	seedChatTask(t, chatRepo, s1)
	seedChatTask(t, chatRepo, s2)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?pageNumber=1&pageSize=10", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.Count != 2 {
		t.Errorf("expected count=2, got %d", resp.Meta.Pagination.Count)
	}

	s := resp.Data[0]
	if s.ID == "" {
		t.Error("expected non-empty ID")
	}
	if s.UserID == "" {
		t.Error("expected non-empty userID")
	}
	if s.CreatedTime == "" {
		t.Error("expected non-empty createdTime")
	}
	if s.HasRunningBackgroundTask {
		t.Error("expected hasRunningBackgroundTask=false")
	}
}

func TestListSessions_BuilderTestSourceMarksEphemeral(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	src := ephemeralTestSessionSource
	testID := idgen.New()
	ctrlID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: testID, UserID: "sam_dev_user", Source: &src})
	_ = sessRepo.Create(ctx, &store.Session{ID: ctrlID, UserID: "sam_dev_user"})
	seedChatTask(t, chatRepo, testID)
	seedChatTask(t, chatRepo, ctrlID)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?pageNumber=1&pageSize=10", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]SessionResponse, len(resp.Data))
	for _, s := range resp.Data {
		byID[s.ID] = s
	}
	if !byID[testID].IsEphemeralTest {
		t.Error("builder_test session: IsEphemeralTest = false, want true (durable source, no live agent)")
	}
	if byID[ctrlID].IsEphemeralTest {
		t.Error("control session: IsEphemeralTest = true, want false")
	}
}

func TestListSessions_LiveAgentFallbackMarksEphemeral(t *testing.T) {
	adapter, core, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	const instance = "agent_ephemeral_live"
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: instance,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: agent.ExtensionURIDisplayName, Params: map[string]any{"display_name": "__test__abc__sam_dev_"}},
			},
		},
	})

	ctx := context.Background()
	agentID := instance
	id := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: id, UserID: "sam_dev_user", AgentID: &agentID})
	seedChatTask(t, chatRepo, id)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?pageNumber=1&pageSize=10", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range resp.Data {
		if s.ID == id {
			found = true
			if !s.IsEphemeralTest {
				t.Error("IsEphemeralTest = false, want true (live-registry fallback, source nil)")
			}
		}
	}
	if !found {
		t.Fatalf("seeded session %s not in list", id)
	}
}

func TestListSessions_Pagination(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	for range 5 {
		id := idgen.New()
		_ = sessRepo.Create(ctx, &store.Session{ID: id, UserID: "sam_dev_user"})
		seedChatTask(t, chatRepo, id)
	}

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?pageNumber=1&pageSize=3", nil)
	mux.ServeHTTP(rec, req)

	var resp PaginatedResponse[SessionResponse]
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if len(resp.Data) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.Count != 5 {
		t.Errorf("expected count=5, got %d", resp.Meta.Pagination.Count)
	}
	if resp.Meta.Pagination.TotalPages != 2 {
		t.Errorf("expected totalPages=2, got %d", resp.Meta.Pagination.TotalPages)
	}
	if resp.Meta.Pagination.NextPage == nil || *resp.Meta.Pagination.NextPage != 2 {
		t.Errorf("expected nextPage=2, got %v", resp.Meta.Pagination.NextPage)
	}
}

func TestGetSession_Found(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}", sessH.handleGet)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+s1, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var data map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"id", "userId", "name", "agentId", "project", "hasRunningBackgroundTask", "createdAt"} {
		if _, ok := data[key]; !ok {
			t.Errorf("missing field: %s", key)
		}
	}
}

func TestGetSession_NotFound(t *testing.T) {
	nonexistent := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}", sessH.handleGet)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+nonexistent, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["message"]; !ok {
		t.Error("missing 'message' key in error response")
	}
	if errID, ok := raw["errorId"].(string); !ok || errID == "" {
		t.Errorf("missing or empty 'errorId' in error response, got %v", raw["errorId"])
	}
}

func TestRenameSession(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/sessions/{id}", sessH.handleRename)

	body := `{"name":"New Title"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/sessions/"+s1, strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, hasData := raw["data"]; hasData {
		t.Error("PATCH response should NOT be wrapped in DataResponse")
	}
	if raw["id"] != s1 {
		t.Errorf("expected id=s1, got %v", raw["id"])
	}
}

func TestRenameSession_EmptyName(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/sessions/{id}", sessH.handleRename)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/sessions/"+s1, strings.NewReader(`{"name":""}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestDeleteSession(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", sessH.handleDelete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/sessions/"+s1, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Error("expected empty body for 204")
	}
}

func TestAssignProject_RejectsUnassignedSentinel(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/sessions/{id}/project", sessH.handleAssignProject)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/sessions/"+s1+"/project",
		strings.NewReader(`{"projectId":"`+store.UnassignedProjectFilter+`"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("assigning the unassigned sentinel: got %d, want 400", rec.Code)
	}

	session, err := sessRepo.GetByID(context.Background(), s1)
	if err != nil {
		t.Fatal(err)
	}
	if session.ProjectID != nil {
		t.Errorf("project_id = %v, want nil — the rejected write must not have landed", *session.ProjectID)
	}
}

func TestSearchSessions_ExcludesNeverStartedSessions(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	started := idgen.New()
	neverStarted := idgen.New()
	startedName := "Weather Started"
	neverName := "Weather Never Started"
	_ = sessRepo.Create(ctx, &store.Session{ID: started, UserID: "sam_dev_user", Name: &startedName})
	_ = sessRepo.Create(ctx, &store.Session{ID: neverStarted, UserID: "sam_dev_user", Name: &neverName})
	seedChatTask(t, chatRepo, started)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/search", sessH.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/search?query=weather", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != started {
		got := make([]string, 0, len(resp.Data))
		for _, s := range resp.Data {
			got = append(got, s.ID)
		}
		t.Fatalf("search returned %v, want only the started session %s", got, started)
	}
}

func TestListSessions_FiltersBySource(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	chatID := idgen.New()
	schedID := idgen.New()
	noSourceID := idgen.New()
	chatName, schedName, noSourceName := "Chat Session", "Scheduled Session", "Legacy No Source"
	scheduler := "scheduler"
	plain := "chat"
	_ = sessRepo.Create(ctx, &store.Session{ID: chatID, UserID: "sam_dev_user", Name: &chatName, Source: &plain})
	_ = sessRepo.Create(ctx, &store.Session{ID: schedID, UserID: "sam_dev_user", Name: &schedName, Source: &scheduler})
	_ = sessRepo.Create(ctx, &store.Session{ID: noSourceID, UserID: "sam_dev_user", Name: &noSourceName})
	for _, id := range []string{chatID, schedID, noSourceID} {
		seedChatTask(t, chatRepo, id)
	}

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	list := func(t *testing.T, query string) []string {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/sessions"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d, want 200", query, rec.Code)
		}
		var resp PaginatedResponse[SessionResponse]
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(resp.Data))
		for _, s := range resp.Data {
			ids = append(ids, s.ID)
		}
		slices.Sort(ids)
		return ids
	}

	t.Run("scheduler returns only scheduler sessions", func(t *testing.T) {
		if got := list(t, "?source=scheduler"); !slices.Equal(got, []string{schedID}) {
			t.Errorf("got %v, want only %s", got, schedID)
		}
	})

	t.Run("chat excludes scheduler and keeps a NULL source", func(t *testing.T) {
		want := []string{chatID, noSourceID}
		slices.Sort(want)
		if got := list(t, "?source=chat"); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("no source filter returns every session", func(t *testing.T) {
		if got := list(t, ""); len(got) != 3 {
			t.Errorf("got %d sessions, want all 3", len(got))
		}
	})

	t.Run("an unknown source is rejected rather than silently ignored", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/sessions?source=bogus", nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("source=bogus: got %d, want 400", rec.Code)
		}
	})
}

func TestSessionHandlers_RejectNonCanonicalUUIDs(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	id := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: id, UserID: "sam_dev_user"})
	seedChatTask(t, chatRepo, id)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)
	mux.HandleFunc("GET /api/v1/sessions/search", sessH.handleSearch)

	canonical := "01a01b28-ad54-7cf0-a0fc-e849ce76c1f4"
	for _, tc := range []struct {
		name  string
		value string
		want  int
	}{
		{"canonical is accepted", canonical, http.StatusOK},
		{"urn form would 500 on postgres", "urn:uuid:" + canonical, http.StatusBadRequest},
		{"braced form never matches on sqlite", "{" + canonical + "}", http.StatusBadRequest},
		{"unhyphenated form never matches on sqlite", strings.ReplaceAll(canonical, "-", ""), http.StatusBadRequest},
		{"uppercase never matches on sqlite", strings.ToUpper(canonical), http.StatusBadRequest},
		{"not a uuid at all", "nope", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/api/v1/sessions?projectId=", "/api/v1/sessions/search?query=x&projectId="} {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest("GET", path+url.QueryEscape(tc.value), nil))
				if rec.Code != tc.want {
					t.Errorf("%s%s: got %d, want %d", path, tc.value, rec.Code, tc.want)
				}
			}
		})
	}
}

func TestListSessions_RejectsMalformedProjectId(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	id := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: id, UserID: "sam_dev_user"})
	seedChatTask(t, chatRepo, id)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?projectId=not-a-uuid", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed projectId on list: got %d, want 400", rec.Code)
	}
}

func TestAssignProject(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/sessions/{id}/project", sessH.handleAssignProject)

	rec := httptest.NewRecorder()
	projID := idgen.New()
	req := httptest.NewRequest("PATCH", "/api/v1/sessions/"+s1+"/project", strings.NewReader(`{"projectId":"`+projID+`"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, hasData := raw["data"]; hasData {
		t.Error("PATCH project response should NOT be wrapped in DataResponse")
	}
}

func TestUnconsumedEvents_NoEvents(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/events/unconsumed", sessH.handleUnconsumedEvents)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/events/unconsumed", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"hasEvents", "taskIds", "sessionId", "eventsByTask"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing camelCase key: %s", key)
		}
	}
	if raw["hasEvents"] != false {
		t.Errorf("expected has_events=false, got %v", raw["hasEvents"])
	}
}

func TestUnconsumedEvents_SkipsRunningTask(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)
	ctx := context.Background()
	s1 := idgen.New()
	running := idgen.New()
	done := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})
	for _, tid := range []string{running, done} {
		_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
			TaskID: tid, SessionID: s1, UserID: "sam_dev_user",
			EventSequence: 1, EventType: "status_update", EventData: `{}`,
		})
	}
	_ = adapter.core.Tasks().Register(running, "Agent", "sam_dev_user", entrypoint.TaskIdentity{})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/events/unconsumed", sessH.handleUnconsumedEvents)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/events/unconsumed", nil)
	mux.ServeHTTP(rec, req)

	var resp UnconsumedEventsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.EventsByTask[running]; ok {
		t.Error("running task must be omitted from the unconsumed-events response")
	}
	if _, ok := resp.EventsByTask[done]; !ok {
		t.Error("completed task must be present in the unconsumed-events response")
	}
}

func TestUnconsumedEvents_WithEvents(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	t1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: t1, SessionID: s1, UserID: "sam_dev_user",
		EventSequence: 1, EventType: "status_update",
		EventData: `{"jsonrpc":"2.0","result":{"kind":"status-update"}}`,
	})
	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: t1, SessionID: s1, UserID: "sam_dev_user",
		EventSequence: 2, EventType: "final_response",
		EventData: `{"jsonrpc":"2.0","result":{"kind":"task"}}`,
	})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/{id}/events/unconsumed", sessH.handleUnconsumedEvents)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/"+s1+"/events/unconsumed", nil)
	mux.ServeHTTP(rec, req)

	var resp UnconsumedEventsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if !resp.HasEvents {
		t.Error("expected has_events=true")
	}
	if len(resp.TaskIDs) != 1 || resp.TaskIDs[0] != t1 {
		t.Errorf("expected task_ids=[t1], got %v", resp.TaskIDs)
	}
	taskEvents, ok := resp.EventsByTask[t1]
	if !ok {
		t.Fatal("missing events for task t1")
	}
	if len(taskEvents.Events) != 2 {
		t.Errorf("expected 2 events, got %d", len(taskEvents.Events))
	}

	wrap, ok := taskEvents.Events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("event Data should be wrapper object, got %T", taskEvents.Events[0].Data)
	}
	if wrap["event"] != "status_update" {
		t.Errorf("wrapper event = %v, want \"status_update\" (inner event mirrors the stored event_type — Python wire shape)", wrap["event"])
	}
	inner, ok := wrap["data"].(string)
	if !ok {
		t.Fatalf("wrapper data should be a JSON string, got %T", wrap["data"])
	}
	if inner != `{"jsonrpc":"2.0","result":{"kind":"status-update"}}` {
		t.Errorf("wrapper data = %q, want the raw envelope string", inner)
	}
}

func TestGenerateTitle_AlreadyExists(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	name := "Existing Title"
	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user", Name: &name})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	body := `{"userMessage":"hello","agentResponse":"hi","force":false}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (title already exists, synchronous), got %d", rec.Code)
	}

	var raw map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&raw)
	if raw["skipped"] != true {
		t.Errorf("expected skipped=true, got %v", raw["skipped"])
	}
}

func TestGenerateTitle_NoLLM(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	body := `{"userMessage":"What is the weather today?","agentResponse":"","force":false}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (fallback title, synchronous), got %d", rec.Code)
	}

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name == "" {
		t.Error("expected session name to be set via fallback")
	}
}

type mockLLMClient struct {
	response   string
	err        error
	noToolCall bool
}

func (m *mockLLMClient) ChatCompletion(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(req.Tools) > 0 && !m.noToolCall {
		return &llm.ChatResponse{
			ToolCalls: []llm.ToolCall{{
				ID:        "call_1",
				Name:      req.Tools[0].Name,
				Arguments: m.response,
			}},
			FinishReason: "tool_calls",
		}, nil
	}
	return &llm.ChatResponse{Content: m.response, FinishReason: "stop"}, nil
}

func (m *mockLLMClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, fmt.Errorf("not implemented")
}

func setupTestAdapterWithDBAndLLM(t *testing.T, llmClient llm.Client) (*HTTPSSEAdapter, *entrypoint.GatewayCore, *store.SessionRepo, *store.ChatTaskRepo, *store.SSEEventRepo) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()

	appConfig := map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": testDBURL(t, "llm"),
		},
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		t.Fatalf("load community flags: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithDefaultLLM(llmClient),
		runtime.WithFeatureRegistry(flagRegistry),
	)
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	db := core.Store()
	if db == nil {
		t.Fatal("expected DB to be initialized")
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })

	return adapter, core, store.NewSessionRepo(db), store.NewChatTaskRepo(db), store.NewSSEEventRepo(db)
}

func TestGenerateTitle_WithLLM(t *testing.T) {
	s1 := idgen.New()
	mock := &mockLLMClient{response: "Weather Discussion"}
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	body := `{"userMessage":"What is the weather today?","agentResponse":"It is sunny and warm."}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}

	time.Sleep(200 * time.Millisecond)

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name != "Weather Discussion" {
		t.Errorf("expected title 'Weather Discussion', got %v", session.Name)
	}
}

func saveChatTaskBody(t *testing.T, autoGenerateTitle bool) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id":                idgen.New(),
		"userMessage":       "What is the weather today?",
		"messageBubbles":    `[{"role":"agent","parts":[{"text":"It is sunny and warm."}]}]`,
		"autoGenerateTitle": autoGenerateTitle,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestSaveChatTask_AutoGeneratesTitle(t *testing.T) {
	s1 := idgen.New()
	mock := &mockLLMClient{response: "Weather Discussion"}
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, sessH: sessH, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/chatTasks", strings.NewReader(saveChatTaskBody(t, true)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	time.Sleep(200 * time.Millisecond)

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name != "Weather Discussion" {
		t.Errorf("expected auto-generated title 'Weather Discussion', got %v", session.Name)
	}
}

func TestSaveChatTask_NoAutoTitleWhenNotRequested(t *testing.T) {
	s1 := idgen.New()
	mock := &mockLLMClient{response: "Weather Discussion"}
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, sessH: sessH, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/chatTasks", strings.NewReader(saveChatTaskBody(t, false)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	time.Sleep(200 * time.Millisecond)

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name != nil {
		t.Errorf("expected no title without the auto-title hint, got %v", *session.Name)
	}
}

func TestSaveChatTask_DoesNotRetitleAlreadyTitled(t *testing.T) {
	s1 := idgen.New()
	existing := "My Real Title"
	mock := &mockLLMClient{response: "Weather Discussion"}
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user", Name: &existing})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, sessH: sessH, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/chatTasks", strings.NewReader(saveChatTaskBody(t, true)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	time.Sleep(200 * time.Millisecond)

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name != existing {
		t.Errorf("expected title unchanged (%q), got %v", existing, session.Name)
	}
}

func TestSaveChatTask_FallsBackToUserMessageWhenNoLLM(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: adapter.logger}
	chatH := &chatTaskHandler{core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, sessH: sessH, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/chatTasks", strings.NewReader(saveChatTaskBody(t, true)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name != "What is the weather today?" {
		t.Errorf("expected fallback title from user message, got %v", session.Name)
	}
}

func TestGenerateTitle_LLMError_FallsBackToUserMessage(t *testing.T) {
	s1 := idgen.New()
	mock := &mockLLMClient{err: fmt.Errorf("LLM unavailable")}
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	body := `{"userMessage":"Tell me about Golang concurrency patterns","agentResponse":"Go uses goroutines."}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}

	time.Sleep(200 * time.Millisecond)

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name == "" {
		t.Error("expected fallback title from user message")
	}
	if session.Name != nil && *session.Name != "Tell me about Golang concurrency patterns" {
		t.Errorf("expected fallback to user message, got %q", *session.Name)
	}
}

func TestGenerateTitle_LLMReturnsEmpty_FallsBackToNewChat(t *testing.T) {
	s1 := idgen.New()
	mock := &mockLLMClient{response: "   "}
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	body := `{"userMessage":"","agentResponse":""}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}

	time.Sleep(200 * time.Millisecond)

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name != "New Chat" {
		t.Errorf("expected 'New Chat' fallback, got %v", session.Name)
	}
}

func TestGenerateTitle_ForceOverridesExisting(t *testing.T) {
	s1 := idgen.New()
	mock := &mockLLMClient{response: "Updated Title"}
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	existingName := "Old Title"
	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user", Name: &existingName})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	body := `{"userMessage":"hello","agentResponse":"hi","force":true}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}

	time.Sleep(200 * time.Millisecond)

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name != "Updated Title" {
		t.Errorf("expected 'Updated Title', got %v", session.Name)
	}
}

func TestGenerateTitle_LongInputTruncated(t *testing.T) {
	s1 := idgen.New()
	mock := &mockLLMClient{response: "Summary Title"}
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	longMsg := strings.Repeat("x", 2000)
	longResp := strings.Repeat("y", 5000)
	body := fmt.Sprintf(`{"userMessage":%q,"agentResponse":%q}`, longMsg, longResp)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}

	time.Sleep(200 * time.Millisecond)

	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name != "Summary Title" {
		t.Errorf("expected 'Summary Title', got %v", session.Name)
	}
}

type gatedLLMClient struct {
	mockLLMClient
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (m *gatedLLMClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.calls.Add(1)
	m.entered <- struct{}{}
	<-m.release
	return &llm.ChatResponse{Content: "Gated Title", FinishReason: "stop"}, nil
}

func TestGenerateTitle_InFlightDedup_OneCallPerSession(t *testing.T) {
	s1 := idgen.New()
	mock := &gatedLLMClient{entered: make(chan struct{}, 8), release: make(chan struct{})}
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	post := func() int {
		rec := httptest.NewRecorder()
		body := `{"userMessage":"hello there","agentResponse":"hi"}`
		req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(body))
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post(); code != http.StatusAccepted {
		t.Fatalf("first POST: expected 202, got %d", code)
	}
	select {
	case <-mock.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first title generation never reached the LLM call")
	}

	for i := range 3 {
		if code := post(); code != http.StatusAccepted {
			t.Fatalf("re-POST %d: expected 202, got %d", i, code)
		}
	}

	close(mock.release)
	time.Sleep(200 * time.Millisecond)

	if got := mock.calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 LLM call across 4 concurrent-window POSTs, got %d", got)
	}
	session, _ := sessRepo.GetByID(context.Background(), s1)
	if session.Name == nil || *session.Name != "Gated Title" {
		t.Errorf("expected title 'Gated Title', got %v", session.Name)
	}
}

type countingErrLLMClient struct {
	mockLLMClient
	calls atomic.Int32
}

func (m *countingErrLLMClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.calls.Add(1)
	return nil, fmt.Errorf("simulated provider error")
}

func TestGenerateTitle_LockReleasedAfterLLMError(t *testing.T) {
	s1 := idgen.New()
	mock := &countingErrLLMClient{}
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	post := func() int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(`{"userMessage":"","agentResponse":""}`))
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	post()
	time.Sleep(200 * time.Millisecond)
	post()
	time.Sleep(200 * time.Millisecond)

	if got := mock.calls.Load(); got != 2 {
		t.Fatalf("lock not released after LLM error: got %d LLM calls across 2 sequential POSTs, want 2", got)
	}
}

func TestGenerateTitle_LockIsPerSession(t *testing.T) {
	s1, s2 := idgen.New(), idgen.New()
	mock := &gatedLLMClient{entered: make(chan struct{}, 8), release: make(chan struct{})}
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, mock)

	_ = sessRepo.Create(context.Background(), &store.Session{ID: s1, UserID: "sam_dev_user"})
	_ = sessRepo.Create(context.Background(), &store.Session{ID: s2, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	post := func(sid string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/generateTitle", strings.NewReader(`{"userMessage":"hello","agentResponse":"hi"}`))
		mux.ServeHTTP(rec, req)
	}

	post(s1)
	post(s2)

	for i := range 2 {
		select {
		case <-mock.entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 2 sessions reached the LLM call — lock is not per-session", i)
		}
	}
	if got := mock.calls.Load(); got != 2 {
		t.Fatalf("expected 2 concurrent per-session generations, got %d", got)
	}
	close(mock.release)
	time.Sleep(200 * time.Millisecond)
}

func TestGenerateTitle_InvalidBody(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader("not json"))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestGenerateTitle_SessionNotFound(t *testing.T) {
	nonexistent := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	body := `{"userMessage":"hi","agentResponse":"hello"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+nonexistent+"/generateTitle", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello"},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"abé", 3, "ab"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestEnrichTitleMessages_FromChatTasks(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	userMsg := "What is the weather in London?"
	_ = chatRepo.Create(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: s1, UserID: "sam_dev_user",
		UserMessage:    &userMsg,
		MessageBubbles: `[{"role":"agent","parts":[{"text":"It is sunny and 22°C in London."}]}]`,
		CreatedTime:    time.Now().UnixMilli(),
	})

	h := &sessionHandler{
		core:     adapter.core,
		sessRepo: sessRepo,
		chatRepo: chatRepo,
		sseRepo:  sseRepo,
		logger:   adapter.logger,
	}

	var gotUser, gotAgent string
	h.enrichTitleMessages(ctx, s1, &gotUser, &gotAgent)

	if gotUser != "What is the weather in London?" {
		t.Errorf("expected user message from chat task, got %q", gotUser)
	}
	if gotAgent != "It is sunny and 22°C in London." {
		t.Errorf("expected agent response from chat task bubbles, got %q", gotAgent)
	}
}

func TestEnrichTitleMessages_FromTaskInitialRequestText(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	db := adapter.core.Store()
	taskRepo := store.NewTaskRepo(db)

	ctx := context.Background()
	s1 := idgen.New()
	taskID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	initialText := "Tell me about Go concurrency"
	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "sam_dev_user", StartTime: time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &s1,
		InitialRequestText: &initialText,
	})

	h := &sessionHandler{
		core:     adapter.core,
		sessRepo: sessRepo,
		chatRepo: chatRepo,
		taskRepo: taskRepo,
		sseRepo:  sseRepo,
		logger:   adapter.logger,
	}

	var gotUser, gotAgent string
	h.enrichTitleMessages(ctx, s1, &gotUser, &gotAgent)

	if gotUser != "Tell me about Go concurrency" {
		t.Errorf("expected user message from task initial_request_text, got %q", gotUser)
	}
}

func TestEnrichTitleMessages_FromSSEFinalResponse(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	db := adapter.core.Store()
	taskRepo := store.NewTaskRepo(db)

	ctx := context.Background()
	s1 := idgen.New()
	taskID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "sam_dev_user", StartTime: time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &s1,
	})

	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskID, SessionID: s1, UserID: "sam_dev_user",
		EventSequence: 1, EventType: "final_response",
		EventData: `{"jsonrpc":"2.0","result":{"kind":"task","task":{"artifacts":[{"parts":[{"kind":"text","text":"Go uses goroutines for concurrency."}]}]}}}`,
	})

	h := &sessionHandler{
		core:     adapter.core,
		sessRepo: sessRepo,
		chatRepo: chatRepo,
		taskRepo: taskRepo,
		sseRepo:  sseRepo,
		logger:   adapter.logger,
	}

	var gotUser, gotAgent string
	h.enrichTitleMessages(ctx, s1, &gotUser, &gotAgent)

	if gotAgent != "Go uses goroutines for concurrency." {
		t.Errorf("expected agent response from SSE final_response, got %q", gotAgent)
	}
}

func TestEnrichTitleMessages_FromSSEStatusUpdates(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	db := adapter.core.Store()
	taskRepo := store.NewTaskRepo(db)

	ctx := context.Background()
	s1 := idgen.New()
	taskID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "sam_dev_user", StartTime: time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &s1,
	})

	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskID, SessionID: s1, UserID: "sam_dev_user",
		EventSequence: 1, EventType: "status_update",
		EventData: `{"jsonrpc":"2.0","result":{"kind":"status-update","status":{"message":{"parts":[{"text":"Hello "}]}}}}`,
	})
	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskID, SessionID: s1, UserID: "sam_dev_user",
		EventSequence: 2, EventType: "status_update",
		EventData: `{"jsonrpc":"2.0","result":{"kind":"status-update","status":{"message":{"parts":[{"text":"World!"}]}}}}`,
	})

	h := &sessionHandler{
		core:     adapter.core,
		sessRepo: sessRepo,
		chatRepo: chatRepo,
		taskRepo: taskRepo,
		sseRepo:  sseRepo,
		logger:   adapter.logger,
	}

	var gotUser, gotAgent string
	h.enrichTitleMessages(ctx, s1, &gotUser, &gotAgent)

	if gotAgent != "Hello World!" {
		t.Errorf("expected accumulated status_update text 'Hello World!', got %q", gotAgent)
	}
}

func TestEnrichTitleMessages_NoEnrichmentNeeded(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	h := &sessionHandler{
		core:     adapter.core,
		sessRepo: sessRepo,
		chatRepo: chatRepo,
		sseRepo:  sseRepo,
		logger:   adapter.logger,
	}

	gotUser := "already provided"
	gotAgent := "already provided too"
	h.enrichTitleMessages(ctx, s1, &gotUser, &gotAgent)

	if gotUser != "already provided" {
		t.Errorf("expected user message unchanged, got %q", gotUser)
	}
	if gotAgent != "already provided too" {
		t.Errorf("expected agent response unchanged, got %q", gotAgent)
	}
}

func TestEnrichTitleMessages_EmptyResults(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	db := adapter.core.Store()
	taskRepo := store.NewTaskRepo(db)

	ctx := context.Background()
	s1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user"})

	h := &sessionHandler{
		core:     adapter.core,
		sessRepo: sessRepo,
		chatRepo: chatRepo,
		taskRepo: taskRepo,
		sseRepo:  sseRepo,
		logger:   adapter.logger,
	}

	var gotUser, gotAgent string
	h.enrichTitleMessages(ctx, s1, &gotUser, &gotAgent)

	if gotUser != "" {
		t.Errorf("expected empty user message, got %q", gotUser)
	}
	if gotAgent != "" {
		t.Errorf("expected empty agent response, got %q", gotAgent)
	}
}

func TestSearchSessions(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	s2 := idgen.New()
	name1 := "Weather Chat"
	name2 := "Code Review"
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user", Name: &name1})
	_ = sessRepo.Create(ctx, &store.Session{ID: s2, UserID: "sam_dev_user", Name: &name2})
	seedChatTask(t, chatRepo, s1)
	seedChatTask(t, chatRepo, s2)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/search", sessH.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/search?query=Weather", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 result, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != s1 {
		t.Errorf("expected session s1, got %s", resp.Data[0].ID)
	}
}

func TestSearchSessions_CaseInsensitive(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	name1 := "Weather Chat"
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user", Name: &name1})
	seedChatTask(t, chatRepo, s1)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/search", sessH.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/search?query=weather", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != s1 {
		t.Fatalf("lowercase query returned %d results, want session %s", len(resp.Data), s1)
	}
}

func TestSearchSessions_FiltersByProject(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	inProject := idgen.New()
	noProject := idgen.New()
	projectID := idgen.New()
	name1 := "Weather Chat"
	name2 := "Weather Report"
	_ = sessRepo.Create(ctx, &store.Session{ID: inProject, UserID: "sam_dev_user", Name: &name1})
	_ = sessRepo.Create(ctx, &store.Session{ID: noProject, UserID: "sam_dev_user", Name: &name2})
	seedChatTask(t, chatRepo, inProject)
	seedChatTask(t, chatRepo, noProject)
	if err := sessRepo.AssignProject(ctx, inProject, projectID); err != nil {
		t.Fatal(err)
	}

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/search", sessH.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/search?query=weather&projectId="+projectID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != inProject {
		t.Fatalf("project-scoped search returned %d results, want only %s", len(resp.Data), inProject)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/sessions/search?query=weather&projectId=not-a-uuid", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed projectId: got %d, want 400", rec.Code)
	}
}

func TestUnassignedProjectFilterWireValue(t *testing.T) {
	if store.UnassignedProjectFilter != "__no_project__" {
		t.Errorf("sentinel = %q, want __no_project__ (must match NO_PROJECT_VALUE in webui/src/features/chat/projectFilter.ts)", store.UnassignedProjectFilter)
	}
}

func TestUnassignedProjectFilter_SearchAndList(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	unassigned := idgen.New()
	assigned := idgen.New()
	projectID := idgen.New()
	name1 := "Weather Unassigned"
	name2 := "Weather Assigned"
	_ = sessRepo.Create(ctx, &store.Session{ID: unassigned, UserID: "sam_dev_user", Name: &name1})
	_ = sessRepo.Create(ctx, &store.Session{ID: assigned, UserID: "sam_dev_user", Name: &name2})
	seedChatTask(t, chatRepo, unassigned)
	seedChatTask(t, chatRepo, assigned)
	if err := sessRepo.AssignProject(ctx, assigned, projectID); err != nil {
		t.Fatal(err)
	}

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions/search", sessH.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions/search?query=weather&projectId="+store.UnassignedProjectFilter, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != unassigned {
		got := make([]string, 0, len(resp.Data))
		for _, s := range resp.Data {
			got = append(got, s.ID)
		}
		t.Fatalf("unassigned-scoped search returned %v, want only %s", got, unassigned)
	}

	listMux := http.NewServeMux()
	listMux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/sessions?projectId="+store.UnassignedProjectFilter, nil)
	listMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list with unassigned sentinel: got %d, want 200", rec.Code)
	}
	var listResp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].ID != unassigned {
		t.Errorf("unassigned-scoped list returned %d rows, want only %s", len(listResp.Data), unassigned)
	}
}

func TestListSessions_PageNumberAlias(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	for range 5 {
		id := idgen.New()
		_ = sessRepo.Create(ctx, &store.Session{ID: id, UserID: "sam_dev_user"})
		seedChatTask(t, chatRepo, id)
	}

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?pageNumber=2&pageSize=2", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 sessions on page 2, got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.PageNumber != 2 {
		t.Errorf("expected pageNumber=2, got %d", resp.Meta.Pagination.PageNumber)
	}
	if resp.Meta.Pagination.Count != 5 {
		t.Errorf("expected count=5, got %d", resp.Meta.Pagination.Count)
	}
}

func TestListSessions_ProjectIdAlias(t *testing.T) {
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	s1 := idgen.New()
	proj := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: s1, UserID: "sam_dev_user", ProjectID: &proj})
	_ = sessRepo.Create(ctx, &store.Session{ID: idgen.New(), UserID: "sam_dev_user"})
	seedChatTask(t, chatRepo, s1)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?projectId="+proj, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 session with project_id filter, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != s1 {
		t.Errorf("expected session s1, got %s", resp.Data[0].ID)
	}
}

func TestMarkSessionViewed_OK(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	sid := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/viewed", sessH.handleMarkViewed)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/viewed", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	v, ok := body["lastViewedAt"]
	if !ok {
		t.Fatalf("missing 'lastViewedAt' key (body=%v)", body)
	}
	viewedAt, ok := v.(string)
	if !ok {
		t.Fatalf("lastViewedAt not a string (ISO 8601): %T", v)
	}
	if viewedAt == "" {
		t.Errorf("lastViewedAt = %q, expected non-empty ISO 8601 timestamp", viewedAt)
	}

	got, _ := sessRepo.GetByID(ctx, sid)
	if got.LastViewedAt == nil {
		t.Fatal("expected last_viewed_at to be persisted")
	}
}

func TestMarkSessionViewed_NotFound(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/viewed", sessH.handleMarkViewed)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+idgen.New()+"/viewed", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestMarkSessionViewed_RejectsPlaceholders(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/viewed", sessH.handleMarkViewed)

	for _, sid := range []string{"null", "undefined"} {
		t.Run(sid, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/viewed", nil)
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("expected 404 for placeholder %q, got %d", sid, rec.Code)
			}
		})
	}
}

func TestMarkSessionViewed_RejectsOtherOwner(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)

	ctx := context.Background()
	sid := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "someone-else"})

	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/viewed", sessH.handleMarkViewed)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions/"+sid+"/viewed", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when caller does not own the session, got %d", rec.Code)
	}
	got, _ := sessRepo.GetByID(ctx, sid)
	if got.LastViewedAt != nil {
		t.Errorf("last_viewed_at mutated for other owner's session: %d", *got.LastViewedAt)
	}
}

func TestListSessions_ExcludesExternalGatewaySessions(t *testing.T) {
	adapter, core, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	db := core.Store()
	schedRepo := store.NewScheduledTaskRepo(db)
	sessKeyRepo := store.NewGatewaySessionKeyRepo(db)
	ctx := context.Background()

	webID := idgen.New()
	chatSrc := "chat"
	webName := "Weather please"
	if err := sessRepo.Create(ctx, &store.Session{ID: webID, UserID: "sam_dev_user", Source: &chatSrc, Name: &webName}); err != nil {
		t.Fatal(err)
	}
	seedChatTask(t, chatRepo, webID)

	slackID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: slackID, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	if err := sessKeyRepo.CreateMapping(ctx, store.GatewayTypeSlack, "C1:169.5", slackID); err != nil {
		t.Fatal(err)
	}
	seedChatTask(t, chatRepo, slackID)

	sessH := &sessionHandler{
		core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo,
		scheduledRepo: schedRepo, sessKeyRepo: sessKeyRepo, logger: adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?pageNumber=1&pageSize=20", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 session (WebUI only), got %d", len(resp.Data))
	}
	if resp.Data[0].ID != webID {
		t.Errorf("expected WebUI session %s, got %s", webID, resp.Data[0].ID)
	}
}

func TestListSessions_EnrichesSchedulerSessions(t *testing.T) {
	adapter, core, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	db := core.Store()
	schedRepo := store.NewScheduledTaskRepo(db)
	sessKeyRepo := store.NewGatewaySessionKeyRepo(db)

	ctx := context.Background()

	task := &store.ScheduledTask{
		ID:                 idgen.New(),
		Name:               "daily weather report",
		Namespace:          "test-ns",
		CreatedBy:          "sam_dev_user",
		ScheduleType:       "cron",
		ScheduleExpression: "0 9 * * *",
		Timezone:           "UTC",
		TargetAgentName:    "WeatherAgent",
		TargetType:         "agent",
		TaskMessage:        `[{"type":"text","text":"weather"}]`,
		Enabled:            true,
	}
	if err := schedRepo.Create(ctx, task); err != nil {
		t.Fatal(err)
	}

	execID := idgen.New()
	_ = schedRepo.CreateExecution(ctx, &store.ScheduledTaskExecution{
		ID:              execID,
		ScheduledTaskID: task.ID,
		Status:          "completed",
		ScheduledFor:    time.Now().UnixMilli(),
	})

	sid := idgen.New()
	src := "scheduler"
	agentName := "WeatherAgent"
	if err := sessRepo.Create(ctx, &store.Session{
		ID: sid, UserID: "sam_dev_user", AgentID: &agentName, Source: &src,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessKeyRepo.CreateMapping(ctx, store.GatewayTypeScheduled, execID, sid); err != nil {
		t.Fatal(err)
	}

	chatID := idgen.New()
	chatSrc := "chat"
	_ = sessRepo.Create(ctx, &store.Session{
		ID: chatID, UserID: "sam_dev_user", Source: &chatSrc,
	})
	seedChatTask(t, chatRepo, chatID)

	sessH := &sessionHandler{
		core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo,
		scheduledRepo: schedRepo, sessKeyRepo: sessKeyRepo, logger: adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?pageNumber=1&pageSize=20", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(resp.Data))
	}

	var sched, chat *SessionResponse
	for i := range resp.Data {
		switch resp.Data[i].ID {
		case sid:
			sched = &resp.Data[i]
		case chatID:
			chat = &resp.Data[i]
		}
	}
	if sched == nil || chat == nil {
		t.Fatalf("missing expected sessions in response: sched=%v chat=%v", sched, chat)
	}

	if sched.ScheduledTaskID == nil || *sched.ScheduledTaskID != task.ID {
		t.Errorf("scheduledTaskId = %v, want %q", sched.ScheduledTaskID, task.ID)
	}
	if sched.ScheduledTaskName == nil || *sched.ScheduledTaskName != task.Name {
		t.Errorf("scheduledTaskName = %v, want %q", sched.ScheduledTaskName, task.Name)
	}
	if sched.Source == nil || *sched.Source != "scheduler" {
		t.Errorf("source = %v, want 'scheduler'", sched.Source)
	}

	if chat.ScheduledTaskID != nil {
		t.Errorf("chat session got scheduledTaskId=%q — enrichment leaked", *chat.ScheduledTaskID)
	}
	if chat.ScheduledTaskName != nil {
		t.Errorf("chat session got scheduledTaskName=%q — enrichment leaked", *chat.ScheduledTaskName)
	}
}

func TestListSessions_EnrichesSchedulerSessions_MissingExecution(t *testing.T) {
	adapter, core, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)
	schedRepo := store.NewScheduledTaskRepo(core.Store())
	sessKeyRepo := store.NewGatewaySessionKeyRepo(core.Store())

	ctx := context.Background()
	missingExec := idgen.New()
	sid := idgen.New()
	src := "scheduler"
	if err := sessRepo.Create(ctx, &store.Session{
		ID: sid, UserID: "sam_dev_user", Source: &src,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessKeyRepo.CreateMapping(ctx, store.GatewayTypeScheduled, missingExec, sid); err != nil {
		t.Fatal(err)
	}

	sessH := &sessionHandler{
		core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo,
		scheduledRepo: schedRepo, sessKeyRepo: sessKeyRepo, logger: adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", sessH.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions?pageNumber=1&pageSize=20", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even with missing execution, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[SessionResponse]
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 1 {
		t.Fatalf("session was dropped by enrichment: got %d rows", len(resp.Data))
	}
	if resp.Data[0].ScheduledTaskID != nil || resp.Data[0].ScheduledTaskName != nil {
		t.Errorf("expected nil scheduledTaskId/Name when execution row is missing, got %v / %v",
			resp.Data[0].ScheduledTaskID, resp.Data[0].ScheduledTaskName)
	}
}

func TestIsEphemeralTestSession(t *testing.T) {
	adapter, core, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)
	h := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}

	const testInstance = "agent_ephemeral"
	const regularInstance = "agent_regular"
	const nilParamInstance = "agent_nil_param"
	const typoURIInstance = "agent_typo_uri"
	const wrongTypeInstance = "agent_wrong_type"
	const multiExtInstance = "agent_multi_ext"

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: testInstance,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: agent.ExtensionURIDisplayName, Params: map[string]any{"display_name": "__test__abc__sam_dev_"}},
			},
		},
	})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: regularInstance,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: agent.ExtensionURIDisplayName, Params: map[string]any{"display_name": "Builder"}},
			},
		},
	})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: nilParamInstance,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: agent.ExtensionURIDisplayName, Params: nil},
			},
		},
	})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: typoURIInstance,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: "https://example.com/something-else", Params: map[string]any{"display_name": "__test__abc__sam_dev_"}},
			},
		},
	})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: wrongTypeInstance,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: agent.ExtensionURIDisplayName, Params: map[string]any{"display_name": 42}},
			},
		},
	})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: multiExtInstance,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: "https://example.com/unrelated", Params: map[string]any{"display_name": "decoy"}},
				{URI: agent.ExtensionURIDisplayName, Params: map[string]any{"display_name": "__test__abc__sam_dev_"}},
			},
		},
	})

	missing := "agent_not_in_registry"

	cases := []struct {
		name  string
		input *string
		want  bool
	}{
		{"nil agentId", nil, false},
		{"empty agentId", ptr(""), false},
		{"agent not in registry", &missing, false},
		{"card with __test__ display name", ptr(testInstance), true},
		{"card with regular display name", ptr(regularInstance), false},
		{"card with nil ext params", ptr(nilParamInstance), false},
		{"display-name on the wrong URI", ptr(typoURIInstance), false},
		{"display_name is not a string", ptr(wrongTypeInstance), false},
		{"display-name extension second in slice", ptr(multiExtInstance), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := h.isEphemeralTestSession(c.input)
			if got != c.want {
				t.Errorf("isEphemeralTestSession(%v) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func newTitleTestHandler(t *testing.T, llmClient *mockLLMClient) (*sessionHandler, *store.SessionRepo) {
	t.Helper()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDBAndLLM(t, llmClient)
	return &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}, sessRepo
}

func generateTitleNow(t *testing.T, h *sessionHandler, sessionID string, force bool) {
	t.Helper()
	h.generateTitleAsync(sessionID, "hello", "hi", h.titleModel(t.Context()), nil, force, func() {})
}

func sessionName(t *testing.T, repo *store.SessionRepo, id string) string {
	t.Helper()
	session, err := repo.GetByID(t.Context(), id)
	if err != nil || session.Name == nil {
		t.Fatalf("read session %s: %v", id, err)
	}
	return *session.Name
}

func TestGenerateTitle_KeepsARenameMadeDuringGeneration(t *testing.T) {
	h, repo := newTitleTestHandler(t, &mockLLMClient{response: "Generated Title"})
	id := idgen.New()
	_ = repo.Create(t.Context(), &store.Session{ID: id, UserID: "sam_dev_user"})
	if err := repo.Rename(t.Context(), id, "Renamed By Hand"); err != nil {
		t.Fatal(err)
	}

	generateTitleNow(t, h, id, false)

	if got := sessionName(t, repo, id); got != "Renamed By Hand" {
		t.Errorf("name = %q, want the rename kept", got)
	}
}

func TestGenerateTitle_ASecondGenerationKeepsTheFirstTitle(t *testing.T) {
	llmClient := &mockLLMClient{response: "First Title"}
	h, repo := newTitleTestHandler(t, llmClient)
	id := idgen.New()
	placeholder := placeholderSessionTitle
	_ = repo.Create(t.Context(), &store.Session{ID: id, UserID: "sam_dev_user", Name: &placeholder})

	generateTitleNow(t, h, id, false)
	llmClient.response = "Second Title"
	generateTitleNow(t, h, id, false)

	if got := sessionName(t, repo, id); got != "First Title" {
		t.Errorf("name = %q, want the first title kept", got)
	}
}

func TestGenerateTitle_ForcedGenerationReplacesTheTitle(t *testing.T) {
	llmClient := &mockLLMClient{response: "First Title"}
	h, repo := newTitleTestHandler(t, llmClient)
	id := idgen.New()
	_ = repo.Create(t.Context(), &store.Session{ID: id, UserID: "sam_dev_user"})

	generateTitleNow(t, h, id, false)
	llmClient.response = "Regenerated Title"
	generateTitleNow(t, h, id, true)

	if got := sessionName(t, repo, id); got != "Regenerated Title" {
		t.Errorf("name = %q, want the regenerated title", got)
	}
}

func TestGenerateTitle_NoLLMForceReplacesTheTitle(t *testing.T) {
	s1 := idgen.New()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)
	existing := "Old Title"
	_ = sessRepo.Create(t.Context(), &store.Session{ID: s1, UserID: "sam_dev_user", Name: &existing})
	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", sessH.handleGenerateTitle)

	body := `{"userMessage":"What is the weather today?","agentResponse":"","force":true}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/sessions/"+s1+"/generateTitle", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := sessionName(t, sessRepo, s1); got != "What is the weather today?" {
		t.Errorf("name = %q, want the fallback title", got)
	}
}
