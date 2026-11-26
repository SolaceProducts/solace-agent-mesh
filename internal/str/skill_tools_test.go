// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import "testing"

const skillMDWithToolsBlock = `---
name: documents
description: Document conversion routing.
tools:
  - tool_type: builtin
    tool_name: convert_file_to_markdown
  - tool_type: builtin
    tool_name: pdf_ops
    timeout_seconds: 120
  - tool_type: builtin-group
    group_name: image_tools
---

# Documents Skill
Body.
`

func TestSkillRegistry_ParsesToolsBlock(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "documents", skillMDWithToolsBlock)

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	entry := reg.GetSkill("documents")
	if entry == nil {
		t.Fatal("missing entry")
	}
	if len(entry.Tools) != 3 {
		t.Fatalf("Tools = %d entries, want 3 (%v)", len(entry.Tools), entry.Tools)
	}
	if tt, _ := entry.Tools[0]["tool_type"].(string); tt != "builtin" {
		t.Errorf("entry[0].tool_type = %q", tt)
	}
	if tn, _ := entry.Tools[1]["tool_name"].(string); tn != "pdf_ops" {
		t.Errorf("entry[1].tool_name = %q", tn)
	}
	if got, ok := entry.Tools[1]["timeout_seconds"]; !ok || got != 120 {
		t.Errorf("entry[1].timeout_seconds = %v (ok=%v), want 120", got, ok)
	}
	if gn, _ := entry.Tools[2]["group_name"].(string); gn != "image_tools" {
		t.Errorf("entry[2].group_name = %q", gn)
	}
}

const skillMDWithDeprecatedRequiredBuiltins = `---
name: legacy
description: legacy skill using required_builtins
required_builtins:
  - get_current_time
  - web_request
---

Body.
`

func TestSkillRegistry_RequiredBuiltins_FoldedIntoTools(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "legacy", skillMDWithDeprecatedRequiredBuiltins)

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	entry := reg.GetSkill("legacy")
	if entry == nil {
		t.Fatal("missing entry")
	}
	if len(entry.Tools) != 2 {
		t.Fatalf("Tools = %d, want 2 (%v)", len(entry.Tools), entry.Tools)
	}
	for i, want := range []string{"get_current_time", "web_request"} {
		if tt, _ := entry.Tools[i]["tool_type"].(string); tt != "builtin" {
			t.Errorf("entry[%d].tool_type = %q", i, tt)
		}
		if tn, _ := entry.Tools[i]["tool_name"].(string); tn != want {
			t.Errorf("entry[%d].tool_name = %q, want %q", i, tn, want)
		}
	}
	if len(entry.RequiredBuiltins) != 2 {
		t.Errorf("RequiredBuiltins should still populate for back-compat, got %v", entry.RequiredBuiltins)
	}
}
