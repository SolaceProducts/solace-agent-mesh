// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestSkillRepo_CreateAndGetByID(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID:              "e0000000-0000-0000-0000-000000000101",
		Name:            "manim_skill",
		Description:     strPtr("Manim animation skill"),
		Tags:            strPtr(`["animation","rendering"]`),
		Origin:          "filesystem",
		DiscoveryStatus: "ready",
		HasReferences:   true,
		HasAssets:       false,
		HasTools:        true,
		ResourceCount:   5,
		BundledTools:    strPtr(`["render_manim"]`),
		Toolsets:        strPtr(`["bamboohr_profile_provider","atlassian_rest_request"]`),
		ConfigYAML:      strPtr("name: manim\nversion: 1"),
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
		CreatedTime:     1000,
		UpdatedTime:     2000,
	}

	if err := repo.Create(ctx, skill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, skill.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("GetByID returned nil")
	}

	if got.ID != skill.ID {
		t.Errorf("ID = %q, want %q", got.ID, skill.ID)
	}
	if got.Name != skill.Name {
		t.Errorf("Name = %q, want %q", got.Name, skill.Name)
	}
	if got.Description == nil || *got.Description != *skill.Description {
		t.Errorf("Description = %v, want %v", got.Description, skill.Description)
	}
	if got.Tags == nil || *got.Tags != *skill.Tags {
		t.Errorf("Tags = %v, want %v", got.Tags, skill.Tags)
	}
	if got.Origin != skill.Origin {
		t.Errorf("Origin = %q, want %q", got.Origin, skill.Origin)
	}
	if got.DiscoveryStatus != skill.DiscoveryStatus {
		t.Errorf("DiscoveryStatus = %q, want %q", got.DiscoveryStatus, skill.DiscoveryStatus)
	}
	if got.HasReferences != true {
		t.Errorf("HasReferences = %v, want true", got.HasReferences)
	}
	if got.HasAssets != false {
		t.Errorf("HasAssets = %v, want false", got.HasAssets)
	}
	if got.HasTools != true {
		t.Errorf("HasTools = %v, want true", got.HasTools)
	}
	if got.ResourceCount != 5 {
		t.Errorf("ResourceCount = %d, want 5", got.ResourceCount)
	}
	if got.BundledTools == nil || *got.BundledTools != *skill.BundledTools {
		t.Errorf("BundledTools = %v, want %v", got.BundledTools, skill.BundledTools)
	}
	if got.Toolsets == nil || *got.Toolsets != *skill.Toolsets {
		t.Errorf("Toolsets = %v, want %v", got.Toolsets, skill.Toolsets)
	}
	if got.ConfigYAML == nil || *got.ConfigYAML != *skill.ConfigYAML {
		t.Errorf("ConfigYAML = %v, want %v", got.ConfigYAML, skill.ConfigYAML)
	}
	if got.CreatedBy != skill.CreatedBy {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, skill.CreatedBy)
	}
	if got.UpdatedBy != skill.UpdatedBy {
		t.Errorf("UpdatedBy = %q, want %q", got.UpdatedBy, skill.UpdatedBy)
	}
	if got.CreatedTime != skill.CreatedTime {
		t.Errorf("CreatedTime = %d, want %d", got.CreatedTime, skill.CreatedTime)
	}
	if got.UpdatedTime != skill.UpdatedTime {
		t.Errorf("UpdatedTime = %d, want %d", got.UpdatedTime, skill.UpdatedTime)
	}
}

func TestSkillRepo_GetByID_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	got, err := repo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestSkillRepo_GetByName(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID:              "e0000000-0000-0000-0000-000000000102",
		Name:            "character_bible",
		Origin:          "filesystem",
		DiscoveryStatus: "ready",
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
	}
	if err := repo.Create(ctx, skill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByName(ctx, "character_bible")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName returned nil")
	}
	if got.ID != skill.ID {
		t.Errorf("ID = %q, want %q", got.ID, skill.ID)
	}
	if got.Name != "character_bible" {
		t.Errorf("Name = %q, want %q", got.Name, "character_bible")
	}
}

