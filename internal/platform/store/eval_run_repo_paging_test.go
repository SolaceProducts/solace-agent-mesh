// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
)

type pagingRun struct {
	groupID string
	model   string
	started int64
}

func newPagingExperiment(ctx context.Context, t *testing.T, db *DB, name, targetAgent string) string {
	t.Helper()
	datasetID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "DS-"+name)
	exp := &EvalExperiment{Name: name, DatasetID: &datasetID, TargetAgent: targetAgent, Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := NewEvalExperimentRepo(db).Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment %q: %v", name, err)
	}
	return exp.ID
}

func createPagingRuns(ctx context.Context, t *testing.T, repo *EvalRunRepo, experimentID string, runs []pagingRun) []string {
	t.Helper()
	ids := make([]string, len(runs))
	for i, pr := range runs {
		snapshot := `{"experiment":{}}`
		r := &EvalRun{ExperimentID: experimentID, Model: pr.model, StartedBy: "u", RunGroupID: groupID(pr.groupID), StartedTime: pr.started, ConfigSnapshot: &snapshot}
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("create run: %v", err)
		}
		ids[i] = r.ID
	}
	return ids
}

func groupID(label string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(label)).String() //nolint:forbidigo // deterministic UUIDv5 test ids for native-uuid columns
}

func groupIDs(labels ...string) []string {
	ids := make([]string, len(labels))
	for i, l := range labels {
		ids[i] = groupID(l)
	}
	return ids
}

func runIDsOf(runs []*EvalRun) []string {
	ids := make([]string, len(runs))
	for i, r := range runs {
		ids[i] = r.ID
	}
	return ids
}

func TestEvalRunRepo_ListByExperimentPaginated(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalRunRepo(db)
	ctx := t.Context()
	expID := newPagingExperiment(ctx, t, db, "paged-runs", "agent")
	otherID := newPagingExperiment(ctx, t, db, "paged-runs-other", "agent")
	ids := createPagingRuns(ctx, t, repo, expID, []pagingRun{
		{"g1", "m", 100}, {"g2", "m", 300}, {"g3", "m", 200},
	})
	createPagingRuns(ctx, t, repo, otherID, []pagingRun{{"g4", "m", 400}})
	newestFirst := []string{ids[1], ids[2], ids[0]}

	rows, total, err := repo.ListByExperimentPaginated(ctx, expID, 1, 10)
	if err != nil || total != 3 || !slices.Equal(runIDsOf(rows), newestFirst) {
		t.Fatalf("page 1 = %v, total %d, err %v; want %v newest first, own runs only", runIDsOf(rows), total, err, newestFirst)
	}
	rows, total, err = repo.ListByExperimentPaginated(ctx, expID, 2, 2)
	if err != nil || total != 3 || !slices.Equal(runIDsOf(rows), newestFirst[2:]) {
		t.Errorf("page 2 of size 2 = %v, total %d, err %v; want the oldest run with the total across pages", runIDsOf(rows), total, err)
	}
	rows, total, err = repo.ListByExperimentPaginated(ctx, expID, 9, 2)
	if err != nil || total != 3 || len(rows) != 0 {
		t.Errorf("past the end = %d rows, total %d, err %v; want none and the total", len(rows), total, err)
	}
	rows, total, err = repo.ListByExperimentPaginated(ctx, groupID("no-such-experiment"), 1, 10)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Errorf("unknown experiment = %d rows, total %d, err %v; want none", len(rows), total, err)
	}
}

func TestEvalRunRepo_ListByExperimentPaginated_TiesFallBackToIDOrder(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalRunRepo(db)
	ctx := t.Context()
	expID := newPagingExperiment(ctx, t, db, "tied-runs", "agent")
	ids := createPagingRuns(ctx, t, repo, expID, []pagingRun{{"g", "m", 100}, {"g", "m", 100}, {"g", "m", 100}})
	slices.Sort(ids)

	var got []string
	for page := 1; page <= 3; page++ {
		rows, _, err := repo.ListByExperimentPaginated(ctx, expID, page, 1)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, runIDsOf(rows)...)
	}
	if !slices.Equal(got, ids) {
		t.Errorf("runs with one start time across pages of one = %v, want %v, none repeated or lost", got, ids)
	}
}

