// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var budgetAssignmentSchemaObjects = []string{
	"budget_assignments",
	"ux_budgets_id_subject_type",
	"ix_budget_assignments_budget",
}

func insertAssignment(t *testing.T, tx *dbutil.Tx, subjectType, subjectRef, budgetID string) error {
	t.Helper()
	_, err := tx.ExecContext(t.Context(),
		`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id,
		                                 created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, 'test', 'test', 1, 1)`,
		idgen.New(), subjectType, subjectRef, budgetID)
	return err
}

func insertBudgetRow(t *testing.T, tx *dbutil.Tx, f budgetFixture) string {
	t.Helper()
	id := idgen.New()
	if err := f.insertWithID(t, tx, id); err != nil {
		t.Fatalf("insert budget %q: %v", f.name, err)
	}
	return id
}

type fkRefusal struct {
	postgresCode string
	sqliteCode   int
}

var (
	fkMissingParent = fkRefusal{postgresCode: "23503", sqliteCode: 787}
	fkRestrict      = fkRefusal{postgresCode: "23001", sqliteCode: 1811}
)

func assertForeignKeyViolation(t *testing.T, db *DB, err error, want fkRefusal) {
	t.Helper()
	if err == nil {
		t.Fatal("write succeeded; want a foreign-key violation")
	}
	if !dbutil.IsForeignKeyViolation(err) {
		t.Errorf("dbutil.IsForeignKeyViolation(%v) = false", err)
	}
	if db.Dialect() == "postgres" {
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.Code != want.postgresCode || pgErr.ConstraintName != "fk_budget_assignments_budget" {
			t.Fatalf("want %s on fk_budget_assignments_budget, got %v", want.postgresCode, err)
		}
		return
	}
	sqlErr, ok := errors.AsType[*sqlite.Error](err)
	if !ok || sqlErr.Code() != want.sqliteCode || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("want a FOREIGN KEY failure with extended code %d, got %v", want.sqliteCode, err)
	}
}

func TestBudgetAssignmentsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "budget_assignments", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()

		assertApplied := func(stage string) {
			t.Helper()
			for _, name := range budgetAssignmentSchemaObjects {
				if !schemaObjectExists(t, db, name) {
					t.Errorf("%s: %s missing", stage, name)
				}
			}
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		assertApplied("after up")

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		for _, name := range budgetAssignmentSchemaObjects {
			if schemaObjectExists(t, db, name) {
				t.Errorf("after down: %s survived the rollback", name)
			}
		}
		if !schemaObjectExists(t, db, "budgets") {
			t.Error("after down: budgets was dropped with the assignments")
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		assertApplied("after re-apply")
	})
}

func TestBudgetAssignmentsSchema(t *testing.T) {
	t.Parallel()

	const ref = "3f2b8c1e-5d4a-4e6f-9a7b-1c2d3e4f5a6b"
	forEachPlatformDialectAt(t, "budget_assignments", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}

		t.Run("accepts a user assignment to a user budget", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				budgetID := insertBudgetRow(t, tx, userBudget("Engineering"))
				if err := insertAssignment(t, tx, "user", ref, budgetID); err != nil {
					t.Errorf("insert refused: %v", err)
				}
			})
		})

		t.Run("rejects an empty subject_ref", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				budgetID := insertBudgetRow(t, tx, userBudget("Engineering"))
				got := violationOf(t, insertAssignment(t, tx, "user", "", budgetID))
				if want := (violation{"check", "ck_budget_assignments_ref_present"}); got != want {
					t.Errorf("refused by %+v, want %+v", got, want)
				}
			})
		})

		t.Run("rejects a second assignment for one subject", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				first := insertBudgetRow(t, tx, userBudget("Engineering"))
				second := insertBudgetRow(t, tx, userBudget("Premium"))
				if err := insertAssignment(t, tx, "user", ref, first); err != nil {
					t.Fatalf("first insert: %v", err)
				}
				want := violation{"unique", "budget_assignments.subject_type, budget_assignments.subject_ref"}
				if db.Dialect() == "postgres" {
					want.target = "uq_budget_assignments_subject"
				}
				err := insertAssignment(t, tx, "user", ref, second)
				if got := violationOf(t, err); got != want {
					t.Errorf("refused by %+v, want %+v", got, want)
				}
				if dbutil.IsForeignKeyViolation(err) {
					t.Errorf("dbutil.IsForeignKeyViolation claimed a unique violation: %v", err)
				}
			})
		})

		t.Run("admits distinct subjects on one budget", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				budgetID := insertBudgetRow(t, tx, userBudget("Engineering"))
				for _, r := range []string{ref, refB} {
					if err := insertAssignment(t, tx, "user", r, budgetID); err != nil {
						t.Fatalf("insert %q refused: %v", r, err)
					}
				}
			})
		})

		t.Run("mesh assignment", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				err := insertAssignment(t, tx, "mesh", ref, SeededMeshBudgetID)
				if db.Dialect() != "postgres" {
					if err != nil {
						t.Errorf("SQLite refused a mesh assignment (%v); the service rule and this test assume it does not", err)
					}
					return
				}
				if got, want := violationOf(t, err), (violation{"check", "ck_budget_assignments_subject_type"}); got != want {
					t.Errorf("refused by %+v, want %+v", got, want)
				}
			})
		})

		t.Run("rejects a tier mismatch", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				assertForeignKeyViolation(t, db, insertAssignment(t, tx, "user", ref, SeededMeshBudgetID), fkMissingParent)
			})
		})

		t.Run("rejects an unknown budget", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				assertForeignKeyViolation(t, db, insertAssignment(t, tx, "user", ref, idgen.New()), fkMissingParent)
			})
		})

		t.Run("restricts deleting an assigned budget", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				budgetID := insertBudgetRow(t, tx, userBudget("Engineering"))
				if err := insertAssignment(t, tx, "user", ref, budgetID); err != nil {
					t.Fatalf("insert assignment: %v", err)
				}
				_, err := tx.ExecContext(ctx, `DELETE FROM budgets WHERE id = ?`, budgetID)
				assertForeignKeyViolation(t, db, err, fkRestrict)
			})
		})

		t.Run("deletes a budget once unassigned", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				budgetID := insertBudgetRow(t, tx, userBudget("Engineering"))
				if err := insertAssignment(t, tx, "user", ref, budgetID); err != nil {
					t.Fatalf("insert assignment: %v", err)
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM budget_assignments WHERE budget_id = ?`, budgetID); err != nil {
					t.Fatalf("delete assignment: %v", err)
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM budgets WHERE id = ?`, budgetID); err != nil {
					t.Errorf("delete refused: %v", err)
				}
			})
		})
	})
}
