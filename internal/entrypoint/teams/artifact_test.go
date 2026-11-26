// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type stubServices struct {
	arts *artifact.ScopedService
}

func (s *stubServices) Broker() broker.Broker                 { return nil }
func (s *stubServices) Config() config.Config                 { return nil }
func (s *stubServices) KVStore() runtime.KVStore              { return nil }
func (s *stubServices) Cache() runtime.Cache                  { return nil }
func (s *stubServices) Metrics() runtime.Metrics              { return runtime.NoopMetrics{} }
func (s *stubServices) Tracer() runtime.Tracer                { return runtime.NoopTracer{} }
func (s *stubServices) LLM(_ string) llm.Client               { return nil }
func (s *stubServices) Artifacts() *artifact.ScopedService    { return s.arts }
func (s *stubServices) Sessions() session.Store               { return nil }
func (s *stubServices) Authenticator() auth.Authenticator     { return auth.NoopAuthenticator{} }
func (s *stubServices) ToolAuthorizer() auth.ToolAuthorizer   { return auth.NoopToolAuthorizer{} }
func (s *stubServices) AgentAuthorizer() auth.AgentAuthorizer { return auth.NoopAgentAuthorizer{} }
func (s *stubServices) AuditLogger() auth.AuditLogger         { return auth.NoopAuditLogger{} }
func (s *stubServices) CredentialService() auth.CredentialService {
	return auth.NoopCredentialService{}
}
func (s *stubServices) ControlAuthorizer() auth.ControlAuthorizer {
	return auth.NoopControlAuthorizer{}
}
func (s *stubServices) FeatureRegistry() *features.Registry { return nil }

func (s *stubServices) UsageReporter() runtime.Component               { return nil }
func (s *stubServices) ModelResolver() runtime.ModelAliasResolver      { return nil }
func (s *stubServices) Bindings() runtime.ModelBindings                { return nil }
func (s *stubServices) ConnectorSpecStore() runtime.ConnectorSpecStore { return nil }
func (s *stubServices) Volumes() volume.Service                        { return nil }
func (s *stubServices) BudgetGate() *budget.Gate                       { return nil }
func (s *stubServices) HTTPClient() *http.Client                       { return http.DefaultClient }
func (s *stubServices) Logger() *slog.Logger                           { return slog.Default() }

func newAdapterWithArtifacts(t *testing.T, srv *httptest.Server) (*TeamsAdapter, *memory.Store) {
	t.Helper()
	memStore := memory.New()
	arts := &artifact.ScopedService{Inner: memStore, Scope: "agent"}

	a := &TeamsAdapter{
		cfg: &TeamsConfig{
			AppID:                "app-id",
			AppPassword:          "app-secret",
			LargeFileThresholdMB: 100,
			APIBaseURL:           srv.URL,
		},
		logger:  teamsNoopLogger(),
		queues:  map[string]*MessageQueue{},
		uploads: NewUploadManager(teamsNoopLogger()),
	}
	a.core = entrypoint.NewGatewayCore(a, map[string]any{})
	a.core.SetServices(&stubServices{arts: arts})
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, "", a.logger)
	a.botClient.apiBaseURL = srv.URL
	a.ctx = t.Context()
	return a, memStore
}

func registerTask(a *TeamsAdapter, taskID, agentName, userID string, meta *TeamsTaskMeta) {
	_ = a.core.Tasks().Register(taskID, agentName, userID, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, meta)
}

func TestLoadArtifactBytes_Success(t *testing.T) {
	srv := fakeBotServer(t, nil, new(int32))
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{SessionID: "sess-1"}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "report.pdf"}
	_, err := memStore.Save(t.Context(), loc, []byte("pdf-content"), "application/pdf")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	content, mimeType, loc, ok := a.loadArtifactBytes(t.Context(), "task-1", "report.pdf", nil)
	if !ok {
		t.Fatal("loadArtifactBytes returned ok=false, want true")
	}
	if string(content) != "pdf-content" {
		t.Errorf("content = %q, want %q", content, "pdf-content")
	}
	if mimeType != "application/pdf" {
		t.Errorf("mimeType = %q, want %q", mimeType, "application/pdf")
	}
	wantLoc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "report.pdf"}
	if loc != wantLoc {
		t.Errorf("loc = %+v, want %+v", loc, wantLoc)
	}
}

func TestLoadArtifactBytes_NoArtifactService(t *testing.T) {
	srv := fakeBotServer(t, nil, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)
	a.core.SetServices(&stubServices{arts: nil})

	meta := &TeamsTaskMeta{SessionID: "sess-1"}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	_, _, _, ok := a.loadArtifactBytes(t.Context(), "task-1", "report.pdf", nil)
	if ok {
		t.Error("loadArtifactBytes with nil artifact service returned ok=true")
	}
}

