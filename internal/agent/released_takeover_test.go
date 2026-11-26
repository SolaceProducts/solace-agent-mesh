// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func resolvedCheckpoint(t *testing.T, store session.Store, owner *Agent) string {
	t.Helper()
	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(t.Context(), state); err != nil {
		t.Fatalf("save: %v", err)
	}
	owner.checkpoints.recordResult(t.Context(), taskID, state.PendingCalls["tc-1"], tool.OK("the peer answered"))
	return taskID
}

func withinOwnTakeoverWindow(c *taskCheckpointer) {
	c.healthMu.Lock()
	c.dbAnsweringSince = time.Time{}
	c.healthMu.Unlock()
}

func TestNewProcessTakesAReleasedTaskWithoutWaiting(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "old")
	taker, takerLLM, takerBrk := replicaOf(t, store, "new")
	withinOwnTakeoverWindow(taker.checkpoints)

	taskID := resolvedCheckpoint(t, store, owner)
	ownerDies(t, store, taskID)

	taker.resumeAbandonedCheckpoints(t.Context(), time.Now())

	assertResumedToCompletion(t, taker, takerLLM, takerBrk, taskID, "the peer answered")
}

func TestNewProcessWaitsBeforeJudgingAnOwnerSilent(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "old")
	taker, _, _ := replicaOf(t, store, "new")
	withinOwnTakeoverWindow(taker.checkpoints)
	taker.checkpoints.takeoverAfter = time.Nanosecond

	taskID := resolvedCheckpoint(t, store, owner)
	time.Sleep(time.Millisecond)

	taker.resumeAbandonedCheckpoints(t.Context(), time.Now())

	if got, _, _ := store.CheckpointOwner(t.Context(), taskID); got != owner.checkpoints.ownerID {
		t.Errorf("owner = %q, want the row left to %q until the new process's own window has passed", got, owner.checkpoints.ownerID)
	}
}

func TestAnArrivingReplyTakesAReleasedTaskWithoutWaiting(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "old")
	taker, takerLLM, takerBrk := replicaOf(t, store, "new")
	withinOwnTakeoverWindow(taker.checkpoints)

	taskID := resolvedCheckpoint(t, store, owner)
	ownerDies(t, store, taskID)

	taker.adoptIfResolved(t.Context(), taskID, taker.logger)

	assertResumedToCompletion(t, taker, takerLLM, takerBrk, taskID, "the peer answered")
}

func TestReleasedOnlyAdoptionTakesOnlyRowsNobodyOwns(t *testing.T) {
	store, db := newSQLiteCheckpointStore(t)
	ctx := t.Context()
	save := func(taskID, owner string) {
		if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
			TaskID: taskID, AppName: "a", AgentName: "a", UserID: "u", SessionID: idgen.New(),
			State: []byte(`{"version":2}`), OwnerID: owner,
		}); err != nil {
			t.Fatalf("save %s: %v", taskID, err)
		}
	}
	save("silent", "old-owner")
	silenceOwner(t, db, "silent", time.Hour)
	save("released", "old-owner")
	if err := store.ReleaseOwnership(ctx, "released", "old-owner"); err != nil {
		t.Fatalf("release: %v", err)
	}

	list, err := store.ListResumableCheckpoints(ctx, "a", time.Now(), checkpointTakeoverAfter, 10, true)
	if err != nil || len(list) != 1 || list[0].TaskID != "released" {
		t.Fatalf("released-only list = %v (err %v), want only the released row", list, err)
	}
	if cp, err := store.AdoptCheckpoint(ctx, "silent", "taker", time.Now(), checkpointLease, checkpointTakeoverAfter, true); err != nil || cp != nil {
		t.Errorf("released-only adoption took a row whose owner only looks silent: %v (err %v)", cp, err)
	}
	if cp, err := store.AdoptCheckpoint(ctx, "released", "taker", time.Now(), checkpointLease, checkpointTakeoverAfter, true); err != nil || cp == nil {
		t.Errorf("released-only adoption left the released row: err %v", err)
	}
	if cp, err := store.AdoptCheckpoint(ctx, "silent", "taker", time.Now(), checkpointLease, checkpointTakeoverAfter, false); err != nil || cp == nil {
		t.Errorf("a taker judging owners did not take the silent row: err %v", err)
	}
}
