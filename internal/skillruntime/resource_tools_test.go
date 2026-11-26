// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceToolDescriptionsNameEveryArgument(t *testing.T) {
	specs := ResourceTools()
	if len(specs) != 3 {
		t.Fatalf("ResourceTools() = %d specs, want list, read and grep", len(specs))
	}
	for _, spec := range specs {
		properties, _ := spec.Parameters["properties"].(map[string]any)
		if len(properties) == 0 {
			t.Errorf("%s declares no parameters", spec.Name)
		}
		for arg := range properties {
			if !strings.Contains(spec.Description, arg) {
				t.Errorf("%s description does not name its %s argument: %q", spec.Name, arg, spec.Description)
			}
			if !strings.Contains(spec.Usage, arg) {
				t.Errorf("%s usage does not name its %s argument: %q", spec.Name, arg, spec.Usage)
			}
		}
		if ResourceToolUsage(spec.Name) != spec.Usage {
			t.Errorf("ResourceToolUsage(%s) does not return the spec's usage", spec.Name)
		}
	}
}

func TestLoadSkillNamesABoundedListOfResourceFiles(t *testing.T) {
	skillsDir := t.TempDir()
	skillDir := filepath.Join(skillsDir, "big")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: big\n---\nBody.\n")
	const fileCount = MaxListedResourcePaths + 10
	for i := range fileCount {
		writeFile(t, filepath.Join(skillDir, "references", fmt.Sprintf("doc%03d.md", i)), "content\n")
	}

	ctx, err := LoadFromDisk(skillsDir, "big")
	if err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	res := LoadSkill(ctx)
	if res.ResourceCount != fileCount {
		t.Fatalf("ResourceCount = %d, want %d", res.ResourceCount, fileCount)
	}
	if len(res.ResourceFiles) != MaxListedResourcePaths {
		t.Fatalf("ResourceFiles has %d paths, want the first %d", len(res.ResourceFiles), MaxListedResourcePaths)
	}
	if res.ResourceFiles[0] != "references/doc000.md" {
		t.Errorf("first resource file = %q, want a path read_skill_resource takes", res.ResourceFiles[0])
	}
	if got, _ := res.AsMap()["resource_files"].([]string); len(got) != MaxListedResourcePaths {
		t.Errorf("AsMap resource_files = %v, want the listed paths on the wire", got)
	}

	guidance := LoadedResourcesGuidance("big", res.ResourceFiles, res.ResourceCount)
	for _, want := range []string{"references/doc049.md", "and 10 more", ReadResourceToolName, GrepResourcesToolName} {
		if !strings.Contains(guidance, want) {
			t.Errorf("guidance does not contain %q: %s", want, guidance)
		}
	}
	if strings.Contains(guidance, "references/doc050.md") {
		t.Errorf("guidance names a path past the bound: %s", guidance)
	}
}

func TestLoadSkillWithoutResourcesNamesNoFiles(t *testing.T) {
	skillsDir := t.TempDir()
	writeFile(t, filepath.Join(skillsDir, "bare", "SKILL.md"), "---\nname: bare\n---\nBody.\n")
	ctx, err := LoadFromDisk(skillsDir, "bare")
	if err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	if _, present := LoadSkill(ctx).AsMap()["resource_files"]; present {
		t.Error("a skill with no resources should carry no resource_files")
	}
}

func TestGrepMatchFeedsStraightIntoRead(t *testing.T) {
	tmp := t.TempDir()
	buildFixtureSkill(t, tmp, "fixture")
	ctx, err := LoadFromDisk(tmp, "fixture")
	if err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	grep, err := GrepResources(ctx, GrepOptions{Pattern: "403"})
	if err != nil {
		t.Fatalf("GrepResources: %v", err)
	}
	matches, _ := grep.AsMap()["matches"].([]map[string]any)
	if len(matches) != 1 {
		t.Fatalf("matches = %v, want the one 403 line", matches)
	}
	match := matches[0]
	path, _ := match["path"].(string)
	line, _ := match["line"].(int)

	read, err := ReadResource(ctx, path, line, line)
	if err != nil {
		t.Fatalf("reading the match's path %q: %v", path, err)
	}
	if read.Content != "403 forbidden" {
		t.Errorf("content at the match's line = %q, want the matched line", read.Content)
	}
	if match["file"] != path {
		t.Errorf("file = %v, path = %v: the existing key must keep carrying the same path", match["file"], path)
	}
}

func TestReadResourceRejectionNamesTheArgumentsAndTheFiles(t *testing.T) {
	tmp := t.TempDir()
	buildFixtureSkill(t, tmp, "fixture")
	ctx, err := LoadFromDisk(tmp, "fixture")
	if err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	cases := []struct {
		name     string
		path     string
		sentinel error
	}{
		{"no path", "", ErrInvalidParam},
		{"a file that does not exist", "references/missing.md", ErrInvalidPath},
		{"a path outside references/ and assets/", "SKILL.md", ErrPathNotInResources},
		{"the references directory itself", "references", ErrPathNotInResources},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, readErr := ReadResource(ctx, tc.path, 0, 0)
			if !errors.Is(readErr, tc.sentinel) {
				t.Fatalf("err = %v, want %v", readErr, tc.sentinel)
			}
			for _, want := range []string{"references/auth.md", "references/errors.md", "assets/logo.txt"} {
				if !strings.Contains(readErr.Error(), want) {
					t.Errorf("error does not name %s: %v", want, readErr)
				}
			}
		})
	}

	_, err = ReadResource(ctx, "", 0, 0)
	if !strings.Contains(err.Error(), ResourceToolUsage(ReadResourceToolName)) {
		t.Errorf("a missing path should repeat the arguments read_skill_resource takes: %v", err)
	}
}

func TestReadResourceRejectionForASkillWithoutFiles(t *testing.T) {
	ctx := SkillContext{Name: "empty", LocalPath: t.TempDir()}
	_, err := ReadResource(ctx, "references/a.md", 0, 0)
	if !errors.Is(err, ErrInvalidPath) || !strings.Contains(err.Error(), `Skill "empty" has no resource files`) {
		t.Errorf("err = %v, want a not-found error saying the skill has no files", err)
	}
}