func TestLoadArtifactBytes_TaskNotTracked(t *testing.T) {
	srv := fakeBotServer(t, nil, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)

	_, _, _, ok := a.loadArtifactBytes(t.Context(), "unknown-task", "file.txt", nil)
	if ok {
		t.Error("loadArtifactBytes with untracked task returned ok=true")
	}
}

func TestLoadArtifactBytes_MetaMissing(t *testing.T) {
	srv := fakeBotServer(t, nil, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)

	_ = a.core.Tasks().Register("task-1", "agent", "user", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata("task-1", "not-a-teams-meta")

	_, _, _, ok := a.loadArtifactBytes(t.Context(), "task-1", "file.txt", nil)
	if ok {
		t.Error("loadArtifactBytes with wrong metadata type returned ok=true")
	}
}

func TestLoadArtifactBytes_EmptyContent(t *testing.T) {
	srv := fakeBotServer(t, nil, new(int32))
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{SessionID: "sess-1"}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "empty.txt"}
	_, err := memStore.Save(t.Context(), loc, []byte{}, "text/plain")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	_, _, _, ok := a.loadArtifactBytes(t.Context(), "task-1", "empty.txt", nil)
	if ok {
		t.Error("loadArtifactBytes with empty content returned ok=true")
	}
}

func TestHandleFilePart_ArtifactURI(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	srv := fakeBotServer(t, &posts, &postCount)
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     srv.URL,
		SessionID:      "sess-1",
		Activity:       &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: srv.URL},
	}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "slides.pptx"}
	_, err := memStore.Save(t.Context(), loc, []byte("pptx-bytes"), "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	a.handleFilePart(t.Context(), "task-1", meta, a2a.Part{
		File: &a2a.FileContent{
			Name: "slides.pptx",
			URI:  "artifact://test-agent/user-1/sess-1/slides.pptx",
		},
	})

	if got := atomic.LoadInt32(&postCount); got < 1 {
		t.Fatal("handleFilePart with artifact:// URI made no Bot Framework calls")
	}

	att, _ := posts[0]["attachments"].([]any)
	if len(att) == 0 {
		t.Fatal("expected FileConsent attachment")
	}
	first, _ := att[0].(map[string]any)
	if ct := first["contentType"]; ct != "application/vnd.microsoft.teams.card.file.consent" {
		t.Errorf("attachment contentType = %v, want file consent card", ct)
	}

	content, _ := first["content"].(map[string]any)
	acceptCtx, _ := content["acceptContext"].(map[string]any)
	wantCtx := map[string]any{
		"app_name":          "test-agent",
		"user_id":           "user-1",
		"session_id":        "sess-1",
		"artifact_filename": "slides.pptx",
	}
	for k, want := range wantCtx {
		if got := acceptCtx[k]; got != want {
			t.Errorf("acceptContext[%q] = %v, want %v", k, got, want)
		}
	}
}

func TestHandleDataPart_ArtifactCompleted(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	srv := fakeBotServer(t, &posts, &postCount)
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{
		ConversationID:   "conv-1",
		ServiceURL:       srv.URL,
		SessionID:        "sess-1",
		StatusActivityID: "status-1",
		Activity:         &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: srv.URL},
	}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "output.xlsx"}
	_, err := memStore.Save(t.Context(), loc, []byte("xlsx-bytes"), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	if err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	q := a.getOrCreateQueue("task-1", meta)
	t.Cleanup(func() { q.Stop(time.Second) })

	a.handleDataPart(t.Context(), "task-1", meta, q, a2a.Part{
		Data: map[string]any{
			"type":     "artifact_creation_progress",
			"filename": "output.xlsx",
			"status":   "completed",
		},
	})

	if got := atomic.LoadInt32(&postCount); got < 1 {
		t.Fatal("handleDataPart completed made no Bot Framework calls for the consent card")
	}

	att, _ := posts[0]["attachments"].([]any)
	if len(att) == 0 {
		t.Fatal("expected FileConsent attachment for completed artifact")
	}
}

func TestHandleDataPart_ArtifactCompleted_WorkingTagSkipped(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	srv := fakeBotServer(t, &posts, &postCount)
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{
		ConversationID:   "conv-1",
		ServiceURL:       srv.URL,
		SessionID:        "sess-1",
		StatusActivityID: "status-1",
		Activity:         &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: srv.URL},
	}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "scratch.json"}
	if _, err := memStore.Save(t.Context(), loc, []byte("{}"), "application/json"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	q := a.getOrCreateQueue("task-1", meta)
	t.Cleanup(func() { q.Stop(time.Second) })

	for _, status := range []string{"in-progress", "completed", "failed"} {
		a.handleDataPart(t.Context(), "task-1", meta, q, a2a.Part{
			Data: map[string]any{
				"type":     "artifact_creation_progress",
				"filename": "scratch.json",
				"status":   status,
				"tags":     []any{"__working"},
			},
		})
	}
	q.Stop(time.Second)

	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("expected no Bot Framework calls for __working artifact, got %d", got)
	}
}

