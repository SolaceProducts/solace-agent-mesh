// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func budgetRepoTest(t *testing.T, fn func(t *testing.T, db *DB, repo *BudgetsRepo)) {
	t.Helper()
	forEachMigratedPlatformDialect(t, func(t *testing.T, db *DB) {
		removeSeededBudgets(t, db)
		fn(t, db, NewBudgetsRepo(db))
	})
}

func forEachMigratedPlatformDialect(t *testing.T, fn func(t *testing.T, db *DB)) {
	t.Helper()
	forEachPlatformDialectClone(t, platformTemplates{sqlite: migratedSQLiteTemplate, postgres: getTestTemplate}, fn)
}

type platformTemplates struct {
	sqlite, postgres func() (*dbtest.Template, error)
}

func forEachPlatformDialectClone(t *testing.T, templates platformTemplates, fn func(t *testing.T, db *DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		fn(t, openTemplateClone(t, templates.sqlite))
	})
	t.Run("postgres", func(t *testing.T) {
		if !postgresLaneActive() {
			t.Skip("postgres lane not configured; set SAM_TEST_PLATFORM_DB_URL")
		}
		t.Parallel()
		fn(t, openTemplateClone(t, templates.postgres))
	})
}

var sqliteTemplateBesidePostgres atomic.Pointer[dbtest.Template]

var migratedSQLiteTemplate = sync.OnceValues(func() (*dbtest.Template, error) {
	if !postgresLaneActive() {
		return getTestTemplate()
	}
	tmpl, err := dbtest.NewTemplateFromURL("", fmt.Sprintf("platform_store_sqlite_%d", os.Getpid()), func(u string) error {
		db, err := Open(u, nil)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		return db.Migrate(context.Background())
	})
	if err == nil {
		sqliteTemplateBesidePostgres.Store(tmpl)
	}
	return tmpl, err
})

func TestMain(m *testing.M) {
	code := m.Run()
	if tmpl := sqliteTemplateBesidePostgres.Load(); tmpl != nil {
		tmpl.Close()
	}
	os.Exit(code)
}

func openTemplateClone(t *testing.T, template func() (*dbtest.Template, error)) *DB {
	t.Helper()
	tmpl, err := template()
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	db, err := Open(tmpl.Clone(t), nil)
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func removeSeededBudgets(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `DELETE FROM budgets WHERE id IN ('`+SeededMeshBudgetID+`', '`+SeededDefaultUserBudgetID+`')`); err != nil {
		t.Fatalf("remove seeded budgets: %v", err)
	}
}

func createBudget(t *testing.T, repo *BudgetsRepo, b *Budget) *Budget {
	t.Helper()
	if b.SubjectType == "" {
		b.SubjectType = BudgetSubjectUser
	}
	if b.CreatedBy == "" {
		b.CreatedBy, b.UpdatedBy = "creator", "creator"
	}
	if err := repo.Create(t.Context(), b); err != nil {
		t.Fatalf("create %q: %v", b.Name, err)
	}
	return b
}

func mustGetBudget(t *testing.T, repo *BudgetsRepo, id string) *Budget {
	t.Helper()
	b, err := repo.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if b == nil {
		t.Fatalf("get %s: not found", id)
	}
	return b
}

func defaultsAtTier(t *testing.T, db *DB, tier string) []string {
	t.Helper()
	ids, err := dbutil.QueryVals[string](t.Context(), db,
		`SELECT id FROM budgets WHERE subject_type = ? AND is_default = 1 ORDER BY id`, tier)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	return ids
}

func isBudgetSentinel(err error) bool {
	for _, s := range []error{ErrBudgetNameTaken, ErrBudgetDefaultTaken, ErrBudgetDefaultConflict, ErrBudgetMeshExists} {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

func TestBudgetsRepo_CreateAndGet(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		b := createBudget(t, repo, &Budget{
			Name: "  Engineering Monthly  ", Amount: new(int64(1999)), WarnPercentage: new(80),
		})

		parsed, err := uuid.Parse(b.ID)
		if err != nil || parsed.Version() != 7 || parsed.String() != b.ID {
			t.Errorf("id %q: want a canonical lowercase UUIDv7 from idgen.New", b.ID)
		}

		got := mustGetBudget(t, repo, b.ID)
		if got.Name != "Engineering Monthly" || got.NameLower != "engineering monthly" {
			t.Errorf("name = %q / %q, want trimmed name and its folded form with spaces kept", got.Name, got.NameLower)
		}
		if got.SubjectType != BudgetSubjectUser || got.IsDefault ||
			got.Amount == nil || *got.Amount != 1999 || got.WarnPercentage == nil || *got.WarnPercentage != 80 ||
			got.CreatedBy != "creator" || got.CreatedTime == 0 || got.UpdatedTime != got.CreatedTime {
			t.Errorf("round-trip mismatch: %+v", got)
		}

		unlimited := createBudget(t, repo, &Budget{Name: "Unlimited"})
		if got := mustGetBudget(t, repo, unlimited.ID); got.Amount != nil || got.WarnPercentage != nil {
			t.Errorf("unlimited budget read back amount=%v warn=%v, want both nil", got.Amount, got.WarnPercentage)
		}

		for _, name := range []string{"engineering monthly", "ENGINEERING MONTHLY", "  Engineering Monthly "} {
			if found, err := repo.GetByName(ctx, name); err != nil || found == nil || found.ID != b.ID {
				t.Errorf("GetByName(%q) = %v, %v; want %s", name, found, err, b.ID)
			}
		}
		if found, err := repo.GetByName(ctx, "EngineeringMonthly"); err != nil || found != nil {
			t.Errorf("GetByName without the space = %v, %v; want nil, nil — spaces are significant", found, err)
		}
		if found, err := repo.GetByID(ctx, idgen.New()); err != nil || found != nil {
			t.Errorf("GetByID(absent) = %v, %v; want nil, nil", found, err)
		}
	})
}

