// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skill"
)

func TestFrameworkSkillToolsOnlyNameConfiguredSkills(t *testing.T) {
	mgr := skill.NewManager()
	mgr.AddAvailable(&skill.SkillInfo{Name: "own-docs"})
	guard := configuredSkillsOnly(mgr)

	cases := []struct {
		name    string
		args    map[string]any
		refused bool
	}{
		{"a configured skill", map[string]any{"skill_name": "own-docs", "path": "references/guide.md"}, false},
		{"another agent's skill", map[string]any{"skill_name": "other-docs", "path": "references/secret.md"}, true},
		{"another agent's skill via the skill alias", map[string]any{"skill": "other-docs", "asset": "assets/report.html.samt"}, true},
		{"a configured skill_name masking another agent's skill alias", map[string]any{"skill_name": "own-docs", "skill": "other-docs"}, true},
		{"a skill name that is not a string", map[string]any{"skill_name": []any{"other-docs"}}, true},
		{"a session artifact, no skill named", map[string]any{"source_artifact": "report.samt"}, false},
		{"an empty skill name, left to the STR to reject", map[string]any{"skill_name": ""}, false},
		{"a case variant of a configured skill", map[string]any{"skill_name": "Own-Docs"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guard(tc.args)
			if got := err != nil; got != tc.refused {
				t.Errorf("refused = %v (%v), want %v", got, err, tc.refused)
			}
		})
	}
}
