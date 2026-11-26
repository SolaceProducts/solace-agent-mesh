// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func setupArtifactAllHandler(t *testing.T) (*artifactHandler, *store.SessionRepo) {
	t.Helper()
	h, _, sessRepo := setupArtifactHandler(t)
	h.allCache = newArtifactCache(30*time.Second, 200)
	return h, sessRepo
}

func seedArtifacts(t *testing.T, h *artifactHandler, userID, sessionID string, filenames ...string) {
	t.Helper()
	ctx := context.Background()
	for _, name := range filenames {
		loc := h.artSvc.Resolve("", userID, sessionID, name)
		if _, err := h.artSvc.Inner.Save(ctx, loc, []byte("content-"+name), "text/plain"); err != nil {
			t.Fatalf("save artifact %s: %v", name, err)
		}
	}
}

func decodeAllResponse(t *testing.T, rec *httptest.ResponseRecorder) PaginatedResponse[ArtifactAllItemResponse] {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[ArtifactAllItemResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestListAll_Empty(t *testing.T) {
	h, _ := setupArtifactAllHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	assertDataIsEmptyArray(t, rec.Body.Bytes())

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 0 {
		t.Errorf("expected totalCount=0, got %d", resp.Meta.Pagination.Count)
	}
	if resp.Meta.Pagination.NextPage != nil {
		t.Error("expected hasMore=false")
	}
	if resp.Meta.Pagination.NextPage != nil {
		t.Error("expected nextPage=nil")
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected empty artifacts, got %d", len(resp.Data))
	}
}

func TestListAll_BasicListing(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})

	seedArtifacts(t, h, "sam_dev_user", sess1, "alpha.txt", "beta.txt", "gamma.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 3 {
		t.Errorf("expected totalCount=3, got %d", resp.Meta.Pagination.Count)
	}
	if len(resp.Data) != 3 {
		t.Errorf("expected 3 artifacts, got %d", len(resp.Data))
	}
	for i := 1; i < len(resp.Data); i++ {
		if resp.Data[i].Filename < resp.Data[i-1].Filename {
			t.Errorf("artifacts not sorted: %s < %s", resp.Data[i].Filename, resp.Data[i-1].Filename)
		}
	}
	for _, a := range resp.Data {
		if a.SessionID != sess1 {
			t.Errorf("expected sessionId=sess1, got %s", a.SessionID)
		}
	}
}

func TestListAll_StampsAgentID(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	builderSess := idgen.New()
	chatSess := idgen.New()
	builderAgent := "Builder"
	_ = sessRepo.Create(ctx, &store.Session{ID: builderSess, UserID: "sam_dev_user", AgentID: &builderAgent})
	_ = sessRepo.Create(ctx, &store.Session{ID: chatSess, UserID: "sam_dev_user"})

	seedArtifacts(t, h, "sam_dev_user", builderSess, "plan.yaml")
	seedArtifacts(t, h, "sam_dev_user", chatSess, "notes.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	agentByFile := map[string]string{}
	for _, a := range resp.Data {
		agentByFile[a.Filename] = a.AgentID
	}
	if agentByFile["plan.yaml"] != "Builder" {
		t.Errorf("builder-session artifact should carry agentId=Builder, got %q", agentByFile["plan.yaml"])
	}
	if agentByFile["notes.txt"] != "" {
		t.Errorf("agentless-session artifact should have empty agentId, got %q", agentByFile["notes.txt"])
	}
}

func TestListAll_ProjectWithoutSession(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)
	h.projectRepo = store.NewProjectRepo(h.core.Store())

	ctx := context.Background()
	userID := "sam_dev_user"

	proj := &store.Project{Name: "Standalone Project", UserID: userID}
	if err := h.projectRepo.Create(ctx, proj); err != nil {
		t.Fatalf("create project: %v", err)
	}
	seedArtifacts(t, h, userID, projectSessionPrefix+proj.ID, "report.pdf")

	sess := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess, UserID: userID})
	seedArtifacts(t, h, userID, sess, "notes.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/all", nil))
	resp := decodeAllResponse(t, rec)

	var report *ArtifactAllItemResponse
	for i := range resp.Data {
		if resp.Data[i].Filename == "report.pdf" {
			report = &resp.Data[i]
		}
	}
	if report == nil {
		t.Fatal("report.pdf (project artifact with no linked session) missing from /artifacts/all")
	}
	if report.ProjectID != proj.ID {
		t.Errorf("projectId = %q, want %q", report.ProjectID, proj.ID)
	}
	if report.ProjectName != "Standalone Project" {
		t.Errorf("projectName = %q, want %q", report.ProjectName, "Standalone Project")
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/all?projectId="+proj.ID, nil))
	resp = decodeAllResponse(t, rec)
	if len(resp.Data) != 1 || resp.Data[0].Filename != "report.pdf" {
		t.Fatalf("project filter should return only report.pdf, got %+v", resp.Data)
	}
}