func TestBudgetsRepo_CreateRefusesWhatTheSchemaMayNot(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()

		supplied := strings.ToUpper(idgen.New())
		b := createBudget(t, repo, &Budget{ID: supplied, Name: "Supplied id"})
		if b.ID != strings.ToLower(supplied) {
			t.Errorf("stored id %q, want the canonical form of %q", b.ID, supplied)
		}
		if got, err := repo.GetByID(ctx, supplied); err != nil || got == nil {
			t.Errorf("GetByID(supplied uppercase id) = %v, %v; want the row", got, err)
		}

		for _, c := range []struct {
			name string
			b    *Budget
			want error
		}{
			{"non-UUID id", &Budget{ID: "b1", Name: "Bad id", SubjectType: BudgetSubjectUser}, ErrBudgetInvalidID},
			{"agent tier", &Budget{Name: "Agents", SubjectType: "agent"}, ErrBudgetUnknownTier},
			{"unknown tier", &Budget{Name: "Teams", SubjectType: "team"}, ErrBudgetUnknownTier},
			{"no tier", &Budget{Name: "Nothing"}, ErrBudgetUnknownTier},
		} {
			if err := repo.Create(ctx, c.b); !errors.Is(err, c.want) {
				t.Errorf("%s: Create = %v, want %v", c.name, err, c.want)
			}
		}
		if n, err := repo.Count(ctx, BudgetFilter{}); err != nil || n != 1 {
			t.Errorf("Count = %d, %v; refused creates must insert nothing", n, err)
		}
	})
}

func TestBudgetsRepo_Ids(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		b := createBudget(t, repo, &Budget{Name: "Engineering"})

		const malformed = "b1"
		if got, err := repo.GetByID(ctx, malformed); err != nil || got != nil {
			t.Errorf("GetByID(%q) = %v, %v; want nil, nil", malformed, got, err)
		}
		for name, call := range map[string]func() error{
			"Update":     func() error { return repo.Update(ctx, BudgetUpdate{ID: malformed, Name: "x-name"}) },
			"SetDefault": func() error { _, _, err := repo.SetDefault(ctx, malformed, "a", 1); return err },
			"Delete":     func() error { _, err := repo.Delete(ctx, malformed); return err },
		} {
			if err := call(); !errors.Is(err, ErrBudgetNotFound) {
				t.Errorf("%s(%q) = %v, want ErrBudgetNotFound", name, malformed, err)
			}
		}

		upper := strings.ToUpper(b.ID)
		if got, err := repo.GetByID(ctx, upper); err != nil || got == nil || got.ID != b.ID {
			t.Errorf("GetByID(uppercase) = %v, %v; want %s", got, err, b.ID)
		}
		if _, err := repo.Delete(ctx, upper); err != nil {
			t.Errorf("Delete(uppercase) = %v", err)
		}
		if got, _ := repo.GetByID(ctx, b.ID); got != nil {
			t.Error("budget survived Delete by uppercase id")
		}
	})
}

func TestBudgetsRepo_UpdateMaintainsNameLower(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		b := createBudget(t, repo, &Budget{Name: "Old Name", Amount: new(int64(100)), WarnPercentage: new(50)})

		if err := repo.Update(ctx, BudgetUpdate{
			ID: b.ID, Name: "  New Name ", Amount: new(int64(250)), WarnPercentage: new(90),
			UpdatedBy: "editor", UpdatedTime: b.UpdatedTime + 1000, ExpectedUpdatedTime: b.UpdatedTime,
		}); err != nil {
			t.Fatalf("update: %v", err)
		}

		if found, err := repo.GetByName(ctx, "new name"); err != nil || found == nil || found.ID != b.ID {
			t.Errorf("GetByName(new name) after rename = %v, %v; want %s", found, err, b.ID)
		}
		if found, err := repo.GetByName(ctx, "old name"); err != nil || found != nil {
			t.Errorf("GetByName(old name) after rename = %v, %v; want nil, nil", found, err)
		}
		got := mustGetBudget(t, repo, b.ID)
		if got.Name != "New Name" || got.NameLower != "new name" ||
			*got.Amount != 250 || *got.WarnPercentage != 90 ||
			got.UpdatedBy != "editor" || got.UpdatedTime != b.UpdatedTime+1000 ||
			got.CreatedBy != "creator" || got.CreatedTime != b.CreatedTime {
			t.Errorf("after update: %+v", got)
		}

		if err := repo.Update(ctx, BudgetUpdate{ID: b.ID, Name: "New Name", UpdatedBy: "editor", UpdatedTime: 1, ExpectedUpdatedTime: b.UpdatedTime + 1000}); err != nil {
			t.Fatalf("clear update: %v", err)
		}
		if got := mustGetBudget(t, repo, b.ID); got.Amount != nil || got.WarnPercentage != nil {
			t.Errorf("after clearing: amount=%v warn=%v, want nil", got.Amount, got.WarnPercentage)
		}

		if err := repo.Update(ctx, BudgetUpdate{ID: idgen.New(), Name: "Absent"}); !errors.Is(err, ErrBudgetNotFound) {
			t.Errorf("Update(absent) = %v, want ErrBudgetNotFound", err)
		}
	})
}

