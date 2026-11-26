// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const pendingIssuer = "https://idp.test"

func tableHasColumn(t *testing.T, db *DB, table, column string) bool {
	t.Helper()
	query := `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`
	if db.Dialect() == "postgres" {
		query = `SELECT COUNT(*) FROM information_schema.columns WHERE table_name = ? AND column_name = ?`
	}
	n, err := dbutil.QueryVal[int](t.Context(), db, query, table, column)
	if err != nil {
		t.Fatalf("column lookup %s.%s: %v", table, column, err)
	}
	return n > 0
}

var budgetProvenanceColumns = []string{"identity_type", "identity_value", "issuer"}

func TestBudgetPendingAssignmentsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "budget_pending_assignments", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		assertApplied := func(stage string, want bool) {
			t.Helper()
			for _, name := range []string{"budget_pending_assignments", "ix_budget_pending_assignments_budget"} {
				if got := schemaObjectExists(t, db, name); got != want {
					t.Errorf("%s: %s exists = %v, want %v", stage, name, got, want)
				}
			}
			for _, column := range budgetProvenanceColumns {
				if got := tableHasColumn(t, db, "budget_assignments", column); got != want {
					t.Errorf("%s: budget_assignments.%s exists = %v, want %v", stage, column, got, want)
				}
			}
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		assertApplied("after up", true)
		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		assertApplied("after down", false)
		if !schemaObjectExists(t, db, "budget_assignments") {
			t.Error("after down: budget_assignments was dropped with the pending table")
		}
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		assertApplied("after re-apply", true)
	})
}

func insertPendingRow(t *testing.T, tx *dbutil.Tx, identityType, value, issuer, budgetID string) error {
	t.Helper()
	_, err := tx.ExecContext(t.Context(),
		`INSERT INTO budget_pending_assignments (`+pendingAssignmentColumns+`)
		 VALUES (?, 'user', ?, ?, ?, ?, 'test', 'test', 1, 1)`,
		idgen.New(), identityType, value, issuer, budgetID)
	return err
}

func TestBudgetPendingAssignmentsSchema(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "budget_pending_assignments", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}

		checks := map[string]struct {
			identityType, value, issuer string
			constraint                  string
		}{
			"rejects a legacy identity":    {"legacy", "a@example.com", "", "ck_budget_pending_assignments_identity_type"},
			"rejects an empty identity":    {"email", "", "", "ck_budget_pending_assignments_value_present"},
			"rejects a sub with no issuer": {"sub", "u-1", "", "ck_budget_pending_assignments_sub_issuer"},
		}
		for name, c := range checks {
			t.Run(name, func(t *testing.T) {
				withRollback(t, db, func(tx *dbutil.Tx) {
					budgetID := insertBudgetRow(t, tx, userBudget("Engineering"))
					got := violationOf(t, insertPendingRow(t, tx, c.identityType, c.value, c.issuer, budgetID))
					if want := (violation{"check", c.constraint}); got != want {
						t.Errorf("refused by %+v, want %+v", got, want)
					}
				})
			})
		}

		t.Run("rejects a second pending budget for one identity", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				first := insertBudgetRow(t, tx, userBudget("Engineering"))
				second := insertBudgetRow(t, tx, userBudget("Premium"))
				if err := insertPendingRow(t, tx, "email", "a@example.com", "", first); err != nil {
					t.Fatalf("first insert: %v", err)
				}
				err := insertPendingRow(t, tx, "email", "a@example.com", "", second)
				want := violation{"unique", "budget_pending_assignments.subject_type, budget_pending_assignments.identity_type, " +
					"budget_pending_assignments.identity_value, budget_pending_assignments.issuer"}
				if db.Dialect() == "postgres" {
					want.target = "uq_budget_pending_assignments_identity"
				}
				if got := violationOf(t, err); got != want {
					t.Errorf("refused by %+v, want %+v", got, want)
				}
				if !isPendingIdentityUniqueViolation(err) {
					t.Errorf("isPendingIdentityUniqueViolation(%v) = false", err)
				}
			})
		})

		t.Run("restricts deleting a budget with a pending assignment", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				budgetID := insertBudgetRow(t, tx, userBudget("Engineering"))
				if err := insertPendingRow(t, tx, "email", "a@example.com", "", budgetID); err != nil {
					t.Fatalf("insert pending: %v", err)
				}
				_, err := tx.ExecContext(ctx, `DELETE FROM budgets WHERE id = ?`, budgetID)
				if !dbutil.IsForeignKeyViolation(err) {
					t.Errorf("delete = %v, want a foreign-key violation", err)
				}
			})
		})

		t.Run("rejects a pending assignment to the mesh budget", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				if err := insertPendingRow(t, tx, "email", "a@example.com", "", SeededMeshBudgetID); !dbutil.IsForeignKeyViolation(err) {
					t.Errorf("insert = %v, want a foreign-key violation", err)
				}
			})
		})
	})
}

