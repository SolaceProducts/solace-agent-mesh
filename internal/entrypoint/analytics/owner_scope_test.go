// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestServiceActivityAgentPerformance_OwnerUserIDPropagated(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	svc := NewService(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	end := now + 100
	mk := func(id, user, agent string) {
		task := &store.Task{
			ID: id, UserID: user, StartTime: now, EndTime: i64p(end),
			Status: strp("completed"), ExecutionMode: "foreground",
			Kind:        store.TaskKindNormal,
			HandlerName: strp(agent), HandlerType: strp(store.TaskHandlerTypeAgent),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("90000000-0000-0000-0000-000000000001", "alice", "AgentAlice")
	mk("90000000-0000-0000-0000-000000000002", "alice", "AgentAlice")
	mk("90000000-0000-0000-0000-000000000003", "bob", "AgentBob")
	mk("90000000-0000-0000-0000-000000000004", "bob", "AgentBob")
	mk("90000000-0000-0000-0000-000000000005", "bob", "AgentBob")

	call := func(t *testing.T, q Query, label string) map[string]int {
		t.Helper()
		q.PageNumber, q.PageSize = 1, 100
		got, err := svc.ActivityAgentPerformance(ctx, q)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		out := map[string]int{}
		for _, r := range got.Data {
			out[r.Agent.Name] = r.InvocationCount
		}
		return out
	}

	assertRows := func(t *testing.T, label string, got, want map[string]int) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: row count = %d, want %d (got=%+v want=%+v)", label, len(got), len(want), got, want)
		}
		for name, n := range want {
			if got[name] != n {
				t.Errorf("%s: %s count = %d, want %d (got=%+v)", label, name, got[name], n, got)
			}
		}
	}

	assertRows(t, "alice-self",
		call(t, Query{OwnerUserID: "alice"}, "alice-self"),
		map[string]int{"AgentAlice": 2})

	assertRows(t, "bob-self",
		call(t, Query{OwnerUserID: "bob"}, "bob-self"),
		map[string]int{"AgentBob": 3})

	assertRows(t, "admin-tenant-wide",
		call(t, Query{}, "admin-tenant-wide"),
		map[string]int{"AgentAlice": 2, "AgentBob": 3})
}

func strp(s string) *string { return &s }