func TestBudgetsRepo_UpdateRefusesAStaleRead(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		b := createBudget(t, repo, &Budget{Name: "Engineering", Amount: new(int64(100)), WarnPercentage: new(50)})

		first := BudgetUpdate{ID: b.ID, Name: b.Name, UpdatedBy: "a", UpdatedTime: b.UpdatedTime + 1, ExpectedUpdatedTime: b.UpdatedTime}
		if err := repo.Update(ctx, first); err != nil {
			t.Fatalf("first edit: %v", err)
		}
		second := BudgetUpdate{ID: b.ID, Name: b.Name, Amount: b.Amount, WarnPercentage: new(80), UpdatedBy: "b", UpdatedTime: b.UpdatedTime + 2, ExpectedUpdatedTime: b.UpdatedTime}
		if err := repo.Update(ctx, second); !errors.Is(err, ErrBudgetStale) {
			t.Fatalf("second edit from the same read = %v, want ErrBudgetStale", err)
		}
		if got := mustGetBudget(t, repo, b.ID); got.Amount != nil || got.WarnPercentage != nil || got.UpdatedBy != "a" {
			t.Errorf("after the refused edit: %+v, want the first edit's unlimited budget", got)
		}

		if err := repo.Update(ctx, BudgetUpdate{ID: b.ID, Name: "ENGINEERING", UpdatedBy: "c", UpdatedTime: b.UpdatedTime + 3, ExpectedUpdatedTime: b.UpdatedTime + 1}); err != nil {
			t.Fatalf("case-only rename: %v", err)
		}
		if got := mustGetBudget(t, repo, b.ID); got.Name != "ENGINEERING" || got.NameLower != "engineering" {
			t.Errorf("after case-only rename: %+v", got)
		}
	})
}

func TestBudgetsRepo_UnicodeNames(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		createBudget(t, repo, &Budget{Name: "Éngineering"})
		err := repo.Create(ctx, &Budget{Name: "éngineering", SubjectType: BudgetSubjectUser})
		if !errors.Is(err, ErrBudgetNameTaken) {
			t.Errorf("case-differing non-ASCII name: %v, want ErrBudgetNameTaken", err)
		}

		for _, needle := range []string{"É", "é", "ÉNG", "éngin", "  éngin  "} {
			got, err := repo.List(ctx, BudgetFilter{Name: needle})
			if err != nil || len(got) != 1 || got[0].Name != "Éngineering" {
				t.Errorf("List(Name=%q) = %v, %v; want the Éngineering budget", needle, budgetNames(got), err)
			}
		}
	})
}

func TestBudgetsRepo_NameFilterEscapesWildcards(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		for _, name := range []string{"50% off", "500 off", "snake_case", "snakeXcase", `back\slash`, "backslash"} {
			createBudget(t, repo, &Budget{Name: name})
		}
		for needle, want := range map[string]string{"50%": "50% off", "e_c": "snake_case", `k\s`: `back\slash`} {
			got, err := repo.List(ctx, BudgetFilter{Name: needle})
			if err != nil || len(got) != 1 || got[0].Name != want {
				t.Errorf("List(Name=%q) = %v, %v; want only %q", needle, budgetNames(got), err, want)
			}
		}
	})
}

func budgetNames(bs []*Budget) []string {
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.Name
	}
	return names
}

