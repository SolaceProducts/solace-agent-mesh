// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseSkillToolManifest_KeepsToolDirUnderTheToolsDir(t *testing.T) {
	toolsDir := filepath.Join(t.TempDir(), "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
tools:
  ok:
    tool_dir: ok
    executable: ok
    description: d
  abs_exe:
    tool_dir: x
    executable: /usr/local/bin/tool
    description: d
  abs_dir:
    tool_dir: /tmp
    executable: t
    description: d
  up_dir:
    tool_dir: ..
    executable: t
    description: d
`
	path := filepath.Join(toolsDir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, _, err := parseSkillToolManifest(nil, path, toolsDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(entries)); !slices.Equal(got, []string{"abs_exe", "ok"}) {
		t.Fatalf("loaded tools = %v, want [abs_exe ok]", got)
	}
	if want := filepath.Join(toolsDir, "ok", "ok"); entries["ok"].Executable != want {
		t.Fatalf("Executable = %q, want %q", entries["ok"].Executable, want)
	}
	if entries["abs_exe"].Executable != "/usr/local/bin/tool" {
		t.Fatalf("Executable = %q, want /usr/local/bin/tool", entries["abs_exe"].Executable)
	}
}
