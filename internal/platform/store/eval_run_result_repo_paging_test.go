// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"fmt"
	"slices"
	"testing"
)

func TestEvalRunResultRepo_ListByRunPaginated(t *testing.T) {
	db := TestDB(t)
	exRepo := NewEvalExampleRepo(db)
	resRepo := NewEvalRunResultRepo(db)
	scoreRepo := NewEvalRunScoreRepo(db)
	ctx := t.Context()

	datasetID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "PagedResultsDataset")
	evaluatorID := newTestEvaluator(ctx, t, NewEvalEvaluatorRepo(db), "paged-results-evaluator", "heuristic")
	examples := make([]*EvalExample, 3)
	for i := range examples {
		examples[i] = &EvalExample{DatasetID: datasetID, SequenceNumber: i + 1, Prompt: fmt.Sprintf("Prompt %d", i+1)}
		if err := exRepo.Create(ctx, examples[i]); err != nil {
			t.Fatalf("create example %d: %v", i+1, err)
		}
	}
	exp := &EvalExperiment{Name: "PagedResultsExp", DatasetID: &datasetID, TargetAgent: "agent", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := NewEvalExperimentRepo(db).Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	runRepo := NewEvalRunRepo(db)
	run := &EvalRun{ExperimentID: exp.ID, Model: "m1", StartedBy: "u"}
	otherRun := &EvalRun{ExperimentID: exp.ID, Model: "m2", StartedBy: "u"}
	for _, r := range []*EvalRun{run, otherRun} {
		if err := runRepo.Create(ctx, r); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}

	type trial struct{ example, index int }
	insertionOrder := []trial{{2, 0}, {0, 1}, {1, 0}, {0, 0}, {1, 1}}
	for _, tr := range insertionOrder {
		res := &EvalRunResult{RunID: run.ID, ExampleID: examples[tr.example].ID, TrialIndex: tr.index}
		if err := resRepo.Create(ctx, res); err != nil {
			t.Fatalf("create result: %v", err)
		}
		if tr == (trial{0, 0}) {
			score := &EvalRunScore{RunResultID: res.ID, EvaluatorID: evaluatorID, Score: 1}
			if err := scoreRepo.Create(ctx, score); err != nil {
				t.Fatalf("create score: %v", err)
			}
		}
	}
	if err := resRepo.Create(ctx, &EvalRunResult{RunID: otherRun.ID, ExampleID: examples[0].ID}); err != nil {
		t.Fatalf("create other run's result: %v", err)
	}

	key := func(r *EvalRunResultWithScores) string {
		for i, e := range examples {
			if e.ID == r.ExampleID {
				return fmt.Sprintf("%d/%d", i, r.TrialIndex)
			}
		}
		return "unknown"
	}
	page := func(number, size int) ([]string, int) {
		t.Helper()
		rows, total, err := resRepo.ListByRunPaginated(ctx, run.ID, number, size)
		if err != nil {
			t.Fatalf("ListByRunPaginated(%d, %d): %v", number, size, err)
		}
		keys := make([]string, len(rows))
		for i, r := range rows {
			keys[i] = key(r)
		}
		return keys, total
	}

	if got, total := page(1, 2); !slices.Equal(got, []string{"0/0", "0/1"}) || total != 5 {
		t.Errorf("page 1 of size 2 = %v of %d, want [0/0 0/1] of 5", got, total)
	}
	if got, total := page(2, 2); !slices.Equal(got, []string{"1/0", "1/1"}) || total != 5 {
		t.Errorf("page 2 of size 2 = %v of %d, want [1/0 1/1] of 5", got, total)
	}
	if got, total := page(3, 2); !slices.Equal(got, []string{"2/0"}) || total != 5 {
		t.Errorf("page 3 of size 2 = %v of %d, want [2/0] of 5", got, total)
	}
	if got, total := page(9, 2); len(got) != 0 || total != 5 {
		t.Errorf("a page past the end = %v of %d, want none of 5", got, total)
	}

	rows, _, err := resRepo.ListByRunPaginated(ctx, run.ID, 1, 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("first row: %v, %d rows", err, len(rows))
	}
	if len(rows[0].Scores) != 1 || rows[0].Scores[0].EvaluatorID != evaluatorID {
		t.Errorf("the first result's scores = %+v, want the one score", rows[0].Scores)
	}

	if rows, total, err := resRepo.ListByRunPaginated(ctx, "00000000-0000-0000-0000-000000000000", 1, 10); err != nil || len(rows) != 0 || total != 0 {
		t.Errorf("an unknown run = %d rows of %d, %v, want none", len(rows), total, err)
	}
}
