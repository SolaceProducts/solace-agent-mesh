// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolsdoc

import (
	"os"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

var generatedTargets = []string{
	"../settings/files/skills/sam-workflow-schema/SKILL.md",
	"../aiassist/skills/sam-author-agent/references/builtin-tools.md",
}

const (
	beginMarker = "<!-- BEGIN GENERATED: builtin-tool-fields -->"
	endMarker   = "<!-- END GENERATED: builtin-tool-fields -->"
)

func TestGeneratedRegionMatchesRegistry(t *testing.T) {
	t.Parallel()
	requireSTRRegistry(t)
	want := generatedTable(t)

	for _, path := range generatedTargets {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := string(data)

		start := strings.Index(content, beginMarker)
		end := strings.Index(content, endMarker)
		if start < 0 || end < 0 {
			t.Fatalf("%s: missing the %s / %s marker pair", path, beginMarker, endMarker)
		}

		got := content[start+len(beginMarker) : end]
		if got != "\n"+want {
			t.Errorf("%s: generated region is stale — run `go generate ./internal/toolsdoc/...` and commit the result", path)
		}
	}
}

func TestTableCoversEveryBindableFactory(t *testing.T) {
	t.Parallel()
	table := generatedTable(t)

	for _, name := range toolinit.BuiltinToolNames() {
		hasRow := strings.Contains(table, "| `"+name+"` |")
		switch bindable := toolinit.AgentBindableBuiltin(name); {
		case bindable && !hasRow:
			t.Errorf("bindable built-in %q has no row in the generated table", name)
		case !bindable && hasRow:
			t.Errorf("built-in %q is not bindable by a built agent but has a row in the generated table", name)
		}
	}
}

func TestBindableFactoriesBuildWithoutConfig(t *testing.T) {
	t.Parallel()
	for _, name := range toolinit.BuiltinToolNames() {
		if !toolinit.AgentBindableBuiltin(name) {
			continue
		}
		bt := toolinit.LookupBuiltinTool(name, nil, toolinit.Options{})
		if bt == nil {
			t.Errorf("bindable factory %q returned no tool for a nil config", name)
			continue
		}
		if bt.Name() != name {
			t.Errorf("factory %q builds a tool named %q; the row must carry the name an agent binds", name, bt.Name())
		}
	}
}

func TestTableCarriesTheSchedulingFamilyAndLeavesOutBuilderTools(t *testing.T) {
	t.Parallel()
	table := generatedTable(t)
	for _, name := range []string{"schedule_task", "list_scheduled_tasks", "update_scheduled_task", "delete_scheduled_task", "set_scheduled_task_enabled"} {
		if !strings.Contains(table, "| `"+name+"` |") {
			t.Errorf("scheduling tool %q has no row", name)
		}
	}
	for _, name := range []string{"validate_build_manifest", "platform_list_components", "sub_task", "select_voice", "query_task_health"} {
		if strings.Contains(table, "| `"+name+"` |") {
			t.Errorf("%q has a row but a built agent cannot bind it", name)
		}
	}
}

func TestWebRequestRowSeparatesTheTwoNamespaces(t *testing.T) {
	t.Parallel()
	row := rowFor(t, "web_request")

	inputs, results := splitCells(t, row)

	if !strings.Contains(inputs, "`output_artifact_filename`") {
		t.Errorf("input cell must name output_artifact_filename; got %q", inputs)
	}
	if !strings.Contains(results, "`filename`") {
		t.Errorf("result cell must name filename; got %q", results)
	}
	if strings.Contains(results, "`output_filename`") || strings.Contains(results, "`output_artifact_filename`") {
		t.Errorf("result cell must not carry an input parameter name; got %q", results)
	}
	if !strings.Contains(inputs, "`url` (string) **req**") {
		t.Errorf("required inputs must be marked; got %q", inputs)
	}
	if !strings.Contains(results, "`truncated` (boolean) *opt*") {
		t.Errorf("branch-dependent result fields must be marked optional; got %q", results)
	}
}

func TestQueryDataWithSQLKeepsItsOutputFilename(t *testing.T) {
	t.Parallel()
	_, results := splitCells(t, rowFor(t, "query_data_with_sql"))
	if !strings.Contains(results, "`output_filename`") {
		t.Errorf("result cell must name output_filename; got %q", results)
	}
}

func TestToolWithoutInputsOrDataIsStatedExplicitly(t *testing.T) {
	t.Parallel()

	inputs, _ := splitCells(t, rowFor(t, "get_current_time"))
	if !strings.Contains(inputs, "*none*") {
		t.Errorf("a tool with no parameters must say so; got %q", inputs)
	}

	_, results := splitCells(t, rowFor(t, "create_image_from_description"))
	if !strings.Contains(results, "no result fields") {
		t.Errorf("an empty result schema must say so; got %q", results)
	}
}

func TestUndeclaredResultSchemaWarnsRatherThanClaimingNoFields(t *testing.T) {
	t.Parallel()
	undeclared := resultCell(nil)
	if !strings.Contains(undeclared, "not declared") {
		t.Errorf("an undeclared result schema must be reported as unknown; got %q", undeclared)
	}
	if strings.Contains(undeclared, "no result fields") {
		t.Errorf("an undeclared result schema must not read as an empty one; got %q", undeclared)
	}

	empty := resultCell(&tool.JSONSchema{Type: "object", Properties: map[string]any{}})
	if !strings.Contains(empty, "no result fields") {
		t.Errorf("an empty result schema must report that there are none; got %q", empty)
	}
}

func TestGeneratedTableIsDeterministic(t *testing.T) {
	t.Parallel()
	if first, second := generatedTable(t), generatedTable(t); first != second {
		t.Error("two renders differ — property or tool ordering is not stable")
	}
}

func rowFor(t *testing.T, name string) string {
	t.Helper()
	for _, line := range strings.Split(generatedTable(t), "\n") {
		if strings.HasPrefix(line, "| `"+name+"` |") {
			return line
		}
	}
	t.Fatalf("no row for tool %q", name)
	return ""
}

func splitCells(t *testing.T, row string) (inputs, results string) {
	t.Helper()
	cells := strings.Split(strings.Trim(row, "|"), " | ")
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells in %q, got %d", row, len(cells))
	}
	return cells[1], cells[2]
}

func TestBuiltinToolsReferenceNamesTheYAMLOnlyToolsTheTableLeavesOut(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(generatedTargets[1])
	if err != nil {
		t.Fatalf("read %s: %v", generatedTargets[1], err)
	}
	prose := string(data)
	absent := prose[strings.Index(prose, "Some tools are absent."):strings.Index(prose, beginMarker)]
	if !strings.Contains(absent, "a built agent (Quick Build)") {
		t.Errorf("the absent-tools paragraph must say the table is limited to what a built agent (Quick Build) can bind:\n%s", absent)
	}

	table := generatedTable(t)
	for _, name := range []string{"select_voice", "transcribe_audio", "notify_user"} {
		if !strings.Contains(absent, "`"+name+"`") {
			t.Errorf("the absent-tools paragraph does not name the YAML-only tool %q", name)
		}
		if toolinit.AgentBindableBuiltin(name) || strings.Contains(table, "| `"+name+"` |") {
			t.Errorf("%q is named as YAML-only, so a built agent must not bind it and the table must not list it", name)
		}
	}
}
