// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var budgetSchemaObjects = []string{
	"budgets",
	"ux_budgets_name",
	"ux_budgets_default",
	"ux_budgets_mesh_singleton",
}

func schemaObjectExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	query := `SELECT COUNT(*) FROM sqlite_master WHERE name = ?`
	if db.Dialect() == "postgres" {
		query = `SELECT COUNT(*) FROM pg_class WHERE relname = ?`
	}
	count, err := dbutil.QueryVal[int](t.Context(), db, query, name)
	if err != nil {
		t.Fatalf("schema lookup %s: %v", name, err)
	}
	return count > 0
}

func budgetScopeGrants(t *testing.T, db *DB) int {
	t.Helper()
	count, err := dbutil.QueryVal[int](t.Context(), db,
		`SELECT COUNT(*) FROM rbac_role_scopes WHERE scope LIKE 'budget:%'`)
	if err != nil {
		t.Fatalf("count budget scope grants: %v", err)
	}
	return count
}

func assertSeededBudgets(t *testing.T, db *DB, stage string) {
	t.Helper()
	for _, want := range []Budget{
		{ID: SeededMeshBudgetID, Name: "Agent Mesh", SubjectType: BudgetSubjectMesh, IsDefault: true},
		{ID: SeededDefaultUserBudgetID, Name: "Default User", SubjectType: BudgetSubjectUser, IsDefault: true},
	} {
		got, err := dbutil.QueryRow[Budget](t.Context(), db, `SELECT `+budgetColumns+` FROM budgets WHERE id = ?`, want.ID)
		if err != nil || got == nil {
			t.Errorf("%s: seeded %q = %v, %v", stage, want.Name, got, err)
			continue
		}
		if got.Name != want.Name || got.NameLower != BudgetNameLower(want.Name) || got.SubjectType != want.SubjectType ||
			got.IsDefault != want.IsDefault || got.Amount != nil || got.WarnPercentage != nil ||
			got.CreatedBy != "system" || got.UpdatedBy != "system" || got.CreatedTime <= 0 || got.UpdatedTime != got.CreatedTime {
			t.Errorf("%s: seeded %q = %+v, want unlimited, no threshold, by system, default=%v", stage, want.Name, got, want.IsDefault)
		}
	}
	if n, err := NewBudgetsRepo(db).Count(t.Context(), BudgetFilter{}); err != nil || n != 2 {
		t.Errorf("%s: budgets = %d, %v; want exactly the 2 seeded", stage, n, err)
	}
}

func TestBudgetsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "budgets", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()

		assertApplied := func(stage string) {
			t.Helper()
			for _, name := range budgetSchemaObjects {
				if !schemaObjectExists(t, db, name) {
					t.Errorf("%s: %s missing", stage, name)
				}
			}
			if n := budgetScopeGrants(t, db); n != 0 {
				t.Errorf("%s: budget scope grants = %d, want none", stage, n)
			}
			assertSeededBudgets(t, db, stage)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		assertApplied("after up")

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		for _, name := range budgetSchemaObjects {
			if schemaObjectExists(t, db, name) {
				t.Errorf("after down: %s survived the rollback", name)
			}
		}
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		assertApplied("after re-apply")
	})
}
