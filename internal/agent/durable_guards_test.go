// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

func TestFenceReclaimsAnUnownedCheckpointRatherThanJustKeepingIt(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-reclaim"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	agent.pausedTasks.Store(state)
	if err := store.ReleaseOwnership(ctx, state.TaskID, agent.checkpoints.ownerID); err != nil {
		t.Fatalf("release: %v", err)
	}

	agent.renewOwnedLeases(ctx, time.Now())

	owner, exists, err := store.CheckpointOwner(ctx, state.TaskID)
	if err != nil || !exists {
		t.Fatalf("read owner: %v, %v", exists, err)
	}
	if owner != agent.checkpoints.ownerID {
		t.Errorf("the unowned row was left unowned (owner %q) — the shortfall latches on every tick from here, and once the skew allowance passes a third replica adopts a task this one is still running", owner)
	}
	if agent.pausedTasks.Load(state.TaskID) == nil {
		t.Error("the task was dropped rather than reclaimed")
	}
}

func TestPerPodStoreWarningNamesTheBackend(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		var buf bytes.Buffer
		warnIfStoreIsPerPod(sqliteDialectStore{}, "agent-1", slog.New(slog.NewTextHandler(&buf, nil)))
		if !strings.Contains(buf.String(), "sessionStore=sqlite") {
			t.Errorf("no per-pod warning for a SQLite store: %q — durable resume over a per-pod file writes every checkpoint successfully and no other replica can ever read one, so the feature reports itself as working while delivering nothing", buf.String())
		}
	})

	t.Run("postgres is not warned about", func(t *testing.T) {
		var buf bytes.Buffer
		warnIfStoreIsPerPod(postgresDialectStore{}, "agent-1", slog.New(slog.NewTextHandler(&buf, nil)))
		if strings.Contains(buf.String(), "local to this process") {
			t.Errorf("a shared Postgres store was warned about as per-pod: %q — an alarm that fires on the correct configuration is one an operator learns to ignore", buf.String())
		}
	})
}

func TestWarnsWhenStoreCannotExtendPendingCalls(t *testing.T) {
	construct := func(t *testing.T, store session.Store) string {
		t.Helper()
		var buf bytes.Buffer
		svc := newMockServices(newMockLLM(), store)
		svc.featureRegistry = durableResumeRegistry(true)
		if c := newTaskCheckpointer(svc, "agent-1", newTestAgentBackstops(), slog.New(slog.NewTextHandler(&buf, nil))); c == nil {
			t.Fatal("expected an enabled checkpointer")
		}
		return buf.String()
	}

	t.Run("store without the capability", func(t *testing.T) {
		logs := construct(t, storeWithoutExtend{Store: session.NewMemoryStore()})
		if !strings.Contains(logs, "cannot extend a pending call's expiry") || !strings.Contains(logs, "agentName=agent-1") {
			t.Errorf("no warning for a store that cannot extend pending calls: %q", logs)
		}
	})

	t.Run("store with the capability is not warned about", func(t *testing.T) {
		if logs := construct(t, session.NewMemoryStore()); strings.Contains(logs, "cannot extend") {
			t.Errorf("warned about a store that can extend pending calls: %q", logs)
		}
	})
}

type storeWithoutExtend struct{ session.Store }

type sqliteDialectStore struct{ session.Store }

func (sqliteDialectStore) Dialect() string { return "sqlite" }

type postgresDialectStore struct{ session.Store }

func (postgresDialectStore) Dialect() string { return "postgres" }