func (f assignmentFixture) pend(t *testing.T, identityType, value, issuer, budgetID string) *BudgetPendingAssignment {
	t.Helper()
	p := &BudgetPendingAssignment{
		SubjectType: BudgetSubjectUser, IdentityType: identityType, IdentityValue: value, Issuer: issuer,
		BudgetID: budgetID, CreatedBy: "author", UpdatedBy: "author",
	}
	if err := f.repo.CreatePending(t.Context(), p); err != nil {
		t.Fatalf("pend %s %q: %v", identityType, value, err)
	}
	return p
}

func TestBudgetAssignmentsRepo_ListsPendingWithConcrete(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		concrete := f.assign(t, refA, eng.ID)
		pending := f.pend(t, BudgetIdentityEmail, "alice@example.com", "", eng.ID)
		sortsFirst := f.pend(t, BudgetIdentityEmail, "00@example.com", "", eng.ID)

		as, err := f.repo.List(ctx, BudgetAssignmentFilter{BudgetID: eng.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(as) != 3 || as[0].ID != concrete.ID || as[1].ID != pending.ID || as[2].ID != sortsFirst.ID {
			t.Fatalf("list = %+v, want the concrete assignment, then the pending ones in creation order, not by identity", as)
		}
		got := as[1]
		if got.State != BudgetAssignmentPending || got.SubjectRef != "" || got.IdentityType != BudgetIdentityEmail ||
			got.IdentityValue != "alice@example.com" || got.BudgetName != "Engineering" || got.SubjectEmail != nil {
			t.Errorf("pending row = %+v", got)
		}
		if as[0].State != BudgetAssignmentConcrete {
			t.Errorf("concrete row state = %q", as[0].State)
		}

		for name, c := range map[string]struct {
			filter BudgetAssignmentFilter
			want   []string
		}{
			"state pending":                 {BudgetAssignmentFilter{State: BudgetAssignmentPending}, []string{pending.ID, sortsFirst.ID}},
			"state concrete":                {BudgetAssignmentFilter{State: BudgetAssignmentConcrete}, []string{concrete.ID}},
			"subject excludes pending":      {BudgetAssignmentFilter{SubjectRef: refA}, []string{concrete.ID}},
			"search matches identity":       {BudgetAssignmentFilter{Search: "ALICE", SearchIdentity: true}, []string{pending.ID}},
			"search hides pending":          {BudgetAssignmentFilter{Search: "ALICE"}, nil},
			"search matches recorded sub":   {BudgetAssignmentFilter{Search: "sub-" + refA, SearchIdentity: true}, []string{concrete.ID}},
			"search hides the recorded sub": {BudgetAssignmentFilter{Search: "sub-" + refA}, nil},
		} {
			t.Run(name, func(t *testing.T) {
				as, err := f.repo.List(ctx, c.filter)
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, a := range as {
					ids = append(ids, a.ID)
				}
				if len(ids) != len(c.want) || (len(ids) > 0 && ids[0] != c.want[0]) {
					t.Errorf("ids = %v, want %v", ids, c.want)
				}
				n, err := f.repo.Count(ctx, c.filter)
				if err != nil || n != int64(len(c.want)) {
					t.Errorf("count = %d, %v; want %d", n, err, len(c.want))
				}
			})
		}

		if a := f.mustGet(t, pending.ID); a.State != BudgetAssignmentPending {
			t.Errorf("get pending = %+v", a)
		}
		b := mustGetBudget(t, f.budgets, eng.ID)
		if b.AssignedCount != 1 || b.PendingCount != 2 {
			t.Errorf("counts = %d assigned, %d pending; want 1 and 2", b.AssignedCount, b.PendingCount)
		}
	})
}

