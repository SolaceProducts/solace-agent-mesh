// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func setupProjectCopyHarness(t *testing.T) (*entrypoint.GatewayCore, *store.SessionRepo, *store.ProjectRepo, *artifact.ScopedService) {
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
			"database_url": testDBURL(t, "projcopy"),
		},
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}

	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		t.Fatalf("load community flags: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithArtifacts(artSvc),
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

	return core, store.NewSessionRepo(db), store.NewProjectRepo(db), artSvc
}

func assignProjectAs(t *testing.T, h *sessionHandler, sessionID, projectID, callerUserID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/sessions/{id}/project", h.handleAssignProject)

	body := strings.NewReader(`{"projectId":"` + projectID + `"}`)
	req := httptest.NewRequest("PATCH", "/api/v1/sessions/"+sessionID+"/project", body)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: callerUserID})))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestArtifactCopyProject_SharedUserSeesProjectFiles(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, sharedUser = "owner_user", "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "Shared Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	projLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("project file body"), "application/pdf"); err != nil {
		t.Fatal(err)
	}

	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: sharedUser}); err != nil {
		t.Fatal(err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true})

	h := &sessionHandler{core: core, sessRepo: sessRepo, projectRepo: projectRepo, logger: slog.Default()}
	rec := assignProjectAs(t, h, sessionID, project.ID, sharedUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign project: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	sessLoc := artSvc.Resolve("", sharedUser, sessionID, "doc.pdf")
	content, _, err := artSvc.Inner.Load(ctx, sessLoc, nil)
	if err != nil {
		t.Fatalf("project file missing from shared user's session: %v", err)
	}
	if string(content) != "project file body" {
		t.Errorf("session artifact content = %q, want %q", string(content), "project file body")
	}
}

func TestArtifactCopyProject_OwnerStillWorks(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner = "owner_user"
	project := &store.Project{ID: idgen.New(), Name: "Owned Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	projLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("owned body"), "application/pdf"); err != nil {
		t.Fatal(err)
	}

	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: owner}); err != nil {
		t.Fatal(err)
	}

	h := &sessionHandler{core: core, sessRepo: sessRepo, projectRepo: projectRepo, logger: slog.Default()}
	rec := assignProjectAs(t, h, sessionID, project.ID, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign project: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	sessLoc := artSvc.Resolve("", owner, sessionID, "doc.pdf")
	content, _, err := artSvc.Inner.Load(ctx, sessLoc, nil)
	if err != nil || string(content) != "owned body" {
		t.Fatalf("owner's session missing project file: content=%q err=%v", string(content), err)
	}
}

func TestArtifactCopyProject_NoAccessReturns404(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, stranger = "owner_user", "stranger_user"
	project := &store.Project{ID: idgen.New(), Name: "Private Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	projLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "secret.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("secret"), "application/pdf"); err != nil {
		t.Fatal(err)
	}

	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: stranger}); err != nil {
		t.Fatal(err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: false})

	h := &sessionHandler{core: core, sessRepo: sessRepo, projectRepo: projectRepo, logger: slog.Default()}
	rec := assignProjectAs(t, h, sessionID, project.ID, stranger)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}

	sessLoc := artSvc.Resolve("", stranger, sessionID, "secret.pdf")
	if content, _, _ := artSvc.Inner.Load(ctx, sessLoc, nil); len(content) != 0 {
		t.Errorf("expected no leaked file, got %q", string(content))
	}
}

func TestArtifactCopyProject_NilProjectRepoFallback(t *testing.T) {
	core, sessRepo, _, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const user = "solo_user"
	projectID := idgen.New()
	projLoc := artSvc.Resolve("", user, projectSessionPrefix+projectID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("solo body"), "application/pdf"); err != nil {
		t.Fatal(err)
	}

	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: user}); err != nil {
		t.Fatal(err)
	}

	h := &sessionHandler{core: core, sessRepo: sessRepo, projectRepo: nil, logger: slog.Default()}
	rec := assignProjectAs(t, h, sessionID, projectID, user)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign project: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	sessLoc := artSvc.Resolve("", user, sessionID, "doc.pdf")
	content, _, err := artSvc.Inner.Load(ctx, sessLoc, nil)
	if err != nil || string(content) != "solo body" {
		t.Fatalf("nil-repo fallback failed: content=%q err=%v", string(content), err)
	}
}