func TestSkillRepo_GetByName_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	got, err := repo.GetByName(ctx, "nonexistent_skill")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestSkillRepo_UpsertByName_Insert(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID:              "e0000000-0000-0000-0000-000000000103",
		Name:            "upsert_new_skill",
		Description:     strPtr("Brand new skill"),
		Tags:            strPtr(`["new"]`),
		Origin:          "filesystem",
		DiscoveryStatus: "ready",
		HasReferences:   true,
		HasAssets:       false,
		HasTools:        true,
		ResourceCount:   3,
		BundledTools:    strPtr(`["tool_a"]`),
		Toolsets:        strPtr(`["ts_a","ts_b"]`),
		ConfigYAML:      strPtr("name: new_skill"),
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}

	if err := repo.UpsertByName(ctx, skill); err != nil {
		t.Fatalf("UpsertByName (insert): %v", err)
	}

	got, err := repo.GetByName(ctx, "upsert_new_skill")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("expected skill to exist after upsert insert")
	}
	if got.Name != "upsert_new_skill" {
		t.Errorf("Name = %q, want %q", got.Name, "upsert_new_skill")
	}
	if got.Description == nil || *got.Description != "Brand new skill" {
		t.Errorf("Description = %v, want %q", got.Description, "Brand new skill")
	}
	if got.Origin != "filesystem" {
		t.Errorf("Origin = %q, want %q", got.Origin, "filesystem")
	}
	if got.DiscoveryStatus != "ready" {
		t.Errorf("DiscoveryStatus = %q, want %q", got.DiscoveryStatus, "ready")
	}
	if got.HasReferences != true {
		t.Errorf("HasReferences = %v, want true", got.HasReferences)
	}
	if got.HasTools != true {
		t.Errorf("HasTools = %v, want true", got.HasTools)
	}
	if got.ResourceCount != 3 {
		t.Errorf("ResourceCount = %d, want 3", got.ResourceCount)
	}
	if got.Toolsets == nil || *got.Toolsets != `["ts_a","ts_b"]` {
		t.Errorf("Toolsets = %v, want %q", got.Toolsets, `["ts_a","ts_b"]`)
	}
}

func TestSkillRepo_UpsertByName_Update(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	original := &Skill{
		ID:              "e0000000-0000-0000-0000-000000000104",
		Name:            "upsert_existing_skill",
		Description:     strPtr("Original description"),
		Tags:            strPtr(`["original"]`),
		Origin:          "filesystem",
		DiscoveryStatus: "ready",
		HasReferences:   false,
		HasAssets:       false,
		HasTools:        false,
		ResourceCount:   1,
		Toolsets:        strPtr(`["original_toolset"]`),
		ConfigYAML:      strPtr("name: original_config"),
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
	}
	if err := repo.Create(ctx, original); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := &Skill{
		ID:              "e0000000-0000-0000-0000-999999999999",
		Name:            "upsert_existing_skill",
		Description:     strPtr("Updated description"),
		Tags:            strPtr(`["updated","changed"]`),
		Origin:          "platform",
		DiscoveryStatus: "pending",
		HasReferences:   true,
		HasAssets:       true,
		HasTools:        true,
		ResourceCount:   10,
		Toolsets:        strPtr(`["new_toolset_a","new_toolset_b"]`),
		ConfigYAML:      strPtr("name: overwritten_config"),
		CreatedBy:       "other",
		UpdatedBy:       "reconciler",
	}
	if err := repo.UpsertByName(ctx, updated); err != nil {
		t.Fatalf("UpsertByName (update): %v", err)
	}

	got, err := repo.GetByName(ctx, "upsert_existing_skill")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("expected skill to exist after upsert update")
	}

	if got.Description == nil || *got.Description != "Updated description" {
		t.Errorf("Description should be updated: got %v, want %q", got.Description, "Updated description")
	}
	if got.Tags == nil || *got.Tags != `["updated","changed"]` {
		t.Errorf("Tags should be updated: got %v, want %q", got.Tags, `["updated","changed"]`)
	}
	if got.DiscoveryStatus != "pending" {
		t.Errorf("DiscoveryStatus should be updated: got %q, want %q", got.DiscoveryStatus, "pending")
	}
	if got.HasReferences != true {
		t.Errorf("HasReferences should be updated: got %v, want true", got.HasReferences)
	}
	if got.HasTools != true {
		t.Errorf("HasTools should be updated: got %v, want true", got.HasTools)
	}
	if got.ResourceCount != 10 {
		t.Errorf("ResourceCount should be updated: got %d, want 10", got.ResourceCount)
	}
	if got.Toolsets == nil || *got.Toolsets != `["new_toolset_a","new_toolset_b"]` {
		t.Errorf("Toolsets should be updated: got %v, want %q", got.Toolsets, `["new_toolset_a","new_toolset_b"]`)
	}

	if got.Origin != "filesystem" {
		t.Errorf("Origin should be PRESERVED: got %q, want %q", got.Origin, "filesystem")
	}
	if got.ConfigYAML == nil || *got.ConfigYAML != "name: original_config" {
		t.Errorf("ConfigYAML should be PRESERVED: got %v, want %q", got.ConfigYAML, "name: original_config")
	}

	if got.ID != original.ID {
		t.Errorf("ID should be preserved from original insert: got %q, want %q", got.ID, original.ID)
	}
}