func TestBudgetAssignmentsRepo_CreatePendingRefusals(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations"})
		f.pend(t, BudgetIdentityEmail, "alice@example.com", "", eng.ID)

		for name, c := range map[string]struct {
			p    BudgetPendingAssignment
			want error
		}{
			"identity already pending": {BudgetPendingAssignment{IdentityType: BudgetIdentityEmail, IdentityValue: "alice@example.com", BudgetID: ops.ID}, ErrBudgetAssignmentExists},
			"unknown budget":           {BudgetPendingAssignment{IdentityType: BudgetIdentityEmail, IdentityValue: "bob@example.com", BudgetID: idgen.New()}, ErrBudgetAssignmentNoBudget},
			"mesh budget":              {BudgetPendingAssignment{IdentityType: BudgetIdentityEmail, IdentityValue: "bob@example.com", BudgetID: SeededMeshBudgetID}, ErrBudgetAssignmentNoBudget},
		} {
			t.Run(name, func(t *testing.T) {
				p := c.p
				p.SubjectType, p.CreatedBy, p.UpdatedBy = BudgetSubjectUser, "x", "x"
				if err := f.repo.CreatePending(t.Context(), &p); !errors.Is(err, c.want) {
					t.Errorf("CreatePending = %v, want %v", err, c.want)
				}
			})
		}
	})
}

func TestBudgetAssignmentsRepo_MovesAndDeletesPending(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations"})
		p := f.pend(t, BudgetIdentityEmail, "alice@example.com", "", eng.ID)

		res, err := f.repo.Update(ctx, p.ID, BudgetAssignmentUpdate{BudgetID: ops.ID, Actor: "mover", NowMs: 5})
		previous, changed := res.PreviousBudgetID, res.Moved
		if err != nil || !changed || previous != eng.ID {
			t.Fatalf("Update = %q, %v, %v; want %q, true", previous, changed, err, eng.ID)
		}
		if got := f.mustGet(t, p.ID); got.BudgetID != ops.ID || got.UpdatedBy != "mover" {
			t.Errorf("after move = %+v", got)
		}
		if _, moveErr := f.repo.Update(ctx, p.ID, BudgetAssignmentUpdate{BudgetID: SeededMeshBudgetID, Actor: "mover", NowMs: 6}); !errors.Is(moveErr, ErrBudgetAssignmentNoBudget) {
			t.Errorf("move to the mesh budget = %v, want ErrBudgetAssignmentNoBudget", moveErr)
		}

		budgetID, err := f.repo.Delete(ctx, p.ID)
		if err != nil || budgetID != ops.ID {
			t.Fatalf("Delete = %q, %v; want %q", budgetID, err, ops.ID)
		}
		if a, err := f.repo.GetByID(ctx, p.ID); err != nil || a != nil {
			t.Errorf("after delete GetByID = %+v, %v", a, err)
		}
	})
}

func TestBudgetsRepo_DeleteTakesABudgetsPendingAssignmentsWithIt(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		f.pend(t, BudgetIdentityEmail, "alice@example.com", "", eng.ID)

		deleted, err := f.budgets.Delete(t.Context(), eng.ID)
		if err != nil || len(deleted.Assignments) != 0 || len(deleted.Pending) != 1 {
			t.Errorf("Delete = %+v, %v; want it deleted with its 1 pending assignment", deleted, err)
		}
	})
}