func TestArtifactCopyProject_SeedsBM25IndexIntoSession(t *testing.T) {
	_, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, sharedUser = "owner_user", "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "Indexed Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	idxLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "project_bm25_index.zip")
	if _, err := artSvc.Inner.Save(ctx, idxLoc, []byte("zip-bytes"), "application/zip"); err != nil {
		t.Fatal(err)
	}

	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: sharedUser}); err != nil {
		t.Fatal(err)
	}

	copied, _, err := copyProjectArtifactsToSession(ctx, artSvc,
		project.ID, owner, sharedUser, sessionID,
		true, false, slog.Default())
	if err != nil {
		t.Fatalf("copy: unexpected error: %v", err)
	}
	if copied == 0 {
		t.Fatal("expected the index to be copied")
	}

	sessLoc := artSvc.Resolve("", sharedUser, sessionID, "project_bm25_index.zip")
	if content, _, err := artSvc.Inner.Load(ctx, sessLoc, nil); err != nil || string(content) != "zip-bytes" {
		t.Fatalf("BM25 index not in caller's session scope: content=%q err=%v", string(content), err)
	}
}

func sendMessageAs(t *testing.T, h *messageHandler, agentName, projectID, contextID, callerUserID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:stream", h.handleMessageStream)

	ctxField := ""
	if contextID != "" {
		ctxField = `"contextId":"` + contextID + `",`
	}
	body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user",` + ctxField +
		`"parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"` + agentName +
		`","project_id":"` + projectID + `"}}}}`
	req := httptest.NewRequest("POST", "/api/v1/message:stream", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: callerUserID})))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func newProjectMessageHandler(core *entrypoint.GatewayCore, sessRepo *store.SessionRepo, projectRepo *store.ProjectRepo) *messageHandler {
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "test-agent", URL: "solace:test"})
	return &messageHandler{
		core:        core,
		sseManager:  NewSSEManager(16, slog.Default()),
		sessRepo:    sessRepo,
		projectRepo: projectRepo,
	}
}

func TestHandleMessage_ProjectSharedUserSeesFiles(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, sharedUser = "owner_user", "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "Shared Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	projLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("project file body"), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true})

	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	rec := sendMessageAs(t, h, "test-agent", project.ID, "", sharedUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("send message: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp JSONRPCResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var task TaskResult
	if err := json.Unmarshal(resultBytes, &task); err != nil {
		t.Fatal(err)
	}
	if task.ContextID == "" {
		t.Fatal("expected a session contextID in the task result")
	}

	sessLoc := artSvc.Resolve("", sharedUser, task.ContextID, "doc.pdf")
	content, _, err := artSvc.Inner.Load(ctx, sessLoc, nil)
	if err != nil {
		t.Fatalf("project file missing from shared user's session: %v", err)
	}
	if string(content) != "project file body" {
		t.Errorf("session artifact content = %q, want %q", string(content), "project file body")
	}
}

func TestHandleMessage_ProjectNoAccessFallsThrough(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, stranger = "owner_user", "stranger_user"
	project := &store.Project{ID: idgen.New(), Name: "Private Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	projLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "secret.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("secret"), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: false})

	contextID := idgen.New()
	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	rec := sendMessageAs(t, h, "test-agent", project.ID, contextID, stranger)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (graceful fallthrough), got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp JSONRPCResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("expected no JSON-RPC error, got %+v", resp.Error)
	}

	sessLoc := artSvc.Resolve("", stranger, contextID, "secret.pdf")
	if content, _, _ := artSvc.Inner.Load(ctx, sessLoc, nil); len(content) != 0 {
		t.Errorf("expected no leaked file, got %q", string(content))
	}
}

