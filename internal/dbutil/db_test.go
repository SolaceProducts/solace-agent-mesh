// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseURL_SQLitePaths(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		url     string
		wantDSN string
		wantErr bool
	}{
		{
			name:    "absolute path (4 slashes)",
			url:     "sqlite:////tmp/test.db",
			wantDSN: "file:/tmp/test.db",
		},
		{
			name:    "extra slashes (5 slashes)",
			url:     "sqlite://///tmp/test.db",
			wantDSN: "file:/tmp/test.db",
		},
		{
			name:    "relative path (3 slashes)",
			url:     "sqlite:///sam.db",
			wantDSN: "file:" + filepath.Join(cwd, "sam.db"),
		},
		{
			name:    "relative path with subdir",
			url:     "sqlite:///data/sam.db",
			wantDSN: "file:" + filepath.Join(cwd, "data", "sam.db"),
		},
		{
			name:    "in-memory",
			url:     "sqlite:///:memory:",
			wantDSN: "file::memory:?cache=shared",
		},
		{
			name:    "named in-memory",
			url:     "sqlite:///:memory:test_1",
			wantDSN: "file:test_1?mode=memory&cache=shared",
		},
		{
			name:    "empty path",
			url:     "sqlite:///",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialect, dsn, err := parseURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dialect != "sqlite" {
				t.Errorf("dialect = %q, want sqlite", dialect)
			}
			if dsn != tt.wantDSN {
				t.Errorf("dsn = %q, want %q", dsn, tt.wantDSN)
			}
		})
	}
}

func TestParseURL_Postgres(t *testing.T) {
	dialect, dsn, err := parseURL("postgres://user:pass@localhost:5432/mydb")
	if err != nil {
		t.Fatal(err)
	}
	if dialect != "pgx" {
		t.Errorf("dialect = %q, want pgx", dialect)
	}
	if dsn != "postgres://user:pass@localhost:5432/mydb" {
		t.Errorf("dsn = %q, want original URL", dsn)
	}
}

func TestParseURL_Unsupported(t *testing.T) {
	_, _, err := parseURL("ftp://localhost/db")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestParseURL_ErrorOmitsCredentials(t *testing.T) {
	const password = "hunter2-parseurl-canary"
	dsn := "mongo://sam:" + password + "@db.internal:27017/sam"

	_, _, err := parseURL(dsn)
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("error %q must not contain DSN password", err.Error())
	}
	if strings.Contains(err.Error(), "db.internal") {
		t.Fatalf("error %q must not echo host portion of unsupported DSN", err.Error())
	}
}

func TestPostgresKeepAliveConfig(t *testing.T) {
	cfg := postgresKeepAliveConfig()
	if !cfg.Enable {
		t.Error("Enable = false, want true")
	}
	if cfg.Idle != 10*time.Second {
		t.Errorf("Idle = %v, want 10s", cfg.Idle)
	}
	if cfg.Interval != 5*time.Second {
		t.Errorf("Interval = %v, want 5s", cfg.Interval)
	}
	if cfg.Count != 3 {
		t.Errorf("Count = %d, want 3", cfg.Count)
	}
}

func TestBuildPostgresConfig_DialFuncInstalled(t *testing.T) {
	cfg, err := buildPostgresConfig("postgres://user:pass@127.0.0.1:5432/mydb?sslmode=disable")
	if err != nil {
		t.Fatalf("buildPostgresConfig: %v", err)
	}
	if cfg.DialFunc == nil {
		t.Fatal("DialFunc is nil, want custom dialer with keepalive config")
	}
}

func TestBuildPostgresConfig_InvalidDSN(t *testing.T) {
	_, err := buildPostgresConfig("postgres://[invalid")
	if err == nil {
		t.Fatal("expected error for invalid DSN")
	}
	if !strings.Contains(err.Error(), "parse postgres config") {
		t.Errorf("error = %q, want it to mention parse postgres config", err.Error())
	}
}

func TestBuildPostgresConfig_ErrorOmitsCredentials(t *testing.T) {
	const password = "hunter2-pgconfig-canary"
	dsn := "postgres://sam:" + password + "@[invalid:5432/sam"

	_, err := buildPostgresConfig(dsn)
	if err == nil {
		t.Fatal("expected error for malformed DSN")
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("error %q must not contain DSN password", err.Error())
	}
}

