// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type budgetFixture struct {
	name        string
	subjectType string
	amount      *int64
	warn        *int
	isDefault   int
}

func userBudget(name string) budgetFixture {
	return budgetFixture{name: name, subjectType: "user", amount: new(int64(100))}
}

func (f budgetFixture) insert(t *testing.T, tx *dbutil.Tx) error {
	t.Helper()
	return f.insertWithID(t, tx, idgen.New())
}

func (f budgetFixture) insertWithID(t *testing.T, tx *dbutil.Tx, id string) error {
	t.Helper()
	_, err := tx.ExecContext(t.Context(),
		`INSERT INTO budgets (id, name, name_lower, subject_type, amount, warn_percentage,
		                      is_default, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'test', 'test', 1, 1)`,
		id, f.name, BudgetNameLower(f.name), f.subjectType, f.amount, f.warn, f.isDefault)
	return err
}

type violation struct {
	kind   string
	target string
}

func violationOf(t *testing.T, err error) violation {
	t.Helper()
	if err == nil {
		t.Fatal("write succeeded; want a constraint violation")
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "23514":
			return violation{"check", pgErr.ConstraintName}
		case "23505":
			return violation{"unique", pgErr.ConstraintName}
		}
		t.Fatalf("postgres error %s is not a check or unique violation: %v", pgErr.Code, err)
	}
	for kind, marker := range map[string]string{"check": "CHECK constraint failed: ", "unique": "UNIQUE constraint failed: "} {
		if _, rest, ok := strings.Cut(err.Error(), marker); ok {
			target, _, _ := strings.Cut(rest, " (")
			return violation{kind, target}
		}
	}
	t.Fatalf("not a constraint violation: %v", err)
	return violation{}
}