func TestHandleMessage_RevokedShareDoesNotBrickSession(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, sharedUser = "owner_user", "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "Shared Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	projLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("project file body"), "application/pdf"); err != nil {
		t.Fatal(err)
	}

	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: sharedUser, ProjectID: &project.ID}); err != nil {
		t.Fatal(err)
	}

	sharing := &mockProjectSharingService{hasAccess: true}
	core.SetProjectSharingService(sharing)
	h := newProjectMessageHandler(core, sessRepo, projectRepo)

	if rec := sendMessageAs(t, h, "test-agent", "", sessionID, sharedUser); rec.Code != http.StatusOK {
		t.Fatalf("first message: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	sessLoc := artSvc.Resolve("", sharedUser, sessionID, "doc.pdf")
	if content, _, err := artSvc.Inner.Load(ctx, sessLoc, nil); err != nil || string(content) != "project file body" {
		t.Fatalf("expected file copied while shared: content=%q err=%v", string(content), err)
	}

	sharing.hasAccess = false

	rec := sendMessageAs(t, h, "test-agent", "", sessionID, sharedUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("post-revoke message: expected 200 (not bricked), got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp JSONRPCResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("post-revoke message: expected no JSON-RPC error, got %+v", resp.Error)
	}
}

func TestArtifactCopyProject_AssignMalformedProjectID400(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	ctx := context.Background()

	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: "owner_user"}); err != nil {
		t.Fatal(err)
	}

	h := &sessionHandler{core: core, sessRepo: sessRepo, projectRepo: projectRepo, logger: slog.Default()}
	rec := assignProjectAs(t, h, sessionID, "not-a-uuid", "owner_user")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed projectId, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleMessage_MalformedProjectID400(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)

	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	rec := sendMessageAs(t, h, "test-agent", "not-a-uuid", "", "shared_user")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed project_id, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp JSONRPCResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("expected JSON-RPC codeInvalidParams, got %+v", resp.Error)
	}
}

type listKeysFailer struct {
	artifact.Service
	failSessionID string
}

func (f listKeysFailer) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	if scope.SessionID == f.failSessionID {
		return nil, errors.New("backend unavailable")
	}
	return f.Service.ListKeys(ctx, scope)
}

func TestArtifactCopyProject_DedupListFailureDoesNotClobber(t *testing.T) {
	_, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, sharedUser = "owner_user", "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "Shared Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	projLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("project copy"), "application/pdf"); err != nil {
		t.Fatal(err)
	}

	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: sharedUser}); err != nil {
		t.Fatal(err)
	}
	sessLoc := artSvc.Resolve("", sharedUser, sessionID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, sessLoc, []byte("user edit"), "application/pdf"); err != nil {
		t.Fatal(err)
	}

	artSvc.Inner = listKeysFailer{Service: artSvc.Inner, failSessionID: sessionID}

	copied, _, err := copyProjectArtifactsToSession(ctx, artSvc,
		project.ID, owner, sharedUser, sessionID,
		false, false, slog.Default())
	if err == nil {
		t.Fatal("expected an error when the dedup listing fails")
	}
	if copied != 0 {
		t.Fatalf("expected nothing copied, got %d", copied)
	}
	if content, _, _ := artSvc.Inner.Load(ctx, sessLoc, nil); string(content) != "user edit" {
		t.Errorf("user edit was clobbered: latest = %q, want %q", string(content), "user edit")
	}
}

