// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverToolsIntoKeepsRefusedEntryWhenRenameCollides(t *testing.T) {
	dir := t.TempDir()
	holder := filepath.Join(dir, "holder")
	writeMultiToolStub(t, holder, "shared")
	lone := filepath.Join(dir, "lone")
	writeMultiToolStub(t, lone, "shared")

	entries := map[string]*ManifestEntry{
		"shared":     {ToolName: "shared", Executable: holder, ToolDir: dir},
		"legacyname": {ToolName: "legacyname", Executable: lone, ToolDir: dir},
	}

	discoverToolsInto(entries, nil, testLogger())

	kept, ok := entries["legacyname"]
	if !ok {
		t.Fatalf("legacyname vanished: its key was removed for a replacement that lost the "+
			"collision, so the manifest silently holds one fewer tool than declared. entries: %v",
			entryKeysOf(entries))
	}
	if !kept.Unservable {
		t.Error("legacyname survived but is still advertised; nothing will bind its invoke topic")
	}
	if kept.UnservableReason == "" {
		t.Error("legacyname is unservable with no reason recorded")
	}
	if kept.Executable != lone {
		t.Errorf("legacyname.Executable = %q, want its own %q", kept.Executable, lone)
	}

	winner, ok := entries["shared"]
	if !ok {
		t.Fatalf("the exactly-matched entry lost its own name; got %v", entryKeysOf(entries))
	}
	if winner.Executable != holder {
		t.Errorf("shared.Executable = %q, want the entry that matched exactly, %q",
			winner.Executable, holder)
	}
	if len(entries) != 2 {
		t.Errorf("entry count = %d, want 2; got %v", len(entries), entryKeysOf(entries))
	}
}

func TestDiscoverToolsIntoExpandsSharedBinaryOnce(t *testing.T) {
	dir := t.TempDir()
	multi := filepath.Join(dir, "multi")
	writeMultiToolStub(t, multi, "one", "two")

	const passes = 20
	for pass := range passes {
		var buf bytes.Buffer
		entries := map[string]*ManifestEntry{
			"ph_a": {ToolName: "ph_a", Executable: multi, ToolDir: dir, SandboxProfile: "profile_a"},
			"ph_b": {ToolName: "ph_b", Executable: multi, ToolDir: dir, SandboxProfile: "profile_b"},
		}

		discoverToolsInto(entries, nil, captureLogger(&buf))

		if logged := buf.String(); strings.Contains(logged, "collides with an existing entry") {
			t.Fatalf("pass %d: the collision backstop fired on a shared binary expanded twice; "+
				"its message names one executable on both sides and no operator action would "+
				"fix it", pass)
		}
		for _, name := range []string{"one", "two"} {
			e, ok := entries[name]
			if !ok {
				t.Fatalf("pass %d: %q missing after expansion; got %v", pass, name, entryKeysOf(entries))
			}
			if e.Unservable {
				t.Fatalf("pass %d: %s expanded from a working binary but is marked unservable", pass, name)
			}
			if e.SandboxProfile != "profile_a" {
				t.Fatalf("pass %d: %s inherited SandboxProfile %q, want %q from the first "+
					"placeholder by name; the choice is varying between runs, so the sandbox a "+
					"tool is confined by depends on map order at startup",
					pass, name, e.SandboxProfile, "profile_a")
			}
		}
		if len(entries) != 2 {
			t.Fatalf("pass %d: entry count = %d, want 2 (both placeholders replaced); got %v",
				pass, len(entries), entryKeysOf(entries))
		}
	}
}

func TestDiscoverToolsIntoPrefersWorkingBinaryOverUnservableEntry(t *testing.T) {
	dir := t.TempDir()
	multi := filepath.Join(dir, "multi")
	writeMultiToolStub(t, multi, "foo", "bar")

	entries := map[string]*ManifestEntry{
		"placeholder": {ToolName: "placeholder", Executable: multi, ToolDir: dir},
		"foo":         {ToolName: "foo", Executable: filepath.Join(dir, "never_installed"), ToolDir: dir},
	}

	discoverToolsInto(entries, nil, testLogger())

	foo, ok := entries["foo"]
	if !ok {
		t.Fatalf("foo missing entirely; got %v", entryKeysOf(entries))
	}
	if foo.Unservable {
		t.Errorf("foo is still the dead entry (%s); a binary that reports the name and runs "+
			"lost to one whose executable is absent", foo.UnservableReason)
	}
	if foo.Executable != multi {
		t.Errorf("foo.Executable = %q, want the working %q", foo.Executable, multi)
	}
	if _, ok := entries["bar"]; !ok {
		t.Error("bar was dropped; the expansion's other tool should be unaffected")
	}
}

func TestDiscoverToolsIntoRestoreDoesNotOverwriteWorkingEntry(t *testing.T) {
	dir := t.TempDir()

	blocked := filepath.Join(dir, "blocked")
	writeMultiToolStub(t, blocked, "x", "y")
	exeX := filepath.Join(dir, "exe_x")
	writeMultiToolStub(t, exeX, "x")
	exeY := filepath.Join(dir, "exe_y")
	writeMultiToolStub(t, exeY, "y")

	working := filepath.Join(dir, "working")
	writeMultiToolStub(t, working, "foo", "spare")

	entries := map[string]*ManifestEntry{
		"foo": {ToolName: "foo", Executable: blocked, ToolDir: dir},
		"x":   {ToolName: "x", Executable: exeX, ToolDir: dir},
		"y":   {ToolName: "y", Executable: exeY, ToolDir: dir},
		"zzz": {ToolName: "zzz", Executable: working, ToolDir: dir},
	}

	discoverToolsInto(entries, nil, testLogger())

	foo, ok := entries["foo"]
	if !ok {
		t.Fatalf("foo missing entirely; got %v", entryKeysOf(entries))
	}
	if foo.Unservable {
		t.Errorf("foo is the restored placeholder (%s); a dead entry reclaimed a key a working "+
			"binary had already taken", foo.UnservableReason)
	}
	if foo.Executable != working {
		t.Errorf("foo.Executable = %q, want the working %q", foo.Executable, working)
	}
	if _, ok := entries["spare"]; !ok {
		t.Errorf("spare missing; the working expansion should be intact. got %v", entryKeysOf(entries))
	}
}
