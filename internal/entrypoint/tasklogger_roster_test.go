// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestB08_TaskLoggerStoresCallTimeRosterWithoutCredentials(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{Enabled: true, LogStatusUpdates: true, QueueSize: 100}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	taskID := tid("b08-roster")
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{
		"kind": "status-update", "taskId": taskID, "metadata": map[string]any{"agent_name": "RosterAgent"},
		"status": map[string]any{"state": "working", "message": map[string]any{"kind": "message", "role": "agent", "messageId": "m1",
			"parts": []any{map[string]any{"kind": "data", "data": map[string]any{
				"type": "llm_invocation",
				"request": map[string]any{"model": "m", "contents": []any{}, "config": map[string]any{
					"system_instruction": "sys",
					"tools": []any{map[string]any{
						"api_key": "sk-ROSTER-ENTRY-SECRET",
						"function_declarations": []any{map[string]any{
							"name": "search_docs", "description": "Search the docs",
							"parameters":  map[string]any{"type": "object"},
							"auth_config": map[string]any{"client_secret": "ROSTER-DECL-SECRET"},
						}},
					}},
				}},
			}}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	svc.handleBrokerMessage(&broker.Message{Topic: "test-ns/a2a/v1/gateway/status/gw-test/" + taskID, Payload: payload})
	svc.Stop()

	hashes, err := dbutil.QueryVals[string](t.Context(), db,
		`SELECT DISTINCT config_tools_blob FROM task_events WHERE task_id = ? AND config_tools_blob IS NOT NULL`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 1 {
		t.Fatalf("roster references for task = %v, want exactly one", hashes)
	}
	contents, err := eventRepo.Blobs().GetContentByHashes(t.Context(), hashes)
	if err != nil {
		t.Fatal(err)
	}
	roster := contents[hashes[0]]
	if !strings.Contains(roster, `"search_docs"`) || !strings.Contains(roster, `"Search the docs"`) {
		t.Fatalf("roster lost the declaration name or description: %s", roster)
	}
	events, err := eventRepo.GetByTaskID(t.Context(), taskID)
	if err != nil || len(events) != 1 {
		t.Fatalf("events: %v (%d)", err, len(events))
	}
	for _, stored := range []string{roster, events[0].Payload} {
		if strings.Contains(stored, "SECRET") || strings.Contains(stored, "auth_config") || strings.Contains(stored, "api_key") {
			t.Fatalf("auth material persisted: %s", stored)
		}
	}
}
