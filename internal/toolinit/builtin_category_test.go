// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"maps"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func resolvableGroupNames() []string {
	return append(slices.Sorted(maps.Keys(schema.ValidBuiltinGroups)), switchOnlyGroups...)
}

func canonicalGroupIDs() []string {
	var out []string
	for _, name := range resolvableGroupNames() {
		id := schema.CanonicalBuiltinGroup(name)
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func TestBuiltinToolCategoriesMatchTheGroupRegistry(t *testing.T) {
	registry := append(canonicalGroupIDs(), taxonomy.BuiltinCategoryUngrouped)
	slices.Sort(registry)
	catalog := slices.Sorted(slices.Values(taxonomy.BuiltinToolCategories))
	if !slices.Equal(catalog, registry) {
		t.Errorf("the catalog approves\n  %v\nthe group registry resolves\n  %v\nkeep them in step, or an install that adopts a shipped toolset reports it as untried forever",
			catalog, registry)
	}
}

func TestEveryBuiltinGroupCategoryResolvesToTools(t *testing.T) {
	for _, category := range builtinGroupCategories {
		if len(LookupBuiltinGroup(category, Options{})) == 0 {
			t.Errorf("category %q resolves to no tools, so nothing in it can ever be classified under it", category)
		}
	}
}

func TestTheUngroupedSentinelIsReachedByRealTools(t *testing.T) {
	if slices.Contains(builtinGroupCategories, taxonomy.BuiltinCategoryUngrouped) {
		t.Fatalf("%q is being treated as a group id", taxonomy.BuiltinCategoryUngrouped)
	}
	if LookupBuiltinGroup(taxonomy.BuiltinCategoryUngrouped, Options{}) != nil {
		t.Errorf("%q resolves to tools, so it is a group name and not a sentinel", taxonomy.BuiltinCategoryUngrouped)
	}
	reported := 0
	for name := range builtinToolFactories {
		entry := map[string]any{"tool_type": ToolTypeBuiltin, "tool_name": name}
		if ProvenanceOf(entry, name).BuiltinCategory == taxonomy.BuiltinCategoryUngrouped {
			reported++
		}
	}
	if reported == 0 {
		t.Errorf("no shipped tool reports %q, so it is dead vocabulary counted as untried by every install",
			taxonomy.BuiltinCategoryUngrouped)
	}
}

func TestTheUngroupedSentinelIsNotAcceptedAsAGroupName(t *testing.T) {
	entry := map[string]any{"tool_type": ToolTypeBuiltinGroup, "group_name": taxonomy.BuiltinCategoryUngrouped}
	if got := ProvenanceOf(entry, "list_artifacts").BuiltinCategory; got != "" {
		t.Errorf("group_name %q reported %q, want none", taxonomy.BuiltinCategoryUngrouped, got)
	}
}

func undecidedToolNames(t *testing.T) []string {
	t.Helper()
	containing := map[string][]string{}
	for _, category := range builtinGroupCategories {
		for _, name := range LookupBuiltinGroup(category, Options{}) {
			containing[name] = append(containing[name], category)
		}
	}
	var out []string
	for name, categories := range containing {
		if len(categories) > 1 {
			out = append(out, name)
		}
	}
	for name := range builtinToolFactories {
		if len(containing[name]) == 0 {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func TestBuiltinCategoryOverridesCoverExactlyTheUndecidedTools(t *testing.T) {
	want := undecidedToolNames(t)
	got := slices.Sorted(maps.Keys(builtinCategoryOverrides))
	if !slices.Equal(got, want) {
		t.Errorf("the overrides decide\n  %v\nthe registry leaves undecided\n  %v\nclassify the difference explicitly", got, want)
	}
}

func TestEveryBuiltinCategoryOverrideNamesAnApprovedCategory(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(builtinCategoryOverrides)) {
		category := builtinCategoryOverrides[name]
		if category == "" {
			t.Errorf("%q is mapped to nothing, so it reports an unrefined builtin no query can distinguish from a tool that never ran — name its group, or %q",
				name, taxonomy.BuiltinCategoryUngrouped)
			continue
		}
		if !slices.Contains(taxonomy.BuiltinToolCategories, category) {
			t.Errorf("%q is mapped to %q, which the catalog does not approve", name, category)
		}
	}
}

func TestEveryShippedBuiltinReportsACategory(t *testing.T) {
	names := map[string]bool{}
	for _, category := range builtinGroupCategories {
		for _, name := range LookupBuiltinGroup(category, Options{}) {
			names[name] = true
		}
	}
	for name := range builtinToolFactories {
		names[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		entry := map[string]any{"tool_type": ToolTypeBuiltin, "tool_name": name}
		if ProvenanceOf(entry, name).BuiltinCategory == "" {
			t.Errorf("%q reports no builtin category, so every install that uses it reports an unrefined builtin", name)
		}
	}
}

func TestNoToolNameEverReachesTheProvenance(t *testing.T) {
	names := map[string]bool{}
	for _, category := range builtinGroupCategories {
		for _, name := range LookupBuiltinGroup(category, Options{}) {
			names[name] = true
		}
	}
	for name := range builtinToolFactories {
		names[name] = true
	}

	for _, name := range slices.Sorted(maps.Keys(names)) {
		got := ProvenanceOf(map[string]any{"tool_type": ToolTypeBuiltin, "tool_name": name}, name).BuiltinCategory
		if got == "" {
			continue
		}
		if got == name {
			t.Errorf("%q reports its own name as its category", name)
		}
		if !slices.Contains(taxonomy.BuiltinToolCategories, got) {
			t.Errorf("%q reports %q, which is not an approved category", name, got)
		}
	}
}

func TestAnUnapprovedGroupNameReportsNoCategory(t *testing.T) {
	for _, group := range []string{
		"acme_internal_tools",
		"artifact_managemnt",
		"",
		"builtin_artifact_tools_v2",
	} {
		entry := map[string]any{"tool_type": ToolTypeBuiltinGroup, "group_name": group}
		if got := ProvenanceOf(entry, "list_artifacts").BuiltinCategory; got != "" {
			t.Errorf("group_name %q reported category %q, want none", group, got)
		}
	}
}

func TestGroupAliasesReportOneCategory(t *testing.T) {
	for alias, want := range map[string]string{
		"artifact_management": "builtin_artifact_tools",
		"web_tools":           "builtin_web_request_tools",
		"research":            "builtin_research_tools",
		"image_tools":         "builtin_image_tools",
		"code_execution":      "builtin_code_execution",
	} {
		entry := map[string]any{"tool_type": ToolTypeBuiltinGroup, "group_name": alias}
		if got := ProvenanceOf(entry, "any_member").BuiltinCategory; got != want {
			t.Errorf("group_name %q reported %q, want %q", alias, got, want)
		}
	}
}

func TestAGroupEntryReportsTheGroupItGranted(t *testing.T) {
	entry := map[string]any{"tool_type": ToolTypeBuiltinGroup, "group_name": "general_agent_tools"}
	if got := ProvenanceOf(entry, "get_current_time").BuiltinCategory; got != "general_agent_tools" {
		t.Errorf("BuiltinCategory = %q, want the granted group general_agent_tools", got)
	}
	named := map[string]any{"tool_type": ToolTypeBuiltin, "tool_name": "get_current_time"}
	if got := ProvenanceOf(named, "get_current_time").BuiltinCategory; got != "builtin_time_tools" {
		t.Errorf("BuiltinCategory = %q, want the tool's own group builtin_time_tools", got)
	}
}

func TestOnlyABuiltinReportsABuiltinCategory(t *testing.T) {
	cases := []map[string]any{
		{"tool_type": ToolTypeBuiltin, "tool_name": "list_artifacts", "source_connector_category": "sql"},
		{"tool_type": "openapi"},
		{"tool_type": "mcp"},
		{},
	}
	for _, entry := range cases {
		if got := ProvenanceOf(entry, "list_artifacts").BuiltinCategory; got != "" {
			t.Errorf("%v reported builtin category %q, want none", entry, got)
		}
	}
}

func TestUnambiguousMembersAreIndexedFromTheRegistry(t *testing.T) {
	for name, want := range map[string]string{
		"list_artifacts":            "builtin_artifact_tools",
		"web_request":               "builtin_web_request_tools",
		"deep_research":             "builtin_research_tools",
		"notify_user":               "notification_tools",
		"schedule_task":             "scheduling_tools",
		"query_token_usage":         "observability_analytics",
		"pptx_create_presentation":  "builtin_document_tools",
		"mermaid_diagram_generator": "builtin_diagram_tools",
		"execute_python_code":       "builtin_code_execution",
		"volume_grep":               "builtin_volume_tools",
		"ffprobe":                   "builtin_media_tools",
	} {
		entry := map[string]any{"tool_type": ToolTypeBuiltin, "tool_name": name}
		if got := ProvenanceOf(entry, name).BuiltinCategory; got != want {
			t.Errorf("%q reported %q, want %q", name, got, want)
		}
		if _, hardcoded := builtinCategoryOverrides[name]; hardcoded {
			t.Errorf("%q is in the override table, so this case proves nothing about the derivation", name)
		}
	}
}
