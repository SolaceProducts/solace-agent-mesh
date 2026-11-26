// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func TestEvalRunScoreRepo_ErroredRoundTrips(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	dID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "ErroredDS")
	example := &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "p"}
	if err := NewEvalExampleRepo(db).Create(ctx, example); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &EvalExperiment{Name: "ErroredExp", DatasetID: &dID, TargetAgent: "agent", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := NewEvalExperimentRepo(db).Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	evID := newTestEvaluator(ctx, t, NewEvalEvaluatorRepo(db), "ErroredEv", "llm_judge")
	run := &EvalRun{ExperimentID: exp.ID, Model: "m", StartedBy: "u"}
	if err := NewEvalRunRepo(db).Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	results := []*EvalRunResult{
		{RunID: run.ID, ExampleID: example.ID, TrialIndex: 0},
		{RunID: run.ID, ExampleID: example.ID, TrialIndex: 1},
		{RunID: run.ID, ExampleID: example.ID, TrialIndex: 2},
	}
	if err := NewEvalRunResultRepo(db).BulkCreate(ctx, results); err != nil {
		t.Fatalf("create results: %v", err)
	}
	errored, graded := true, false
	scores := NewEvalRunScoreRepo(db)
	if err := scores.Create(ctx, &EvalRunScore{RunResultID: results[0].ID, EvaluatorID: evID, Errored: &errored}); err != nil {
		t.Fatalf("create errored score: %v", err)
	}
	if err := scores.BulkCreate(ctx, []*EvalRunScore{
		{RunResultID: results[1].ID, EvaluatorID: evID, Score: 1, Errored: &graded},
		{RunResultID: results[2].ID, EvaluatorID: evID, Score: 1},
	}); err != nil {
		t.Fatalf("create graded scores: %v", err)
	}

	got, err := scores.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("list scores: %v", err)
	}
	byResult := map[string]*bool{}
	for _, s := range got {
		byResult[s.RunResultID] = s.Errored
	}
	if e := byResult[results[0].ID]; e == nil || !*e {
		t.Errorf("errored score reads %v, want true", e)
	}
	if e := byResult[results[1].ID]; e == nil || *e {
		t.Errorf("graded score reads %v, want false", e)
	}
	if e := byResult[results[2].ID]; e != nil {
		t.Errorf("score written without the flag reads %v, want nil", *e)
	}
}

func TestEvalRunRepo_UpdateProgressClearsTakeovers(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	dID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "TakeoverDS")
	exp := &EvalExperiment{Name: "TakeoverExp", DatasetID: &dID, TargetAgent: "agent", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := NewEvalExperimentRepo(db).Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	runs := NewEvalRunRepo(db)
	run := &EvalRun{ExperimentID: exp.ID, Model: "m", StartedBy: "u"}
	if err := runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE eval_runs SET takeovers = 2, progress_current = 3 WHERE id = ?`, run.ID); err != nil {
		t.Fatalf("seed takeovers: %v", err)
	}

	if err := runs.UpdateProgress(ctx, run.ID, 3); err != nil {
		t.Fatalf("update progress without progress: %v", err)
	}
	if got := mustGetRun(t, runs, run.ID); got.Takeovers != 2 {
		t.Errorf("takeovers after no new trial = %d, want 2", got.Takeovers)
	}

	if err := runs.UpdateProgress(ctx, run.ID, 4); err != nil {
		t.Fatalf("update progress: %v", err)
	}
	if got := mustGetRun(t, runs, run.ID); got.Takeovers != 0 || got.ProgressCurrent != 4 {
		t.Errorf("after a saved trial takeovers = %d, progress = %d, want 0 and 4", got.Takeovers, got.ProgressCurrent)
	}
}

func mustGetRun(t *testing.T, runs *EvalRunRepo, id string) *EvalRun {
	t.Helper()
	got, err := runs.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	return got
}

func TestEvalGenerationOperationRepo_RequestRoundTrips(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewEvalGenerationOperationRepo(db)
	datasetID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "RequestDS")
	request := `{"agent":"a","count":3}`
	if err := repo.Create(ctx, &EvalGenerationOperation{
		ID: genOpID(1), OperationType: EvalGenerationExamples, DatasetID: &datasetID, Status: EvalGenerationInProgress,
		CreatedBy: "u", CreatedTime: genOpCreatedTime, Deadline: genOpDeadline, Request: &request,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := getGenerationOperation(ctx, t, repo, genOpID(1))
	if got.Request == nil || *got.Request != request {
		t.Errorf("request = %v, want %q", got.Request, request)
	}
	if got.Takeovers != 0 {
		t.Errorf("takeovers = %d, want 0", got.Takeovers)
	}
}

func markStaleRunsFailed(ctx context.Context, r *EvalRunRepo, budgetSeconds float64) ([]string, error) {
	stale, err := r.ListStaleRunIDs(ctx, budgetSeconds)
	if err != nil {
		return nil, err
	}
	var finalized []string
	for _, runID := range stale {
		updated, ferr := r.FinalizeStaleRun(ctx, runID, "Run exceeded maximum duration.")
		if ferr != nil {
			return finalized, ferr
		}
		if updated {
			finalized = append(finalized, runID)
		}
	}
	return finalized, nil
}
