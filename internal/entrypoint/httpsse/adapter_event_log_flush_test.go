// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestAdapterEventLogUsesConfiguredFlushInterval(t *testing.T) {
	const (
		longInterval  = 2 * time.Second
		shortInterval = 100 * time.Millisecond
		notYetWindow  = 300 * time.Millisecond
	)

	t.Run("long interval holds the second event until the tick", func(t *testing.T) {
		repo, appendEvent := newAsyncEventLogAdapter(t, longInterval)
		taskID := appendEvent(t, "")
		waitForDurableEvents(t, repo, taskID, 1, longInterval)

		appendEvent(t, taskID)
		time.Sleep(notYetWindow)
		if n := durableEventCount(t, repo, taskID); n != 1 {
			t.Fatalf("durable events %v after second append = %d, want 1: the configured %v interval did not reach the flusher", notYetWindow, n, longInterval)
		}
		waitForDurableEvents(t, repo, taskID, 2, 2*longInterval)
	})

	t.Run("short interval flushes the second event quickly", func(t *testing.T) {
		repo, appendEvent := newAsyncEventLogAdapter(t, shortInterval)
		taskID := appendEvent(t, "")
		waitForDurableEvents(t, repo, taskID, 1, longInterval)

		appendEvent(t, taskID)
		waitForDurableEvents(t, repo, taskID, 2, longInterval/2)
	})
}

func newAsyncEventLogAdapter(t *testing.T, interval time.Duration) (*store.SSEEventRepo, func(t *testing.T, taskID string) string) {
	t.Helper()
	adapter, _, _, _, repo := setupTestAdapterWithDBConfig(t, map[string]any{
		"async_event_log":             true,
		"event_log_flush_interval_ms": int(interval.Milliseconds()),
	})
	sessionID := idgen.New()
	appendEvent := func(t *testing.T, taskID string) string {
		t.Helper()
		if taskID == "" {
			taskID = idgen.New()
		}
		ctx, d := delivery.Attach(t.Context(), "", false, func() {})
		defer d.Release()
		if _, err := adapter.eventLog.Append(ctx, taskID, sessionID, "sam_dev_user", "status_update", []byte(`{}`)); err != nil {
			t.Fatalf("Append: %v", err)
		}
		return taskID
	}
	return repo, appendEvent
}

func durableEventCount(t *testing.T, repo *store.SSEEventRepo, taskID string) int {
	t.Helper()
	rows, err := repo.GetByTask(t.Context(), taskID, 0)
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}
	return len(rows)
}

func waitForDurableEvents(t *testing.T, repo *store.SSEEventRepo, taskID string, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		n := durableEventCount(t, repo, taskID)
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable events = %d after %v, want %d", n, within, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