func TestHandleDataPart_ArtifactCompleted_Dedupe(t *testing.T) {
	var postCount int32
	srv := fakeBotServer(t, nil, &postCount)
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{
		ConversationID:   "conv-1",
		ServiceURL:       srv.URL,
		SessionID:        "sess-1",
		StatusActivityID: "status-1",
		Activity:         &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: srv.URL},
	}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "dup.pdf"}
	_, _ = memStore.Save(t.Context(), loc, []byte("pdf"), "application/pdf")

	q := a.getOrCreateQueue("task-1", meta)
	t.Cleanup(func() { q.Stop(time.Second) })

	completedPart := a2a.Part{
		Data: map[string]any{
			"type":     "artifact_creation_progress",
			"filename": "dup.pdf",
			"status":   "completed",
		},
	}

	a.handleDataPart(t.Context(), "task-1", meta, q, completedPart)
	first := atomic.LoadInt32(&postCount)

	a.handleDataPart(t.Context(), "task-1", meta, q, completedPart)
	second := atomic.LoadInt32(&postCount)

	if second != first {
		t.Errorf("duplicate completed signal made %d extra Bot Framework calls; want 0", second-first)
	}
}

func TestHandleDataPart_ArtifactCompleted_FallbackText(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	srv := fakeBotServer(t, &posts, &postCount)
	a, _ := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{
		ConversationID:   "conv-1",
		ServiceURL:       srv.URL,
		SessionID:        "sess-1",
		StatusActivityID: "status-1",
		Activity:         &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: srv.URL},
	}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	q := a.getOrCreateQueue("task-1", meta)
	t.Cleanup(func() { q.Stop(time.Second) })

	a.handleDataPart(t.Context(), "task-1", meta, q, a2a.Part{
		Data: map[string]any{
			"type":     "artifact_creation_progress",
			"filename": "missing.pdf",
			"status":   "completed",
		},
	})

	q.Stop(2 * time.Second)

	if got := atomic.LoadInt32(&postCount); got < 1 {
		t.Fatal("expected at least 1 Bot Framework call for the fallback text update")
	}

	found := false
	for _, p := range posts {
		if text, ok := p["text"].(string); ok {
			if contains(text, "missing.pdf") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("fallback text update should mention the artifact filename")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := range len(s) - len(sub) + 1 {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestHandleDataPart_ArtifactCompleted_MIMEPrecedence(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	srv := fakeBotServer(t, &posts, &postCount)
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{
		ConversationID:   "conv-1",
		ServiceURL:       srv.URL,
		SessionID:        "sess-1",
		StatusActivityID: "status-1",
		Activity:         &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: srv.URL},
	}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "doc.pdf"}
	_, _ = memStore.Save(t.Context(), loc, []byte("pdf-bytes"), "application/octet-stream")

	q := a.getOrCreateQueue("task-1", meta)
	t.Cleanup(func() { q.Stop(time.Second) })

	a.handleDataPart(t.Context(), "task-1", meta, q, a2a.Part{
		Data: map[string]any{
			"type":      "artifact_creation_progress",
			"filename":  "doc.pdf",
			"status":    "completed",
			"mime_type": "application/pdf",
		},
	})

	if got := atomic.LoadInt32(&postCount); got < 1 {
		t.Fatal("expected a Bot Framework call")
	}

	att, _ := posts[0]["attachments"].([]any)
	if len(att) == 0 {
		t.Fatal("expected FileConsent attachment")
	}
}

func TestHandleDataPart_ArtifactCompleted_VersionRouting(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	srv := fakeBotServer(t, &posts, &postCount)
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{
		ConversationID:   "conv-1",
		ServiceURL:       srv.URL,
		SessionID:        "sess-1",
		StatusActivityID: "status-1",
		Activity:         &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: srv.URL},
	}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "data.csv"}
	_, _ = memStore.Save(t.Context(), loc, []byte("v0-content"), "text/csv")
	_, _ = memStore.Save(t.Context(), loc, []byte("v1-content"), "text/csv")

	q := a.getOrCreateQueue("task-1", meta)
	t.Cleanup(func() { q.Stop(time.Second) })

	a.handleDataPart(t.Context(), "task-1", meta, q, a2a.Part{
		Data: map[string]any{
			"type":     "artifact_creation_progress",
			"filename": "data.csv",
			"status":   "completed",
			"version":  float64(0),
		},
	})

	if got := atomic.LoadInt32(&postCount); got < 1 {
		t.Fatal("expected a Bot Framework call for version 0")
	}
}

