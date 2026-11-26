// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openRawTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type basicRow struct {
	ID    int64   `db:"id"`
	Name  string  `db:"name"`
	Email *string `db:"email"`
}

func TestScanRows_Basic(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, email TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1, 'alice', 'a@b.com')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (2, 'bob', NULL)`)

	rows, err := db.QueryContext(ctx, `SELECT id, name, email FROM t ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[basicRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(results))
	}
	if results[0].ID != 1 || results[0].Name != "alice" || results[0].Email == nil || *results[0].Email != "a@b.com" {
		t.Errorf("row 0 mismatch: %+v", results[0])
	}
	if results[1].ID != 2 || results[1].Name != "bob" || results[1].Email != nil {
		t.Errorf("row 1 mismatch: %+v", results[1])
	}
}

type allNullRow struct {
	A *string `db:"a"`
	B *int64  `db:"b"`
}

func TestScanRows_AllNullPointers(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (a TEXT, b INTEGER)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (NULL, NULL)`)

	rows, err := db.QueryContext(ctx, `SELECT a, b FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[allNullRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 row, got %d", len(results))
	}
	if results[0].A != nil || results[0].B != nil {
		t.Errorf("expected all nil: %+v", results[0])
	}
}

type boolRow struct {
	ID     int64 `db:"id"`
	Active bool  `db:"active"`
}

func TestScanRows_BoolFromInt(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, active INTEGER)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1, 1)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (2, 0)`)

	rows, err := db.QueryContext(ctx, `SELECT id, active FROM t ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[boolRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(results))
	}
	if !results[0].Active {
		t.Error("expected row 0 active=true")
	}
	if results[1].Active {
		t.Error("expected row 1 active=false")
	}
}

type manyBoolRow struct {
	A bool `db:"a"`
	B bool `db:"b"`
	C bool `db:"c"`
	D bool `db:"d"`
	E bool `db:"e"`
	F bool `db:"f"`
}

func TestScanRows_ManyBoolColumnsOnFirstRow(t *testing.T) {
	db := openRawTestDB(t)
	rows, err := db.QueryContext(t.Context(), `SELECT 1 AS a, 1 AS b, 1 AS c, 1 AS d, 1 AS e, 1 AS f`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[manyBoolRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := results[0]; got != (manyBoolRow{true, true, true, true, true, true}) {
		t.Errorf("first row = %+v, want every column true", got)
	}
}

type boolSourceRow struct {
	Native   bool  `db:"native"`
	Text     bool  `db:"text_true"`
	Optional *bool `db:"optional"`
	Absent   *bool `db:"absent"`
}

func TestScanRows_BoolFromNativeAndTextSources(t *testing.T) {
	db := openRawTestDB(t)
	rows, err := db.QueryContext(t.Context(), `SELECT true AS native, 't' AS text_true, 'false' AS optional, NULL AS absent`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[boolSourceRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	got := results[0]
	if !got.Native || !got.Text {
		t.Errorf("native=%v text=%v, want both true", got.Native, got.Text)
	}
	if got.Optional == nil || *got.Optional {
		t.Errorf("optional = %v, want false", got.Optional)
	}
	if got.Absent != nil {
		t.Errorf("absent = %v, want nil", *got.Absent)
	}
}

func TestNullBoolScan_BoolAndRejects(t *testing.T) {
	var b nullBool
	if err := b.Scan(true); err != nil || !b.Valid || !b.Bool {
		t.Fatalf("scan native true: %+v, %v", b, err)
	}
	if err := b.Scan("maybe"); err == nil {
		t.Fatal("scan of an unrecognized string succeeded")
	}
	if err := b.Scan(1.5); err == nil {
		t.Fatal("scan of a non-integral float succeeded")
	}
	for _, src := range []any{float64(1), "2", []byte("-1"), int64(3)} {
		if err := b.Scan(src); err != nil || !b.Valid || !b.Bool {
			t.Errorf("scan %#v: %+v, %v; want true like the integer scanner it replaced", src, b, err)
		}
	}
	for _, src := range []any{float64(0), "0", []byte("false")} {
		if err := b.Scan(src); err != nil || !b.Valid || b.Bool {
			t.Errorf("scan %#v: %+v, %v; want false", src, b, err)
		}
	}
}

type snakeRow struct {
	UserID      int64
	CreatedTime int64
}

func TestScanRows_SnakeCaseFallback(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (user_id INTEGER, created_time INTEGER)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (42, 1000)`)

	rows, err := db.QueryContext(ctx, `SELECT user_id, created_time FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[snakeRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 row, got %d", len(results))
	}
	if results[0].UserID != 42 || results[0].CreatedTime != 1000 {
		t.Errorf("unexpected: %+v", results[0])
	}
}

type partialRow struct {
	Name string `db:"name"`
}

func TestScanRows_ExtraColumnsDiscarded(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, extra TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1, 'alice', 'ignored')`)

	rows, err := db.QueryContext(ctx, `SELECT id, name, extra FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[partialRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "alice" {
		t.Errorf("unexpected: %+v", results)
	}
}

func TestScanRows_EmptyResult(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER)`)

	rows, err := db.QueryContext(ctx, `SELECT id FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[basicRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if results == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(results))
	}
}

type parent struct {
	child
	Name string `db:"name"`
}

type child struct {
	Age int `db:"age"`
}

func TestScanRows_EmbeddedStructReturnsError(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (name TEXT, age INTEGER)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES ('test', 1)`)

	rows, err := db.QueryContext(ctx, `SELECT name, age FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ScanRows[parent](rows)
	if err == nil {
		t.Fatal("expected error for embedded struct")
	}
	if got := err.Error(); got != "dbutil: embedded struct not supported (flat structs only)" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestQueryRow_Found(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, email TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1, 'alice', 'a@b.com')`)

	row, err := QueryRow[basicRow](ctx, db, `SELECT id, name, email FROM t WHERE id = ?`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Fatal("expected non-nil row")
	}
	if row.ID != 1 || row.Name != "alice" {
		t.Errorf("unexpected: %+v", row)
	}
}

func TestQueryRow_NotFound(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, email TEXT)`)

	row, err := QueryRow[basicRow](ctx, db, `SELECT id, name, email FROM t WHERE id = ?`, 999)
	if err != nil {
		t.Fatal(err)
	}
	if row != nil {
		t.Fatalf("expected nil, got %+v", row)
	}
}

func TestQueryRows_Multiple(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, email TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1, 'alice', NULL)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (2, 'bob', NULL)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (3, 'carol', NULL)`)

	results, err := QueryRows[basicRow](ctx, db, `SELECT id, name, email FROM t ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(results))
	}
	if results[2].Name != "carol" {
		t.Errorf("unexpected: %+v", results[2])
	}
}

