// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type skillConfigPrewriteFixture struct {
	svc       *AgentService
	agentRepo *store.AgentRepo
	skillRepo *store.SkillRepo
	skill     *store.Skill
}

func newSkillConfigPrewriteFixture(t *testing.T) *skillConfigPrewriteFixture {
	t.Helper()
	db := testOpenDB(t)
	skillRepo := store.NewSkillRepo(db)
	agentRepo := store.NewAgentRepo(db)

	bt := skillSchemaSecretRequired
	sk := &store.Skill{
		Name: "prewrite_skill", Origin: "platform", DiscoveryStatus: "ready",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := skillRepo.Create(t.Context(), sk); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	svc := NewAgentService(agentRepo, nil, nil, nil, nil, nil, slog.New(slog.DiscardHandler))
	svc.WithSkillRepo(skillRepo)
	return &skillConfigPrewriteFixture{svc: svc, agentRepo: agentRepo, skillRepo: skillRepo, skill: sk}
}

func (f *skillConfigPrewriteFixture) createRequest(name string, toolValues map[string]map[string]any) CreateAgentRequest {
	return CreateAgentRequest{
		Name:         name,
		Description:  "A valid description that is long enough",
		SystemPrompt: "You are helpful.",
		SkillIDs:     []string{f.skill.ID},
		SkillConfigs: []SkillConfigBinding{{SkillID: f.skill.ID, ConfigValues: toolValues}},
	}
}

var (
	unknownBundledToolValues = map[string]map[string]any{"no_such_tool": {"api_key": "x"}}
	validBundledToolValues   = map[string]map[string]any{"render": {"api_key": "sk-valid"}}
)

const unknownBundledToolDetailKey = "prewrite_skill/no_such_tool"

func requireUnknownBundledToolRejection(t *testing.T, err error) {
	t.Helper()
	if details := requireValidationError(t, err).Details[unknownBundledToolDetailKey]; len(details) == 0 {
		t.Fatalf("validation error = %v, want a detail on %s", err, unknownBundledToolDetailKey)
	}
}

func TestAgentService_Create_InvalidSkillConfigWritesNoAgent(t *testing.T) {
	t.Parallel()
	f := newSkillConfigPrewriteFixture(t)
	ctx := t.Context()
	const name = "prewrite-create-agent"

	_, err := f.svc.Create(ctx, f.createRequest(name, unknownBundledToolValues), "u")
	requireUnknownBundledToolRejection(t, err)

	stray, _, _, _, _, err := f.agentRepo.GetByName(ctx, name)
	if err != nil {
		t.Fatalf("look up agent by name: %v", err)
	}
	if stray != nil {
		t.Fatalf("rejected create left agent row %s behind", stray.ID)
	}

	created, err := f.svc.Create(ctx, f.createRequest(name, validBundledToolValues), "u")
	if err != nil {
		t.Fatalf("corrected retry under the same name: %v", err)
	}
	if len(created.SkillIDs) != 1 || created.SkillIDs[0] != f.skill.ID {
		t.Errorf("created agent skillIds = %v, want [%s]", created.SkillIDs, f.skill.ID)
	}
}

func TestAgentService_Update_InvalidSkillConfigLeavesAgentUnchanged(t *testing.T) {
	t.Parallel()
	f := newSkillConfigPrewriteFixture(t)
	ctx := t.Context()

	created, err := f.svc.Create(ctx, f.createRequest("prewrite-update-agent", validBundledToolValues), "u")
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	rowsBefore, err := f.skillRepo.GetAgentSkillRowsForAgent(ctx, created.ID)
	if err != nil {
		t.Fatalf("load agent skill rows: %v", err)
	}

	skillIDs := []string{f.skill.ID}
	_, err = f.svc.Update(ctx, created.ID, UpdateAgentRequest{
		Name:         created.Name,
		Description:  "A changed description that must not be committed",
		SystemPrompt: "You are helpful.",
		SkillIDs:     &skillIDs,
		SkillConfigs: []SkillConfigBinding{{SkillID: f.skill.ID, ConfigValues: unknownBundledToolValues}},
	}, "u")
	requireUnknownBundledToolRejection(t, err)

	stored, _, _, _, _, err := f.agentRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload agent: %v", err)
	}
	if stored.Description == nil || *stored.Description != "A valid description that is long enough" {
		t.Errorf("rejected update committed the request's description")
	}

	rowsAfter, err := f.skillRepo.GetAgentSkillRowsForAgent(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload agent skill rows: %v", err)
	}
	if !reflect.DeepEqual(rowsAfter, rowsBefore) {
		t.Errorf("rejected update changed agent_skills rows: before %+v, after %+v", rowsBefore, rowsAfter)
	}
}

func TestAgentService_Create_RedactedSecretWithNoPriorIsDropped(t *testing.T) {
	t.Parallel()
	f := newSkillConfigPrewriteFixture(t)
	ctx := t.Context()

	created, err := f.svc.Create(ctx, f.createRequest("prewrite-sentinel-agent", map[string]map[string]any{
		"render": {"api_key": redactedSentinel, "model_id": "gpt-5"},
	}), "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	rows, err := f.skillRepo.GetAgentSkillRowsForAgent(ctx, created.ID)
	if err != nil {
		t.Fatalf("load agent skill rows: %v", err)
	}
	if len(rows) != 1 || rows[0].ConfigValues == nil {
		t.Fatalf("agent skill rows = %+v, want one row with config values", rows)
	}
	if got, want := *rows[0].ConfigValues, `{"render":{"model_id":"gpt-5"}}`; got != want {
		t.Errorf("stored config values = %s, want %s", got, want)
	}
}

func TestAgentService_Update_OmittedSkillIDsKeepsBindings(t *testing.T) {
	t.Parallel()
	f := newSkillConfigPrewriteFixture(t)
	ctx := t.Context()

	created, err := f.svc.Create(ctx, f.createRequest("prewrite-omit-skills-agent", validBundledToolValues), "u")
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	rowsBefore, err := f.skillRepo.GetAgentSkillRowsForAgent(ctx, created.ID)
	if err != nil {
		t.Fatalf("load agent skill rows: %v", err)
	}

	_, err = f.svc.Update(ctx, created.ID, UpdateAgentRequest{
		Name:         created.Name,
		Description:  "A changed description that is long enough",
		SystemPrompt: "You are helpful.",
	}, "u")
	if err != nil {
		t.Fatalf("update without skillIds: %v", err)
	}

	rowsAfter, err := f.skillRepo.GetAgentSkillRowsForAgent(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload agent skill rows: %v", err)
	}
	if len(rowsAfter) != 1 || !reflect.DeepEqual(rowsAfter, rowsBefore) {
		t.Errorf("update without skillIds changed agent_skills rows: before %+v, after %+v", rowsBefore, rowsAfter)
	}
}
