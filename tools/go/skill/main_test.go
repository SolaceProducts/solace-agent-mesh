// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func buildFixture(t *testing.T) string {
	t.Helper()
	skillsDir := t.TempDir()
	skillDir := filepath.Join(skillsDir, "fixture")
	mustWrite(t, filepath.Join(skillDir, "SKILL.md"), `---
name: fixture
description: Test fixture
tags:
  - test
---

# fixture

Body text here.
`)
	mustWrite(t, filepath.Join(skillDir, "references", "auth.md"), "# Auth\nJWT tokens.\n")
	mustWrite(t, filepath.Join(skillDir, "assets", "logo.txt"), "logo\n")
	return skillsDir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setSkillsEnv(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("SAM_SKILLS_DIR", dir)
}

func TestLoadSkill_EnvFallback(t *testing.T) {
	dir := buildFixture(t)
	setSkillsEnv(t, dir)

	res, err := loadSkill(t.Context(), LoadSkillParams{SkillName: "fixture"}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("loadSkill: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	if res.Data["skill_name"] != "fixture" {
		t.Errorf("skill_name = %v", res.Data["skill_name"])
	}
	if res.Data["has_references"] != true {
		t.Errorf("has_references = %v", res.Data["has_references"])
	}
	if res.Data["resource_count"] != 2 {
		t.Errorf("resource_count = %v", res.Data["resource_count"])
	}
	meta, _ := res.Data["metadata"].(map[string]any)
	if meta == nil || meta["description"] != "Test fixture" {
		t.Errorf("metadata wrong: %v", res.Data["metadata"])
	}
}

func TestLoadSkill_NoSkillsDir(t *testing.T) {
	t.Setenv("SAM_SKILLS_DIR", "")

	res, err := loadSkill(t.Context(), LoadSkillParams{SkillName: "fixture"}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("loadSkill: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Errorf("status = %q, want error", res.Status)
	}
	if !strings.Contains(res.Message, "skills directory") {
		t.Errorf("message = %q", res.Message)
	}
}

func TestListSkillResources_EnvFallback(t *testing.T) {
	dir := buildFixture(t)
	setSkillsEnv(t, dir)

	res, err := listSkillResources(t.Context(), ListSkillResourcesParams{SkillName: "fixture"}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("listSkillResources: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status: %q", res.Status)
	}
	if res.Data["count"] != 2 {
		t.Errorf("count = %v, want 2", res.Data["count"])
	}
}

func TestReadSkillResource_EnvFallback(t *testing.T) {
	dir := buildFixture(t)
	setSkillsEnv(t, dir)

	res, err := readSkillResource(t.Context(), ReadSkillResourceParams{
		SkillName: "fixture",
		Path:      "references/auth.md",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("readSkillResource: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status: %q, message: %q", res.Status, res.Message)
	}
	content, _ := res.Data["content"].(string)
	if !strings.Contains(content, "# Auth") || !strings.Contains(content, "JWT tokens") {
		t.Errorf("content = %q, want the whole file", content)
	}
}

func TestReadSkillResource_LineRange(t *testing.T) {
	dir := buildFixture(t)
	setSkillsEnv(t, dir)

	res, err := readSkillResource(t.Context(), ReadSkillResourceParams{
		SkillName: "fixture",
		Path:      "references/auth.md",
		StartLine: new(2),
		EndLine:   new(2),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("readSkillResource: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status: %q, message: %q", res.Status, res.Message)
	}
	content, _ := res.Data["content"].(string)
	if !strings.Contains(content, "JWT tokens") {
		t.Errorf("content = %q (expected line 2)", content)
	}
}

func TestReadSkillResource_OutsideAllowedDirs(t *testing.T) {
	dir := buildFixture(t)
	setSkillsEnv(t, dir)

	res, err := readSkillResource(t.Context(), ReadSkillResourceParams{
		SkillName: "fixture",
		Path:      "SKILL.md",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("readSkillResource: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Errorf("expected error for SKILL.md (outside references/assets), got %q", res.Status)
	}
}

func TestGrepSkillResources_EnvFallback(t *testing.T) {
	dir := buildFixture(t)
	setSkillsEnv(t, dir)

	res, err := grepSkillResources(t.Context(), GrepSkillResourcesParams{
		SkillName: "fixture",
		Pattern:   "JWT",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("grepSkillResources: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status: %q", res.Status)
	}
	if res.Data["match_count"] != 1 {
		t.Errorf("match_count = %v, want 1", res.Data["match_count"])
	}
}

func TestGrepSkillResources_CaseInsensitive(t *testing.T) {
	dir := buildFixture(t)
	setSkillsEnv(t, dir)

	res, _ := grepSkillResources(t.Context(), GrepSkillResourcesParams{
		SkillName: "fixture",
		Pattern:   "jwt",
	}, &sdk.ToolContext{})
	if res.Data["match_count"] != 0 {
		t.Errorf("case-sensitive 'jwt' should miss: %v", res.Data["match_count"])
	}

	res, _ = grepSkillResources(t.Context(), GrepSkillResourcesParams{
		SkillName:       "fixture",
		Pattern:         "jwt",
		CaseInsensitive: new(true),
	}, &sdk.ToolContext{})
	if res.Data["match_count"] != 1 {
		t.Errorf("case-insensitive 'jwt' should match: %v", res.Data["match_count"])
	}
}

func TestMissingRequiredParams(t *testing.T) {
	setSkillsEnv(t, buildFixture(t))
	cases := []struct {
		name, want string
		fn         func() *sdk.Result
	}{
		{"loadSkill", "", func() *sdk.Result {
			r, _ := loadSkill(t.Context(), LoadSkillParams{}, &sdk.ToolContext{})
			return r
		}},
		{"listSkillResources", skillruntime.ResourceToolUsage(skillruntime.ListResourcesToolName), func() *sdk.Result {
			r, _ := listSkillResources(t.Context(), ListSkillResourcesParams{}, &sdk.ToolContext{})
			return r
		}},
		{"readSkillResource missing skill_name", skillruntime.ResourceToolUsage(skillruntime.ReadResourceToolName), func() *sdk.Result {
			r, _ := readSkillResource(t.Context(), ReadSkillResourceParams{Path: "x"}, &sdk.ToolContext{})
			return r
		}},
		{"readSkillResource missing path", "path", func() *sdk.Result {
			r, _ := readSkillResource(t.Context(), ReadSkillResourceParams{SkillName: "fixture"}, &sdk.ToolContext{})
			return r
		}},
		{"grepSkillResources missing skill_name", skillruntime.ResourceToolUsage(skillruntime.GrepResourcesToolName), func() *sdk.Result {
			r, _ := grepSkillResources(t.Context(), GrepSkillResourcesParams{Pattern: "x"}, &sdk.ToolContext{})
			return r
		}},
		{"grepSkillResources missing pattern", skillruntime.ResourceToolUsage(skillruntime.GrepResourcesToolName), func() *sdk.Result {
			r, _ := grepSkillResources(t.Context(), GrepSkillResourcesParams{SkillName: "fixture"}, &sdk.ToolContext{})
			return r
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.fn()
			if res.Status != sdk.StatusError {
				t.Errorf("expected error status, got %q", res.Status)
			}
			if !strings.Contains(res.Message, tc.want) {
				t.Errorf("message %q does not say %q", res.Message, tc.want)
			}
		})
	}
}

func TestResourceToolsCarryTheirSharedDescriptions(t *testing.T) {
	for _, spec := range skillruntime.ResourceTools() {
		if got := resourceToolDescription(spec.Name); got == "" || got != spec.Description {
			t.Errorf("%s description = %q, want the shared one", spec.Name, got)
		}
	}
}

func TestSkillParamsSchemasRequireOnlyTheirNamedParams(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"read_skill_resource", sdk.BuildSchema[ReadSkillResourceParams]().Required, []string{"skill_name", "path"}},
		{"grep_skill_resources", sdk.BuildSchema[GrepSkillResourcesParams]().Required, []string{"skill_name", "pattern"}},
	}
	for _, c := range cases {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s required = %v, want %v", c.name, c.got, c.want)
		}
	}
}