func TestHandleDataPart_ArtifactCompleted_StringVersion(t *testing.T) {
	var postCount int32
	srv := fakeBotServer(t, nil, &postCount)
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{
		ConversationID:   "conv-1",
		ServiceURL:       srv.URL,
		SessionID:        "sess-1",
		StatusActivityID: "status-1",
		Activity:         &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: srv.URL},
	}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "report.txt"}
	_, _ = memStore.Save(t.Context(), loc, []byte("content"), "text/plain")

	q := a.getOrCreateQueue("task-1", meta)
	t.Cleanup(func() { q.Stop(time.Second) })

	a.handleDataPart(t.Context(), "task-1", meta, q, a2a.Part{
		Data: map[string]any{
			"type":     "artifact_creation_progress",
			"filename": "report.txt",
			"status":   "completed",
			"version":  "0",
		},
	})

	if got := atomic.LoadInt32(&postCount); got < 1 {
		t.Fatal("expected a Bot Framework call — string version should be parsed")
	}
}

func TestHandleFilePart_ArtifactURI_Dedupe(t *testing.T) {
	var postCount int32
	srv := fakeBotServer(t, nil, &postCount)
	a, memStore := newAdapterWithArtifacts(t, srv)

	meta := &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     srv.URL,
		SessionID:      "sess-1",
		Activity:       &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: srv.URL},
	}
	registerTask(a, "task-1", "test-agent", "user-1", meta)

	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: "replay.pptx"}
	_, _ = memStore.Save(t.Context(), loc, []byte("pptx"), "application/vnd.openxmlformats-officedocument.presentationml.presentation")

	part := a2a.Part{
		File: &a2a.FileContent{
			Name: "replay.pptx",
			URI:  "artifact://test-agent/user-1/sess-1/replay.pptx",
		},
	}

	a.handleFilePart(t.Context(), "task-1", meta, part)
	first := atomic.LoadInt32(&postCount)
	if first < 1 {
		t.Fatal("first handleFilePart should send a consent card")
	}

	a.handleFilePart(t.Context(), "task-1", meta, part)
	second := atomic.LoadInt32(&postCount)
	if second != first {
		t.Errorf("replay sent %d extra calls; want 0 (deduped)", second-first)
	}
}

func teamsFinalTask(taskID string, state a2a.TaskState) *a2a.Task {
	return &a2a.Task{ID: taskID, Kind: "task", Status: a2a.TaskStatus{State: state}}
}

func newAdapterForFinalResponse(t *testing.T, taskID string) (*TeamsAdapter, *[]capturedCall, *sync.Mutex) {
	t.Helper()
	srv, calls, mu := newMockBotServer(t)
	a, _ := newAdapterWithArtifacts(t, srv)
	meta := &TeamsTaskMeta{
		ConversationID:   "conv-1",
		ServiceURL:       srv.URL,
		StatusActivityID: "status-1",
		Activity:         &Activity{ID: "user-1"},
	}
	registerTask(a, taskID, "agent", "user", meta)
	return a, calls, mu
}

func TestOnFinalResponse_Completed_DeletesStatusActivity(t *testing.T) {
	a, calls, mu := newAdapterForFinalResponse(t, "task-ok")

	a.OnFinalResponse(t.Context(), "task-ok", teamsFinalTask("task-ok", a2a.TaskStateCompleted), nil)

	mu.Lock()
	defer mu.Unlock()
	deletes, puts := 0, 0
	for _, c := range *calls {
		switch c.Method {
		case http.MethodDelete:
			if c.Path == "/v3/conversations/conv-1/activities/status-1" {
				deletes++
			}
		case http.MethodPut:
			puts++
		}
	}
	if deletes != 1 {
		t.Errorf("DELETE of status activity = %d, want 1", deletes)
	}
	if puts != 0 {
		t.Errorf("PUT finalize calls = %d, want 0 (no terminal text on success)", puts)
	}
}

func TestOnFinalResponse_FailedState_FinalizesWithError(t *testing.T) {
	for _, state := range []a2a.TaskState{a2a.TaskStateFailed, a2a.TaskStateRejected} {
		t.Run(string(state), func(t *testing.T) {
			a, calls, mu := newAdapterForFinalResponse(t, "task-x")

			a.OnFinalResponse(t.Context(), "task-x", teamsFinalTask("task-x", state), nil)

			mu.Lock()
			defer mu.Unlock()
			deletes, gotErr := 0, false
			for _, c := range *calls {
				if c.Method == http.MethodDelete {
					deletes++
				}
				if c.Method == http.MethodPut {
					if txt, _ := c.Body["text"].(string); strings.Contains(txt, "error occurred") {
						gotErr = true
					}
				}
			}
			if deletes != 0 {
				t.Errorf("DELETE calls = %d, want 0 (failure must not silently delete)", deletes)
			}
			if !gotErr {
				t.Error("expected a PUT finalize containing an error message")
			}
		})
	}
}
