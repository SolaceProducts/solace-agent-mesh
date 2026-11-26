// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func newTestDataset(ctx context.Context, t *testing.T, repo *EvalDatasetRepo, name string) string {
	t.Helper()
	d := &EvalDataset{Name: name, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatalf("create dataset %q: %v", name, err)
	}
	return d.ID
}

func newTestEvaluator(ctx context.Context, t *testing.T, repo *EvalEvaluatorRepo, name, evType string) string {
	t.Helper()
	e := &EvalEvaluator{
		Name:      name,
		Type:      evType,
		CreatedBy: "u",
		UpdatedBy: "u",
	}
	if evType == "llm_judge" {
		tmpl := "test template"
		choices := `{"Yes": 1.0, "No": 0.0}`
		e.PromptTemplate = &tmpl
		e.ChoiceScores = &choices
	}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("create evaluator %q: %v", name, err)
	}
	return e.ID
}

func TestEvalDatasetRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalDatasetRepo(db)
	ctx := t.Context()

	desc := "test dataset for unit tests"
	d := &EvalDataset{Name: "Dataset A", Description: &desc, CreatedBy: "alice", UpdatedBy: "alice"}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, err := repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Dataset A" {
		t.Errorf("name = %q, want Dataset A", got.Name)
	}

	wc, err := repo.GetWithCounts(ctx, d.ID)
	if err != nil {
		t.Fatalf("get with counts: %v", err)
	}
	if wc.ExampleCount != 0 || wc.ExperimentCount != 0 {
		t.Errorf("counts = (%d, %d), want (0, 0)", wc.ExampleCount, wc.ExperimentCount)
	}

	newName := "Dataset A Renamed"
	updated, err := repo.Update(ctx, d.ID, &newName, nil, "bob")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != newName {
		t.Errorf("updated name = %q, want %q", updated.Name, newName)
	}
	if updated.UpdatedBy != "bob" {
		t.Errorf("updated_by = %q, want bob", updated.UpdatedBy)
	}

	if err := repo.Delete(ctx, d.ID, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, d.ID); !errors.Is(err, ErrEvalNotFound) {
		t.Errorf("after delete err = %v, want ErrEvalNotFound", err)
	}
	if err := repo.Delete(ctx, d.ID, nil); !errors.Is(err, ErrEvalNotFound) {
		t.Errorf("delete missing = %v, want ErrEvalNotFound", err)
	}
}

func TestEvalDatasetRepo_NameUniqueness(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalDatasetRepo(db)
	ctx := t.Context()

	if err := repo.Create(ctx, &EvalDataset{Name: "Foo", CreatedBy: "u", UpdatedBy: "u"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("rejects_case_insensitive_dup", func(t *testing.T) {
		err := repo.Create(ctx, &EvalDataset{Name: "FOO", CreatedBy: "u", UpdatedBy: "u"})
		if !errors.Is(err, ErrEvalNameTaken) {
			t.Errorf("err = %v, want ErrEvalNameTaken", err)
		}
	})

	t.Run("update_collision_blocks_rename", func(t *testing.T) {
		other := &EvalDataset{Name: "Bar", CreatedBy: "u", UpdatedBy: "u"}
		if err := repo.Create(ctx, other); err != nil {
			t.Fatalf("create other: %v", err)
		}
		newName := "foo"
		_, err := repo.Update(ctx, other.ID, &newName, nil, "u")
		if !errors.Is(err, ErrEvalNameTaken) {
			t.Errorf("err = %v, want ErrEvalNameTaken", err)
		}
	})

	t.Run("name_exists", func(t *testing.T) {
		taken, err := repo.NameExists(ctx, "FoO")
		if err != nil {
			t.Fatalf("name exists: %v", err)
		}
		if !taken {
			t.Error("expected NameExists(\"FoO\") to be true")
		}
	})
}

func TestEvalDatasetRepo_ListPaginatedAndCounts(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	ctx := t.Context()

	ids := map[string]string{}
	for i, name := range []string{"Alpha", "Beta", "Gamma"} {
		d := &EvalDataset{Name: name, CreatedBy: "u", UpdatedBy: "u"}
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		ids[name] = d.ID
		for j := range i {
			ex := &EvalExample{DatasetID: d.ID, SequenceNumber: j + 1, Prompt: "p"}
			if j == 1 {
				input := `{"orderId":"O-1"}`
				ex.InputData = &input
			}
			if err := exRepo.Create(ctx, ex); err != nil {
				t.Fatalf("create example: %v", err)
			}
		}
	}

	page, total, err := repo.ListPaginated(ctx, 1, 10, "", EvalDatasetSortName, "asc")
	if err != nil {
		t.Fatalf("list paginated: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(page) != 3 {
		t.Fatalf("len(page) = %d, want 3", len(page))
	}
	if page[0].Name != "Alpha" || page[2].Name != "Gamma" {
		t.Errorf("sort by name asc: got %s..%s", page[0].Name, page[2].Name)
	}
	if page[0].ExampleCount != 0 || page[1].ExampleCount != 1 || page[2].ExampleCount != 2 {
		t.Errorf("example counts = (%d, %d, %d), want (0, 1, 2)",
			page[0].ExampleCount, page[1].ExampleCount, page[2].ExampleCount)
	}
	if page[0].InputDataExampleCount != 0 || page[1].InputDataExampleCount != 0 || page[2].InputDataExampleCount != 1 {
		t.Errorf("input data example counts = (%d, %d, %d), want (0, 0, 1)",
			page[0].InputDataExampleCount, page[1].InputDataExampleCount, page[2].InputDataExampleCount)
	}

	t.Run("input_data_counts_on_every_read", func(t *testing.T) {
		gamma, err := repo.GetWithCounts(ctx, ids["Gamma"])
		if err != nil {
			t.Fatalf("get with counts: %v", err)
		}
		if gamma.ExampleCount != 2 || gamma.InputDataExampleCount != 1 {
			t.Errorf("GetWithCounts(Gamma) = %d examples, %d with input data; want 2 and 1", gamma.ExampleCount, gamma.InputDataExampleCount)
		}
		all, err := repo.ListWithCounts(ctx)
		if err != nil {
			t.Fatalf("list with counts: %v", err)
		}
		got := map[string]int{}
		for _, d := range all {
			got[d.Name] = d.InputDataExampleCount
		}
		if got["Alpha"] != 0 || got["Beta"] != 0 || got["Gamma"] != 1 {
			t.Errorf("ListWithCounts input data counts = %v, want Gamma 1 and the others 0", got)
		}
	})

	t.Run("name_filter", func(t *testing.T) {
		page, total, err := repo.ListPaginated(ctx, 1, 10, "ALPH", EvalDatasetSortCreatedTime, "desc")
		if err != nil {
			t.Fatalf("filter: %v", err)
		}
		if total != 1 || len(page) != 1 || page[0].Name != "Alpha" {
			t.Errorf("filter result = %+v (total %d), want one Alpha row", page, total)
		}
	})

	t.Run("page_two_offset", func(t *testing.T) {
		p1, _, _ := repo.ListPaginated(ctx, 1, 2, "", EvalDatasetSortName, "asc")
		p2, _, _ := repo.ListPaginated(ctx, 2, 2, "", EvalDatasetSortName, "asc")
		if len(p1) != 2 || len(p2) != 1 {
			t.Errorf("page sizes = %d / %d, want 2 / 1", len(p1), len(p2))
		}
	})
}

func TestEvalExampleRepo_CRUDAndCascade(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "Dataset")

	seq, err := exRepo.NextSequenceNumber(ctx, dID)
	if err != nil {
		t.Fatalf("next seq empty: %v", err)
	}
	if seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}

	resp := "expected"
	ex := &EvalExample{DatasetID: dID, SequenceNumber: seq, Prompt: "p1", ExpectedResponse: &resp}
	if cErr := exRepo.Create(ctx, ex); cErr != nil {
		t.Fatalf("create: %v", cErr)
	}

	seq2, err := exRepo.NextSequenceNumber(ctx, dID)
	if err != nil {
		t.Fatalf("next seq after one: %v", err)
	}
	if seq2 != 2 {
		t.Errorf("next seq = %d, want 2", seq2)
	}

	if bErr := exRepo.BulkCreate(ctx, []*EvalExample{
		{DatasetID: dID, SequenceNumber: 2, Prompt: "p2"},
		{DatasetID: dID, SequenceNumber: 3, Prompt: "p3"},
	}); bErr != nil {
		t.Fatalf("bulk create: %v", bErr)
	}

	all, err := exRepo.ListByDataset(ctx, dID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("list len = %d, want 3", len(all))
	}

	t.Run("paginated_with_filter", func(t *testing.T) {
		page, total, err := exRepo.ListByDatasetPaginated(ctx, dID, 1, 10, "p2", "", false)
		if err != nil {
			t.Fatalf("paginated: %v", err)
		}
		if total != 1 || len(page) != 1 || page[0].Prompt != "p2" {
			t.Errorf("filtered = %+v (total %d), want single p2 match", page, total)
		}
	})

	t.Run("update_partial", func(t *testing.T) {
		nP := "p1-updated"
		updated, err := exRepo.Update(ctx, ex.ID, &nP, nil, false)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.Prompt != nP {
			t.Errorf("prompt = %q, want %q", updated.Prompt, nP)
		}
		if updated.ExpectedResponse == nil || *updated.ExpectedResponse != "expected" {
			t.Errorf("expected_response = %v, want unchanged 'expected'", updated.ExpectedResponse)
		}
	})

	t.Run("unique_sequence_violation", func(t *testing.T) {
		err := exRepo.Create(ctx, &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "dup"})
		if err == nil {
			t.Fatal("expected unique constraint violation on duplicate (dataset_id, sequence_number)")
		}
	})

	t.Run("cascade_on_dataset_delete", func(t *testing.T) {
		if err := dRepo.Delete(ctx, dID, nil); err != nil {
			t.Fatalf("delete dataset: %v", err)
		}
		remaining, err := exRepo.ListByDataset(ctx, dID)
		if err != nil {
			t.Fatalf("list after cascade: %v", err)
		}
		if len(remaining) != 0 {
			t.Errorf("after cascade, examples = %d, want 0", len(remaining))
		}
	})
}

