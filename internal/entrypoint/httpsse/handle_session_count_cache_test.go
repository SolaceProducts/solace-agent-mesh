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
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func seedStartedSession(t *testing.T, sessRepo *store.SessionRepo, userID, agentID string) string {
	t.Helper()
	sched := "scheduler"
	id := idgen.New()
	s := &store.Session{ID: id, UserID: userID, Source: &sched}
	if agentID != "" {
		s.AgentID = &agentID
	}
	if err := sessRepo.Create(context.Background(), s); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return id
}

func listSessions(t *testing.T, h *sessionHandler, userID, rawQuery string) PaginatedResponse[SessionResponse] {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", h.handleList)
	req := httptest.NewRequest("GET", "/api/v1/sessions?"+rawQuery, nil)
	req = asUser(req, userID)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sessions (%s): got %d, body=%s", rawQuery, rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[SessionResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestListSessions_CountCached_NextPageExact(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)
	sessH := &sessionHandler{
		core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo,
		countCache: runtime.NewMapCache(), logger: adapter.logger,
	}

	ids := []string{
		seedStartedSession(t, sessRepo, "alice", ""),
		seedStartedSession(t, sessRepo, "alice", ""),
		seedStartedSession(t, sessRepo, "alice", ""),
	}

	resp := listSessions(t, sessH, "alice", "pageNumber=1&pageSize=2")
	if resp.Meta.Pagination.Count != 3 {
		t.Fatalf("initial count = %d, want 3", resp.Meta.Pagination.Count)
	}
	if resp.Meta.Pagination.NextPage == nil || *resp.Meta.Pagination.NextPage != 2 {
		t.Fatalf("initial nextPage = %v, want 2", resp.Meta.Pagination.NextPage)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("initial page len = %d, want 2", len(resp.Data))
	}

	if _, err := sessRepo.SoftDelete(context.Background(), ids[0], "alice"); err != nil {
		t.Fatal(err)
	}

	resp = listSessions(t, sessH, "alice", "pageNumber=1&pageSize=2")
	if resp.Meta.Pagination.Count != 3 {
		t.Errorf("post-delete count = %d, want 3 (cached/stale proves single COUNT)", resp.Meta.Pagination.Count)
	}
	if resp.Meta.Pagination.NextPage != nil {
		t.Errorf("post-delete nextPage = %v, want nil (exact from fresh page)", *resp.Meta.Pagination.NextPage)
	}
	if len(resp.Data) != 2 {
		t.Errorf("post-delete page len = %d, want 2 (fresh)", len(resp.Data))
	}
}

func TestListSessions_CountCache_UserAndFilterIsolation(t *testing.T) {
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)
	sessH := &sessionHandler{
		core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo,
		countCache: runtime.NewMapCache(), logger: adapter.logger,
	}

	seedStartedSession(t, sessRepo, "alice", "agent-a")
	seedStartedSession(t, sessRepo, "alice", "agent-a")
	seedStartedSession(t, sessRepo, "alice", "agent-b")
	for range 5 {
		seedStartedSession(t, sessRepo, "bob", "")
	}

	if got := listSessions(t, sessH, "alice", "pageNumber=1&pageSize=50").Meta.Pagination.Count; got != 3 {
		t.Fatalf("alice count = %d, want 3", got)
	}

	if got := listSessions(t, sessH, "bob", "pageNumber=1&pageSize=50").Meta.Pagination.Count; got != 5 {
		t.Errorf("bob count = %d, want 5 (must not share alice's cache entry)", got)
	}

	if got := listSessions(t, sessH, "alice", "pageNumber=1&pageSize=50&includeAgentIds=agent-a").Meta.Pagination.Count; got != 2 {
		t.Errorf("alice includeAgentIds=agent-a count = %d, want 2 (filter must not reuse default entry)", got)
	}
}

func TestSessionCountCacheKey_FilterSensitivity(t *testing.T) {
	base := sessionCountCacheKey("u1", "", "", []string{"a", "b"}, nil)

	if base == sessionCountCacheKey("u2", "", "", []string{"a", "b"}, nil) {
		t.Error("key must differ by userID")
	}
	if base == sessionCountCacheKey("u1", "proj-1", "", []string{"a", "b"}, nil) {
		t.Error("key must differ by projectID")
	}
	if base == sessionCountCacheKey("u1", "", "", []string{"a"}, nil) {
		t.Error("key must differ by excludeAgentIds")
	}
	if base == sessionCountCacheKey("u1", "", "", []string{"a", "b"}, []string{"a"}) {
		t.Error("key must differ by includeAgentIds")
	}
	if base != sessionCountCacheKey("u1", "", "", []string{"b", "a"}, nil) {
		t.Error("key must be order-insensitive for excludeAgentIds")
	}
	if base == sessionCountCacheKey("u1", "", store.SourceFilterScheduler, []string{"a", "b"}, nil) {
		t.Error("key must differ by source")
	}
	if sessionCountCacheKey("u1", "", store.SourceFilterChat, nil, nil) == sessionCountCacheKey("u1", "", store.SourceFilterScheduler, nil, nil) {
		t.Error("the two tab sources must not share a cache key")
	}
}
