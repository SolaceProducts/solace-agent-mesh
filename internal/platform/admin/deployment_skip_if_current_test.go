// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func watchAgentCards(t *testing.T, h *e2eHarness) *platpkg.AgentRegistry {
	t.Helper()
	cards := platpkg.NewAgentRegistry()
	queue, err := h.brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create discovery subscription: %v", err)
	}
	if err := h.brk.Subscribe(queue, a2a.DiscoverySubscription(h.a2aNamespace)); err != nil {
		t.Fatalf("subscribe discovery: %v", err)
	}
	ctx, cancel := context.WithCancel(h.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msg, err := h.brk.Receive(ctx, queue)
			if err != nil || msg == nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			var card a2a.AgentCard
			if json.Unmarshal(msg.Payload, &card) == nil && card.Name != "" {
				if msg.UserProperties["removed"] == "true" {
					cards.Remove(card.Name)
				} else {
					cards.AddOrUpdate(card)
				}
			}
			msg.Ack()
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return cards
}

func waitForCardDeployment(t *testing.T, cards *platpkg.AgentRegistry, instance, deploymentID string) *a2a.AgentCard {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if card := cards.Get(instance); card != nil && platpkg.GetAgentDeploymentID(card) == deploymentID {
			return card
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no card for %s carrying deployment %s", instance, deploymentID)
	return nil
}

func runningInstance(t *testing.T, h *e2eHarness, name string) awe.Instance {
	t.Helper()
	for _, inst := range h.aweExe.Instances() {
		if inst.Name() == name {
			return inst
		}
	}
	t.Fatalf("no running instance %s", name)
	return nil
}

type deployedAgent struct {
	h        *e2eHarness
	cards    *platpkg.AgentRegistry
	agentID  string
	instance string
	first    *AgentDeploymentResponse
	card     *a2a.AgentCard
}

func deployAgentWithCards(t *testing.T) deployedAgent {
	t.Helper()
	h := setupE2E(t, nil)
	cards := watchAgentCards(t, h)
	h.deploySvc.WithSkipIfCurrent(cards, h.deployRepo)
	agentID := h.createAgent("skip-if-current", "You answer questions about the weather in plain language.")
	first := h.deploy(agentID)
	instance := h.agentInstanceName(agentID)
	card := waitForCardDeployment(t, cards, instance, first.ID)
	return deployedAgent{h: h, cards: cards, agentID: agentID, instance: instance, first: first, card: card}
}

func (d deployedAgent) deploy(t *testing.T, skipIfCurrent bool) *AgentDeploymentResponse {
	t.Helper()
	resp, err := d.h.deploySvc.Create(d.h.ctx, CreateAgentDeploymentRequest{AgentID: d.agentID, Action: DeployActionDeploy, SkipIfCurrent: skipIfCurrent}, "e2e-user")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	return resp
}

func (d deployedAgent) rewriteAgentRow(t *testing.T, mutate func(*store.Agent)) {
	t.Helper()
	agent, toolsets, inputModes, outputModes, connectorIDs, err := d.h.agentRepo.GetByID(d.h.ctx, d.agentID)
	if err != nil || agent == nil {
		t.Fatalf("load agent: %v", err)
	}
	mutate(agent)
	if err := d.h.agentRepo.Update(d.h.ctx, agent, toolsets, inputModes, outputModes, connectorIDs, nil); err != nil {
		t.Fatalf("rewrite agent row: %v", err)
	}
}

func TestSkipIfCurrentKeepsTheInstanceOfAnUnchangedAgent(t *testing.T) {
	d := deployAgentWithCards(t)
	before := runningInstance(t, d.h, d.instance)

	resp := d.deploy(t, true)

	if resp.ID != d.first.ID || resp.Status != DeployStatusSuccess {
		t.Fatalf("got deployment %s (%s), want the existing %s", resp.ID, resp.Status, d.first.ID)
	}
	latest, err := d.h.deployRepo.GetLatestByAgentID(d.h.ctx, d.agentID)
	if err != nil || latest == nil || latest.ID != d.first.ID {
		t.Fatalf("a skipped deploy must not record a new attempt; latest = %+v, err = %v", latest, err)
	}
	if runningInstance(t, d.h, d.instance) != before {
		t.Fatal("the running instance was replaced, so the agent restarted")
	}
}

func TestDeployWithoutSkipIfCurrentStillRestartsAnUnchangedAgent(t *testing.T) {
	d := deployAgentWithCards(t)
	before := runningInstance(t, d.h, d.instance)

	resp := d.deploy(t, false)

	if resp.ID == d.first.ID {
		t.Fatal("a deploy without the flag returned the existing deployment")
	}
	waitForCardDeployment(t, d.cards, d.instance, resp.ID)
	if runningInstance(t, d.h, d.instance) == before {
		t.Fatal("a deploy without the flag kept the old instance")
	}
}

func TestSkipIfCurrentRedeploysARowSavedWithoutADeploy(t *testing.T) {
	d := deployAgentWithCards(t)
	d.rewriteAgentRow(t, func(agent *store.Agent) {
		prompt := "You answer questions about the weather and always give a forecast."
		agent.SystemPrompt = &prompt
	})

	resp := d.deploy(t, true)

	if resp.ID == d.first.ID {
		t.Fatal("skipped a deploy whose rendered config differs from the running one")
	}
	waitForCardDeployment(t, d.cards, d.instance, resp.ID)
}

func TestSkipIfCurrentRedeploysAChangeTheSyncHashCannotSee(t *testing.T) {
	d := deployAgentWithCards(t)
	d.rewriteAgentRow(t, func(agent *store.Agent) {
		provider := "another-model"
		agent.ModelProvider = &provider
	})
	view, err := d.h.agentSvc.GetByID(d.h.ctx, d.agentID)
	if err != nil || view.SyncStatus == nil || *view.SyncStatus != "in_sync" {
		t.Fatalf("precondition: the provider edit should be invisible to syncStatus, got %v (err %v)", view.SyncStatus, err)
	}

	resp := d.deploy(t, true)

	if resp.ID == d.first.ID {
		t.Fatal("skipped a deploy the model-provider change requires")
	}
}

func TestSkipIfCurrentRedeploysWhenTheRunningInstanceIsFromAnotherDeployment(t *testing.T) {
	d := deployAgentWithCards(t)
	d.useCard(cardFromDeployment(d.card, "an-earlier-deployment"))

	resp := d.deploy(t, true)

	if resp.ID == d.first.ID {
		t.Fatal("skipped although the running instance does not come from the latest deployment")
	}
}

func cardFromDeployment(card *a2a.AgentCard, deploymentID string) a2a.AgentCard {
	other := *card
	other.Capabilities.Extensions = make([]a2a.AgentExtension, len(card.Capabilities.Extensions))
	for i, ext := range card.Capabilities.Extensions {
		ext.Params = maps.Clone(ext.Params)
		if ext.URI == platpkg.ExtensionURIDeployment {
			ext.Params["deployment_id"] = deploymentID
		}
		other.Capabilities.Extensions[i] = ext
	}
	return other
}

func (d deployedAgent) useCard(card a2a.AgentCard) {
	cards := platpkg.NewAgentRegistry()
	cards.AddOrUpdate(card)
	d.h.deploySvc.WithSkipIfCurrent(cards, d.h.deployRepo)
}

func TestSkipIfCurrentRedeploysAnAgentWithNoRunningInstance(t *testing.T) {
	d := deployAgentWithCards(t)
	d.h.deploySvc.WithSkipIfCurrent(platpkg.NewAgentRegistry(), d.h.deployRepo)

	resp := d.deploy(t, true)

	if resp.ID == d.first.ID {
		t.Fatal("skipped although no instance is running")
	}
}

func TestSkipIfCurrentRedeploysAnAgentReportingProblems(t *testing.T) {
	d := deployAgentWithCards(t)
	degraded := *d.card
	degraded.Capabilities.Extensions = append(append([]a2a.AgentExtension{}, d.card.Capabilities.Extensions...), a2a.HealthProblemsExtension([]health.Problem{
		{ID: "queue:a2a/skip-if-current", Severity: health.Degraded, Detail: "request queue unavailable", Since: time.Now()},
	}))
	d.useCard(degraded)

	resp := d.deploy(t, true)

	if resp.ID == d.first.ID {
		t.Fatal("skipped the restart a degraded agent needs")
	}
}

func TestSkipIfCurrentRedeploysAnAgentWhoseLatestAttemptFailed(t *testing.T) {
	d := deployAgentWithCards(t)
	first, err := d.h.deployRepo.GetByID(d.h.ctx, d.first.ID)
	if err != nil || first == nil || first.AgentConfigSnapshot == nil {
		t.Fatalf("load first deployment: %v", err)
	}
	failedID := idgen.New()
	snapshot := strings.ReplaceAll(*first.AgentConfigSnapshot, d.first.ID, failedID)
	failure := "a later payload was rejected"
	if err := d.h.deploymentRepo.Create(d.h.ctx, &store.AgentDeployment{
		ID:                  failedID,
		AgentID:             d.agentID,
		Status:              DeployStatusFailed,
		Action:              DeployActionDeploy,
		AgentConfigSnapshot: &snapshot,
		ErrorMessage:        &failure,
		CreatedBy:           "e2e-user",
		CreatedTime:         time.Now().Add(time.Second).UnixMilli(),
	}); err != nil {
		t.Fatalf("record failed attempt: %v", err)
	}
	d.useCard(cardFromDeployment(d.card, failedID))

	resp := d.deploy(t, true)

	if resp.ID == d.first.ID || resp.ID == failedID {
		t.Fatalf("answered with an existing deployment (%s) although the latest attempt failed", resp.ID)
	}
}

func TestSkipIfCurrentDeploysWhenNoCardsAreAvailable(t *testing.T) {
	d := deployAgentWithCards(t)
	d.h.deploySvc.WithSkipIfCurrent(nil, d.h.deployRepo)

	resp := d.deploy(t, true)

	if resp.ID == d.first.ID {
		t.Fatal("skipped with no way to see which deployment is running")
	}
}

func (d deployedAgent) deployInput(t *testing.T) DeployInput {
	t.Helper()
	agent, toolsets, inputModes, outputModes, connectorIDs, err := d.h.agentRepo.GetByID(d.h.ctx, d.agentID)
	if err != nil || agent == nil {
		t.Fatalf("load agent: %v", err)
	}
	return DeployInput{Agent: agent, Action: DeployActionDeploy, Toolsets: toolsets, InputModes: inputModes, OutputModes: outputModes, ConnectorIDs: connectorIDs}
}

func TestSkipIfCurrentNeverSkipsADeployerWhoseSnapshotIsNotThePayload(t *testing.T) {
	d := deployAgentWithCards(t)
	standard := d.h.deploySvc.deployers.Lookup(AgentTypeStandard)
	proxy := d.h.deploySvc.deployers.Lookup(AgentTypeA2AProxy)

	if current := d.h.deploySvc.currentDeployment(d.h.ctx, standard, d.deployInput(t)); current == nil || current.ID != d.first.ID {
		t.Fatalf("precondition: the standard deployer should find %s current, got %+v", d.first.ID, current)
	}
	if current := d.h.deploySvc.currentDeployment(d.h.ctx, proxy, d.deployInput(t)); current != nil {
		t.Fatal("a deployer that cannot replay its snapshot was treated as proof of the running config")
	}
}

func TestSkipIfCurrentDeploysWhenTheLatestDeploymentCannotBeRead(t *testing.T) {
	d := deployAgentWithCards(t)
	standard := d.h.deploySvc.deployers.Lookup(AgentTypeStandard)
	if current := d.h.deploySvc.currentDeployment(d.h.ctx, standard, d.deployInput(t)); current == nil {
		t.Fatal("precondition: the agent should read as current while the lookup works")
	}
	cancelled, cancel := context.WithCancel(d.h.ctx)
	cancel()

	if current := d.h.deploySvc.currentDeployment(cancelled, standard, d.deployInput(t)); current != nil {
		t.Fatal("a failed lookup was treated as proof of the running config")
	}
}

func TestSkipIfCurrentRedeploysWhenAnotherReplicaRunsAnOlderDeployment(t *testing.T) {
	d := deployAgentWithCards(t)
	cards := platpkg.NewAgentRegistry()
	cards.AddOrUpdateInstance(cardFromDeployment(d.card, "an-earlier-deployment"), "replica-b")
	cards.AddOrUpdateInstance(*d.card, "replica-a")
	d.h.deploySvc.WithSkipIfCurrent(cards, d.h.deployRepo)

	resp := d.deploy(t, true)

	if resp.ID == d.first.ID {
		t.Fatal("skipped although replica-b still runs an earlier deployment")
	}
}

func TestSkipIfCurrentKeepsAnAgentWhoseReplicasAreAllCurrent(t *testing.T) {
	d := deployAgentWithCards(t)
	cards := platpkg.NewAgentRegistry()
	cards.AddOrUpdateInstance(*d.card, "replica-a")
	cards.AddOrUpdateInstance(*d.card, "replica-b")
	d.h.deploySvc.WithSkipIfCurrent(cards, d.h.deployRepo)

	resp := d.deploy(t, true)

	if resp.ID != d.first.ID {
		t.Fatalf("got deployment %s, want the existing %s: every replica runs the latest deployment", resp.ID, d.first.ID)
	}
}