func TestBudgetsRepo_ListSorts(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		for _, b := range []*Budget{
			{Name: "echo"}, {Name: "delta", Amount: new(int64(300))}, {Name: "charlie", Amount: new(int64(100))},
			{Name: "bravo"}, {Name: "alpha", Amount: new(int64(300))},
		} {
			createBudget(t, repo, b)
		}
		for _, c := range []struct {
			name   string
			filter BudgetFilter
			want   []string
		}{
			{"name, by default", BudgetFilter{}, []string{"alpha", "bravo", "charlie", "delta", "echo"}},
			{"name descending", BudgetFilter{SortBy: BudgetSortName, Descending: true}, []string{"echo", "delta", "charlie", "bravo", "alpha"}},
			{"amount, unlimited last", BudgetFilter{SortBy: BudgetSortAmount}, []string{"charlie", "alpha", "delta", "bravo", "echo"}},
			{"amount descending, unlimited first", BudgetFilter{SortBy: BudgetSortAmount, Descending: true}, []string{"bravo", "echo", "alpha", "delta", "charlie"}},
		} {
			t.Run(c.name, func(t *testing.T) {
				c.filter.SubjectType = BudgetSubjectUser
				all, err := repo.List(ctx, c.filter)
				if err != nil {
					t.Fatal(err)
				}
				if got := budgetNames(all); !slices.Equal(got, c.want) {
					t.Errorf("List = %v, want %v", got, c.want)
				}
				var paged []string
				for offset := 0; offset < len(c.want); offset += 2 {
					f := c.filter
					f.Limit, f.Offset = 2, offset
					page, err := repo.List(ctx, f)
					if err != nil {
						t.Fatal(err)
					}
					paged = append(paged, budgetNames(page)...)
				}
				if !slices.Equal(paged, c.want) {
					t.Errorf("paged = %v, want %v", paged, c.want)
				}
			})
		}
	})
}

func TestBudgetsRepo_ListAndCount(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		for _, name := range []string{"golf", "delta", "alpha", "foxtrot", "charlie", "echo", "bravo"} {
			createBudget(t, repo, &Budget{Name: name})
		}
		createBudget(t, repo, &Budget{Name: "mesh cap", SubjectType: BudgetSubjectMesh})
		delta, err := repo.GetByName(ctx, "delta")
		if err != nil || delta == nil {
			t.Fatalf("get delta: %v", err)
		}
		if _, _, err = repo.SetDefault(ctx, delta.ID, "p", 1); err != nil {
			t.Fatalf("promote delta: %v", err)
		}

		all, err := repo.List(ctx, BudgetFilter{SubjectType: BudgetSubjectUser})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		want := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf"}
		if got := budgetNames(all); !slices.Equal(got, want) {
			t.Errorf("List(user) = %v, want %v", got, want)
		}

		var paged []string
		for offset := 0; offset < len(want); offset += 3 {
			page, err := repo.List(ctx, BudgetFilter{SubjectType: BudgetSubjectUser, Limit: 3, Offset: offset})
			if err != nil {
				t.Fatalf("page at %d: %v", offset, err)
			}
			if len(page) > 3 {
				t.Fatalf("page at %d has %d rows, limit 3", offset, len(page))
			}
			paged = append(paged, budgetNames(page)...)
		}
		if !slices.Equal(paged, want) {
			t.Errorf("paged = %v, want %v", paged, want)
		}

		for _, c := range []struct {
			filter BudgetFilter
			want   int64
		}{
			{BudgetFilter{}, 8},
			{BudgetFilter{SubjectType: BudgetSubjectUser}, 7},
			{BudgetFilter{SubjectType: BudgetSubjectMesh}, 1},
			{BudgetFilter{Name: "o"}, 4},
			{BudgetFilter{SubjectType: BudgetSubjectUser, Name: "cap"}, 0},
			{BudgetFilter{SubjectType: BudgetSubjectUser, Limit: 2}, 7},
			{BudgetFilter{DefaultOnly: true}, 2},
			{BudgetFilter{SubjectType: BudgetSubjectMesh, DefaultOnly: true}, 1},
		} {
			n, err := repo.Count(ctx, c.filter)
			if err != nil || n != c.want {
				t.Errorf("Count(%+v) = %d, %v; want %d", c.filter, n, err, c.want)
			}
			list, err := repo.List(ctx, BudgetFilter{SubjectType: c.filter.SubjectType, Name: c.filter.Name, DefaultOnly: c.filter.DefaultOnly})
			if err != nil || int64(len(list)) != c.want {
				t.Errorf("List(%+v) has %d rows, %v; Count says %d", c.filter, len(list), err, c.want)
			}
		}
	})
}

func TestBudgetsRepo_Conflicts(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		first := createBudget(t, repo, &Budget{Name: "Engineering", IsDefault: true})
		createBudget(t, repo, &Budget{Name: "Mesh cap", SubjectType: BudgetSubjectMesh})
		other := createBudget(t, repo, &Budget{Name: "Sales"})

		for _, c := range []struct {
			name string
			err  error
			want error
		}{
			{"duplicate name on create", repo.Create(ctx, &Budget{Name: "ENGINEERING", SubjectType: BudgetSubjectUser}), ErrBudgetNameTaken},
			{"duplicate name on rename", repo.Update(ctx, BudgetUpdate{ID: other.ID, Name: "engineering", ExpectedUpdatedTime: other.UpdatedTime}), ErrBudgetNameTaken},
			{"second default", repo.Create(ctx, &Budget{Name: "Premium", SubjectType: BudgetSubjectUser, IsDefault: true}), ErrBudgetDefaultTaken},
			{"second mesh budget", repo.Create(ctx, &Budget{Name: "Other mesh", SubjectType: BudgetSubjectMesh}), ErrBudgetMeshExists},
		} {
			if !errors.Is(c.err, c.want) {
				t.Errorf("%s: %v, want %v", c.name, c.err, c.want)
			}
			if _, ok := errors.AsType[*pgconn.PgError](c.err); db.Dialect() == "postgres" && !ok {
				t.Errorf("%s: *pgconn.PgError not reachable through the classified error", c.name)
			}
		}

		err := repo.Create(ctx, &Budget{ID: first.ID, Name: "Collides", SubjectType: BudgetSubjectUser})
		if err == nil || !dbutil.IsUniqueViolation(err) {
			t.Fatalf("duplicate id: %v, want a unique violation", err)
		}
		if isBudgetSentinel(err) {
			t.Errorf("duplicate id classified as a conflict (%v); it must stay unclassified so it surfaces as a 500", err)
		}
	})
}

