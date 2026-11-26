// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func seedRichAgent(ctx context.Context, t *testing.T, svc *Service, name string) entplatform.AgentResponse {
	t.Helper()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent(name)},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	stored := agentNamed(ctx, t, svc, name)

	skillID := seedSkill(ctx, t, svc, "flow_helper")
	connectorID := seedConnector(ctx, t, svc, "flow_db")

	skillIDs := []string{skillID}
	if _, err := svc.deps.Agents.Update(ctx, stored.ID, entplatform.UpdateAgentRequest{
		Name:          stored.Name,
		Description:   stored.Description,
		SystemPrompt:  stored.SystemPrompt,
		Toolsets:      stored.Toolsets,
		SkillIDs:      &skillIDs,
		Connectors:    []string{connectorID},
		ModelProvider: []string{"planning"},
		InputModes:    []string{"text", "file"},
		OutputModes:   []string{"text", "image"},
		Skills: []entplatform.SkillRequest{{
			Name:        "summarize",
			Description: "Summarizes a document for the caller.",
			Tags:        []string{"docs"},
			Examples:    []string{"summarize this"},
		}},
		AdditionalConfigurations: json.RawMessage(`{"supports_streaming":true}`),
	}, "agent-page"); err != nil {
		t.Fatalf("seed rich agent: %v", err)
	}
	return agentNamed(ctx, t, svc, name)
}

func agentNamed(ctx context.Context, t *testing.T, svc *Service, name string) entplatform.AgentResponse {
	t.Helper()
	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	i := slices.IndexFunc(agents, func(a entplatform.AgentResponse) bool { return a.Name == name })
	if i < 0 {
		t.Fatalf("agent %q not found; have %v", name, agentNames(agents))
	}
	return agents[i]
}

func agentNames(agents []entplatform.AgentResponse) []string {
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, a.Name)
	}
	return out
}

func seedSkill(ctx context.Context, t *testing.T, svc *Service, name string) string {
	t.Helper()
	created, err := svc.deps.Skills.Create(ctx, entplatform.CreateSkillRequest{
		Name:        name,
		Description: "A skill attached outside the Builder.",
	}, "agent-page")
	if err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	return created.ID
}

func seedConnector(ctx context.Context, t *testing.T, svc *Service, name string) string {
	t.Helper()
	created, err := svc.deps.Connectors.Create(ctx, entplatform.CreateConnectorRequest{
		Name:        name,
		Description: "A connector attached outside the Builder.",
		Type:        "sql",
		Subtype:     "postgres",
		Values: map[string]any{
			"hostname": "db", "port": 5432, "database": "app",
			"username": "svc", "password": "real-password",
		},
	}, "agent-page")
	if err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	return created.ID
}

func editedAgent(name string) BuildComponentInput {
	c := agentComponent(name)
	c.Content = strings.Replace(c.Content,
		"instruction: You are a test agent that answers questions.",
		"instruction: You are a test agent with a freshly edited instruction.", 1)
	return c
}

func TestApply_AgentEditPreservesEverythingTheArtifactIsSilentAbout(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	before := seedRichAgent(ctx, t, svc, "rich_agent")

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{editedAgent("rich_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "rich_agent"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v", op)
	}

	after := agentNamed(ctx, t, svc, "rich_agent")

	if !strings.Contains(after.SystemPrompt, "freshly edited") {
		t.Errorf("the edit itself did not land: systemPrompt = %q", after.SystemPrompt)
	}

	if len(before.SkillIDs) == 0 {
		t.Fatal("the seed attached no skills, so the assertions below prove nothing")
	}

	for _, tc := range []struct {
		field     string
		got, want any
	}{
		{"skillIds", after.SkillIDs, before.SkillIDs},
		{"connectors", after.Connectors, before.Connectors},
		{"modelProvider", after.ModelProvider, before.ModelProvider},
		{"inputModes", sorted(after.InputModes), sorted(before.InputModes)},
		{"outputModes", sorted(after.OutputModes), sorted(before.OutputModes)},
	} {
		if !slices.Equal(tc.got.([]string), tc.want.([]string)) {
			t.Errorf("%s = %v, want %v — destroyed by an edit that never mentioned it", tc.field, tc.got, tc.want)
		}
	}

	if !strings.Contains(string(after.AdditionalConfigurations), "supports_streaming") {
		t.Errorf("additionalConfigurations = %s, want the seeded runtime config intact", after.AdditionalConfigurations)
	}

	if len(after.Skills) != 1 {
		t.Fatalf("skills = %v, want the one seeded ability", after.Skills)
	}
	if len(after.Skills[0].Tags) == 0 || len(after.Skills[0].Examples) == 0 {
		t.Errorf("skills[0] = %+v, want tags and examples intact — peers route on this metadata", after.Skills[0])
	}
}

