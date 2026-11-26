// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqlitegate

import (
	"database/sql"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

const (
	peopleDefinition = `CREATE TABLE "people" ("name" NUMERIC, "age" NUMERIC, "city" NUMERIC, "profile" NUMERIC)`
	citiesDefinition = `CREATE TABLE "cities" ("city" NUMERIC, "country" NUMERIC)`

	dottedCapitalI = "İ"
	kelvinSign     = "K"

	pagesLookalike       = "sql" + dottedCapitalI + "te_dbpage"
	databaseLookalike    = "pragma_database_l" + dottedCapitalI + "st"
	foreignKeysLookalike = "pragma_foreign_" + kelvinSign + "ey_list"

	actionCreateTrigger = 7
	actionDelete        = 9
	actionInsert        = 18
	actionPragma        = 19
	actionRead          = 20
	actionTransaction   = 22
	actionUpdate        = 23
	actionAttach        = 24
	actionDetach        = 25
	actionAlterTable    = 26
	actionFunction      = 31
	actionSavepoint     = 32

	resultError = 1
	resultAuth  = 23
)

var testDefinitions = []string{
	peopleDefinition,
	citiesDefinition,
	`CREATE TABLE "odd ""name""" ("a b" NUMERIC)`,
	`CREATE TABLE "Sales" ("Region" NUMERIC, "amount" NUMERIC)`,
	`CREATE TABLE "Émile" ("x" NUMERIC)`,
}

func refusalOrAdmitted(t *testing.T, err error) Refusal {
	t.Helper()
	if err == nil {
		return Refusal{}
	}
	refusal, ok := errors.AsType[*Refusal](err)
	if !ok {
		t.Fatalf("got %v (%T), want a *Refusal or nil", err, err)
	}
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("a *Refusal does not match ErrRefused: %v", err)
	}
	return *refusal
}

