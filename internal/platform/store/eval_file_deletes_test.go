// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type evalRunSeed struct {
	runID, exampleID, evaluatorID, datasetID, experimentID string
}

func seedEvalRun(t *testing.T, db *DB, status string) evalRunSeed {
	t.Helper()
	ctx := t.Context()
	dID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "files-"+status)
	example := &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "p"}
	if err := NewEvalExampleRepo(db).Create(ctx, example); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &EvalExperiment{Name: "files-" + status, DatasetID: &dID, TargetAgent: "agent", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := NewEvalExperimentRepo(db).Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &EvalRun{ExperimentID: exp.ID, Model: "m", StartedBy: "u", Status: status}
	if err := NewEvalRunRepo(db).Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return evalRunSeed{
		runID: run.ID, exampleID: example.ID, datasetID: dID, experimentID: exp.ID,
		evaluatorID: newTestEvaluator(ctx, t, NewEvalEvaluatorRepo(db), "ev-"+status, "llm_judge"),
	}
}

func recordInto(got *EvalFileDeletes) EvalFileDeleteEvent {
	return func(d EvalFileDeletes) (*OutboxEvent, error) {
		*got = d
		return &OutboxEvent{EventType: "eval.files.delete", AggregateID: "00000000-0000-4000-8000-000000000001", Payload: "{}"}, nil
	}
}