type ptrBoolRow struct {
	ID     int64 `db:"id"`
	Active *bool `db:"active"`
}

func TestScanRows_PtrBoolFromInt(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, active INTEGER)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1, 1)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (2, 0)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (3, NULL)`)

	rows, err := db.QueryContext(ctx, `SELECT id, active FROM t ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[ptrBoolRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(results))
	}
	if results[0].Active == nil || !*results[0].Active {
		t.Error("row 0: expected active=true")
	}
	if results[1].Active == nil || *results[1].Active {
		t.Error("row 1: expected active=false")
	}
	if results[2].Active != nil {
		t.Errorf("row 2: expected nil for NULL, got %v", *results[2].Active)
	}
}

type fullModelRow struct {
	ID          string  `db:"id"`
	Name        *string `db:"name"`
	UserID      string  `db:"user_id"`
	StartTime   int64   `db:"start_time"`
	EndTime     *int64  `db:"end_time"`
	TokenCount  *int    `db:"token_count"`
	IsActive    bool    `db:"is_active"`
	IsDeleted   bool    `db:"is_deleted"`
	Description *string `db:"description"`
	Score       *int64  `db:"score"`
}

func TestScanRows_FullModelTypes(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (
		id TEXT, name TEXT, user_id TEXT,
		start_time INTEGER, end_time INTEGER,
		token_count INTEGER, is_active INTEGER,
		is_deleted INTEGER, description TEXT, score INTEGER
	)`)
	_, _ = db.ExecContext(ctx,
		`INSERT INTO t VALUES ('id-1', 'test', 'u1', 1000, 2000, 42, 1, 0, 'desc', 99)`)
	_, _ = db.ExecContext(ctx,
		`INSERT INTO t VALUES ('id-2', NULL, 'u2', 3000, NULL, NULL, 0, 1, NULL, NULL)`)

	rows, err := db.QueryContext(ctx, `SELECT * FROM t ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[fullModelRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(results))
	}

	r := results[0]
	if r.ID != "id-1" {
		t.Errorf("ID = %q", r.ID)
	}
	if r.Name == nil || *r.Name != "test" {
		t.Errorf("Name = %v", r.Name)
	}
	if r.StartTime != 1000 {
		t.Errorf("StartTime = %d", r.StartTime)
	}
	if r.EndTime == nil || *r.EndTime != 2000 {
		t.Errorf("EndTime = %v", r.EndTime)
	}
	if r.TokenCount == nil || *r.TokenCount != 42 {
		t.Errorf("TokenCount = %v", r.TokenCount)
	}
	if !r.IsActive {
		t.Error("IsActive should be true")
	}
	if r.IsDeleted {
		t.Error("IsDeleted should be false")
	}

	r2 := results[1]
	if r2.Name != nil {
		t.Errorf("row 2 Name should be nil, got %q", *r2.Name)
	}
	if r2.EndTime != nil {
		t.Errorf("row 2 EndTime should be nil, got %d", *r2.EndTime)
	}
	if r2.TokenCount != nil {
		t.Errorf("row 2 TokenCount should be nil, got %d", *r2.TokenCount)
	}
	if r2.IsActive {
		t.Error("row 2 IsActive should be false")
	}
	if !r2.IsDeleted {
		t.Error("row 2 IsDeleted should be true")
	}
	if r2.Description != nil {
		t.Errorf("row 2 Description should be nil")
	}
	if r2.Score != nil {
		t.Errorf("row 2 Score should be nil")
	}
}

