// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenPostgresPoolRefusesOtherDialectsWithoutTouchingThem(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	if _, err := OpenPostgresPool(t.Context(), "sqlite:///"+dir+"/gw.db", 2, nil); !errors.Is(err, ErrNotPostgres) {
		t.Fatalf("OpenPostgresPool(sqlite) = %v, want ErrNotPostgres", err)
	}
	if matches, _ := filepath.Glob(dir); len(matches) != 0 {
		t.Error("refusing a SQLite URL still created its directory")
	}
}

func TestOpenPostgresPoolReportsAServerThatDoesNotAnswer(t *testing.T) {
	if _, err := OpenPostgresPool(t.Context(), "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1", 2, nil); err == nil {
		t.Fatal("a pool on a closed port opened")
	}
}

func TestWithReadOnlyTxRunsAndKeepsNoWrite(t *testing.T) {
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "ro.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, createErr := db.ExecContext(t.Context(), `CREATE TABLE t (id INTEGER)`); createErr != nil {
		t.Fatal(createErr)
	}
	ran := false
	err = db.WithReadOnlyTx(t.Context(), func(tx *Tx) error {
		ran = true
		_, readErr := QueryVal[int](t.Context(), tx, `SELECT count(*) FROM t WHERE id = ?`, 1)
		return readErr
	})
	if err != nil || !ran {
		t.Fatalf("read-only transaction ran=%v err=%v", ran, err)
	}
	if err := db.WithReadOnlyTx(t.Context(), func(tx *Tx) error {
		_, insertErr := tx.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (1)`)
		return insertErr
	}); err != nil {
		t.Fatal(err)
	}
	if n, countErr := QueryVal[int](t.Context(), db, `SELECT count(*) FROM t`); countErr != nil || n != 0 {
		t.Errorf("rows after the transaction = %d (%v), want none", n, countErr)
	}
	if _, writeErr := db.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (1)`); writeErr != nil {
		t.Errorf("a write after a read-only transaction = %v, want the connection writable again", writeErr)
	}
	want := errors.New("stop")
	if err := db.WithReadOnlyTx(t.Context(), func(*Tx) error { return want }); !errors.Is(err, want) {
		t.Errorf("error from fn = %v, want it returned", err)
	}
}

func TestAReadEndedByItsContextLeavesTheSQLiteConnectionWritable(t *testing.T) {
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "ro.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, createErr := db.ExecContext(t.Context(), `CREATE TABLE t (id INTEGER)`); createErr != nil {
		t.Fatal(createErr)
	}
	ended := map[string]func(ctx context.Context) error{
		"cancelled inside the read": func(ctx context.Context) error {
			ctx, cancel := context.WithCancel(ctx)
			return db.WithReadOnlyTx(ctx, func(tx *Tx) error {
				cancel()
				_, readErr := QueryVal[int](ctx, tx, `SELECT count(*) FROM t`)
				return readErr
			})
		},
		"timed out between statements": func(ctx context.Context) error {
			return db.WithBoundedReadSnapshot(ctx, time.Millisecond, func(tx *Tx) error {
				<-time.After(20 * time.Millisecond)
				_, readErr := QueryVal[int](ctx, tx, `SELECT count(*) FROM t`)
				return readErr
			})
		},
	}
	for name, read := range ended {
		if readErr := read(t.Context()); readErr == nil {
			t.Errorf("%s: the read succeeded, so it proves nothing", name)
		}
		if _, writeErr := db.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (1)`); writeErr != nil {
			t.Errorf("%s: a write on the same handle afterwards = %v", name, writeErr)
		}
	}
}

func TestAnAttachmentIsRegisteredForAnyDatabaseFileAndSkippedInMemory(t *testing.T) {
	for dsn, want := range map[string]bool{
		`file:C:\Users\me\AppData\sam\platform.db?_pragma=busy_timeout(5000)`: true,
		`file:\\server\share\platform.db`:                                     true,
		"file:/var/lib/sam/platform.db":                                       true,
		"file::memory:?cache=shared":                                          false,
		"file:named?mode=memory&cache=shared":                                 false,
	} {
		registerSQLiteAttachments(dsn, []string{"companion"})
		sqliteAttachMu.Lock()
		_, got := sqliteAttachByDSN[dsn]
		delete(sqliteAttachByDSN, dsn)
		sqliteAttachMu.Unlock()
		if got != want {
			t.Errorf("attachment registered for %q = %v, want %v", dsn, got, want)
		}
	}
	if got, want := SQLiteAttachmentPath(`C:\Users\me\platform.db`, "evidence"), `C:\Users\me\platform.evidence.db`; got != want {
		t.Errorf("companion of a drive-letter path = %q, want %q", got, want)
	}
}

func TestAnInMemoryDatabaseOpensWithoutItsAttachment(t *testing.T) {
	db, err := Open("sqlite:///:memory:", nil, WithSQLiteAttachment("companion"))
	if err != nil {
		t.Fatalf("open = %v, want the database without the attachment", err)
	}
	_ = db.Close()
}

func TestEveryPooledSQLiteConnectionEnforcesForeignKeys(t *testing.T) {
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "fk.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 2 {
		conn, connErr := db.Conn(t.Context())
		if connErr != nil {
			t.Fatal(connErr)
		}
		var on int
		if scanErr := conn.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&on); scanErr != nil || on != 1 {
			t.Errorf("foreign_keys on a pooled connection = %d (%v), want 1", on, scanErr)
		}
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
	}
}