func TestListAll_SharedProjectScannedUnderOwnerPrefix(t *testing.T) {
	h, _ := setupArtifactAllHandler(t)
	h.projectRepo = store.NewProjectRepo(h.core.Store())

	ctx := context.Background()
	requester := "sam_dev_user"
	owner := "other_owner"

	shared := &store.Project{Name: "Shared Project", UserID: owner}
	if err := h.projectRepo.Create(ctx, shared); err != nil {
		t.Fatalf("create project: %v", err)
	}
	seedArtifacts(t, h, owner, projectSessionPrefix+shared.ID, "spec.md")

	h.core.SetProjectSharingService(&mockProjectSharingService{sharedIDs: []string{shared.ID}, hasAccess: true})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/all", nil))
	resp := decodeAllResponse(t, rec)

	var spec *ArtifactAllItemResponse
	for i := range resp.Data {
		if resp.Data[i].Filename == "spec.md" {
			spec = &resp.Data[i]
		}
	}
	if spec == nil {
		t.Fatal("shared project artifact spec.md missing from /artifacts/all")
	}
	if spec.ProjectID != shared.ID || spec.ProjectName != "Shared Project" {
		t.Errorf("shared artifact projectId/name = %q/%q, want %q/Shared Project", spec.ProjectID, spec.ProjectName, shared.ID)
	}
	_ = requester
}

func TestListAll_StaleProjectReferenceNoLeak(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)
	h.projectRepo = store.NewProjectRepo(h.core.Store())
	h.core.SetProjectSharingService(&mockProjectSharingService{sharedIDs: nil, hasAccess: false})

	ctx := context.Background()
	requester := "sam_dev_user"
	owner := "other_owner"

	foreign := &store.Project{Name: "Foreign Project", UserID: owner}
	if err := h.projectRepo.Create(ctx, foreign); err != nil {
		t.Fatalf("create project: %v", err)
	}
	seedArtifacts(t, h, owner, projectSessionPrefix+foreign.ID, "secret.txt")

	staleSess := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: staleSess, UserID: requester, ProjectID: &foreign.ID})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/all", nil))
	resp := decodeAllResponse(t, rec)

	for _, a := range resp.Data {
		if a.Filename == "secret.txt" {
			t.Fatalf("SECURITY: requester must not see foreign owner's project artifact; leaked %+v", a)
		}
	}
}

func TestListAll_ProjectEnumerationErrorNotCached(t *testing.T) {
	h, _ := setupArtifactAllHandler(t)
	h.projectRepo = store.NewProjectRepo(h.core.Store())
	h.core.SetProjectSharingService(&mockProjectSharingService{sharedIDsErr: errors.New("sharing backend down")})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/all", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on degraded (uncacheable) enumeration, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if _, cached := h.allCache.get("sam_dev_user"); cached {
		t.Error("degraded collect must not be cached")
	}
}

func TestListAll_Pagination(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})

	for i := range 5 {
		seedArtifacts(t, h, "sam_dev_user", sess1, fmt.Sprintf("file%02d.txt", i))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all?pageNumber=1&pageSize=2", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 5 {
		t.Errorf("expected totalCount=5, got %d", resp.Meta.Pagination.Count)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 artifacts on page 1, got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.NextPage == nil {
		t.Error("expected hasMore=true")
	}
	if resp.Meta.Pagination.NextPage == nil || *resp.Meta.Pagination.NextPage != 2 {
		t.Errorf("expected nextPage=2, got %v", resp.Meta.Pagination.NextPage)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all?pageNumber=3&pageSize=2", nil)
	mux.ServeHTTP(rec, req)

	resp = decodeAllResponse(t, rec)
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 artifact on page 3, got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.NextPage != nil {
		t.Error("expected hasMore=false on last page")
	}
	if resp.Meta.Pagination.NextPage != nil {
		t.Error("expected nextPage=nil on last page")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all?pageNumber=10&pageSize=2", nil)
	mux.ServeHTTP(rec, req)

	resp = decodeAllResponse(t, rec)
	if len(resp.Data) != 0 {
		t.Errorf("expected 0 artifacts beyond range, got %d", len(resp.Data))
	}
}

func TestListAll_Search(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})

	seedArtifacts(t, h, "sam_dev_user", sess1, "report.csv", "Report_Final.pdf", "image.png", "quarterly_report.xlsx")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all?search=report", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 3 {
		t.Errorf("expected 3 matches for 'report', got %d", resp.Meta.Pagination.Count)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all?search=nonexistent", nil)
	mux.ServeHTTP(rec, req)

	resp = decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 0 {
		t.Errorf("expected 0 matches, got %d", resp.Meta.Pagination.Count)
	}
}

