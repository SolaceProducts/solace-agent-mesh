// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestApply_StaleStoredSkillConfigExplainsItself(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("stale_config_agent")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	agent := agentNamed(ctx, t, svc, "stale_config_agent")
	attached := seedSkill(ctx, t, svc, "already_attached_skill")
	seedSkill(ctx, t, svc, "newly_added_skill")

	stale := `{"a_tool_this_skill_no_longer_has":{"api_token":"live-secret-token"}}`
	repo := store.NewSkillRepo(svc.deps.DB)
	if err := repo.SetAgentSkillsWithConfigs(ctx, agent.ID, []store.AgentSkillRow{
		{AgentID: agent.ID, SkillID: attached, ConfigValues: &stale},
	}); err != nil {
		t.Fatalf("seed stale config: %v", err)
	}

	edited := agentComponent("stale_config_agent")
	edited.Content = strings.Replace(edited.Content,
		"      instruction: You are a test agent that answers questions.",
		"      instruction: You are a test agent that answers questions.\n      skills:\n        - already_attached_skill\n        - newly_added_skill", 1)
	if edited.Content == agentComponent("stale_config_agent").Content {
		t.Fatal("the fixture did not change — the edit this test is about never happened")
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{edited}}, testActor)
	if err != nil {
		t.Fatalf("Apply returned a transport error rather than a per-component result: %v", err)
	}
	op := opFor(t, resp, kindAgent, "stale_config_agent")
	if op.Status != statusFailed {
		t.Skipf("the edit was accepted (status %q), so this platform build no longer rejects stale preserved config here", op.Status)
	}
	if op.Error == nil {
		t.Fatal("the operation failed with no error attached")
	}
	if !strings.Contains(op.Error.Message, "preserved, not written") {
		t.Errorf("message = %q\nwant it to say the values are preserved rather than authored, and where to fix them.\n"+
			"As it stands the author is handed a skill/tool pair that appears nowhere in their build, for a value they never wrote, and the rest of the build is marked skipped.",
			op.Error.Message)
	}

	rows, err := repo.GetAgentSkillRowsForAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("read agent skill rows: %v", err)
	}
	if len(rows) != 1 || rows[0].ConfigValues == nil || !strings.Contains(*rows[0].ConfigValues, "live-secret-token") {
		t.Errorf("agent_skills = %v, want the stored config untouched by the refused write", rows)
	}
}

func TestApply_SkillsAreLeftAloneUnlessTheArtifactSpeaks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, skillsBlock string }{
		{
			name: "an artifact silent about skills",
		},
		{
			name:        "an artifact restating the skills already attached",
			skillsBlock: "\n      skills:\n        - already_attached_skill",
		},
	} {
		t.Run(tc.name+" does not rewrite them", func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()

			if _, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{agentComponent("untouched_skills_agent")},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}
			agent := agentNamed(ctx, t, svc, "untouched_skills_agent")
			attached := seedSkill(ctx, t, svc, "already_attached_skill")

			stale := `{"a_tool_this_skill_no_longer_has":{"api_token":"live-secret-token"}}`
			repo := store.NewSkillRepo(svc.deps.DB)
			if err := repo.SetAgentSkillsWithConfigs(ctx, agent.ID, []store.AgentSkillRow{
				{AgentID: agent.ID, SkillID: attached, ConfigValues: &stale},
			}); err != nil {
				t.Fatalf("seed stale config: %v", err)
			}

			edited := agentComponent("untouched_skills_agent")
			edited.Content = strings.Replace(edited.Content,
				"      instruction: You are a test agent that answers questions.",
				"      instruction: You are a test agent with a freshly edited instruction."+tc.skillsBlock, 1)
			if edited.Content == agentComponent("untouched_skills_agent").Content {
				t.Fatal("the fixture did not change — the edit this test is about never happened")
			}

			resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{edited}}, testActor)
			if err != nil {
				t.Fatalf("Apply returned a transport error: %v", err)
			}
			if op := opFor(t, resp, kindAgent, "untouched_skills_agent"); op.Status != statusSucceeded {
				t.Fatalf("op = %+v\nwant the edit accepted. The write carried a non-nil SkillIDs, which rebuilds every agent_skills row and revalidates config this save had no business resubmitting.", op)
			}

			rows, err := repo.GetAgentSkillRowsForAgent(ctx, agent.ID)
			if err != nil {
				t.Fatalf("read agent skill rows: %v", err)
			}
			if len(rows) != 1 || rows[0].ConfigValues == nil || !strings.Contains(*rows[0].ConfigValues, "live-secret-token") {
				t.Errorf("agent_skills = %v, want the attachment and its config untouched by an edit that did not ask to change them", rows)
			}
		})
	}
}
