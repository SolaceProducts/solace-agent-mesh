// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

var toolNameLine = regexp.MustCompile(`(?m)\btool_name:\s*([a-z][a-z0-9_]*)\b`)

var namedTools = map[string]bool{
	"analyze_data":                 true,
	"instantiate_template":         true,
	"detect_objects":               true,
	"create_replenishment_command": true,
}

func TestBuilderCorpus_OnlyShowsBindableBuiltinToolNames(t *testing.T) {
	docs := markdownDocs(t, "settings.SchemaSkillsFS", settings.SchemaSkillsFS(), "")
	docs["builder_agent.yaml instruction"] = loadBuilderPrompt(t).Apps[0].AppConfig.Instruction

	scanned := 0
	for name, body := range docs {
		for _, m := range toolNameLine.FindAllStringSubmatch(body, -1) {
			scanned++
			tool := m[1]
			if toolinit.BuiltinToolHasToolset(tool) || namedTools[tool] {
				continue
			}
			t.Errorf("%s shows `tool_name: %s`, which no built-in toolset registers, so the build save refuses it.\n"+
				"Use a tool a built-in group registers; a skill's or connector's tool is reached through the skill or connector.", name, tool)
		}
	}
	if scanned == 0 {
		t.Fatal("no tool_name line was found in the Builder corpus; the scan asserted nothing")
	}
}

func TestBuilderPrompt_NamesTheRoutesAnUnbindableToolTakes(t *testing.T) {
	doc := loadBuilderPrompt(t)
	instruction := strings.Join(strings.Fields(doc.Apps[0].AppConfig.Instruction), " ")

	for _, want := range []string{
		"`skills:` (e.g. `- name: sam-excel`)",
		"connector component in the build",
		"under both `depends_on` and `connectors`",
		"only when the user named the system",
		"leave the tool out and say a connector has to be attached",
		"`sub_task` and `index_search` need no entry",
		"never copy a name from it into `tools:`",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("the prompt no longer says %q; the Builder is told how each tool route works, and this sentence is part of it", want)
		}
	}
	if strings.Contains(instruction, "will only work if a deployed Secure Tool Runtime worker provides it") {
		t.Error("the prompt tells the Builder an unlisted tool_name works when a Secure Tool Runtime worker provides it; the save refuses it")
	}
}

func TestToolSchemaDescription_DoesNotPromiseAnSTRNameResolves(t *testing.T) {
	data, err := schema.SchemaContentFiltered("tool")
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Join(strings.Fields(string(data)), " ")

	if strings.Contains(content, "resolves only if a Secure Tool Runtime (STR) worker hosts it") {
		t.Error("the tool schema still says an unlisted tool_name resolves when an STR worker hosts it; the build save refuses such a name")
	}
	for _, want := range []string{"only when a built-in group registers it", "For the Builder, a tool no group registers", "through a skill", "through a connector"} {
		if !strings.Contains(content, want) {
			t.Errorf("the tool schema's tool_name description no longer says %q", want)
		}
	}
}