func withRollback(t *testing.T, db *DB, fn func(tx *dbutil.Tx)) {
	t.Helper()
	tx, err := db.BeginTx(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	fn(tx)
}

func TestBudgetsSchema_Checks(t *testing.T) {
	t.Parallel()

	with := func(mutate func(*budgetFixture)) budgetFixture {
		f := userBudget("Engineering")
		mutate(&f)
		return f
	}

	rejected := []struct {
		name         string
		row          budgetFixture
		constraint   string
		postgresOnly bool
	}{
		{"warn 0", with(func(f *budgetFixture) { f.warn = new(0) }), "ck_budgets_warn_range", false},
		{"warn 101", with(func(f *budgetFixture) { f.warn = new(101) }), "ck_budgets_warn_range", false},
		{"name 2 chars", with(func(f *budgetFixture) { f.name = "ab" }), "ck_budgets_name_length", false},
		{"name 2 multibyte chars", with(func(f *budgetFixture) { f.name = "Éé" }), "ck_budgets_name_length", false},
		{"name 256 chars", with(func(f *budgetFixture) { f.name = strings.Repeat("a", 256) }), "ck_budgets_name_length", false},
		{"negative amount", with(func(f *budgetFixture) { f.amount = new(int64(-1)) }), "ck_budgets_amount_nonneg", false},
		{"warn with null amount", with(func(f *budgetFixture) { f.amount, f.warn = nil, new(50) }), "ck_budgets_warn_requires_amount", false},
		{"is_default 2", with(func(f *budgetFixture) { f.isDefault = 2 }), "ck_budgets_is_default_binary", false},
		{"unknown tier", with(func(f *budgetFixture) { f.subjectType = "agent" }), "ck_budgets_subject_type", true},
		{"mesh not default", with(func(f *budgetFixture) { f.subjectType, f.isDefault = "mesh", 0 }), "ck_budgets_mesh_is_default", true},
	}

	accepted := []struct {
		name string
		row  budgetFixture
	}{
		{"warn 1", with(func(f *budgetFixture) { f.warn = new(1) })},
		{"warn 100", with(func(f *budgetFixture) { f.warn = new(100) })},
		{"name 3 chars", with(func(f *budgetFixture) { f.name = "abc" })},
		{"name 3 multibyte chars", with(func(f *budgetFixture) { f.name = "Ééé" })},
		{"name 255 chars", with(func(f *budgetFixture) { f.name = strings.Repeat("a", 255) })},
		{"zero amount", with(func(f *budgetFixture) { f.amount = new(int64(0)) })},
		{"unlimited without warn", with(func(f *budgetFixture) { f.amount, f.warn = nil, nil })},
		{"is_default 1", with(func(f *budgetFixture) { f.isDefault = 1 })},
		{"mesh default", with(func(f *budgetFixture) { f.subjectType, f.isDefault = "mesh", 1 })},
	}

	forEachPlatformDialectAt(t, "budgets", func(t *testing.T, db *DB, version int64) {
		if err := dbutil.MigrateUpTo(t.Context(), db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		removeSeededBudgets(t, db)
		for _, c := range rejected {
			t.Run("rejects "+c.name, func(t *testing.T) {
				if c.postgresOnly && db.Dialect() != "postgres" {
					t.Skip("Postgres-only CHECK; the SQLite migration omits it by design")
				}
				withRollback(t, db, func(tx *dbutil.Tx) {
					got := violationOf(t, c.row.insert(t, tx))
					if want := (violation{"check", c.constraint}); got != want {
						t.Errorf("refused by %+v, want %+v", got, want)
					}
				})
			})
		}
		for _, c := range accepted {
			t.Run("accepts "+c.name, func(t *testing.T) {
				withRollback(t, db, func(tx *dbutil.Tx) {
					if err := c.row.insert(t, tx); err != nil {
						t.Errorf("insert refused: %v", err)
					}
				})
			})
		}
	})
}

func TestBudgetsSchema_UniqueIndexes(t *testing.T) {
	t.Parallel()

	mesh := func(name string) budgetFixture {
		return budgetFixture{name: name, subjectType: "mesh", amount: new(int64(100)), isDefault: 1}
	}
	userDefault := func(name string) budgetFixture {
		f := userBudget(name)
		f.isDefault = 1
		return f
	}

	const (
		defaultPG, defaultSQLite     = "ux_budgets_default", "budgets.subject_type, budgets.is_default"
		singletonPG, singletonSQLite = "ux_budgets_mesh_singleton", "budgets.subject_type"
	)
	nonDefaultMesh := func(name string) budgetFixture {
		f := mesh(name)
		f.isDefault = 0
		return f
	}
	rejected := []struct {
		name          string
		first, second budgetFixture
		postgres      []string
		sqlite        []string
		sqliteOnly    bool
	}{
		{"case-differing name", userBudget("Engineering"), userBudget("engineering"),
			[]string{"ux_budgets_name"}, []string{"budgets.name_lower"}, false},
		{"second default at a tier", userDefault("Standard"), userDefault("Premium"),
			[]string{defaultPG}, []string{defaultSQLite}, false},
		{"second mesh budget", mesh("Mesh cap"), mesh("Other mesh cap"),
			[]string{defaultPG, singletonPG}, []string{defaultSQLite, singletonSQLite}, false},
		{"second non-default mesh budget", nonDefaultMesh("Mesh cap"), nonDefaultMesh("Other mesh cap"),
			nil, []string{singletonSQLite}, true},
	}

	accepted := []struct {
		name string
		rows []budgetFixture
	}{
		{"many non-default budgets at a tier", []budgetFixture{userBudget("Standard"), userBudget("Premium"), userBudget("Basic")}},
		{"one default beside non-defaults", []budgetFixture{userDefault("Standard"), userBudget("Premium")}},
		{"mesh budget beside user budgets", []budgetFixture{mesh("Mesh cap"), userDefault("Standard"), userBudget("Premium")}},
	}

	forEachPlatformDialectAt(t, "budgets", func(t *testing.T, db *DB, version int64) {
		if err := dbutil.MigrateUpTo(t.Context(), db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		removeSeededBudgets(t, db)
		for _, c := range rejected {
			t.Run("rejects "+c.name, func(t *testing.T) {
				targets := c.sqlite
				if db.Dialect() == "postgres" {
					if c.sqliteOnly {
						t.Skip("Postgres refuses these rows by CHECK before any index")
					}
					targets = c.postgres
				}
				withRollback(t, db, func(tx *dbutil.Tx) {
					if err := c.first.insert(t, tx); err != nil {
						t.Fatalf("first insert: %v", err)
					}
					got := violationOf(t, c.second.insert(t, tx))
					if got.kind != "unique" || !slices.Contains(targets, got.target) {
						t.Errorf("refused by %+v, want a unique violation of one of %v", got, targets)
					}
				})
			})
		}
		for _, c := range accepted {
			t.Run("accepts "+c.name, func(t *testing.T) {
				withRollback(t, db, func(tx *dbutil.Tx) {
					for _, row := range c.rows {
						if err := row.insert(t, tx); err != nil {
							t.Fatalf("insert %q refused: %v", row.name, err)
						}
					}
				})
			})
		}
	})
}
