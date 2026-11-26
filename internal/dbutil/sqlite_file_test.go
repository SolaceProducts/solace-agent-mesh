// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSQLiteFileKeepsTheDatabaseAtThePathItWasGiven(t *testing.T) {
	t.Parallel()
	for _, directory := range []string{"plain", "has space", "has#hash", "has?query", "has%25percent", "has%zzpercent"} {
		t.Run(directory, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), directory)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatalf("os.Mkdir: %v", err)
			}
			path := filepath.Join(dir, "list.db")

			first, err := OpenSQLiteFile(path, nil)
			if err != nil {
				t.Fatalf("open %s: %v", path, err)
			}
			if _, execErr := first.ExecContext(t.Context(), "CREATE TABLE kept (id TEXT PRIMARY KEY)"); execErr != nil {
				t.Fatalf("first.ExecContext: %v", execErr)
			}
			if closeErr := first.Close(); closeErr != nil {
				t.Fatalf("first.Close: %v", closeErr)
			}
			if _, statErr := os.Stat(path); statErr != nil {
				t.Fatalf("the database file is not at the path given: %v", statErr)
			}
			beside, err := os.ReadDir(filepath.Dir(dir))
			if err != nil || len(beside) != 1 {
				t.Fatalf("entries beside the directory given: %v, err=%v; want only that directory", beside, err)
			}

			reopened, err := OpenSQLiteFile(path, nil)
			if err != nil {
				t.Fatalf("reopen %s: %v", path, err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			if _, queryErr := reopened.ExecContext(t.Context(), "INSERT INTO kept (id) VALUES ('a')"); queryErr != nil {
				t.Fatalf("the table created before the reopen: %v", queryErr)
			}
		})
	}
}