func TestEvalEvaluatorRepo_CRUDAndCheckConstraint(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalEvaluatorRepo(db)
	ctx := t.Context()

	t.Run("create_heuristic_no_template", func(t *testing.T) {
		ht := "valid_json"
		e := &EvalEvaluator{Name: "VJ", Type: "heuristic", HeuristicType: &ht, CreatedBy: "u", UpdatedBy: "u"}
		if err := repo.Create(ctx, e); err != nil {
			t.Fatalf("create heuristic: %v", err)
		}
	})

	t.Run("create_llm_judge_missing_template_violates_check", func(t *testing.T) {
		e := &EvalEvaluator{Name: "Bad", Type: "llm_judge", CreatedBy: "u", UpdatedBy: "u"}
		err := repo.Create(ctx, e)
		if err == nil {
			t.Fatal("expected CHECK constraint to reject llm_judge without prompt_template/choice_scores")
		}
	})

	t.Run("create_llm_judge_with_template", func(t *testing.T) {
		_ = newTestEvaluator(ctx, t, repo, "LJ", "llm_judge")
	})

	t.Run("update_clears_optional_fields", func(t *testing.T) {
		id := newTestEvaluator(ctx, t, repo, "LJ2", "llm_judge")
		newName := "LJ2-renamed"
		desc := "new desc"
		got, err := repo.Update(ctx, id, EvalEvaluatorUpdate{Name: &newName, Description: &desc, UpdatedBy: "bob"})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if got.Name != newName {
			t.Errorf("name = %q, want %q", got.Name, newName)
		}
		if got.Description == nil || *got.Description != desc {
			t.Errorf("description = %v, want %q", got.Description, desc)
		}
	})

	t.Run("get_by_ids", func(t *testing.T) {
		ht := "valid_json"
		e := &EvalEvaluator{Name: "VJ-batch", Type: "heuristic", HeuristicType: &ht, CreatedBy: "u", UpdatedBy: "u"}
		if err := repo.Create(ctx, e); err != nil {
			t.Fatalf("create: %v", err)
		}
		got, err := repo.GetByIDs(ctx, []string{e.ID, "ffffffff-ffff-ffff-ffff-ffffffffffff"})
		if err != nil {
			t.Fatalf("get by ids: %v", err)
		}
		if len(got) != 1 || got[0].ID != e.ID {
			t.Errorf("got %v, want only the existing evaluator", got)
		}
	})

	t.Run("list_with_counts", func(t *testing.T) {
		got, err := repo.ListWithCounts(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("expected at least one evaluator")
		}
		for _, e := range got {
			if e.ExperimentCount != 0 {
				t.Errorf("evaluator %s count = %d, want 0", e.Name, e.ExperimentCount)
			}
		}
	})
}

