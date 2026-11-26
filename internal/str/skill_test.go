// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkillMD(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func writeSkillRef(t *testing.T, dir, skillName, refName, content string) {
	t.Helper()
	refsDir := filepath.Join(dir, skillName, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatalf("mkdir refs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, refName), []byte(content), 0o644); err != nil {
		t.Fatalf("write ref: %v", err)
	}
}

const basicSkillMD = `---
name: coding-standards
description: Coding standards for the team
tags:
  - coding
  - standards
---

# Coding Standards
Follow these rules when writing code.
`

func TestSkillRegistry_LoadBasic(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "coding-standards", basicSkillMD)

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	entry := reg.GetSkill("coding-standards")
	if entry == nil {
		t.Fatal("expected skill entry")
	}
	if entry.Name != "coding-standards" {
		t.Errorf("name = %q, want coding-standards", entry.Name)
	}
	if entry.Description != "Coding standards for the team" {
		t.Errorf("description = %q", entry.Description)
	}
	if len(entry.Tags) != 2 || entry.Tags[0] != "coding" || entry.Tags[1] != "standards" {
		t.Errorf("tags = %v", entry.Tags)
	}
	if entry.InstructionContent != "# Coding Standards\nFollow these rules when writing code." {
		t.Errorf("instruction = %q", entry.InstructionContent)
	}
	if entry.LocalPath != filepath.Join(dir, "coding-standards") {
		t.Errorf("localPath = %q", entry.LocalPath)
	}
}

func TestSkillRegistry_LoadWithReferences(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "api-ref", `---
name: api-ref
description: API reference
---
Use grep_skill_resources to search.
`)
	writeSkillRef(t, dir, "api-ref", "endpoints.md", "GET /users\n")
	writeSkillRef(t, dir, "api-ref", "examples.md", "POST /users\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	entry := reg.GetSkill("api-ref")
	if entry == nil {
		t.Fatal("expected skill entry")
	}
	if !entry.HasReferences {
		t.Error("expected HasReferences=true")
	}
	if entry.ResourceCount != 2 {
		t.Errorf("resourceCount = %d, want 2", entry.ResourceCount)
	}
	if entry.ReferenceCount != 2 {
		t.Errorf("referenceCount = %d, want 2", entry.ReferenceCount)
	}
	if entry.AssetCount != 0 {
		t.Errorf("assetCount = %d, want 0", entry.AssetCount)
	}
}

func TestSkillRegistry_LoadWithAssets(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "visual", `---
name: visual
description: Skill with assets
---
Instructions here.
`)
	assetsDir := filepath.Join(dir, "visual", "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "logo.png"), []byte("fake image"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	entry := reg.GetSkill("visual")
	if entry == nil {
		t.Fatal("expected skill entry")
	}
	if !entry.HasAssets {
		t.Error("expected HasAssets=true")
	}
	if entry.ResourceCount != 1 {
		t.Errorf("resourceCount = %d, want 1", entry.ResourceCount)
	}
	if entry.AssetCount != 1 {
		t.Errorf("assetCount = %d, want 1", entry.AssetCount)
	}
	if entry.ReferenceCount != 0 {
		t.Errorf("referenceCount = %d, want 0", entry.ReferenceCount)
	}
}

func TestSkillRegistry_LoadMultiple(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "skill-a", "---\nname: skill-a\ndescription: A\n---\nA instructions\n")
	writeSkillMD(t, dir, "skill-b", "---\nname: skill-b\ndescription: B\n---\nB instructions\n")
	writeSkillMD(t, dir, "skill-c", "---\nname: skill-c\ndescription: C\n---\nC instructions\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	names := reg.SkillNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(names))
	}
	for _, name := range []string{"skill-a", "skill-b", "skill-c"} {
		if _, ok := names[name]; !ok {
			t.Errorf("missing skill %q", name)
		}
	}
}

func TestSkillRegistry_LoadEmptyDir(t *testing.T) {
	dir := t.TempDir()

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(reg.Skills()) != 0 {
		t.Errorf("expected 0 skills, got %d", len(reg.Skills()))
	}
}

func TestSkillRegistry_LoadNonexistentDir(t *testing.T) {
	reg := NewSkillRegistry("/tmp/nonexistent-skills-dir-" + t.Name())
	if err := reg.Load(); err != nil {
		t.Fatalf("expected no error for missing dir, got: %v", err)
	}
	if len(reg.Skills()) != 0 {
		t.Errorf("expected 0 skills, got %d", len(reg.Skills()))
	}
}

