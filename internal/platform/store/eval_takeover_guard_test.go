// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import "testing"

func TestEvalRunRepo_TakeoverCleanupLeavesACancelledRunAlone(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	runs := NewEvalRunRepo(db)
	for _, status := range []string{"running", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			seed := seedEvalRun(t, db, status)
			if err := NewEvalRunResultRepo(db).Create(ctx, &EvalRunResult{RunID: seed.runID, ExampleID: seed.exampleID}); err != nil {
				t.Fatal(err)
			}
			if err := runs.UpdateProgress(ctx, seed.runID, 5); err != nil {
				t.Fatal(err)
			}
			deleted, err := runs.DeleteHalfSavedResults(ctx, seed.runID, []string{seed.evaluatorID})
			if err != nil {
				t.Fatal(err)
			}
			if err = runs.ResetProgress(ctx, seed.runID, 0); err != nil {
				t.Fatal(err)
			}
			run, err := runs.GetByID(ctx, seed.runID)
			if err != nil {
				t.Fatal(err)
			}
			wantDeleted, wantProgress := 1, 0
			if status == "cancelled" {
				wantDeleted, wantProgress = 0, 5
			}
			if deleted != wantDeleted || run.ProgressCurrent != wantProgress {
				t.Errorf("deleted %d, progress %d; want %d and %d", deleted, run.ProgressCurrent, wantDeleted, wantProgress)
			}
		})
	}
}