func TestEvalExperimentRepo_CRUDAndJunction(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	evRepo := NewEvalEvaluatorRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "Exp Dataset")
	ev1 := newTestEvaluator(ctx, t, evRepo, "Ev1", "llm_judge")
	ev2 := newTestEvaluator(ctx, t, evRepo, "Ev2", "llm_judge")

	exp := &EvalExperiment{
		Name:           "Exp1",
		DatasetID:      &dID,
		TargetAgent:    "agent-a",
		Models:         `[{"model": "m1"}]`,
		RunsPerExample: 2,
		MaxWorkers:     8,
		CreatedBy:      "u",
		UpdatedBy:      "u",
	}
	if err := expRepo.Create(ctx, exp, []string{ev1, ev2}); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("get_enriched_has_dataset_name_and_evaluators", func(t *testing.T) {
		enriched, err := expRepo.GetEnriched(ctx, exp.ID)
		if err != nil {
			t.Fatalf("get enriched: %v", err)
		}
		if enriched.DatasetName == nil || *enriched.DatasetName != "Exp Dataset" {
			t.Errorf("dataset_name = %v, want Exp Dataset", enriched.DatasetName)
		}
		if len(enriched.EvaluatorIDs) != 2 {
			t.Errorf("evaluator ids = %v, want 2", enriched.EvaluatorIDs)
		}
		if enriched.LatestRunStatus != nil {
			t.Errorf("latest run status = %v, want nil (no runs yet)", enriched.LatestRunStatus)
		}
	})

	t.Run("evaluator_count_reflects_attachment", func(t *testing.T) {
		evs, err := evRepo.ListWithCounts(ctx)
		if err != nil {
			t.Fatalf("list evaluators: %v", err)
		}
		var cnt int
		for _, e := range evs {
			if e.ID == ev1 || e.ID == ev2 {
				if e.ExperimentCount != 1 {
					t.Errorf("evaluator %s experiment_count = %d, want 1", e.Name, e.ExperimentCount)
				}
				cnt++
			}
		}
		if cnt != 2 {
			t.Errorf("matched evaluators = %d, want 2", cnt)
		}
	})

	t.Run("attached_evaluator_delete_cascades_junction", func(t *testing.T) {
		idsBefore, err := expRepo.GetEvaluatorIDs(ctx, exp.ID)
		if err != nil {
			t.Fatalf("get evaluator ids: %v", err)
		}
		if len(idsBefore) != 2 {
			t.Fatalf("junction rows before delete = %d, want 2", len(idsBefore))
		}
		if err = evRepo.Delete(ctx, ev2); err != nil {
			t.Fatalf("delete attached evaluator: %v", err)
		}
		idsAfter, err := expRepo.GetEvaluatorIDs(ctx, exp.ID)
		if err != nil {
			t.Fatalf("get evaluator ids: %v", err)
		}
		if len(idsAfter) != 1 || idsAfter[0] != ev1 {
			t.Errorf("junction rows after delete = %v, want [%s]", idsAfter, ev1)
		}
	})

	t.Run("list_with_filters", func(t *testing.T) {
		got, err := expRepo.List(ctx, dID, "", "")
		if err != nil {
			t.Fatalf("list dataset_id: %v", err)
		}
		if len(got) != 1 || got[0].ID != exp.ID {
			t.Errorf("dataset_id filter returned %d rows; want 1", len(got))
		}
		got, err = expRepo.List(ctx, "", ev1, "")
		if err != nil {
			t.Fatalf("list evaluator_id: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("evaluator_id filter returned %d rows; want 1", len(got))
		}
		got, err = expRepo.List(ctx, "", "", "no-such-agent")
		if err != nil {
			t.Fatalf("list target_agent: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("target_agent miss returned %d rows; want 0", len(got))
		}
	})

	t.Run("update_partial", func(t *testing.T) {
		newName := "Exp1-updated"
		newRuns := 5
		updated, err := expRepo.Update(ctx, exp.ID, EvalExperimentUpdate{Name: &newName, RunsPerExample: &newRuns, UpdatedBy: "bob"})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.Name != newName || updated.RunsPerExample != newRuns {
			t.Errorf("update result = %+v", updated)
		}
		if updated.MaxWorkers != 8 {
			t.Errorf("max_workers = %d, want unchanged 8", updated.MaxWorkers)
		}
	})

	t.Run("delete_experiment_cascades_junction", func(t *testing.T) {
		if err := expRepo.Delete(ctx, exp.ID, nil); err != nil {
			t.Fatalf("delete experiment: %v", err)
		}
		ids, err := expRepo.GetEvaluatorIDs(ctx, exp.ID)
		if err != nil {
			t.Fatalf("get evaluator ids: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("after experiment delete, junction rows = %d, want 0", len(ids))
		}
		if err := evRepo.Delete(ctx, ev1); err != nil {
			t.Errorf("delete evaluator post-detach: %v", err)
		}
	})
}

func TestEvalRunRepo_StatusTransitionsAndStaleSweep(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "Run Dataset")
	exp := &EvalExperiment{
		Name: "RunExp", DatasetID: &dID, TargetAgent: "agent-z", Models: "[]",
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}

	run := &EvalRun{ExperimentID: exp.ID, Model: "m1", StartedBy: "u"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.Status != "pending" {
		t.Errorf("status = %q, want pending", run.Status)
	}

	t.Run("set_running_first_caller_wins", func(t *testing.T) {
		ok, err := runRepo.SetRunning(ctx, run.ID, 10)
		if err != nil {
			t.Fatalf("set running: %v", err)
		}
		if !ok {
			t.Fatal("first SetRunning should win")
		}
		ok, err = runRepo.SetRunning(ctx, run.ID, 10)
		if err != nil {
			t.Fatalf("set running 2: %v", err)
		}
		if ok {
			t.Fatal("second SetRunning must lose")
		}
	})

	t.Run("has_active_runs_true", func(t *testing.T) {
		active, err := runRepo.HasActiveRuns(ctx, exp.ID)
		if err != nil {
			t.Fatalf("has active: %v", err)
		}
		if !active {
			t.Error("expected has_active_runs to be true while running")
		}
	})

	t.Run("has_active_runs_for_dataset", func(t *testing.T) {
		active, err := runRepo.HasActiveRunsForDataset(ctx, dID)
		if err != nil {
			t.Fatalf("has active dataset: %v", err)
		}
		if !active {
			t.Error("expected has_active_runs_for_dataset to be true")
		}
	})

	t.Run("update_status_terminal_sets_completed_time", func(t *testing.T) {
		updated, err := runRepo.UpdateStatus(ctx, run.ID, "completed")
		if err != nil {
			t.Fatalf("update status: %v", err)
		}
		if updated.CompletedTime == nil {
			t.Fatal("completed_time must be set on terminal status")
		}
	})

	t.Run("update_summary_scores_respects_cancelled", func(t *testing.T) {
		run2 := &EvalRun{ExperimentID: exp.ID, Model: "m2", StartedBy: "u"}
		if err := runRepo.Create(ctx, run2); err != nil {
			t.Fatalf("create run2: %v", err)
		}
		if _, err := runRepo.UpdateStatus(ctx, run2.ID, "cancelled"); err != nil {
			t.Fatalf("set cancelled: %v", err)
		}
		got, err := runRepo.UpdateSummaryScores(ctx, run2.ID, `{"avg":1.0}`, "completed", nil)
		if err != nil {
			t.Fatalf("summary scores: %v", err)
		}
		if got.Status != "cancelled" {
			t.Errorf("status = %q, want cancelled", got.Status)
		}
	})

	t.Run("update_summary_scores_respects_a_cancel_landing_after_the_read", func(t *testing.T) {
		run3 := &EvalRun{ExperimentID: exp.ID, Model: "m3", StartedBy: "u"}
		if err := runRepo.Create(ctx, run3); err != nil {
			t.Fatalf("create run3: %v", err)
		}
		if _, err := runRepo.UpdateStatus(ctx, run3.ID, "running"); err != nil {
			t.Fatalf("set running: %v", err)
		}

		cancelRepo := NewEvalRunRepo(db)
		cancelRepo.beforeSummaryScoresWrite = func(id string) {
			if _, err := runRepo.UpdateStatus(ctx, id, "cancelled"); err != nil {
				t.Errorf("concurrent cancel: %v", err)
			}
		}

		got, err := cancelRepo.UpdateSummaryScores(ctx, run3.ID, `{"avg":1.0}`, "completed", nil)
		if err != nil {
			t.Fatalf("summary scores: %v", err)
		}
		if got.Status != "cancelled" {
			t.Errorf("status = %q, want cancelled (a cancel between the read and the write must still win)", got.Status)
		}
	})

	t.Run("mark_stale_runs_failed", func(t *testing.T) {
		stale := &EvalRun{ExperimentID: exp.ID, Model: "m-stale", StartedBy: "u"}
		if err := runRepo.Create(ctx, stale); err != nil {
			t.Fatalf("create stale: %v", err)
		}
		oneHourAgo := stale.StartedTime - 3_600_000
		if _, err := db.ExecContext(ctx,
			`UPDATE eval_runs SET status = 'running', progress_total = 1, coordinator_started_at = ? WHERE id = ?`,
			oneHourAgo, stale.ID); err != nil {
			t.Fatalf("force running: %v", err)
		}
		ids, err := markStaleRunsFailed(ctx, runRepo, 1)
		if err != nil {
			t.Fatalf("mark stale: %v", err)
		}
		if len(ids) != 1 || ids[0] != stale.ID {
			t.Errorf("stale ids = %v, want [%s]", ids, stale.ID)
		}
		got, err := runRepo.GetByID(ctx, stale.ID)
		if err != nil {
			t.Fatalf("get stale: %v", err)
		}
		if got.Status != "failed" {
			t.Errorf("stale status = %q, want failed", got.Status)
		}
	})

	t.Run("list_distinct_target_agents", func(t *testing.T) {
		agents, err := runRepo.ListDistinctTargetAgents(ctx)
		if err != nil {
			t.Fatalf("agents: %v", err)
		}
		found := false
		for _, a := range agents {
			if a == "agent-z" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected agent-z in distinct list; got %v", agents)
		}
	})
}

func TestEvalRunRepo_DeriveAndFinalize(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	resRepo := NewEvalRunResultRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "DeriveDataset")
	example := &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "p"}
	if err := exRepo.Create(ctx, example); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &EvalExperiment{Name: "DeriveExp", DatasetID: &dID, TargetAgent: "agent",
		Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}

	makeRun := func(trial int, withResults func(runID string)) (string, *EvalRun) {
		run := &EvalRun{ExperimentID: exp.ID, Model: fmt.Sprintf("m%d", trial), StartedBy: "u"}
		if err := runRepo.Create(ctx, run); err != nil {
			t.Fatalf("create run: %v", err)
		}
		oneHourAgo := run.StartedTime - 3_600_000
		if _, err := db.ExecContext(ctx,
			`UPDATE eval_runs SET status = 'running', progress_total = 2, coordinator_started_at = ? WHERE id = ?`,
			oneHourAgo, run.ID); err != nil {
			t.Fatalf("force running: %v", err)
		}
		if withResults != nil {
			withResults(run.ID)
		}
		return run.ID, run
	}

	errMsg := "boom"

	t.Run("no_results_is_failed", func(t *testing.T) {
		runID, _ := makeRun(0, nil)
		got, err := runRepo.DeriveFinalStatusFromResults(ctx, runID)
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		if got != "failed" {
			t.Errorf("status = %q, want failed", got)
		}
	})

	t.Run("all_errored_is_failed", func(t *testing.T) {
		runID, _ := makeRun(1, func(rid string) {
			if err := resRepo.BulkCreate(ctx, []*EvalRunResult{
				{RunID: rid, ExampleID: example.ID, TrialIndex: 0, ErrorMessage: &errMsg},
				{RunID: rid, ExampleID: example.ID, TrialIndex: 1, ErrorMessage: &errMsg},
			}); err != nil {
				t.Fatalf("bulk results: %v", err)
			}
		})
		got, err := runRepo.DeriveFinalStatusFromResults(ctx, runID)
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		if got != "failed" {
			t.Errorf("status = %q, want failed", got)
		}
	})

	t.Run("all_succeeded_is_completed", func(t *testing.T) {
		runID, _ := makeRun(2, func(rid string) {
			if err := resRepo.BulkCreate(ctx, []*EvalRunResult{
				{RunID: rid, ExampleID: example.ID, TrialIndex: 0},
				{RunID: rid, ExampleID: example.ID, TrialIndex: 1},
			}); err != nil {
				t.Fatalf("bulk results: %v", err)
			}
		})
		got, err := runRepo.DeriveFinalStatusFromResults(ctx, runID)
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		if got != "completed" {
			t.Errorf("status = %q, want completed", got)
		}
	})

	t.Run("mixed_is_completed_with_warnings", func(t *testing.T) {
		runID, _ := makeRun(3, func(rid string) {
			if err := resRepo.BulkCreate(ctx, []*EvalRunResult{
				{RunID: rid, ExampleID: example.ID, TrialIndex: 0},
				{RunID: rid, ExampleID: example.ID, TrialIndex: 1, ErrorMessage: &errMsg},
			}); err != nil {
				t.Fatalf("bulk results: %v", err)
			}
		})
		got, err := runRepo.DeriveFinalStatusFromResults(ctx, runID)
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		if got != "completed_with_warnings" {
			t.Errorf("status = %q, want completed_with_warnings", got)
		}
	})

	partial := EvalFailureKindPartialFailure
	for name, tc := range map[string]struct {
		trial int
		rows  []*EvalRunResult
	}{
		"all_partial_is_completed_with_warnings": {20, []*EvalRunResult{
			{ExampleID: example.ID, TrialIndex: 0, FailureKind: &partial},
			{ExampleID: example.ID, TrialIndex: 1, FailureKind: &partial},
		}},
		"partial_plus_errored_is_completed_with_warnings": {21, []*EvalRunResult{
			{ExampleID: example.ID, TrialIndex: 0, FailureKind: &partial},
			{ExampleID: example.ID, TrialIndex: 1, ErrorMessage: &errMsg},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			runID, _ := makeRun(tc.trial, func(rid string) {
				for _, r := range tc.rows {
					r.RunID = rid
				}
				if err := resRepo.BulkCreate(ctx, tc.rows); err != nil {
					t.Fatalf("bulk results: %v", err)
				}
			})
			got, err := runRepo.DeriveFinalStatusFromResults(ctx, runID)
			if err != nil {
				t.Fatalf("derive: %v", err)
			}
			if got != "completed_with_warnings" {
				t.Errorf("status = %q, want completed_with_warnings", got)
			}
		})
	}

	t.Run("finalize_stale_run_writes_derived_status_and_error_message", func(t *testing.T) {
		runID, _ := makeRun(4, func(rid string) {
			if err := resRepo.BulkCreate(ctx, []*EvalRunResult{
				{RunID: rid, ExampleID: example.ID, TrialIndex: 0},
				{RunID: rid, ExampleID: example.ID, TrialIndex: 1, ErrorMessage: &errMsg},
			}); err != nil {
				t.Fatalf("bulk results: %v", err)
			}
		})
		updated, err := runRepo.FinalizeStaleRun(ctx, runID, "Run exceeded maximum duration.")
		if err != nil {
			t.Fatalf("finalize: %v", err)
		}
		if !updated {
			t.Error("updated = false, want true for a running row")
		}
		got, err := runRepo.GetByID(ctx, runID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status != "completed_with_warnings" {
			t.Errorf("status = %q, want completed_with_warnings", got.Status)
		}
		if got.ErrorMessage == nil || *got.ErrorMessage != "Run exceeded maximum duration." {
			t.Errorf("error_message = %v, want 'Run exceeded maximum duration.'", got.ErrorMessage)
		}
		if got.CompletedTime == nil {
			t.Error("completed_time must be set")
		}
	})

	t.Run("finalize_is_idempotent_on_non_running", func(t *testing.T) {
		runID, _ := makeRun(5, nil)
		if _, err := runRepo.UpdateStatus(ctx, runID, "cancelled"); err != nil {
			t.Fatalf("set cancelled: %v", err)
		}
		updated, err := runRepo.FinalizeStaleRun(ctx, runID, "Run exceeded maximum duration.")
		if err != nil {
			t.Fatalf("finalize: %v", err)
		}
		if updated {
			t.Error("updated = true, want false for a non-running row")
		}
		got, err := runRepo.GetByID(ctx, runID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status != "cancelled" {
			t.Errorf("status = %q, want cancelled (finalize must not overwrite terminal)", got.Status)
		}
	})

	t.Run("list_stale_run_ids_excludes_unstarted", func(t *testing.T) {
		run := &EvalRun{ExperimentID: exp.ID, Model: "m-pending", StartedBy: "u"}
		if err := runRepo.Create(ctx, run); err != nil {
			t.Fatalf("create run: %v", err)
		}
		stale, err := runRepo.ListStaleRunIDs(ctx, 1)
		if err != nil {
			t.Fatalf("list stale: %v", err)
		}
		for _, id := range stale {
			if id == run.ID {
				t.Errorf("unstarted run included in stale set: %s", run.ID)
			}
		}
	})

	t.Run("mark_stale_runs_failed_partial_success_lands_warnings", func(t *testing.T) {
		runID, _ := makeRun(6, func(rid string) {
			if err := resRepo.BulkCreate(ctx, []*EvalRunResult{
				{RunID: rid, ExampleID: example.ID, TrialIndex: 0},
				{RunID: rid, ExampleID: example.ID, TrialIndex: 1, ErrorMessage: &errMsg},
			}); err != nil {
				t.Fatalf("bulk results: %v", err)
			}
		})
		ids, err := markStaleRunsFailed(ctx, runRepo, 1)
		if err != nil {
			t.Fatalf("mark stale: %v", err)
		}
		foundOurs := false
		for _, id := range ids {
			if id == runID {
				foundOurs = true
			}
		}
		if !foundOurs {
			t.Fatalf("expected %s in stale list; got %v", runID, ids)
		}
		got, err := runRepo.GetByID(ctx, runID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status != "completed_with_warnings" {
			t.Errorf("status = %q, want completed_with_warnings", got.Status)
		}
	})
}

func TestEvalRunResultAndScoreRepos(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	evRepo := NewEvalEvaluatorRepo(db)
	runRepo := NewEvalRunRepo(db)
	resRepo := NewEvalRunResultRepo(db)
	scoreRepo := NewEvalRunScoreRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "ResultDataset")
	example := &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "p"}
	if err := exRepo.Create(ctx, example); err != nil {
		t.Fatalf("create example: %v", err)
	}

	exp := &EvalExperiment{Name: "ResultExp", DatasetID: &dID, TargetAgent: "agent",
		Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	evID := newTestEvaluator(ctx, t, evRepo, "EvR", "llm_judge")

	run := &EvalRun{ExperimentID: exp.ID, Model: "m1", StartedBy: "u"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	results := []*EvalRunResult{
		{RunID: run.ID, ExampleID: example.ID, TrialIndex: 0},
		{RunID: run.ID, ExampleID: example.ID, TrialIndex: 1},
	}
	if err := resRepo.BulkCreate(ctx, results); err != nil {
		t.Fatalf("bulk create results: %v", err)
	}
	if results[0].ID == "" {
		t.Fatal("expected result ID set after bulk create")
	}

	dup := &EvalRunResult{RunID: run.ID, ExampleID: example.ID, TrialIndex: 0}
	if err := resRepo.Create(ctx, dup); err == nil {
		t.Fatal("expected unique violation on (run_id, example_id, trial_index)")
	}

	if err := scoreRepo.BulkCreate(ctx, []*EvalRunScore{
		{RunResultID: results[0].ID, EvaluatorID: evID, Score: 1.0},
		{RunResultID: results[1].ID, EvaluatorID: evID, Score: 0.5},
	}); err != nil {
		t.Fatalf("bulk scores: %v", err)
	}

	dupScore := &EvalRunScore{RunResultID: results[0].ID, EvaluatorID: evID, Score: 0.7}
	if err := scoreRepo.Create(ctx, dupScore); err == nil {
		t.Fatal("expected unique violation on (run_result_id, evaluator_id)")
	}

	t.Run("list_by_run_attaches_scores", func(t *testing.T) {
		got, err := resRepo.ListByRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("list by run: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("results = %d, want 2", len(got))
		}
		for _, r := range got {
			if len(r.Scores) != 1 {
				t.Errorf("result %s scores = %d, want 1", r.ID, len(r.Scores))
			}
		}
	})

	t.Run("average_scores_by_evaluator", func(t *testing.T) {
		avg, err := scoreRepo.AverageScoresByEvaluator(ctx, run.ID)
		if err != nil {
			t.Fatalf("avg: %v", err)
		}
		got := avg[evID]
		if got < 0.74 || got > 0.76 {
			t.Errorf("avg = %v, want ~0.75", got)
		}
	})

	t.Run("evaluator_with_scores_can_be_deleted_scores_survive", func(t *testing.T) {
		if err := evRepo.Delete(ctx, evID); err != nil {
			t.Fatalf("delete evaluator with scores: %v", err)
		}
		results, err := resRepo.ListByRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("list by run: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("results gone — scores must survive evaluator delete")
		}
		var seen int
		for _, r := range results {
			for _, s := range r.Scores {
				if s.EvaluatorID == evID {
					seen++
				}
			}
		}
		if seen == 0 {
			t.Errorf("no surviving scores reference evaluator %s", evID)
		}
	})

	t.Run("set_keys", func(t *testing.T) {
		if err := resRepo.SetExecutionDataKey(ctx, results[0].ID, "s3://exec/key"); err != nil {
			t.Fatalf("set exec key: %v", err)
		}
		if err := resRepo.SetTaskEventsKey(ctx, results[0].ID, "s3://events/key"); err != nil {
			t.Fatalf("set events key: %v", err)
		}
		got, err := resRepo.GetByID(ctx, results[0].ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.ExecutionDataKey == nil || *got.ExecutionDataKey != "s3://exec/key" {
			t.Errorf("exec key = %v", got.ExecutionDataKey)
		}
		if got.TaskEventsKey == nil || *got.TaskEventsKey != "s3://events/key" {
			t.Errorf("events key = %v", got.TaskEventsKey)
		}
	})

	t.Run("delete_run_cascades_results_and_scores", func(t *testing.T) {
		if _, err := runRepo.UpdateStatus(ctx, run.ID, "completed"); err != nil {
			t.Fatalf("complete run: %v", err)
		}
		if err := runRepo.Delete(ctx, run.ID, nil); err != nil {
			t.Fatalf("delete run: %v", err)
		}
		got, err := resRepo.ListByRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("list after cascade: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("after run delete, results = %d, want 0", len(got))
		}
		scores, err := scoreRepo.ListByRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("scores after cascade: %v", err)
		}
		if len(scores) != 0 {
			t.Errorf("after run delete, scores = %d, want 0", len(scores))
		}
	})
}

func TestEvalExperimentRepo_LatestRunStatus(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "DS-latest")
	exp := &EvalExperiment{
		Name: "Exp-latest", DatasetID: &dID, TargetAgent: "agent",
		Models: "[]", CreatedBy: "u", UpdatedBy: "u",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	older := &EvalRun{ExperimentID: exp.ID, Model: "m1", StartedBy: "u", StartedTime: 100}
	newer := &EvalRun{ExperimentID: exp.ID, Model: "m2", StartedBy: "u", StartedTime: 200}
	if err := runRepo.Create(ctx, older); err != nil {
		t.Fatalf("create older: %v", err)
	}
	if err := runRepo.Create(ctx, newer); err != nil {
		t.Fatalf("create newer: %v", err)
	}
	if _, err := runRepo.UpdateStatus(ctx, newer.ID, "running"); err != nil {
		t.Fatalf("update newer status: %v", err)
	}

	enriched, err := expRepo.GetEnriched(ctx, exp.ID)
	if err != nil {
		t.Fatalf("get enriched: %v", err)
	}
	if enriched.LatestRunStatus == nil || *enriched.LatestRunStatus != "running" {
		t.Errorf("latest status = %v, want running", enriched.LatestRunStatus)
	}
	if enriched.LatestRunTime == nil || *enriched.LatestRunTime != 200 {
		t.Errorf("latest time = %v, want 200", enriched.LatestRunTime)
	}
}

func TestEvalExperimentRepo_ListPaginated(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	evRepo := NewEvalEvaluatorRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "DS-paged-experiments")
	judge := newTestEvaluator(ctx, t, evRepo, "Judge", "llm_judge")
	other := newTestEvaluator(ctx, t, evRepo, "Other", "llm_judge")
	for i, c := range []struct {
		name       string
		evaluators []string
	}{{"bravo", []string{judge}}, {"Alpha", []string{other}}, {"charlie", []string{judge, other}}, {"Delta", nil}} {
		exp := &EvalExperiment{
			Name: c.name, DatasetID: &dID, TargetAgent: "agent", Models: "[]",
			CreatedBy: "u", UpdatedBy: "u", CreatedTime: int64(1000 + i), UpdatedTime: int64(1000 + i),
		}
		if err := expRepo.Create(ctx, exp, c.evaluators); err != nil {
			t.Fatalf("create %q: %v", c.name, err)
		}
	}
	names := func(rows []*EvalExperimentEnriched) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Name
		}
		return out
	}

	t.Run("second_page", func(t *testing.T) {
		rows, total, err := expRepo.ListPaginated(ctx, EvalExperimentFilter{}, 2, 3, EvalExperimentSortCreatedTime, false)
		if err != nil || total != 4 || !slices.Equal(names(rows), []string{"Delta"}) {
			t.Fatalf("page 2 of size 3 = %v, total %d, err %v; want the last experiment of 4", names(rows), total, err)
		}
	})

	t.Run("page_past_the_end_is_empty", func(t *testing.T) {
		rows, total, err := expRepo.ListPaginated(ctx, EvalExperimentFilter{}, 9, 3, EvalExperimentSortCreatedTime, true)
		if err != nil || total != 4 || len(rows) != 0 {
			t.Fatalf("page 9 = %v, total %d, err %v; want no rows and the full total", names(rows), total, err)
		}
	})

	t.Run("evaluator_filter", func(t *testing.T) {
		rows, total, err := expRepo.ListPaginated(ctx, EvalExperimentFilter{EvaluatorID: judge}, 1, 10, EvalExperimentSortCreatedTime, true)
		if err != nil || total != 2 || !slices.Equal(names(rows), []string{"charlie", "bravo"}) {
			t.Fatalf("judge filter = %v, total %d, err %v; want the two experiments it scores, newest first", names(rows), total, err)
		}
		rows, total, err = expRepo.ListPaginated(ctx, EvalExperimentFilter{EvaluatorID: other}, 1, 1, EvalExperimentSortCreatedTime, true)
		if err != nil || total != 2 || !slices.Equal(names(rows), []string{"charlie"}) {
			t.Fatalf("other filter page 1 of size 1 = %v, total %d, err %v; want the newest of its two with a total of 2", names(rows), total, err)
		}
	})

	t.Run("sort_by_name_ignores_case", func(t *testing.T) {
		asc, _, err := expRepo.ListPaginated(ctx, EvalExperimentFilter{}, 1, 10, EvalExperimentSortName, false)
		if err != nil || !slices.Equal(names(asc), []string{"Alpha", "bravo", "charlie", "Delta"}) {
			t.Fatalf("name asc = %v, err %v; want alphabetical regardless of case", names(asc), err)
		}
		desc, _, err := expRepo.ListPaginated(ctx, EvalExperimentFilter{}, 1, 10, EvalExperimentSortName, true)
		if err != nil || !slices.Equal(names(desc), []string{"Delta", "charlie", "bravo", "Alpha"}) {
			t.Fatalf("name desc = %v, err %v; want reverse alphabetical", names(desc), err)
		}
	})
}

