// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"maps"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"
)

const (
	plainMigration = `-- +goose Up
ALTER TABLE tasks ADD COLUMN note TEXT;
-- +goose Down
ALTER TABLE tasks DROP COLUMN note;
`
	concurrentMigration = `-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_note ON tasks (note);
-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_tasks_note;
`
)

func sources(fsys fstest.MapFS) []*goose.Source {
	var out []*goose.Source
	for i, name := range slices.Sorted(maps.Keys(fsys)) {
		out = append(out, &goose.Source{Type: goose.TypeSQL, Path: name, Version: int64(i + 1)})
	}
	return out
}

func TestLockTimeoutSegments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		dialect     string
		lockTimeout time.Duration
		files       fstest.MapFS
		want        []migrationSegment
	}{
		{
			name:    "sqlite has no lock_timeout",
			dialect: "sqlite",
			files:   fstest.MapFS{"00001_a.sql": {Data: []byte(concurrentMigration)}},
			want:    []migrationSegment{{upTo: goose.MaxVersion}},
		},
		{
			name:    "nothing pending keeps the default",
			dialect: "postgres",
			files:   fstest.MapFS{},
			want:    []migrationSegment{{lockTimeout: defaultMigrationLockTimeout, upTo: goose.MaxVersion}},
		},
		{
			name:    "plain migrations share one segment",
			dialect: "postgres",
			files: fstest.MapFS{
				"00001_a.sql": {Data: []byte(plainMigration)},
				"00002_b.sql": {Data: []byte(plainMigration)},
			},
			want: []migrationSegment{{lockTimeout: defaultMigrationLockTimeout, upTo: goose.MaxVersion, versions: []int64{1, 2}}},
		},
		{
			name:    "only the concurrent migration runs without a timeout",
			dialect: "postgres",
			files: fstest.MapFS{
				"00001_a.sql": {Data: []byte(plainMigration)},
				"00002_b.sql": {Data: []byte(concurrentMigration)},
				"00003_c.sql": {Data: []byte(concurrentMigration)},
				"00004_d.sql": {Data: []byte(plainMigration)},
				"00005_e.sql": {Data: []byte(plainMigration)},
			},
			want: []migrationSegment{
				{lockTimeout: defaultMigrationLockTimeout, upTo: 1, versions: []int64{1}},
				{lockTimeout: 0, upTo: 3, versions: []int64{2, 3}},
				{lockTimeout: defaultMigrationLockTimeout, upTo: goose.MaxVersion, versions: []int64{4, 5}},
			},
		},
		{
			name:        "a trailing concurrent migration extends to the max version",
			dialect:     "postgres",
			lockTimeout: 2 * time.Second,
			files: fstest.MapFS{
				"00001_a.sql": {Data: []byte(plainMigration)},
				"00002_b.sql": {Data: []byte(concurrentMigration)},
			},
			want: []migrationSegment{
				{lockTimeout: 2 * time.Second, upTo: 1, versions: []int64{1}},
				{lockTimeout: 0, upTo: goose.MaxVersion, versions: []int64{2}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := MigrateConfig{Dialect: tc.dialect, SQLiteFS: tc.files, PostgresFS: tc.files, LockTimeout: tc.lockTimeout}
			got, err := lockTimeoutSegments(cfg, sources(tc.files))
			if err != nil {
				t.Fatal(err)
			}
			sameSegment := func(a, b migrationSegment) bool {
				return a.lockTimeout == b.lockTimeout && a.upTo == b.upTo && slices.Equal(a.versions, b.versions)
			}
			if !slices.EqualFunc(got, tc.want, sameSegment) {
				t.Fatalf("segments = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLockTimeoutSegments_MissingFile(t *testing.T) {
	t.Parallel()
	cfg := MigrateConfig{Dialect: "postgres", PostgresFS: fstest.MapFS{}}
	pending := []*goose.Source{{Type: goose.TypeSQL, Path: "00001_missing.sql", Version: 1}}
	if _, err := lockTimeoutSegments(cfg, pending); err == nil {
		t.Fatal("expected an error for a pending migration missing from the FS")
	}
}

func TestRunsConcurrentIndexOperation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want bool
	}{
		{name: "concurrent build", body: concurrentMigration, want: true},
		{
			name: "concurrent drop",
			body: "-- +goose NO TRANSACTION\n-- +goose Up\nDROP INDEX CONCURRENTLY IF EXISTS idx_a;\n",
			want: true,
		},
		{
			name: "annotation spacing and case follow goose",
			body: "--+goose no transaction\n--   +goose   UP\ncreate index concurrently if not exists idx_a on t (a);\n",
			want: true,
		},
		{name: "plain migration", body: plainMigration},
		{
			name: "queued definition inside a transaction",
			body: "-- +goose Up\nINSERT INTO gateway_async_indexes (name, definition, queued_at) VALUES\n" +
				"    ('idx_a', 'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON t (a)', 1);\n",
		},
		{
			name: "concurrently only in a comment",
			body: "-- +goose NO TRANSACTION\n-- +goose Up\n-- built CONCURRENTLY by the queue\nALTER TABLE t ADD COLUMN a TEXT;\n",
		},
		{
			name: "concurrently only in the Down section",
			body: "-- +goose NO TRANSACTION\n-- +goose Up\nALTER TABLE t ADD COLUMN a TEXT;\n-- +goose Down\nDROP INDEX CONCURRENTLY IF EXISTS idx_a;\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := runsConcurrentIndexOperation([]byte(tc.body)); got != tc.want {
				t.Fatalf("runsConcurrentIndexOperation = %v, want %v", got, tc.want)
			}
		})
	}
}
