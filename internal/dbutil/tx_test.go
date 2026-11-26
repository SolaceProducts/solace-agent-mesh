// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDBUtil(t *testing.T) *DB {
	t.Helper()
	db, err := Open("sqlite:///:memory:", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, _ = db.ExecContext(context.Background(), `CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT)`)
	return db
}

func TestTx_Rebind(t *testing.T) {
	got := rebind("postgres", "SELECT * FROM t WHERE a = ? AND b = ?")
	want := "SELECT * FROM t WHERE a = $1 AND b = $2"
	if got != want {
		t.Errorf("rebind postgres:\n got  %q\n want %q", got, want)
	}

	query := "SELECT * FROM t WHERE a = ?"
	if rebind("sqlite", query) != query {
		t.Error("rebind sqlite should be no-op")
	}
}

func TestWithTx_CommitOnSuccess(t *testing.T) {
	db := openTestDBUtil(t)
	ctx := context.Background()

	err := db.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "key1", "val1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	val, err := QueryVal[string](ctx, db, `SELECT v FROM kv WHERE k = ?`, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if val != "val1" {
		t.Errorf("val = %q, want val1", val)
	}
}

func TestWithTx_RollbackOnError(t *testing.T) {
	db := openTestDBUtil(t)
	ctx := context.Background()

	err := db.WithTx(ctx, func(tx *Tx) error {
		_, _ = tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "key1", "val1")
		return fmt.Errorf("forced error")
	})
	if err == nil || err.Error() != "forced error" {
		t.Fatalf("expected forced error, got %v", err)
	}

	val, err := QueryVal[string](ctx, db, `SELECT v FROM kv WHERE k = ?`, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("expected empty (rolled back), got %q", val)
	}
}

func TestWithTx_RollbackOnPanic(t *testing.T) {
	db := openTestDBUtil(t)
	ctx := context.Background()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to propagate")
		}
		if r != "boom" {
			t.Fatalf("expected panic 'boom', got %v", r)
		}
		val, err := QueryVal[string](ctx, db, `SELECT v FROM kv WHERE k = ?`, "key1")
		if err != nil {
			t.Fatal(err)
		}
		if val != "" {
			t.Errorf("expected empty (rolled back), got %q", val)
		}
	}()

	_ = db.WithTx(ctx, func(tx *Tx) error {
		_, _ = tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "key1", "val1")
		panic("boom")
	})
}

func TestWithTxVal_ReturnsValue(t *testing.T) {
	db := openTestDBUtil(t)
	ctx := context.Background()

	_, _ = db.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "key1", "val1")

	val, err := WithTxVal[string](ctx, db, func(tx *Tx) (string, error) {
		var v string
		err := tx.QueryRowContext(ctx, `SELECT v FROM kv WHERE k = ?`, "key1").Scan(&v)
		return v, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if val != "val1" {
		t.Errorf("val = %q, want val1", val)
	}
}

func TestWithTxVal_RollbackOnError(t *testing.T) {
	db := openTestDBUtil(t)
	ctx := context.Background()

	_, err := WithTxVal[int](ctx, db, func(tx *Tx) (int, error) {
		_, _ = tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "key1", "val1")
		return 0, fmt.Errorf("abort")
	})
	if err == nil {
		t.Fatal("expected error")
	}

	val, qErr := QueryVal[string](ctx, db, `SELECT v FROM kv WHERE k = ?`, "key1")
	if qErr != nil {
		t.Fatal(qErr)
	}
	if val != "" {
		t.Errorf("expected empty (rolled back), got %q", val)
	}
}

func TestTx_QueryRow(t *testing.T) {
	db := openTestDBUtil(t)
	ctx := context.Background()

	type kv struct {
		K string `db:"k"`
		V string `db:"v"`
	}

	_ = db.WithTx(ctx, func(tx *Tx) error {
		_, _ = tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "a", "1")
		row, err := QueryRow[kv](ctx, tx, `SELECT k, v FROM kv WHERE k = ?`, "a")
		if err != nil {
			t.Fatal(err)
		}
		if row == nil || row.K != "a" || row.V != "1" {
			t.Errorf("unexpected: %+v", row)
		}
		return nil
	})
}

func TestTx_QueryVal(t *testing.T) {
	db := openTestDBUtil(t)
	ctx := context.Background()

	_ = db.WithTx(ctx, func(tx *Tx) error {
		_, _ = tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "a", "1")
		_, _ = tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "b", "2")

		count, err := QueryVal[int](ctx, tx, `SELECT COUNT(*) FROM kv`)
		if err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Errorf("count = %d, want 2", count)
		}
		return nil
	})
}

func TestBeginTx_ManualCommit(t *testing.T) {
	db := openTestDBUtil(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "key1", "val1")
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		t.Fatal(err)
	}

	val, err := QueryVal[string](ctx, db, `SELECT v FROM kv WHERE k = ?`, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if val != "val1" {
		t.Errorf("val = %q, want val1", val)
	}
}

func TestTx_SatisfiesQuerier(t *testing.T) {
	db := openTestDBUtil(t)
	ctx := context.Background()

	_ = db.WithTx(ctx, func(tx *Tx) error {
		var q Querier = tx
		_, err := q.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, "a", "1")
		if err != nil {
			t.Fatal(err)
		}

		row := q.QueryRowContext(ctx, `SELECT v FROM kv WHERE k = ?`, "a")
		var v string
		if err := row.Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != "1" {
			t.Errorf("v = %q", v)
		}
		return sql.ErrTxDone
	})
}