func TestEvalRunRepo_ListRunGroupIDsByExperiment(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalRunRepo(db)
	ctx := t.Context()
	expID := newPagingExperiment(ctx, t, db, "paged-groups", "agent")
	otherID := newPagingExperiment(ctx, t, db, "paged-groups-other", "agent")
	createPagingRuns(ctx, t, repo, expID, []pagingRun{
		{"old", "a", 100}, {"old", "b", 150}, {"new", "a", 300}, {"mid", "a", 200}, {"mid", "b", 210},
	})
	createPagingRuns(ctx, t, repo, otherID, []pagingRun{{"foreign", "a", 999}})

	ids, total, err := repo.ListRunGroupIDsByExperiment(ctx, expID, 1, 10)
	if err != nil || total != 3 || !slices.Equal(ids, groupIDs("new", "mid", "old")) {
		t.Fatalf("groups = %v, total %d, err %v; want each group once, the one that started first last", ids, total, err)
	}
	ids, total, err = repo.ListRunGroupIDsByExperiment(ctx, expID, 2, 2)
	if err != nil || total != 3 || !slices.Equal(ids, groupIDs("old")) {
		t.Errorf("page 2 of size 2 = %v, total %d, err %v; want the oldest group with the total across pages", ids, total, err)
	}
	ids, total, err = repo.ListRunGroupIDsByExperiment(ctx, expID, 9, 2)
	if err != nil || total != 3 || len(ids) != 0 {
		t.Errorf("past the end = %v, total %d, err %v; want none and the total", ids, total, err)
	}
}

