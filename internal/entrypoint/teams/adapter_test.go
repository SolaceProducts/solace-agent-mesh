// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func teamsNoopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestAdapter(t *testing.T) *TeamsAdapter {
	t.Helper()
	cfg := &TeamsConfig{
		AppID:       "app-id",
		AppPassword: "app-secret",
		HTTPHost:    "127.0.0.1",
		HTTPPort:    0,
		RunAs:       "default",
	}
	a, err := NewTeamsAdapter(nil)
	if err != nil {
		t.Fatalf("NewTeamsAdapter: %v", err)
	}
	a.cfg = cfg
	a.logger = teamsNoopLogger()
	a.server = NewTeamsServer(cfg, nil, func(context.Context, *Activity) *InvokeResponse { return nil }, a.logger)
	a.feedbackCtx = runtime.NewMapCache()
	return a
}

func TestStart_Stop_NoListener(t *testing.T) {
	a := newTestAdapter(t)
	a.uploads = NewUploadManager(a.logger)

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestHandler_NilBeforeInit(t *testing.T) {
	a, err := NewTeamsAdapter(nil)
	if err != nil {
		t.Fatalf("NewTeamsAdapter: %v", err)
	}
	if h := a.Handler(); h != nil {
		t.Errorf("Handler() before Init = %T, want nil", h)
	}
}

func TestHandler_ReturnsMuxBeforeStart(t *testing.T) {
	a := newTestAdapter(t)

	h := a.Handler()
	if h == nil {
		t.Fatal("Handler() returned nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /health returned %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("GET /health body = %q, want JSON containing \"ok\"", rec.Body.String())
	}
}

func TestHandler_MessagesRequiresAuth(t *testing.T) {
	a := newTestAdapter(t)
	a.botAuth = NewBotAuthenticator(a.cfg.AppID, a.logger)
	a.server = NewTeamsServer(a.cfg, a.botAuth, func(context.Context, *Activity) *InvokeResponse { return nil }, a.logger)

	req := httptest.NewRequest(http.MethodPost, "/api/messages", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /api/messages without auth returned %d, want 401", rec.Code)
	}
}

func fakeBotServer(t *testing.T, posts *[]map[string]any, postCount *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		atomic.AddInt32(postCount, 1)
		if posts != nil {
			*posts = append(*posts, m)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "act-eager"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPostInitialStatus_PostsPlainText(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	srv := fakeBotServer(t, &posts, &postCount)

	a := newTestAdapter(t)
	a.cfg.InitialStatusMessage = "Processing your request..."
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = srv.URL
	a.ctx = context.Background()

	meta := &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     srv.URL,
		Activity:       &Activity{ID: "user-msg-1"},
	}

	a.postInitialStatus(t.Context(), "task-1", meta)

	if got := atomic.LoadInt32(&postCount); got < 1 {
		t.Fatalf("postInitialStatus made no Bot Framework calls; want at least 1")
	}
	first := posts[0]
	if first["text"] != "Processing your request..." {
		t.Errorf("first POST text = %v, want %q", first["text"], a.cfg.InitialStatusMessage)
	}
	if _, hasAtt := first["attachments"]; hasAtt {
		t.Errorf("first POST contains attachments — must be omitted to avoid the cards.unsupported regression")
	}
	if first["replyToId"] != "user-msg-1" {
		t.Errorf("first POST replyToId = %v, want %q", first["replyToId"], "user-msg-1")
	}
	if meta.StatusActivityID != "act-eager" {
		t.Errorf("StatusActivityID = %q, want %q (server-returned id must be captured)", meta.StatusActivityID, "act-eager")
	}
}

func TestPostInitialStatus_SkipWhenMessageEmpty(t *testing.T) {
	var postCount int32
	srv := fakeBotServer(t, nil, &postCount)

	a := newTestAdapter(t)
	a.cfg.InitialStatusMessage = ""
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = srv.URL
	a.ctx = context.Background()

	meta := &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     srv.URL,
		Activity:       &Activity{ID: "user-msg-1"},
	}

	a.postInitialStatus(t.Context(), "task-1", meta)

	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Errorf("expected 0 Bot Framework calls when InitialStatusMessage is empty, got %d", got)
	}
}