func outboxCount(t *testing.T, db *DB) int {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM outbox_events WHERE event_type = 'eval.files.delete'`)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEvalRunRepo_DeleteRefusesAnActiveRunAndRecordsItsFiles(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	seed := seedEvalRun(t, db, "running")
	runs := NewEvalRunRepo(db)
	meta := `[{"filename":"a.md","version":0}]`
	if err := NewEvalRunResultRepo(db).Create(ctx, &EvalRunResult{RunID: seed.runID, ExampleID: seed.exampleID, ArtifactsMetadata: &meta}); err != nil {
		t.Fatal(err)
	}

	var got EvalFileDeletes
	if err := runs.Delete(ctx, seed.runID, recordInto(&got)); !errors.Is(err, ErrEvalRunActive) {
		t.Fatalf("delete of a running run = %v, want ErrEvalRunActive", err)
	}
	if _, err := runs.UpdateStatus(ctx, seed.runID, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := runs.Delete(ctx, seed.runID, recordInto(&got)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(got.RunIDs) != 1 || got.RunIDs[0] != seed.runID || len(got.TrialSnapshots) != 1 {
		t.Errorf("recorded %+v, want the run and its one trial snapshot", got)
	}
	if outboxCount(t, db) != 1 {
		t.Error("the delete wrote no outbox event")
	}
}

func TestEvalDeletesRecordTheFilesTheyRemove(t *testing.T) {
	t.Run("experiment", func(t *testing.T) {
		db := TestDB(t)
		seed := seedEvalRun(t, db, "completed")
		var got EvalFileDeletes
		if err := NewEvalExperimentRepo(db).Delete(t.Context(), seed.experimentID, recordInto(&got)); err != nil {
			t.Fatal(err)
		}
		if len(got.RunIDs) != 1 || got.RunIDs[0] != seed.runID {
			t.Errorf("recorded runs %v, want the experiment's run", got.RunIDs)
		}
	})
	t.Run("run group", func(t *testing.T) {
		db := TestDB(t)
		seed := seedEvalRun(t, db, "completed")
		run, err := NewEvalRunRepo(db).GetByID(t.Context(), seed.runID)
		if err != nil {
			t.Fatal(err)
		}
		var got EvalFileDeletes
		if n, err := NewEvalRunRepo(db).DeleteByRunGroup(t.Context(), run.RunGroupID, recordInto(&got)); err != nil || n != 1 {
			t.Fatalf("delete group = %d, %v", n, err)
		}
		if len(got.RunIDs) != 1 {
			t.Errorf("recorded runs %v, want one", got.RunIDs)
		}
	})
	for _, scope := range []string{"dataset", "example"} {
		t.Run(scope, func(t *testing.T) {
			db := TestDB(t)
			ctx := t.Context()
			seed := seedEvalRun(t, db, "completed")
			file := &EvalExampleArtifact{ExampleID: seed.exampleID, Role: EvalArtifactRoleInput, Filename: "in.txt", MIMEType: "text/plain", SessionID: "s1", ContentHash: "h"}
			if err := NewEvalExampleArtifactRepo(db).Upsert(ctx, file, nil); err != nil {
				t.Fatal(err)
			}
			var got EvalFileDeletes
			var err error
			if scope == "dataset" {
				err = NewEvalDatasetRepo(db).Delete(ctx, seed.datasetID, recordInto(&got))
			} else {
				err = NewEvalExampleRepo(db).Delete(ctx, seed.exampleID, recordInto(&got))
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.ExampleFiles) != 1 || got.ExampleFiles[0] != (EvalExampleFile{SessionID: "s1", Filename: "in.txt"}) {
				t.Errorf("recorded files %+v, want the example's file", got.ExampleFiles)
			}
		})
	}
}

func TestEvalExampleArtifactRepo_RecordsReplacedAndClearedFiles(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	seed := seedEvalRun(t, db, "completed")
	rows := NewEvalExampleArtifactRepo(db)
	first := &EvalExampleArtifact{ExampleID: seed.exampleID, Role: EvalArtifactRoleInput, Filename: "a.txt", MIMEType: "text/plain", SessionID: "s1", ContentHash: "h1"}
	var got EvalFileDeletes
	if err := rows.Upsert(ctx, first, recordInto(&got)); err != nil {
		t.Fatal(err)
	}
	if !got.Empty() {
		t.Errorf("a first upload recorded %+v, want nothing", got)
	}
	second := &EvalExampleArtifact{ExampleID: seed.exampleID, Role: EvalArtifactRoleInput, Filename: "a.txt", MIMEType: "text/plain", SessionID: "s2", ContentHash: "h2"}
	if err := rows.Upsert(ctx, second, recordInto(&got)); err != nil {
		t.Fatal(err)
	}
	if len(got.ExampleFiles) != 1 || got.ExampleFiles[0].SessionID != "s1" {
		t.Errorf("a replace recorded %+v, want the replaced file", got.ExampleFiles)
	}
	if referenced, err := rows.SessionReferenced(ctx, "s2"); err != nil || !referenced {
		t.Errorf("SessionReferenced(s2) = %v, %v; want true", referenced, err)
	}
	if referenced, err := rows.SessionReferenced(ctx, "s1"); err != nil || referenced {
		t.Errorf("SessionReferenced(s1) = %v, %v; want false", referenced, err)
	}
	got = EvalFileDeletes{}
	if err := rows.Delete(ctx, seed.exampleID, EvalArtifactRoleInput, recordInto(&got)); err != nil {
		t.Fatal(err)
	}
	if len(got.ExampleFiles) != 1 || got.ExampleFiles[0].SessionID != "s2" {
		t.Errorf("a clear recorded %+v, want the cleared file", got.ExampleFiles)
	}
	if err := rows.RecordFiles(ctx, []EvalExampleFile{{SessionID: "s3", Filename: "b.txt"}}, recordInto(&got)); err != nil {
		t.Fatal(err)
	}
	if outboxCount(t, db) != 3 {
		t.Errorf("outbox events = %d, want 3", outboxCount(t, db))
	}
}

func TestEvalRunResultRepo_CreateWithFiles(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	seed := seedEvalRun(t, db, "running")
	results := NewEvalRunResultRepo(db)
	res := &EvalRunResult{RunID: seed.runID, ExampleID: seed.exampleID}
	if err := results.CreateWithFiles(ctx, res, func(context.Context) {
		key := "k"
		res.ExecutionDataKey = &key
	}); err != nil {
		t.Fatal(err)
	}
	got, err := results.GetByID(ctx, res.ID)
	if err != nil || got.ExecutionDataKey == nil || *got.ExecutionDataKey != "k" {
		t.Errorf("saved row = %+v, %v; want the key the files step recorded", got, err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM eval_runs WHERE id = ?`, seed.runID); err != nil {
		t.Fatal(err)
	}
	wrote := false
	late := &EvalRunResult{RunID: seed.runID, ExampleID: seed.exampleID, TrialIndex: 1}
	if err := results.CreateWithFiles(ctx, late, func(context.Context) { wrote = true }); err == nil {
		t.Error("a trial of a deleted run was saved")
	}
	if wrote {
		t.Error("a trial of a deleted run wrote files")
	}
}

