// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromDisk_Happy(t *testing.T) {
	tmp := t.TempDir()
	buildFixtureSkill(t, tmp, "fixture")

	ctx, err := LoadFromDisk(tmp, "fixture")
	if err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	if ctx.Name != "fixture" {
		t.Errorf("Name: got %q, want fixture", ctx.Name)
	}
	if !ctx.HasReferences {
		t.Errorf("HasReferences should be true")
	}
	if !ctx.HasAssets {
		t.Errorf("HasAssets should be true")
	}
	if ctx.ResourceCount != 3 {
		t.Errorf("ResourceCount: got %d, want 3", ctx.ResourceCount)
	}
	if ctx.Description != "Test fixture skill" {
		t.Errorf("Description: got %q", ctx.Description)
	}
	if len(ctx.Tags) != 2 {
		t.Errorf("Tags: got %v, want 2 entries", ctx.Tags)
	}
	if !strings.Contains(ctx.InstructionContent, "test skill") {
		t.Errorf("InstructionContent missing body: %q", ctx.InstructionContent)
	}
	if len(ctx.BundledTools) != 1 {
		t.Errorf("BundledTools: got %d, want 1", len(ctx.BundledTools))
	}
	if len(ctx.RequiredBuiltins) != 1 || ctx.RequiredBuiltins[0] != "read_skill_resource" {
		t.Errorf("RequiredBuiltins: got %v", ctx.RequiredBuiltins)
	}
}

func TestLoadFromDisk_NotFound(t *testing.T) {
	tmp := t.TempDir()
	_, err := LoadFromDisk(tmp, "nonexistent")
	if !errors.Is(err, ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestLoadFromDisk_EmptyArgs(t *testing.T) {
	if _, err := LoadFromDisk("", "fixture"); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("empty skillsDir: expected ErrInvalidParam, got %v", err)
	}
	if _, err := LoadFromDisk("/tmp", ""); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("empty skillName: expected ErrInvalidParam, got %v", err)
	}
}

func TestLoadFromDisk_PathTraversal(t *testing.T) {
	tmp := t.TempDir()
	_, err := LoadFromDisk(tmp, "../etc")
	if !errors.Is(err, ErrPathOutsideSkill) {
		t.Errorf("expected ErrPathOutsideSkill, got %v", err)
	}
}

func TestLoadFromDisk_NoFrontmatter(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "bare")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "# bare\nno frontmatter\n")

	ctx, err := LoadFromDisk(tmp, "bare")
	if err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	if ctx.Name != "bare" {
		t.Errorf("Name should fall back to dir name, got %q", ctx.Name)
	}
	if ctx.HasReferences || ctx.HasAssets {
		t.Errorf("should have no resources, got refs=%v assets=%v", ctx.HasReferences, ctx.HasAssets)
	}
	if ctx.ResourceCount != 0 {
		t.Errorf("ResourceCount: got %d, want 0", ctx.ResourceCount)
	}
}

func TestLoadSkill_ResultShape(t *testing.T) {
	ctx := SkillContext{
		Name:               "demo",
		InstructionContent: "do the thing",
		HasReferences:      true,
		ResourceCount:      2,
		Description:        "demo skill",
		Tags:               []string{"a", "b"},
		Examples:           []string{"ex1"},
		BundledTools:       []map[string]any{{"tool_name": "t1"}},
		RequiredBuiltins:   []string{"read_skill_resource"},
	}
	res := LoadSkill(ctx)

	if res.SkillName != "demo" || res.Instructions != "do the thing" {
		t.Errorf("basic fields: %+v", res)
	}
	if res.Metadata == nil {
		t.Fatalf("Metadata should be populated")
	}
	if res.Metadata["description"] != "demo skill" {
		t.Errorf("metadata.description: %v", res.Metadata["description"])
	}
	if res.Metadata["name"] != "demo" {
		t.Errorf("metadata.name should be skill name: %v", res.Metadata["name"])
	}

	m := res.AsMap()
	if m["skill_name"] != "demo" {
		t.Errorf("AsMap skill_name: %v", m["skill_name"])
	}
	if m["bundled_tools"] == nil {
		t.Errorf("AsMap should include bundled_tools when present")
	}
	if m["required_builtins"] == nil {
		t.Errorf("AsMap should include required_builtins when present")
	}
}

func TestLoadSkill_NoMetadata(t *testing.T) {
	ctx := SkillContext{Name: "x", InstructionContent: "y"}
	res := LoadSkill(ctx)
	if res.Metadata != nil {
		t.Errorf("Metadata should be nil when no description/tags/examples")
	}
	m := res.AsMap()
	if _, has := m["metadata"]; has {
		t.Errorf("AsMap should omit empty metadata")
	}
}

func TestLoadFromMount(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")

	ctx, err := LoadFromMount(skillDir, "fixture")
	if err != nil {
		t.Fatalf("LoadFromMount: %v", err)
	}
	if ctx.LocalPath != skillDir {
		t.Errorf("LocalPath: got %q, want %q", ctx.LocalPath, skillDir)
	}
	if !ctx.HasReferences {
		t.Errorf("HasReferences should be true")
	}
}
