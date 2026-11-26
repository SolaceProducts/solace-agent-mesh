// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"slices"
	"testing"
)

func TestCanonicalBuiltinGroups(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			"maps the python artifact_management spelling",
			[]string{"builtin_artifact_tools", "artifact_management", "builtin_web_request_tools"},
			[]string{"builtin_artifact_tools", "builtin_web_request_tools"},
		},
		{
			"maps canonical builder group names to platform ids",
			[]string{"web_tools", "research", "image_tools", "code_execution"},
			[]string{"builtin_web_request_tools", "builtin_research_tools", "builtin_image_tools", "builtin_code_execution"},
		},
		{
			"collapses an alias onto its already-present platform id",
			[]string{"builtin_web_request_tools", "web_tools"},
			[]string{"builtin_web_request_tools"},
		},
		{
			"passes through groups with no catalog entry",
			[]string{"general_agent_tools", "scheduling_tools", "observability_analytics"},
			[]string{"general_agent_tools", "scheduling_tools", "observability_analytics"},
		},
		{
			"passes through custom tool package names",
			[]string{"my_custom_package"},
			[]string{"my_custom_package"},
		},
		{
			"already-canonical input is unchanged",
			[]string{"builtin_artifact_tools", "builtin_web_request_tools"},
			[]string{"builtin_artifact_tools", "builtin_web_request_tools"},
		},
		{"empty input", []string{}, []string{}},
		{"nil input", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CanonicalBuiltinGroups(tt.input)
			if !slices.Equal(got, tt.want) {
				t.Errorf("CanonicalBuiltinGroups(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCanonicalBuiltinGroup_Idempotent(t *testing.T) {
	t.Parallel()
	for name := range ValidBuiltinGroups {
		once := CanonicalBuiltinGroup(name)
		if twice := CanonicalBuiltinGroup(once); twice != once {
			t.Errorf("CanonicalBuiltinGroup(%q) = %q, but canonicalizing again gave %q", name, once, twice)
		}
	}
}

func TestBuiltinGroupAliases_TargetsAreValid(t *testing.T) {
	t.Parallel()
	for alias, target := range builtinGroupAliases {
		if !IsValidBuiltinGroup(alias) {
			t.Errorf("alias %q is not a valid builtin group", alias)
		}
		if !IsValidBuiltinGroup(target) {
			t.Errorf("alias %q targets %q, which is not a valid builtin group", alias, target)
		}
		if alias == target {
			t.Errorf("alias %q maps to itself", alias)
		}
	}
}
