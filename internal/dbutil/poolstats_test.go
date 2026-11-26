// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestSetApplicationName(t *testing.T) {
	tests := []struct {
		name string
		url  string
		pool string
		want string
	}{
		{name: "a named pool reports the binary and its name", url: "postgres://u:p@127.0.0.1:5432/db", pool: "platform", want: "dbutil.test/platform"},
		{name: "an unnamed pool reports the binary", url: "postgres://u:p@127.0.0.1:5432/db", pool: "", want: "dbutil.test"},
		{name: "a name the URL sets is kept", url: "postgres://u:p@127.0.0.1:5432/db?application_name=ops", pool: "platform", want: "ops"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PGAPPNAME", "")
			cfg, err := buildPostgresConfig(tc.url)
			if err != nil {
				t.Fatalf("buildPostgresConfig: %v", err)
			}
			setApplicationName(cfg, tc.pool)
			if got := cfg.RuntimeParams["application_name"]; got != tc.want {
				t.Errorf("application_name = %q, want %q", got, tc.want)
			}
		})
	}
}

func postgresURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SAM_TEST_DBUTIL_DB_URL")
	if url == "" {
		t.Skip("SAM_TEST_DBUTIL_DB_URL is not set")
	}
	return url
}

func connectionApplicationName(t *testing.T, db *DB) string {
	t.Helper()
	var name string
	if err := db.QueryRowContext(t.Context(), "SELECT application_name FROM pg_stat_activity WHERE pid = pg_backend_pid()").Scan(&name); err != nil {
		t.Fatalf("read application_name: %v", err)
	}
	return name
}

func reportedMaxOpen(pool string) (int, bool) {
	s, ok := PoolStats()[pool]
	return s.MaxOpenConnections, ok
}

func assertNamedPoolUntilClosed(t *testing.T, db *DB, pool, wantApplicationName string, wantMaxOpen int) {
	t.Helper()
	if got := connectionApplicationName(t, db); got != wantApplicationName {
		t.Errorf("application_name = %q, want %q", got, wantApplicationName)
	}
	if got, ok := reportedMaxOpen(pool); !ok || got != wantMaxOpen {
		t.Errorf("PoolStats()[%q] max open = %d (reported %v), want %d", pool, got, ok, wantMaxOpen)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := reportedMaxOpen(pool); ok {
		t.Errorf("PoolStats() still reports %q after Close", pool)
	}
}

func TestOpenNamesThePool(t *testing.T) {
	db, err := Open(postgresURL(t), nil, WithPoolName("open-named"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	assertNamedPoolUntilClosed(t, db, "open-named", "dbutil.test/open-named", 65)
}

func TestOpenReportsAPoolWithoutANameAsUnnamed(t *testing.T) {
	db, err := Open(postgresURL(t), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	assertNamedPoolUntilClosed(t, db, "unnamed", "dbutil.test", 65)
}

func TestOpenPostgresPoolNamesThePool(t *testing.T) {
	db, err := OpenPostgresPool(t.Context(), postgresURL(t), 3, nil, WithPoolName("pool-named"))
	if err != nil {
		t.Fatalf("OpenPostgresPool: %v", err)
	}
	assertNamedPoolUntilClosed(t, db, "pool-named", "dbutil.test/pool-named", 3)
}

func TestOpenReadOnlyPostgresPoolNamesThePool(t *testing.T) {
	db, err := OpenReadOnlyPostgresPool(t.Context(), postgresURL(t), 2, nil, WithPoolName("read-only-named"))
	if err != nil {
		t.Fatalf("OpenReadOnlyPostgresPool: %v", err)
	}
	assertNamedPoolUntilClosed(t, db, "read-only-named", "dbutil.test/read-only-named", 2)
}

func TestPoolStatsSumsPoolsThatShareAName(t *testing.T) {
	url := postgresURL(t)
	first, err := OpenPostgresPool(t.Context(), url, 3, nil, WithPoolName("same-name"))
	if err != nil {
		t.Fatalf("OpenPostgresPool: %v", err)
	}
	second, err := OpenPostgresPool(t.Context(), url, 4, nil, WithPoolName("same-name"))
	if err != nil {
		t.Fatalf("OpenPostgresPool: %v", err)
	}
	if got, _ := reportedMaxOpen("same-name"); got != 7 {
		t.Errorf("two pools named alike: max open = %d, want 7", got)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got, _ := reportedMaxOpen("same-name"); got != 4 {
		t.Errorf("after closing one: max open = %d, want 4", got)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func checkOut(t *testing.T, db *DB) *sql.Conn {
	t.Helper()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	return conn
}

func checkIn(t *testing.T, conn *sql.Conn) {
	t.Helper()
	if err := conn.Close(); err != nil {
		t.Fatalf("return connection: %v", err)
	}
}

func TestPoolStatsKeepsTheRunningTotalsOfAClosedPoolUnderItsName(t *testing.T) {
	url := postgresURL(t)
	pool := "reopened-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	first, err := OpenPostgresPool(t.Context(), url, 3, nil, WithPoolName(pool))
	if err != nil {
		t.Fatalf("OpenPostgresPool: %v", err)
	}
	inUse, idle, overflow := checkOut(t, first), checkOut(t, first), checkOut(t, first)
	first.SetMaxIdleConns(1)
	checkIn(t, idle)
	checkIn(t, overflow)
	if err = first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	checkIn(t, inUse)

	second, err := OpenPostgresPool(t.Context(), url, 2, nil, WithPoolName(pool))
	if err != nil {
		t.Fatalf("OpenPostgresPool: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	want := sql.DBStats{MaxOpenConnections: 2, OpenConnections: 1, Idle: 1, MaxIdleClosed: 1}
	if diff := cmp.Diff(want, PoolStats()[pool]); diff != "" {
		t.Errorf("PoolStats()[%q] after closing the pool and opening another under its name mismatch (-want +got):\n%s", pool, diff)
	}
}
