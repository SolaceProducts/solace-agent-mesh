// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
)

func TestApply_DeckSkillFollowsTheDocumentedRules(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	const documentTools = `
      tools:
        - tool_type: builtin-group
          group_name: builtin_document_tools`
	body := func(name, tools, skills string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: name, Content: `
apps:
  - name: ` + name + `
    app_config:
      agent_name: ` + name + `
      instruction: Report on sales.` + tools + skills + `
      agent_card:
        description: "Reports on sales data."
`}
	}
	apply := func(component BuildComponentInput) {
		t.Helper()
		if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{component}}, testActor); err != nil {
			t.Fatalf("Apply %s: %v", component.Name, err)
		}
	}
	refs := func(name string) []string { return agentNamed(ctx, t, svc, name).SkillRefs }
	expect := func(step, name string, want ...string) {
		t.Helper()
		got := slices.Sorted(slices.Values(refs(name)))
		if !slices.Equal(got, want) {
			t.Errorf("%s: skillRefs = %v, want %v", step, got, want)
		}
	}

	apply(body("new_reporter", documentTools, ""))
	expect("new agent with Document Tools", "new_reporter", deckSkill)

	apply(body("new_reporter", documentTools, ""))
	expect("silent re-apply", "new_reporter", deckSkill)

	apply(body("new_reporter", documentTools, "\n      skills: []"))
	expect("authored empty skill list on a Document Tools agent", "new_reporter", deckSkill)

	apply(body("old_reporter", "", ""))
	expect("existing agent without the deck tool", "old_reporter")

	apply(body("old_reporter", documentTools, ""))
	expect("Document Tools added, skills not stated", "old_reporter")

	apply(body("old_reporter", documentTools, "\n      skills:\n        - name: sam-docx"))
	expect("Document Tools added, skills stated", "old_reporter", "sam-docx", deckSkill)
}

func TestDeckSkillIsARealBuiltin(t *testing.T) {
	t.Parallel()
	if !slices.Contains(builtinskills.Names(), deckSkill) {
		t.Errorf("deckSkill %q is not a built-in skill (have %v); every Document Tools agent would get a reference that resolves to nothing", deckSkill, builtinskills.Names())
	}
}