func TestScanRows_MissingColumnsZeroInit(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES ('1', 'alice')`)

	rows, err := db.QueryContext(ctx, `SELECT id, name FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[fullModelRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 row, got %d", len(results))
	}
	r := results[0]
	if r.StartTime != 0 {
		t.Errorf("unqueried StartTime should be zero, got %d", r.StartTime)
	}
	if r.IsActive {
		t.Error("unqueried bool should be false")
	}
	if r.EndTime != nil {
		t.Error("unqueried pointer should be nil")
	}
}

type dbTagSkipRow struct {
	ID      int64  `db:"id"`
	Ignored string `db:"-"`
	Name    string `db:"name"`
}

func TestScanRows_DbTagSkip(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1, 'test')`)

	rows, err := db.QueryContext(ctx, `SELECT id, name FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ScanRows[dbTagSkipRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Ignored != "" {
		t.Errorf("db:\"-\" field should be empty, got %q", results[0].Ignored)
	}
	if results[0].Name != "test" {
		t.Errorf("Name = %q", results[0].Name)
	}
}

func TestQueryRow_WithQuerier(t *testing.T) {
	rawDB := openRawTestDB(t)
	ctx := context.Background()
	_, _ = rawDB.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, email TEXT)`)
	_, _ = rawDB.ExecContext(ctx, `INSERT INTO t VALUES (1, 'alice', 'a@b.com')`)

	tx, err := rawDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := QueryRow[basicRow](ctx, tx, `SELECT id, name, email FROM t WHERE id = ?`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil || row.Name != "alice" {
		t.Errorf("expected alice via tx, got %+v", row)
	}
}

func TestQueryRowPtrs_Multiple(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, email TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1, 'alice', 'a@b.com')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (2, 'bob', NULL)`)

	ptrs, err := QueryRowPtrs[basicRow](ctx, db, `SELECT id, name, email FROM t ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ptrs) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(ptrs))
	}
	if ptrs[0].Name != "alice" || ptrs[1].Name != "bob" {
		t.Errorf("unexpected: %+v, %+v", ptrs[0], ptrs[1])
	}
	ptrs[0].Name = "modified"
	if ptrs[1].Name == "modified" {
		t.Error("pointer elements should be independent")
	}
}

func TestQueryRowPtrs_Empty(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, email TEXT)`)

	ptrs, err := QueryRowPtrs[basicRow](ctx, db, `SELECT id, name, email FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ptrs) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(ptrs))
	}
}

func TestScanRowPtrs_Basic(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER, name TEXT, email TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1, 'alice', NULL)`)

	rows, err := db.QueryContext(ctx, `SELECT id, name, email FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	ptrs, err := ScanRowPtrs[basicRow](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(ptrs) != 1 || ptrs[0].Name != "alice" {
		t.Errorf("unexpected: %+v", ptrs)
	}
}

func TestQueryVal_Count(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (2)`)

	count, err := QueryVal[int](ctx, db, `SELECT COUNT(*) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestQueryVal_NullInt64(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (val INTEGER)`)

	val, err := QueryVal[sql.NullInt64](ctx, db, `SELECT MAX(val) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if val.Valid {
		t.Errorf("expected NULL for empty table, got %d", val.Int64)
	}

	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (42)`)
	val, err = QueryVal[sql.NullInt64](ctx, db, `SELECT MAX(val) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if !val.Valid || val.Int64 != 42 {
		t.Errorf("expected 42, got %+v", val)
	}
}

func TestQueryVal_String(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (role TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES ('admin')`)

	role, err := QueryVal[string](ctx, db, `SELECT role FROM t LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	if role != "admin" {
		t.Errorf("role = %q, want admin", role)
	}
}

func TestQueryVal_NoRows(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (role TEXT)`)

	role, err := QueryVal[string](ctx, db, `SELECT role FROM t WHERE role = 'nope'`)
	if err != nil {
		t.Fatal(err)
	}
	if role != "" {
		t.Errorf("expected empty string for no rows, got %q", role)
	}
}

func TestQueryVals_Strings(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (name TEXT)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES ('alice')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES ('bob')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES ('carol')`)

	names, err := QueryVals[string](ctx, db, `SELECT name FROM t ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 || names[0] != "alice" || names[2] != "carol" {
		t.Errorf("names = %v", names)
	}
}

func TestQueryVals_Empty(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER)`)

	ids, err := QueryVals[int](ctx, db, `SELECT id FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if ids != nil {
		t.Errorf("expected nil for empty, got %v", ids)
	}
}
