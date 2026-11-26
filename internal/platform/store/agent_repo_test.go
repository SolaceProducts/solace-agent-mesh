// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func TestAgentRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := context.Background()

	desc := "A test agent for unit testing purposes"
	prompt := "You are a helpful assistant that can answer questions. You should always be polite and provide accurate information to the best of your ability. Please help the user."
	agent := &Agent{
		Name:         "test-agent",
		Description:  &desc,
		SystemPrompt: &prompt,
		CreatedBy:    "user1",
		UpdatedBy:    "user1",
	}
	toolsets := []string{"builtin_artifact_tools"}
	inputModes := []string{"text"}
	outputModes := []string{"text", "file"}
	err := repo.Create(ctx, agent, toolsets, inputModes, outputModes, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if agent.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, gotToolsets, gotInput, gotOutput, gotConn, err := repo.GetByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected agent")
	}
	if got.Name != "test-agent" {
		t.Errorf("name = %q, want %q", got.Name, "test-agent")
	}
	if len(gotToolsets) != 1 || gotToolsets[0] != "builtin_artifact_tools" {
		t.Errorf("toolsets = %v, want [builtin_artifact_tools]", gotToolsets)
	}
	if len(gotInput) != 1 || gotInput[0] != "text" {
		t.Errorf("input modes = %v, want [text]", gotInput)
	}
	if len(gotOutput) != 2 {
		t.Errorf("output modes = %v, want 2 items", gotOutput)
	}
	if len(gotConn) != 0 {
		t.Errorf("connectors = %v, want empty", gotConn)
	}

	newDesc := "Updated description for testing"
	got.Name = "renamed-agent"
	got.Description = &newDesc
	got.UpdatedBy = "user2"
	err = repo.Update(ctx, got, []string{"builtin_web_request_tools"}, []string{"text", "file"}, []string{"text"}, nil, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got2, toolsets2, input2, output2, _, err := repo.GetByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got2.Name != "renamed-agent" {
		t.Errorf("name = %q, want %q", got2.Name, "renamed-agent")
	}
	if len(toolsets2) != 1 || toolsets2[0] != "builtin_web_request_tools" {
		t.Errorf("toolsets = %v, want [builtin_web_request_tools]", toolsets2)
	}
	if len(input2) != 2 {
		t.Errorf("input modes = %v, want 2", input2)
	}
	if len(output2) != 1 {
		t.Errorf("output modes = %v, want 1", output2)
	}

	list, total, err := repo.ListPaginated(ctx, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(list) != 1 {
		t.Errorf("len = %d, want 1", len(list))
	}

	err = repo.Delete(ctx, agent.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	gone, _, _, _, _, err := repo.GetByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if gone != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestAgentRepo_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := context.Background()

	got, _, _, _, _, err := repo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil")
	}

	err = repo.Delete(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err == nil {
		t.Fatal("expected error deleting nonexistent")
	}
}

func TestAgentRepo_DeploymentStatus(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := context.Background()

	agent := &Agent{Name: "agent1", Description: strp("Agent for testing purposes"), CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	status, err := repo.GetDeploymentStatus(ctx, agent.ID)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status != "not_deployed" {
		t.Errorf("status = %q, want %q", status, "not_deployed")
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO agent_deployments (id, agent_id, status, action, created_by, created_time, updated_time)
		 VALUES ('d0000000-0000-0000-0000-000000000001', ?, 'success', 'deploy', 'u', 100, 100)`, agent.ID)
	if err != nil {
		t.Fatalf("insert deployment: %v", err)
	}

	status, err = repo.GetDeploymentStatus(ctx, agent.ID)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status != "deployed" {
		t.Errorf("status = %q, want %q", status, "deployed")
	}

	statuses, err := repo.GetDeploymentStatuses(ctx, []string{agent.ID})
	if err != nil {
		t.Fatalf("batch statuses: %v", err)
	}
	if statuses[agent.ID] != "deployed" {
		t.Errorf("batch status = %q, want %q", statuses[agent.ID], "deployed")
	}
}

func TestAgentRepo_NormalizedNameIndex(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := t.Context()

	desc := "test agent"
	agent := &Agent{Name: "My Agent", Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("case_insensitive_lookup", func(t *testing.T) {
		for _, lookup := range []string{"my agent", "MYAGENT", "MyAgent", "MY AGENT", "myagent"} {
			got, _, _, _, _, err := repo.GetByName(ctx, lookup)
			if err != nil {
				t.Fatalf("GetByName(%q): %v", lookup, err)
			}
			if got == nil || got.ID != agent.ID {
				t.Errorf("GetByName(%q) = %v, want ID %q", lookup, got, agent.ID)
			}
		}
	})

	t.Run("rejects_normalized_duplicate", func(t *testing.T) {
		dup := &Agent{Name: "myagent", Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
		if err := repo.Create(ctx, dup, nil, nil, nil, nil, nil); err == nil {
			t.Fatal("expected unique constraint violation for normalized duplicate name")
		}
	})
}

func TestAgentRepo_ListExcludesA2AProxy(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := t.Context()

	desc := "description used for unit testing purposes"

	standard := &Agent{Name: "Standard", Description: &desc, Type: "standard", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, standard, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create standard: %v", err)
	}
	proxy := &Agent{Name: "Proxy", Description: &desc, Type: "a2a_proxy", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, proxy, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create proxy: %v", err)
	}

	t.Run("ListPaginated_excludes_proxy", func(t *testing.T) {
		list, total, err := repo.ListPaginated(ctx, 1, 10)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1 (proxy excluded)", total)
		}
		if len(list) != 1 {
			t.Fatalf("len = %d, want 1", len(list))
		}
		if list[0].Agent.Type == "a2a_proxy" {
			t.Errorf("unexpected a2a_proxy row in list")
		}
	})

	t.Run("GetByID_still_returns_proxy", func(t *testing.T) {
		got, _, _, _, _, err := repo.GetByID(ctx, proxy.ID)
		if err != nil {
			t.Fatalf("get proxy: %v", err)
		}
		if got == nil || got.Type != "a2a_proxy" {
			t.Fatalf("GetByID proxy = %v, want a2a_proxy row", got)
		}
	})
}

func TestAgentRepo_ExcludedTools_RoundTrip(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := t.Context()

	desc := "agent for excluded_tools round-trip testing"
	agent := &Agent{Name: "excl-agent", Description: &desc, CreatedBy: "u", UpdatedBy: "u"}

	exclusions := map[string]string{
		"pkg_a": `["foo","bar"]`,
		"pkg_b": `["baz"]`,
	}
	toolsets := []string{"pkg_a", "pkg_b", "builtin_artifact_tools"}
	if err := repo.Create(ctx, agent, toolsets, nil, nil, nil, exclusions); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("initial_exclusions_persisted", func(t *testing.T) {
		gotToolsets, gotExcl, err := repo.GetToolsetsWithExclusions(ctx, agent.ID)
		if err != nil {
			t.Fatalf("GetToolsetsWithExclusions: %v", err)
		}
		if len(gotToolsets) != 3 {
			t.Errorf("toolsets len = %d, want 3", len(gotToolsets))
		}
		if gotExcl["pkg_a"] != `["foo","bar"]` {
			t.Errorf("pkg_a exclusions = %q, want [\"foo\",\"bar\"]", gotExcl["pkg_a"])
		}
		if gotExcl["pkg_b"] != `["baz"]` {
			t.Errorf("pkg_b exclusions = %q, want [\"baz\"]", gotExcl["pkg_b"])
		}
		if _, ok := gotExcl["builtin_artifact_tools"]; ok {
			t.Error("builtin_artifact_tools should have NULL excluded_tools, not appear in map")
		}
	})

	t.Run("update_changes_exclusions", func(t *testing.T) {
		newExcl := map[string]string{
			"pkg_a": `["only_foo"]`,
		}
		agent.UpdatedBy = "u2"
		if err := repo.Update(ctx, agent, toolsets, nil, nil, nil, newExcl); err != nil {
			t.Fatalf("update: %v", err)
		}
		gotToolsets, gotExcl, err := repo.GetToolsetsWithExclusions(ctx, agent.ID)
		if err != nil {
			t.Fatalf("GetToolsetsWithExclusions after update: %v", err)
		}
		if len(gotToolsets) != 3 {
			t.Errorf("toolsets len after update = %d, want 3", len(gotToolsets))
		}
		if gotExcl["pkg_a"] != `["only_foo"]` {
			t.Errorf("pkg_a after update = %q, want [\"only_foo\"]", gotExcl["pkg_a"])
		}
		if _, ok := gotExcl["pkg_b"]; ok {
			t.Error("pkg_b should have NULL excluded_tools after update with no exclusion for it")
		}
	})

	t.Run("nil_exclusions_clears_all", func(t *testing.T) {
		agent.UpdatedBy = "u3"
		if err := repo.Update(ctx, agent, toolsets, nil, nil, nil, nil); err != nil {
			t.Fatalf("update with nil exclusions: %v", err)
		}
		_, gotExcl, err := repo.GetToolsetsWithExclusions(ctx, agent.ID)
		if err != nil {
			t.Fatalf("GetToolsetsWithExclusions after nil exclusions: %v", err)
		}
		if len(gotExcl) != 0 {
			t.Errorf("expected no exclusions after nil update, got %v", gotExcl)
		}
	})
}

func TestAgentRepo_DeploymentStatus_CreatedTimeTieBreak(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	deployRepo := NewAgentDeploymentRepo(db)
	ctx := t.Context()

	desc := "agent for tie-break test"
	agent := &Agent{Name: "tiebreak-agent", Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	tiedTime := int64(1_000_000_000)

	undeploy := &AgentDeployment{
		AgentID:     agent.ID,
		Status:      "success",
		Action:      "undeploy",
		CreatedBy:   "u",
		CreatedTime: tiedTime,
	}
	if err := deployRepo.Create(ctx, undeploy); err != nil {
		t.Fatalf("create undeploy row: %v", err)
	}

	deploy := &AgentDeployment{
		AgentID:     agent.ID,
		Status:      "success",
		Action:      "deploy",
		CreatedBy:   "u",
		CreatedTime: tiedTime,
	}
	if err := deployRepo.Create(ctx, deploy); err != nil {
		t.Fatalf("create deploy row: %v", err)
	}

	status, err := agentRepo.GetDeploymentStatus(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetDeploymentStatus: %v", err)
	}
	if status != "deployed" {
		t.Errorf("GetDeploymentStatus = %q, want %q (later deploy row must win)", status, "deployed")
	}

	statuses, err := agentRepo.GetDeploymentStatuses(ctx, []string{agent.ID})
	if err != nil {
		t.Fatalf("GetDeploymentStatuses: %v", err)
	}
	if statuses[agent.ID] != "deployed" {
		t.Errorf("GetDeploymentStatuses[agent] = %q, want %q (continue branch must skip the smaller-id row)", statuses[agent.ID], "deployed")
	}
}

func strp(s string) *string { return &s }

func TestAgentRepo_GetAll_ExcludesEphemerals(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := context.Background()

	regularDesc := "regular"
	regularPrompt := "You are a helpful assistant. Long enough to satisfy the platform's prompt-length validator. Please answer politely."
	regular := &Agent{
		Name:         "regular-agent-getall",
		Description:  &regularDesc,
		SystemPrompt: &regularPrompt,
		CreatedBy:    "user1",
		UpdatedBy:    "user1",
	}
	if err := repo.Create(ctx, regular, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create regular: %v", err)
	}

	ephName := "__test__ephemeral-getall"
	ephDesc := "ephemeral"
	ephPrompt := "ephemeral test prompt — long enough to satisfy the platform's prompt-length validator. Please be helpful."
	sessionID := "sess-1"
	componentName := "Source"
	expires := int64(1)
	eph := &Agent{
		Name:                ephName,
		Description:         &ephDesc,
		SystemPrompt:        &ephPrompt,
		CreatedBy:           "user1",
		UpdatedBy:           "user1",
		IsEphemeral:         true,
		ExpiresAt:           &expires,
		SourceSessionID:     &sessionID,
		SourceComponentName: &componentName,
	}
	if err := repo.Create(ctx, eph, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create ephemeral: %v", err)
	}

	got, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	for _, a := range got {
		if a.IsEphemeral {
			t.Errorf("GetAll returned ephemeral agent %s; ephemerals must be excluded", a.Name)
		}
		if a.Name == ephName {
			t.Errorf("ephemeral agent %s leaked into GetAll", a.Name)
		}
	}
	var found bool
	for _, a := range got {
		if a.Name == "regular-agent-getall" {
			found = true
			break
		}
	}
	if !found {
		t.Error("regular agent missing from GetAll result")
	}
}

func TestAgentRepo_ListByConnectorID_FiltersA2AProxyAndEphemeral(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	connectorRepo := NewConnectorRepo(db)
	ctx := context.Background()

	connDesc := "test connector for list-by-connector"
	connSubtype := "rest"
	connValues := "{}"
	connector := &Connector{
		Name:        "test-connector-for-list",
		Type:        "http",
		Description: &connDesc,
		Subtype:     &connSubtype,
		Values:      &connValues,
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}
	if err := connectorRepo.Create(ctx, connector); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	connectorID := connector.ID

	prompt := "Helpful assistant. Long enough to satisfy the prompt-length validator and keep platform conventions happy."

	regularDesc := "with connector"
	regular := &Agent{
		Name: "list-by-conn-regular", Description: &regularDesc, SystemPrompt: &prompt,
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := repo.Create(ctx, regular, nil, []string{"text"}, []string{"text"}, []string{connectorID}, nil); err != nil {
		t.Fatalf("create regular: %v", err)
	}

	got, total, err := repo.ListByConnectorID(ctx, connectorID, 1, 50)
	if err != nil {
		t.Fatalf("ListByConnectorID: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(got) != 1 || got[0].Agent.Name != "list-by-conn-regular" {
		t.Errorf("got = %v, want one entry", got)
	}
}
