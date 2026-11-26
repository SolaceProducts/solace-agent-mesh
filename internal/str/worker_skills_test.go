// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"log/slog"
	"os"
	"reflect"
	"testing"
)

func newTestWorkerWithManifestEntries(toolNames ...string) *Worker {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	w := &Worker{logger: logger}
	w.manifest = NewManifestFromSources(nil, logger)
	for _, name := range toolNames {
		w.manifest.Register(name, &ManifestEntry{ToolName: name})
	}
	return w
}

func TestExpandSkillToolsets_UploadedToolsetExpands(t *testing.T) {
	w := newTestWorkerWithManifestEntries(
		"bamboohr_profile_provider__search_users",
		"bamboohr_profile_provider__find_users",
		"bamboohr_profile_provider__get_user_profile",
		"unrelated_pkg__noise",
	)
	skill := &SkillEntry{
		Name:     "user",
		Toolsets: []string{"bamboohr_profile_provider"},
	}

	got := w.expandSkillToolsets(skill)
	want := []map[string]any{
		{"tool_type": "builtin", "tool_name": "bamboohr_profile_provider__find_users"},
		{"tool_type": "builtin", "tool_name": "bamboohr_profile_provider__get_user_profile"},
		{"tool_type": "builtin", "tool_name": "bamboohr_profile_provider__search_users"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded tools mismatch\n got:  %#v\n want: %#v", got, want)
	}
}

func TestExpandSkillToolsets_UnknownToolsetFallsBackToBuiltinGroup(t *testing.T) {
	w := newTestWorkerWithManifestEntries()
	skill := &SkillEntry{
		Name:     "documents",
		Toolsets: []string{"builtin_artifact_tools"},
	}

	got := w.expandSkillToolsets(skill)
	want := []map[string]any{
		{"tool_type": "builtin-group", "group_name": "builtin_artifact_tools"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded tools mismatch\n got:  %#v\n want: %#v", got, want)
	}
}

func TestExpandSkillToolsets_PreservesInlineToolsAndAppendsToolsets(t *testing.T) {
	w := newTestWorkerWithManifestEntries(
		"bamboohr_profile_provider__search_users",
	)
	skill := &SkillEntry{
		Name: "user",
		Tools: []map[string]any{
			{"tool_type": "builtin", "tool_name": "get_current_time"},
		},
		Toolsets: []string{"bamboohr_profile_provider"},
	}

	got := w.expandSkillToolsets(skill)
	want := []map[string]any{
		{"tool_type": "builtin", "tool_name": "get_current_time"},
		{"tool_type": "builtin", "tool_name": "bamboohr_profile_provider__search_users"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded tools mismatch\n got:  %#v\n want: %#v", got, want)
	}
}

func TestExpandSkillToolsets_EmptyToolsetsReturnsToolsVerbatim(t *testing.T) {
	w := newTestWorkerWithManifestEntries("bamboohr_profile_provider__search_users")
	inline := []map[string]any{
		{"tool_type": "builtin", "tool_name": "get_current_time"},
	}
	skill := &SkillEntry{Name: "noop", Tools: inline}

	got := w.expandSkillToolsets(skill)
	if !reflect.DeepEqual(got, inline) {
		t.Fatalf("expected verbatim Tools, got %#v", got)
	}
}

func TestExpandSkillToolsets_SkipsEmptyToolsetNames(t *testing.T) {
	w := newTestWorkerWithManifestEntries("real_toolset__do_thing")
	skill := &SkillEntry{
		Name:     "s",
		Toolsets: []string{"", "real_toolset", ""},
	}

	got := w.expandSkillToolsets(skill)
	want := []map[string]any{
		{"tool_type": "builtin", "tool_name": "real_toolset__do_thing"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty-string filter\n got:  %#v\n want: %#v", got, want)
	}
}

func TestExpandSkillToolsets_SkipsBuiltinAndUnservableManifestEntries(t *testing.T) {
	w := newTestWorkerWithManifestEntries()
	w.manifest.Register("toolset_x__servable", &ManifestEntry{ToolName: "toolset_x__servable"})
	w.manifest.Register("toolset_x__broken", &ManifestEntry{ToolName: "toolset_x__broken", Unservable: true})
	w.manifest.Register("toolset_x__builtin", &ManifestEntry{ToolName: "toolset_x__builtin", IsBuiltin: true})

	skill := &SkillEntry{Name: "s", Toolsets: []string{"toolset_x"}}
	got := w.expandSkillToolsets(skill)
	want := []map[string]any{
		{"tool_type": "builtin", "tool_name": "toolset_x__servable"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filter mismatch\n got:  %#v\n want: %#v", got, want)
	}
}
