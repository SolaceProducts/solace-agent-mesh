// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
)

func budgetStateFixture(blocked bool) budget.EnforcementState {
	return budget.EnforcementState{
		BudgetsInUse: true,
		MeshBlocked:  blocked,
		PeriodEnd:    time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		ComputedAt:   time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
	}
}

func TestBudgetStateRepo_LoadsNothingUntilAStateIsSaved(t *testing.T) {
	repo := NewBudgetStateRepo(TestDB(t))
	state, err := repo.LoadBudgetState(t.Context())
	if err != nil || state != nil {
		t.Errorf("load = %+v, %v; want nil, nil", state, err)
	}
}

func TestBudgetStateRepo_KeepsOnlyTheLatestState(t *testing.T) {
	repo := NewBudgetStateRepo(TestDB(t))
	if err := repo.SaveBudgetState(t.Context(), budgetStateFixture(true)); err != nil {
		t.Fatalf("save: %v", err)
	}
	want := budgetStateFixture(false)
	want.ComputedAt = want.ComputedAt.Add(time.Minute)
	if err := repo.SaveBudgetState(t.Context(), want); err != nil {
		t.Fatalf("save again: %v", err)
	}

	got, err := repo.LoadBudgetState(t.Context())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || got.MeshBlocked != want.MeshBlocked || !got.PeriodEnd.Equal(want.PeriodEnd) || !got.ComputedAt.Equal(want.ComputedAt) {
		t.Errorf("load = %+v, want %+v", got, want)
	}
}

func TestBudgetStateRepo_ARowItCannotTrustIsAnError(t *testing.T) {
	for name, row := range map[string]string{
		"not JSON":                      `{"meshBlocked": tru`,
		"a block with no budget in use": `{"budgetsInUse": false, "meshBlocked": true, "periodEnd": "2026-11-01T00:00:00Z", "computedAt": "2026-10-16T12:00:00Z"}`,
		"a state no platform stamped":   `{"budgetsInUse": true, "meshBlocked": false}`,
	} {
		t.Run(name, func(t *testing.T) {
			db := TestDB(t)
			if _, err := db.ExecContext(t.Context(),
				`INSERT INTO gateway_budget_state (id, state, updated_time) VALUES (1, ?, 0)`, row); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if state, err := NewBudgetStateRepo(db).LoadBudgetState(t.Context()); err == nil {
				t.Errorf("load = %+v, nil; want an error", state)
			}
		})
	}
}

func TestBudgetStateMigration(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "budget_state.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	version := migrationVersion(t, sqliteMigrations(), "budget_state")
	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
	if err := NewBudgetStateRepo(db).SaveBudgetState(ctx, budgetStateFixture(true)); err != nil {
		t.Fatalf("save after the migration: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO gateway_budget_state (id, state, updated_time) VALUES (2, '{}', 0)`); err == nil {
		t.Error("the table took a second row")
	}
	if err := db.MigrateDownTo(ctx, version-1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT 1 FROM gateway_budget_state`); err == nil {
		t.Fatal("gateway_budget_state survived the down migration")
	}
	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}