func TestApply_AgentEditPreservesSkillConfigValues(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("configured_skills_agent")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	agent := agentNamed(ctx, t, svc, "configured_skills_agent")
	skillID := seedSkill(ctx, t, svc, "credential_bearing_skill")

	const storedConfig = `{"cfg_tool":{"api_token":"live-secret-token"}}`
	repo := store.NewSkillRepo(svc.deps.DB)
	cfg := storedConfig
	if err := repo.SetAgentSkillsWithConfigs(ctx, agent.ID, []store.AgentSkillRow{
		{AgentID: agent.ID, SkillID: skillID, ConfigValues: &cfg},
	}); err != nil {
		t.Fatalf("seed skill config: %v", err)
	}

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{editedAgent("configured_skills_agent")},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	rows, err := repo.GetAgentSkillRowsForAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("read agent skill rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("agent_skills = %v, want the seeded attachment intact", rows)
	}
	if rows[0].ConfigValues == nil || !strings.Contains(*rows[0].ConfigValues, "live-secret-token") {
		t.Errorf("config_values = %v, want the seeded credential intact — a prompt edit destroyed it",
			rows[0].ConfigValues)
	}
}

func TestApply_AgentCardSkillsKeepTheirRoutingMetadata(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	rich := BuildComponentInput{
		Kind: kindAgent,
		Name: "advertising_agent",
		Content: `
apps:
  - name: advertising_agent
    app_config:
      agent_name: advertising_agent
      instruction: You are a test agent that answers questions.
      agent_card:
        description: "An agent that advertises a fully described skill."
        skills:
          - id: doc_search
            name: search
            description: Finds documents matching a query.
            tags: [docs, search]
            examples: ["find the onboarding guide"]
            inputModes: [text]
            outputModes: [text]
`,
	}

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{rich}}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	skills := agentNamed(ctx, t, svc, "advertising_agent").Skills
	if len(skills) != 1 {
		t.Fatalf("skills = %v, want the one advertised ability", skills)
	}
	got := skills[0]
	for _, tc := range []struct {
		field string
		empty bool
	}{
		{"id", got.ID == ""},
		{"tags", len(got.Tags) == 0},
		{"examples", len(got.Examples) == 0},
		{"inputModes", len(got.InputModes) == 0},
		{"outputModes", len(got.OutputModes) == 0},
	} {
		if tc.empty {
			t.Errorf("skills[0].%s was dropped by the converter; peers route on it. Got %+v", tc.field, got)
		}
	}
}

func agentWithSkillRef(name, skillName string) BuildComponentInput {
	return BuildComponentInput{
		Kind: kindAgent,
		Name: name,
		Content: `
apps:
  - name: ` + name + `
    app_config:
      agent_name: ` + name + `
      instruction: You are a test agent that answers questions.
      skills:
        - name: ` + skillName + `
      agent_card:
        description: "An agent that attaches a custom platform skill."
`,
	}
}

func TestApply_AgentWithCustomSkillConverges(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedSkill(ctx, t, svc, "cfg_skill")

	comp := agentWithSkillRef("skilled_agent", "cfg_skill")
	for pass := 1; pass <= 2; pass++ {
		if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "skilled_agent"); op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged — nothing was edited", op.Action, op.ChangedFields)
	}
	if got := agentNamed(ctx, t, svc, "skilled_agent"); len(got.SkillIDs) != 1 {
		t.Errorf("skillIds = %v, want the skill still attached", got.SkillIDs)
	}
}