func TestBudgetsRepo_SetDefault(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		a := createBudget(t, repo, &Budget{Name: "Standard"})
		b := createBudget(t, repo, &Budget{Name: "Premium"})
		mesh := createBudget(t, repo, &Budget{Name: "Mesh cap", SubjectType: BudgetSubjectMesh})

		if demoted, promoted, err := repo.SetDefault(ctx, a.ID, "promoter", 5000); err != nil || !promoted || demoted != "" {
			t.Fatalf("promote a = %q, %v; want nothing demoted", demoted, err)
		}
		if got := defaultsAtTier(t, db, BudgetSubjectUser); !slices.Equal(got, []string{a.ID}) {
			t.Fatalf("defaults = %v, want [a]", got)
		}

		if demoted, promoted, err := repo.SetDefault(ctx, b.ID, "second", 6000); err != nil || !promoted || demoted != a.ID {
			t.Fatalf("promote b over a = %q, %v; want a demoted", demoted, err)
		}
		if got := defaultsAtTier(t, db, BudgetSubjectUser); !slices.Equal(got, []string{b.ID}) {
			t.Fatalf("defaults = %v, want [b]", got)
		}
		if got := mustGetBudget(t, repo, a.ID); got.UpdatedBy != "second" || got.UpdatedTime != 6000 {
			t.Errorf("demoted incumbent stamped %q/%d, want second/6000", got.UpdatedBy, got.UpdatedTime)
		}

		if demoted, promoted, err := repo.SetDefault(ctx, b.ID, "again", 7000); err != nil || promoted || demoted != "" {
			t.Fatalf("re-promote b = %q, %v, %v; want nothing promoted or demoted", demoted, promoted, err)
		}
		if got := mustGetBudget(t, repo, b.ID); !got.IsDefault || got.UpdatedBy != "second" || got.UpdatedTime != 6000 {
			t.Errorf("re-promote rewrote the row: %+v", got)
		}

		if _, promoted, err := repo.SetDefault(ctx, mesh.ID, "p", 1); err != nil || promoted {
			t.Errorf("SetDefault(mesh) = %v, %v; want nothing promoted", promoted, err)
		}
		if got := mustGetBudget(t, repo, mesh.ID); !got.IsDefault || got.UpdatedBy == "p" {
			t.Errorf("SetDefault(mesh) rewrote the row or left it non-default: %+v", got)
		}
		if _, _, err := repo.SetDefault(ctx, idgen.New(), "p", 1); !errors.Is(err, ErrBudgetNotFound) {
			t.Errorf("SetDefault(absent) = %v, want ErrBudgetNotFound", err)
		}
		if got := defaultsAtTier(t, db, BudgetSubjectUser); !slices.Equal(got, []string{b.ID}) {
			t.Errorf("defaults after refused calls = %v, want [b]", got)
		}
	})
}

func TestBudgetsRepo_Delete(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		plain := createBudget(t, repo, &Budget{Name: "Plain"})
		def := createBudget(t, repo, &Budget{Name: "Standard"})
		mesh := createBudget(t, repo, &Budget{Name: "Mesh cap", SubjectType: BudgetSubjectMesh})
		if _, _, err := repo.SetDefault(ctx, def.ID, "p", 1); err != nil {
			t.Fatal(err)
		}

		if _, err := repo.Delete(ctx, plain.ID); err != nil {
			t.Errorf("delete plain: %v", err)
		}
		if _, err := repo.Delete(ctx, def.ID); !errors.Is(err, ErrBudgetIsDefault) {
			t.Errorf("delete default = %v, want ErrBudgetIsDefault", err)
		}
		mustGetBudget(t, repo, def.ID)
		successor := createBudget(t, repo, &Budget{Name: "Successor"})
		if _, _, err := repo.SetDefault(ctx, successor.ID, "p", 2); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Delete(ctx, def.ID); err != nil {
			t.Errorf("delete once demoted: %v", err)
		}
		if _, err := repo.Delete(ctx, mesh.ID); !errors.Is(err, ErrBudgetIsDefault) {
			t.Errorf("delete mesh = %v, want ErrBudgetIsDefault", err)
		}
		mustGetBudget(t, repo, mesh.ID)
		if _, err := repo.Delete(ctx, plain.ID); !errors.Is(err, ErrBudgetNotFound) {
			t.Errorf("delete twice = %v, want ErrBudgetNotFound", err)
		}
	})
}