func TestSkillRegistry_LoadSkipsDirWithoutSkillMD(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillMD(t, dir, "valid", "---\nname: valid\ndescription: valid\n---\nInstructions\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(reg.Skills()) != 1 {
		t.Errorf("expected 1 skill, got %d", len(reg.Skills()))
	}
}

func TestSkillRegistry_LoadMalformedFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "good", "---\nname: good\ndescription: Good\n---\nInstructions\n")
	writeSkillMD(t, dir, "bad", "---\n: invalid: : yaml: [\n---\nBody\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(reg.Skills()) != 1 {
		t.Errorf("expected 1 skill (bad skipped), got %d", len(reg.Skills()))
	}
	if reg.GetSkill("good") == nil {
		t.Error("expected good skill to be present")
	}
}

func TestSkillRegistry_LoadNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "bare", "# Just Instructions\nNo frontmatter here.\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	entry := reg.GetSkill("bare")
	if entry == nil {
		t.Fatal("expected skill entry with directory name")
	}
	if entry.Name != "bare" {
		t.Errorf("name = %q, want bare", entry.Name)
	}
	if entry.Description != "" {
		t.Errorf("expected empty description, got %q", entry.Description)
	}
}

func TestSkillRegistry_LoadSymlink(t *testing.T) {
	dir := t.TempDir()
	realDir := t.TempDir()
	writeSkillMD(t, realDir, "real-skill", "---\nname: linked-skill\ndescription: Via symlink\n---\nContent\n")

	if err := os.Symlink(filepath.Join(realDir, "real-skill"), filepath.Join(dir, "linked-skill")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	entry := reg.GetSkill("linked-skill")
	if entry == nil {
		t.Fatal("expected symlinked skill to be discovered")
	}
	if entry.Description != "Via symlink" {
		t.Errorf("description = %q", entry.Description)
	}
}

func TestSkillRegistry_LoadAllFrontmatterFields(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "full", `---
name: full-skill
description: Full skill with all fields
tags:
  - tag1
  - tag2
examples:
  - "How do I use this?"
  - "Show me an example"
required_builtins:
  - web_request
---

# Full Skill
Instructions body.
`)

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	entry := reg.GetSkill("full-skill")
	if entry == nil {
		t.Fatal("expected skill entry")
	}
	if len(entry.Tags) != 2 {
		t.Errorf("tags = %v, want 2", entry.Tags)
	}
	if len(entry.Examples) != 2 {
		t.Errorf("examples = %v, want 2", entry.Examples)
	}
	if len(entry.RequiredBuiltins) != 1 || entry.RequiredBuiltins[0] != "web_request" {
		t.Errorf("requiredBuiltins = %v", entry.RequiredBuiltins)
	}
}

func TestSkillRegistry_ReloadDetectsAdded(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "existing", "---\nname: existing\ndescription: Existing\n---\nBody\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(reg.Skills()) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(reg.Skills()))
	}

	writeSkillMD(t, dir, "new-skill", "---\nname: new-skill\ndescription: New\n---\nNew body\n")

	diff, err := reg.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if diff == nil {
		t.Fatal("expected diff, got nil")
	}
	if _, ok := diff.Added["new-skill"]; !ok {
		t.Errorf("expected new-skill in Added, got %v", diff.Added)
	}
	if len(diff.Removed) != 0 {
		t.Errorf("expected no removals, got %v", diff.Removed)
	}
	if len(diff.Modified) != 0 {
		t.Errorf("expected no modifications, got %v", diff.Modified)
	}

	if reg.GetSkill("new-skill") == nil {
		t.Error("new-skill should be in registry after reload")
	}
}

func TestSkillRegistry_ReloadDetectsRemoved(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "keep", "---\nname: keep\ndescription: Keep\n---\nBody\n")
	writeSkillMD(t, dir, "remove-me", "---\nname: remove-me\ndescription: Doomed\n---\nBody\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(reg.Skills()) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(reg.Skills()))
	}

	if err := os.RemoveAll(filepath.Join(dir, "remove-me")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	diff, err := reg.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if diff == nil {
		t.Fatal("expected diff, got nil")
	}
	if _, ok := diff.Removed["remove-me"]; !ok {
		t.Errorf("expected remove-me in Removed, got %v", diff.Removed)
	}
	if len(diff.Added) != 0 {
		t.Errorf("expected no additions, got %v", diff.Added)
	}

	if reg.GetSkill("remove-me") != nil {
		t.Error("remove-me should not be in registry after reload")
	}
	if reg.GetSkill("keep") == nil {
		t.Error("keep should still be in registry")
	}
}