func TestBudgetAssignmentsRepo_BindPendingForUser(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations"})

		t.Run("binds by email, whatever its case at sign-in", func(t *testing.T) {
			user := seedAssignmentUser(t, f.db, "sub-bob", "jit", nil, new("Bob@Example.com"))
			p := f.pend(t, BudgetIdentityEmail, "bob@example.com", "", eng.ID)
			got, err := f.repo.BindPendingForUser(ctx, user, "bob@example.com", 9)
			if err != nil || got.Bound == nil || got.Bound.ID != p.ID || len(got.Superseded) != 0 {
				t.Fatalf("bind = %+v, %v; want %s bound", got, err, p.ID)
			}
			a := f.mustGet(t, got.AssignmentID)
			if a.SubjectRef != user || a.BudgetID != eng.ID || a.IdentityType != BudgetIdentityEmail ||
				a.IdentityValue != "bob@example.com" || a.CreatedBy != "author" || a.UpdatedBy != "bob@example.com" {
				t.Errorf("bound assignment = %+v", a)
			}
			if gone, _ := f.repo.GetByID(ctx, p.ID); gone != nil {
				t.Errorf("pending row survived binding: %+v", gone)
			}
		})

		t.Run("binds by sub", func(t *testing.T) {
			user := seedAssignmentUser(t, f.db, "sub-carol", "jit", nil, nil)
			f.pend(t, BudgetIdentitySub, "sub-carol", pendingIssuer, ops.ID)
			got, err := f.repo.BindPendingForUser(ctx, user, "x", 9)
			if err != nil || got.Bound == nil || f.mustGet(t, got.AssignmentID).BudgetID != ops.ID {
				t.Errorf("bind = %+v, %v; want bound to Operations", got, err)
			}
		})

		t.Run("prefers the sub over the email", func(t *testing.T) {
			user := seedAssignmentUser(t, f.db, "sub-dan", "jit", nil, new("dan@example.com"))
			email := f.pend(t, BudgetIdentityEmail, "dan@example.com", "", eng.ID)
			sub := f.pend(t, BudgetIdentitySub, "sub-dan", pendingIssuer, ops.ID)
			got, err := f.repo.BindPendingForUser(ctx, user, "x", 9)
			if err != nil || got.Bound == nil || got.Bound.ID != sub.ID ||
				len(got.Superseded) != 1 || got.Superseded[0].ID != email.ID {
				t.Errorf("bind = %+v, %v; want the sub bound and the email superseded", got, err)
			}
		})

		t.Run("keeps an existing assignment", func(t *testing.T) {
			user := seedAssignmentUser(t, f.db, "sub-erin", "jit", nil, new("erin@example.com"))
			existing := f.assign(t, user, eng.ID)
			p := f.pend(t, BudgetIdentityEmail, "erin@example.com", "", ops.ID)
			got, err := f.repo.BindPendingForUser(ctx, user, "x", 9)
			if err != nil || got.Bound != nil || len(got.Superseded) != 1 || got.Superseded[0].ID != p.ID {
				t.Errorf("bind = %+v, %v; want nothing bound and the pending row superseded", got, err)
			}
			if a := f.mustGet(t, existing.ID); a.BudgetID != eng.ID {
				t.Errorf("existing assignment moved to %s", a.BudgetID)
			}
		})

		t.Run("leaves other identities alone", func(t *testing.T) {
			user := seedAssignmentUser(t, f.db, "sub-fay", "jit", nil, new("fay@example.com"))
			other := f.pend(t, BudgetIdentityEmail, "someone@example.com", "", eng.ID)
			otherIssuer := f.pend(t, BudgetIdentitySub, "sub-fay", "https://other.idp", eng.ID)
			got, err := f.repo.BindPendingForUser(ctx, user, "x", 9)
			if err != nil || got.Bound != nil || len(got.Superseded) != 0 {
				t.Errorf("bind = %+v, %v; want nothing", got, err)
			}
			for _, id := range []string{other.ID, otherIssuer.ID} {
				f.mustGet(t, id)
			}
		})

		t.Run("ignores an unknown user", func(t *testing.T) {
			if got, err := f.repo.BindPendingForUser(ctx, idgen.New(), "x", 9); err != nil || got.Bound != nil || len(got.Superseded) != 0 {
				t.Errorf("bind = %+v, %v; want nothing", got, err)
			}
		})
	})
}

