// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestRemovingAnAgentPurgesItsReleasedCheckpointsOnly(t *testing.T) {
	store := session.NewMemoryStore()
	exe := NewExecutor(runtime.NewRuntime(broker.NewDevBroker(), nil, runtime.WithSessions(store)).Services())
	ctx := t.Context()
	released, held := time.Now().Add(-time.Second), time.Now().Add(time.Minute)
	for taskID, lease := range map[string]time.Time{"task-released": released, "task-held": held} {
		if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
			TaskID: taskID, AppName: "gone", AgentName: "gone", UserID: "u", SessionID: "s",
			OwnerID: "owner", LeaseExpiresAt: &lease,
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	if err := exe.RemoveInstance(ctx, "gone"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if cp, _ := store.GetCheckpoint(ctx, "task-released"); cp != nil {
		t.Fatal("a removed agent's released checkpoint was kept; nothing would ever take it over")
	}
	if cp, _ := store.GetCheckpoint(ctx, "task-held"); cp == nil {
		t.Fatal("a checkpoint another process still holds was removed")
	}
}

func newPurgeExecutor(t *testing.T) (*Executor, *session.MemoryStore) {
	t.Helper()
	store := session.NewMemoryStore()
	lapsed := time.Now().Add(-time.Second)
	if err := store.SaveCheckpoint(t.Context(), &session.TaskCheckpoint{
		TaskID: "task-released", AppName: "warm", AgentName: "warm", UserID: "u", SessionID: "s", OwnerID: "owner", LeaseExpiresAt: &lapsed,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	exe := NewExecutor(runtime.NewRuntime(broker.NewDevBroker(), nil, runtime.WithSessions(store)).Services())
	exe.AddInstance(newRemoverInstance("warm", "agent"))
	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()
	return exe, store
}

func TestRemovingARunningAgentPurgesItsReleasedCheckpoints(t *testing.T) {
	exe, store := newPurgeExecutor(t)
	if err := exe.RemoveInstance(t.Context(), "warm"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if cp, _ := store.GetCheckpoint(t.Context(), "task-released"); cp != nil {
		t.Fatal("removing a running agent kept its released checkpoint")
	}
}

func TestReplacingAnAgentKeepsItsCheckpoints(t *testing.T) {
	exe, store := newPurgeExecutor(t)
	if err := exe.removeInstance(t.Context(), "warm", removeForReplace); err != nil {
		t.Fatalf("remove for replace: %v", err)
	}
	if cp, _ := store.GetCheckpoint(t.Context(), "task-released"); cp == nil {
		t.Fatal("a replace removed a checkpoint the re-added agent takes over")
	}
}

func TestUndeployingAnAgentStopsItPurgesItsCheckpointsAndKeepsItsQueue(t *testing.T) {
	exe, store := newPurgeExecutor(t)
	inst := exe.instances[0].inst.(*removerInstance)
	if err := exe.removeInstance(t.Context(), "warm", removeForUndeploy); err != nil {
		t.Fatalf("remove for undeploy: %v", err)
	}
	if inst.removeCalled.Load() != 0 {
		t.Fatal("an undeploy deleted the agent's queue on the AWE; the leader deletes it once every AWE confirms")
	}
	if inst.stopCalled.Load() != 1 {
		t.Fatalf("stop called %d times, want 1: an undeploy ends the agent's open tasks", inst.stopCalled.Load())
	}
	if cp, _ := store.GetCheckpoint(t.Context(), "task-released"); cp != nil {
		t.Fatal("an undeploy kept the released checkpoint; no copy of the agent is left to take it over")
	}
}