func TestArtifactCopyProject_AssignSurfaces5xxOnCopyFailure(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner = "owner_user"
	project := &store.Project{ID: idgen.New(), Name: "Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: owner}); err != nil {
		t.Fatal(err)
	}

	artSvc.Inner = listKeysFailer{Service: artSvc.Inner, failSessionID: projectSessionPrefix + project.ID}

	h := &sessionHandler{core: core, sessRepo: sessRepo, projectRepo: projectRepo, logger: slog.Default()}
	rec := assignProjectAs(t, h, sessionID, project.ID, owner)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on copy failure, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func submitAndCaptureMetadata(t *testing.T, core *entrypoint.GatewayCore, h *messageHandler, projectID, contextID, caller string) map[string]any {
	t.Helper()
	brk := core.Services().Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "test-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	rec := sendMessageAs(t, h, "test-agent", projectID, contextID, caller)
	if rec.Code != http.StatusOK {
		t.Fatalf("send message: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(rctx, q)
	if err != nil {
		t.Fatalf("expected a task published to the agent: %v", err)
	}

	var env struct {
		Params struct {
			Message struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		t.Fatalf("decode published request: %v", err)
	}
	return env.Params.Message.Metadata
}

func TestHandleMessage_StripsClientProjectIDWhenNoIndex(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner = "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "No Index Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}

	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	meta := submitAndCaptureMetadata(t, core, h, project.ID, "", owner)
	if _, ok := meta["project_id"]; ok {
		t.Errorf("client-supplied project_id should be stripped (no index), got metadata=%v", meta)
	}
}

func TestHandleMessage_ForwardsProjectIDWhenIndexPresent(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner = "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "Indexed Project", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	idxLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "project_bm25_index.zip")
	if _, err := artSvc.Inner.Save(ctx, idxLoc, []byte("zip-bytes"), "application/zip"); err != nil {
		t.Fatal(err)
	}

	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	meta := submitAndCaptureMetadata(t, core, h, project.ID, "", owner)
	if got, _ := meta["project_id"].(string); got != project.ID {
		t.Errorf("expected project_id=%s forwarded to agent, got metadata=%v", project.ID, meta)
	}
}

func TestArtifactCopyProject_SkipsExistingSessionArtifact(t *testing.T) {
	_, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner = "owner_user"
	project := &store.Project{ID: idgen.New(), Name: "P", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	projLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("project version"), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: owner}); err != nil {
		t.Fatal(err)
	}
	sessLoc := artSvc.Resolve("", owner, sessionID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, sessLoc, []byte("user edit"), "application/pdf"); err != nil {
		t.Fatal(err)
	}

	copied, _, err := copyProjectArtifactsToSession(ctx, artSvc,
		project.ID, owner, owner, sessionID,
		false, false, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if copied != 0 {
		t.Errorf("expected the existing artifact to be skipped, copied=%d", copied)
	}
	if content, _, _ := artSvc.Inner.Load(ctx, sessLoc, nil); string(content) != "user edit" {
		t.Errorf("existing session artifact was overwritten: %q", string(content))
	}
}

func TestArtifactCopyProject_PreservesArtifactMetadata(t *testing.T) {
	_, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, sharedUser = "owner_user", "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "P", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	if _, err := artifact.SaveWithMetadata(ctx, artSvc, "", owner, projectSessionPrefix+project.ID,
		"doc.pdf", []byte("body"), "application/pdf",
		artifact.MetadataOpts{Description: "quarterly report", Tags: []string{"finance", "q4"}},
		slog.Default()); err != nil {
		t.Fatal(err)
	}
	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: sharedUser}); err != nil {
		t.Fatal(err)
	}

	copied, _, err := copyProjectArtifactsToSession(ctx, artSvc,
		project.ID, owner, sharedUser, sessionID,
		false, false, slog.Default())
	if err != nil || copied != 1 {
		t.Fatalf("expected 1 copied, got copied=%d err=%v", copied, err)
	}

	meta := artifact.LoadCompanionMetadata(ctx, artSvc, "", sharedUser, sessionID, "doc.pdf", nil)
	if meta == nil {
		t.Fatal("expected companion metadata on the copied artifact")
	}
	if meta["description"] != "quarterly report" {
		t.Errorf("description not preserved: %v", meta["description"])
	}
	if pending, _ := meta["project_context_pending"].(bool); !pending {
		t.Errorf("expected project_context_pending=true, got %v", meta["project_context_pending"])
	}
}

type loadFailer struct {
	artifact.Service
	failSessionID string
}

func (f loadFailer) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	if loc.SessionID == f.failSessionID {
		return nil, "", errors.New("backend unavailable")
	}
	return f.Service.Load(ctx, loc, version)
}

func TestHandleMessage_AccessCheckErrorDegrades(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	ctx := context.Background()

	project := &store.Project{ID: idgen.New(), Name: "P", UserID: "owner_user"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{viewAccessErr: errors.New("sharing backend down")})

	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	meta := submitAndCaptureMetadata(t, core, h, project.ID, "", "stranger_user")
	if _, ok := meta["project_id"]; ok {
		t.Errorf("access-check error must degrade (no project_id forwarded), got metadata=%v", meta)
	}
}

