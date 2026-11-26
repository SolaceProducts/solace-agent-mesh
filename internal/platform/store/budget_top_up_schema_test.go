// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type topUpRow struct {
	subjectType, subjectRef string
	amount, expiresTime     int64
	createdTime             int64
}

func validTopUpRow() topUpRow {
	return topUpRow{subjectType: "user", subjectRef: refB, amount: 50, expiresTime: 2000, createdTime: 1000}
}

func insertTopUpRow(t *testing.T, tx *dbutil.Tx, r topUpRow) error {
	t.Helper()
	_, err := tx.ExecContext(t.Context(),
		`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time,
		                             created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, 'test', 'test', ?, ?)`,
		idgen.New(), r.subjectType, r.subjectRef, r.amount, r.expiresTime, r.createdTime, r.createdTime)
	return err
}

func TestBudgetTopUpsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "budget_top_ups", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		if !schemaObjectExists(t, db, "budget_top_ups") {
			t.Fatal("after up: budget_top_ups missing")
		}
		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		if schemaObjectExists(t, db, "budget_top_ups") {
			t.Error("after down: budget_top_ups survived the rollback")
		}
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		if !schemaObjectExists(t, db, "budget_top_ups") {
			t.Error("after re-apply: budget_top_ups missing")
		}
	})
}

func TestBudgetTopUpsSchema(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "budget_top_ups", func(t *testing.T, db *DB, version int64) {
		if err := dbutil.MigrateUpTo(t.Context(), db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}

		with := func(mutate func(*topUpRow)) topUpRow {
			r := validTopUpRow()
			mutate(&r)
			return r
		}

		for _, c := range []struct {
			name string
			row  topUpRow
		}{
			{"a user top-up", validTopUpRow()},
			{"the mesh top-up", with(func(r *topUpRow) { r.subjectType, r.subjectRef = BudgetSubjectMesh, BudgetMeshSubjectRef })},
		} {
			t.Run("accepts "+c.name, func(t *testing.T) {
				withRollback(t, db, func(tx *dbutil.Tx) {
					if err := insertTopUpRow(t, tx, c.row); err != nil {
						t.Errorf("insert refused: %v", err)
					}
				})
			})
		}

		for _, c := range []struct {
			name       string
			row        topUpRow
			constraint string
		}{
			{"an empty subject_ref", with(func(r *topUpRow) { r.subjectRef = "" }), "ck_budget_top_ups_ref_present"},
			{"a mesh row with a user's ref", with(func(r *topUpRow) { r.subjectType = BudgetSubjectMesh }), "ck_budget_top_ups_mesh_ref"},
			{"a zero amount", with(func(r *topUpRow) { r.amount = 0 }), "ck_budget_top_ups_amount_positive"},
			{"a negative amount", with(func(r *topUpRow) { r.amount = -1 }), "ck_budget_top_ups_amount_positive"},
			{"an expiry at creation", with(func(r *topUpRow) { r.expiresTime = r.createdTime }), "ck_budget_top_ups_expires_after_created"},
			{"an expiry before creation", with(func(r *topUpRow) { r.expiresTime = r.createdTime - 1 }), "ck_budget_top_ups_expires_after_created"},
		} {
			t.Run("rejects "+c.name, func(t *testing.T) {
				withRollback(t, db, func(tx *dbutil.Tx) {
					got := violationOf(t, insertTopUpRow(t, tx, c.row))
					if want := (violation{"check", c.constraint}); got != want {
						t.Errorf("refused by %+v, want %+v", got, want)
					}
				})
			})
		}

		t.Run("an unknown subject type", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				err := insertTopUpRow(t, tx, with(func(r *topUpRow) { r.subjectType = "agent" }))
				if db.Dialect() != "postgres" {
					if err != nil {
						t.Errorf("SQLite refused an agent top-up (%v); the service rule and this test assume it does not", err)
					}
					return
				}
				if got, want := violationOf(t, err), (violation{"check", "ck_budget_top_ups_subject_type"}); got != want {
					t.Errorf("refused by %+v, want %+v", got, want)
				}
			})
		})

		t.Run("rejects a second top-up for one subject with the same expiry", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				if err := insertTopUpRow(t, tx, validTopUpRow()); err != nil {
					t.Fatalf("first insert: %v", err)
				}
				want := violation{"unique", "budget_top_ups.subject_type, budget_top_ups.subject_ref, budget_top_ups.expires_time"}
				if db.Dialect() == "postgres" {
					want.target = "uq_budget_top_ups_subject_expiry"
				}
				if got := violationOf(t, insertTopUpRow(t, tx, with(func(r *topUpRow) { r.amount = 80 }))); got != want {
					t.Errorf("refused by %+v, want %+v", got, want)
				}
			})
		})

		t.Run("admits one subject's top-ups for different periods", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				for _, expiry := range []int64{2000, 3000} {
					if err := insertTopUpRow(t, tx, with(func(r *topUpRow) { r.expiresTime = expiry })); err != nil {
						t.Fatalf("insert expiring at %d refused: %v", expiry, err)
					}
				}
			})
		})

		t.Run("admits several subjects with one expiry", func(t *testing.T) {
			withRollback(t, db, func(tx *dbutil.Tx) {
				for _, ref := range []string{refB, refC} {
					if err := insertTopUpRow(t, tx, with(func(r *topUpRow) { r.subjectRef = ref })); err != nil {
						t.Fatalf("insert for %s refused: %v", ref, err)
					}
				}
			})
		})
	})
}
