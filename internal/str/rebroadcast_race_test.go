// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublishOneToolInitSnapshotsSchemaFields(t *testing.T) {
	dir := t.TempDir()
	const key = "probe"
	exe := filepath.Join(dir, "probe")

	writeMultiToolStub(t, exe, "probe")
	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := "version: 1\ntools:\n  " + key + ":\n    executable: " + exe + "\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	brk := newTestBroker(t)
	w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	entry := w.manifest.GetTool(key)
	if entry == nil {
		t.Fatalf("precondition: %q missing from the manifest", key)
	}

	runRefreshRace(t, w.manifest, key, func() {
		w.publishOneToolInit(ctx, entry, map[string][]string{})
	})

	if got := w.manifest.SnapshotEntry(entry); got.Parameters == nil {
		t.Error("entry lost its discovered parameters")
	}
}

func runRefreshRace(t *testing.T, m *Manifest, toolName string, read func()) {
	t.Helper()

	var refreshes atomic.Int64
	var done atomic.Bool
	var wg sync.WaitGroup
	observed := make(map[int64]struct{})

	wg.Go(func() {
		defer done.Store(true)
		for range 5 {
			if _, err := m.RefreshToolSchema(toolName); err != nil {
				t.Errorf("RefreshToolSchema: %v", err)
				return
			}
			refreshes.Add(1)
		}
	})
	wg.Go(func() {
		deadline := time.Now().Add(30 * time.Second)
		for !done.Load() && time.Now().Before(deadline) {
			read()
			observed[refreshes.Load()] = struct{}{}
		}
	})
	wg.Wait()

	if len(observed) < 2 {
		t.Errorf("reader observed %d distinct refresh counts, want at least 2; it never ran "+
			"across a refresh boundary, so this test would pass with the locking reverted",
			len(observed))
	}
}
