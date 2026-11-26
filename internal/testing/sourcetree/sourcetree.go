// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sourcetree

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

const syncConfig = ".github/source-sync/copy.bara.sky"

func Root(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found walking up from the test's working directory")
		}
		dir = parent
	}
}

func IsInternal(t testing.TB) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(Root(t), filepath.FromSlash(syncConfig)))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatalf("stat %s: %v", syncConfig, err)
	}
	return true
}

func RequireInternal(t testing.TB) {
	t.Helper()
	if !IsInternal(t) {
		t.Skip("reads files the published source tree does not include")
	}
}
