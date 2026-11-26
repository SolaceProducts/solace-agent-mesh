// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestDeleteCheckpointIfOwnedKeysOnTheOwner(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		soon := time.Now().Add(time.Hour)

		t.Run("another replica's row is left alone", func(t *testing.T) {
			taskID, sessID := idgen.New(), idgen.New()
			seed("app", "user", sessID)
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: "app", OwnerID: "the-successor", LeaseExpiresAt: &soon,
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			deleted, err := store.DeleteCheckpointIfOwned(ctx, taskID, "the-fenced-replica")
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			if deleted {
				t.Error("a fenced replica deleted the row its successor owns — the successor loses the task it is running")
			}
			if cp, _ := store.GetCheckpoint(ctx, taskID); cp == nil {
				t.Error("the successor's row is gone")
			}
		})

		t.Run("our own row goes, with its correlations", func(t *testing.T) {
			taskID, sessID := idgen.New(), idgen.New()
			seed("app", "user", sessID)
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: "app", OwnerID: "mine", LeaseExpiresAt: &soon,
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
				CorrID: "corr-own", TaskID: taskID, AgentName: "app",
				ToolCallID: "tc-1", Kind: PendingKindPeer,
			}}); err != nil {
				t.Fatalf("register: %v", err)
			}
			deleted, err := store.DeleteCheckpointIfOwned(ctx, taskID, "mine")
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			if !deleted {
				t.Fatal("the owner could not delete its own row at its terminal path — nothing else will, so a late reply resumes work that already finished")
			}
			if cp, _ := store.GetCheckpoint(ctx, taskID); cp != nil {
				t.Error("the row survived a reported delete")
			}
			if row, _ := store.LookupPendingCall(ctx, "app", "corr-own"); row != nil {
				t.Error("the correlation row outlived its checkpoint, so a late reply is adopted against a task that is gone")
			}
		})

		t.Run("an unowned row counts as ours", func(t *testing.T) {
			taskID, sessID := idgen.New(), idgen.New()
			seed("app", "user", sessID)
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: "app", OwnerID: "mine", LeaseExpiresAt: &soon,
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			if err := store.ReleaseOwnership(ctx, taskID, "mine"); err != nil {
				t.Fatalf("release: %v", err)
			}
			deleted, err := store.DeleteCheckpointIfOwned(ctx, taskID, "mine")
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			if !deleted {
				t.Error("a released row could not be deleted by the replica still running the task — it then suppresses its own terminal publish and nobody answers the caller")
			}
		})
	})
}

func TestDeleteAbandonedCheckpointKeysOnTheLease(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()

		t.Run("a live lease is respected", func(t *testing.T) {
			taskID, sessID := idgen.New(), idgen.New()
			seed("app", "user", sessID)
			soon := time.Now().Add(time.Hour)
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: "app", PendingCount: 1, OwnerID: "a-live-replica", LeaseExpiresAt: &soon,
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			deleted, err := store.DeleteAbandonedCheckpoint(ctx, taskID, time.Now())
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			if deleted {
				t.Error("one replica destroyed a task another holds a live lease on — that replica's work vanishes mid-turn")
			}
		})

		t.Run("a lapsed lease is taken, with its correlations", func(t *testing.T) {
			taskID, sessID := idgen.New(), idgen.New()
			seed("app", "user", sessID)
			lapsed := time.Now().Add(-time.Hour)
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: "app", PendingCount: 1, OwnerID: "a-dead-replica", LeaseExpiresAt: &lapsed,
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
				CorrID: "corr-dead", TaskID: taskID, AgentName: "app", Kind: PendingKindPeer,
			}}); err != nil {
				t.Fatalf("register: %v", err)
			}
			deleted, err := store.DeleteAbandonedCheckpoint(ctx, taskID, time.Now())
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			if !deleted {
				t.Fatal("a task whose owner is gone and whose call can never be answered was left in place — every tick from now on logs the same give-up")
			}
			if cp, _ := store.GetCheckpoint(ctx, taskID); cp != nil {
				t.Error("the checkpoint survived, so it is invisible to the reaper and to the sweep forever")
			}
			if row, _ := store.LookupPendingCall(ctx, "app", "corr-dead"); row != nil {
				t.Error("the correlation survived its checkpoint")
			}
		})
	})
}

func TestRegisterPendingCallsReplacesTheTasksSet(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
			AgentName: "app", PendingCount: 1,
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
			CorrID: "corr-turn-1", TaskID: taskID, AgentName: "app",
			ToolCallID: "tc-a", Kind: PendingKindPeer,
		}}); err != nil {
			t.Fatalf("register turn 1: %v", err)
		}

		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
			CorrID: "corr-turn-2", TaskID: taskID, AgentName: "app",
			ToolCallID: "tc-b", Kind: PendingKindPeer,
		}}); err != nil {
			t.Fatalf("register turn 2: %v", err)
		}

		if row, _ := store.LookupPendingCall(ctx, "app", "corr-turn-1"); row != nil {
			t.Error("a correlation row from the previous turn survived the next pause — it still reads as an outstanding call, so a late reply for it decrements this turn's count and the turn resumes without its answer")
		}
		if row, _ := store.LookupPendingCall(ctx, "app", "corr-turn-2"); row == nil {
			t.Fatal("this turn's own correlation row is missing")
		}

		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), nil); err != nil {
			t.Fatalf("register empty: %v", err)
		}
		if row, _ := store.LookupPendingCall(ctx, "app", "corr-turn-2"); row != nil {
			t.Error("an empty registration left the previous turn's rows in place, so a pause with no correlatable calls inherits the last pause's outstanding-ness")
		}
	})
}