func TestListAll_SearchWithPagination(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})

	for i := range 5 {
		seedArtifacts(t, h, "sam_dev_user", sess1, fmt.Sprintf("doc_%02d.txt", i))
	}
	seedArtifacts(t, h, "sam_dev_user", sess1, "image.png")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all?search=doc&pageSize=2&pageNumber=1", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 5 {
		t.Errorf("expected totalCount=5, got %d", resp.Meta.Pagination.Count)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 artifacts on page, got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.NextPage == nil {
		t.Error("expected hasMore=true")
	}
}

func TestListAll_Deduplication_ProjectTakesPrecedence(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess2 := idgen.New()
	projID := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: idgen.New(), UserID: "sam_dev_user", ProjectID: &projID})
	_ = sessRepo.Create(ctx, &store.Session{ID: sess2, UserID: "sam_dev_user"})

	seedArtifacts(t, h, "sam_dev_user", "project-"+projID, "shared.txt")
	seedArtifacts(t, h, "sam_dev_user", sess2, "shared.txt")
	seedArtifacts(t, h, "sam_dev_user", sess2, "standalone.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 2 {
		t.Errorf("expected 2 (deduplicated), got %d", resp.Meta.Pagination.Count)
	}

	for _, a := range resp.Data {
		if a.Filename == "shared.txt" {
			if a.ProjectID != projID {
				t.Errorf("expected projectId=%s for deduplicated entry, got %q", projID, a.ProjectID)
			}
			return
		}
	}
	t.Error("shared.txt not found in response")
}

func TestListAll_MultipleSessionsSameUser(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	sess2 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})
	_ = sessRepo.Create(ctx, &store.Session{ID: sess2, UserID: "sam_dev_user"})

	seedArtifacts(t, h, "sam_dev_user", sess1, "file_a.txt")
	seedArtifacts(t, h, "sam_dev_user", sess2, "file_b.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 2 {
		t.Errorf("expected 2 artifacts across sessions, got %d", resp.Meta.Pagination.Count)
	}
}

func TestCollectAllArtifacts_ManySessionsParallel(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)
	ctx := context.Background()
	const userID = "sam_dev_user"

	const nSessions = 25
	sessionByIndex := make([]string, nSessions)
	for i := range nSessions {
		sess := idgen.New()
		sessionByIndex[i] = sess
		if err := sessRepo.Create(ctx, &store.Session{ID: sess, UserID: userID}); err != nil {
			t.Fatalf("create session: %v", err)
		}
		name := fmt.Sprintf("unique-%02d.txt", i)
		if _, err := artifact.SaveWithMetadata(ctx, h.artSvc,
			"", userID, sess, name,
			[]byte("content-"+name), fmt.Sprintf("text/x-%02d", i),
			artifact.MetadataOpts{
				SuppressSchema: true,
				Source:         fmt.Sprintf("src-%02d", i),
				Tags:           []string{fmt.Sprintf("tag-%02d", i)},
			}, slog.Default()); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
		seedArtifacts(t, h, userID, sess, "shared.txt")
	}

	projID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: idgen.New(), UserID: userID, ProjectID: &projID}); err != nil {
		t.Fatalf("create project session: %v", err)
	}
	seedArtifacts(t, h, userID, "project-"+projID, "shared.txt")

	items, failed, err := h.collectAllArtifacts(ctx, userID)
	if err != nil {
		t.Fatalf("collectAllArtifacts: %v", err)
	}
	if failed != 0 {
		t.Fatalf("failed scope count = %d, want 0", failed)
	}

	if len(items) != nSessions+1 {
		t.Fatalf("got %d artifacts, want %d", len(items), nSessions+1)
	}

	byName := make(map[string]ArtifactAllItemResponse, len(items))
	for _, it := range items {
		if _, dup := byName[it.Filename]; dup {
			t.Fatalf("duplicate filename in result: %s", it.Filename)
		}
		byName[it.Filename] = it
	}

	for i := range nSessions {
		name := fmt.Sprintf("unique-%02d.txt", i)
		it, ok := byName[name]
		if !ok {
			t.Errorf("missing %s", name)
			continue
		}
		if it.SessionID != sessionByIndex[i] {
			t.Errorf("%s: sessionId = %q, want %q (cross-wired across sessions)", name, it.SessionID, sessionByIndex[i])
		}
		if wantTag := fmt.Sprintf("tag-%02d", i); !slices.Contains(it.Tags, wantTag) {
			t.Errorf("%s: tags %v missing %q (companion metadata cross-wired)", name, it.Tags, wantTag)
		}
		if wantSrc := fmt.Sprintf("src-%02d", i); it.Source == nil || *it.Source != wantSrc {
			t.Errorf("%s: source = %v, want %q (companion metadata cross-wired)", name, it.Source, wantSrc)
		}
		if wantMIME := fmt.Sprintf("text/x-%02d", i); it.MIMEType == nil || *it.MIMEType != wantMIME {
			t.Errorf("%s: mime = %v, want %q (version metadata cross-wired)", name, it.MIMEType, wantMIME)
		}
	}

	shared, ok := byName["shared.txt"]
	if !ok {
		t.Fatal("shared.txt missing from result")
	}
	if shared.ProjectID != projID {
		t.Errorf("shared.txt projectId = %q, want %q (project precedence lost)", shared.ProjectID, projID)
	}
}

