// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func assertProxiesPassStreamThrough(t *testing.T, stream string, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200", stream, resp.StatusCode)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("%s: X-Accel-Buffering = %q, want %q", stream, got, "no")
	}
	directives := map[string]bool{}
	for directive := range strings.SplitSeq(resp.Header.Get("Cache-Control"), ",") {
		directives[strings.TrimSpace(directive)] = true
	}
	for _, want := range []string{"no-cache", "no-store", "no-transform"} {
		if !directives[want] {
			t.Errorf("%s: Cache-Control = %q, want it to contain %s", stream, resp.Header.Get("Cache-Control"), want)
		}
	}
}

func TestTaskStream_TellsProxiesNotToBufferOrTransform(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "", "")

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/sse/subscribe/"+taskID, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	assertProxiesPassStreamThrough(t, "task stream", resp)
}

func TestTaskStreamReplayedFromLog_TellsProxiesNotToBufferOrTransform(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	if _, err := adapter.eventLog.Append(t.Context(), taskID, idgen.New(), "user-1", "final_response", []byte(`{"jsonrpc":"2.0","result":{"kind":"task"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := adapter.eventLog.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}

	subHandler := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", subHandler.handleSubscribe)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	assertProxiesPassStreamThrough(t, "replayed task stream", resp)
}

func TestNotificationStream_TellsProxiesNotToBufferOrTransform(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	server := httptest.NewServer(adapter.Handler())
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/sse/notifications", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	assertProxiesPassStreamThrough(t, "notification stream", resp)
}
