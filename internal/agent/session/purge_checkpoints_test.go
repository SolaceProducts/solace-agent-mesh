// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type purgeStore interface {
	Store
}

func purgeStores(t *testing.T) map[string]purgeStore {
	return map[string]purgeStore{"sql": newTestSQLStore(t), "memory": NewMemoryStore()}
}

func savePurgeCheckpoint(t *testing.T, store Store, agent string, lease time.Time) string {
	t.Helper()
	ctx := t.Context()
	taskID, sessID := idgen.New(), idgen.New()
	if err := store.Create(ctx, &Session{AppName: agent, UserID: "user", SessionID: sessID, ContextID: sessID}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
		TaskID: taskID, AppName: agent, AgentName: agent, UserID: "user", SessionID: sessID,
		PendingCount: 1, OwnerID: "owner", LeaseExpiresAt: &lease,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	expires := lease.Add(time.Minute)
	if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
		CorrID: "corr-" + taskID, TaskID: taskID, ToolCallID: "tc", AgentName: agent, Kind: PendingKindPeer, ExpiresAt: &expires,
	}}); err != nil {
		t.Fatalf("register pending call: %v", err)
	}
	return taskID
}

func TestPurgeAbandonedCheckpointsKeepsHeldLeasesAndRemovesLapsedOnes(t *testing.T) {
	for name, store := range purgeStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			now := time.Now()
			lapsed := savePurgeCheckpoint(t, store, "gone-agent", now.Add(-48*time.Hour))
			held := savePurgeCheckpoint(t, store, "gone-agent", now.Add(time.Minute))
			otherAgent := savePurgeCheckpoint(t, store, "other-agent", now.Add(-48*time.Hour))

			removed, err := store.PurgeAbandonedCheckpoints(ctx, "gone-agent", now.Add(-24*time.Hour), testPurgeBatch)
			if err != nil {
				t.Fatalf("purge: %v", err)
			}
			if removed != 1 {
				t.Fatalf("removed = %d, want 1", removed)
			}
			if cp, _ := store.GetCheckpoint(ctx, lapsed); cp != nil {
				t.Fatal("a checkpoint whose lease lapsed two days ago was kept")
			}
			if pc, _ := store.LookupPendingCall(ctx, "gone-agent", "corr-"+lapsed); pc != nil {
				t.Fatal("the removed checkpoint's pending call was kept")
			}
			if cp, _ := store.GetCheckpoint(ctx, held); cp == nil {
				t.Fatal("a checkpoint with a held lease was removed")
			}
			if cp, _ := store.GetCheckpoint(ctx, otherAgent); cp == nil {
				t.Fatal("another agent's checkpoint was removed by an agent-scoped purge")
			}

			if removed, err = store.PurgeAbandonedCheckpoints(ctx, "", now.Add(-24*time.Hour), testPurgeBatch); err != nil || removed != 1 {
				t.Fatalf("purge of every agent removed %d (%v), want 1", removed, err)
			}
		})
	}
}

func TestPurgeAbandonedCheckpointsRemovesOrphanedPendingCalls(t *testing.T) {
	for name, store := range purgeStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			now := time.Now()
			old, fresh := now.Add(-48*time.Hour), now.Add(time.Hour)
			seedOrphanCalls(t, store, []PendingCall{
				{CorrID: "orphan-old", TaskID: "task-gone", ToolCallID: "a", AgentName: "agent", Kind: PendingKindPeer, ExpiresAt: &old},
				{CorrID: "orphan-fresh", TaskID: "task-gone", ToolCallID: "b", AgentName: "agent", Kind: PendingKindPeer, ExpiresAt: &fresh},
			})

			if _, err := store.PurgeAbandonedCheckpoints(ctx, "", now.Add(-24*time.Hour), testPurgeBatch); err != nil {
				t.Fatalf("purge: %v", err)
			}
			if pc, _ := store.LookupPendingCall(ctx, "agent", "orphan-old"); pc != nil {
				t.Fatal("an orphaned pending call past its expiry was kept")
			}
			if pc, _ := store.LookupPendingCall(ctx, "agent", "orphan-fresh"); pc == nil {
				t.Fatal("an orphaned pending call still within its expiry was removed")
			}
		})
	}
}

func TestPurgeAbandonedCheckpointsReportsAClosedDatabase(t *testing.T) {
	store := newTestSQLStore(t)
	_ = store.db.Close()
	if _, err := store.PurgeAbandonedCheckpoints(t.Context(), "agent", time.Now(), testPurgeBatch); err == nil {
		t.Fatal("a purge on a closed database reported success")
	}
}

func TestPurgeAbandonedCheckpointsRemovesTheirPendingCallsWhateverTheirExpiry(t *testing.T) {
	for name, store := range purgeStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			now := time.Now()
			taskID := savePurgeCheckpoint(t, store, "undeployed", now.Add(-time.Minute))
			later := now.Add(45 * time.Minute)
			if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
				CorrID: "hil-" + taskID, TaskID: taskID, ToolCallID: "ask", AgentName: "undeployed", Kind: PendingKindHILApproval, ExpiresAt: &later,
			}}); err != nil {
				t.Fatalf("register pending call: %v", err)
			}

			if _, err := store.PurgeAbandonedCheckpoints(ctx, "undeployed", now, testPurgeBatch); err != nil {
				t.Fatalf("purge: %v", err)
			}

			if pc, _ := store.LookupPendingCall(ctx, "undeployed", "hil-"+taskID); pc != nil {
				t.Fatal("a removed checkpoint's pending call outlived it because its own expiry was later")
			}
		})
	}
}