func TestBudgetsRepo_MeshIsAlwaysTheDefault(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		mesh := createBudget(t, repo, &Budget{Name: "Mesh cap", SubjectType: BudgetSubjectMesh})
		if !mesh.IsDefault || !mustGetBudget(t, repo, mesh.ID).IsDefault {
			t.Fatal("a created mesh budget is not its tier's default")
		}
		if db.Dialect() != "postgres" {
			return
		}
		_, err := db.ExecContext(ctx, `UPDATE budgets SET is_default = 0 WHERE id = ?`, mesh.ID)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.ConstraintName != "ck_budgets_mesh_is_default" {
			t.Errorf("direct demotion of the mesh budget on Postgres = %v, want refusal by ck_budgets_mesh_is_default", err)
		}
	})
}

func TestBudgetsRepo_DeleteRacingAPromotion(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		if db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writers; the race exists only on Postgres")
		}
		ctx := t.Context()
		incumbent := createBudget(t, repo, &Budget{Name: "Incumbent"})
		target := createBudget(t, repo, &Budget{Name: "Target"})
		if _, _, err := repo.SetDefault(ctx, incumbent.ID, "p", 1); err != nil {
			t.Fatal(err)
		}
		assignToBudget(t, db, refA, target.ID)
		if err := db.WithTx(ctx, func(tx *dbutil.Tx) error {
			return insertPendingRow(t, tx, "email", "pending@example.com", "", target.ID)
		}); err != nil {
			t.Fatal(err)
		}

		tx, beginErr := db.BeginTx(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		defer func() { _ = tx.Rollback() }()
		for _, stmt := range []struct {
			sql string
			id  string
		}{
			{`UPDATE budgets SET is_default = 0 WHERE id = ?`, incumbent.ID},
			{`UPDATE budgets SET is_default = 1 WHERE id = ?`, target.ID},
		} {
			if _, err := tx.ExecContext(ctx, stmt.sql, stmt.id); err != nil {
				t.Fatalf("staging transaction: %v", err)
			}
		}

		done := make(chan error, 1)
		go func() { _, err := repo.Delete(ctx, target.ID); done <- err }()

		dbtest.WaitForLockWait(t, db, "SELECT is_default FROM budgets")
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit staging transaction: %v", err)
		}

		if err := <-done; !errors.Is(err, ErrBudgetIsDefault) {
			t.Errorf("Delete racing its promotion = %v, want ErrBudgetIsDefault", err)
		}
		if got := mustGetBudget(t, repo, target.ID); !got.IsDefault || got.AssignedCount != 1 || got.PendingCount != 1 {
			t.Errorf("target after the race: %+v, want it kept as the default with its assignments", got)
		}
	})
}

func TestBudgetsRepo_SetDefaultRace(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		if db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writers; the race exists only on Postgres")
		}
		ctx := t.Context()
		incumbent := createBudget(t, repo, &Budget{Name: "Incumbent"})
		winner := createBudget(t, repo, &Budget{Name: "Winner"})
		loser := createBudget(t, repo, &Budget{Name: "Loser"})
		if _, _, err := repo.SetDefault(ctx, incumbent.ID, "p", 1); err != nil {
			t.Fatal(err)
		}

		tx, beginErr := db.BeginTx(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		defer func() { _ = tx.Rollback() }()
		for _, stmt := range []struct {
			sql string
			id  string
		}{
			{`UPDATE budgets SET is_default = 0 WHERE id = ?`, incumbent.ID},
			{`UPDATE budgets SET is_default = 1 WHERE id = ?`, winner.ID},
		} {
			if _, err := tx.ExecContext(ctx, stmt.sql, stmt.id); err != nil {
				t.Fatalf("staging transaction: %v", err)
			}
		}

		done := make(chan error, 1)
		go func() {
			_, _, err := repo.SetDefault(ctx, loser.ID, "loser", 2)
			done <- err
		}()

		dbtest.WaitForLockWait(t, db, "UPDATE budgets SET is_default = 0")
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit staging transaction: %v", err)
		}

		raceErr := <-done
		if !errors.Is(raceErr, ErrBudgetDefaultConflict) {
			t.Errorf("losing SetDefault = %v, want ErrBudgetDefaultConflict", raceErr)
		}
		if errors.Is(raceErr, ErrBudgetDefaultTaken) {
			t.Error("a lost race reported as ErrBudgetDefaultTaken — an operator error it is not")
		}
		if got := defaultsAtTier(t, db, BudgetSubjectUser); !slices.Equal(got, []string{winner.ID}) {
			t.Errorf("defaults = %v, want exactly the winner", got)
		}
		if _, _, err := repo.SetDefault(ctx, loser.ID, "loser", 3); err != nil {
			t.Errorf("retry: %v", err)
		}
		if got := defaultsAtTier(t, db, BudgetSubjectUser); !slices.Equal(got, []string{loser.ID}) {
			t.Errorf("defaults after retry = %v, want exactly the loser", got)
		}
	})
}

