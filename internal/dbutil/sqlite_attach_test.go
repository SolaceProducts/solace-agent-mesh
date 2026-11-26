// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const freshProcessEnv = "DBUTIL_ATTACH_HOOK_FRESH_PROCESS"

func TestTheAttachmentHookIsInstalledBeforeAnyDatabaseOpens(t *testing.T) {
	if os.Getenv(freshProcessEnv) == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTheAttachmentHookIsInstalledBeforeAnyDatabaseOpens$", "-test.count=1")
		cmd.Env = append(os.Environ(), freshProcessEnv+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("in a process where no other test ran first: %v\n%s", err, out)
		}
		return
	}

	path := filepath.Join(t.TempDir(), "main.db")
	_, dsn, err := parseURL("sqlite:///" + path)
	if err != nil {
		t.Fatal(err)
	}
	dsn = sqliteDSN(dsn, openOptions{})
	sqliteAttachMu.Lock()
	sqliteAttachByDSN[dsn] = []string{"companion"}
	sqliteAttachMu.Unlock()

	db, err := Open("sqlite:///"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var attached int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_database_list WHERE name = 'companion'`).Scan(&attached); err != nil {
		t.Fatal(err)
	}
	if attached != 1 {
		t.Errorf("companion databases attached on a connection opened before any Open asked for an attachment = %d, want 1: the hook was not installed at init", attached)
	}
}