func TestEvalRunRepo_ListRunGroupsPaginated(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "DS-groups")
	exp := &EvalExperiment{
		Name: "Exp-groups", DatasetID: &dID, TargetAgent: "agent-grp", Models: "[]",
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}

	groupA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	groupB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	nameA := "Group A"
	nameB := "Group B"
	for _, gr := range []struct {
		groupID string
		name    string
		started int64
	}{
		{groupA, nameA, 100},
		{groupA, nameA, 110},
		{groupB, nameB, 200},
	} {
		r := &EvalRun{
			ExperimentID: exp.ID, Model: "m", StartedBy: "u",
			RunGroupID: gr.groupID, RunName: &gr.name, StartedTime: gr.started,
		}
		if err := runRepo.Create(ctx, r); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}

	groups, total, err := runRepo.ListRunGroupsPaginated(ctx, 1, 10, RunGroupFilter{})
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if total != 2 {
		t.Errorf("total groups = %d, want 2", total)
	}
	if len(groups) != 2 {
		t.Fatalf("returned groups = %d, want 2", len(groups))
	}
	if groups[0].RunGroupID != groupB {
		t.Errorf("first group = %s, want %s", groups[0].RunGroupID, groupB)
	}
	if groups[1].RunCount != 2 {
		t.Errorf("group A run_count = %d, want 2", groups[1].RunCount)
	}
	if groups[0].TargetType != EvalTargetTypeAgent {
		t.Errorf("target type = %q, want the experiment's", groups[0].TargetType)
	}
	if len(groups[1].Statuses) != 2 {
		t.Errorf("group A statuses = %d, want 2", len(groups[1].Statuses))
	}

	t.Run("name_filter", func(t *testing.T) {
		groups, total, err := runRepo.ListRunGroupsPaginated(ctx, 1, 10, RunGroupFilter{Name: "Group A"})
		if err != nil {
			t.Fatalf("name filter: %v", err)
		}
		if total != 1 || len(groups) != 1 || groups[0].RunGroupID != groupA {
			t.Errorf("name filter result = %+v (total %d)", groups, total)
		}
	})

	t.Run("agent_filter", func(t *testing.T) {
		groups, total, err := runRepo.ListRunGroupsPaginated(ctx, 1, 10, RunGroupFilter{Agent: "agent-grp"})
		if err != nil {
			t.Fatalf("agent filter: %v", err)
		}
		if total != 2 || len(groups) != 2 {
			t.Errorf("agent filter total = %d, want 2", total)
		}
	})

	t.Run("name_filter_no_match", func(t *testing.T) {
		groups, total, err := runRepo.ListRunGroupsPaginated(ctx, 1, 10, RunGroupFilter{Name: "no-such-group"})
		if err != nil {
			t.Fatalf("no-match: %v", err)
		}
		if total != 0 {
			t.Errorf("no-match total = %d, want 0", total)
		}
		if len(groups) != 0 {
			t.Errorf("no-match returned %d groups, want 0", len(groups))
		}
	})

	t.Run("page_out_of_range", func(t *testing.T) {
		groups, total, err := runRepo.ListRunGroupsPaginated(ctx, 5, 10, RunGroupFilter{Agent: "agent-grp"})
		if err != nil {
			t.Fatalf("page out of range: %v", err)
		}
		if total != 2 {
			t.Errorf("page out of range total = %d, want 2", total)
		}
		if len(groups) != 0 {
			t.Errorf("page out of range returned %d groups, want 0", len(groups))
		}
	})

	t.Run("defaults_zero_inputs", func(t *testing.T) {
		groups, total, err := runRepo.ListRunGroupsPaginated(ctx, 0, 0, RunGroupFilter{})
		if err != nil {
			t.Fatalf("defaults: %v", err)
		}
		if total != 2 || len(groups) != 2 {
			t.Errorf("defaults: total=%d groups=%d, want 2/2", total, len(groups))
		}
	})

	t.Run("workflow_target", func(t *testing.T) {
		flow := &EvalExperiment{Name: "Exp-flow", DatasetID: &dID, TargetAgent: "order_flow", TargetType: EvalTargetTypeWorkflow,
			Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
		if err := expRepo.Create(ctx, flow, nil); err != nil {
			t.Fatalf("create workflow exp: %v", err)
		}
		name := "Flow group"
		if err := runRepo.Create(ctx, &EvalRun{ExperimentID: flow.ID, RunGroupID: "cccccccc-cccc-cccc-cccc-cccccccccccc", RunName: &name, StartedTime: 300, StartedBy: "u"}); err != nil {
			t.Fatalf("create run: %v", err)
		}
		groups, _, err := runRepo.ListRunGroupsPaginated(ctx, 1, 10, RunGroupFilter{Name: "Flow group"})
		if err != nil || len(groups) != 1 || groups[0].TargetType != EvalTargetTypeWorkflow || groups[0].TargetAgent != "order_flow" {
			t.Errorf("workflow group = %+v, %v; want targetType workflow naming the workflow", groups, err)
		}
	})

	t.Run("target_type_filter", func(t *testing.T) {
		wfExp := &EvalExperiment{
			Name: "Exp-groups-wf", DatasetID: &dID, TargetAgent: "OrderFlow", TargetType: EvalTargetTypeWorkflow, Models: "[]",
			CreatedBy: "u", UpdatedBy: "u",
		}
		if err := expRepo.Create(ctx, wfExp, nil); err != nil {
			t.Fatalf("create workflow exp: %v", err)
		}
		groupC := "dddddddd-dddd-dddd-dddd-dddddddddddd"
		nameC := "Group C"
		if err := runRepo.Create(ctx, &EvalRun{ExperimentID: wfExp.ID, Model: "m", StartedBy: "u", RunGroupID: groupC, RunName: &nameC, StartedTime: 300}); err != nil {
			t.Fatalf("create workflow run: %v", err)
		}

		workflows, total, err := runRepo.ListRunGroupsPaginated(ctx, 1, 10, RunGroupFilter{TargetType: EvalTargetTypeWorkflow})
		if err != nil {
			t.Fatalf("workflow filter: %v", err)
		}
		if !slices.ContainsFunc(workflows, func(g *EvalRunGroupSummary) bool { return g.RunGroupID == groupC }) || slices.ContainsFunc(workflows, func(g *EvalRunGroupSummary) bool { return g.TargetType != EvalTargetTypeWorkflow }) {
			t.Errorf("workflow filter result = %+v (total %d), want only workflow groups, group C among them", workflows, total)
		}
		agents, total, err := runRepo.ListRunGroupsPaginated(ctx, 1, 10, RunGroupFilter{TargetType: EvalTargetTypeAgent})
		if err != nil {
			t.Fatalf("agent type filter: %v", err)
		}
		if total != 2 || len(agents) != 2 {
			t.Errorf("agent type filter total = %d, want the two agent groups", total)
		}
	})
}

