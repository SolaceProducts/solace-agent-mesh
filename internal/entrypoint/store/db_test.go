// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
)

var testTemplate *dbtest.Template

func TestMain(m *testing.M) {
	tmpl, err := dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_GATEWAY_DB_URL"),
		"gateway_store",
		func(u string) error {
			db, openErr := Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.Migrate(context.Background())
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest template: %v\n", err)
		os.Exit(1)
	}
	testTemplate = tmpl
	code := m.Run()
	testTemplate.Close()
	os.Exit(code)
}

func newTestDB(t *testing.T) *DB {
	t.Helper()
	t.Parallel()
	db, err := Open(testTemplate.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpen_SQLiteURL(t *testing.T) {
	db, err := Open("sqlite:///:memory:", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if db.Dialect() != "sqlite" {
		t.Errorf("dialect = %q, want sqlite", db.Dialect())
	}

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "memory" && journalMode != "wal" {
		t.Errorf("journal_mode = %q", journalMode)
	}
}

func TestOpen_UnsupportedScheme(t *testing.T) {
	_, err := Open("ftp://localhost/db", slog.Default())
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}