func TestBudgetAssignmentsRepo_ConcurrentBindsReportOnce(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		user := seedAssignmentUser(t, f.db, "sub-gus", "jit", nil, new("gus@example.com"))
		f.pend(t, BudgetIdentityEmail, "gus@example.com", "", eng.ID)
		f.pend(t, BudgetIdentitySub, "sub-gus", pendingIssuer, eng.ID)

		results := make([]BudgetBindResult, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Go(func() { results[i], errs[i] = f.repo.BindPendingForUser(t.Context(), user, "x", 9) })
		}
		wg.Wait()
		bound, superseded := 0, 0
		for i := range results {
			if errs[i] != nil {
				t.Fatalf("bind %d: %v", i, errs[i])
			}
			if results[i].Bound != nil {
				bound++
			}
			superseded += len(results[i].Superseded)
		}
		if bound != 1 || superseded != 1 {
			t.Errorf("bound %d, superseded %d across both; want 1 and 1", bound, superseded)
		}
	})
}

func TestBudgetAssignmentsRepo_AnAssignmentSupersedesItsUsersPending(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations"})

		t.Run("create", func(t *testing.T) {
			user := seedAssignmentUser(t, f.db, "sub-hal", "jit", nil, new("hal@example.com"))
			p := f.pend(t, BudgetIdentityEmail, "hal@example.com", "", ops.ID)
			superseded, err := f.repo.Create(ctx, &BudgetAssignment{
				SubjectType: BudgetSubjectUser, SubjectRef: user, BudgetID: eng.ID, CreatedBy: "x", UpdatedBy: "x",
			})
			if err != nil || len(superseded) != 1 || superseded[0].ID != p.ID {
				t.Errorf("Create superseded %+v, %v; want %s", superseded, err, p.ID)
			}
		})

		t.Run("bulk add", func(t *testing.T) {
			user := seedAssignmentUser(t, f.db, "sub-ida", "jit", nil, nil)
			p := f.pend(t, BudgetIdentitySub, "sub-ida", pendingIssuer, ops.ID)
			changes, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{
				BudgetID: eng.ID, SubjectType: BudgetSubjectUser, Add: []string{user}, Actor: "x", NowMs: 1,
			})
			if err != nil || len(changes) != 1 || len(changes[0].Superseded) != 1 || changes[0].Superseded[0].ID != p.ID {
				t.Errorf("changes = %+v, %v; want the add to supersede %s", changes, err, p.ID)
			}
		})

		if n, err := f.repo.Count(ctx, BudgetAssignmentFilter{State: BudgetAssignmentPending}); err != nil || n != 0 {
			t.Errorf("pending left = %d, %v; want 0", n, err)
		}
	})
}

func TestBudgetAssignmentsRepo_ReportsASharedEmail(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		shared := seedAssignmentUser(t, f.db, "sub-jo-1", "jit", nil, new("jo@example.com"))
		seedAssignmentUser(t, f.db, "sub-jo-2", "jit", nil, new("jo@example.com"))
		unique := seedAssignmentUser(t, f.db, "sub-kim", "jit", nil, new("kim@example.com"))

		for user, want := range map[string]bool{shared: true, unique: false} {
			if got := f.mustGet(t, f.assign(t, user, eng.ID).ID).SubjectEmailShared; got != want {
				t.Errorf("user %s: SubjectEmailShared = %v, want %v", user, got, want)
			}
		}
	})
}

