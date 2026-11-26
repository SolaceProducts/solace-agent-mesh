// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"testing"
	"time"

	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func TestPurgeRemovesDayOldResolvedPausedRowsInBatches(t *testing.T) {
	w, _ := newHILInstance(t)
	ctx := t.Context()
	insert := func(id string) {
		if err := w.pausedRepo.Insert(ctx, wfstore.PausedWorkflow{RequestID: id, ExecutionID: "e", TaskID: "task", WorkflowName: "wf", NodeID: "n"}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	resolve := func(id string) {
		if _, ok, err := w.pausedRepo.Claim(ctx, id, "resumed"); err != nil || !ok {
			t.Fatalf("claim %s: ok=%v err=%v", id, ok, err)
		}
	}
	for i := range resolvedPausedPurgeBatch + 3 {
		id := fmt.Sprintf("old-%d", i)
		insert(id)
		resolve(id)
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour).UnixMilli()
	if _, err := w.pausedDB.ExecContext(ctx, `UPDATE workflow_paused SET resolved_at = ?`, twoDaysAgo); err != nil {
		t.Fatal(err)
	}
	insert("recent")
	resolve("recent")
	insert("waiting")

	w.purgeResolvedPaused(ctx)

	for _, id := range []string{"old-0", fmt.Sprintf("old-%d", resolvedPausedPurgeBatch+2)} {
		if row, _ := w.pausedRepo.Get(ctx, id); row != nil {
			t.Fatalf("%s answered two days ago was kept", id)
		}
	}
	for _, id := range []string{"recent", "waiting"} {
		if row, err := w.pausedRepo.Get(ctx, id); err != nil || row == nil {
			t.Fatalf("%s was purged: err=%v", id, err)
		}
	}
}
