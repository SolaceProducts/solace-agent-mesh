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
	"sync"
	"testing"
	"time"
)

type streamReader struct {
	mu      sync.Mutex
	buf     strings.Builder
	updates chan struct{}
	done    chan struct{}
}

func newStreamReader(resp *http.Response) *streamReader {
	r := &streamReader{
		updates: make(chan struct{}, 16),
		done:    make(chan struct{}),
	}
	go func() {
		defer close(r.done)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				r.mu.Lock()
				r.buf.Write(buf[:n])
				r.mu.Unlock()
				select {
				case r.updates <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

func (r *streamReader) snapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func (r *streamReader) wait(t *testing.T, marker string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		r.mu.Lock()
		s := r.buf.String()
		r.mu.Unlock()
		if strings.Contains(s, marker) {
			return s
		}
		select {
		case <-r.updates:
		case <-r.done:
			return r.snapshot()
		case <-deadline:
			return r.snapshot()
		}
	}
}

func TestUserNotificationsEndpoint_StreamsEvents(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	srv := httptest.NewServer(adapter.Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/sse/notifications", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	reader := newStreamReader(resp)

	if got := reader.wait(t, "notification stream established", 2*time.Second); !strings.Contains(got, ": notification stream established") {
		t.Fatalf("expected opening comment, got: %q", got)
	}

	uid := adapter.core.Config().DefaultUserIdentity
	if uid == "" {
		t.Fatal("default user identity is empty; cannot target the connected stream")
	}

	if err := adapter.sseManager.SendUserNotification(uid, "session_created", map[string]any{
		"sessionId": "sess-1",
	}); err != nil {
		t.Fatalf("SendUserNotification: %v", err)
	}

	got := reader.wait(t, "session_created", 2*time.Second)
	if !strings.Contains(got, "event: session_created") {
		t.Errorf("expected session_created frame, got: %q", got)
	}
	if !strings.Contains(got, `"sessionId":"sess-1"`) {
		t.Errorf("expected session_id payload in data field, got: %q", got)
	}
}

func TestUserNotificationsEndpoint_DisconnectUnregistersQueue(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	srv := httptest.NewServer(adapter.Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(t.Context())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/sse/notifications", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	reader := newStreamReader(resp)
	if got := reader.wait(t, "notification stream established", 2*time.Second); !strings.Contains(got, "stream established") {
		_ = resp.Body.Close()
		t.Fatalf("did not receive opening frame: %q", got)
	}

	uid := adapter.core.Config().DefaultUserIdentity
	if uid == "" {
		_ = resp.Body.Close()
		t.Fatal("default user identity is empty")
	}
	if got := userNotifQueueCount(adapter.sseManager, uid); got != 1 {
		_ = resp.Body.Close()
		t.Fatalf("queue count before disconnect = %d, want 1", got)
	}

	cancel()
	_ = resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if userNotifQueueCount(adapter.sseManager, uid) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("queue was not unregistered after disconnect; count = %d", userNotifQueueCount(adapter.sseManager, uid))
}

func userNotifQueueCount(m *SSEManager, userID string) int {
	m.userNotif.mu.Lock()
	defer m.userNotif.mu.Unlock()
	return len(m.userNotif.queues[userID])
}