func TestBudgetAssignmentsRepo_CreateRacingABindLeavesOneAssignment(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations"})
		user := seedAssignmentUser(t, f.db, "sub-gus", "jit", nil, new("gus@example.com"))
		f.pend(t, BudgetIdentityEmail, "gus@example.com", "", eng.ID)

		tx, err := f.db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, user); err != nil {
			t.Fatal(err)
		}

		var bound BudgetBindResult
		var bindErr, createErr error
		var superseded []BudgetPendingAssignment
		var wg sync.WaitGroup
		wg.Go(func() { bound, bindErr = f.repo.BindPendingForUser(ctx, user, "x", 9) })
		wg.Go(func() {
			superseded, createErr = f.repo.Create(ctx, &BudgetAssignment{
				SubjectType: BudgetSubjectUser, SubjectRef: user, BudgetID: ops.ID, CreatedBy: "admin", UpdatedBy: "admin",
			})
		})
		waitForUserLockWaiters(t, f.db, 2)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()

		if bindErr != nil {
			t.Fatalf("bind: %v", bindErr)
		}
		bindWon := bound.Bound != nil && errors.Is(createErr, ErrBudgetAssignmentExists)
		createWon := createErr == nil && len(superseded) == 1 && bound.Bound == nil
		if !bindWon && !createWon {
			t.Errorf("bind = %+v, create = %v superseding %d; want exactly one to win", bound, createErr, len(superseded))
		}
		if n, _ := f.repo.Count(ctx, BudgetAssignmentFilter{SubjectRef: user}); n != 1 {
			t.Errorf("the user has %d assignments, want 1", n)
		}
		if n, _ := f.repo.Count(ctx, BudgetAssignmentFilter{State: BudgetAssignmentPending}); n != 0 {
			t.Errorf("%d pending rows remain, want 0", n)
		}
	})
}

func waitForUserLockWaiters(t *testing.T, db *DB, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock'
			    AND query LIKE 'SELECT id, email, sub, issuer, provisioned_via FROM users WHERE id IN%'`).Scan(&waiting); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d writers blocked on the user row", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBudgetAssignmentsRepo_BulkSavesSharingEmailsDoNotDeadlock(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering"})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations"})
		users := make([]string, 4)
		for i := range users {
			users[i] = seedAssignmentUser(t, f.db, "sub-shared-"+string(rune('a'+i)), "jit", nil, nil)
		}
		slices.Sort(users)
		emailOf := map[string]string{users[0]: "e1@example.com", users[3]: "e1@example.com", users[1]: "e2@example.com", users[2]: "e2@example.com"}
		for id, email := range emailOf {
			if _, err := f.db.ExecContext(ctx, `UPDATE users SET email = ? WHERE id = ?`, email, id); err != nil {
				t.Fatal(err)
			}
		}
		first := f.pend(t, BudgetIdentityEmail, "e1@example.com", "", eng.ID)
		f.pend(t, BudgetIdentityEmail, "e2@example.com", "", eng.ID)

		tx, err := f.db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `SELECT id FROM budget_pending_assignments WHERE id = ? FOR UPDATE`, first.ID); err != nil {
			t.Fatal(err)
		}

		save := func(budgetID string, add ...string) func() error {
			return func() error {
				_, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{
					BudgetID: budgetID, SubjectType: BudgetSubjectUser, Add: add, Actor: "x", NowMs: 1,
				})
				return err
			}
		}
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Go(func() { errs[0] = save(eng.ID, users[0], users[2])() })
		waitForPendingLockWaiters(t, f.db, 1)
		wg.Go(func() { errs[1] = save(ops.ID, users[1], users[3])() })
		waitForPendingLockWaiters(t, f.db, 2)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("save %d: %v; want both saves to finish", i+1, err)
			}
		}
	})
}

func waitForPendingLockWaiters(t *testing.T, db *DB, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock'
			    AND query LIKE 'SELECT id, subject_type, identity_type%'`).Scan(&waiting); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d saves blocked on the pending rows", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
