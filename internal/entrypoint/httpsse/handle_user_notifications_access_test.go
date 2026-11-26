// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func setupNoAuthAdapter(t *testing.T) *HTTPSSEAdapter {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()

	appConfig := map[string]any{
		"namespace":          "test-ns",
		"session_secret_key": "test-secret",
		"gateway_id":         "test-gw-noauth",
		"fastapi_host":       "127.0.0.1",
		"fastapi_port":       0,
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	return adapter
}

func notifMux(adapter *HTTPSSEAdapter) *http.ServeMux {
	mux := http.NewServeMux()
	h := &userNotificationsHandler{
		core:       adapter.core,
		sseManager: adapter.sseManager,
		logger:     adapter.logger,
	}
	mux.HandleFunc("GET /api/v1/sse/notifications", h.handleSubscribe)
	return mux
}

func TestUserNotificationsSubscribe_Unauthenticated(t *testing.T) {
	adapter := setupNoAuthAdapter(t)
	mux := notifMux(adapter)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sse/notifications", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated subscribe: got %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not authenticated") {
		t.Errorf("expected 'not authenticated' in body, got %q", rec.Body.String())
	}

	adapter.sseManager.userNotif.mu.Lock()
	queueLen := len(adapter.sseManager.userNotif.queues)
	adapter.sseManager.userNotif.mu.Unlock()
	if queueLen != 0 {
		t.Errorf("no queue should be registered after 401; found %d entries", queueLen)
	}
}

func TestUserNotificationsSubscribe_PerUserIsolation(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	withIdentity := func(userID string, next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := auth.NewTestSubject(auth.SubjectFields{UserID: userID, Email: userID + "@example.com"})
			next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), id)))
		})
	}

	h := &userNotificationsHandler{
		core:       adapter.core,
		sseManager: adapter.sseManager,
		logger:     adapter.logger,
	}

	aliceSrv := httptest.NewServer(withIdentity("alice", http.HandlerFunc(h.handleSubscribe)))
	t.Cleanup(aliceSrv.Close)

	bobSrv := httptest.NewServer(withIdentity("bob", http.HandlerFunc(h.handleSubscribe)))
	t.Cleanup(bobSrv.Close)

	aliceCtx, cancelAlice := context.WithCancel(t.Context())
	t.Cleanup(cancelAlice)
	aliceReq, _ := http.NewRequestWithContext(aliceCtx, http.MethodGet, aliceSrv.URL+"/api/v1/sse/notifications", nil)
	aliceResp, err := http.DefaultClient.Do(aliceReq)
	if err != nil {
		t.Fatalf("alice subscribe: %v", err)
	}
	t.Cleanup(func() { _ = aliceResp.Body.Close() })
	if aliceResp.StatusCode != http.StatusOK {
		t.Fatalf("alice subscribe: got %d, want 200", aliceResp.StatusCode)
	}

	bobCtx, cancelBob := context.WithCancel(t.Context())
	t.Cleanup(cancelBob)
	bobReq, _ := http.NewRequestWithContext(bobCtx, http.MethodGet, bobSrv.URL+"/api/v1/sse/notifications", nil)
	bobResp, err := http.DefaultClient.Do(bobReq)
	if err != nil {
		t.Fatalf("bob subscribe: %v", err)
	}
	t.Cleanup(func() { _ = bobResp.Body.Close() })
	if bobResp.StatusCode != http.StatusOK {
		t.Fatalf("bob subscribe: got %d, want 200", bobResp.StatusCode)
	}

	aliceReader := newStreamReader(aliceResp)
	bobReader := newStreamReader(bobResp)

	const openFrame = "notification stream established"
	if got := aliceReader.wait(t, openFrame, 2*time.Second); !strings.Contains(got, openFrame) {
		t.Fatalf("alice stream: missing opening frame; got %q", got)
	}
	if got := bobReader.wait(t, openFrame, 2*time.Second); !strings.Contains(got, openFrame) {
		t.Fatalf("bob stream: missing opening frame; got %q", got)
	}

	if n := userNotifQueueCount(adapter.sseManager, "alice"); n != 1 {
		t.Fatalf("alice queue count = %d, want 1", n)
	}
	if n := userNotifQueueCount(adapter.sseManager, "bob"); n != 1 {
		t.Fatalf("bob queue count = %d, want 1", n)
	}

	if err := adapter.sseManager.SendUserNotification("alice", "task_completed", map[string]any{
		"taskId": "t-alice-123",
	}); err != nil {
		t.Fatalf("SendUserNotification for alice: %v", err)
	}

	if got := aliceReader.wait(t, "task_completed", 2*time.Second); !strings.Contains(got, "task_completed") {
		t.Errorf("alice stream: expected task_completed event, got %q", got)
	}
	if !strings.Contains(aliceReader.snapshot(), "t-alice-123") {
		t.Errorf("alice stream: expected taskId payload, got %q", aliceReader.snapshot())
	}

	const isolationWait = 300 * time.Millisecond
	time.Sleep(isolationWait)
	if bobSnap := bobReader.snapshot(); strings.Contains(bobSnap, "task_completed") || strings.Contains(bobSnap, "t-alice-123") {
		t.Errorf("bob stream received alice's notification (isolation failure): %q", bobSnap)
	}
}