func TestCheckAcceptsSingleReadOnlySelects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
	}{
		{"projection", `SELECT name, age FROM people WHERE city = 'NYC' ORDER BY name LIMIT 5`},
		{"star", `SELECT * FROM people`},
		{"count without column", `SELECT count(*) FROM people`},
		{"count, table written in another case", `SELECT count(*) FROM PEOPLE`},
		{"count qualified", `SELECT count(*) FROM main.people`},
		{"count, database written in another case", `SELECT count(*) FROM MAIN.people`},
		{"count of a cte", `WITH adults AS (SELECT * FROM people WHERE age >= 18) SELECT count(*) FROM adults`},
		{"count of a subquery", `SELECT count(*) FROM (SELECT DISTINCT city FROM people) AS c`},
		{"count of json items", `SELECT count(*) FROM json_each('[1,2,3]')`},
		{"count of columns", `SELECT count(*) FROM pragma_table_info('people')`},
		{"exists", `SELECT EXISTS (SELECT 1 FROM cities) AS any_city`},
		{"join", `SELECT p.name, c.country FROM people p LEFT JOIN cities c ON c.city = p.city`},
		{"aggregate", `SELECT city, count(*) AS n, avg(age), group_concat(name, ', ') FROM people GROUP BY city HAVING n > 1`},
		{"cte", `WITH adults AS (SELECT * FROM people WHERE age >= 18) SELECT city, sum(age) FROM adults GROUP BY city`},
		{"recursive cte", `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n WHERE x < 5) SELECT x FROM n`},
		{"window", `SELECT name, row_number() OVER (PARTITION BY city ORDER BY age DESC) AS rn, lag(age) OVER (ORDER BY age) FROM people`},
		{"subquery", `SELECT name FROM people WHERE age > (SELECT avg(age) FROM people)`},
		{"compound", `SELECT city FROM people UNION SELECT city FROM cities EXCEPT SELECT 'nowhere'`},
		{"compound aggregates", `SELECT 'city' AS dim, city AS k, COUNT(*) AS n, ROUND(SUM(age), 2) AS amt FROM people GROUP BY city UNION ALL SELECT 'all', 'all', COUNT(*), ROUND(SUM(age), 2) FROM people UNION ALL SELECT 'cities', 'distinct', COUNT(DISTINCT city), 0 FROM people UNION ALL SELECT 'max', name, 1, age FROM people WHERE age = (SELECT MAX(age) FROM people)`},
		{"case and math", `SELECT CASE WHEN age > 30 THEN 'senior' ELSE 'junior' END, round(sqrt(age), 2), abs(-1) FROM people`},
		{"date functions", `SELECT date('now'), strftime('%Y', '2024-01-02'), julianday('2024-01-02') FROM people`},
		{"json scalar", `SELECT json_extract(profile, '$.team'), profile ->> '$.team', json_object('n', name) FROM people`},
		{"json table function", `SELECT p.name, j.value FROM people p, json_each(p.profile, '$.tags') j`},
		{"json tree", `SELECT key, value FROM json_tree('{"a":{"b":1}}')`},
		{"json aggregate", `SELECT json_group_array(name) FROM people`},
		{"quoted identifiers", `SELECT "a b" FROM "odd ""name"""`},
		{"schema catalog", `SELECT name, sql FROM sqlite_master WHERE type = 'table'`},
		{"schema catalog alias", `SELECT name FROM sqlite_schema`},
		{"column listing", `SELECT name, type FROM pragma_table_info('people')`},
		{"no table", `SELECT 1 + 1 AS two, sqlite_version()`},
		{"values", `VALUES (1, 'a'), (2, 'b')`},
		{"trailing semicolon", `SELECT name FROM people;`},
		{"trailing whitespace", "SELECT name FROM people ;  \n\t "},
		{"trailing comment", "SELECT name FROM people; -- that is all\n/* really */"},
		{"leading comment", "/* names */ -- first\nSELECT name FROM people"},
		{"leading empty statement", `; SELECT name FROM people`},
		{"semicolon in string", `SELECT 'a;b' AS v, "name" FROM people WHERE name <> ';'`},
		{"semicolon in comment", "SELECT name /* ; */ FROM people -- ; DROP TABLE people"},
		{"keyword in string", `SELECT 'ATTACH DATABASE; PRAGMA; DROP' FROM people`},
		{"unbound parameter", `SELECT name FROM people WHERE age > ?`},
		{"mixed-case table, as defined", `SELECT Region FROM Sales`},
		{"mixed-case table, upper case", `SELECT Region FROM SALES`},
		{"mixed-case table, lower case", `SELECT region FROM sales`},
		{"non-ASCII table by its exact name", `SELECT x FROM "Émile"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := CheckSingleReadOnlySelect(testDefinitions, tc.query); err != nil {
				t.Fatalf("CheckSingleReadOnlySelect(%q) = %v, want nil", tc.query, err)
			}
		})
	}
}

func TestCheckRefusesEverythingElse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	elsewhere := dir + "/elsewhere.db"
	tests := []struct {
		name  string
		query string
		want  Refusal
	}{
		{"two selects", `SELECT 1; SELECT 2`, Refusal{Reason: "multiple_statements"}},
		{"empty statement then two selects", `; SELECT 1; SELECT 2`, Refusal{Reason: "multiple_statements"}},
		{"second select after a remark", "SELECT 1; -- next\nSELECT 2", Refusal{Reason: "multiple_statements"}},
		{"select then a statement the authorizer is not asked about", `SELECT 1; VACUUM`, Refusal{Reason: "multiple_statements"}},
		{"select then a denied statement", `SELECT 1; DROP TABLE people`, Refusal{Reason: "denied_action", Action: actionDelete}},
		{"denied statement then select", `PRAGMA query_only = OFF; SELECT 1`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"empty", ``, Refusal{Reason: "no_statement"}},
		{"only whitespace", " \n\t", Refusal{Reason: "no_statement"}},
		{"only a comment", `-- nothing to run`, Refusal{Reason: "no_statement"}},
		{"only semicolons", `;;`, Refusal{Reason: "no_statement"}},

		{"attach", `ATTACH DATABASE '` + elsewhere + `' AS e`, Refusal{Reason: "denied_action", Action: actionAttach}},
		{"attach memory", `ATTACH DATABASE ':memory:' AS e`, Refusal{Reason: "denied_action", Action: actionAttach}},
		{"attach then read", `ATTACH DATABASE '` + elsewhere + `' AS e; SELECT * FROM e.sqlite_master`, Refusal{Reason: "denied_action", Action: actionAttach}},
		{"detach", `DETACH DATABASE main`, Refusal{Reason: "denied_action", Action: actionDetach}},
		{"vacuum", `VACUUM`, Refusal{Reason: "not_read_only_select"}},
		{"vacuum into", `VACUUM INTO '` + elsewhere + `'`, Refusal{Reason: "not_read_only_select"}},
		{"vacuum main into", `VACUUM main INTO '` + elsewhere + `'`, Refusal{Reason: "not_read_only_select"}},
		{"load extension", `SELECT load_extension('` + elsewhere + `')`, Refusal{Reason: "denied_action", Action: actionFunction}},
		{"load extension in a filter", `SELECT name FROM people WHERE load_extension('x', 'y') IS NULL`, Refusal{Reason: "denied_action", Action: actionFunction}},

		{"pragma query_only", `PRAGMA query_only = OFF`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"pragma journal_mode", `PRAGMA journal_mode = DELETE`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"pragma temp_store", `PRAGMA temp_store = FILE`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"pragma temp_store_directory", `PRAGMA temp_store_directory = '` + dir + `'`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"pragma writable_schema", `PRAGMA writable_schema = ON`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"pragma trusted_schema", `PRAGMA trusted_schema = ON`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"pragma locking_mode", `PRAGMA main.locking_mode = EXCLUSIVE`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"pragma that only reads", `PRAGMA table_info(people)`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"pragma database_list", `PRAGMA database_list`, Refusal{Reason: "denied_action", Action: actionPragma}},
		{"pragma table function outside the readable two", `SELECT * FROM pragma_database_list`, Refusal{Reason: "denied_action", Action: actionRead}},

		{"insert", `INSERT INTO people (name) VALUES ('x')`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"insert returning", `INSERT INTO people (name) VALUES ('x') RETURNING *`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"update", `UPDATE people SET age = 0`, Refusal{Reason: "denied_action", Action: actionUpdate}},
		{"update returning", `UPDATE people SET age = 0 RETURNING name`, Refusal{Reason: "denied_action", Action: actionUpdate}},
		{"delete", `DELETE FROM people`, Refusal{Reason: "denied_action", Action: actionDelete}},
		{"delete returning", `DELETE FROM people RETURNING name`, Refusal{Reason: "denied_action", Action: actionDelete}},
		{"replace", `REPLACE INTO people (name) VALUES ('x')`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"cte insert", `WITH x AS (SELECT 'y') INSERT INTO people (name) SELECT * FROM x`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"create table", `CREATE TABLE t (x)`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"create table as", `CREATE TABLE t AS SELECT * FROM people`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"create temp table", `CREATE TEMP TABLE t (x)`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"create view", `CREATE VIEW v AS SELECT * FROM people`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"create temp view", `CREATE TEMP VIEW v AS SELECT * FROM people`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"create index", `CREATE INDEX i ON people (name)`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"create trigger", `CREATE TRIGGER tr AFTER INSERT ON people BEGIN DELETE FROM cities; END`, Refusal{Reason: "denied_action", Action: actionCreateTrigger}},
		{"create virtual table", `CREATE VIRTUAL TABLE v USING fts5(body)`, Refusal{Reason: "denied_action", Action: actionInsert}},
		{"alter rename", `ALTER TABLE people RENAME TO persons`, Refusal{Reason: "denied_action", Action: actionAlterTable}},
		{"alter add column", `ALTER TABLE people ADD COLUMN extra`, Refusal{Reason: "denied_action", Action: actionAlterTable}},
		{"drop table", `DROP TABLE people`, Refusal{Reason: "denied_action", Action: actionDelete}},
		{"reindex", `REINDEX`, Refusal{Reason: "not_read_only_select"}},
		{"analyze", `ANALYZE`, Refusal{Reason: "denied_action", Action: actionInsert}},

		{"begin", `BEGIN`, Refusal{Reason: "denied_action", Action: actionTransaction}},
		{"commit", `COMMIT`, Refusal{Reason: "denied_action", Action: actionTransaction}},
		{"rollback", `ROLLBACK`, Refusal{Reason: "denied_action", Action: actionTransaction}},
		{"savepoint", `SAVEPOINT s`, Refusal{Reason: "denied_action", Action: actionSavepoint}},
		{"release", `RELEASE s`, Refusal{Reason: "denied_action", Action: actionSavepoint}},

		{"explain", `EXPLAIN SELECT name FROM people`, Refusal{Reason: "not_read_only_select"}},
		{"explain query plan", `EXPLAIN QUERY PLAN SELECT name FROM people`, Refusal{Reason: "not_read_only_select"}},
		{"explain of a copy to a file", `EXPLAIN VACUUM INTO '` + elsewhere + `'`, Refusal{Reason: "not_read_only_select"}},

		{"raw pages", `SELECT * FROM sqlite_dbpage`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"raw page count", `SELECT count(*) FROM sqlite_dbpage`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"page statistics", `SELECT * FROM dbstat`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"page statistics count", `SELECT count(*) FROM dbstat`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"pragma table function count", `SELECT count(*) FROM pragma_database_list`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"pragma table function given a setting", `SELECT count(*) FROM pragma_journal_mode('delete')`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"temp catalog", `SELECT * FROM sqlite_temp_master`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"temp catalog count", `SELECT count(*) FROM temp.sqlite_master`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"cte named as a built-in table", `WITH dbstat AS (SELECT 1) SELECT count(*) FROM dbstat`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"log function", `SELECT sqlite_log(1, 'x')`, Refusal{Reason: "denied_action", Action: actionFunction}},
		{"fts function", `SELECT fts5_source_id()`, Refusal{Reason: "denied_action", Action: actionFunction}},
		{"rtree function", `SELECT rtreecheck('people')`, Refusal{Reason: "denied_action", Action: actionFunction}},
		{"geopoly function", `SELECT geopoly_area('[[0,0],[1,0],[1,1],[0,0]]')`, Refusal{Reason: "denied_action", Action: actionFunction}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := refusalOrAdmitted(t, CheckSingleReadOnlySelect(testDefinitions, tc.query))
			if got != tc.want {
				t.Errorf("CheckSingleReadOnlySelect(%q) = %+v, want %+v", tc.query, got, tc.want)
			}
		})
	}
	t.Cleanup(func() {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("directory holds %d entries (%v), want none: a check touched the disk", len(entries), err)
		}
	})
}

func TestCheckComparesTableNamesAsSQLiteDoes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		table string
		query string
		want  Refusal
	}{
		{"dotted capital I: the raw page table stays refused", pagesLookalike, `SELECT * FROM sqlite_dbpage`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"dotted capital I: the raw page count stays refused", pagesLookalike, `SELECT count(*) FROM sqlite_dbpage`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"dotted capital I: the table itself is readable", pagesLookalike, `SELECT x FROM "` + pagesLookalike + `"`, Refusal{}},
		{"dotted capital I: a pragma table function stays refused", databaseLookalike, `SELECT * FROM pragma_database_list`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"dotted capital I: that table itself is readable", databaseLookalike, `SELECT x FROM "` + databaseLookalike + `"`, Refusal{}},
		{"kelvin sign: a pragma table function stays refused", foreignKeysLookalike, `SELECT * FROM pragma_foreign_key_list('people')`, Refusal{Reason: "denied_action", Action: actionRead}},
		{"kelvin sign: the table itself is readable", foreignKeysLookalike, `SELECT x FROM "` + foreignKeysLookalike + `"`, Refusal{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			definitions := []string{peopleDefinition, `CREATE TABLE "` + tc.table + `" ("x" NUMERIC)`}
			got := refusalOrAdmitted(t, CheckSingleReadOnlySelect(definitions, tc.query))
			if got != tc.want {
				t.Errorf("CheckSingleReadOnlySelect(%q) = %+v, want %+v", tc.query, got, tc.want)
			}
		})
	}
}

func TestCheckReportsCompileErrorsLikeTheDriver(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})
	db.SetMaxOpenConns(1)
	for _, definition := range testDefinitions {
		if _, defineErr := db.Exec(definition); defineErr != nil {
			t.Fatal(defineErr)
		}
	}

	tests := []struct {
		name  string
		query string
	}{
		{"unknown column", `SELECT missing FROM people`},
		{"unknown table", `SELECT * FROM nowhere`},
		{"unknown function", `SELECT no_such_function(name) FROM people`},
		{"syntax", `SELEC name FROM people`},
		{"incomplete", `SELECT name FROM people WHERE`},
		{"syntax in the text after the statement", `SELECT name FROM people; SELEC 2`},
		{"unknown schema", `SELECT * FROM elsewhere.people`},
		{"non-ASCII table name in another case is another, unknown table", `SELECT x FROM "émile"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, driverErr := db.Exec(tc.query)
			if driverErr == nil {
				t.Fatalf("driver accepted %q", tc.query)
			}
			err := CheckSingleReadOnlySelect(testDefinitions, tc.query)
			compileErr, ok := errors.AsType[*CompileError](err)
			if !ok {
				t.Fatalf("CheckSingleReadOnlySelect(%q) = %v, want a *CompileError", tc.query, err)
			}
			if compileErr.Error() != driverErr.Error() {
				t.Errorf("gate says %q, driver says %q", compileErr.Error(), driverErr.Error())
			}
		})
	}
}

