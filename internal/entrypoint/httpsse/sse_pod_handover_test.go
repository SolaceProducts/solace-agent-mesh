// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestSSEFinalResponseSurvivesGatewayHandover(t *testing.T) {
	ctx := context.Background()
	sdb := store.TestDB(t)
	repo := store.NewSSEEventRepo(sdb)
	taskRepo := store.NewTaskRepo(sdb)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	newAdapter := func() *HTTPSSEAdapter {
		return &HTTPSSEAdapter{
			sseManager: NewSSEManager(100, logger),
			eventLog:   eventlog.NewPersistentEventLog(repo, taskRepo, logger),
			logger:     logger,
		}
	}

	replayHasFinal := func(taskID string) bool {
		t.Helper()
		events, err := eventlog.NewPersistentEventLog(repo, taskRepo, logger).Replay(ctx, taskID, 0)
		if err != nil {
			t.Fatalf("Replay(%s): %v", taskID, err)
		}
		for _, e := range events {
			if e.EventType == "final_response" {
				return true
			}
		}
		return false
	}

	finalResponse := func(taskID, contextID string) *a2a.Task {
		return &a2a.Task{ID: taskID, ContextID: contextID}
	}
	rawOf := func(task *a2a.Task) []byte {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": task.ID, "result": task})
		return raw
	}

	t.Run("successor pod persists final via wire context", func(t *testing.T) {
		taskID := idgen.New()
		sessionID := idgen.New()

		pod1 := newAdapter()
		pod1.sseManager.RegisterTask(taskID, sessionID, "user-1")

		pod2 := newAdapter()
		task := finalResponse(taskID, sessionID)
		pod2.OnFinalResponse(ctx, taskID, task, rawOf(task))

		if !replayHasFinal(taskID) {
			t.Fatal("final_response was not durably persisted on the successor pod; the answer would be lost on reconnect")
		}
	})

	t.Run("no session id anywhere is dropped", func(t *testing.T) {
		taskID := idgen.New()
		pod2 := newAdapter()
		task := finalResponse(taskID, "")
		pod2.OnFinalResponse(ctx, taskID, task, rawOf(task))

		if replayHasFinal(taskID) {
			t.Fatal("expected the durable write to be dropped when no session id is resolvable")
		}
	})
}
