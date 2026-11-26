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

func TestDeleteAbandonedCheckpointClearsAnOrphanedCorrelation(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)

		past := time.Now().Add(-time.Hour)
		seedOrphanCalls(t, store, []PendingCall{{
			CorrID: "corr-orphan", TaskID: taskID, AgentName: "app",
			Kind: PendingKindPeer, ExpiresAt: &past,
		}})

		deleted, err := store.DeleteAbandonedCheckpoint(ctx, taskID, time.Now())
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if !deleted {
			t.Error("an orphaned correlation with no checkpoint was reported as nothing to do")
		}
		if row, _ := store.LookupPendingCall(ctx, "app", "corr-orphan"); row != nil {
			t.Error("the orphaned correlation survived — it has no checkpoint to resume and is now reaped on every tick forever, which is the outcome the give-up exists to prevent")
		}
	})
}

func TestRecordToolResultGateIsTaskScoped(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		mine, theirs, sessID := idgen.New(), idgen.New(), idgen.New()
		seed("app", "user", sessID)

		for _, id := range []string{mine, theirs} {
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: id, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: "app", PendingCount: 1,
			}); err != nil {
				t.Fatalf("save %s: %v", id, err)
			}
		}
		if err := store.RegisterPendingCalls(ctx, theirs, heldOwner(t, store, theirs), []PendingCall{{
			CorrID: "corr-shared", TaskID: theirs, AgentName: "app",
			ToolCallID: "tc-1", Kind: PendingKindPeer,
		}}); err != nil {
			t.Fatalf("register: %v", err)
		}

		if _, err := store.RecordToolResult(ctx, mine, "corr-shared", &ToolCallResult{
			ToolCallID: "tc-1", Content: "answer",
		}); err == nil {
			t.Error("a result recorded against the wrong task consumed another task's correlation and decremented its own count")
		}
		if row, _ := store.LookupPendingCall(ctx, "app", "corr-shared"); row == nil {
			t.Error("another task's correlation row was consumed by a record against a different task")
		}
		cp, err := store.GetCheckpoint(ctx, mine)
		if err != nil || cp == nil {
			t.Fatalf("read back: %v, %v", cp, err)
		}
		if cp.PendingCount != 1 {
			t.Errorf("pending count = %d, want 1 — a result for a call this task never made drove it toward resuming early", cp.PendingCount)
		}
	})
}

func TestListExpiredPendingCallsIsScopedToItsAgent(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		past := time.Now().Add(-time.Hour)

		for _, agentName := range []string{"mine", "theirs"} {
			taskID, sessID := idgen.New(), idgen.New()
			seed("app", "user", sessID)
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: agentName, PendingCount: 1,
			}); err != nil {
				t.Fatalf("save %s: %v", agentName, err)
			}
			if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
				CorrID: "corr-" + agentName, TaskID: taskID, AgentName: agentName,
				ToolCallID: "tc-1", Kind: PendingKindPeer, ExpiresAt: &past,
			}}); err != nil {
				t.Fatalf("register %s: %v", agentName, err)
			}
		}

		got, err := store.ListExpiredPendingCalls(ctx, "mine", time.Now(), 10)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d expired calls for agent mine, want 1 — an unscoped query hands this reaper another agent's task, which it then resumes with its own tools, model and config", len(got))
		}
		if got[0].AgentName != "mine" {
			t.Errorf("the reaper for mine was handed a call belonging to %q", got[0].AgentName)
		}
	})
}

func TestLookupPendingCallIsScopedToItsAgent(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
			AgentName: "theirs", PendingCount: 1,
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
			CorrID: "corr-theirs", TaskID: taskID, AgentName: "theirs",
			ToolCallID: "tc-1", Kind: PendingKindPeer,
		}}); err != nil {
			t.Fatalf("register: %v", err)
		}

		if row, err := store.LookupPendingCall(ctx, "mine", "corr-theirs"); err != nil || row != nil {
			t.Errorf("agent \"mine\" resolved a correlation belonging to \"theirs\" (%v, %v) — it then records into their checkpoint and adopts their task", row, err)
		}
		if row, err := store.LookupPendingCall(ctx, "theirs", "corr-theirs"); err != nil || row == nil {
			t.Errorf("the owning agent could not resolve its own correlation (%v, %v) — its peer replies are dropped and every call times out", row, err)
		}
	})
}
