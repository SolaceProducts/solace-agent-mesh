// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const surfaceHashTestPrompt = "You are a test agent. This system prompt is long enough to satisfy the validator."

func assertRowSurfaceHashes(t *testing.T, label string, row *store.AgentDeployment, want AgentSurfaceHashes) {
	t.Helper()
	if row == nil {
		t.Fatalf("%s: deployment row not found", label)
	}
	if got := agentSurfaceHashesOf(row); got != want {
		t.Errorf("%s: surface hashes\n got %+v\nwant %+v", label, got, want)
	}
}

func expectedSurfaceHashesFromSnapshot(t *testing.T, row *store.AgentDeployment, agentName, description string) AgentSurfaceHashes {
	t.Helper()
	if row.AgentConfigSnapshot == nil {
		t.Fatal("deployment row carries no config snapshot to derive the expected hashes from")
	}
	payloads, err := (&StandardAgentDeployer{}).ReplayFromSnapshot(*row.AgentConfigSnapshot)
	if err != nil {
		t.Fatalf("replay snapshot: %v", err)
	}
	want, err := computeAgentSurfaceHashes(t.Context(), payloads, &agentCardSurface{Name: agentName, Description: description}, skillContentDigests(nil))
	if err != nil {
		t.Fatalf("compute expected hashes: %v", err)
	}
	return want
}

func latestAgentDeployment(t *testing.T, h *e2eHarness, agentID string) *store.AgentDeployment {
	t.Helper()
	row, err := h.deployRepo.GetLatestByAgentID(h.ctx, agentID)
	if err != nil {
		t.Fatalf("latest deployment: %v", err)
	}
	if row == nil {
		t.Fatal("no deployment row")
	}
	return row
}

func updateAgentForSurfaceTest(t *testing.T, h *e2eHarness, agentID, name, description, prompt string) {
	t.Helper()
	if _, err := h.agentSvc.Update(h.ctx, agentID, UpdateAgentRequest{
		Name:         name,
		Description:  description,
		SystemPrompt: prompt,
		Toolsets:     []string{},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
	}, "e2e-user"); err != nil {
		t.Fatalf("update agent: %v", err)
	}
}

func redeployAgent(t *testing.T, h *e2eHarness, agentID string) {
	t.Helper()
	resp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{AgentID: agentID, Action: DeployActionUpdate}, "e2e-user")
	if err != nil {
		t.Fatalf("update deployment: %v", err)
	}
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("update deployment status = %q (%s)", resp.Status, resp.ErrorMessage)
	}
}

func TestB03_DeployAndUpdatePersistSurfaceHashes(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	const name, description = "surface_hash_agent", "E2E test agent"
	agentID := h.createAgent(name, surfaceHashTestPrompt)
	h.deploy(agentID)

	deployed := latestAgentDeployment(t, h, agentID)
	want := expectedSurfaceHashesFromSnapshot(t, deployed, name, description)
	assertAllSurfacesKnown(t, want)
	assertRowSurfaceHashes(t, "B03 successful deploy", deployed, want)

	updateAgentForSurfaceTest(t, h, agentID, name, description, surfaceHashTestPrompt+" Be terse.")
	redeployAgent(t, h, agentID)
	promptUpdate := latestAgentDeployment(t, h, agentID)
	if promptUpdate.Action != DeployActionUpdate {
		t.Fatalf("latest action = %q, want the update row", promptUpdate.Action)
	}
	afterPrompt := agentSurfaceHashesOf(promptUpdate)
	assertAllSurfacesKnown(t, afterPrompt)
	assertChangedSurfaces(t, want, afterPrompt, "prompt", "behaviour")

	updateAgentForSurfaceTest(t, h, agentID, name, "E2E test agent with a new public description", surfaceHashTestPrompt+" Be terse.")
	redeployAgent(t, h, agentID)
	assertChangedSurfaces(t, afterPrompt, agentSurfaceHashesOf(latestAgentDeployment(t, h, agentID)), "card")
}

