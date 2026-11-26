// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestBudgetsRepo_MeshBudgetIsTheSeededMeshBudget(t *testing.T) {
	db := TestDB(t)
	repo := NewBudgetsRepo(db)

	mesh, err := repo.MeshBudget(t.Context())
	if err != nil {
		t.Fatalf("mesh budget: %v", err)
	}
	if mesh.ID != SeededMeshBudgetID || mesh.Amount != nil || mesh.WarnPercentage != nil {
		t.Errorf("seeded mesh budget = %+v, want the seeded id, unlimited and no warning", mesh)
	}

	if _, execErr := db.ExecContext(t.Context(), `UPDATE budgets SET amount = 42, warn_percentage = 80 WHERE subject_type = 'mesh'`); execErr != nil {
		t.Fatalf("limit the mesh: %v", execErr)
	}
	mesh, err = repo.MeshBudget(t.Context())
	if err != nil {
		t.Fatalf("mesh budget: %v", err)
	}
	if mesh.Amount == nil || *mesh.Amount != 42 || mesh.WarnPercentage == nil || *mesh.WarnPercentage != 80 {
		t.Errorf("mesh budget = %+v, want 42 warning at 80%%", mesh)
	}
}

func TestBudgetsRepo_MeshBudgetFailsWithoutAMeshBudget(t *testing.T) {
	db := TestDB(t)
	if _, err := db.ExecContext(t.Context(), `DELETE FROM budgets WHERE subject_type = 'mesh'`); err != nil {
		t.Fatalf("remove the mesh budget: %v", err)
	}
	if _, err := NewBudgetsRepo(db).MeshBudget(t.Context()); !errors.Is(err, ErrBudgetNotFound) {
		t.Errorf("err = %v, want ErrBudgetNotFound", err)
	}
}

func TestBudgetsRepo_AnyLimitedOnlyOnceABudgetHasAnAmount(t *testing.T) {
	db := TestDB(t)
	repo := NewBudgetsRepo(db)

	limited, err := repo.AnyLimited(t.Context())
	if err != nil {
		t.Fatalf("any limited: %v", err)
	}
	if limited {
		t.Error("a fresh install, whose seeded budgets are unlimited, reports a limit")
	}

	if _, execErr := db.ExecContext(t.Context(), `UPDATE budgets SET amount = 1 WHERE subject_type = 'user'`); execErr != nil {
		t.Fatalf("limit the user default: %v", execErr)
	}
	if limited, err = repo.AnyLimited(t.Context()); err != nil || !limited {
		t.Errorf("any limited = %v (err %v) once the user default has an amount, want true", limited, err)
	}
}

func TestBudgetsRepo_UserLimitsResolvesTheTiersInputs(t *testing.T) {
	db := TestDB(t)
	repo := NewBudgetsRepo(db)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC).UnixMilli()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`UPDATE budgets SET amount = 100, warn_percentage = 90 WHERE id = ?`, SeededDefaultUserBudgetID)
	limitedBudget, unlimitedBudget := idgen.New(), idgen.New()
	exec(`INSERT INTO budgets (id, name, name_lower, subject_type, amount, warn_percentage, is_default, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, 'user', 7, 50, 0, 'a', 'a', 1, 1)`, limitedBudget, "b "+limitedBudget, "b "+limitedBudget)
	exec(`INSERT INTO budgets (id, name, name_lower, subject_type, amount, is_default, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, 'user', NULL, 0, 'a', 'a', 1, 1)`, unlimitedBudget, "b "+unlimitedBudget, "b "+unlimitedBudget)
	limitedUser, unlimitedUser, toppedUp := idgen.New(), idgen.New(), idgen.New()
	exec(`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'user', ?, ?, 'a', 'a', 1, 1)`, idgen.New(), strings.ToUpper(limitedUser), limitedBudget)
	exec(`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'user', ?, ?, 'a', 'a', 1, 1)`, idgen.New(), unlimitedUser, unlimitedBudget)
	topUp := func(subjectType, ref string, amount, expires int64) {
		exec(`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
			VALUES (?, ?, ?, ?, ?, 'a', 'a', 1, 1)`, idgen.New(), subjectType, ref, amount, expires)
	}
	topUp("user", toppedUp, 5, now+2000)
	topUp("user", toppedUp, 9, now+1000)
	topUp("user", limitedUser, 50, now-1)
	topUp("mesh", "mesh", 1000, now+1000)

	limits, err := repo.UserLimits(t.Context(), now)
	if err != nil {
		t.Fatalf("user limits: %v", err)
	}
	if d := limits.Default; d.ID != SeededDefaultUserBudgetID || d.Amount == nil || *d.Amount != 100 || d.WarnPercentage == nil || *d.WarnPercentage != 90 {
		t.Errorf("default = %+v, want the seeded default at 100 warning at 90%%", d)
	}
	if got := limits.Assigned[limitedUser]; got.ID != limitedBudget || got.Amount == nil || *got.Amount != 7 ||
		got.WarnPercentage == nil || *got.WarnPercentage != 50 || len(limits.Assigned) != 2 {
		t.Errorf("assigned = %v, want the limited user (keyed lowercase) on 7 warning at 50%% and the unlimited user", limits.Assigned)
	}
	if got, ok := limits.Assigned[unlimitedUser]; !ok || got.ID != unlimitedBudget || got.Amount != nil {
		t.Errorf("unlimited user's assigned budget = %+v (%v), want present and unlimited", got, ok)
	}
	if want := map[string]int64{toppedUp: 5}; !maps.Equal(limits.TopUps, want) {
		t.Errorf("top-ups = %v, want %v: the latest-expiring active user top-up only", limits.TopUps, want)
	}
}

func TestBudgetsRepo_UserLimitsWithoutADefaultIsUnlimited(t *testing.T) {
	db := TestDB(t)
	if _, err := db.ExecContext(t.Context(), `DELETE FROM budgets WHERE id = ?`, SeededDefaultUserBudgetID); err != nil {
		t.Fatalf("remove the default: %v", err)
	}
	limits, err := NewBudgetsRepo(db).UserLimits(t.Context(), 1)
	if err != nil {
		t.Fatalf("user limits: %v", err)
	}
	if limits.Default.ID != "" || limits.Default.Amount != nil {
		t.Errorf("limits = %+v, want no default", limits)
	}
}

func TestBudgetsRepo_UserLimitsNeedsTheTimeItReadsAt(t *testing.T) {
	if _, err := NewBudgetsRepo(TestDB(t)).UserLimits(t.Context(), 0); !errors.Is(err, errNoNow) {
		t.Errorf("err = %v, want errNoNow", err)
	}
}