func TestSkillRepo_Update(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID:              "e0000000-0000-0000-0000-000000000105",
		Name:            "updatable_skill",
		Description:     strPtr("Before update"),
		Origin:          "filesystem",
		DiscoveryStatus: "ready",
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
	}
	if err := repo.Create(ctx, skill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	skill.Description = strPtr("After update")
	skill.UpdatedBy = "editor"
	skill.Toolsets = strPtr(`["updated_toolset"]`)
	if err := repo.Update(ctx, skill); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByID(ctx, skill.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Description == nil || *got.Description != "After update" {
		t.Errorf("Description = %v, want %q", got.Description, "After update")
	}
	if got.UpdatedBy != "editor" {
		t.Errorf("UpdatedBy = %q, want %q", got.UpdatedBy, "editor")
	}
	if got.Toolsets == nil || *got.Toolsets != `["updated_toolset"]` {
		t.Errorf("Toolsets = %v, want %q", got.Toolsets, `["updated_toolset"]`)
	}
}

func TestSkillRepo_Update_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID:              "fffffffe-ffff-ffff-ffff-ffffffffffff",
		Name:            "ghost_skill",
		Origin:          "filesystem",
		DiscoveryStatus: "ready",
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
	}
	err := repo.Update(ctx, skill)
	if err == nil {
		t.Fatal("expected error updating nonexistent skill")
	}
}