func TestEvalRepos_AdditionalCoverage(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	evRepo := NewEvalEvaluatorRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	resRepo := NewEvalRunResultRepo(db)
	scoreRepo := NewEvalRunScoreRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "AddCov")

	t.Run("dataset_list_and_list_with_counts", func(t *testing.T) {
		_ = newTestDataset(ctx, t, dRepo, "AddCov-Other")
		all, err := dRepo.List(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(all) < 2 {
			t.Errorf("list returned %d, want >= 2", len(all))
		}
		wc, err := dRepo.ListWithCounts(ctx)
		if err != nil {
			t.Fatalf("list with counts: %v", err)
		}
		if len(wc) < 2 {
			t.Errorf("list with counts returned %d, want >= 2", len(wc))
		}
	})

	t.Run("evaluator_list", func(t *testing.T) {
		_ = newTestEvaluator(ctx, t, evRepo, "Cov-Ev", "llm_judge")
		all, err := evRepo.List(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(all) == 0 {
			t.Fatal("expected at least one evaluator")
		}
	})

	t.Run("example_count_and_ids", func(t *testing.T) {
		for i := range 3 {
			if err := exRepo.Create(ctx, &EvalExample{
				DatasetID: dID, SequenceNumber: i + 1, Prompt: "p",
			}); err != nil {
				t.Fatalf("seed example %d: %v", i, err)
			}
		}
		count, err := exRepo.CountByDataset(ctx, dID)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
		ids, err := exRepo.ListIDsByDataset(ctx, dID)
		if err != nil {
			t.Fatalf("ids: %v", err)
		}
		if len(ids) != 3 {
			t.Errorf("ids = %v, want 3", ids)
		}
	})

	t.Run("example_delete_paths", func(t *testing.T) {
		other := newTestDataset(ctx, t, dRepo, "AddCov-Del")
		ex := &EvalExample{DatasetID: other, SequenceNumber: 1, Prompt: "p"}
		if err := exRepo.Create(ctx, ex); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := exRepo.Delete(ctx, ex.ID, nil); err != nil {
			t.Fatalf("delete: %v", err)
		}
		for i := range 2 {
			if err := exRepo.Create(ctx, &EvalExample{
				DatasetID: other, SequenceNumber: i + 1, Prompt: "p",
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		n, err := exRepo.DeleteByDataset(ctx, other)
		if err != nil {
			t.Fatalf("delete by dataset: %v", err)
		}
		if n != 2 {
			t.Errorf("deleted = %d, want 2", n)
		}
	})

	t.Run("experiment_attach_detach", func(t *testing.T) {
		ev1 := newTestEvaluator(ctx, t, evRepo, "Cov-Ev1", "llm_judge")
		ev2 := newTestEvaluator(ctx, t, evRepo, "Cov-Ev2", "llm_judge")
		exp := &EvalExperiment{
			Name: "Cov-Exp", DatasetID: &dID, TargetAgent: "agent",
			Models: "[]", CreatedBy: "u", UpdatedBy: "u",
		}
		if err := expRepo.Create(ctx, exp, []string{ev1}); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := expRepo.AttachEvaluators(ctx, exp.ID, []string{ev2}); err != nil {
			t.Fatalf("attach: %v", err)
		}
		ids, err := expRepo.GetEvaluatorIDs(ctx, exp.ID)
		if err != nil {
			t.Fatalf("ids: %v", err)
		}
		if len(ids) != 2 {
			t.Errorf("ids = %v, want 2", ids)
		}
		if dErr := expRepo.DetachAllEvaluators(ctx, exp.ID); dErr != nil {
			t.Fatalf("detach: %v", dErr)
		}
		ids, err = expRepo.GetEvaluatorIDs(ctx, exp.ID)
		if err != nil {
			t.Fatalf("ids after detach: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("ids after detach = %v, want 0", ids)
		}
		empty := ""
		newAgent := "new-agent"
		newModels := `[{"model":"m"}]`
		newWorkers := 3
		updated, err := expRepo.Update(ctx, exp.ID, EvalExperimentUpdate{
			DatasetID:          &empty,
			PrimaryEvaluatorID: &empty,
			TargetAgent:        &newAgent,
			Models:             &newModels,
			MaxWorkers:         &newWorkers,
			UpdatedBy:          "bob",
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.DatasetID != nil {
			t.Errorf("dataset_id = %v, want nil after empty-string clear", updated.DatasetID)
		}
		if updated.TargetAgent != newAgent {
			t.Errorf("target_agent = %q, want %q", updated.TargetAgent, newAgent)
		}
	})

	t.Run("run_helpers", func(t *testing.T) {
		exp := &EvalExperiment{
			Name: "Cov-RunExp", DatasetID: &dID, TargetAgent: "agent",
			Models: "[]", CreatedBy: "u", UpdatedBy: "u",
		}
		if err := expRepo.Create(ctx, exp, nil); err != nil {
			t.Fatalf("create exp: %v", err)
		}
		groupID := "11111111-1111-1111-1111-111111111111"
		for i := range 2 {
			r := &EvalRun{
				ExperimentID: exp.ID, Model: "m", StartedBy: "u",
				RunGroupID: groupID, StartedTime: int64(100 + i),
			}
			if err := runRepo.Create(ctx, r); err != nil {
				t.Fatalf("create run: %v", err)
			}
			if err := runRepo.UpdateProgress(ctx, r.ID, i+1); err != nil {
				t.Fatalf("update progress: %v", err)
			}
		}
		listExp, err := runRepo.ListByExperiment(ctx, exp.ID)
		if err != nil {
			t.Fatalf("list by exp: %v", err)
		}
		if len(listExp) != 2 {
			t.Errorf("list by exp = %d, want 2", len(listExp))
		}
		listGrp, err := runRepo.ListByRunGroup(ctx, groupID)
		if err != nil {
			t.Fatalf("list by group: %v", err)
		}
		if len(listGrp) != 2 {
			t.Fatalf("list by group = %d, want 2", len(listGrp))
		}
		if upErr := runRepo.UpdateProgress(ctx, listGrp[1].ID, 5); upErr != nil {
			t.Fatalf("update progress: %v", upErr)
		}
		if upErr := runRepo.UpdateProgress(ctx, listGrp[1].ID, 3); upErr != nil {
			t.Fatalf("update progress: %v", upErr)
		}
		got, getErr := runRepo.GetByID(ctx, listGrp[1].ID)
		if getErr != nil {
			t.Fatalf("get run: %v", getErr)
		}
		if got.ProgressCurrent != 5 {
			t.Errorf("progress after a stale write = %d, want 5", got.ProgressCurrent)
		}
		ids, err := runRepo.ListIDsByStatus(ctx, "pending")
		if err != nil {
			t.Fatalf("ids by status: %v", err)
		}
		if len(ids) < 2 {
			t.Errorf("pending ids = %v, want >= 2", ids)
		}
		_, err = runRepo.UpdateSummaryScores(ctx, listExp[0].ID, `{"avg":0.9}`, "completed", nil)
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		errMsg := "boom"
		_, err = runRepo.UpdateSummaryScores(ctx, listExp[1].ID, `{"avg":0}`, "failed", &errMsg)
		if err != nil {
			t.Fatalf("summary err: %v", err)
		}
		n, err := runRepo.DeleteByRunGroup(ctx, groupID, nil)
		if err != nil {
			t.Fatalf("delete group: %v", err)
		}
		if n != 2 {
			t.Errorf("deleted = %d, want 2", n)
		}
	})

	t.Run("run_result_and_score_helpers", func(t *testing.T) {
		ex := &EvalExample{DatasetID: dID, SequenceNumber: 99, Prompt: "p"}
		if err := exRepo.Create(ctx, ex); err != nil {
			t.Fatalf("create example: %v", err)
		}
		exp := &EvalExperiment{
			Name: "Cov-Res", DatasetID: &dID, TargetAgent: "agent",
			Models: "[]", CreatedBy: "u", UpdatedBy: "u",
		}
		if err := expRepo.Create(ctx, exp, nil); err != nil {
			t.Fatalf("create exp: %v", err)
		}
		ev := newTestEvaluator(ctx, t, evRepo, "Cov-ResEv", "llm_judge")
		runs := []*EvalRun{
			{ExperimentID: exp.ID, Model: "m1", StartedBy: "u"},
			{ExperimentID: exp.ID, Model: "m2", StartedBy: "u"},
		}
		for _, r := range runs {
			if err := runRepo.Create(ctx, r); err != nil {
				t.Fatalf("create run: %v", err)
			}
		}
		r0 := &EvalRunResult{RunID: runs[0].ID, ExampleID: ex.ID}
		r1 := &EvalRunResult{RunID: runs[1].ID, ExampleID: ex.ID}
		if err := resRepo.Create(ctx, r0); err != nil {
			t.Fatalf("res 0: %v", err)
		}
		if err := resRepo.Create(ctx, r1); err != nil {
			t.Fatalf("res 1: %v", err)
		}
		passT, passF := true, false
		if err := scoreRepo.Create(ctx, &EvalRunScore{
			RunResultID: r0.ID, EvaluatorID: ev, Score: 1.0, Passed: &passT,
		}); err != nil {
			t.Fatalf("score 0: %v", err)
		}
		if err := scoreRepo.Create(ctx, &EvalRunScore{
			RunResultID: r1.ID, EvaluatorID: ev, Score: 0.0, Passed: &passF,
		}); err != nil {
			t.Fatalf("score 1: %v", err)
		}
		got, err := resRepo.ListByRunIDs(ctx, []string{runs[0].ID, runs[1].ID})
		if err != nil {
			t.Fatalf("list by run ids: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("results = %d, want 2", len(got))
		}
		s, err := scoreRepo.ListByResult(ctx, r0.ID)
		if err != nil {
			t.Fatalf("list by result: %v", err)
		}
		if len(s) != 1 || s[0].Passed == nil || !*s[0].Passed {
			t.Errorf("scores by result = %+v, want one passing score", s)
		}
	})
}

func TestEvalRepos_NotFoundAndSortBranches(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	evRepo := NewEvalEvaluatorRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	resRepo := NewEvalRunResultRepo(db)
	ctx := t.Context()

	missing := "ffffffff-ffff-ffff-ffff-ffffffffffff"

	t.Run("get_by_id_not_found_paths", func(t *testing.T) {
		if _, err := dRepo.GetByID(ctx, missing); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("dataset: %v", err)
		}
		if _, err := exRepo.GetByID(ctx, missing); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("example: %v", err)
		}
		if _, err := evRepo.GetByID(ctx, missing); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("evaluator: %v", err)
		}
		if _, err := expRepo.GetByID(ctx, missing); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("experiment: %v", err)
		}
		if _, err := runRepo.GetByID(ctx, missing); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("run: %v", err)
		}
		if _, err := resRepo.GetByID(ctx, missing); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("run_result: %v", err)
		}
	})

	t.Run("update_not_found_paths", func(t *testing.T) {
		newName := "x"
		if _, err := dRepo.Update(ctx, missing, &newName, nil, "u"); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("dataset: %v", err)
		}
		if _, err := exRepo.Update(ctx, missing, &newName, nil, false); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("example: %v", err)
		}
		if _, err := evRepo.Update(ctx, missing, EvalEvaluatorUpdate{Name: &newName, UpdatedBy: "u"}); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("evaluator: %v", err)
		}
		if _, err := expRepo.Update(ctx, missing, EvalExperimentUpdate{Name: &newName, UpdatedBy: "u"}); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("experiment: %v", err)
		}
		if _, err := runRepo.UpdateStatus(ctx, missing, "completed"); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("run: %v", err)
		}
		if _, err := runRepo.UpdateSummaryScores(ctx, missing, "{}", "completed", nil); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("run summary: %v", err)
		}
	})

	t.Run("delete_not_found_paths", func(t *testing.T) {
		if err := exRepo.Delete(ctx, missing, nil); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("example: %v", err)
		}
		if err := evRepo.Delete(ctx, missing); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("evaluator: %v", err)
		}
		if err := expRepo.Delete(ctx, missing, nil); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("experiment: %v", err)
		}
		if err := runRepo.Delete(ctx, missing, nil); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("run: %v", err)
		}
	})

	t.Run("dataset_sort_branches", func(t *testing.T) {
		_ = newTestDataset(ctx, t, dRepo, "Sort-A")
		_ = newTestDataset(ctx, t, dRepo, "Sort-B")
		for _, sortBy := range []EvalDatasetSortField{
			EvalDatasetSortCreatedTime,
			EvalDatasetSortUpdatedTime,
			EvalDatasetSortName,
			EvalDatasetSortExampleCount,
			EvalDatasetSortExperimentCount,
			EvalDatasetSortField("unknown-falls-back-to-default"),
		} {
			if _, _, err := dRepo.ListPaginated(ctx, 1, 10, "", sortBy, "asc"); err != nil {
				t.Errorf("sort %q: %v", sortBy, err)
			}
		}
	})
}