func TestArtifactCopyProject_AssignAccessCheckErrorReturns500(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	ctx := context.Background()

	project := &store.Project{ID: idgen.New(), Name: "P", UserID: "owner_user"}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: "stranger_user"}); err != nil {
		t.Fatal(err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{viewAccessErr: errors.New("sharing backend down")})

	h := &sessionHandler{core: core, sessRepo: sessRepo, projectRepo: projectRepo, logger: slog.Default()}
	rec := assignProjectAs(t, h, sessionID, project.ID, "stranger_user")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on access-check error, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactCopyProject_AssignUnknownProjectReturns404(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	ctx := context.Background()

	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: "owner_user"}); err != nil {
		t.Fatal(err)
	}

	h := &sessionHandler{core: core, sessRepo: sessRepo, projectRepo: projectRepo, logger: slog.Default()}
	rec := assignProjectAs(t, h, sessionID, idgen.New(), "owner_user")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown project, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactCopyProject_AllArtifactLoadsFailReturnsError(t *testing.T) {
	_, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner = "owner_user"
	project := &store.Project{ID: idgen.New(), Name: "P", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	projLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "doc.pdf")
	if _, err := artSvc.Inner.Save(ctx, projLoc, []byte("body"), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	sessionID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: owner}); err != nil {
		t.Fatal(err)
	}

	artSvc.Inner = loadFailer{Service: artSvc.Inner, failSessionID: projectSessionPrefix + project.ID}

	copied, _, err := copyProjectArtifactsToSession(ctx, artSvc,
		project.ID, owner, owner, sessionID,
		true, true, slog.Default())
	if err == nil || copied != 0 {
		t.Fatalf("expected error and 0 copied on total load failure, got copied=%d err=%v", copied, err)
	}
}

func TestHandleMessage_CopyFailureDegradesAndUnadvertises(t *testing.T) {
	core, sessRepo, projectRepo, artSvc := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner = "owner_user"
	project := &store.Project{ID: idgen.New(), Name: "P", UserID: owner}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	idxLoc := artSvc.Resolve("", owner, projectSessionPrefix+project.ID, "project_bm25_index.zip")
	if _, err := artSvc.Inner.Save(ctx, idxLoc, []byte("zip-bytes"), "application/zip"); err != nil {
		t.Fatal(err)
	}
	contextID := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: contextID, UserID: owner}); err != nil {
		t.Fatal(err)
	}
	artSvc.Inner = listKeysFailer{Service: artSvc.Inner, failSessionID: contextID}

	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	meta := submitAndCaptureMetadata(t, core, h, project.ID, contextID, owner)
	if _, ok := meta["project_id"]; ok {
		t.Errorf("copy failure must un-advertise index_search (strip project_id), got metadata=%v", meta)
	}
}

func submitAndCaptureFirstText(t *testing.T, core *entrypoint.GatewayCore, h *messageHandler, projectID, contextID, caller string) string {
	t.Helper()
	brk := core.Services().Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "test-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	rec := sendMessageAs(t, h, "test-agent", projectID, contextID, caller)
	if rec.Code != http.StatusOK {
		t.Fatalf("send message: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(rctx, q)
	if err != nil {
		t.Fatalf("expected a task published to the agent: %v", err)
	}

	var env struct {
		Params struct {
			Message struct {
				Parts []struct {
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		t.Fatalf("decode published request: %v", err)
	}
	for _, p := range env.Params.Message.Parts {
		if p.Kind == "text" {
			return p.Text
		}
	}
	t.Fatalf("published message had no text part: %s", msg.Payload)
	return ""
}

func TestHandleMessage_SharedViewerGetsOwnerInstructions(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, sharedUser = "owner_user", "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "Shared Project", UserID: owner, SystemPrompt: strptr("be terse")}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true})

	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	text := submitAndCaptureFirstText(t, core, h, project.ID, "", sharedUser)

	if !strings.Contains(text, `workspace: "Shared Project"`) {
		t.Errorf("shared viewer's message missing project workspace framing: %q", text)
	}
	if !strings.Contains(text, "be terse") {
		t.Errorf("shared viewer's message missing owner Instructions: %q", text)
	}
	if !strings.Contains(text, "USER QUERY:\nhello") {
		t.Errorf("original query not preserved under USER QUERY marker: %q", text)
	}
}

func TestHandleMessage_ContinuingSessionSkipsInstructionInjection(t *testing.T) {
	core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
	ctx := context.Background()

	const owner, sharedUser = "owner_user", "shared_user"
	project := &store.Project{ID: idgen.New(), Name: "Shared Project", UserID: owner, SystemPrompt: strptr("be terse")}
	if err := projectRepo.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true})

	h := newProjectMessageHandler(core, sessRepo, projectRepo)
	text := submitAndCaptureFirstText(t, core, h, project.ID, idgen.New(), sharedUser)

	if text != "hello" {
		t.Errorf("continuing session must not inject project framing, got %q", text)
	}
}