func TestOpen_SQLiteNoKeepalive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if db.Dialect() != "sqlite" {
		t.Errorf("dialect = %q, want sqlite", db.Dialect())
	}
}

func TestOpen_SQLitePragmas(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	tests := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
		{"synchronous", "1"},
		{"cache_size", "-64000"},
		{"wal_autocheckpoint", "1000"},
	}

	for _, tt := range tests {
		t.Run(tt.pragma, func(t *testing.T) {
			var val string
			if err := db.QueryRow("PRAGMA " + tt.pragma).Scan(&val); err != nil {
				t.Fatalf("query PRAGMA %s: %v", tt.pragma, err)
			}
			if val != tt.want {
				t.Errorf("PRAGMA %s = %q, want %q", tt.pragma, val, tt.want)
			}
		})
	}
}

func TestOpen_SQLiteConcurrentColdOpens(t *testing.T) {
	url := "sqlite:///" + filepath.Join(t.TempDir(), "shared.db")

	const openers = 12
	errs := make(chan error, openers)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	for range openers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			db, err := Open(url, nil)
			if err != nil {
				errs <- err
				return
			}
			errs <- db.Close()
		}()
	}
	start.Done()
	done.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent open failed: %v", err)
		}
	}
}

func TestOpen_SQLiteWALSwitchGivesUpUnderASustainedLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")
	url := "sqlite:///" + filepath.ToSlash(path)

	holder, openErr := sql.Open("sqlite", path)
	if openErr != nil {
		t.Fatalf("open the lock holder: %v", openErr)
	}
	defer func() {
		if closeErr := holder.Close(); closeErr != nil {
			t.Errorf("close the lock holder: %v", closeErr)
		}
	}()
	for _, stmt := range []string{"PRAGMA journal_mode=DELETE", "CREATE TABLE t (x INTEGER)"} {
		if _, execErr := holder.Exec(stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}
	tx, beginErr := holder.Begin()
	if beginErr != nil {
		t.Fatalf("begin the blocking write: %v", beginErr)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Errorf("release the write lock: %v", rollbackErr)
		}
	}()
	if _, execErr := tx.Exec("INSERT INTO t VALUES (1)"); execErr != nil {
		t.Fatalf("take the write lock: %v", execErr)
	}

	db, err := Open(url, slog.New(slog.DiscardHandler))
	if err == nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
		t.Fatal("Open succeeded while the WAL switch was locked out for its whole retry budget")
	}
	if !strings.Contains(err.Error(), "enable WAL") {
		t.Errorf("error does not name the WAL switch: %v", err)
	}
}

func TestEnvIntOrDefault(t *testing.T) {
	t.Run("empty returns default", func(t *testing.T) {
		t.Setenv("DBUTIL_TEST_KNOB", "")
		if got := envIntOrDefault("DBUTIL_TEST_KNOB", 7, nil); got != 7 {
			t.Errorf("got %d, want 7", got)
		}
	})
	t.Run("valid override", func(t *testing.T) {
		t.Setenv("DBUTIL_TEST_KNOB", "42")
		if got := envIntOrDefault("DBUTIL_TEST_KNOB", 7, nil); got != 42 {
			t.Errorf("got %d, want 42", got)
		}
	})
	t.Run("negative falls back", func(t *testing.T) {
		t.Setenv("DBUTIL_TEST_KNOB", "-5")
		if got := envIntOrDefault("DBUTIL_TEST_KNOB", 7, nil); got != 7 {
			t.Errorf("got %d, want 7", got)
		}
	})
	t.Run("garbage falls back", func(t *testing.T) {
		t.Setenv("DBUTIL_TEST_KNOB", "not-a-number")
		if got := envIntOrDefault("DBUTIL_TEST_KNOB", 7, nil); got != 7 {
			t.Errorf("got %d, want 7", got)
		}
	})
	t.Run("warn fires on invalid value with logger", func(t *testing.T) {
		var buf strings.Builder
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		t.Setenv("DBUTIL_TEST_KNOB", "0")
		if got := envIntOrDefault("DBUTIL_TEST_KNOB", 11, logger); got != 11 {
			t.Errorf("got %d, want 11", got)
		}
		if !strings.Contains(buf.String(), "DBUTIL_TEST_KNOB") {
			t.Errorf("warn output %q must mention env var name", buf.String())
		}
		if !strings.Contains(buf.String(), "ignoring invalid env override") {
			t.Errorf("warn output %q must include the canonical message", buf.String())
		}
	})
}