func TestEvalRunResultRepo_OrderBySequenceNumber(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	resRepo := NewEvalRunResultRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "OrderingDataset")
	examples := make([]*EvalExample, 5)
	for i := 0; i < 5; i++ {
		examples[i] = &EvalExample{
			DatasetID:      dID,
			SequenceNumber: i + 1,
			Prompt:         fmt.Sprintf("Prompt %d", i+1),
		}
		if err := exRepo.Create(ctx, examples[i]); err != nil {
			t.Fatalf("create example %d: %v", i+1, err)
		}
	}

	exp := &EvalExperiment{
		Name: "OrderingExp", DatasetID: &dID, TargetAgent: "agent",
		Models: "[]", CreatedBy: "u", UpdatedBy: "u",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	run := &EvalRun{ExperimentID: exp.ID, Model: "m1", StartedBy: "u"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	for i := 4; i >= 0; i-- {
		prompt := fmt.Sprintf("Prompt %d", i+1)
		res := &EvalRunResult{
			RunID:       run.ID,
			ExampleID:   examples[i].ID,
			TrialIndex:  0,
			InputPrompt: &prompt,
		}
		if err := resRepo.Create(ctx, res); err != nil {
			t.Fatalf("create result %d: %v", i+1, err)
		}
	}

	got, err := resRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("list by run: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("results = %d, want 5", len(got))
	}
	for i, r := range got {
		want := fmt.Sprintf("Prompt %d", i+1)
		if r.InputPrompt == nil || *r.InputPrompt != want {
			t.Errorf("result[%d].InputPrompt = %v, want %q", i, r.InputPrompt, want)
		}
	}
}

func TestEvalRunResultRepo_ListByRunIDsOrderBySequenceNumber(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	resRepo := NewEvalRunResultRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "BatchOrderingDataset")
	examples := make([]*EvalExample, 4)
	for i := 0; i < 4; i++ {
		examples[i] = &EvalExample{
			DatasetID:      dID,
			SequenceNumber: i + 1,
			Prompt:         fmt.Sprintf("Prompt %d", i+1),
		}
		if err := exRepo.Create(ctx, examples[i]); err != nil {
			t.Fatalf("create example %d: %v", i+1, err)
		}
	}

	exp := &EvalExperiment{
		Name: "BatchOrderingExp", DatasetID: &dID, TargetAgent: "agent",
		Models: "[]", CreatedBy: "u", UpdatedBy: "u",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	runGroupID := "22222222-2222-2222-2222-222222222222"
	run1 := &EvalRun{ExperimentID: exp.ID, Model: "m1", StartedBy: "u", RunGroupID: runGroupID}
	run2 := &EvalRun{ExperimentID: exp.ID, Model: "m2", StartedBy: "u", RunGroupID: runGroupID}
	if err := runRepo.Create(ctx, run1); err != nil {
		t.Fatalf("create run1: %v", err)
	}
	if err := runRepo.Create(ctx, run2); err != nil {
		t.Fatalf("create run2: %v", err)
	}

	run1Order := []int{3, 1, 2, 0}
	run2Order := []int{2, 0, 3, 1}

	for _, idx := range run1Order {
		prompt := fmt.Sprintf("Run1-Prompt %d", idx+1)
		res := &EvalRunResult{
			RunID:       run1.ID,
			ExampleID:   examples[idx].ID,
			TrialIndex:  0,
			InputPrompt: &prompt,
		}
		if err := resRepo.Create(ctx, res); err != nil {
			t.Fatalf("create run1 result for example %d: %v", idx+1, err)
		}
	}
	for _, idx := range run2Order {
		prompt := fmt.Sprintf("Run2-Prompt %d", idx+1)
		res := &EvalRunResult{
			RunID:       run2.ID,
			ExampleID:   examples[idx].ID,
			TrialIndex:  0,
			InputPrompt: &prompt,
		}
		if err := resRepo.Create(ctx, res); err != nil {
			t.Fatalf("create run2 result for example %d: %v", idx+1, err)
		}
	}

	got, err := resRepo.ListByRunIDs(ctx, []string{run1.ID, run2.ID})
	if err != nil {
		t.Fatalf("list by run ids: %v", err)
	}
	if len(got) != 8 {
		t.Fatalf("results = %d, want 8", len(got))
	}

	byRun := map[string][]*EvalRunResultWithScores{}
	for _, r := range got {
		byRun[r.RunID] = append(byRun[r.RunID], r)
	}

	for runID, runName := range map[string]string{run1.ID: "Run1", run2.ID: "Run2"} {
		results := byRun[runID]
		if len(results) != 4 {
			t.Errorf("%s: results = %d, want 4", runName, len(results))
			continue
		}
		for i, r := range results {
			want := fmt.Sprintf("%s-Prompt %d", runName, i+1)
			if r.InputPrompt == nil || *r.InputPrompt != want {
				t.Errorf("%s: result[%d].InputPrompt = %v, want %q", runName, i, r.InputPrompt, want)
			}
		}
	}
}

func TestEvalRunResultRepo_ArtifactMetadataColumns(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	resRepo := NewEvalRunResultRepo(db)

	dID := newTestDataset(ctx, t, dRepo, "InputArtifactDataset")
	example := &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "p"}
	if err := exRepo.Create(ctx, example); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &EvalExperiment{Name: "InputArtifactExp", DatasetID: &dID, TargetAgent: "agent",
		Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	run := &EvalRun{ExperimentID: exp.ID, Model: "m1", StartedBy: "u"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	meta := `{"filename":"config.yaml","version":0,"content_hash":"abc"}`
	expectedMeta := `{"filename":"reference.md","version":0,"content_hash":"exp"}`
	assertMetaEq := func(label string, got *string, want string) {
		t.Helper()
		if got == nil {
			t.Errorf("%s artifact metadata = nil, want %s", label, want)
			return
		}
		var gotVal, wantVal any
		if err := json.Unmarshal([]byte(*got), &gotVal); err != nil {
			t.Errorf("%s artifact metadata is not valid JSON (%q): %v", label, *got, err)
			return
		}
		if err := json.Unmarshal([]byte(want), &wantVal); err != nil {
			t.Fatalf("fixture is not valid JSON: %v", err)
		}
		if !reflect.DeepEqual(gotVal, wantVal) {
			t.Errorf("%s artifact metadata = %s, want %s", label, *got, want)
		}
	}
	assertBoth := func(label string, r *EvalRunResult) {
		t.Helper()
		assertMetaEq(label+" input", r.InputArtifactMetadata, meta)
		assertMetaEq(label+" expected", r.ExpectedArtifactMetadata, expectedMeta)
	}
	created := &EvalRunResult{RunID: run.ID, ExampleID: example.ID, TrialIndex: 0,
		InputArtifactMetadata: &meta, ExpectedArtifactMetadata: &expectedMeta}
	if err := resRepo.Create(ctx, created); err != nil {
		t.Fatalf("create result: %v", err)
	}
	bulked := &EvalRunResult{RunID: run.ID, ExampleID: example.ID, TrialIndex: 1,
		InputArtifactMetadata: &meta, ExpectedArtifactMetadata: &expectedMeta}
	textOnly := &EvalRunResult{RunID: run.ID, ExampleID: example.ID, TrialIndex: 2}
	if err := resRepo.BulkCreate(ctx, []*EvalRunResult{bulked, textOnly}); err != nil {
		t.Fatalf("bulk create results: %v", err)
	}

	got, err := resRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	assertBoth("GetByID", got.EvalRunResult)

	rows, err := resRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("list by run: %v", err)
	}
	byTrial := make(map[int]*EvalRunResultWithScores, len(rows))
	for _, r := range rows {
		byTrial[r.TrialIndex] = r
	}
	for _, trial := range []int{0, 1} {
		r := byTrial[trial]
		if r == nil {
			t.Errorf("trial %d missing from ListByRun", trial)
			continue
		}
		assertBoth(fmt.Sprintf("trial %d", trial), r.EvalRunResult)
	}
	switch r := byTrial[2]; {
	case r == nil:
		t.Error("text-only trial missing from ListByRun")
	case r.InputArtifactMetadata != nil:
		t.Errorf("text-only trial input_artifact_metadata = %q, want nil", *r.InputArtifactMetadata)
	case r.ExpectedArtifactMetadata != nil:
		t.Errorf("text-only trial expected_artifact_metadata = %q, want nil", *r.ExpectedArtifactMetadata)
	}

	acrossRuns, err := resRepo.ListByRunIDs(ctx, []string{run.ID})
	if err != nil {
		t.Fatalf("list by run ids: %v", err)
	}
	found := false
	for _, r := range acrossRuns {
		if r.ID != created.ID {
			continue
		}
		found = true
		assertBoth("ListByRunIDs", r.EvalRunResult)
	}
	if !found {
		t.Error("created result missing from ListByRunIDs")
	}
}

func TestEvalExampleArtifactRepo(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	aRepo := NewEvalExampleArtifactRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "Artifacts")
	resp := "expected"
	ex := &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "p1", ExpectedResponse: &resp}
	if err := exRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("fresh_example_hydrates_no_artifacts", func(t *testing.T) {
		got, err := exRepo.GetByID(ctx, ex.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if len(got.Artifacts) != 0 {
			t.Errorf("artifacts = %+v, want none", got.Artifacts)
		}
	})

	input := &EvalExampleArtifact{
		ExampleID:   ex.ID,
		Role:        EvalArtifactRoleInput,
		Filename:    "config.yaml",
		MIMEType:    "application/yaml",
		SizeBytes:   42,
		Version:     0,
		SessionID:   "eval-example-" + ex.ID + "-input",
		ContentHash: "abc123",
	}

	t.Run("upsert_round_trips_version_zero", func(t *testing.T) {
		if err := aRepo.Upsert(ctx, input, nil); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		got, err := exRepo.GetByID(ctx, ex.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		row := got.ArtifactByRole(EvalArtifactRoleInput)
		if row == nil {
			t.Fatal("input artifact missing after Upsert")
		}
		if row.Version != 0 {
			t.Errorf("version = %d, want 0", row.Version)
		}
		if row.Filename != input.Filename || row.MIMEType != input.MIMEType ||
			row.SizeBytes != input.SizeBytes || row.SessionID != input.SessionID ||
			row.ContentHash != input.ContentHash {
			t.Errorf("row = %+v, want fields of %+v", row, input)
		}
		if got.Prompt != "p1" || got.ExpectedResponse == nil || *got.ExpectedResponse != "expected" {
			t.Errorf("Upsert touched the example's own columns: %+v", got)
		}
	})

	t.Run("both_roles_coexist_with_the_same_filename", func(t *testing.T) {
		expected := &EvalExampleArtifact{
			ExampleID:   ex.ID,
			Role:        EvalArtifactRoleExpected,
			Filename:    "config.yaml",
			MIMEType:    "application/yaml",
			SizeBytes:   7,
			Version:     0,
			SessionID:   "eval-example-" + ex.ID + "-expected",
			ContentHash: "ref999",
		}
		if err := aRepo.Upsert(ctx, expected, nil); err != nil {
			t.Fatalf("Upsert expected: %v", err)
		}
		got, err := exRepo.GetByID(ctx, ex.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if len(got.Artifacts) != 2 {
			t.Fatalf("artifacts = %d rows, want 2", len(got.Artifacts))
		}
		if got.ArtifactByRole(EvalArtifactRoleExpected).ContentHash != "ref999" {
			t.Error("expected-role row did not round-trip")
		}
	})

	t.Run("a_second_row_for_the_same_role_violates_the_unique_constraint", func(t *testing.T) {
		_, err := db.ExecContext(ctx,
			`INSERT INTO eval_example_artifacts (`+evalExampleArtifactColumns+`)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"11111111-1111-7111-8111-111111111111", ex.ID, EvalArtifactRoleInput,
			"other.txt", "text/plain", int64(1), 0, "s", "h", int64(1), int64(1))
		if err == nil {
			t.Fatal("second input-role row inserted; the unique constraint is missing")
		}
		if !dbutil.IsUniqueViolation(err) {
			t.Fatalf("err = %v, want a unique-constraint violation", err)
		}
	})

	t.Run("replace_updates_in_place", func(t *testing.T) {
		replaced := *input
		replaced.ID = ""
		replaced.CreatedTime = 0
		replaced.Version = 1
		replaced.ContentHash = "def456"
		replaced.SizeBytes = 99
		if err := aRepo.Upsert(ctx, &replaced, nil); err != nil {
			t.Fatalf("Upsert replace: %v", err)
		}
		if replaced.ID != input.ID || replaced.CreatedTime != input.CreatedTime {
			t.Errorf("Upsert did not write the surviving row's id/created_time back: got (%s, %d), want (%s, %d)",
				replaced.ID, replaced.CreatedTime, input.ID, input.CreatedTime)
		}
		got, err := exRepo.GetByID(ctx, ex.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if len(got.Artifacts) != 2 {
			t.Fatalf("artifacts = %d rows, want 2 (replace must not add a row)", len(got.Artifacts))
		}
		row := got.ArtifactByRole(EvalArtifactRoleInput)
		if row.Version != 1 || row.ContentHash != "def456" || row.SizeBytes != 99 {
			t.Errorf("replace did not update the row: %+v", row)
		}
	})

	t.Run("prompt_update_does_not_clobber_the_artifacts", func(t *testing.T) {
		newPrompt := "p1-edited"
		if _, err := exRepo.Update(ctx, ex.ID, &newPrompt, nil, false); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, err := exRepo.GetByID(ctx, ex.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Prompt != newPrompt {
			t.Errorf("prompt = %q, want %q", got.Prompt, newPrompt)
		}
		if len(got.Artifacts) != 2 {
			t.Errorf("prompt edit dropped artifact rows: %+v", got.Artifacts)
		}
	})

	t.Run("list_and_paginate_hydrate_the_artifacts", func(t *testing.T) {
		all, err := exRepo.ListByDataset(ctx, dID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(all) != 1 || all[0].ArtifactByRole(EvalArtifactRoleInput) == nil {
			t.Errorf("ListByDataset lost the artifacts: %+v", all)
		}
		page, _, err := exRepo.ListByDatasetPaginated(ctx, dID, 1, 10, "", "", false)
		if err != nil {
			t.Fatalf("paginated: %v", err)
		}
		if len(page) != 1 || page[0].ArtifactByRole(EvalArtifactRoleExpected) == nil {
			t.Errorf("ListByDatasetPaginated lost the artifacts: %+v", page)
		}
	})

	t.Run("upsert_advances_the_example_updated_time", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `UPDATE eval_examples SET updated_time = 0 WHERE id = ?`, ex.ID); err != nil {
			t.Fatalf("backdate: %v", err)
		}
		again := *input
		again.ID = ""
		if err := aRepo.Upsert(ctx, &again, nil); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		got, err := exRepo.GetByID(ctx, ex.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.UpdatedTime == 0 {
			t.Error("updated_time still backdated; attaching a file is a modification of the example")
		}
	})

	t.Run("delete_removes_only_that_role", func(t *testing.T) {
		if err := aRepo.Delete(ctx, ex.ID, EvalArtifactRoleInput, nil); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		got, err := exRepo.GetByID(ctx, ex.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.ArtifactByRole(EvalArtifactRoleInput) != nil {
			t.Error("input row survived Delete")
		}
		if got.ArtifactByRole(EvalArtifactRoleExpected) == nil {
			t.Error("Delete of the input role removed the expected row too")
		}
	})

	t.Run("delete_is_idempotent_for_a_missing_row", func(t *testing.T) {
		if err := aRepo.Delete(ctx, ex.ID, EvalArtifactRoleInput, nil); err != nil {
			t.Fatalf("Delete on an absent row: %v", err)
		}
	})

	t.Run("upsert_against_a_missing_example", func(t *testing.T) {
		missing := *input
		missing.ID = ""
		missing.ExampleID = "00000000-0000-0000-0000-000000000096"
		if err := aRepo.Upsert(ctx, &missing, nil); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("Upsert err = %v, want ErrEvalNotFound", err)
		}
	})

	t.Run("deleting_the_example_cascades_the_rows", func(t *testing.T) {
		if err := exRepo.Delete(ctx, ex.ID, nil); err != nil {
			t.Fatalf("delete example: %v", err)
		}
		count, err := dbutil.QueryVal[int](ctx, db,
			`SELECT COUNT(*) FROM eval_example_artifacts WHERE example_id = ?`, ex.ID)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Errorf("artifact rows = %d after example delete, want 0 (ON DELETE CASCADE)", count)
		}
	})

	t.Run("delete_against_a_missing_example", func(t *testing.T) {
		if err := aRepo.Delete(ctx, ex.ID, EvalArtifactRoleInput, nil); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("Delete err = %v, want ErrEvalNotFound", err)
		}
	})
}

func TestEvalExampleArtifactRepo_SumBytesByDataset(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	aRepo := NewEvalExampleArtifactRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "Sums")
	otherID := newTestDataset(ctx, t, dRepo, "Other")

	t.Run("empty_dataset_sums_to_zero", func(t *testing.T) {
		sum, err := aRepo.SumBytesByDataset(ctx, dID)
		if err != nil {
			t.Fatalf("sum: %v", err)
		}
		if sum != 0 {
			t.Errorf("sum = %d, want 0", sum)
		}
	})

	withFiles := &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "a"}
	withoutFile := &EvalExample{DatasetID: dID, SequenceNumber: 2, Prompt: "b"}
	foreign := &EvalExample{DatasetID: otherID, SequenceNumber: 1, Prompt: "d"}
	for _, e := range []*EvalExample{withFiles, withoutFile, foreign} {
		if err := exRepo.Create(ctx, e); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	setBytes := func(exampleID, role string, size int64) {
		t.Helper()
		if err := aRepo.Upsert(ctx, &EvalExampleArtifact{
			ExampleID: exampleID, Role: role, Filename: "f.bin",
			MIMEType: "application/octet-stream", SizeBytes: size,
			SessionID: "s", ContentHash: "h",
		}, nil); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	setBytes(withFiles.ID, EvalArtifactRoleInput, 100)
	setBytes(withFiles.ID, EvalArtifactRoleExpected, 250)
	setBytes(foreign.ID, EvalArtifactRoleInput, 9999)

	t.Run("sums_both_roles_for_only_this_dataset", func(t *testing.T) {
		sum, err := aRepo.SumBytesByDataset(ctx, dID)
		if err != nil {
			t.Fatalf("sum: %v", err)
		}
		if sum != 350 {
			t.Errorf("sum = %d, want 350", sum)
		}
	})

	t.Run("deleting_a_role_removes_its_bytes_from_the_sum", func(t *testing.T) {
		if err := aRepo.Delete(ctx, withFiles.ID, EvalArtifactRoleExpected, nil); err != nil {
			t.Fatalf("delete: %v", err)
		}
		sum, err := aRepo.SumBytesByDataset(ctx, dID)
		if err != nil {
			t.Fatalf("sum: %v", err)
		}
		if sum != 100 {
			t.Errorf("sum = %d, want 100", sum)
		}
	})
}

func TestEvalExperimentRepo_RunAs(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "RunAs Dataset")
	runAs := "system:eval"
	exp := &EvalExperiment{
		Name:        "RunAs Exp",
		DatasetID:   &dID,
		TargetAgent: "agent-a",
		Models:      "[]",
		RunAs:       &runAs,
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("round_trips_on_get_and_list", func(t *testing.T) {
		got, err := expRepo.GetByID(ctx, exp.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.RunAs == nil || *got.RunAs != runAs {
			t.Errorf("run_as = %v, want %q", got.RunAs, runAs)
		}
		rows, err := expRepo.List(ctx, "", "", "agent-a")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(rows) != 1 || rows[0].RunAs == nil || *rows[0].RunAs != runAs {
			t.Errorf("list row = %+v, want run_as %q", rows[0].EvalExperiment, runAs)
		}
		if rows[0].DatasetUpdatedTime == nil {
			t.Error("enriched dataset_updated_time = nil, want the dataset's updated_time")
		}
	})

	t.Run("update_sets_and_preserves", func(t *testing.T) {
		next := "system:other"
		updated, err := expRepo.Update(ctx, exp.ID, EvalExperimentUpdate{RunAs: &next, UpdatedBy: "bob"})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.RunAs == nil || *updated.RunAs != next {
			t.Errorf("run_as = %v, want %q", updated.RunAs, next)
		}

		updated, err = expRepo.Update(ctx, exp.ID, EvalExperimentUpdate{UpdatedBy: "bob"})
		if err != nil {
			t.Fatalf("unrelated update: %v", err)
		}
		if updated.RunAs == nil || *updated.RunAs != next {
			t.Errorf("run_as = %v after unrelated update, want preserved %q", updated.RunAs, next)
		}
	})

	t.Run("update_clears_to_null", func(t *testing.T) {
		clear := ""
		updated, err := expRepo.Update(ctx, exp.ID, EvalExperimentUpdate{RunAs: &clear, UpdatedBy: "bob"})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.RunAs != nil {
			t.Errorf("run_as = %q, want NULL", *updated.RunAs)
		}
		got, err := expRepo.GetByID(ctx, exp.ID)
		if err != nil {
			t.Fatalf("get after clear: %v", err)
		}
		if got.RunAs != nil {
			t.Errorf("persisted run_as = %q, want NULL", *got.RunAs)
		}
	})
}

func TestEvalRunRepo_RunAsRoundTrip(t *testing.T) {
	db := TestDB(t)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()

	exp := &EvalExperiment{Name: "RunAs Run Exp", TargetAgent: "agent-a", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	runAs := "system:eval"
	run := &EvalRun{
		ExperimentID: exp.ID,
		Status:       "completed",
		Model:        "general",
		StartedTime:  100,
		RunAs:        &runAs,
		StartedBy:    "u",
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	got, err := runRepo.GetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RunAs == nil || *got.RunAs != runAs {
		t.Errorf("run_as = %v, want %q", got.RunAs, runAs)
	}

	groups, err := runRepo.LastRunGroupsForExperiment(ctx, exp.ID, []string{"general"}, 10)
	if err != nil {
		t.Fatalf("last groups: %v", err)
	}
	if len(groups) != 1 || groups[0].RunAs == nil || *groups[0].RunAs != runAs {
		t.Errorf("last groups = %+v, want run_as %q", groups, runAs)
	}
}

func TestEvalRunResultRepo_FailureKind(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	resRepo := NewEvalRunResultRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "FailKind Dataset")
	ex := &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "p"}
	if err := exRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &EvalExperiment{Name: "FailKind Exp", DatasetID: &dID, TargetAgent: "agent-a", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	run := &EvalRun{ExperimentID: exp.ID, Status: "completed", Model: "general", StartedBy: "u"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	kind := "timeout"
	single := &EvalRunResult{RunID: run.ID, ExampleID: ex.ID, TrialIndex: 0, FailureKind: &kind}
	if err := resRepo.Create(ctx, single); err != nil {
		t.Fatalf("create result: %v", err)
	}
	bulkKind := "agent_error"
	bulk := &EvalRunResult{RunID: run.ID, ExampleID: ex.ID, TrialIndex: 1, FailureKind: &bulkKind}
	if err := resRepo.BulkCreate(ctx, []*EvalRunResult{bulk}); err != nil {
		t.Fatalf("bulk create: %v", err)
	}

	got, err := resRepo.GetByID(ctx, single.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.FailureKind == nil || *got.FailureKind != kind {
		t.Errorf("failure_kind = %v, want %q", got.FailureKind, kind)
	}

	rows, err := resRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("list by run: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 results, got %d", len(rows))
	}
	byTrial := map[int]*string{}
	for _, r := range rows {
		byTrial[r.TrialIndex] = r.FailureKind
	}
	if byTrial[0] == nil || *byTrial[0] != kind {
		t.Errorf("trial 0 failure_kind = %v, want %q", byTrial[0], kind)
	}
	if byTrial[1] == nil || *byTrial[1] != bulkKind {
		t.Errorf("trial 1 failure_kind = %v, want %q", byTrial[1], bulkKind)
	}
}

func TestEvalExampleRepo_MutationsTouchDatasetUpdatedTime(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	ctx := t.Context()

	d := &EvalDataset{Name: "Touch Dataset", CreatedBy: "u", UpdatedBy: "u", CreatedTime: 1000, UpdatedTime: 1000}
	if err := dRepo.Create(ctx, d); err != nil {
		t.Fatalf("create dataset: %v", err)
	}

	datasetUpdatedTime := func(t *testing.T) int64 {
		t.Helper()
		got, err := dRepo.GetByID(ctx, d.ID)
		if err != nil {
			t.Fatalf("get dataset: %v", err)
		}
		return got.UpdatedTime
	}
	resetUpdatedTime := func(t *testing.T) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `UPDATE eval_datasets SET updated_time = 1000 WHERE id = ?`, d.ID); err != nil {
			t.Fatalf("reset updated_time: %v", err)
		}
	}

	ex := &EvalExample{DatasetID: d.ID, SequenceNumber: 1, Prompt: "p"}
	if err := exRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	if got := datasetUpdatedTime(t); got <= 1000 {
		t.Errorf("after example create, dataset updated_time = %d, want > 1000", got)
	}

	resetUpdatedTime(t)
	if err := exRepo.BulkCreate(ctx, []*EvalExample{{DatasetID: d.ID, SequenceNumber: 2, Prompt: "p2"}}); err != nil {
		t.Fatalf("bulk create: %v", err)
	}
	if got := datasetUpdatedTime(t); got <= 1000 {
		t.Errorf("after bulk create, dataset updated_time = %d, want > 1000", got)
	}

	resetUpdatedTime(t)
	newPrompt := "p-updated"
	if _, err := exRepo.Update(ctx, ex.ID, &newPrompt, nil, false); err != nil {
		t.Fatalf("update example: %v", err)
	}
	if got := datasetUpdatedTime(t); got <= 1000 {
		t.Errorf("after example update, dataset updated_time = %d, want > 1000", got)
	}

	resetUpdatedTime(t)
	if err := exRepo.Delete(ctx, ex.ID, nil); err != nil {
		t.Fatalf("delete example: %v", err)
	}
	if got := datasetUpdatedTime(t); got <= 1000 {
		t.Errorf("after example delete, dataset updated_time = %d, want > 1000", got)
	}

	resetUpdatedTime(t)
	if n, err := exRepo.DeleteByDataset(ctx, d.ID); err != nil || n != 1 {
		t.Fatalf("delete by dataset: n=%d err=%v", n, err)
	}
	if got := datasetUpdatedTime(t); got <= 1000 {
		t.Errorf("after delete-by-dataset, dataset updated_time = %d, want > 1000", got)
	}
}