func TestSkillRepo_Delete(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID:              "e0000000-0000-0000-0000-000000000106",
		Name:            "deletable_skill",
		Origin:          "filesystem",
		DiscoveryStatus: "ready",
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
	}
	if err := repo.Create(ctx, skill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, skill.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.GetByID(ctx, skill.ID)
	if err != nil {
		t.Fatalf("GetByID after delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestSkillRepo_Delete_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	err := repo.Delete(ctx, "fffffffd-ffff-ffff-ffff-ffffffffffff")
	if err == nil {
		t.Fatal("expected error deleting nonexistent skill")
	}
}

func TestSkillRepo_ListPaginated(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	for i, name := range []string{"skill_a", "skill_b", "skill_c"} {
		skill := &Skill{
			ID:              "e0000000-0000-0000-0000-00000000020" + string(rune('0'+i)),
			Name:            name,
			Origin:          "filesystem",
			DiscoveryStatus: "ready",
			CreatedBy:       "admin",
			UpdatedBy:       "admin",
			CreatedTime:     int64(1000 + i),
		}
		if err := repo.Create(ctx, skill); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	skills, total, err := repo.ListPaginated(ctx, 1, 2)
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(skills) != 2 {
		t.Errorf("len(skills) = %d, want 2", len(skills))
	}
}

func TestSkillRepo_SetDiscoveryStatus(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID:              "e0000000-0000-0000-0000-000000000107",
		Name:            "status_skill",
		Origin:          "filesystem",
		DiscoveryStatus: "pending",
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
	}
	if err := repo.Create(ctx, skill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.SetDiscoveryStatus(ctx, "status_skill", "ready"); err != nil {
		t.Fatalf("SetDiscoveryStatus: %v", err)
	}

	got, err := repo.GetByName(ctx, "status_skill")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName returned nil")
	}
	if got.DiscoveryStatus != "ready" {
		t.Errorf("DiscoveryStatus = %q, want %q", got.DiscoveryStatus, "ready")
	}
}

func TestSkillRepo_SetAgentSkills(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill1 := &Skill{
		ID: "e0000000-0000-0000-0000-000000000201", Name: "skill_one",
		Origin: "filesystem", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
	}
	skill2 := &Skill{
		ID: "e0000000-0000-0000-0000-000000000202", Name: "skill_two",
		Origin: "filesystem", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
	}
	for _, s := range []*Skill{skill1, skill2} {
		if err := repo.Create(ctx, s); err != nil {
			t.Fatalf("Create skill %s: %v", s.Name, err)
		}
	}

	agentID := "a0000000-0000-0000-0000-000000000001"

	agentRepo := NewAgentRepo(db)
	agent := &Agent{ID: agentID, Name: "test-agent", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	if err := repo.SetAgentSkills(ctx, agentID, []string{skill1.ID, skill2.ID}); err != nil {
		t.Fatalf("SetAgentSkills: %v", err)
	}

	ids, err := repo.GetSkillIDsForAgent(ctx, agentID)
	if err != nil {
		t.Fatalf("GetSkillIDsForAgent: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("len(ids) = %d, want 2", len(ids))
	}

	if replaceErr := repo.SetAgentSkills(ctx, agentID, []string{skill1.ID}); replaceErr != nil {
		t.Fatalf("SetAgentSkills (replace): %v", replaceErr)
	}

	ids, err = repo.GetSkillIDsForAgent(ctx, agentID)
	if err != nil {
		t.Fatalf("GetSkillIDsForAgent after replace: %v", err)
	}
	if len(ids) != 1 || ids[0] != skill1.ID {
		t.Errorf("ids = %v, want [%s]", ids, skill1.ID)
	}
}

func TestSkillRepo_GetSkillIDsForAgents(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill1 := &Skill{
		ID: "e0000000-0000-0000-0000-000000000301", Name: "skill_a",
		Origin: "filesystem", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
	}
	skill2 := &Skill{
		ID: "e0000000-0000-0000-0000-000000000302", Name: "skill_b",
		Origin: "filesystem", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
	}
	for _, s := range []*Skill{skill1, skill2} {
		if err := repo.Create(ctx, s); err != nil {
			t.Fatalf("Create skill %s: %v", s.Name, err)
		}
	}

	agentRepo := NewAgentRepo(db)
	withSkills := "a0000000-0000-0000-0000-000000000011"
	oneSkill := "a0000000-0000-0000-0000-000000000012"
	noSkills := "a0000000-0000-0000-0000-000000000013"
	for _, id := range []string{withSkills, oneSkill, noSkills} {
		agent := &Agent{ID: id, Name: "agent-" + id, Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
		if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %s: %v", id, err)
		}
	}
	if err := repo.SetAgentSkills(ctx, withSkills, []string{skill1.ID, skill2.ID}); err != nil {
		t.Fatalf("SetAgentSkills(withSkills): %v", err)
	}
	if err := repo.SetAgentSkills(ctx, oneSkill, []string{skill1.ID}); err != nil {
		t.Fatalf("SetAgentSkills(oneSkill): %v", err)
	}

	if got, err := repo.GetSkillIDsForAgents(ctx, nil); err != nil {
		t.Fatalf("GetSkillIDsForAgents(nil): %v", err)
	} else if len(got) != 0 {
		t.Errorf("GetSkillIDsForAgents(nil) = %v, want empty", got)
	}

	got, err := repo.GetSkillIDsForAgents(ctx, []string{withSkills, oneSkill, noSkills})
	if err != nil {
		t.Fatalf("GetSkillIDsForAgents: %v", err)
	}

	if want := []string{skill1.ID, skill2.ID}; !slices.Equal(got[withSkills], want) {
		t.Errorf("got[withSkills] = %v, want %v", got[withSkills], want)
	}
	if want := []string{skill1.ID}; !slices.Equal(got[oneSkill], want) {
		t.Errorf("got[oneSkill] = %v, want %v", got[oneSkill], want)
	}
	if _, present := got[noSkills]; present {
		t.Errorf("agent with no skills should be absent from map, got %v", got[noSkills])
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.GetSkillIDsForAgents(canceled, []string{withSkills}); err == nil {
		t.Error("GetSkillIDsForAgents with canceled context: expected error, got nil")
	}
}

func TestSkillRepo_SkillConfigRoundtrip(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID: "e0000000-0000-0000-0000-000000000301", Name: "render_skill",
		Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
	}
	if err := repo.Create(ctx, skill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got, err := repo.GetSkillConfig(ctx, skill.ID); err != nil {
		t.Fatalf("GetSkillConfig empty: %v", err)
	} else if got != nil {
		t.Errorf("expected nil before insert, got %+v", got)
	}

	vals := strPtr(`{"render":{"api_key":"sk-test","quality":"high"}}`)
	if err := repo.SetSkillConfig(ctx, skill.ID, vals, "operator"); err != nil {
		t.Fatalf("SetSkillConfig: %v", err)
	}

	got, err := repo.GetSkillConfig(ctx, skill.ID)
	if err != nil {
		t.Fatalf("GetSkillConfig after insert: %v", err)
	}
	if got == nil {
		t.Fatal("GetSkillConfig returned nil after insert")
	}
	if got.SkillID != skill.ID {
		t.Errorf("SkillID = %q, want %q", got.SkillID, skill.ID)
	}
	if got.ConfigValues == nil || *got.ConfigValues != *vals {
		t.Errorf("ConfigValues = %v, want %v", got.ConfigValues, vals)
	}
	if got.UpdatedBy != "operator" {
		t.Errorf("UpdatedBy = %q, want operator", got.UpdatedBy)
	}
	if got.UpdatedTime == 0 {
		t.Error("UpdatedTime not stamped")
	}

	newVals := strPtr(`{"render":{"api_key":"sk-rotated"}}`)
	if upsertErr := repo.SetSkillConfig(ctx, skill.ID, newVals, "operator2"); upsertErr != nil {
		t.Fatalf("SetSkillConfig upsert: %v", upsertErr)
	}
	got, _ = repo.GetSkillConfig(ctx, skill.ID)
	if got == nil || *got.ConfigValues != *newVals || got.UpdatedBy != "operator2" {
		t.Errorf("after upsert got = %+v", got)
	}

	missingID := "e0000000-0000-0000-0000-000000000999"
	batch, err := repo.GetSkillConfigsForSkills(ctx, []string{skill.ID, missingID})
	if err != nil {
		t.Fatalf("GetSkillConfigsForSkills: %v", err)
	}
	if len(batch) != 1 {
		t.Errorf("len(batch) = %d, want 1", len(batch))
	}
	if _, ok := batch[skill.ID]; !ok {
		t.Errorf("missing batch entry for %s", skill.ID)
	}

	if empty, err := repo.GetSkillConfigsForSkills(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("empty input → %v / %v", empty, err)
	}

	if err := repo.SetSkillConfig(ctx, skill.ID, nil, "operator3"); err != nil {
		t.Fatalf("SetSkillConfig clear: %v", err)
	}
	got, _ = repo.GetSkillConfig(ctx, skill.ID)
	if got == nil || got.ConfigValues != nil {
		t.Errorf("after clear got = %+v", got)
	}
}

func TestSkillRepo_AgentSkillsWithConfigs(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill1 := &Skill{
		ID: "e0000000-0000-0000-0000-000000000401", Name: "skill_a",
		Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
	}
	skill2 := &Skill{
		ID: "e0000000-0000-0000-0000-000000000402", Name: "skill_b",
		Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
	}
	for _, s := range []*Skill{skill1, skill2} {
		if err := repo.Create(ctx, s); err != nil {
			t.Fatalf("create skill %s: %v", s.Name, err)
		}
	}

	agentID := "a0000000-0000-0000-0000-000000000300"
	agentRepo := NewAgentRepo(db)
	if err := agentRepo.Create(ctx, &Agent{ID: agentID, Name: "agent-x", Description: strPtr("x"), CreatedBy: "u", UpdatedBy: "u"}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	rows := []AgentSkillRow{
		{AgentID: agentID, SkillID: skill1.ID, ConfigValues: strPtr(`{"render":{"api_key":"sk-agent"}}`)},
		{AgentID: agentID, SkillID: skill2.ID},
	}
	if err := repo.SetAgentSkillsWithConfigs(ctx, agentID, rows); err != nil {
		t.Fatalf("SetAgentSkillsWithConfigs: %v", err)
	}

	loaded, err := repo.GetAgentSkillRowsForAgent(ctx, agentID)
	if err != nil {
		t.Fatalf("GetAgentSkillRowsForAgent: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("len(loaded) = %d, want 2", len(loaded))
	}

	bySkill := map[string]AgentSkillRow{}
	for _, r := range loaded {
		bySkill[r.SkillID] = r
	}
	if got := bySkill[skill1.ID]; got.ConfigValues == nil || *got.ConfigValues != `{"render":{"api_key":"sk-agent"}}` {
		t.Errorf("skill1 ConfigValues = %v", got.ConfigValues)
	}
	if got := bySkill[skill2.ID]; got.ConfigValues != nil {
		t.Errorf("skill2 ConfigValues = %v, want nil", got.ConfigValues)
	}

	if err := repo.SetAgentSkillsWithConfigs(ctx, agentID, []AgentSkillRow{{AgentID: agentID, SkillID: skill1.ID}}); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	loaded, _ = repo.GetAgentSkillRowsForAgent(ctx, agentID)
	if len(loaded) != 1 || loaded[0].ConfigValues != nil {
		t.Errorf("after re-set loaded = %+v", loaded)
	}
}

func TestSkillRepo_GetAgentsUsingSkill(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID: "e0000000-0000-0000-0000-000000000301", Name: "shared_skill",
		Origin: "filesystem", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
	}
	if err := repo.Create(ctx, skill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	agentA := "a0000000-0000-0000-0000-000000000010"
	agentB := "a0000000-0000-0000-0000-000000000011"

	agentRepo := NewAgentRepo(db)
	for _, agID := range []string{agentA, agentB} {
		ag := &Agent{ID: agID, Name: "agent-" + agID[len(agID)-4:], Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
		if err := agentRepo.Create(ctx, ag, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %s: %v", agID, err)
		}
	}

	if err := repo.SetAgentSkills(ctx, agentA, []string{skill.ID}); err != nil {
		t.Fatalf("SetAgentSkills agentA: %v", err)
	}
	if err := repo.SetAgentSkills(ctx, agentB, []string{skill.ID}); err != nil {
		t.Fatalf("SetAgentSkills agentB: %v", err)
	}

	agents, err := repo.GetAgentsUsingSkill(ctx, skill.ID)
	if err != nil {
		t.Fatalf("GetAgentsUsingSkill: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("len(agents) = %d, want 2", len(agents))
	}
}

func TestSkillRepo_GetAgentsBySkillIDs(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill1 := &Skill{ID: "e0000000-0000-0000-0000-000000000401", Name: "skill_alpha", Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin"}
	skill2 := &Skill{ID: "e0000000-0000-0000-0000-000000000402", Name: "skill_beta", Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin"}
	for _, s := range []*Skill{skill1, skill2} {
		if err := repo.Create(ctx, s); err != nil {
			t.Fatalf("create skill %s: %v", s.Name, err)
		}
	}

	agentA := "a0000000-0000-0000-0000-000000000020"
	agentB := "a0000000-0000-0000-0000-000000000021"
	agentRepo := NewAgentRepo(db)
	for _, ag := range []*Agent{
		{ID: agentA, Name: "agent-alpha", Description: strPtr("a"), CreatedBy: "u", UpdatedBy: "u"},
		{ID: agentB, Name: "agent-beta", Description: strPtr("b"), CreatedBy: "u", UpdatedBy: "u"},
	} {
		if err := agentRepo.Create(ctx, ag, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %s: %v", ag.ID, err)
		}
	}

	if err := repo.SetAgentSkills(ctx, agentA, []string{skill1.ID}); err != nil {
		t.Fatalf("SetAgentSkills agentA: %v", err)
	}
	if err := repo.SetAgentSkills(ctx, agentB, []string{skill1.ID, skill2.ID}); err != nil {
		t.Fatalf("SetAgentSkills agentB: %v", err)
	}

	missingID := "e0000000-0000-0000-0000-000000000499"
	got, err := repo.GetAgentsBySkillIDs(ctx, []string{skill1.ID, skill2.ID, missingID})
	if err != nil {
		t.Fatalf("GetAgentsBySkillIDs: %v", err)
	}
	if len(got[skill1.ID]) != 2 {
		t.Errorf("skill1 agents = %d, want 2", len(got[skill1.ID]))
	}
	if len(got[skill2.ID]) != 1 {
		t.Errorf("skill2 agents = %d, want 1", len(got[skill2.ID]))
	}
	if got[skill2.ID][0].Name != "agent-beta" || got[skill2.ID][0].ID != agentB {
		t.Errorf("skill2 ref = %+v, want agent-beta/%s", got[skill2.ID][0], agentB)
	}
	if _, ok := got[missingID]; !ok {
		t.Errorf("missing skill key absent from result map")
	}
	if len(got[missingID]) != 0 {
		t.Errorf("missing skill agents = %d, want 0", len(got[missingID]))
	}

	empty, err := repo.GetAgentsBySkillIDs(ctx, nil)
	if err != nil {
		t.Fatalf("GetAgentsBySkillIDs(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty input result = %d, want 0", len(empty))
	}
}

func TestSkillRepo_GetAgentsBySkillRefNames(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()
	agentRepo := NewAgentRepo(db)

	mk := func(id, name string, refs *string) *Agent {
		return &Agent{ID: id, Name: name, Description: strPtr("t"), SkillRefs: refs, CreatedBy: "u", UpdatedBy: "u"}
	}
	agents := []*Agent{
		mk("a0000000-0000-0000-0000-000000000030", "agent-x", strPtr(`["sam-knowledge"]`)),
		mk("a0000000-0000-0000-0000-000000000031", "agent-y", strPtr(`["sam-docs","sam-knowledge"]`)),
		mk("a0000000-0000-0000-0000-000000000032", "agent-z", nil),
		mk("a0000000-0000-0000-0000-000000000033", "agent-w", strPtr(`{not valid json`)),
	}
	for _, ag := range agents {
		if err := agentRepo.Create(ctx, ag, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %s: %v", ag.ID, err)
		}
	}

	got, err := repo.GetAgentsBySkillRefNames(ctx, []string{"sam-knowledge", "sam-docs", "unknown-skill"})
	if err != nil {
		t.Fatalf("GetAgentsBySkillRefNames: %v", err)
	}
	if len(got["sam-knowledge"]) != 2 {
		t.Errorf("sam-knowledge agents = %d, want 2 (agent-x, agent-y)", len(got["sam-knowledge"]))
	}
	if len(got["sam-docs"]) != 1 {
		t.Errorf("sam-docs agents = %d, want 1 (agent-y)", len(got["sam-docs"]))
	}
	if len(got["sam-docs"]) == 1 && got["sam-docs"][0].Name != "agent-y" {
		t.Errorf("sam-docs ref = %q, want agent-y", got["sam-docs"][0].Name)
	}
	if _, ok := got["unknown-skill"]; !ok {
		t.Errorf("unknown-skill key absent from result map")
	}
	if len(got["unknown-skill"]) != 0 {
		t.Errorf("unknown-skill agents = %d, want 0", len(got["unknown-skill"]))
	}

	empty, err := repo.GetAgentsBySkillRefNames(ctx, nil)
	if err != nil {
		t.Fatalf("GetAgentsBySkillRefNames(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty input result = %d, want 0", len(empty))
	}
}

func TestSkillRepo_MarkFailed(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skill := &Skill{
		ID:              "e0000000-0000-0000-0000-000000000401",
		Name:            "markfailed_skill",
		Origin:          "platform",
		DiscoveryStatus: "pending",
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
	}
	if err := repo.Create(ctx, skill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	past := time.Now().Add(-time.Hour).UnixMilli()
	if failed, err := repo.FailStalePending(ctx, skill.ID, past); err != nil || failed {
		t.Fatalf("FailStalePending before the skill went pending = %v, %v; want it kept", failed, err)
	}
	future := time.Now().Add(time.Hour).UnixMilli()
	if failed, err := repo.FailStalePending(ctx, skill.ID, future); err != nil || !failed {
		t.Fatalf("FailStalePending = %v, %v; want the pending skill failed", failed, err)
	}

	got, err := repo.GetByID(ctx, skill.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("GetByID returned nil")
	}
	if got.DiscoveryStatus != "failed" {
		t.Errorf("DiscoveryStatus = %q, want \"failed\"", got.DiscoveryStatus)
	}

	if err := repo.SetDiscoveryStatus(ctx, skill.Name, "ready"); err != nil {
		t.Fatalf("SetDiscoveryStatus: %v", err)
	}
	if failed, err := repo.FailStalePending(ctx, skill.ID, future); err != nil || failed {
		t.Fatalf("FailStalePending on a discovered skill = %v, %v; want it kept", failed, err)
	}
	if failed, err := repo.FailStalePending(ctx, "e0000000-0000-0000-0000-000000000000", future); err != nil || failed {
		t.Fatalf("FailStalePending on an unknown id = %v, %v; want nothing failed", failed, err)
	}
}

func TestSkillRepo_GetByIDs(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	skills := []*Skill{
		{
			ID: "e0000000-0000-0000-0000-000000000501", Name: "skill_alpha",
			Origin: "filesystem", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
		},
		{
			ID: "e0000000-0000-0000-0000-000000000502", Name: "skill_beta",
			Origin: "filesystem", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
		},
		{
			ID: "e0000000-0000-0000-0000-000000000503", Name: "skill_gamma",
			Origin: "filesystem", DiscoveryStatus: "ready", CreatedBy: "admin", UpdatedBy: "admin",
		},
	}
	for _, s := range skills {
		if err := repo.Create(ctx, s); err != nil {
			t.Fatalf("Create %s: %v", s.Name, err)
		}
	}

	got, err := repo.GetByIDs(ctx, []string{skills[0].ID, skills[2].ID})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}

	names := make(map[string]bool)
	for _, s := range got {
		names[s.Name] = true
	}
	if !names["skill_alpha"] {
		t.Errorf("skill_alpha missing from results")
	}
	if !names["skill_gamma"] {
		t.Errorf("skill_gamma missing from results")
	}

	empty, err := repo.GetByIDs(ctx, []string{"e0000000-0000-0000-0000-000000000000"})
	if err != nil {
		t.Fatalf("GetByIDs nonexistent: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected empty slice for nonexistent ID, got %d", len(empty))
	}

	nilResult, err := repo.GetByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("GetByIDs nil: %v", err)
	}
	if nilResult != nil {
		t.Errorf("expected nil for nil input, got %v", nilResult)
	}
}

func TestSkillRepo_ReferenceAssetCountsRoundTrip(t *testing.T) {
	db := TestDB(t)
	repo := NewSkillRepo(db)
	ctx := context.Background()

	assertCounts := func(t *testing.T, label string, got *Skill, ref, asset, total int) {
		t.Helper()
		if got == nil {
			t.Fatalf("%s: skill is nil", label)
		}
		if got.ReferenceCount != ref {
			t.Errorf("%s: ReferenceCount = %d, want %d", label, got.ReferenceCount, ref)
		}
		if got.AssetCount != asset {
			t.Errorf("%s: AssetCount = %d, want %d", label, got.AssetCount, asset)
		}
		if got.ResourceCount != total {
			t.Errorf("%s: ResourceCount = %d, want %d", label, got.ResourceCount, total)
		}
	}

	created := &Skill{
		ID:              "e0000000-0000-0000-0000-0000000000aa",
		Name:            "split_counts_skill",
		Origin:          "platform",
		DiscoveryStatus: "ready",
		HasReferences:   true,
		HasAssets:       true,
		ResourceCount:   10,
		ReferenceCount:  3,
		AssetCount:      7,
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
	}
	if err := repo.Create(ctx, created); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	assertCounts(t, "after Create", got, 3, 7, 10)

	upserted := &Skill{
		Name:            "upsert_split_counts",
		Origin:          "platform",
		DiscoveryStatus: "ready",
		HasReferences:   true,
		HasAssets:       true,
		ResourceCount:   12,
		ReferenceCount:  4,
		AssetCount:      8,
		CreatedBy:       "admin",
		UpdatedBy:       "admin",
	}
	if err = repo.UpsertByName(ctx, upserted); err != nil {
		t.Fatalf("UpsertByName insert: %v", err)
	}
	got, err = repo.GetByName(ctx, upserted.Name)
	if err != nil {
		t.Fatalf("GetByName after insert: %v", err)
	}
	assertCounts(t, "after UpsertByName insert", got, 4, 8, 12)

	upserted.ReferenceCount = 9
	upserted.AssetCount = 2
	upserted.ResourceCount = 11
	if err = repo.UpsertByName(ctx, upserted); err != nil {
		t.Fatalf("UpsertByName update: %v", err)
	}
	got, err = repo.GetByName(ctx, upserted.Name)
	if err != nil {
		t.Fatalf("GetByName after update: %v", err)
	}
	assertCounts(t, "after UpsertByName update", got, 9, 2, 11)

	created.ReferenceCount = 1
	created.AssetCount = 6
	created.ResourceCount = 7
	if err = repo.Update(ctx, created); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID after Update: %v", err)
	}
	assertCounts(t, "after Update", got, 1, 6, 7)
}
