// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestConnectorConfigFingerprints_ChangeOnConnectorEdit(t *testing.T) {
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)
	ctx := t.Context()

	vals := `{"operations":[{"tool_name":"a"}]}`
	desc := "connector for fingerprint test"
	sub := "solace"
	conn := &store.Connector{Name: "fp-conn", Description: &desc, Type: "event_mesh", Subtype: &sub, Values: &vals, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, conn); err != nil {
		t.Fatalf("create connector: %v", err)
	}

	stored, err := repo.GetByID(ctx, conn.ID)
	if err != nil {
		t.Fatalf("get connector: %v", err)
	}
	fp1 := connectorConfigFingerprints(ctx, repo, []string{conn.ID})
	want := []string{fmt.Sprintf("%s:%d", conn.ID, stored.UpdatedTime)}
	if !slices.Equal(fp1, want) {
		t.Fatalf("fingerprint = %v, want %v", fp1, want)
	}

	bumped := stored.UpdatedTime + 1000
	if _, err := db.ExecContext(ctx, `UPDATE connectors SET updated_time = ? WHERE id = ?`, bumped, conn.ID); err != nil {
		t.Fatalf("bump updated_time: %v", err)
	}

	fp2 := connectorConfigFingerprints(ctx, repo, []string{conn.ID})
	if slices.Equal(fp1, fp2) {
		t.Fatalf("fingerprint unchanged after connector edit: still %v", fp2)
	}

	agent := &store.Agent{Name: "agent"}
	if computeAgentConfigHash(agent, nil, nil, nil, fp1, nil) == computeAgentConfigHash(agent, nil, nil, nil, fp2, nil) {
		t.Fatal("agent config hash unchanged after connector edit — agent would not be flagged out_of_sync")
	}
}

func TestConnectorConfigFingerprints_EdgeCases(t *testing.T) {
	ctx := context.Background()
	if got := connectorConfigFingerprints(ctx, nil, []string{"c1"}); len(got) != 0 {
		t.Errorf("nil repo should yield no fingerprints, got %v", got)
	}

	repo := store.NewConnectorRepo(testOpenDB(t))
	if got := connectorConfigFingerprints(ctx, repo, nil); len(got) != 0 {
		t.Errorf("empty ids should yield no fingerprints, got %v", got)
	}
	if got := connectorConfigFingerprints(ctx, repo, []string{"does-not-exist"}); !slices.Equal(got, []string{"does-not-exist:0"}) {
		t.Errorf("missing connector fingerprint = %v, want [does-not-exist:0]", got)
	}
}

func TestComputeAgentConfigHash_Deterministic(t *testing.T) {
	t.Parallel()
	agent := &store.Agent{Name: "test-agent", Description: strPtr("desc"), SystemPrompt: strPtr("prompt")}
	h1 := computeAgentConfigHash(agent, []string{"a", "b"}, []string{"text"}, []string{"text"}, []string{"c1"}, nil)
	h2 := computeAgentConfigHash(agent, []string{"a", "b"}, []string{"text"}, []string{"text"}, []string{"c1"}, nil)
	if h1 != h2 {
		t.Errorf("same inputs should produce same hash: %s != %s", h1, h2)
	}
}

func TestComputeAgentConfigHash_SortOrder(t *testing.T) {
	t.Parallel()
	agent := &store.Agent{Name: "test-agent"}
	h1 := computeAgentConfigHash(agent, []string{"b", "a"}, []string{"audio", "text"}, []string{"text"}, []string{"c2", "c1"}, nil)
	h2 := computeAgentConfigHash(agent, []string{"a", "b"}, []string{"text", "audio"}, []string{"text"}, []string{"c1", "c2"}, nil)
	if h1 != h2 {
		t.Errorf("different order should produce same hash: %s != %s", h1, h2)
	}
}