func TestApply_RemovingASkillThatCarriedConfigSucceeds(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	skillID := seedSkill(ctx, t, svc, "cfg_skill")

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{agentWithSkillRef("skilled_agent", "cfg_skill")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	agent := agentNamed(ctx, t, svc, "skilled_agent")

	cfg := `{"cfg_tool":{"api_token":"live-secret-token"}}`
	if err := store.NewSkillRepo(svc.deps.DB).SetAgentSkillsWithConfigs(ctx, agent.ID,
		[]store.AgentSkillRow{{AgentID: agent.ID, SkillID: skillID, ConfigValues: &cfg}}); err != nil {
		t.Fatalf("seed skill config: %v", err)
	}

	removed := BuildComponentInput{
		Kind: kindAgent,
		Name: "skilled_agent",
		Content: `
apps:
  - name: skilled_agent
    app_config:
      agent_name: skilled_agent
      instruction: You are a test agent that answers questions.
      skills: []
      agent_card:
        description: "An agent that attaches a custom platform skill."
`,
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{removed}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "skilled_agent"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want succeeded — removing a skill is a legitimate edit", op)
	}
	if got := agentNamed(ctx, t, svc, "skilled_agent"); len(got.SkillIDs) != 0 {
		t.Errorf("skillIds = %v, want the skill detached", got.SkillIDs)
	}
}

func TestApply_RemovingAToolsetThatCarriedConfigSucceeds(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	withTools := agentComponent("toolset_agent")
	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{withTools},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	stored := agentNamed(ctx, t, svc, "toolset_agent")
	if len(stored.Toolsets) == 0 {
		t.Fatal("seed bound no toolset")
	}

	if _, err := svc.deps.Agents.Update(ctx, stored.ID, entplatform.UpdateAgentRequest{
		Name: stored.Name, Description: stored.Description, SystemPrompt: stored.SystemPrompt,
		Toolsets: stored.Toolsets,
		ToolsetConfigs: []entplatform.ToolsetConfigBinding{{
			ToolsetName:  stored.Toolsets[0],
			ConfigValues: map[string]any{"list_artifacts": map[string]any{"max_results": 5}},
		}},
	}, "agent-page"); err != nil {
		t.Fatalf("seed toolsetConfigs: %v", err)
	}

	noTools := BuildComponentInput{
		Kind: kindAgent,
		Name: "toolset_agent",
		Content: `
apps:
  - name: toolset_agent
    app_config:
      agent_name: toolset_agent
      instruction: You are a test agent that answers questions.
      tools: []
      agent_card:
        description: "An agent created by the plan integration test."
`,
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{noTools}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "toolset_agent"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want succeeded — removing a toolset is a legitimate edit", op)
	}
	if got := agentNamed(ctx, t, svc, "toolset_agent"); len(got.Toolsets) != 0 {
		t.Errorf("toolsets = %v, want them removed", got.Toolsets)
	}
}

func TestApply_AgentWithStoredToolsetConfigsConverges(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	comp := agentComponent("configured_agent")
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	stored := agentNamed(ctx, t, svc, "configured_agent")

	if _, err := svc.deps.Agents.Update(ctx, stored.ID, entplatform.UpdateAgentRequest{
		Name:         stored.Name,
		Description:  stored.Description,
		SystemPrompt: stored.SystemPrompt,
		Toolsets:     stored.Toolsets,
		ToolsetConfigs: []entplatform.ToolsetConfigBinding{{
			ToolsetName:  stored.Toolsets[0],
			ConfigValues: map[string]any{"list_artifacts": map[string]any{"max_results": 5}},
		}},
	}, "agent-page"); err != nil {
		t.Fatalf("seed toolsetConfigs: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	op := opFor(t, resp, kindAgent, "configured_agent")
	if op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged — an unauthored stored field cannot make the plan churn",
			op.Action, op.ChangedFields)
	}
	if got := agentNamed(ctx, t, svc, "configured_agent"); len(got.ToolsetConfigs) != 1 {
		t.Errorf("toolsetConfigs = %v, want the stored binding intact", got.ToolsetConfigs)
	}
}

func TestApply_AgentClearsWhatTheArtifactActuallyAuthors(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("shrinking_agent")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	if got := agentNamed(ctx, t, svc, "shrinking_agent"); len(got.Toolsets) == 0 {
		t.Fatal("seed did not bind a toolset")
	}

	noTools := BuildComponentInput{
		Kind: kindAgent,
		Name: "shrinking_agent",
		Content: `
apps:
  - name: shrinking_agent
    app_config:
      agent_name: shrinking_agent
      instruction: You are a test agent that answers questions.
      tools: []
      agent_card:
        description: "An agent created by the plan integration test."
`,
	}
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{noTools}}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := agentNamed(ctx, t, svc, "shrinking_agent"); len(got.Toolsets) != 0 {
		t.Errorf("toolsets = %v, want empty — an authored empty tools block must clear them", got.Toolsets)
	}
}

func sorted(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func TestApply_AuthoredEmptySkillSetDetachesOnItsOwn(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedSkill(ctx, t, svc, "lonely_skill")

	apply := func(component BuildComponentInput) BuildOperationResult {
		t.Helper()
		resp, err := svc.Apply(ctx, ApplyBuildRequest{
			Components: []BuildComponentInput{component},
		}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return opFor(t, resp, kindAgent, "lonely_agent")
	}
	attached := func() []string { return agentNamed(ctx, t, svc, "lonely_agent").SkillIDs }

	body := func(skills string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "lonely_agent", Content: `
apps:
  - name: lonely_agent
    app_config:
      agent_name: lonely_agent
      instruction: You are a test agent that answers questions.` + skills + `
      agent_card:
        description: "An agent that attaches a custom platform skill."
`}
	}
	withSkill := body("\n      skills:\n        - name: lonely_skill")
	silent, emptied := body(""), body("\n      skills: []")

	apply(withSkill)
	if got := attached(); len(got) != 1 {
		t.Fatalf("seed skillIds = %v, want one attached", got)
	}

	if op := apply(silent); op.Action != actionUnchanged {
		t.Errorf("silent artifact: action = %q, want unchanged", op.Action)
	}
	if got := attached(); len(got) != 1 {
		t.Errorf("after silence skillIds = %v, want the skill preserved", got)
	}

	op := apply(emptied)
	if op.Action != actionUpdate {
		t.Errorf("authored empty set: action = %q, want update", op.Action)
	}
	if !slices.Contains(op.ChangedFields, "skillIds") {
		t.Errorf("changedFields = %v, want skillIds named — the diff cannot see it, so we must", op.ChangedFields)
	}
	if got := attached(); len(got) != 0 {
		t.Errorf("after an authored empty set skillIds = %v, want detached", got)
	}

	if op := apply(emptied); op.Action != actionUnchanged {
		t.Errorf("re-applying the empty set: action = %q, want unchanged — the promotion must not be sticky", op.Action)
	}
}
