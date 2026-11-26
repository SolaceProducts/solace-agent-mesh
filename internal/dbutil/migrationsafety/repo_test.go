// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrationsafety

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

var baselines = map[string]int64{
	"internal/agent/session/migrations/postgres":          14,
	"internal/entrypoint/store/migrations/postgres":       51,
	"internal/notify/store/migrations/postgres":           7,
	"internal/platform/store/migrations/postgres":         90,
	"internal/productanalytics/store/migrations/postgres": 3,
	"internal/volume/migrations/postgres":                 0,
	"internal/workflow/store/migrations/postgres":         2,
}

const guide = "the migrationsafety package doc: migrations run before the process is healthy"

var migrationVersion = regexp.MustCompile(`^(\d+)_.*\.sql$`)

func TestRepositoryMigrations(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	dirs := migrationDirs(t, root)
	if len(dirs) == 0 {
		t.Fatal("found no Postgres migration directories; has the layout changed?")
	}
	for _, dir := range dirs {
		baseline, known := baselines[dir]
		if !known {
			t.Errorf("%s is not in baselines; add it with 0 so all of its migrations are checked", dir)
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			m := migrationVersion.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			version, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if version <= baseline {
				continue
			}
			path := filepath.Join(dir, e.Name())
			src, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range Check(src) {
				t.Errorf("%s: %s\n  see %s", path, f, guide)
			}
		}
	}
}

func TestBaselinesExist(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	for dir := range baselines {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Errorf("baselines names %s, which does not exist: %v", dir, err)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func migrationDirs(t *testing.T, root string) []string {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || filepath.Base(filepath.Dir(path)) != "migrations" {
			return nil
		}
		if d.Name() == "postgres" {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			dirs = append(dirs, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirs)
	return dirs
}