func TestComputeAgentConfigHash_FieldChanges(t *testing.T) {
	t.Parallel()
	baseline := &store.Agent{Name: "agent", Description: strPtr("desc"), SystemPrompt: strPtr("prompt")}
	baseHash := computeAgentConfigHash(baseline, []string{"tools"}, []string{"text"}, []string{"text"}, []string{"c1"}, nil)

	tests := []struct {
		name     string
		agent    *store.Agent
		toolsets []string
		input    []string
		output   []string
		connIDs  []string
		wantDiff bool
	}{
		{
			name:     "change name",
			agent:    &store.Agent{Name: "other", Description: strPtr("desc"), SystemPrompt: strPtr("prompt")},
			toolsets: []string{"tools"}, input: []string{"text"}, output: []string{"text"}, connIDs: []string{"c1"},
			wantDiff: true,
		},
		{
			name:     "change description",
			agent:    &store.Agent{Name: "agent", Description: strPtr("other"), SystemPrompt: strPtr("prompt")},
			toolsets: []string{"tools"}, input: []string{"text"}, output: []string{"text"}, connIDs: []string{"c1"},
			wantDiff: true,
		},
		{
			name:     "change system prompt",
			agent:    &store.Agent{Name: "agent", Description: strPtr("desc"), SystemPrompt: strPtr("other")},
			toolsets: []string{"tools"}, input: []string{"text"}, output: []string{"text"}, connIDs: []string{"c1"},
			wantDiff: true,
		},
		{
			name:     "add toolset",
			agent:    &store.Agent{Name: "agent", Description: strPtr("desc"), SystemPrompt: strPtr("prompt")},
			toolsets: []string{"tools", "extra"}, input: []string{"text"}, output: []string{"text"}, connIDs: []string{"c1"},
			wantDiff: true,
		},
		{
			name:     "add input mode",
			agent:    &store.Agent{Name: "agent", Description: strPtr("desc"), SystemPrompt: strPtr("prompt")},
			toolsets: []string{"tools"}, input: []string{"text", "audio"}, output: []string{"text"}, connIDs: []string{"c1"},
			wantDiff: true,
		},
		{
			name:     "add output mode",
			agent:    &store.Agent{Name: "agent", Description: strPtr("desc"), SystemPrompt: strPtr("prompt")},
			toolsets: []string{"tools"}, input: []string{"text"}, output: []string{"text", "audio"}, connIDs: []string{"c1"},
			wantDiff: true,
		},
		{
			name:     "add connector",
			agent:    &store.Agent{Name: "agent", Description: strPtr("desc"), SystemPrompt: strPtr("prompt")},
			toolsets: []string{"tools"}, input: []string{"text"}, output: []string{"text"}, connIDs: []string{"c1", "c2"},
			wantDiff: true,
		},
		{
			name:     "change skills",
			agent:    &store.Agent{Name: "agent", Description: strPtr("desc"), SystemPrompt: strPtr("prompt"), Skills: strPtr(`["newskill"]`)},
			toolsets: []string{"tools"}, input: []string{"text"}, output: []string{"text"}, connIDs: []string{"c1"},
			wantDiff: true,
		},
		{
			name:     "change skill refs",
			agent:    &store.Agent{Name: "agent", Description: strPtr("desc"), SystemPrompt: strPtr("prompt"), SkillRefs: strPtr(`["ref1"]`)},
			toolsets: []string{"tools"}, input: []string{"text"}, output: []string{"text"}, connIDs: []string{"c1"},
			wantDiff: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := computeAgentConfigHash(tt.agent, tt.toolsets, tt.input, tt.output, tt.connIDs, nil)
			if tt.wantDiff && h == baseHash {
				t.Error("expected different hash from baseline")
			}
			if !tt.wantDiff && h != baseHash {
				t.Error("expected same hash as baseline")
			}
		})
	}
}

func TestComputeAgentConfigHash_NilFields(t *testing.T) {
	t.Parallel()
	agent1 := &store.Agent{Name: "agent"}
	agent2 := &store.Agent{Name: "agent", Description: nil, SystemPrompt: nil, Skills: nil, SkillRefs: nil}
	h1 := computeAgentConfigHash(agent1, nil, nil, nil, nil, nil)
	h2 := computeAgentConfigHash(agent2, nil, nil, nil, nil, nil)
	if h1 != h2 {
		t.Errorf("nil fields should produce same hash: %s != %s", h1, h2)
	}

	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64 (SHA-256 hex)", len(h1))
	}
}

func TestValidateDeploymentStateTransition_Deploy(t *testing.T) {
	t.Parallel()
	for _, status := range []string{DisplayStatusNotDeployed, DisplayStatusDeployed, DisplayStatusDeployFailed, "anything"} {
		if err := validateDeploymentStateTransition(DeployActionDeploy, status); err != nil {
			t.Errorf("deploy from %q should always succeed, got: %v", status, err)
		}
	}
}

func TestValidateDeploymentStateTransition_Update(t *testing.T) {
	t.Parallel()
	if err := validateDeploymentStateTransition(DeployActionUpdate, DisplayStatusDeployed); err != nil {
		t.Errorf("update from deployed should succeed: %v", err)
	}
	if err := validateDeploymentStateTransition(DeployActionUpdate, DisplayStatusDeployFailed); err != nil {
		t.Errorf("update from deploy_failed should succeed: %v", err)
	}
	err := validateDeploymentStateTransition(DeployActionUpdate, DisplayStatusNotDeployed)
	if err == nil {
		t.Error("update from not_deployed should fail")
	}
}

func TestValidateDeploymentStateTransition_Undeploy(t *testing.T) {
	t.Parallel()
	for _, status := range []string{DisplayStatusNotDeployed, DisplayStatusDeployed, DisplayStatusDeployFailed, "anything"} {
		if err := validateDeploymentStateTransition(DeployActionUndeploy, status); err != nil {
			t.Errorf("undeploy from %q should always succeed, got: %v", status, err)
		}
	}
}

func TestValidateDeploymentStateTransition_ErrorMessages(t *testing.T) {
	t.Parallel()
	err := validateDeploymentStateTransition(DeployActionUpdate, DisplayStatusNotDeployed)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !containsSubstr(msg, "not_deployed") {
		t.Errorf("error should mention current status, got: %s", msg)
	}
	if !containsSubstr(msg, "deployed or deploy_failed") {
		t.Errorf("error should mention valid states, got: %s", msg)
	}
}

