// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbtest

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

type RowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func WaitForLockWait(t testing.TB, db RowQuerier, prefix string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE $1`, prefix+"%").Scan(&waiting); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no statement starting %q blocked", prefix)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