type failingScopeScanService struct {
	artifact.Service
	vl          scopeArtifactVersionLister
	failSession map[string]bool
}

func (f *failingScopeScanService) ListArtifactVersions(ctx context.Context, scope artifact.KeyScope) (map[string][]int, error) {
	if f.failSession[scope.SessionID] {
		return nil, errors.New("simulated scope scan error")
	}
	return f.vl.ListArtifactVersions(ctx, scope)
}

func TestCollectAllArtifacts_ParallelScanFailureCount(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)
	ctx := context.Background()
	const userID = "sam_dev_user"

	vl, ok := h.artSvc.Inner.(scopeArtifactVersionLister)
	if !ok {
		t.Fatal("backend does not implement scopeArtifactVersionLister; test would not exercise the parallel scan path")
	}

	const nSessions = 20
	const nFail = 5
	failSession := make(map[string]bool)
	for i := range nSessions {
		sess := idgen.New()
		if err := sessRepo.Create(ctx, &store.Session{ID: sess, UserID: userID}); err != nil {
			t.Fatalf("create session: %v", err)
		}
		seedArtifacts(t, h, userID, sess, fmt.Sprintf("file-%02d.txt", i))
		if i < nFail {
			failSession[sess] = true
		}
	}

	h.artSvc.Inner = &failingScopeScanService{Service: h.artSvc.Inner, vl: vl, failSession: failSession}

	items, failed, err := h.collectAllArtifacts(ctx, userID)
	if err != nil {
		t.Fatalf("collectAllArtifacts: %v", err)
	}
	if failed != nFail {
		t.Errorf("failed scope count = %d, want %d", failed, nFail)
	}
	if len(items) != nSessions-nFail {
		t.Errorf("collected %d artifacts, want %d (only non-failing scopes contribute)", len(items), nSessions-nFail)
	}
}

func TestListAll_PageSizeClamping(t *testing.T) {
	h, _ := setupArtifactAllHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	for _, qs := range []string{
		"?pageSize=0",
		"?pageSize=-1",
		"?pageSize=200",
		"?pageNumber=0",
		"?pageNumber=-5",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/artifacts/all"+qs, nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("query %s: expected 200, got %d", qs, rec.Code)
		}
	}
}

func TestArtifactCache_GetSet(t *testing.T) {
	c := newArtifactCache(1*time.Second, 10)

	items := []ArtifactAllItemResponse{
		{ArtifactInfoResponse: ArtifactInfoResponse{Filename: "a.txt"}, SessionID: idgen.New()},
	}
	c.set("user1", items)

	got, ok := c.get("user1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 1 || got[0].Filename != "a.txt" {
		t.Errorf("unexpected cached items: %+v", got)
	}
}

func TestArtifactCache_Expiry(t *testing.T) {
	c := newArtifactCache(1*time.Millisecond, 10)

	c.set("user1", []ArtifactAllItemResponse{
		{ArtifactInfoResponse: ArtifactInfoResponse{Filename: "a.txt"}},
	})

	time.Sleep(5 * time.Millisecond)

	_, ok := c.get("user1")
	if ok {
		t.Error("expected cache miss after TTL expiry")
	}
}

func TestArtifactCache_Invalidate(t *testing.T) {
	c := newArtifactCache(1*time.Minute, 10)

	c.set("user1", []ArtifactAllItemResponse{
		{ArtifactInfoResponse: ArtifactInfoResponse{Filename: "a.txt"}},
	})
	c.invalidate("user1")

	_, ok := c.get("user1")
	if ok {
		t.Error("expected cache miss after invalidation")
	}
}

func TestArtifactCache_LRUEviction(t *testing.T) {
	c := newArtifactCache(1*time.Minute, 2)

	c.set("user1", []ArtifactAllItemResponse{})
	time.Sleep(1 * time.Millisecond)
	c.set("user2", []ArtifactAllItemResponse{})

	c.set("user3", []ArtifactAllItemResponse{})

	if _, ok := c.get("user1"); ok {
		t.Error("expected user1 to be evicted")
	}
	if _, ok := c.get("user2"); !ok {
		t.Error("expected user2 to still be cached")
	}
	if _, ok := c.get("user3"); !ok {
		t.Error("expected user3 to still be cached")
	}
}

