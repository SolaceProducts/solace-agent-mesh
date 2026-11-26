// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

type testCheckpoint struct {
	TaskID         string
	ExecutionID    string
	WorkflowName   string
	DefinitionHash string
	StateSnapshot  string
	ExecContext    string
	DeadlineAt     *int64
}

func putCheckpoint(t *testing.T, repo *wfstore.RunRepo, row testCheckpoint) {
	t.Helper()
	_, err := repo.WriteStep(t.Context(), row.ExecutionID, wfstore.Holder{Key: wfstore.HeldByRequest}, wfstore.RunState{
		TaskID:         row.TaskID,
		WorkflowName:   row.WorkflowName,
		DefinitionHash: row.DefinitionHash,
		StateSnapshot:  row.StateSnapshot,
		ExecContext:    row.ExecContext,
		DeadlineAt:     row.DeadlineAt,
	})
	if err != nil {
		t.Fatalf("seed stored run: %v", err)
	}
}

func markAnswered(t *testing.T, repo *wfstore.RunRepo, executionID string) error {
	t.Helper()
	run, err := repo.Get(t.Context(), executionID)
	if err != nil || run == nil {
		t.Fatalf("read stored run: %v %v", run, err)
	}
	return repo.MarkAnswered(t.Context(), executionID, run.Holder())
}

func latestRun(ctx context.Context, db *wfstore.DB, taskID string) (*wfstore.WorkflowRun, error) {
	return dbutil.QueryRow[wfstore.WorkflowRun](ctx, db,
		`SELECT execution_id, task_id, workflow_name, definition_hash, state_snapshot,
		        exec_context, deadline_at, status, held_by, open_pauses, created_at,
		        updated_at, answered_at, ended_at
		 FROM workflow_checkpoint WHERE task_id = ? ORDER BY CASE WHEN ended_at IS NULL THEN 0 WHEN answered_at IS NOT NULL THEN 1 ELSE 2 END,
		          created_at DESC, updated_at DESC LIMIT 1`, taskID)
}

func (w *WorkflowInstance) runOfTask(ctx context.Context, taskID string) (*wfstore.WorkflowRun, error) {
	w.pausedMu.RLock()
	db := w.pausedDB
	w.pausedMu.RUnlock()
	return latestRun(ctx, db, taskID)
}

func runAnswered(row *wfstore.WorkflowRun) bool { return row != nil && row.AnsweredAt != nil }