func TestB03_StandardDeployerBuildResultCarriesSurfaceHashes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := testOpenDB(t)
	logger := slog.Default()
	skillRepo := store.NewSkillRepo(db)
	deployer := NewStandardAgentDeployer(NewYAMLService("test-ns", logger), store.NewConnectorRepo(db), nil, skillRepo, logger)

	digested := &store.Skill{Name: "digested-skill", Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	if err := skillRepo.Create(ctx, digested); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE skills SET content_hash = ? WHERE id = ?`, "zip-sha-1", digested.ID); err != nil {
		t.Fatalf("stamp content hash: %v", err)
	}

	agent := &store.Agent{ID: "11111111-2222-3333-4444-555555555555", Name: "builder", Description: new("public description"), SystemPrompt: new(surfaceHashTestPrompt)}
	build := func(t *testing.T, agent *store.Agent, toolsets []string, skillNames []string) AgentSurfaceHashes {
		t.Helper()
		result, err := deployer.BuildPayloads(ctx, DeployInput{
			Agent: agent, Action: DeployActionDeploy, Toolsets: toolsets,
			InputModes: []string{"text"}, OutputModes: []string{"text"},
			Opts: DeployOptions{SkillNames: skillNames, DeploymentID: time.Now().String()},
		})
		if err != nil {
			t.Fatalf("BuildPayloads: %v", err)
		}
		return result.SurfaceHashes
	}

	base := build(t, agent, []string{"artifact_management"}, []string{"digested-skill"})
	assertAllSurfacesKnown(t, base)

	t.Run("B03_toolset_change_moves_toolset_and_behaviour_only", func(t *testing.T) {
		assertChangedSurfaces(t, base, build(t, agent, []string{"artifact_management", "web"}, []string{"digested-skill"}), "toolset", "behaviour")
	})

	t.Run("B03_skill_content_change_moves_capability_and_behaviour_only", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `UPDATE skills SET content_hash = ? WHERE id = ?`, "zip-sha-2", digested.ID); err != nil {
			t.Fatalf("restamp content hash: %v", err)
		}
		t.Cleanup(func() {
			_, _ = db.ExecContext(context.Background(), `UPDATE skills SET content_hash = ? WHERE id = ?`, "zip-sha-1", digested.ID)
		})
		assertChangedSurfaces(t, base, build(t, agent, []string{"artifact_management"}, []string{"digested-skill"}), "capability", "behaviour")
	})

	t.Run("B03_legacy_skill_ref_without_digest_leaves_capability_and_behaviour_unknown", func(t *testing.T) {
		got := build(t, agent, []string{"artifact_management"}, []string{"digested-skill", "legacy-filesystem-skill"})
		if got.Capability != "" || got.Behaviour != "" {
			t.Errorf("capability=%q behaviour=%q, want both unknown", got.Capability, got.Behaviour)
		}
		if got.Prompt != base.Prompt || got.Toolset != base.Toolset || got.Card != base.Card {
			t.Errorf("known surfaces moved: %+v vs %+v", got, base)
		}
	})

	t.Run("B03_deployment_id_does_not_move_hashes", func(t *testing.T) {
		assertChangedSurfaces(t, base, build(t, agent, []string{"artifact_management"}, []string{"digested-skill"}))
	})

	for name, prompt := range map[string]string{
		"plain":           surfaceHashTestPrompt,
		"leading_newline": "\n" + surfaceHashTestPrompt,
		"only_newlines":   "\n\n" + surfaceHashTestPrompt + "\n",
	} {
		t.Run("B03_snapshot_replay_reproduces_build_hashes/"+name, func(t *testing.T) {
			withPrompt := *agent
			withPrompt.SystemPrompt = new(prompt)
			result, err := deployer.BuildPayloads(ctx, DeployInput{
				Agent: &withPrompt, Action: DeployActionDeploy, Toolsets: []string{"artifact_management"},
				InputModes: []string{"text"}, OutputModes: []string{"text"},
				Opts: DeployOptions{SkillNames: []string{"digested-skill"}},
			})
			if err != nil {
				t.Fatalf("BuildPayloads: %v", err)
			}
			assertAllSurfacesKnown(t, result.SurfaceHashes)
			payloads, err := deployer.ReplayFromSnapshot(result.SnapshotYAML)
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			replayed, err := computeAgentSurfaceHashes(ctx, payloads, &agentCardSurface{Name: agent.Name, Description: *agent.Description}, skillContentDigests(skillRepo))
			if err != nil {
				t.Fatalf("hash replay: %v", err)
			}
			if replayed != result.SurfaceHashes {
				t.Errorf("replayed snapshot hashes differ from build hashes:\n got %+v\nwant %+v", replayed, result.SurfaceHashes)
			}
		})
	}

	t.Run("B03_tab_led_multiline_instruction_keeps_the_deploy_and_only_the_card", func(t *testing.T) {
		tabLed := *agent
		tabLed.SystemPrompt = new("\tYou are a test agent.\nThis system prompt spans two lines.")
		result, err := deployer.BuildPayloads(ctx, DeployInput{
			Agent: &tabLed, Action: DeployActionDeploy, Toolsets: []string{"artifact_management"},
			InputModes: []string{"text"}, OutputModes: []string{"text"},
		})
		if err != nil {
			t.Fatalf("BuildPayloads failed over its surface hashes: %v", err)
		}
		if _, replayErr := deployer.ReplayFromSnapshot(result.SnapshotYAML); replayErr == nil {
			t.Fatal("the snapshot now replays a tab-led multi-line instruction; pick another unreplayable config")
		}
		if want := (AgentSurfaceHashes{Card: base.Card}); result.SurfaceHashes != want {
			t.Errorf("hashes = %+v, want only the card %+v", result.SurfaceHashes, want)
		}
	})

	t.Run("B03_unreplayable_snapshot_leaves_only_the_card", func(t *testing.T) {
		card := &agentCardSurface{Name: agent.Name, Description: *agent.Description}
		got := deployer.snapshotSurfaceHashes(ctx, agent.Name, "apps: [unterminated", card)
		if want := (AgentSurfaceHashes{Card: base.Card}); got != want {
			t.Errorf("hashes = %+v, want only the card %+v", got, want)
		}
	})

	t.Run("B03_skill_lookup_failure_keeps_the_deploy", func(t *testing.T) {
		closedDB := testOpenDB(t)
		closedRepo := store.NewSkillRepo(closedDB)
		if err := closedDB.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
		broken := NewStandardAgentDeployer(NewYAMLService("test-ns", logger), store.NewConnectorRepo(db), nil, closedRepo, logger)
		result, err := broken.BuildPayloads(ctx, DeployInput{
			Agent: agent, Action: DeployActionDeploy, Toolsets: []string{"artifact_management"},
			InputModes: []string{"text"}, OutputModes: []string{"text"},
			Opts: DeployOptions{SkillNames: []string{"digested-skill"}},
		})
		if err != nil {
			t.Fatalf("BuildPayloads failed over a skill digest lookup: %v", err)
		}
		want := AgentSurfaceHashes{Prompt: base.Prompt, Toolset: base.Toolset, Card: base.Card}
		if result.SurfaceHashes != want {
			t.Errorf("hashes = %+v, want capability and behaviour unknown and the rest known %+v", result.SurfaceHashes, want)
		}
	})
}

func TestB03_A2AProxyDeployerHashesOnlyThePublicCard(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	a2aRepo := store.NewA2AAgentRepo(testOpenDB(t))
	authType, authConfig := "bearer", `{"token":"remote-secret"}`
	a2a := &store.A2AAgent{
		AgentURL: "https://example.com/agent", Name: "Web Search", Description: new("Searches the web"),
		AgentCardLocation: "none", AgentAuthType: &authType, AgentAuthConfig: &authConfig,
		RequestTimeoutSeconds: 90, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := a2aRepo.Create(ctx, a2a); err != nil {
		t.Fatalf("seed a2a row: %v", err)
	}
	result, err := NewA2AProxyDeployer(a2aRepo, "test-ns").BuildPayloads(ctx, DeployInput{
		Agent: &store.Agent{ID: a2a.ID, Name: a2a.Name, Type: AgentTypeA2AProxy}, Action: DeployActionDeploy,
	})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}
	want := AgentSurfaceHashes{Card: sha256Hex(`{"description":"Searches the web","name":"Web Search","v":1}`)}
	if result.SurfaceHashes != want {
		t.Errorf("A2A proxy hashes = %+v, want only the card of its name and description %+v", result.SurfaceHashes, want)
	}
}

func unreachableControlClient(t *testing.T) *control.Client {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return control.NewClient(brk, "test-ns", control.WithTimeout(100*time.Millisecond))
}

func TestB03_FailedPostRowKeepsItsBuiltSurfaceHashes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := testOpenDB(t)
	logger := slog.Default()
	agentRepo := store.NewAgentRepo(db)
	connectorRepo := store.NewConnectorRepo(db)
	deploymentRepo := store.NewDeploymentRepo(db)
	yamlSvc := NewYAMLService("test-ns", logger)
	registry := NewDefaultDeployerRegistry(yamlSvc, connectorRepo, nil, nil, nil, "test-ns", logger)
	deploySvc := NewDeploymentService(deploymentRepo, agentRepo, connectorRepo, nil, yamlSvc, unreachableControlClient(t), registry, logger)
	agentSvc := NewAgentService(agentRepo, store.NewAgentDeploymentRepo(db), connectorRepo, nil, nil, nil, logger)

	created, err := agentSvc.Create(ctx, CreateAgentRequest{
		Name: "failed_deploy_agent", Description: "agent whose control-plane POST fails",
		SystemPrompt: surfaceHashTestPrompt, InputModes: []string{"text"}, OutputModes: []string{"text"},
	}, "test-user")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	resp, err := deploySvc.Create(ctx, CreateAgentDeploymentRequest{AgentID: created.ID, Action: DeployActionDeploy}, "test-user")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want failed", resp.Status)
	}
	row, err := deploymentRepo.GetByID(ctx, resp.ID)
	if err != nil {
		t.Fatalf("load failed row: %v", err)
	}
	if row.AgentConfigSnapshot == nil {
		t.Fatal("failed row has no snapshot, so the build never ran and this test proves nothing about the hashes")
	}
	want := expectedSurfaceHashesFromSnapshot(t, row, "failed_deploy_agent", "agent whose control-plane POST fails")
	assertAllSurfacesKnown(t, want)
	assertRowSurfaceHashes(t, "B03 failed POST row", row, want)
}
