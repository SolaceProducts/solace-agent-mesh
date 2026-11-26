// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builtinskills

import (
	"io/fs"
	"path"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSkillProvidingTool(t *testing.T) {
	tests := []struct {
		tool      string
		wantSkill string
		wantOK    bool
	}{
		{"xlsx_read", "sam-excel", true},
		{"xlsx_write", "sam-excel", true},
		{"docx_create_document", "sam-docx", true},
		{"web_request", "", false},
		{"execute_sql_query", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			skill, ok := SkillProvidingTool(tt.tool)
			if skill != tt.wantSkill || ok != tt.wantOK {
				t.Errorf("SkillProvidingTool(%q) = %q, %v; want %q, %v", tt.tool, skill, ok, tt.wantSkill, tt.wantOK)
			}
		})
	}
}

func TestEveryOwnedToolBelongsToARealSkill(t *testing.T) {
	for tool, skill := range toolOwners() {
		if !HasName(skill) {
			t.Errorf("tool %q is attributed to %q, which is not a built-in skill", tool, skill)
		}
	}
	if len(toolOwners()) == 0 {
		t.Fatal("no built-in skill tool was indexed; the manifests were not read")
	}
}

func TestToolOwningSkillsListsEverySkillThatOwnsATool(t *testing.T) {
	var want []string
	for _, skill := range Names() {
		if internalSkills[skill] {
			continue
		}
		data, err := fs.ReadFile(FS(), path.Join(skill, toolManifestPath))
		if err != nil {
			continue
		}
		var manifest toolManifest
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			t.Fatalf("%s: %v", skill, err)
		}
		if len(manifest.Tools) > 0 {
			want = append(want, skill)
		}
	}
	slices.Sort(want)

	if got := ToolOwningSkills(); !slices.Equal(got, want) || len(want) == 0 {
		t.Errorf("ToolOwningSkills() = %v, want %v (a non-empty list)", got, want)
	}
}
