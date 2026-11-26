// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func TestMergedSkillRegistry_MirrorWinsOnNameCollision(t *testing.T) {
	builtinDir := t.TempDir()
	mirrorDir := t.TempDir()

	writeSkillMD(t, builtinDir, "my-skill", `---
name: my-skill
description: builtin version
---
builtin content`)

	writeSkillMD(t, mirrorDir, "my-skill", `---
name: my-skill
description: mirror version
---
mirror content`)

	reg := NewSkillRegistryWithBuiltins(mirrorDir, builtinDir)
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	entry := reg.GetSkill("my-skill")
	if entry == nil {
		t.Fatal("expected skill entry")
	}
	if entry.Description != "mirror version" {
		t.Errorf("description = %q, want %q", entry.Description, "mirror version")
	}
	if entry.LocalPath != filepath.Join(mirrorDir, "my-skill") {
		t.Errorf("LocalPath = %q, want path inside mirrorDir", entry.LocalPath)
	}
}

func TestMergedSkillRegistry_BuiltinsVisibleWhenMirrorEmpty(t *testing.T) {
	builtinDir := t.TempDir()
	mirrorDir := t.TempDir()

	writeSkillMD(t, builtinDir, "builtin-skill", `---
name: builtin-skill
description: from builtin
---
builtin content`)

	reg := NewSkillRegistryWithBuiltins(mirrorDir, builtinDir)
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	entry := reg.GetSkill("builtin-skill")
	if entry == nil {
		t.Fatal("expected builtin skill to be visible when mirror dir is empty")
	}
	if entry.Description != "from builtin" {
		t.Errorf("description = %q, want %q", entry.Description, "from builtin")
	}
	if entry.LocalPath != filepath.Join(builtinDir, "builtin-skill") {
		t.Errorf("LocalPath = %q, want path inside builtinDir", entry.LocalPath)
	}
}

func TestMergedSkillRegistry_FallbackWhenBuiltinDirEmpty(t *testing.T) {
	mirrorDir := t.TempDir()
	writeSkillMD(t, mirrorDir, "only-skill", `---
name: only-skill
description: only in mirror
---
mirror content`)

	reg := NewSkillRegistryWithBuiltins(mirrorDir, "")
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	entry := reg.GetSkill("only-skill")
	if entry == nil {
		t.Fatal("expected skill to be found")
	}
	if entry.Description != "only in mirror" {
		t.Errorf("description = %q, want %q", entry.Description, "only in mirror")
	}
}

func TestMergedSkillRegistry_ReadOnlyRootfsScenario(t *testing.T) {
	builtinDir := t.TempDir()
	mirrorDir := t.TempDir()

	if builtinDir == mirrorDir {
		t.Fatal("test setup error: dirs must differ")
	}

	if err := os.WriteFile(filepath.Join(builtinDir, "sentinel.txt"), []byte("baked"), 0o644); err != nil {
		t.Fatal(err)
	}

	proc := NewSkillProcessor(mirrorDir, slog.Default())

	changed, err := proc.ProcessAll()
	if err != nil {
		t.Fatalf("ProcessAll: %v", err)
	}
	if changed {
		t.Error("expected no changes from empty mirrorDir")
	}

	entries, err := os.ReadDir(builtinDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "sentinel.txt" {
		t.Errorf("builtinDir was modified unexpectedly: %v", entries)
	}
}

func writeBundledSkill(t *testing.T, baseDir, name, desc string, withBinary bool) {
	t.Helper()
	writeSkillMD(t, baseDir, name, "---\nname: "+name+"\ndescription: "+desc+"\n---\nbody")
	toolsDir := filepath.Join(baseDir, name, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const manifest = "version: 1\ntools:\n  do_thing:\n    runtime: go\n    executable: myrunner\n    tool_dir: myrunner\n    timeout_seconds: 60\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if withBinary {
		binDir := filepath.Join(toolsDir, "myrunner")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "myrunner"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMergedSkillRegistry_ImageBuiltinWinsForReservedPrefix(t *testing.T) {
	builtinDir := t.TempDir()
	mirrorDir := t.TempDir()

	writeBundledSkill(t, builtinDir, "sam-fixture", "image copy", true)
	writeBundledSkill(t, mirrorDir, "sam-fixture", "mirror copy", false)

	reg := NewSkillRegistryWithBuiltins(mirrorDir, builtinDir)
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	entry := reg.GetSkill("sam-fixture")
	if entry == nil {
		t.Fatal("expected sam-fixture skill")
	}
	if entry.Description != "image copy" {
		t.Errorf("description = %q, want %q (image builtin must win)", entry.Description, "image copy")
	}
	if entry.LocalPath != filepath.Join(builtinDir, "sam-fixture") {
		t.Errorf("LocalPath = %q, want path inside builtinDir", entry.LocalPath)
	}
	bt := entry.BundledToolManifest["do_thing"]
	if bt == nil {
		t.Fatal("expected bundled tool do_thing in resolved entry")
	}
	wantExe := filepath.Join(builtinDir, "sam-fixture", "tools", "myrunner", "myrunner")
	if bt.Executable != wantExe {
		t.Errorf("Executable = %q, want %q (inside builtinDir)", bt.Executable, wantExe)
	}
	if !isExecutableFile(bt.Executable) {
		t.Errorf("resolved executable %q is not executable", bt.Executable)
	}
}

func TestMergedSkillRegistry_NonReservedNameMirrorStillWins(t *testing.T) {
	builtinDir := t.TempDir()
	mirrorDir := t.TempDir()

	writeSkillMD(t, builtinDir, "plain-skill", "---\nname: plain-skill\ndescription: builtin\n---\nx")
	writeSkillMD(t, mirrorDir, "plain-skill", "---\nname: plain-skill\ndescription: mirror\n---\nx")

	reg := NewSkillRegistryWithBuiltins(mirrorDir, builtinDir)
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := reg.GetSkill("plain-skill")
	if entry == nil {
		t.Fatal("expected plain-skill")
	}
	if entry.Description != "mirror" {
		t.Errorf("description = %q, want %q (non-reserved name: mirror still wins)", entry.Description, "mirror")
	}
}

func TestEnrichSkillToolParams_UsesLocalPath(t *testing.T) {
	builtinDir := t.TempDir()
	mirrorDir := t.TempDir()

	writeSkillMD(t, builtinDir, "helper", `---
name: helper
description: builtin helper
---
content`)

	reg := NewSkillRegistryWithBuiltins(mirrorDir, builtinDir)
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	entry := reg.GetSkill("helper")
	if entry == nil {
		t.Fatal("expected helper skill")
	}

	w := &Worker{
		cfg: WorkerConfig{
			SkillsDir: mirrorDir,
		},
		skillRegistry: reg,
	}

	params := &remotetool.InvokeParams{
		ToolName: "helper__do_thing",
		Args:     map[string]any{},
	}
	manifestEntry := &ManifestEntry{ToolName: "helper__do_thing"}

	_ = os.MkdirAll(filepath.Join(builtinDir, "helper"), 0o755)

	w.enrichSkillToolParams(params, manifestEntry)

	if len(manifestEntry.ExtraMounts) == 0 {
		t.Fatal("expected ExtraMounts to be populated")
	}
	wantHostPath := filepath.Join(builtinDir, "helper")
	if manifestEntry.ExtraMounts[0].HostPath != wantHostPath {
		t.Errorf("ExtraMount.HostPath = %q, want %q (builtinDir/helper)",
			manifestEntry.ExtraMounts[0].HostPath, wantHostPath)
	}
}
