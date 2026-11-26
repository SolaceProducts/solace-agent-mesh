// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

const runtimeConfigHeading = "### Runtime config files"

func agentSchemaSkill(t *testing.T) string {
	t.Helper()
	for name, body := range markdownDocs(t, "settings.SchemaSkillsFS", settings.SchemaSkillsFS(), "") {
		if strings.HasSuffix(name, "sam-agent-schema/SKILL.md") {
			return body
		}
	}
	t.Fatal("sam-agent-schema/SKILL.md is not in the Builder's skills")
	return ""
}

func TestAgentSchemaSkill_TeachesModelProviderNotModel(t *testing.T) {
	skill := agentSchemaSkill(t)
	builderSection, runtimeSection, found := strings.Cut(skill, runtimeConfigHeading)
	if !found {
		t.Fatalf("the skill no longer separates runtime config files from the Builder with %q", runtimeConfigHeading)
	}
	start := strings.Index(builderSection, "## 2. LLM Model Configuration")
	if start < 0 {
		t.Fatal("section 2 of the skill is gone")
	}
	forBuilder := builderSection[start:]

	for _, want := range []string{
		"`model_provider`",
		`model_provider: ["planning"]`,
		"`platform_list_components` with `component_type: model`",
		"Never write `model:`",
		"beside it on the `apps[]` item",
	} {
		if !strings.Contains(forBuilder, want) {
			t.Errorf("the Builder half of section 2 no longer says %q", want)
		}
	}
	for line := range strings.SplitSeq(forBuilder, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "model:") {
			t.Errorf("the Builder half of section 2 shows a model: line the save refuses: %q", line)
		}
	}
	if !strings.Contains(runtimeSection, `model: "openai/gpt-4o"`) {
		t.Error("the runtime-config half lost the literal model: form it exists to document")
	}
}

func TestBuilderPrompt_NamesTheOneKeyThatSetsAModel(t *testing.T) {
	instruction := strings.Join(strings.Fields(loadBuilderPrompt(t).Apps[0].AppConfig.Instruction), " ")
	for _, want := range []string{
		"the only key that sets it is `model_provider`, inside `app_config`",
		"`platform_list_components` with `component_type: model`",
		"To switch an existing agent's model, edit its artifact and set that key.",
		"Never write `model:` for it, in `app_config` or beside it",
		"Tell the user the model changed only after the save succeeds.",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("the prompt no longer says %q; it is how the Builder learns where an agent's model lives", want)
		}
	}
}

func TestBuilderDesignGuides_ModelConfigurationRowNamesModelProvider(t *testing.T) {
	checked := 0
	for _, guide := range []string{"builder/agent-design.md", "builder/efficiency.md"} {
		body, err := fs.ReadFile(design.FS(), guide)
		if err != nil {
			t.Fatalf("read %s: %v", guide, err)
		}
		for line := range strings.Lines(string(body)) {
			if !strings.HasPrefix(line, "| Model configuration |") {
				continue
			}
			checked++
			if !strings.Contains(line, "`model_provider`") || !strings.Contains(line, "`sam-agent-schema` section 2") {
				t.Errorf("%s: the Model configuration row does not point at model_provider: %q", guide, line)
			}
		}
	}
	if checked != 2 {
		t.Errorf("found %d Model configuration rows in the Builder lookup tables, want 2", checked)
	}
}