func TestCheckReportsADefinitionItCannotUseAsInternal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	elsewhere := dir + "/elsewhere.db"
	tests := []struct {
		name       string
		definition string
		want       InternalError
	}{
		{"incomplete", `CREATE TABLE`, InternalError{Stage: "define", ResultCode: resultError}},
		{"empty", ``, InternalError{Stage: "define"}},
		{"only a comment", `-- nothing`, InternalError{Stage: "define"}},
		{"a table that is already defined", peopleDefinition, InternalError{Stage: "define", ResultCode: resultError}},
		{"a select", `SELECT 1`, InternalError{Stage: "define", ResultCode: resultAuth}},
		{"attach", `ATTACH DATABASE '` + elsewhere + `' AS e`, InternalError{Stage: "define", ResultCode: resultAuth}},
		{"copy to a file", `VACUUM INTO '` + elsewhere + `'`, InternalError{Stage: "define"}},
		{"setting", `PRAGMA trusted_schema = ON`, InternalError{Stage: "define", ResultCode: resultAuth}},
		{"second statement", `CREATE TABLE t (x); ATTACH DATABASE '` + elsewhere + `' AS e`, InternalError{Stage: "define"}},
		{"create table as", `CREATE TABLE t AS SELECT * FROM sqlite_master`, InternalError{Stage: "define", ResultCode: resultAuth}},
		{"temporary table", `CREATE TEMP TABLE t (x)`, InternalError{Stage: "define", ResultCode: resultAuth}},
		{"view", `CREATE VIEW v AS SELECT 1`, InternalError{Stage: "define", ResultCode: resultAuth}},
		{"virtual table", `CREATE VIRTUAL TABLE v USING fts5(body)`, InternalError{Stage: "define", ResultCode: resultAuth}},
		{"generated column that calls a function", `CREATE TABLE t (x, y AS (upper(x)))`, InternalError{Stage: "define", ResultCode: resultError}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CheckSingleReadOnlySelect([]string{peopleDefinition, tc.definition}, `SELECT name FROM people`)
			internal, ok := errors.AsType[*InternalError](err)
			if !ok {
				t.Fatalf("definition %q: got %v (%T), want an *InternalError", tc.definition, err, err)
			}
			if *internal != tc.want {
				t.Errorf("definition %q: got %+v, want %+v", tc.definition, *internal, tc.want)
			}
			if _, isCompileError := errors.AsType[*CompileError](err); isCompileError || errors.Is(err, ErrRefused) {
				t.Errorf("definition %q: %v reads as a verdict on the query", tc.definition, err)
			}
		})
	}
	t.Cleanup(func() {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("directory holds %d entries (%v), want none: a definition touched the disk", len(entries), err)
		}
	})
}