func TestSkillRegistry_ReloadDetectsModified(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "mutable", "---\nname: mutable\ndescription: Version 1\n---\nOriginal body\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	entry := reg.GetSkill("mutable")
	if entry.Description != "Version 1" {
		t.Fatalf("description = %q, want Version 1", entry.Description)
	}

	writeSkillMD(t, dir, "mutable", "---\nname: mutable\ndescription: Version 2\n---\nUpdated body\n")

	diff, err := reg.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if diff == nil {
		t.Fatal("expected diff, got nil")
	}
	if _, ok := diff.Modified["mutable"]; !ok {
		t.Errorf("expected mutable in Modified, got %v", diff.Modified)
	}

	entry = reg.GetSkill("mutable")
	if entry.Description != "Version 2" {
		t.Errorf("description = %q, want Version 2", entry.Description)
	}
	if entry.InstructionContent != "Updated body" {
		t.Errorf("instruction = %q, want Updated body", entry.InstructionContent)
	}
}

func TestSkillRegistry_ReloadNoChanges(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "stable", "---\nname: stable\ndescription: Stable\n---\nBody\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	diff, err := reg.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if diff != nil {
		t.Errorf("expected nil diff for no changes, got %+v", diff)
	}
}

func TestSkillRegistry_ReloadAddedReference(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "docs", "---\nname: docs\ndescription: Docs\n---\nBody\n")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	entry := reg.GetSkill("docs")
	if entry.HasReferences {
		t.Fatal("expected no references initially")
	}

	writeSkillRef(t, dir, "docs", "guide.md", "# Guide\n")

	diff, err := reg.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if diff == nil {
		t.Fatal("expected diff after adding reference")
	}
	if _, ok := diff.Modified["docs"]; !ok {
		t.Errorf("expected docs in Modified, got %v", diff.Modified)
	}

	entry = reg.GetSkill("docs")
	if !entry.HasReferences {
		t.Error("expected HasReferences=true after adding reference")
	}
	if entry.ResourceCount != 1 {
		t.Errorf("resourceCount = %d, want 1", entry.ResourceCount)
	}
}

func TestSkillRegistry_ParsesToolsetsField(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "with-toolsets", `---
name: with-toolsets
description: Skill that brings two toolsets on load
toolsets:
  - bamboohr_profile_provider
  - builtin_file_tools
---
Instructions body.
`)

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	entry := reg.GetSkill("with-toolsets")
	if entry == nil {
		t.Fatal("expected skill entry")
	}
	want := []string{"bamboohr_profile_provider", "builtin_file_tools"}
	if len(entry.Toolsets) != len(want) {
		t.Fatalf("toolsets = %v, want %v", entry.Toolsets, want)
	}
	for i, ts := range want {
		if entry.Toolsets[i] != ts {
			t.Errorf("toolsets[%d] = %q, want %q", i, entry.Toolsets[i], ts)
		}
	}
}

func TestSkillRegistry_ParsesToolsAndToolsetsTogether(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "mixed", `---
name: mixed
description: Mixed declaration
tools:
  - tool_type: builtin
    tool_name: get_current_time
toolsets:
  - bamboohr_profile_provider
---
Body.
`)

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	entry := reg.GetSkill("mixed")
	if entry == nil {
		t.Fatal("expected entry")
	}
	if len(entry.Tools) != 1 || entry.Tools[0]["tool_name"] != "get_current_time" {
		t.Errorf("tools = %v, want one entry naming get_current_time", entry.Tools)
	}
	if len(entry.Toolsets) != 1 || entry.Toolsets[0] != "bamboohr_profile_provider" {
		t.Errorf("toolsets = %v, want [bamboohr_profile_provider]", entry.Toolsets)
	}
}

func TestSkillRegistry_ReloadDetectsToolsetsChange(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "ts-skill", `---
name: ts-skill
description: ts
toolsets:
  - alpha
---
Body.
`)

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	writeSkillMD(t, dir, "ts-skill", `---
name: ts-skill
description: ts
toolsets:
  - alpha
  - beta
---
Body.
`)

	diff, err := reg.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if diff == nil {
		t.Fatal("expected diff after toolsets-only change")
	}
	if _, ok := diff.Modified["ts-skill"]; !ok {
		t.Errorf("expected ts-skill in Modified, got %v", diff.Modified)
	}
}