func containsSubstr(s, sub string) bool {
	for i := range len(s) - len(sub) + 1 {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestAgentService_BuildResponse_InstanceNameAndRuntimeStatus(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	registry := platpkg.NewAgentRegistry()

	svc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, nil)

	desc := "agent used for unit testing purposes only"
	prompt := "You are a helpful assistant that always answers politely and accurately to the best of your ability."
	agent := &store.Agent{
		Name:         "Web Search",
		Type:         "standard",
		Description:  &desc,
		SystemPrompt: &prompt,
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := agentRepo.Create(t.Context(), agent, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	wantInstance := AgentInstanceName(agent)
	expectInstance := "agent_" + strings.ReplaceAll(agent.ID, "-", "_")
	if wantInstance != expectInstance {
		t.Fatalf("AgentInstanceName = %q, want %q (UUID-derived)", wantInstance, expectInstance)
	}

	t.Run("instance_name_populated", func(t *testing.T) {
		resp, err := svc.GetByID(t.Context(), agent.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if resp.InstanceName != wantInstance {
			t.Errorf("InstanceName = %q, want %q", resp.InstanceName, wantInstance)
		}
		if resp.Name != "Web Search" {
			t.Errorf("Name = %q, want the display name", resp.Name)
		}
	})

	t.Run("registry_lookup_uses_instance_name", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{Name: wantInstance, Description: "live"})

		resp, err := svc.GetByID(t.Context(), agent.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if resp.RuntimeStatus.Status() != RuntimeStatusRunning {
			t.Errorf("RuntimeStatus = %q, want %q (registry has card under instance name)", resp.RuntimeStatus.Status(), RuntimeStatusRunning)
		}
	})

	t.Run("registry_card_with_active_problems_reports_degraded", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{
			Name: wantInstance,
			Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
				a2a.HealthProblemsExtension([]health.Problem{{ID: "queue:x", Severity: health.Degraded, Detail: "temporary receive error; retrying automatically"}}),
			}},
		})

		resp, err := svc.GetByID(t.Context(), agent.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if resp.RuntimeStatus.Status() != RuntimeStatusDegraded {
			t.Errorf("RuntimeStatus.Status() = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusDegraded)
		}
		if len(resp.RuntimeStatus.Problems()) != 1 || resp.RuntimeStatus.Problems()[0].ID != "queue:x" {
			t.Errorf("RuntimeStatus.Problems() = %v, want one problem with id queue:x", resp.RuntimeStatus.Problems())
		}
	})

	t.Run("registry_miss_does_not_leak_old_key", func(t *testing.T) {
		registry.Clear()
		legacyKey := "agent_" + agent.ID
		registry.AddOrUpdate(a2a.AgentCard{Name: legacyKey, Description: "ghost"})

		resp, err := svc.GetByID(t.Context(), agent.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if resp.RuntimeStatus.Status() == "running" {
			t.Errorf("RuntimeStatus = %q, want not running (legacy key must not match)", resp.RuntimeStatus.Status())
		}
	})
}

func TestAgentService_BuildResponse_DisconnectedRuntime(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	deployRepo := store.NewAgentDeploymentRepo(db)
	registry := platpkg.NewAgentRegistry()
	svc := NewAgentService(agentRepo, deployRepo, nil, nil, registry, nil, nil)

	desc := "agent for disconnected runtime test"
	prompt := "You are an assistant."
	agent := &store.Agent{
		Name:         "Lonely Agent",
		Type:         "standard",
		Description:  &desc,
		SystemPrompt: &prompt,
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := agentRepo.Create(t.Context(), agent, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	hash := "h"
	snap := `{"name":"Lonely Agent"}`
	dep := &store.AgentDeployment{
		AgentID:             agent.ID,
		Status:              DeployStatusSuccess,
		Action:              DeployActionDeploy,
		ConfigHash:          &hash,
		AgentConfigSnapshot: &snap,
		CreatedBy:           "u",
		UpdatedTime:         time.Now().Add(-10 * runtimeGracePeriod).UnixMilli(),
	}
	if err := deployRepo.Create(t.Context(), dep); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	registry.Clear()

	resp, err := svc.GetByID(t.Context(), agent.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resp.DeploymentStatus != DisplayStatusDeployed {
		t.Fatalf("precondition: DeploymentStatus = %q, want deployed", resp.DeploymentStatus)
	}
	if resp.RuntimeStatus.Status() != RuntimeStatusDisconnected {
		t.Errorf("RuntimeStatus = %q, want %q (deployed past grace period + registry empty)",
			resp.RuntimeStatus.Status(), RuntimeStatusDisconnected)
	}
}

func TestAgentService_BuildResponse_StartingDuringGrace(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	deployRepo := store.NewAgentDeploymentRepo(db)
	registry := platpkg.NewAgentRegistry()
	svc := NewAgentService(agentRepo, deployRepo, nil, nil, registry, nil, nil)

	desc := "agent for grace-period test"
	prompt := "You are an assistant."
	agent := &store.Agent{
		Name:         "Booting Agent",
		Type:         "standard",
		Description:  &desc,
		SystemPrompt: &prompt,
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := agentRepo.Create(t.Context(), agent, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	hash := "h"
	snap := `{"name":"Booting Agent"}`
	dep := &store.AgentDeployment{
		AgentID:             agent.ID,
		Status:              DeployStatusSuccess,
		Action:              DeployActionDeploy,
		ConfigHash:          &hash,
		AgentConfigSnapshot: &snap,
		CreatedBy:           "u",
		UpdatedTime:         time.Now().UnixMilli(),
	}
	if err := deployRepo.Create(t.Context(), dep); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	registry.Clear()

	resp, err := svc.GetByID(t.Context(), agent.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resp.RuntimeStatus.Status() != RuntimeStatusStarting {
		t.Errorf("RuntimeStatus = %q, want %q (deployed within grace period)",
			resp.RuntimeStatus.Status(), RuntimeStatusStarting)
	}
}

func TestAgentService_DeployedConnectorSnapshots(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := context.Background()
	agentRepo := store.NewAgentRepo(db)
	deployRepo := store.NewAgentDeploymentRepo(db)
	connectorRepo := store.NewConnectorRepo(db)
	registry := platpkg.NewAgentRegistry()
	svc := NewAgentService(agentRepo, deployRepo, connectorRepo, nil, registry, nil, nil)

	subtype := "bedrock"
	vals := "{}"
	conn := &store.Connector{Name: "pet diet", Description: strPtr("pet diet kb"), Type: "knowledge_base", Subtype: &subtype, Values: &vals, CreatedBy: "u", UpdatedBy: "u"}
	if err := connectorRepo.Create(ctx, conn); err != nil {
		t.Fatalf("create connector: %v", err)
	}

	pastGrace := time.Now().Add(-10 * runtimeGracePeriod).UnixMilli()
	hash := "h"
	seedDeployedAgent := func(t *testing.T, name, dataSnapshot string) *AgentResponse {
		t.Helper()
		desc, prompt := "d", "p"
		agent := &store.Agent{Name: name, Type: "standard", Description: &desc, SystemPrompt: &prompt, CreatedBy: "u", UpdatedBy: "u"}
		if err := agentRepo.Create(ctx, agent, nil, []string{"text"}, []string{"text"}, []string{conn.ID}, nil); err != nil {
			t.Fatalf("create agent: %v", err)
		}
		dep := &store.AgentDeployment{AgentID: agent.ID, Status: DeployStatusSuccess, Action: DeployActionDeploy, ConfigHash: &hash, AgentDataSnapshot: &dataSnapshot, CreatedBy: "u", UpdatedTime: pastGrace}
		if err := deployRepo.Create(ctx, dep); err != nil {
			t.Fatalf("seed deployment: %v", err)
		}
		resp, err := svc.GetByID(ctx, agent.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if resp.DeploymentStatus != DisplayStatusDeployed {
			t.Fatalf("precondition: DeploymentStatus = %q, want deployed", resp.DeploymentStatus)
		}
		return resp
	}

	deployedSnapshots := func(t *testing.T, resp *AgentResponse) []ConnectorSnapshot {
		t.Helper()
		deployed, ok := resp.Deployed.(*DeployedAgentInfo)
		if !ok || deployed == nil {
			t.Fatalf("resp.Deployed not *DeployedAgentInfo: %T", resp.Deployed)
		}
		return deployed.ConnectorSnapshots
	}

	t.Run("legacy snapshot backfills connectorSnapshots from connector IDs", func(t *testing.T) {
		resp := seedDeployedAgent(t, "Legacy Snapshot Agent", `{"name":"Legacy Snapshot Agent","connectors":["`+conn.ID+`"]}`)
		got := deployedSnapshots(t, resp)
		if len(got) != 1 || got[0].ID != conn.ID || got[0].Name != "pet diet" || got[0].Subtype != "bedrock" {
			t.Fatalf("expected backfilled [pet diet], got %+v", got)
		}
	})

	t.Run("stored connectorSnapshots used as-is (deploy-time name preserved)", func(t *testing.T) {
		snap := `{"name":"Snapshot Agent","connectors":["` + conn.ID + `"],"connectorSnapshots":[{"id":"` + conn.ID + `","name":"pet diet (as deployed)","type":"knowledge_base","subtype":"bedrock"}]}`
		resp := seedDeployedAgent(t, "Snapshot Agent", snap)
		got := deployedSnapshots(t, resp)
		if len(got) != 1 || got[0].Name != "pet diet (as deployed)" {
			t.Fatalf("expected stored snapshot name preserved, got %+v", got)
		}
	})

	t.Run("ListPaginated backfills legacy deployed connectorSnapshots", func(t *testing.T) {
		seedDeployedAgent(t, "List Backfill Agent", `{"name":"List Backfill Agent","connectors":["`+conn.ID+`"]}`)
		page, err := svc.ListPaginated(ctx, 1, 100, "")
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		var found bool
		for i := range page.Data {
			if page.Data[i].Name != "List Backfill Agent" {
				continue
			}
			found = true
			deployed, ok := page.Data[i].Deployed.(*DeployedAgentInfo)
			if !ok || deployed == nil {
				t.Fatalf("list item Deployed not *DeployedAgentInfo: %T", page.Data[i].Deployed)
			}
			if len(deployed.ConnectorSnapshots) != 1 || deployed.ConnectorSnapshots[0].Name != "pet diet" {
				t.Fatalf("expected list-path backfill [pet diet], got %+v", deployed.ConnectorSnapshots)
			}
		}
		if !found {
			t.Fatal("List Backfill Agent not found in ListPaginated results")
		}
	})

	t.Run("since-deleted connector id is omitted, not an error", func(t *testing.T) {
		bogus := "00000000-0000-0000-0000-000000000000"
		resp := seedDeployedAgent(t, "Deleted Conn Agent", `{"name":"Deleted Conn Agent","connectors":["`+bogus+`"]}`)
		if got := deployedSnapshots(t, resp); len(got) != 0 {
			t.Fatalf("expected no connectorSnapshots for a since-deleted connector, got %+v", got)
		}
	})
}

func TestBuildResponse_DedupsMigratedSkillRefs(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	skillRepo := store.NewSkillRepo(db)
	svc := NewAgentService(agentRepo, nil, nil, nil, nil, nil, slog.Default())
	svc.WithSkillRepo(skillRepo)

	sk := &store.Skill{Name: "migrated-skill", Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "t", UpdatedBy: "t"}
	if err := skillRepo.Create(ctx, sk); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	created, err := svc.Create(ctx, CreateAgentRequest{
		Description:  "test agent for fixture purposes",
		Name:         "dedup-agent",
		SystemPrompt: "You are an assistant",
		SkillIDs:     []string{sk.ID},
		SkillRefs:    []string{"migrated-skill", "sam-docs"},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "tester")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	resp, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if want := []string{"sam-docs"}; !slices.Equal(resp.SkillRefs, want) {
		t.Errorf("SkillRefs = %v, want %v (migrated dup dropped, builtin kept)", resp.SkillRefs, want)
	}
	if want := []string{sk.ID}; !slices.Equal(resp.SkillIDs, want) {
		t.Errorf("SkillIDs = %v, want %v", resp.SkillIDs, want)
	}

	noM2M, err := svc.Create(ctx, CreateAgentRequest{
		Description:  "test agent for fixture purposes",
		Name:         "no-m2m-agent",
		SystemPrompt: "You are an assistant",
		SkillRefs:    []string{"legacy-fs-skill", "sam-docs"},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "tester")
	if err != nil {
		t.Fatalf("create no-m2m agent: %v", err)
	}
	resp2, err := svc.GetByID(ctx, noM2M.ID)
	if err != nil {
		t.Fatalf("GetByID no-m2m: %v", err)
	}
	if want := []string{"legacy-fs-skill", "sam-docs"}; !slices.Equal(resp2.SkillRefs, want) {
		t.Errorf("no-M2M SkillRefs = %v, want %v (all refs kept)", resp2.SkillRefs, want)
	}
}

func TestValidateModelProvider_RejectsPlaceholder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	repo := store.NewModelConfigRepo(testOpenDB(t))
	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mcSvc := NewModelConfigService(repo, slog.Default())
	mcSvc.SetFeatureRegistry(decisionNodesOn())

	svc := &AgentService{modelConfigSvc: mcSvc}

	if err := svc.validateModelProvider(ctx, "general"); err == nil {
		t.Fatal("expected ValidationError for placeholder 'general' alias")
	} else {
		var vErr *ValidationError
		if !errors.As(err, &vErr) {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
		if !strings.Contains(vErr.Message, "Models UI") {
			t.Errorf("error should point operators at the Models UI; got %q", vErr.Message)
		}
	}

	row, err := repo.GetByAlias(ctx, "general")
	if err != nil || row == nil {
		t.Fatalf("look up seeded general row: %v", err)
	}
	if _, err := mcSvc.Update(ctx, row.ID, &UpdateModelConfigRequest{
		Provider:   strPtr("openai"),
		ModelName:  strPtr("gpt-4o"),
		APIBase:    strPtr("https://llm.example.com/v1"),
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-real"},
	}, "tester"); err != nil {
		t.Fatalf("upgrade placeholder: %v", err)
	}
	if err := svc.validateModelProvider(ctx, "general"); err != nil {
		t.Errorf("real config should validate, got: %v", err)
	}
}

func makeReadyPackage(ctx context.Context, t *testing.T, repo *store.ToolPackageRepo, name string) *store.ToolPackage {
	t.Helper()
	pkg := &store.ToolPackage{
		Name:            name,
		DiscoveryStatus: "ready",
		CreatedBy:       "tester",
		UpdatedBy:       "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package %q: %v", name, err)
	}
	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatalf("mark package ready: %v", err)
	}
	return pkg
}

func TestToolsetConfigs_RoundTrip(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	svc := NewAgentService(agentRepo, nil, nil, toolPackageRepo, nil, nil, slog.Default())

	makeReadyPackage(ctx, t, toolPackageRepo, "my-pkg")

	created, err := svc.Create(ctx, CreateAgentRequest{
		Name:        "tc-roundtrip-agent",
		Description: "agent for toolset config round-trip testing",
		SystemPrompt: "You are a helpful assistant with access to custom tools. " +
			"Use them appropriately based on user requests and context.",
		Toolsets:    []string{"my-pkg"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "my-pkg", ConfigValues: map[string]any{"api_key": "xxx"}},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if len(got.ToolsetConfigs) != 1 {
		t.Fatalf("want 1 toolset config, got %d", len(got.ToolsetConfigs))
	}
	if got.ToolsetConfigs[0].ToolsetName != "my-pkg" {
		t.Errorf("toolset name = %q, want %q", got.ToolsetConfigs[0].ToolsetName, "my-pkg")
	}
	if v, _ := got.ToolsetConfigs[0].ConfigValues["api_key"].(string); v != "xxx" {
		t.Errorf("api_key = %q, want %q", v, "xxx")
	}
}

func TestToolsetConfigs_UpdatePreservesWhenOmitted(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	svc := NewAgentService(agentRepo, nil, nil, toolPackageRepo, nil, nil, slog.Default())

	makeReadyPackage(ctx, t, toolPackageRepo, "preserve-pkg")

	created, err := svc.Create(ctx, CreateAgentRequest{
		Name:        "tc-preserve-agent",
		Description: "agent for toolset config preserve testing",
		SystemPrompt: "You are a helpful assistant with access to custom tools. " +
			"Use them appropriately based on user requests and context.",
		Toolsets:    []string{"preserve-pkg"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "preserve-pkg", ConfigValues: map[string]any{"token": "abc"}},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, UpdateAgentRequest{
		Name:         "tc-preserve-agent",
		Description:  "agent for toolset config preserve testing",
		SystemPrompt: created.SystemPrompt,
		Toolsets:     []string{"preserve-pkg"},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "tester")
	if err != nil {
		t.Fatalf("Update (no toolsetConfigs): %v", err)
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if len(got.ToolsetConfigs) != 1 {
		t.Fatalf("want 1 toolset config after preserve-update, got %d", len(got.ToolsetConfigs))
	}
	if v, _ := got.ToolsetConfigs[0].ConfigValues["token"].(string); v != "abc" {
		t.Errorf("token = %q, want %q", v, "abc")
	}
}

func TestToolsetConfigs_UpdateReplaces(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	svc := NewAgentService(agentRepo, nil, nil, toolPackageRepo, nil, nil, slog.Default())

	makeReadyPackage(ctx, t, toolPackageRepo, "replace-pkg-a")
	makeReadyPackage(ctx, t, toolPackageRepo, "replace-pkg-b")

	created, err := svc.Create(ctx, CreateAgentRequest{
		Name:        "tc-replace-agent",
		Description: "agent for toolset config replace testing",
		SystemPrompt: "You are a helpful assistant with access to custom tools. " +
			"Use them appropriately based on user requests and context.",
		Toolsets:    []string{"replace-pkg-a", "replace-pkg-b"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "replace-pkg-a", ConfigValues: map[string]any{"key": "old"}},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, UpdateAgentRequest{
		Name:         "tc-replace-agent",
		Description:  "agent for toolset config replace testing",
		SystemPrompt: created.SystemPrompt,
		Toolsets:     []string{"replace-pkg-a", "replace-pkg-b"},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "replace-pkg-b", ConfigValues: map[string]any{"secret": "new"}},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("Update (with toolsetConfigs): %v", err)
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if len(got.ToolsetConfigs) != 1 {
		t.Fatalf("want 1 toolset config after replace-update, got %d: %+v", len(got.ToolsetConfigs), got.ToolsetConfigs)
	}
	if got.ToolsetConfigs[0].ToolsetName != "replace-pkg-b" {
		t.Errorf("toolset name = %q, want replace-pkg-b", got.ToolsetConfigs[0].ToolsetName)
	}
	if v, _ := got.ToolsetConfigs[0].ConfigValues["secret"].(string); v != "new" {
		t.Errorf("secret = %q, want %q", v, "new")
	}
}

func TestToolsetConfigs_UnboundToolset(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	svc := NewAgentService(agentRepo, nil, nil, toolPackageRepo, nil, nil, slog.Default())

	makeReadyPackage(ctx, t, toolPackageRepo, "bound-pkg")
	makeReadyPackage(ctx, t, toolPackageRepo, "unbound-pkg")

	_, err := svc.Create(ctx, CreateAgentRequest{
		Name:        "tc-unbound-agent",
		Description: "agent for unbound toolset config testing",
		SystemPrompt: "You are a helpful assistant with access to custom tools. " +
			"Use them appropriately based on user requests and context.",
		Toolsets:    []string{"bound-pkg"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "unbound-pkg", ConfigValues: map[string]any{"key": "val"}},
		},
	}, "tester")
	if err == nil {
		t.Fatal("expected error for unbound toolset, got nil")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(valErr.Message, "unbound-pkg") {
		t.Errorf("error message should mention the unbound toolset name: %q", valErr.Message)
	}
}

func TestToolsetConfigs_EmptyClears(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	svc := NewAgentService(agentRepo, nil, nil, toolPackageRepo, nil, nil, slog.Default())

	makeReadyPackage(ctx, t, toolPackageRepo, "clear-pkg")

	created, err := svc.Create(ctx, CreateAgentRequest{
		Name:        "tc-clear-agent",
		Description: "agent for toolset config clear testing",
		SystemPrompt: "You are a helpful assistant with access to custom tools. " +
			"Use them appropriately based on user requests and context.",
		Toolsets:    []string{"clear-pkg"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "clear-pkg", ConfigValues: map[string]any{"key": "val"}},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, UpdateAgentRequest{
		Name:           "tc-clear-agent",
		Description:    "agent for toolset config clear testing",
		SystemPrompt:   created.SystemPrompt,
		Toolsets:       []string{"clear-pkg"},
		InputModes:     []string{"text"},
		OutputModes:    []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{},
	}, "tester")
	if err != nil {
		t.Fatalf("Update (empty toolsetConfigs): %v", err)
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if len(got.ToolsetConfigs) != 0 {
		t.Errorf("want 0 toolset configs after clear, got %d: %+v", len(got.ToolsetConfigs), got.ToolsetConfigs)
	}
}

func TestToolsetConfigs_BuiltinRoundTrip(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	svc := NewAgentService(agentRepo, nil, nil, toolPackageRepo, nil, nil, slog.Default())

	created, err := svc.Create(ctx, CreateAgentRequest{
		Name:        "builtin-cfg-agent",
		Description: "agent for builtin per-tool config round-trip",
		SystemPrompt: "You are a helpful assistant with access to builtin tools. " +
			"Use them appropriately based on user requests and context.",
		Toolsets:    []string{"builtin_artifact_tools"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "builtin_artifact_tools", ConfigValues: map[string]any{
				"list_artifacts": map[string]any{"limit": 10},
			}},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.ToolsetConfigs) != 1 {
		t.Fatalf("want 1 toolset config, got %d", len(got.ToolsetConfigs))
	}
	binding := got.ToolsetConfigs[0]
	if binding.ToolsetName != "builtin_artifact_tools" {
		t.Errorf("toolset name = %q, want builtin_artifact_tools", binding.ToolsetName)
	}
	inner, _ := binding.ConfigValues["list_artifacts"].(map[string]any)
	if inner == nil {
		t.Fatalf("missing list_artifacts entry: %+v", binding.ConfigValues)
	}
	if v, _ := inner["limit"].(float64); v != 10 {
		t.Errorf("list_artifacts.limit = %v, want 10", inner["limit"])
	}
}

func TestToolsetConfigs_BuiltinRejectsUnknownToolName(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	svc := NewAgentService(agentRepo, nil, nil, toolPackageRepo, nil, nil, slog.Default())

	_, err := svc.Create(ctx, CreateAgentRequest{
		Name:        "builtin-cfg-bad",
		Description: "agent that authors a non-existent builtin tool name",
		SystemPrompt: "You are a helpful assistant with access to builtin tools. " +
			"Use them appropriately based on user requests and context.",
		Toolsets:    []string{"builtin_artifact_tools"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "builtin_artifact_tools", ConfigValues: map[string]any{
				"foobar": map[string]any{"k": "v"},
			}},
		},
	}, "tester")
	if err == nil {
		t.Fatal("expected ValidationError, got nil")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(valErr.Message, "foobar") {
		t.Errorf("error should name bad key foobar: %q", valErr.Message)
	}
	if !strings.Contains(valErr.Message, "builtin_artifact_tools") {
		t.Errorf("error should name the toolset: %q", valErr.Message)
	}
}

func TestListPaginated_ToolsetConfigsPresent(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	svc := NewAgentService(agentRepo, nil, nil, toolPackageRepo, nil, nil, slog.Default())

	pkg := makeReadyPackage(ctx, t, toolPackageRepo, "list-pkg")
	schema := `[{"key":"secret","type":"string","secret":true}]`
	if err := toolPackageRepo.UpsertToolFile(ctx, &store.ToolPackageFile{
		PackageID:    pkg.ID,
		ToolName:     "run",
		ConfigSchema: &schema,
	}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}

	created, err := svc.Create(ctx, CreateAgentRequest{
		Name:        "list-cfg-agent",
		Description: "agent for list toolset config testing",
		SystemPrompt: "You are a helpful assistant with access to custom tools. " +
			"Use them appropriately based on user requests and context.",
		Toolsets:    []string{"list-pkg"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
		ToolsetConfigs: []ToolsetConfigBinding{
			{ToolsetName: "list-pkg", ConfigValues: map[string]any{"run": map[string]any{"secret": "abc"}}},
		},
	}, "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	byID, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(byID.ToolsetConfigs) == 0 {
		t.Fatal("GetByID: expected toolsetConfigs to be populated")
	}

	page, err := svc.ListPaginated(ctx, 1, 20, "")
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	var found *AgentResponse
	for i := range page.Data {
		if page.Data[i].ID == created.ID {
			found = &page.Data[i]
			break
		}
	}
	if found == nil {
		t.Fatal("created agent not found in list response")
	}
	if found.ToolsetConfigs == nil {
		t.Fatal("list response should carry toolsetConfigs")
	}
	if len(found.ToolsetConfigs) != 1 {
		t.Fatalf("expected 1 toolset config binding, got %d", len(found.ToolsetConfigs))
	}
	if found.ToolsetConfigs[0].ToolsetName != "list-pkg" {
		t.Errorf("toolset name = %q, want list-pkg", found.ToolsetConfigs[0].ToolsetName)
	}
	run, _ := found.ToolsetConfigs[0].ConfigValues["run"].(map[string]any)
	if run == nil {
		t.Fatalf("expected nested config under tool name 'run', got %+v", found.ToolsetConfigs[0].ConfigValues)
	}
	if got := run["secret"]; got != "<REDACTED>" {
		t.Errorf("secret leaked in list response: got %v, want \"<REDACTED>\"", got)
	}
}

func TestAgentResponseWireShape_TypeAndSkillIDs(t *testing.T) {
	resp := AgentResponse{
		ID:       "id-1",
		Name:     "agent",
		Type:     "standard",
		SkillIDs: []string{"uuid-a"},
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, ok := asMap["type"].(string); !ok || got != "standard" {
		t.Errorf(`expected "type":"standard" on wire, got %v (full: %s)`, asMap["type"], raw)
	}
	if _, present := asMap["agentType"]; present {
		t.Errorf(`legacy "agentType" key must not be emitted, got: %s`, raw)
	}
	if _, present := asMap["skillIds"]; !present {
		t.Errorf(`expected "skillIds" key on wire, got: %s`, raw)
	}
}

func TestAgentService_BuildResponse_ToolRegistrations(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	registry := platpkg.NewAgentRegistry()
	svc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, nil)

	desc := "agent for tool-registration surface test"
	prompt := "You are an assistant."
	agent := &store.Agent{
		Name:         "Spec URL Agent",
		Type:         "standard",
		Description:  &desc,
		SystemPrompt: &prompt,
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := agentRepo.Create(t.Context(), agent, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	instance := AgentInstanceName(agent)

	t.Run("failed_registration_surfaces", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{
			Name: instance,
			Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
				URI: platpkg.ExtensionURIToolRegistrations,
				Params: map[string]any{"entries": []any{map[string]any{
					"tool_type":    "openapi",
					"connector_id": "conn-1",
					"name":         "openapi",
					"status":       "failed",
					"error":        "load spec from https://x: HTTP 500",
					"tool_count":   0,
				}}},
			}}},
		})

		resp, err := svc.GetByID(t.Context(), agent.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if len(resp.ToolRegistrations) != 1 {
			t.Fatalf("ToolRegistrations = %d, want 1", len(resp.ToolRegistrations))
		}
		reg := resp.ToolRegistrations[0]
		if reg.Status != "failed" || reg.ConnectorID != "conn-1" || reg.ToolType != "openapi" {
			t.Errorf("registration = %+v, want failed openapi entry for conn-1", reg)
		}
		if reg.Error == "" {
			t.Error("failed registration must carry the error summary")
		}
	})

	t.Run("ready_registration_surfaces_tool_names", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{
			Name: instance,
			Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
				URI: platpkg.ExtensionURIToolRegistrations,
				Params: map[string]any{"entries": []any{map[string]any{
					"tool_type":    "openapi",
					"connector_id": "conn-1",
					"name":         "openapi",
					"status":       "ready",
					"tool_count":   2,
					"tool_names":   []any{"whoami", "list_my_expenses"},
				}}},
			}}},
		})

		resp, err := svc.GetByID(t.Context(), agent.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if len(resp.ToolRegistrations) != 1 {
			t.Fatalf("ToolRegistrations = %d, want 1", len(resp.ToolRegistrations))
		}
		reg := resp.ToolRegistrations[0]
		if reg.Status != "ready" || reg.ToolCount != 2 {
			t.Errorf("registration = %+v, want ready entry with toolCount 2", reg)
		}
		if !reflect.DeepEqual(reg.ToolNames, []string{"whoami", "list_my_expenses"}) {
			t.Errorf("ToolNames = %v, want [whoami list_my_expenses] surfaced through GetByID", reg.ToolNames)
		}
	})

	t.Run("card_without_extension_reports_none", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{Name: instance, Description: "python agent"})

		resp, err := svc.GetByID(t.Context(), agent.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if resp.RuntimeStatus.Status() != RuntimeStatusRunning {
			t.Errorf("RuntimeStatus = %q, want running", resp.RuntimeStatus.Status())
		}
		if resp.ToolRegistrations != nil {
			t.Errorf("ToolRegistrations = %v, want absent for cards without the extension", resp.ToolRegistrations)
		}
	})
}
