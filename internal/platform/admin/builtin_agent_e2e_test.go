// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/google/go-cmp/cmp"
)

func newTestReconciler(h *e2eHarness, deploySvc *DeploymentService) *Reconciler {
	rec := NewReconciler(
		h.agentRepo, h.deployRepo, h.connectorRepo,
		h.workflowRepo, h.gatewayRepo, h.gwDeployRepo,
		h.yamlSvc, nil, h.controlClient, h.brk, h.namespace, slog.Default(),
	)
	rec.SetDeployerRegistry(NewDefaultDeployerRegistry(h.yamlSvc, h.connectorRepo, h.toolPackageRepo, nil, h.a2aRepo, h.namespace, slog.Default()))
	rec.SetDeploymentService(deploySvc)
	return rec
}

func askForAgents(t *testing.T, h *e2eHarness) control.AgentsReply {
	t.Helper()
	var reply control.AgentsReply
	decodeAWEAnswer(t, askForAgentsAnswer(t, h), &reply)
	return reply
}

func askForAgentsAnswer(t *testing.T, h *e2eHarness) []byte {
	t.Helper()
	topic := a2a.AWEAgentsRequestTopic(h.namespace)
	resp, err := h.brk.Request(h.ctx, topic, &broker.Message{
		ReplyTo:        topic + "/reply/test",
		UserProperties: map[string]string{discovery.ProcessIDProperty: "test-awe"},
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("no answer to the AWE's request: %v", err)
	}
	return resp.Payload
}

func decodeAWEAnswer(t *testing.T, payload []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(payload, into); err != nil {
		t.Fatalf("decode the answer %q: %v", payload, err)
	}
}

func listedAgent(reply control.AgentsReply, instance string) *control.AgentApply {
	for i := range reply.Agents {
		if reply.Agents[i].Name == instance {
			return &reply.Agents[i]
		}
	}
	return nil
}

func TestE2E_AWEAnswerListsTheDeployedConfigOfEachAgent(t *testing.T) {
	h := setupE2E(t, []*llm.ChatResponse{{Content: "ok"}})
	agentID := h.createAgent("asked-agent", "be brief")
	instance := h.agentInstanceName(agentID)
	if got := listedAgent(askForAgents(t, h), instance); got != nil {
		t.Fatalf("an agent never deployed is listed: %+v", got)
	}

	deployed := h.deploy(agentID)
	got := listedAgent(askForAgents(t, h), instance)
	if got == nil || got.DeploymentID != deployed.ID || got.Undeploy || len(got.Payloads) == 0 {
		t.Fatalf("deployed agent listed as %+v, want deployment %s with its config", got, deployed.ID)
	}
	if name, _ := got.Payloads[0]["name"].(string); name != instance {
		t.Fatalf("listed config starts %q, want %q", name, instance)
	}

	h.undeploy(agentID)
	if got := listedAgent(askForAgents(t, h), instance); got == nil || !got.Undeploy {
		t.Fatalf("undeployed agent listed as %+v, want an undeploy", got)
	}
}

func TestE2E_AnAWEOfThePreviousReleaseKeepsAskingWhenThisGWEAnswers(t *testing.T) {
	h := setupE2E(t, []*llm.ChatResponse{{Content: "ok"}})
	h.deploy(h.createAgent("rollback-agent", "be brief"))
	var previousRelease struct {
		Done bool `json:"done"`
	}
	decodeAWEAnswer(t, askForAgentsAnswer(t, h), &previousRelease)
	if previousRelease.Done {
		t.Fatal("an AWE of the previous release reads this answer as done and stops asking, with no agent started")
	}
}

func TestE2E_AWEStartsAgainAnAgentStoppedOutsideThePlatform(t *testing.T) {
	h := setupE2E(t, []*llm.ChatResponse{{Content: "ok"}})
	agentID := h.createAgent("restarted-agent", "be brief")
	h.deploy(agentID)
	instance := h.agentInstanceName(agentID)
	h.waitForInstance(instance, 5*time.Second)

	if _, err := h.controlClient.Custom(h.ctx, "awe", "delete", instancesPathPrefix+instance, deleteKeepQueuesBody); err != nil {
		t.Fatalf("stop the agent: %v", err)
	}
	h.nudgeAWEs()
	h.waitForInstance(instance, 5*time.Second)
}

func TestE2E_BuiltinAgentsFollowTheReleaseConfig(t *testing.T) {
	h := setupE2E(t, []*llm.ChatResponse{{Content: "ok"}})
	if err := SeedBuiltinAgents(h.ctx, h.agentSvc, h.agentRepo, false, false, slog.Default()); err != nil {
		t.Fatalf("SeedBuiltinAgents: %v", err)
	}
	orch, _, _, _, _, err := h.agentRepo.GetByName(h.ctx, builtinOrchestratorName)
	if err != nil || orch == nil {
		t.Fatalf("seeded Orchestrator missing: %v", err)
	}
	rec := newTestReconciler(h, h.deploySvc)

	rec.deployBuiltinsOnce(h.ctx)
	first := waitForSuccessfulAgentDeploy(t, h, orch.ID, "")
	if first == "" {
		t.Fatal("first deploy never succeeded")
	}
	rec.deployBuiltinsOnce(h.ctx)
	if d, _ := h.deployRepo.GetLatestByAgentID(h.ctx, orch.ID); d == nil || d.ID != first {
		t.Fatalf("an unchanged built-in was redeployed: first=%s latest=%s", first, depID(d))
	}

	orchReq, err := buildBuiltinOrchestratorRequest()
	if err != nil {
		t.Fatal(err)
	}
	orchReq.SystemPrompt = "release-two-orchestrator-prompt"
	if _, err := h.agentSvc.Update(h.ctx, orch.ID, builtinUpdateRequest(orchReq), builtinAgentActor); err != nil {
		t.Fatalf("seed update: %v", err)
	}
	rec.deployBuiltinsOnce(h.ctx)
	second := waitForSuccessfulAgentDeploy(t, h, orch.ID, first)
	if second == "" {
		t.Fatal("a release config change was not deployed")
	}

	orchReq.SystemPrompt = "operator-prompt"
	if _, err := h.agentSvc.Update(h.ctx, orch.ID, builtinUpdateRequest(orchReq), "operator@example.com"); err != nil {
		t.Fatalf("operator update: %v", err)
	}
	rec.deployBuiltinsOnce(h.ctx)
	if d, _ := h.deployRepo.GetLatestByAgentID(h.ctx, orch.ID); d == nil || d.ID != second {
		t.Fatalf("an operator's customization was deployed: want %s got %s", second, depID(d))
	}
}

func waitForSuccessfulAgentDeploy(t *testing.T, h *e2eHarness, agentID, prev string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d, _ := h.deployRepo.GetLatestByAgentID(h.ctx, agentID); d != nil && d.Status == DeployStatusSuccess && d.ID != prev {
			return d.ID
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

func depID(d *store.AgentDeployment) string {
	if d == nil {
		return "<none>"
	}
	return d.ID
}

func rejectingDeploymentService(t *testing.T, h *e2eHarness) *DeploymentService {
	t.Helper()
	dead := control.NewClient(h.brk, "no-app-owns-this-namespace", control.WithTimeout(300*time.Millisecond))
	return NewDeploymentService(
		h.deploymentRepo, h.agentRepo, h.connectorRepo, h.toolPackageRepo, h.yamlSvc, dead,
		NewDefaultDeployerRegistry(h.yamlSvc, h.connectorRepo, h.toolPackageRepo, nil, h.a2aRepo, h.namespace, slog.Default()),
		slog.Default(),
	)
}

func TestDeployBuiltinUpdates_ControlPlaneRejects_DoesNotLogSuccess(t *testing.T) {
	h := setupE2E(t, nil)
	if err := SeedBuiltinAgents(h.ctx, h.agentSvc, h.agentRepo, false, false, slog.Default()); err != nil {
		t.Fatalf("SeedBuiltinAgents: %v", err)
	}
	seeded, _, _, _, _, err := h.agentRepo.GetByName(h.ctx, builtinOrchestratorName)
	if err != nil || seeded == nil {
		t.Fatalf("precondition: the Orchestrator was not seeded (err=%v)", err)
	}

	logs := &syncedBuffer{}
	prevDefault := slog.Default()
	defer slog.SetDefault(prevDefault)
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))

	newTestReconciler(h, rejectingDeploymentService(t, h)).deployBuiltinsOnce(h.ctx)

	row, err := h.deployRepo.GetLatestByAgentID(h.ctx, seeded.ID)
	if err != nil || row == nil || row.Status != DeployStatusFailed {
		t.Fatalf("precondition: want a failed deployment row, got %+v (err=%v)", row, err)
	}
	if !strings.Contains(logs.String(), "built-in agent deploy did not succeed") {
		t.Errorf("a rejected built-in deploy was not reported as failed: %q", logs.String())
	}
}

func TestE2E_AnEditedRemoteAgentReachesAWEsOnlyWhenDeployed(t *testing.T) {
	h := setupE2E(t, nil)
	a2aID := h.createA2AAgent("edited_remote_agent", "https://remote.example.com")
	deployed := h.deployA2AAgent(a2aID)
	agent, _, _, _, _, err := h.agentRepo.GetByID(h.ctx, a2aID)
	if err != nil || agent == nil {
		t.Fatalf("remote agent: %+v, %v", agent, err)
	}
	instance := h.deploySvc.deployers.Lookup(agent.Type).InstanceName(agent)
	before := listedAgent(askForAgents(t, h), instance)
	if before == nil || before.DeploymentID != deployed.ID || len(before.Payloads) == 0 {
		t.Fatalf("deployed remote agent listed as %+v, want deployment %s with its config", before, deployed.ID)
	}

	stored, err := h.a2aRepo.GetByID(h.ctx, a2aID)
	if err != nil || stored == nil {
		t.Fatalf("remote agent row: %+v, %v", stored, err)
	}
	stored.AgentURL = "https://edited.example.com"
	if err := h.a2aRepo.Update(h.ctx, stored); err != nil {
		t.Fatalf("edit the remote agent URL: %v", err)
	}
	if diff := cmp.Diff(before, listedAgent(askForAgents(t, h), instance)); diff != "" {
		t.Fatalf("an edit that was not deployed changed what AWEs are told (-before +after):\n%s", diff)
	}
}

func TestE2E_AnUndeployInFlightOfADeletedAgentIsStillListed(t *testing.T) {
	h := setupE2E(t, nil)
	agentID := h.createAgent("deleted_mid_undeploy", "prompt")
	h.deploy(agentID)
	instance := h.agentInstanceName(agentID)
	h.waitForInstance(instance, 5*time.Second)

	undeploy := store.AgentDeployment{ID: idgen.New(), AgentID: agentID, Action: DeployActionUndeploy}
	body, err := json.Marshal(agentIntent{Deployment: undeploy, Instance: instance})
	if err != nil {
		t.Fatalf("encode the intent: %v", err)
	}
	if err := store.NewDeploymentIntentRepo(h.db).Put(h.ctx, &store.DeploymentIntent{ID: undeploy.ID, Kind: store.IntentKindAgent, ObjectID: agentID, Payload: string(body)}); err != nil {
		t.Fatalf("record the undeploy in flight: %v", err)
	}
	if err := h.agentRepo.Delete(h.ctx, agentID); err != nil {
		t.Fatalf("delete the agent row: %v", err)
	}

	got := listedAgent(askForAgents(t, h), instance)
	if got == nil || !got.Undeploy || got.DeploymentID != undeploy.ID {
		t.Fatalf("deleted agent with an undeploy in flight listed as %+v, want undeploy %s", got, undeploy.ID)
	}
}
