// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestToolsetCatalog_IsBuiltFromTheCanonicalRegistry(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	if len(cat.toolNamesByToolsetID) == 0 {
		t.Fatal("catalog is empty; the canonical registry should have populated it")
	}
	for group := range schema.ValidBuiltinGroups {
		tools := toolinit.LookupBuiltinGroup(group, toolinit.Options{})
		if len(tools) == 0 {
			continue
		}
		id := schema.CanonicalBuiltinGroup(group)
		if _, ok := cat.toolNamesByToolsetID[id]; !ok {
			t.Errorf("group %q (platform id %q) is missing from the catalog", group, id)
		}
	}
}

func TestResolveToolsetBindings_WholeGroupGrant(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	toolsets, excluded, err := resolveToolsetBindings([]string{"artifact_management"}, nil, cat)
	if err != nil {
		t.Fatalf("resolveToolsetBindings: %v", err)
	}
	if !slices.Contains(toolsets, "builtin_artifact_tools") {
		t.Errorf("toolsets = %v, want the canonical builtin_artifact_tools", toolsets)
	}
	if excluded != nil {
		t.Errorf("a whole-group grant should exclude nothing, got %v", excluded)
	}
}

func TestResolveToolsetBindings_SharedOwnerIsNotSplit(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	toolsets, excluded, err := resolveToolsetBindings(nil, []string{"web_request", "web_search_google"}, cat)
	if err != nil {
		t.Fatalf("resolveToolsetBindings: %v", err)
	}
	if len(toolsets) != 1 {
		t.Fatalf("toolsets = %v, want exactly one covering toolset", toolsets)
	}
	if toolsets[0] != "builtin_web_request_tools" {
		t.Errorf("toolsets = %v, want builtin_web_request_tools", toolsets)
	}
	if len(excluded) != 0 {
		t.Errorf("both members were requested, so nothing should be excluded; got %v", excluded)
	}
}

func TestResolveToolsetBindings_NarrowsWithExclusions(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	toolsets, excluded, err := resolveToolsetBindings(nil, []string{"web_request"}, cat)
	if err != nil {
		t.Fatalf("resolveToolsetBindings: %v", err)
	}
	if !slices.Contains(toolsets, "builtin_web_request_tools") {
		t.Fatalf("toolsets = %v, want the owning toolset bound", toolsets)
	}
	drop := excluded["builtin_web_request_tools"]
	if !slices.Contains(drop, "web_search_google") {
		t.Errorf("excluded = %v, want the unrequested sibling web_search_google", drop)
	}
	if slices.Contains(drop, "web_request") {
		t.Error("the requested tool must not be excluded")
	}
}

func TestResolveToolsetBindings_GroupGrantCoversItsTools(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	toolsets, excluded, err := resolveToolsetBindings([]string{"web_tools"}, []string{"web_search_google"}, cat)
	if err != nil {
		t.Fatalf("resolveToolsetBindings: %v", err)
	}
	if len(toolsets) != 1 || toolsets[0] != "builtin_web_request_tools" {
		t.Errorf("toolsets = %v, want only the already-granted group", toolsets)
	}
	if len(excluded) != 0 {
		t.Errorf("a whole-group grant must not acquire exclusions, got %v", excluded)
	}
}

func TestResolveToolsetBindings_UnknownToolIsAnError(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	tests := []struct {
		name  string
		tools []string
		want  []string
	}{
		{"a name nothing provides", []string{"no_such_tool"}, []string{`"no_such_tool"`, "builtin-group"}},
		{"a skill's tool names the skill", []string{"xlsx_read"}, []string{`"xlsx_read"`, "sam-excel", "skills:"}},
		{"a connector's runtime names the connector", []string{"execute_sql_query"}, []string{`"execute_sql_query"`, "SQL connectors"}},
		{"every unbindable tool is named once, sorted", []string{"xlsx_read", "docx_create_document", "execute_sql_query", "web_request"},
			[]string{`"docx_create_document", "execute_sql_query", "xlsx_read" to this agent`, "sam-docx", "sam-excel", "SQL connectors"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := resolveToolsetBindings(nil, tt.tools, cat)
			if err == nil {
				t.Fatal("expected an error for an unbindable tool, got nil")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), `"web_request"`) {
				t.Errorf("error %q names web_request, which binds", err)
			}
		})
	}
}

func TestToolsetCatalog_AgreesWithTheValidatorsView(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	names := map[string]struct{}{"xlsx_read": {}, "execute_sql_query": {}, "docx_create_document": {}, "sub_task": {}, "no_such_tool": {}}
	for name := range schema.ValidBuiltinToolNames {
		names[name] = struct{}{}
	}
	for group := range schema.ValidBuiltinGroups {
		for _, member := range toolinit.LookupBuiltinGroup(group, toolinit.Options{}) {
			names[member] = struct{}{}
		}
	}
	for name := range names {
		saveBinds := len(cat.toolsetIDsByToolName[name]) > 0
		if validatorBinds := toolinit.BuiltinToolHasToolset(name); validatorBinds != saveBinds {
			t.Errorf("%q: validator says bindable=%v, save says %v", name, validatorBinds, saveBinds)
		}
	}
}

func TestResolveToolsetBindings_IsDeterministic(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	tools := []string{"web_request", "load_artifact", "get_current_time"}
	first, firstExcluded, err := resolveToolsetBindings(nil, tools, cat)
	if err != nil {
		t.Fatalf("resolveToolsetBindings: %v", err)
	}
	for range 20 {
		got, gotExcluded, err := resolveToolsetBindings(nil, tools, cat)
		if err != nil {
			t.Fatalf("resolveToolsetBindings: %v", err)
		}
		if !slices.Equal(got, first) {
			t.Fatalf("toolsets varied between runs: %v then %v", first, got)
		}
		if len(gotExcluded) != len(firstExcluded) {
			t.Fatalf("exclusions varied between runs: %v then %v", firstExcluded, gotExcluded)
		}
		for id, drop := range gotExcluded {
			if !slices.Equal(drop, firstExcluded[id]) {
				t.Fatalf("exclusions for %s varied: %v then %v", id, firstExcluded[id], drop)
			}
		}
	}
}

func TestToolOwners_AgreesWithTheSetCover(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	tests := []struct {
		name          string
		groups, tools []string
		want          map[string]string
	}{
		{"a shared owner covers both", nil, []string{"web_request", "web_search_google"},
			map[string]string{"web_request": "builtin_web_request_tools", "web_search_google": "builtin_web_request_tools"}},
		{"a lone tool goes to the cover's pick", nil, []string{"web_search_google"},
			map[string]string{"web_search_google": "builtin_research_tools"}},
		{"a whole group owns every member", []string{"web_tools"}, nil,
			map[string]string{"web_request": "builtin_web_request_tools", "web_search_google": "builtin_web_request_tools"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toolsets, excluded, err := resolveToolsetBindings(tc.groups, tc.tools, cat)
			if err != nil {
				t.Fatalf("resolveToolsetBindings: %v", err)
			}
			if got := toolOwners(toolsets, excluded, cat); !maps.Equal(got, tc.want) {
				t.Errorf("owners = %v, want %v", got, tc.want)
			}
		})
	}
}