func TestBudgetsRepo_SetDefaultRaceForTheSameBudget(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		if db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writers; the race exists only on Postgres")
		}
		ctx := t.Context()
		incumbent := createBudget(t, repo, &Budget{Name: "Incumbent"})
		target := createBudget(t, repo, &Budget{Name: "Target"})
		if _, _, err := repo.SetDefault(ctx, incumbent.ID, "p", 1); err != nil {
			t.Fatal(err)
		}

		tx, beginErr := db.BeginTx(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		defer func() { _ = tx.Rollback() }()
		for _, stmt := range []struct {
			sql string
			id  string
		}{
			{`UPDATE budgets SET is_default = 0 WHERE id = ?`, incumbent.ID},
			{`UPDATE budgets SET is_default = 1, updated_by = 'first', updated_time = 2 WHERE id = ?`, target.ID},
		} {
			if _, err := tx.ExecContext(ctx, stmt.sql, stmt.id); err != nil {
				t.Fatalf("staging transaction: %v", err)
			}
		}

		type outcome struct {
			demoted  string
			promoted bool
			err      error
		}
		done := make(chan outcome, 1)
		go func() {
			demoted, promoted, err := repo.SetDefault(ctx, target.ID, "second", 3)
			done <- outcome{demoted, promoted, err}
		}()

		dbtest.WaitForLockWait(t, db, "UPDATE budgets SET is_default = 0")
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit staging transaction: %v", err)
		}

		got := <-done
		if got.err != nil || got.promoted || got.demoted != "" {
			t.Errorf("second SetDefault = %q, %v, %v; want nothing promoted or demoted", got.demoted, got.promoted, got.err)
		}
		if b := mustGetBudget(t, repo, target.ID); !b.IsDefault || b.UpdatedBy != "first" || b.UpdatedTime != 2 {
			t.Errorf("target after both = %+v, want the first promotion's stamp", b)
		}
		if got := defaultsAtTier(t, db, BudgetSubjectUser); !slices.Equal(got, []string{target.ID}) {
			t.Errorf("defaults = %v, want exactly the target", got)
		}
	})
}

func assignToBudget(t *testing.T, db *DB, ref, budgetID string) {
	t.Helper()
	insertOrphanAssignment(t, db, ref, budgetID)
}

func TestBudgetsRepo_DeleteTakesItsAssignmentsWithIt(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		assigned := createBudget(t, repo, &Budget{Name: "Engineering"})
		other := createBudget(t, repo, &Budget{Name: "Research"})
		assignToBudget(t, db, refA, assigned.ID)
		assignToBudget(t, db, refB, assigned.ID)
		assignToBudget(t, db, refC, other.ID)
		if err := db.WithTx(ctx, func(tx *dbutil.Tx) error {
			return insertPendingRow(t, tx, "email", "pending@example.com", "", assigned.ID)
		}); err != nil {
			t.Fatal(err)
		}

		deleted, err := repo.Delete(ctx, assigned.ID)
		if err != nil || len(deleted.Assignments) != 2 || len(deleted.Pending) != 1 {
			t.Fatalf("delete assigned = %+v, %v; want 2 assignments and 1 pending deleted", deleted, err)
		}
		if got, _ := repo.GetByID(ctx, assigned.ID); got != nil {
			t.Error("assigned budget survived its delete")
		}
		for table, want := range map[string]int{"budget_assignments": 1, "budget_pending_assignments": 0} {
			if n, err := dbutil.QueryVal[int](ctx, db, `SELECT COUNT(*) FROM `+table); err != nil || n != want {
				t.Errorf("%s after the delete = %d, %v; want %d, the other budget's left alone", table, n, err, want)
			}
		}

		def := createBudget(t, repo, &Budget{Name: "Standard"})
		if _, _, err := repo.SetDefault(ctx, def.ID, "p", 1); err != nil {
			t.Fatal(err)
		}
		assignToBudget(t, db, refD, def.ID)
		if _, err := repo.Delete(ctx, def.ID); !errors.Is(err, ErrBudgetIsDefault) {
			t.Errorf("delete assigned default = %v, want ErrBudgetIsDefault", err)
		}
		if got := mustGetBudget(t, repo, def.ID); got.AssignedCount != 1 {
			t.Errorf("default after the refused delete: %d assigned, want its assignment kept", got.AssignedCount)
		}
	})
}

