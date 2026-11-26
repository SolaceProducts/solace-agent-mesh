// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func strLoadResult(t *testing.T, skillsDir, name string) map[string]any {
	t.Helper()
	ctx, err := skillruntime.LoadFromDisk(skillsDir, name)
	if err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	wire, err := json.Marshal(skillruntime.LoadSkill(ctx).AsMap())
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(wire, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func writeSkillFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSkillResultNamesTheResourceFilesAndTheToolsThatReadThem(t *testing.T) {
	skillsDir := t.TempDir()
	skillDir := filepath.Join(skillsDir, "api-docs")
	writeSkillFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: api-docs\n---\nUse the docs.\n")
	writeSkillFile(t, filepath.Join(skillDir, "references", "auth.md"), "# Auth\n")
	writeSkillFile(t, filepath.Join(skillDir, "assets", "schema.json"), "{}\n")

	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "api-docs"})
	lt := NewLoadSkillTool(mgr, &mockSkillLoader{result: strLoadResult(t, skillsDir, "api-docs")}, &mockToolRegistrar{})

	result, err := lt.Execute(t.Context(), map[string]any{"name": "api-docs"},
		&tool.Context{SkillState: &mockSkillState{loaded: map[string]bool{}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"references/auth.md", "assets/schema.json",
		skillruntime.ListResourcesToolName, skillruntime.ReadResourceToolName, skillruntime.GrepResourcesToolName,
	} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("load_skill message does not contain %q: %s", want, result.Message)
		}
	}
	if got := mgr.GetLoaded("api-docs").ResourceFiles; len(got) != 2 {
		t.Errorf("loaded skill's ResourceFiles = %v, want both files kept for argument errors", got)
	}
}

func TestLoadSkillResultFromAnSTRThatSendsNoPathsPointsAtTheListTool(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "legacy"})
	loader := &mockSkillLoader{result: map[string]any{"instructions": "Body.", "resource_count": float64(4)}}
	lt := NewLoadSkillTool(mgr, loader, &mockToolRegistrar{})

	result, err := lt.Execute(t.Context(), map[string]any{"name": "legacy"},
		&tool.Context{SkillState: &mockSkillState{loaded: map[string]bool{}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message, "4 resource files") ||
		!strings.Contains(result.Message, "Call list_skill_resources to see their paths") {
		t.Errorf("message = %s, want the count and a pointer to list_skill_resources", result.Message)
	}
}

func TestLoadingASkillWithResourcesBeforeItsInitShowsTheResourceTools(t *testing.T) {
	skillsDir := t.TempDir()
	skillDir := filepath.Join(skillsDir, "api-docs")
	writeSkillFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: api-docs\n---\nUse the docs.\n")
	writeSkillFile(t, filepath.Join(skillDir, "references", "auth.md"), "# Auth\n")

	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "api-docs"})
	if mgr.HasAnyResources() {
		t.Fatal("a skill known by name only already counts as having resources")
	}
	lt := NewLoadSkillTool(mgr, &mockSkillLoader{result: strLoadResult(t, skillsDir, "api-docs")}, &mockToolRegistrar{})
	if _, err := lt.Execute(t.Context(), map[string]any{"name": "api-docs"},
		&tool.Context{SkillState: &mockSkillState{loaded: map[string]bool{}}}); err != nil {
		t.Fatal(err)
	}
	if !mgr.HasAnyResources() {
		t.Error("the resource tools stay hidden after load_skill reported the skill's resource files")
	}
}

func TestLoadingASkillWithOnlyAssetsBeforeItsInitShowsTheTemplateTools(t *testing.T) {
	skillsDir := t.TempDir()
	skillDir := filepath.Join(skillsDir, "report-kit")
	writeSkillFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: report-kit\n---\nUse the template.\n")
	writeSkillFile(t, filepath.Join(skillDir, "assets", "report.samt"), "template\n")

	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "report-kit"})
	lt := NewLoadSkillTool(mgr, &mockSkillLoader{result: strLoadResult(t, skillsDir, "report-kit")}, &mockToolRegistrar{})
	if _, err := lt.Execute(t.Context(), map[string]any{"name": "report-kit"},
		&tool.Context{SkillState: &mockSkillState{loaded: map[string]bool{}}}); err != nil {
		t.Fatal(err)
	}
	if !mgr.HasAnyAssets() || !mgr.HasAnyResources() {
		t.Errorf("assets %v, resources %v after loading a skill with assets, want both on", mgr.HasAnyAssets(), mgr.HasAnyResources())
	}
}