func TestListAll_CacheInvalidationOnUpload(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})

	seedArtifacts(t, h, "sam_dev_user", sess1, "initial.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)
	mux.HandleFunc("POST /api/v1/artifacts/upload", h.handleUpload)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Fatalf("expected 1 artifact initially, got %d", resp.Meta.Pagination.Count)
	}

	uploadReq := multipartUpload(t, "new_file.txt", "new content", sess1)
	uploadRec := httptest.NewRecorder()
	mux.ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d %s", uploadRec.Code, uploadRec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp = decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 2 {
		t.Errorf("expected 2 artifacts after upload, got %d", resp.Meta.Pagination.Count)
	}
}

func TestListAll_CacheInvalidationOnDelete(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})

	seedArtifacts(t, h, "sam_dev_user", sess1, "keep.txt", "remove.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)
	mux.HandleFunc("DELETE /api/v1/artifacts/{id}/{filename}", h.handleDelete)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/all", nil))
	if resp := decodeAllResponse(t, rec); resp.Meta.Pagination.Count != 2 {
		t.Fatalf("expected 2 artifacts initially, got %d", resp.Meta.Pagination.Count)
	}

	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, httptest.NewRequest("DELETE", "/api/v1/artifacts/"+sess1+"/remove.txt", nil))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete failed: %d %s", delRec.Code, delRec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/all", nil))
	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Errorf("expected 1 artifact after delete, got %d", resp.Meta.Pagination.Count)
	}
	for _, a := range resp.Data {
		if a.Filename == "remove.txt" {
			t.Error("deleted artifact remove.txt still present after delete (stale cache)")
		}
	}
}

type prefilterCountingService struct {
	artifact.Service
	mu             sync.Mutex
	listKeyCalls   map[string]int
	filterSessions map[string]struct{}
	hasFilter      bool
	prefilterErr   error
}

func (p *prefilterCountingService) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	p.mu.Lock()
	p.listKeyCalls[scope.SessionID]++
	p.mu.Unlock()
	return p.Service.ListKeys(ctx, scope)
}

func (p *prefilterCountingService) ListSessionsWithArtifactsForUser(ctx context.Context, appName, userID string) (map[string]struct{}, bool, error) {
	if p.prefilterErr != nil {
		return nil, false, p.prefilterErr
	}
	return p.filterSessions, p.hasFilter, nil
}

func (p *prefilterCountingService) callCount(sessionID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listKeyCalls[sessionID]
}

func wrapWithPrefilter(h *artifactHandler, filterSessions map[string]struct{}, hasFilter bool, err error) *prefilterCountingService {
	wrapped := &prefilterCountingService{
		Service:        h.artSvc.Inner,
		listKeyCalls:   map[string]int{},
		filterSessions: filterSessions,
		hasFilter:      hasFilter,
		prefilterErr:   err,
	}
	h.artSvc.Inner = wrapped
	return wrapped
}

func TestListAll_PrefilterSkipsEmptySessions(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sessEmpty1 := idgen.New()
	sessHasArt := idgen.New()
	sessEmpty2 := idgen.New()
	for _, sid := range []string{sessEmpty1, sessHasArt, sessEmpty2} {
		_ = sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"})
	}
	seedArtifacts(t, h, "sam_dev_user", sessHasArt, "result.txt")

	counting := wrapWithPrefilter(h, map[string]struct{}{sessHasArt: {}}, true, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Fatalf("expected 1 artifact, got %d", resp.Meta.Pagination.Count)
	}
	if len(resp.Data) != 1 || resp.Data[0].SessionID != sessHasArt {
		t.Errorf("expected single artifact in session %q, got %+v", sessHasArt, resp.Data)
	}

	if got := counting.callCount(sessHasArt); got != 1 {
		t.Errorf("ListKeys call count for sessHasArt = %d, want 1", got)
	}
	for _, empty := range []string{sessEmpty1, sessEmpty2} {
		if got := counting.callCount(empty); got != 0 {
			t.Errorf("ListKeys called %d times for empty session %q; expected 0 (prefilter should skip)", got, empty)
		}
	}
}