func TestEvalRunRepo_ListRunStatesByRunGroups(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalRunRepo(db)
	ctx := t.Context()
	expID := newPagingExperiment(ctx, t, db, "grouped-runs", "agent")
	otherID := newPagingExperiment(ctx, t, db, "grouped-runs-other", "agent")
	ids := createPagingRuns(ctx, t, repo, expID, []pagingRun{
		{"g1", "b-model", 100}, {"g1", "a-model", 110}, {"g2", "a-model", 200}, {"g3", "a-model", 300},
	})
	createPagingRuns(ctx, t, repo, otherID, []pagingRun{{"foreign", "a-model", 400}})

	rows, err := repo.ListRunStatesByRunGroups(ctx, expID, groupIDs("g1", "g2"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{ids[1], ids[2], ids[0]}; !slices.Equal(runIDsOf(rows), want) {
		t.Errorf("runs = %v, want %v: the listed groups only, ordered by model then start", runIDsOf(rows), want)
	}
	if rows[0].RunGroupID != groupID("g1") || rows[0].StartedTime != 110 || rows[0].ConfigSnapshot != nil {
		t.Errorf("first run = %+v, want its state read and its snapshot left unset", rows[0])
	}
	if rows, err = repo.ListRunStatesByRunGroups(ctx, expID, nil); err != nil || len(rows) != 0 {
		t.Errorf("no groups = %d rows, err %v; want none", len(rows), err)
	}
	if rows, err = repo.ListRunStatesByRunGroups(ctx, expID, groupIDs("unknown", "foreign")); err != nil || len(rows) != 0 {
		t.Errorf("unknown or another experiment's group = %d rows, err %v; want none", len(rows), err)
	}
}

func TestEvalRunRepo_ListByRunGroupPaginated(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalRunRepo(db)
	ctx := t.Context()
	expID := newPagingExperiment(ctx, t, db, "group-page", "agent")
	ids := createPagingRuns(ctx, t, repo, expID, []pagingRun{
		{"g", "c-model", 100}, {"g", "a-model", 110}, {"g", "b-model", 120}, {"other", "a-model", 130},
	})
	byModel := []string{ids[1], ids[2], ids[0]}

	rows, total, err := repo.ListByRunGroupPaginated(ctx, groupID("g"), 1, 2)
	if err != nil || total != 3 || !slices.Equal(runIDsOf(rows), byModel[:2]) {
		t.Fatalf("page 1 = %v, total %d, err %v; want the first two models of the group", runIDsOf(rows), total, err)
	}
	rows, total, err = repo.ListByRunGroupPaginated(ctx, groupID("g"), 2, 2)
	if err != nil || total != 3 || !slices.Equal(runIDsOf(rows), byModel[2:]) {
		t.Errorf("page 2 = %v, total %d, err %v; want the last model", runIDsOf(rows), total, err)
	}
	if rows, total, err = repo.ListByRunGroupPaginated(ctx, groupID("unknown"), 1, 2); err != nil || total != 0 || len(rows) != 0 {
		t.Errorf("unknown group = %d rows, total %d, err %v; want none", len(rows), total, err)
	}
}

func TestEvalRunRepo_ListDistinctRunTargetsPaginated(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalRunRepo(db)
	ctx := t.Context()
	for i, agent := range []string{"zeta", "alpha", "zeta", "mid"} {
		id := newPagingExperiment(ctx, t, db, "agents-"+string(rune('a'+i)), agent)
		createPagingRuns(ctx, t, repo, id, []pagingRun{{"g", "m", 100}})
		if agent == "mid" {
			if _, err := db.ExecContext(ctx, `UPDATE eval_experiments SET target_type = ? WHERE id = ?`, EvalTargetTypeWorkflow, id); err != nil {
				t.Fatalf("make mid a workflow: %v", err)
			}
		}
	}
	newPagingExperiment(ctx, t, db, "never-run", "unrun")

	alpha := RunTarget{Name: "alpha", TargetType: EvalTargetTypeAgent}
	mid := RunTarget{Name: "mid", TargetType: EvalTargetTypeWorkflow}
	zeta := RunTarget{Name: "zeta", TargetType: EvalTargetTypeAgent}
	targets, total, err := repo.ListDistinctRunTargetsPaginated(ctx, "", 1, 10)
	if err != nil || total != 3 || !slices.Equal(targets, []RunTarget{alpha, mid, zeta}) {
		t.Fatalf("targets = %v, total %d, err %v; want each target with a run once, alphabetically, with its type", targets, total, err)
	}
	targets, total, err = repo.ListDistinctRunTargetsPaginated(ctx, "", 2, 2)
	if err != nil || total != 3 || !slices.Equal(targets, []RunTarget{zeta}) {
		t.Errorf("page 2 of size 2 = %v, total %d, err %v; want the last target with the total across pages", targets, total, err)
	}
	if targets, total, err = repo.ListDistinctRunTargetsPaginated(ctx, "", 9, 2); err != nil || total != 3 || len(targets) != 0 {
		t.Errorf("past the end = %v, total %d, err %v; want none and the total", targets, total, err)
	}
	if targets, total, err = repo.ListDistinctRunTargetsPaginated(ctx, EvalTargetTypeWorkflow, 1, 10); err != nil || total != 1 || !slices.Equal(targets, []RunTarget{mid}) {
		t.Errorf("workflows = %v, total %d, err %v; want only mid", targets, total, err)
	}
	if targets, total, err = repo.ListDistinctRunTargetsPaginated(ctx, EvalTargetTypeAgent, 1, 10); err != nil || total != 2 || !slices.Equal(targets, []RunTarget{alpha, zeta}) {
		t.Errorf("agents = %v, total %d, err %v; want alpha and zeta", targets, total, err)
	}
}

func TestEvalRunRepo_CreateIfIdle(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewEvalRunRepo(db)
	countRuns := func(experimentID string) int {
		t.Helper()
		runs, err := repo.ListByExperiment(ctx, experimentID)
		if err != nil {
			t.Fatal(err)
		}
		return len(runs)
	}
	newRuns := func(experimentID string, n int) []*EvalRun {
		runs := make([]*EvalRun, n)
		for i := range runs {
			runs[i] = &EvalRun{ExperimentID: experimentID, StartedBy: "u"}
		}
		return runs
	}

	t.Run("an idle experiment gets every run, a busy one none", func(t *testing.T) {
		experimentID := newPagingExperiment(ctx, t, db, "idle", "agent")
		if err := repo.CreateIfIdle(ctx, experimentID, newRuns(experimentID, 2)); err != nil {
			t.Fatalf("first create = %v", err)
		}
		if err := repo.CreateIfIdle(ctx, experimentID, newRuns(experimentID, 1)); !errors.Is(err, ErrEvalRunActive) {
			t.Fatalf("create while pending = %v, want ErrEvalRunActive", err)
		}
		if n := countRuns(experimentID); n != 2 {
			t.Errorf("runs = %d, want the first create's 2", n)
		}
	})

	t.Run("an unknown experiment is not found", func(t *testing.T) {
		missing := uuid.NewString() //nolint:forbidigo // an id no row has
		if err := repo.CreateIfIdle(ctx, missing, newRuns(missing, 1)); !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("create = %v, want ErrEvalNotFound", err)
		}
	})

	t.Run("a failed insert keeps none of the runs", func(t *testing.T) {
		experimentID := newPagingExperiment(ctx, t, db, "rollback", "agent")
		runs := newRuns(experimentID, 2)
		runs[0].ID = uuid.NewString() //nolint:forbidigo // a fixed id the second run repeats
		runs[1].ID = runs[0].ID
		if err := repo.CreateIfIdle(ctx, experimentID, runs); err == nil {
			t.Fatal("create with a repeated id = nil, want the insert error")
		}
		if n := countRuns(experimentID); n != 0 {
			t.Errorf("runs = %d, want the first insert rolled back", n)
		}
	})

	t.Run("an experiment deleted while the create waits is not found", func(t *testing.T) {
		if db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		experimentID := newPagingExperiment(ctx, t, db, "deleted", "agent")
		tx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `DELETE FROM eval_experiments WHERE id = ?`, experimentID); err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() { done <- repo.CreateIfIdle(ctx, experimentID, newRuns(experimentID, 1)) }()
		dbtest.WaitForLockWait(t, db, "SELECT id FROM eval_experiments WHERE id")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, ErrEvalNotFound) {
			t.Errorf("create = %v, want ErrEvalNotFound", err)
		}
	})
}