func TestEvalRunRepo_TakeoverFunctions(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	runs := NewEvalRunRepo(db)
	seed := seedEvalRun(t, db, "running")

	if n, ok, err := runs.TakeOver(ctx, seed.runID); err != nil || !ok || n != 1 {
		t.Fatalf("TakeOver = %d, %v, %v; want the first takeover", n, ok, err)
	}
	if n, _, _ := runs.TakeOver(ctx, seed.runID); n != 2 {
		t.Errorf("second TakeOver count = %d, want 2", n)
	}

	results := NewEvalRunResultRepo(db)
	half := &EvalRunResult{RunID: seed.runID, ExampleID: seed.exampleID, TrialIndex: 0}
	errMsg := "agent error"
	failed := &EvalRunResult{RunID: seed.runID, ExampleID: seed.exampleID, TrialIndex: 1, ErrorMessage: &errMsg}
	scored := &EvalRunResult{RunID: seed.runID, ExampleID: seed.exampleID, TrialIndex: 2}
	if err := results.BulkCreate(ctx, []*EvalRunResult{half, failed, scored}); err != nil {
		t.Fatal(err)
	}
	if err := NewEvalRunScoreRepo(db).Create(ctx, &EvalRunScore{RunResultID: scored.ID, EvaluatorID: seed.evaluatorID, Score: 1}); err != nil {
		t.Fatal(err)
	}
	if n, err := runs.DeleteHalfSavedResults(ctx, seed.runID, []string{seed.evaluatorID}); err != nil || n != 1 {
		t.Fatalf("DeleteHalfSavedResults = %d, %v; want only the unscored trial", n, err)
	}
	if err := runs.ResetProgress(ctx, seed.runID, 2); err != nil {
		t.Fatal(err)
	}
	if run, _ := runs.GetByID(ctx, seed.runID); run.ProgressCurrent != 2 {
		t.Errorf("progress = %d, want 2", run.ProgressCurrent)
	}

	if ok, err := runs.FailRunning(ctx, seed.runID, "stopped"); err != nil || !ok {
		t.Fatalf("FailRunning = %v, %v", ok, err)
	}
	if _, ok, _ := runs.TakeOver(ctx, seed.runID); ok {
		t.Error("TakeOver claimed a run that is no longer running")
	}
	if _, cancelled, err := runs.CancelActive(ctx, seed.runID); err != nil || cancelled {
		t.Errorf("CancelActive on a failed run = %v, %v; want it left failed", cancelled, err)
	}

	pending := seedEvalRun(t, db, "pending")
	if ids, err := runs.ListPendingIDsBefore(ctx, time.Now().Add(time.Hour).UnixMilli()); err != nil || len(ids) != 1 || ids[0] != pending.runID {
		t.Errorf("ListPendingIDsBefore = %v, %v; want the pending run", ids, err)
	}
	if row, cancelled, err := runs.CancelActive(ctx, pending.runID); err != nil || !cancelled || row.Status != "cancelled" {
		t.Errorf("CancelActive on a pending run = %+v, %v, %v; want it cancelled", row, cancelled, err)
	}
}
