// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"path"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

var formerlyUnsavedAgentKeys = []string{
	"supports_streaming",
	"artifact_handling_mode",
	"inject_user_profile",
	"enable_embed_resolution",
	"enable_artifact_content_instruction",
	"max_llm_calls_per_task",
}

var notSavedPhrases = []string{"does not save", "not saved"}

func TestBuilderCorpus_NeverSaysASavedKeyIsNotSaved(t *testing.T) {
	files := builderCorpusFiles(t)
	for name, body := range files {
		if path.Base(name) == "SKILL.md" {
			body, _ = inlineSharedSchemas(t, body)
		}
		for i, line := range strings.Split(body, "\n") {
			for _, key := range formerlyUnsavedAgentKeys {
				if !strings.Contains(line, key) {
					continue
				}
				for _, phrase := range notSavedPhrases {
					if strings.Contains(line, phrase) {
						t.Errorf("%s:%d says %s is %q, but the Builder saves it:\n\t%s", name, i+1, key, phrase, strings.TrimSpace(line))
					}
				}
			}
		}
		if strings.Contains(strings.Join(strings.Fields(body), " "), "The Builder does not save this key") {
			t.Errorf("%s still says \"The Builder does not save this key\"; every agent key is saved now", name)
		}
	}
}

func TestBuilderGuidance_TeachesNullRemoval(t *testing.T) {
	files := builderCorpusFiles(t)
	skill := ""
	for name, body := range files {
		if path.Base(path.Dir(name)) == "sam-agent-schema" && path.Base(name) == "SKILL.md" {
			skill, _ = inlineSharedSchemas(t, body)
		}
	}
	if skill == "" {
		t.Fatal("sam-agent-schema/SKILL.md not found in the Builder corpus")
	}
	flat := strings.Join(strings.Fields(skill), " ")
	for _, want := range []string{
		"The Builder saves every key in this schema",
		"Leaving a key out keeps the agent's stored value",
		"write the key with `null`",
		"sets `required_scopes` or `auth` on a built-in entry is refused",
		"Gates only add, as in the runtime. A tool entry's `hil` is laid over its group's `hil` field by field",
		"Leaving it out keeps the stored value; `null` removes it.",
		"`volumes`, `networks`, and `allow_loopback` inside a built-in tool's `tool_config`",
		"peer access needs `allow_list: []`",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("sam-agent-schema no longer says %q", want)
		}
	}

	prompt := strings.Join(strings.Fields(files["builder_agent.yaml"]), " ")
	if !strings.Contains(prompt, "`null` removes it") {
		t.Error("the Builder prompt no longer teaches that `null` removes a stored key")
	}
}

func TestBuilderGuidance_TeachesBuiltinApprovals(t *testing.T) {
	files := builderCorpusFiles(t)
	skill := ""
	for name, body := range files {
		if path.Base(path.Dir(name)) == "sam-agent-schema" && path.Base(name) == "SKILL.md" {
			skill, _ = inlineSharedSchemas(t, body)
		}
	}
	if skill == "" {
		t.Fatal("sam-agent-schema/SKILL.md not found in the Builder corpus")
	}
	flat := strings.Join(strings.Fields(skill), " ")
	for _, want := range []string{
		"The Builder saves a `hil:` block on a `tool_type: builtin` entry for that agent",
		"on a `tool_type: builtin-group` entry for every member tool the group grants",
		"Gates only add, as in the runtime. A tool entry's `hil` is laid over its group's `hil` field by field",
		"`hil: null` or `hil: {}` removes it",
		"A `tool_config` on a `builtin-group` entry is not saved",
		"approval_message",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("sam-agent-schema no longer says %q", want)
		}
	}

	if _, found := mapAt(filteredSchema(t, "tool"), "$defs", "builtin-group", "properties", "hil"); !found {
		t.Error("the Builder's tool schema does not offer hil on a builtin-group entry")
	}

	prompt := strings.Join(strings.Fields(files["builder_agent.yaml"]), " ")
	if !strings.Contains(prompt, "`sam-agent-schema` §12") {
		t.Error("the Builder prompt no longer points at sam-agent-schema §12 for built-in approvals")
	}
}

func TestBuilderGuidance_StopsTeachingRetiredForms(t *testing.T) {
	agent := filteredSchema(t, "agent")
	if _, found := mapAt(agent, "properties", "max_concurrent_tasks"); found {
		t.Error("the Builder's agent schema still offers max_concurrent_tasks, which the runtime ignores")
	}
	if _, found := mapAt(agent, "properties", "skills", "items", "properties", "path"); found {
		t.Error("the Builder's agent schema still offers a skills entry path")
	}
	if _, found := mapAt(agent, "properties", "skills", "items", "properties", "name"); !found {
		t.Error("the Builder's agent schema lost the skills entry name; the check above would pass for the wrong reason")
	}

	tool := filteredSchema(t, "tool")
	if _, found := mapAt(tool, "$defs", "builtin", "properties", "timeout_seconds"); found {
		t.Error("the Builder's tool schema still offers timeout_seconds on a built-in entry")
	}
	if _, found := mapAt(tool, "$defs", "builtin", "properties", "tool_name"); !found {
		t.Error("the Builder's tool schema lost the built-in tool_name; the check above would pass for the wrong reason")
	}

	for name, body := range builderCorpusFiles(t) {
		if path.Base(name) != "SKILL.md" {
			continue
		}
		if strings.Contains(skillsBlocks(body), "- path:") {
			t.Errorf("%s shows a skills entry with path:, which the Builder does not offer", name)
		}
	}
}

func filteredSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := schema.SchemaContentFiltered(name)
	if err != nil {
		t.Fatalf("SchemaContentFiltered(%q): %v", name, err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("filtered %s schema is not YAML: %v", name, err)
	}
	return out
}

func mapAt(root map[string]any, keys ...string) (map[string]any, bool) {
	cur := root
	for _, key := range keys {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func skillsBlocks(body string) string {
	var out strings.Builder
	inBlock := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case line == "skills:" || strings.HasPrefix(line, "skills:"):
			inBlock = true
		case inBlock && (strings.HasPrefix(line, " ") || strings.HasPrefix(trimmed, "-")) && trimmed != "":
			out.WriteString(line + "\n")
		case inBlock && trimmed != "":
			inBlock = false
		}
	}
	return out.String()
}

func inlineSharedSchemas(t *testing.T, skill string) (string, bool) {
	t.Helper()
	inlined := false
	for placeholder, name := range schema.SkillSchemaInline() {
		if !strings.Contains(skill, placeholder) {
			continue
		}
		data, err := schema.SchemaContentFiltered(name)
		if err != nil {
			continue
		}
		skill = strings.ReplaceAll(skill, placeholder, string(data))
		inlined = true
	}
	return skill, inlined
}