func TestListAll_PrefilterNoFilterScansEverySession(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	sess2 := idgen.New()
	for _, sid := range []string{sess1, sess2} {
		_ = sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"})
	}
	seedArtifacts(t, h, "sam_dev_user", sess1, "a.txt")
	seedArtifacts(t, h, "sam_dev_user", sess2, "b.txt")

	counting := wrapWithPrefilter(h, nil, false, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 2 {
		t.Fatalf("expected 2 artifacts, got %d", resp.Meta.Pagination.Count)
	}
	for _, sid := range []string{sess1, sess2} {
		if got := counting.callCount(sid); got != 1 {
			t.Errorf("ListKeys for %q = %d, want 1", sid, got)
		}
	}
}

func TestListAll_PrefilterErrorFallsBack(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	sess2 := idgen.New()
	for _, sid := range []string{sess1, sess2} {
		_ = sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"})
	}
	seedArtifacts(t, h, "sam_dev_user", sess1, "a.txt")

	counting := wrapWithPrefilter(h, nil, false, errors.New("transient S3 error"))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Fatalf("expected 1 artifact after error fallback, got %d", resp.Meta.Pagination.Count)
	}
	for _, sid := range []string{sess1, sess2} {
		if got := counting.callCount(sid); got != 1 {
			t.Errorf("ListKeys for %q = %d, want 1 (fall-through after prefilter error)", sid, got)
		}
	}
}

func TestListAll_PrefilterKeepsProjectStorageSessions(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	projID := idgen.New()
	sessEmpty := idgen.New()
	sessInProj := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sessEmpty, UserID: "sam_dev_user"})
	_ = sessRepo.Create(ctx, &store.Session{ID: sessInProj, UserID: "sam_dev_user", ProjectID: &projID})

	seedArtifacts(t, h, "sam_dev_user", "project-"+projID, "shared.txt")

	counting := wrapWithPrefilter(h, map[string]struct{}{}, true, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Fatalf("expected 1 artifact via project scan, got %d", resp.Meta.Pagination.Count)
	}
	if got := counting.callCount("project-" + projID); got != 1 {
		t.Errorf("project storage session must be scanned even when absent from prefilter; got %d calls", got)
	}
	if got := counting.callCount(sessEmpty); got != 0 {
		t.Errorf("empty non-project session was scanned %d times; expected 0", got)
	}
}

type failingListKeysService struct {
	artifact.Service
	mu           sync.Mutex
	failAll      bool
	failSession  map[string]bool
	listKeyCalls map[string]int
}

func (f *failingListKeysService) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	f.mu.Lock()
	f.listKeyCalls[scope.SessionID]++
	fail := f.failAll || f.failSession[scope.SessionID]
	f.mu.Unlock()
	if fail {
		return nil, errors.New("simulated S3 ListObjectsV2 error")
	}
	return f.Service.ListKeys(ctx, scope)
}

func (f *failingListKeysService) heal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failAll = false
	f.failSession = map[string]bool{}
}

func (f *failingListKeysService) callCount(sessionID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listKeyCalls[sessionID]
}

func TestListAll_StoreOutageNotCached(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})
	seedArtifacts(t, h, "sam_dev_user", sess1, "report.txt")

	failing := &failingListKeysService{
		Service:      h.artSvc.Inner,
		failAll:      true,
		failSession:  map[string]bool{},
		listKeyCalls: map[string]int{},
	}
	h.artSvc.Inner = failing

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 during store outage, got %d; body=%s", rec.Code, rec.Body.String())
	}
	var errResp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode 503 body: %v", err)
	}
	if errResp.Message == "" || errResp.ErrorID == "" {
		t.Errorf("503 must use the standard error envelope; got message=%q errorId=%q", errResp.Message, errResp.ErrorID)
	}

	failing.heal()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Fatalf("expected 1 artifact immediately after recovery (outage must not be cached), got %d", resp.Meta.Pagination.Count)
	}
}

func TestListAll_PartialFailureServedNotCached(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sessGood := idgen.New()
	sessBad := idgen.New()
	for _, sid := range []string{sessGood, sessBad} {
		_ = sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"})
	}
	seedArtifacts(t, h, "sam_dev_user", sessGood, "good.txt")
	seedArtifacts(t, h, "sam_dev_user", sessBad, "bad.txt")

	failing := &failingListKeysService{
		Service:      h.artSvc.Inner,
		failSession:  map[string]bool{sessBad: true},
		listKeyCalls: map[string]int{},
	}
	h.artSvc.Inner = failing

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)
	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Fatalf("expected 1 (partial) artifact, got %d", resp.Meta.Pagination.Count)
	}

	failing.heal()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)
	resp = decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 2 {
		t.Fatalf("expected 2 artifacts after heal (partial must not be cached), got %d", resp.Meta.Pagination.Count)
	}
}

func TestListAll_CleanResultIsCached(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})
	seedArtifacts(t, h, "sam_dev_user", sess1, "a.txt")

	failing := &failingListKeysService{
		Service:      h.artSvc.Inner,
		failSession:  map[string]bool{},
		listKeyCalls: map[string]int{},
	}
	h.artSvc.Inner = failing

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	for range 2 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
		mux.ServeHTTP(rec, req)
		_ = decodeAllResponse(t, rec)
	}
	if got := failing.callCount(sess1); got != 1 {
		t.Errorf("clean result should be cached: expected ListKeys called once (2nd from cache), got %d", got)
	}
}

