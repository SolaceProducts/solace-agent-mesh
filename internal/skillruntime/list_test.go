// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"testing"
)

func TestListResources(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")

	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}
	res := ListResources(ctx)

	if res.Count != 3 {
		t.Errorf("Count: got %d, want 3", res.Count)
	}
	if res.SkillName != "fixture" {
		t.Errorf("SkillName: %q", res.SkillName)
	}

	paths := make(map[string]int64)
	for _, f := range res.Files {
		paths[f.Path] = f.SizeBytes
	}
	for _, want := range []string{"references/auth.md", "references/errors.md", "assets/logo.txt"} {
		if _, ok := paths[want]; !ok {
			t.Errorf("missing %s in list result", want)
		}
	}
	if paths["assets/logo.txt"] == 0 {
		t.Errorf("size_bytes should be > 0 for logo.txt")
	}

	m := res.AsMap()
	files, ok := m["files"].([]map[string]any)
	if !ok {
		t.Fatalf("AsMap files type wrong: %T", m["files"])
	}
	if len(files) != 3 {
		t.Errorf("AsMap files len: %d", len(files))
	}
	if _, has := files[0]["path"]; !has {
		t.Errorf("file entry missing path key")
	}
	if _, has := files[0]["size_bytes"]; !has {
		t.Errorf("file entry missing size_bytes key")
	}
}

func TestListResources_NoResources(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp+"/SKILL.md", "# bare")
	ctx := SkillContext{Name: "bare", LocalPath: tmp}
	res := ListResources(ctx)
	if res.Count != 0 {
		t.Errorf("expected 0 files, got %d", res.Count)
	}
	if len(res.Files) != 0 {
		t.Errorf("expected nil/empty Files, got %v", res.Files)
	}
}
