// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func deleteSystemUser(ctx context.Context, f assignmentFixture, id string) (SystemUserBudgetRowsDeleted, error) {
	var removed SystemUserBudgetRowsDeleted
	err := f.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var err error
		removed, err = NewUsersRepo(f.db).DeleteSystemUserInTx(ctx, tx, id)
		return err
	})
	return removed, err
}

func (f assignmentFixture) countFor(t *testing.T, ref string) int64 {
	t.Helper()
	n, err := f.repo.Count(t.Context(), BudgetAssignmentFilter{SubjectRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteSystemUser_RemovesItsBudgetAssignments(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		bot := seedAssignmentUser(t, f.db, "orders-bot", provisionedViaSystem, nil, nil)
		f.assign(t, bot, eng.ID)
		f.assign(t, refA, eng.ID)

		removed, err := deleteSystemUser(ctx, f, bot)
		if err != nil || removed.Assignments != 1 {
			t.Fatalf("delete = %+v, %v; want 1 assignment removed", removed, err)
		}
		if n := f.countFor(t, bot); n != 0 {
			t.Errorf("the deleted user keeps %d assignments", n)
		}
		if n := f.countFor(t, refA); n != 1 {
			t.Errorf("another user's assignment was touched: %d left", n)
		}

		unassigned := seedAssignmentUser(t, f.db, "unassigned-bot", provisionedViaSystem, nil, nil)
		if removed, err := deleteSystemUser(ctx, f, unassigned); err != nil || removed != (SystemUserBudgetRowsDeleted{}) {
			t.Errorf("delete of an unassigned user = %+v, %v; want nothing removed", removed, err)
		}
	})
}

func TestDeleteSystemUser_RecreatedUserStartsUnassigned(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		first := seedAssignmentUser(t, f.db, "orders-bot", provisionedViaSystem, nil, nil)
		f.assign(t, first, eng.ID)
		if _, err := deleteSystemUser(ctx, f, first); err != nil {
			t.Fatal(err)
		}

		second := seedAssignmentUser(t, f.db, "orders-bot", provisionedViaSystem, nil, nil)
		if second == first {
			t.Fatalf("the recreated user reused id %s", first)
		}
		subjects, err := f.repo.ListUserSubjects(ctx, UserBudgetSubjectFilter{NowMs: time.Now().UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range subjects {
			if s.ID == second && s.BudgetID != nil {
				t.Errorf("the recreated user starts on budget %s", *s.BudgetID)
			}
		}
		if n := f.countFor(t, first) + f.countFor(t, second); n != 0 {
			t.Errorf("%d assignments survive across the recreate", n)
		}
	})
}

func TestDeleteSystemUser_RollsBackTogether(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"budget_assignments", "users"} {
		t.Run("a failing delete from "+table, func(t *testing.T) {
			t.Parallel()
			budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
				ctx := t.Context()
				eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
				bot := seedAssignmentUser(t, f.db, "orders-bot", provisionedViaSystem, nil, nil)
				f.assign(t, bot, eng.ID)
				refuseDeletesFrom(t, f.db, table)

				if _, err := deleteSystemUser(ctx, f, bot); err == nil {
					t.Fatal("delete succeeded through the trigger")
				}
				if n := f.countFor(t, bot); n != 1 {
					t.Errorf("assignments after the failed delete = %d, want 1", n)
				}
				if u, _, err := NewUsersRepo(f.db).GetByID(ctx, bot); err != nil || u == nil {
					t.Errorf("user after the failed delete = %v, %v; want it kept", u, err)
				}
			})
		})
	}
}

func refuseDeletesFrom(t *testing.T, db *DB, table string) {
	t.Helper()
	statements := []string{
		`CREATE TRIGGER refuse_delete BEFORE DELETE ON ` + table + ` BEGIN SELECT RAISE(ABORT, 'refused'); END`,
	}
	if db.Dialect() == "postgres" {
		statements = []string{
			`CREATE FUNCTION refuse_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'refused'; END $$`,
			`CREATE TRIGGER refuse_delete BEFORE DELETE ON ` + table + ` FOR EACH ROW EXECUTE FUNCTION refuse_delete()`,
		}
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("install the refusing trigger: %v", err)
		}
	}
}

func TestDeleteSystemUser_LocksTheUserBeforeDeletingAssignments(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		bot := seedAssignmentUser(t, f.db, "orders-bot", provisionedViaSystem, nil, nil)

		create, err := f.db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = create.Rollback() }()
		if _, err := create.ExecContext(ctx, `SELECT id FROM users WHERE id = ? FOR KEY SHARE`, bot); err != nil {
			t.Fatal(err)
		}

		type result struct {
			removed SystemUserBudgetRowsDeleted
			err     error
		}
		done := make(chan result, 1)
		go func() {
			removed, err := deleteSystemUser(ctx, f, bot)
			done <- result{removed, err}
		}()
		dbtest.WaitForLockWait(t, f.db, "")

		if _, err := create.ExecContext(ctx,
			`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, 'x', 'x', 1, 1)`, "0198f4a0-0000-7000-8000-0000000000ff", BudgetSubjectUser, bot, eng.ID); err != nil {
			t.Fatal(err)
		}
		if err := create.Commit(); err != nil {
			t.Fatal(err)
		}

		if r := <-done; r.err != nil || r.removed.Assignments != 1 {
			t.Errorf("delete = %+v, %v; want it to remove the assignment committed while it waited", r.removed, r.err)
		}
		if n := f.countFor(t, bot); n != 0 {
			t.Errorf("the race left %d orphaned assignments", n)
		}
	})
}

func TestDeleteSystemUser_LeavesOtherSubjectTypes(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() == "postgres" {
			t.Skip("the CHECK refuses a non-user assignment")
		}
		bot := seedAssignmentUser(t, f.db, "orders-bot", provisionedViaSystem, nil, nil)
		if _, err := f.db.ExecContext(t.Context(),
			`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, 'x', 'x', 1, 1)`, "0198f4a0-0000-7000-8000-0000000000fe", BudgetSubjectMesh, bot, SeededMeshBudgetID); err != nil {
			t.Fatalf("insert a mesh assignment: %v", err)
		}
		if removed, err := deleteSystemUser(t.Context(), f, bot); err != nil || removed.Assignments != 0 {
			t.Errorf("delete = %+v, %v; want the mesh row left alone", removed, err)
		}
		if n, err := f.repo.Count(t.Context(), BudgetAssignmentFilter{SubjectType: BudgetSubjectMesh}); err != nil || n != 1 {
			t.Errorf("mesh rows after the delete = %d, %v; want 1", n, err)
		}
	})
}