func TestListAll_EmptyAccountIsCached(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})

	failing := &failingListKeysService{
		Service:      h.artSvc.Inner,
		failSession:  map[string]bool{},
		listKeyCalls: map[string]int{},
	}
	h.artSvc.Inner = failing

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)
	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 0 {
		t.Fatalf("expected empty list (count 0), got %d", resp.Meta.Pagination.Count)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)
	_ = decodeAllResponse(t, rec)
	if got := failing.callCount(sess1); got != 1 {
		t.Errorf("empty account should be cached: expected ListKeys called once (2nd from cache), got %d", got)
	}
}

func TestListAll_FiltersInternalArtifacts(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})

	seedArtifacts(t, h, "sam_dev_user", sess1, "report.txt")
	seedArtifacts(t, h, "sam_dev_user", sess1, "report.converted.txt")
	seedArtifacts(t, h, "sam_dev_user", sess1, "project_bm25_index.zip")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Errorf("expected 1 non-internal artifact, got %d", resp.Meta.Pagination.Count)
	}
	if len(resp.Data) > 0 && resp.Data[0].Filename != "report.txt" {
		t.Errorf("expected report.txt, got %s", resp.Data[0].Filename)
	}
}

func TestListAll_ProjectFilter(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)
	ctx := context.Background()
	const userID = "sam_dev_user"

	projID := idgen.New()
	sessInProj := idgen.New()
	sessStandalone := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sessInProj, UserID: userID, ProjectID: &projID})
	_ = sessRepo.Create(ctx, &store.Session{ID: sessStandalone, UserID: userID})

	seedArtifacts(t, h, userID, "project-"+projID, "in_project.txt")
	seedArtifacts(t, h, userID, sessStandalone, "no_project.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all?projectId="+projID, nil)
	mux.ServeHTTP(rec, req)
	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 || len(resp.Data) != 1 || resp.Data[0].Filename != "in_project.txt" {
		t.Fatalf("projectId filter: expected only in_project.txt, got %+v", resp.Data)
	}
	if resp.Data[0].ProjectID != projID {
		t.Errorf("expected projectId=%s, got %q", projID, resp.Data[0].ProjectID)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all?projectId=__no_project__", nil)
	mux.ServeHTTP(rec, req)
	resp = decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 || len(resp.Data) != 1 || resp.Data[0].Filename != "no_project.txt" {
		t.Fatalf("no-project filter: expected only no_project.txt, got %+v", resp.Data)
	}
	if resp.Data[0].ProjectID != "" {
		t.Errorf("expected empty projectId for unassigned artifact, got %q", resp.Data[0].ProjectID)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)
	resp = decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 2 {
		t.Errorf("no filter: expected 2 artifacts, got %d", resp.Meta.Pagination.Count)
	}
}

func TestListAll_ProjectFilterWithSearch(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)
	ctx := context.Background()
	const userID = "sam_dev_user"

	projID := idgen.New()
	sessInProj := idgen.New()
	sessStandalone := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sessInProj, UserID: userID, ProjectID: &projID})
	_ = sessRepo.Create(ctx, &store.Session{ID: sessStandalone, UserID: userID})

	seedArtifacts(t, h, userID, "project-"+projID, "quarterly_report.csv", "notes.txt")
	seedArtifacts(t, h, userID, sessStandalone, "annual_report.csv")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all?projectId="+projID+"&search=report", nil)
	mux.ServeHTTP(rec, req)
	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 || len(resp.Data) != 1 || resp.Data[0].Filename != "quarterly_report.csv" {
		t.Fatalf("project+search: expected only quarterly_report.csv, got %+v", resp.Data)
	}
}

func TestListAll_ProjectFilter_InvalidUUID(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)
	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})
	seedArtifacts(t, h, "sam_dev_user", sess1, "a.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all?projectId=not-a-uuid", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed projectId, got %d; body=%s", rec.Code, rec.Body.String())
	}
	var errResp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode 400 body: %v", err)
	}
	if errResp.Message == "" || errResp.ErrorID == "" {
		t.Errorf("400 must use the standard error envelope; got message=%q errorId=%q", errResp.Message, errResp.ErrorID)
	}
}

type messageCaptureHandler struct {
	mu       sync.Mutex
	messages []string
}

func (h *messageCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *messageCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, r.Message)
	return nil
}
func (h *messageCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *messageCaptureHandler) WithGroup(string) slog.Handler      { return h }