func TestErrorTextHoldsNothingFromTheQueryOrTheDefinitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		definition string
		query      string
		want       string
	}{
		{
			name:       "refusal",
			definition: peopleDefinition,
			query:      `SELECT name FROM people; ATTACH DATABASE 'private-path.db' AS private_schema`,
			want:       "not a single read-only SELECT statement: denied_action",
		},
		{
			name:       "definition failure",
			definition: `CREATE TABLE "payroll_table" ("salary_band" NUMERIC`,
			query:      `SELECT 1`,
			want:       `the query check failed at stage "define" (SQLite result code 1)`,
		},
		{
			name:       "definition failure SQLite gave no result code for",
			definition: `-- a remark about payroll_table, and nothing to run`,
			query:      `SELECT 1`,
			want:       `the query check failed at stage "define"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CheckSingleReadOnlySelect([]string{tc.definition}, tc.query)
			if err == nil || err.Error() != tc.want {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

var refusedFunctions = functionSet(
	"load_extension", "sqlite_log", "match",
	"bm25", "fts5", "fts5_get_locale", "fts5_insttoken", "fts5_locale",
	"fts5_source_id", "highlight", "snippet",
	"rtreecheck", "rtreedepth", "rtreenode",
	"geopoly_area", "geopoly_bbox", "geopoly_blob", "geopoly_ccw",
	"geopoly_contains_point", "geopoly_debug", "geopoly_group_bbox",
	"geopoly_json", "geopoly_overlap", "geopoly_regular", "geopoly_svg",
	"geopoly_within", "geopoly_xform",
)

func TestEveryDriverFunctionIsClassified(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})
	rows, err := db.Query(`SELECT DISTINCT name FROM pragma_function_list ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var offered []string
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatal(scanErr)
		}
		offered = append(offered, name)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	if len(offered) == 0 {
		t.Fatal("the driver listed no functions")
	}

	for _, name := range offered {
		_, allowed := allowedFunctions[name]
		_, refused := refusedFunctions[name]
		switch {
		case allowed && refused:
			t.Errorf("function %q is both allowed and refused", name)
		case !allowed && !refused:
			t.Errorf("function %q is new in this SQLite build: add it to allowedFunctions or to refusedFunctions", name)
		}
	}
	for name := range allowedFunctions {
		if !slices.Contains(offered, name) {
			t.Errorf("allowedFunctions lists %q, which this SQLite build does not offer", name)
		}
	}
}

func TestChecksRunConcurrently(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if err := CheckSingleReadOnlySelect(testDefinitions, `SELECT name FROM people`); err != nil {
				t.Errorf("a query that should be admitted: %v", err)
			}
		})
		wg.Go(func() {
			err := CheckSingleReadOnlySelect(testDefinitions, `SELECT name FROM people; DELETE FROM people`)
			if !errors.Is(err, ErrRefused) {
				t.Errorf("a query that should be refused: %v", err)
			}
		})
	}
	wg.Wait()
}
