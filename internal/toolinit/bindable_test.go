// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"testing"
)

var wantBindableBuiltins = []string{
	"append_to_artifact",
	"artifact_grep",
	"artifact_search_and_replace_regex",
	"ask_user_question",
	"create_image_from_description",
	"create_sqlite_db",
	"deep_research",
	"delete_artifact",
	"delete_scheduled_task",
	"describe_audio",
	"describe_image",
	"edit_image_with_gemini",
	"generate_image_with_gemini",
	"get_current_time",
	"list_artifacts",
	"list_scheduled_tasks",
	"load_artifact",
	"merge_structured_data",
	"query_data_with_sql",
	"reinterpret_artifact",
	"schedule_task",
	"set_scheduled_task_enabled",
	"transform_data_with_jmespath",
	"update_scheduled_task",
	"web_request",
	"web_search_google",
}

func TestEveryFactoryIsClassified(t *testing.T) {
	var bindable []string
	for _, name := range BuiltinToolNames() {
		if AgentBindableBuiltin(name) {
			bindable = append(bindable, name)
		}
	}
	for _, name := range bindable {
		if !slices.Contains(wantBindableBuiltins, name) {
			t.Errorf("factory %q is bindable but is not in wantBindableBuiltins: add it there, or to nonBindableBuiltins with a reason", name)
		}
	}
	for _, name := range wantBindableBuiltins {
		if !slices.Contains(bindable, name) {
			t.Errorf("%q is in wantBindableBuiltins but AgentBindableBuiltin reports false", name)
		}
	}
}

func TestNonBindableBuiltinsAreRealFactories(t *testing.T) {
	for name, reason := range nonBindableBuiltins {
		if _, ok := builtinToolFactories[name]; !ok {
			t.Errorf("nonBindableBuiltins lists %q, which is not a factory", name)
		}
		if reason == "" {
			t.Errorf("nonBindableBuiltins entry %q has no reason", name)
		}
	}
}

func TestBindableBuiltinsAreRegisteredByAToolset(t *testing.T) {
	for _, name := range wantBindableBuiltins {
		if !BuiltinToolHasToolset(name) {
			t.Errorf("%q is bindable but no built-in toolset registers it, so the build save would refuse it", name)
		}
	}
}

func TestEveryBuilderToolIsNotBindable(t *testing.T) {
	members := LookupBuiltinGroup("builder_tools", Options{})
	if len(members) == 0 {
		t.Fatal("builder_tools resolves to no tools")
	}
	for _, name := range members {
		if AgentBindableBuiltin(name) {
			t.Errorf("builder_tools member %q must not be bindable by a built agent", name)
		}
	}
}

func TestAgentBindableBuiltinNamedCases(t *testing.T) {
	cases := map[string]bool{
		"get_current_time":         true,
		"web_request":              true,
		"schedule_task":            true,
		"sub_task":                 false,
		"validate_build_manifest":  false,
		"platform_list_components": false,
		"query_task_health":        false,
		"pdf_ops":                  false,
		"no_such_tool":             false,
	}
	for name, want := range cases {
		if got := AgentBindableBuiltin(name); got != want {
			t.Errorf("AgentBindableBuiltin(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestSTRBuiltinNames(t *testing.T) {
	names := STRBuiltinNames()
	if !slices.IsSorted(names) || len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Errorf("STRBuiltinNames() = %v, want sorted and without duplicates", names)
	}
	for _, want := range []string{"pdf_ops", "execute_python_code", "convert_pdf_to_markdown", "ffmpeg", "mermaid_diagram_generator", "volume_read"} {
		if !slices.Contains(names, want) {
			t.Errorf("STRBuiltinNames() lacks %q", want)
		}
	}
	for _, name := range names {
		if _, inProcess := builtinToolFactories[name]; inProcess {
			t.Errorf("%q is an in-process factory, so it is not an STR built-in", name)
		}
	}
	for _, unbindable := range []string{"test_harness", "build_apply", "execute_sql_query"} {
		if slices.Contains(names, unbindable) {
			t.Errorf("STRBuiltinNames() lists %q, which no bindable group names", unbindable)
		}
	}
}