func (h *messageCaptureHandler) contains(substr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.messages {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

type cancelDuringListService struct {
	artifact.Service
	mu       sync.Mutex
	cancel   context.CancelFunc
	disabled bool
}

func (c *cancelDuringListService) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	c.mu.Lock()
	disabled := c.disabled
	c.mu.Unlock()
	if disabled {
		return c.Service.ListKeys(ctx, scope)
	}
	c.cancel()
	return nil, context.Canceled
}

func (c *cancelDuringListService) disable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disabled = true
}

func TestListAll_ClientCancelIsNotAStoreError(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})
	seedArtifacts(t, h, "sam_dev_user", sess1, "report.txt")

	logs := &messageCaptureHandler{}
	h.logger = slog.New(logs)

	reqCtx, cancel := context.WithCancel(context.Background())
	canceller := &cancelDuringListService{Service: h.artSvc.Inner, cancel: cancel}
	h.artSvc.Inner = canceller

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil).WithContext(reqCtx)
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusInternalServerError || rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("client cancel must not produce a 5xx; got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if logs.contains("list artifact versions error") {
		t.Error("client cancel must not log a per-session 'list artifact versions error' warn")
	}
	if logs.contains("artifact list served degraded") || logs.contains("artifact store unavailable") {
		t.Error("client cancel must not be reported as a store degradation/outage")
	}
	if logs.contains("collect all artifacts error") {
		t.Error("client cancel must not be logged as an error")
	}

	canceller.disable()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)
	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Fatalf("expected 1 artifact after client returns (cancel must not be cached), got %d", resp.Meta.Pagination.Count)
	}
}

type instrumentationLikeWrapper struct {
	artifact.Service
}

func (w instrumentationLikeWrapper) Inner() artifact.Service { return w.Service }

type listCountingStore struct {
	artifact.Service
	mu                        sync.Mutex
	listVersionsCalls         int
	listArtifactVersionsCalls int
}

func (c *listCountingStore) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	c.mu.Lock()
	c.listVersionsCalls++
	c.mu.Unlock()
	return c.Service.ListVersions(ctx, loc)
}

func (c *listCountingStore) ListArtifactVersions(ctx context.Context, scope artifact.KeyScope) (map[string][]int, error) {
	c.mu.Lock()
	c.listArtifactVersionsCalls++
	c.mu.Unlock()
	return c.Service.(scopeArtifactVersionLister).ListArtifactVersions(ctx, scope)
}

func (c *listCountingStore) counts() (listVersions, listArtifactVersions int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listVersionsCalls, c.listArtifactVersionsCalls
}

func TestListAll_UnwrapsInstrumentationForPrefilter(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sessEmpty := idgen.New()
	sessHasArt := idgen.New()
	for _, sid := range []string{sessEmpty, sessHasArt} {
		_ = sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user"})
	}
	seedArtifacts(t, h, "sam_dev_user", sessHasArt, "result.txt")

	counting := &prefilterCountingService{
		Service:        h.artSvc.Inner,
		listKeyCalls:   map[string]int{},
		filterSessions: map[string]struct{}{sessHasArt: {}},
		hasFilter:      true,
	}
	h.artSvc.Inner = instrumentationLikeWrapper{Service: counting}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 1 {
		t.Fatalf("expected 1 artifact, got %d", resp.Meta.Pagination.Count)
	}
	if got := counting.callCount(sessEmpty); got != 0 {
		t.Errorf("prefilter must engage through the instrumentation wrapper; empty session scanned %d times, want 0", got)
	}
	if got := counting.callCount(sessHasArt); got != 1 {
		t.Errorf("ListKeys for sessHasArt = %d, want 1", got)
	}
}

func TestListAll_UnwrapsInstrumentationForVersionLister(t *testing.T) {
	h, sessRepo := setupArtifactAllHandler(t)

	ctx := context.Background()
	sess1 := idgen.New()
	_ = sessRepo.Create(ctx, &store.Session{ID: sess1, UserID: "sam_dev_user"})
	seedArtifacts(t, h, "sam_dev_user", sess1, "a.txt", "b.txt", "c.txt")

	counting := &listCountingStore{Service: h.artSvc.Inner}
	h.artSvc.Inner = instrumentationLikeWrapper{Service: counting}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/all", h.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/all", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeAllResponse(t, rec)
	if resp.Meta.Pagination.Count != 3 {
		t.Fatalf("expected 3 artifacts, got %d", resp.Meta.Pagination.Count)
	}

	listVersions, listArtifactVersions := counting.counts()
	if listArtifactVersions == 0 {
		t.Error("single-scan ListArtifactVersions must engage through the instrumentation wrapper, got 0 calls")
	}
	if listVersions != 0 {
		t.Errorf("per-artifact ListVersions fallback must not run when the version lister is reachable; got %d calls", listVersions)
	}
}
