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

func TestCheckRowsAffected_OneRow(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO t VALUES (1)`)

	result, err := db.ExecContext(ctx, `DELETE FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRowsAffected(result); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckRowsAffected_ZeroRows(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE t (id INTEGER)`)

	result, err := db.ExecContext(ctx, `DELETE FROM t WHERE id = 999`)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRowsAffected(result); err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestBoolToInt(t *testing.T) {
	if BoolToInt(true) != 1 {
		t.Error("expected 1 for true")
	}
	if BoolToInt(false) != 0 {
		t.Error("expected 0 for false")
	}
}
