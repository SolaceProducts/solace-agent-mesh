// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"path/filepath"
	"testing"
)

func TestDiscoverToolsIntoRefusesSoleToolRenameAcrossTools(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pkgbin")
	writeMultiToolStub(t, exe, "only_reported")

	entries := map[string]*ManifestEntry{
		"pkg__alpha": {ToolName: "pkg__alpha", Executable: exe, ToolDir: dir},
		"pkg__beta":  {ToolName: "pkg__beta", Executable: exe, ToolDir: dir},
	}

	discoverToolsInto(entries, nil, testLogger())

	for _, name := range []string{"pkg__alpha", "pkg__beta"} {
		e, ok := entries[name]
		if !ok {
			t.Fatalf("%q was re-keyed away; the manifest lost a tool. entries: %v",
				name, entryKeysOf(entries))
		}
		if !e.Unservable {
			t.Errorf("%s: Unservable = false; an entry whose tool could not be identified "+
				"must not stay advertised", name)
		}
		if e.UnservableReason == "" {
			t.Errorf("%s: no UnservableReason recorded", name)
		}
	}
	if _, claimed := entries["only_reported"]; claimed {
		t.Error("the reported name was claimed by one of the two entries; " +
			"which one it belongs to is not knowable")
	}
	if len(entries) != 2 {
		t.Errorf("entry count = %d, want 2; got %v", len(entries), entryKeysOf(entries))
	}
}

func TestDiscoverToolsIntoRenamesWhenBinaryBacksOneTool(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "solebin")
	writeMultiToolStub(t, exe, "actual_name")

	entries := map[string]*ManifestEntry{
		"manifest_name": {ToolName: "manifest_name", Executable: exe, ToolDir: dir},
	}

	discoverToolsInto(entries, nil, testLogger())

	if _, stillThere := entries["manifest_name"]; stillThere {
		t.Error("entry kept its manifest key; the rename should still run for a lone tool")
	}
	renamed, ok := entries["actual_name"]
	if !ok {
		t.Fatalf("no entry under the reported name; got %v", entryKeysOf(entries))
	}
	if renamed.Unservable {
		t.Error("a determinate rename must not mark the entry unservable")
	}
}

func TestDiscoverToolsIntoWarnsOnExpandedNameCollision(t *testing.T) {
	dir := t.TempDir()

	multi := filepath.Join(dir, "multi")
	writeMultiToolStub(t, multi, "shared_name", "unique_name")
	other := filepath.Join(dir, "other")
	writeMultiToolStub(t, other, "shared_name")

	entries := map[string]*ManifestEntry{
		"placeholder": {ToolName: "placeholder", Executable: multi, ToolDir: dir},
		"shared_name": {ToolName: "shared_name", Executable: other, ToolDir: dir},
	}

	discoverToolsInto(entries, nil, testLogger())

	kept, ok := entries["shared_name"]
	if !ok {
		t.Fatalf("shared_name vanished entirely; got %v", entryKeysOf(entries))
	}
	if kept.Executable != other {
		t.Errorf("shared_name.Executable = %q, want the pre-existing %q; the expansion "+
			"overwrote an entry that was already there", kept.Executable, other)
	}
	if _, ok := entries["unique_name"]; !ok {
		t.Error("the non-colliding expanded tool was dropped")
	}
}