func TestBudgetsRepo_DeleteRacesAnAssignment(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		ctx := t.Context()
		const insertLate = `INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id,
		                                created_by, updated_by, created_time, updated_time)
		 VALUES ('00000000-0000-7000-8000-00000000000a', 'user', 'system:late', OLD.id, 't', 't', 1, 1);`
		trigger := `CREATE TRIGGER assign_on_budget_delete BEFORE DELETE ON budgets
		 BEGIN ` + insertLate + ` END`
		if db.Dialect() == "postgres" {
			if _, err := db.ExecContext(ctx, `CREATE FUNCTION assign_on_budget_delete() RETURNS trigger
			 LANGUAGE plpgsql AS $$ BEGIN `+insertLate+` RETURN OLD; END $$`); err != nil {
				t.Fatalf("create trigger function: %v", err)
			}
			trigger = `CREATE TRIGGER assign_on_budget_delete BEFORE DELETE ON budgets
			 FOR EACH ROW EXECUTE FUNCTION assign_on_budget_delete()`
		}
		if _, err := db.ExecContext(ctx, trigger); err != nil {
			t.Fatalf("create trigger: %v", err)
		}

		b := createBudget(t, repo, &Budget{Name: "Engineering"})
		assignToBudget(t, db, refA, b.ID)
		if err := db.WithTx(ctx, func(tx *dbutil.Tx) error {
			return insertPendingRow(t, tx, "email", "pending@example.com", "", b.ID)
		}); err != nil {
			t.Fatal(err)
		}
		_, err := repo.Delete(ctx, b.ID)
		if !errors.Is(err, ErrBudgetAssignedWhileDeleting) || !dbutil.IsForeignKeyViolation(err) {
			t.Fatalf("delete = %v, want ErrBudgetAssignedWhileDeleting wrapping the FK refusal", err)
		}
		mustGetBudget(t, repo, b.ID)
		n, err := dbutil.QueryVal[int](ctx, db, `SELECT COUNT(*) FROM budget_assignments`)
		if err != nil || n != 1 {
			t.Errorf("assignments after the refused delete = %d, %v; want the existing one restored and the trigger's insert rolled back", n, err)
		}
		if n, err := dbutil.QueryVal[int](ctx, db, `SELECT COUNT(*) FROM budget_pending_assignments`); err != nil || n != 1 {
			t.Errorf("pending assignments after the refused delete = %d, %v; want the existing one restored", n, err)
		}
	})
}

func TestBudgetsRepo_DeleteDeadlockingAPendingBindIsARetryableRace(t *testing.T) {
	t.Parallel()
	budgetRepoTest(t, func(t *testing.T, db *DB, repo *BudgetsRepo) {
		if db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writers; the deadlock exists only on Postgres")
		}
		ctx := t.Context()
		var superuser bool
		if err := db.QueryRowContext(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&superuser); err != nil {
			t.Fatal(err)
		}
		if !superuser {
			t.Skip("staging which side the deadlock aborts sets deadlock_timeout, which takes a superuser")
		}
		b := createBudget(t, repo, &Budget{Name: "Engineering"})
		assignToBudget(t, db, refA, b.ID)
		if err := db.WithTx(ctx, func(tx *dbutil.Tx) error {
			return insertPendingRow(t, tx, "email", "pending@example.com", "", b.ID)
		}); err != nil {
			t.Fatal(err)
		}

		blocker, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Rollback() }()
		if _, err = blocker.ExecContext(ctx, `SELECT id FROM budget_assignments WHERE budget_id = ? FOR UPDATE`, b.ID); err != nil {
			t.Fatalf("lock the assignment row: %v", err)
		}
		bind, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = bind.Rollback() }()
		if _, err = bind.ExecContext(ctx, `SET LOCAL deadlock_timeout = '30s'`); err != nil {
			t.Fatalf("lengthen the bind's deadlock_timeout: %v", err)
		}
		if _, err = bind.ExecContext(ctx, `SELECT id FROM budget_pending_assignments WHERE budget_id = ? FOR UPDATE`, b.ID); err != nil {
			t.Fatalf("lock the pending row: %v", err)
		}

		deleteDone := make(chan error, 1)
		go func() { _, deleteErr := repo.Delete(ctx, b.ID); deleteDone <- deleteErr }()
		dbtest.WaitForLockWait(t, db, "DELETE FROM budget_assignments")
		bindDone := make(chan error, 1)
		go func() {
			_, insertErr := bind.ExecContext(ctx, `INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id,
			                                created_by, updated_by, created_time, updated_time)
			 VALUES (?, 'user', ?, ?, 't', 't', 1, 1)`, idgen.New(), refB, b.ID)
			bindDone <- insertErr
		}()
		dbtest.WaitForLockWait(t, db, "INSERT INTO budget_assignments")
		if err := blocker.Rollback(); err != nil {
			t.Fatalf("release the assignment row: %v", err)
		}

		if err := <-deleteDone; !errors.Is(err, ErrBudgetAssignedWhileDeleting) || !dbutil.IsDeadlock(err) {
			t.Fatalf("delete = %v, want ErrBudgetAssignedWhileDeleting wrapping the deadlock", err)
		}
		if err := <-bindDone; err != nil {
			t.Fatalf("bind insert after the delete aborted: %v", err)
		}
		if err := bind.Commit(); err != nil {
			t.Fatalf("commit the bind: %v", err)
		}
		if got := mustGetBudget(t, repo, b.ID); got.AssignedCount != 2 || got.PendingCount != 1 {
			t.Errorf("budget after the aborted delete: %+v, want its assignment restored beside the bind's, and its pending row kept", got)
		}
	})
}