func TestGetOrCreateQueue_AppliesConfigInProduction(t *testing.T) {
	a := newTestAdapter(t)
	a.cfg.APIBaseURL = ""
	a.cfg.MinAPIInterval = 50 * time.Millisecond
	a.cfg.BufferUpdateInterval = 123 * time.Millisecond
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.queues = map[string]*MessageQueue{}

	meta := &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     "https://example",
		Activity:       &Activity{ID: "user-msg-1"},
	}
	q := a.getOrCreateQueue("task-1", meta)
	t.Cleanup(func() { q.Stop(time.Second) })

	if q.minAPIInterval != 50*time.Millisecond {
		t.Errorf("minAPIInterval = %v, want config-derived 50ms", q.minAPIInterval)
	}
	if q.bufferInterval != 123*time.Millisecond {
		t.Errorf("bufferInterval = %v, want config-derived 123ms", q.bufferInterval)
	}
}

func TestGetOrCreateQueue_TestModeZerosThrottle(t *testing.T) {
	a := newTestAdapter(t)
	a.cfg.APIBaseURL = "http://localhost:9999"
	a.cfg.MinAPIInterval = 200 * time.Millisecond
	a.cfg.BufferUpdateInterval = 500 * time.Millisecond
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.queues = map[string]*MessageQueue{}

	meta := &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     "https://example",
		Activity:       &Activity{ID: "user-msg-1"},
	}
	q := a.getOrCreateQueue("task-1", meta)
	t.Cleanup(func() { q.Stop(time.Second) })

	if q.minAPIInterval != 0 {
		t.Errorf("minAPIInterval = %v, want 0 in test mode", q.minAPIInterval)
	}
	if q.bufferInterval != 0 {
		t.Errorf("bufferInterval = %v, want 0 in test mode", q.bufferInterval)
	}
}

func TestHandleDataPart_ArtifactCreationProgress_StatusBranches(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		wantMethod string
		wantText   string
	}{
		{name: "in-progress shows status no fetch", status: "in-progress", wantMethod: http.MethodPut, wantText: "💭 💾 Creating `chart.png`..."},
		{name: "failed queues error text no fetch", status: "failed", wantMethod: http.MethodPost, wantText: "❌ Failed to create artifact: `chart.png`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls, mu := newMockBotServer(t)
			t.Cleanup(srv.Close)

			a := newTestAdapter(t)
			a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
			a.botClient.apiBaseURL = srv.URL
			a.ctx = context.Background()

			q := newTestQueue(t, srv)
			q.Start()
			t.Cleanup(func() { q.Stop(5 * time.Second) })

			meta := &TeamsTaskMeta{
				ConversationID:   "conv-1",
				ServiceURL:       srv.URL,
				StatusActivityID: "status-1",
			}

			a.handleDataPart(t.Context(), "task-1", meta, q, a2a.Part{
				Kind: "data",
				Data: map[string]any{
					"type":     "artifact_creation_progress",
					"status":   tc.status,
					"filename": "chart.png",
				},
			})
			q.WaitDrain()

			mu.Lock()
			defer mu.Unlock()
			var matched bool
			for _, c := range *calls {
				if c.Method == tc.wantMethod {
					if text, _ := c.Body["text"].(string); text == tc.wantText {
						matched = true
						break
					}
				}
			}
			if !matched {
				paths := make([]string, len(*calls))
				for i, c := range *calls {
					paths[i] = c.Method + " " + c.Path + " text=" + jsonStringField(c.Body, "text")
				}
				t.Errorf("no %s call with text %q; calls = %v", tc.wantMethod, tc.wantText, paths)
			}
		})
	}
}

func jsonStringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
