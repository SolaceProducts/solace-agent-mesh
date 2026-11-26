// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func startLeaderWithReconciler(t *testing.T, h *e2eHarness) (*Reconciler, *store.DeploymentIntentRepo) {
	t.Helper()
	intents := store.NewDeploymentIntentRepo(h.db)
	l := newDeployLeader(store.NewDeployRequestRepo(h.db), intents, slog.Default())
	l.check, l.poll = 20*time.Millisecond, 20*time.Millisecond
	serveDeploys(l, h.deploySvc, h.wfDeploySvc, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = l.serve(t.Context(), leader.Term{Number: 1})
	}()
	t.Cleanup(func() { <-done })
	rec := newTestReconciler(h, h.deploySvc)
	rec.SetDeploys(l)
	return rec, intents
}

func requireOneRow(t *testing.T, h *e2eHarness, count, objectID string) {
	t.Helper()
	var n int
	if err := h.db.QueryRowContext(h.ctx, count, objectID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("deployment rows = %d, %v, want only the deploy's own", n, err)
	}
}

func TestE2E_ASlowAgentStartSucceedsWhileAWEAsksAreAnswered(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	_, intents := startLeaderWithReconciler(t, h)
	agentID := h.createAgent("awe_ask_finishes_agent", "prompt")

	stalled := h.stallNextRollouts(1, 3*time.Second)
	type outcome struct {
		resp *AgentDeploymentResponse
		err  error
	}
	deployed := make(chan outcome, 1)
	go func() {
		resp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{AgentID: agentID, Action: DeployActionDeploy}, "e2e-user")
		deployed <- outcome{resp, err}
	}()
	<-stalled

	asked := time.Now()
	if reply := askForAgents(t, h); listedAgent(reply, h.agentInstanceName(agentID)) == nil {
		t.Fatalf("reply = %+v, want the agent in flight listed", reply)
	}
	if waited := time.Since(asked); waited > 2*time.Second {
		t.Errorf("the AWE was answered after %s, behind the unanswered start", waited)
	}
	got := <-deployed
	if got.err != nil || got.resp == nil || got.resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy = %+v, %v, want success", got.resp, got.err)
	}
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)
	requireOneRow(t, h, `SELECT COUNT(*) FROM agent_deployments WHERE agent_id = ?`, agentID)
	if left, _ := intents.List(h.ctx, store.IntentKindAgent); len(left) != 0 {
		t.Fatalf("operations still in flight = %+v", left)
	}
}

func TestE2E_ASlowWorkflowStartSucceedsWhileAWEAsksAreAnswered(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	_, intents := startLeaderWithReconciler(t, h)
	wfID := h.createWorkflow("awe_ask_finishes_wf", map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{start.output}}"},
			"nodes":          []any{map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test"}},
		},
	})

	stalled := h.stallNextRollouts(1, 3*time.Second)
	type outcome struct {
		resp *WorkflowDeploymentResponse
		err  error
	}
	deployed := make(chan outcome, 1)
	go func() {
		resp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionDeploy}, "e2e-user")
		deployed <- outcome{resp, err}
	}()
	<-stalled

	if reply := askForAgents(t, h); listedAgent(reply, workflowInstanceName(wfID)) == nil {
		t.Fatalf("reply = %+v, want the workflow in flight listed", reply)
	}
	got := <-deployed
	if got.err != nil || got.resp == nil || got.resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy = %+v, %v, want success", got.resp, got.err)
	}
	h.waitForInstance(workflowInstanceName(wfID), 5*time.Second)
	requireOneRow(t, h, `SELECT COUNT(*) FROM workflow_deployments WHERE workflow_id = ?`, wfID)
	if left, _ := intents.List(h.ctx, store.IntentKindWorkflow); len(left) != 0 {
		t.Fatalf("operations still in flight = %+v", left)
	}
}

func TestE2E_DeploysOfAnAgentStayInOrderWhileAStartIsSlow(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	startLeaderWithReconciler(t, h)
	agentID := h.createAgent("awe_ask_keeps_order", "prompt")
	h.deploy(agentID)

	stalled := h.stallNextRollouts(1, 3*time.Second)
	update := func(out chan<- *AgentDeploymentResponse) {
		resp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{AgentID: agentID, Action: DeployActionUpdate}, "e2e-user")
		if err != nil {
			t.Error(err)
		}
		out <- resp
	}
	first, second := make(chan *AgentDeploymentResponse, 1), make(chan *AgentDeploymentResponse, 1)
	go update(first)
	<-stalled
	go update(second)
	waitForRequests(t, h, 2)

	a, b := <-first, <-second
	if a == nil || b == nil || a.Status != DeployStatusSuccess || b.Status != DeployStatusSuccess {
		t.Fatalf("deploys = %+v, %+v, want both to succeed", a, b)
	}
	newest, err := h.deployRepo.GetLatestByAgentID(h.ctx, agentID)
	if err != nil || newest == nil || newest.ID != b.ID {
		t.Fatalf("newest row = %+v, %v, want the later deploy %s", newest, err, b.ID)
	}
	h.waitForAWEsToRun(h.agentInstanceName(agentID), b.ID)
}

func waitForRequests(t *testing.T, h *e2eHarness, n int) {
	t.Helper()
	requests := store.NewDeployRequestRepo(h.db)
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, err := requests.Waiting(t.Context(), store.IntentKindAgent, time.Hour)
		if err == nil && len(pending) == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d agent requests wait, want %d (%v)", len(pending), n, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestE2E_ANewLeaderFinishesADeployLeftInFlight(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	agentID := h.createAgent("awe_ask_finishes_left", "prompt")
	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)
	row, err := h.deployRepo.GetLatestByAgentID(h.ctx, agentID)
	if err != nil || row == nil || row.AgentConfigSnapshot == nil {
		t.Fatalf("deployed row: %+v, %v", row, err)
	}
	payloads, err := h.deploySvc.deployers.Lookup(AgentTypeStandard).ReplayFromSnapshot(*row.AgentConfigSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	intents := store.NewDeploymentIntentRepo(h.db)

	left := *row
	left.ID, left.Action, left.Status = idgen.New(), DeployActionUpdate, ""
	body, _ := json.Marshal(agentIntent{Deployment: left, Payloads: payloads})
	if err := intents.Put(h.ctx, &store.DeploymentIntent{ID: left.ID, Kind: store.IntentKindAgent, ObjectID: agentID, Payload: string(body)}); err != nil {
		t.Fatal(err)
	}
	startLeaderWithReconciler(t, h)

	deadline := time.Now().Add(5 * time.Second)
	for {
		recorded, _ := h.deployRepo.GetByID(h.ctx, left.ID)
		inFlight, _ := intents.List(h.ctx, store.IntentKindAgent)
		if recorded != nil && recorded.Status == DeployStatusSuccess && len(inFlight) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after a new leader term the deploy left in flight is recorded as %+v with %d still in flight", recorded, len(inFlight))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestE2E_AFinishedDeployDeliveredAgainIsNotRunAgain(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	_, intents := startLeaderWithReconciler(t, h)
	agentID := h.createAgent("delivered_again", "prompt")
	req := CreateAgentDeploymentRequest{AgentID: agentID, Action: DeployActionDeploy}
	first := h.deploy(agentID)
	instance := h.agentInstanceName(agentID)
	h.waitForInstance(instance, 5*time.Second)
	h.undeploy(agentID)
	h.waitForInstanceGone(instance, 5*time.Second)

	answered, err := h.deploySvc.leader.forward(h.ctx, opAgentDeploy, agentDeployArgs{Req: req, UserID: "e2e-user"}, first.ID, func(context.Context) bool { return false })
	if err != nil || answered != first.ID {
		t.Fatalf("the copy was answered (%q, %v), want the recorded deployment %s", answered, err, first.ID)
	}
	h.deploy(h.createAgent("checked_after_the_copy", "prompt"))
	if _, err := h.aweExe.GetInstance(instance); err == nil {
		t.Fatal("the undeployed agent runs again after the copy was answered")
	}
	requireRows(t, h, agentID, 2)
	if inFlight, _ := intents.List(h.ctx, store.IntentKindAgent); len(inFlight) != 0 {
		t.Fatalf("operations still in flight = %+v", inFlight)
	}
}

func TestE2E_ADeploymentRecordedBeforeStoredPayloadsIsReplayedFromItsSnapshot(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	agentID := h.createAgent("recorded_before_payloads", "prompt")
	deployed := h.deploy(agentID)
	row, err := h.deployRepo.GetByID(h.ctx, deployed.ID)
	if err != nil || row == nil || derefStr(row.DeployedPayloads) == "" {
		t.Fatalf("deployed row = %+v, %v, want stored payloads", row, err)
	}
	if _, err = h.db.ExecContext(h.ctx, `UPDATE agent_deployments SET deployed_payloads = NULL WHERE id = ?`, deployed.ID); err != nil {
		t.Fatalf("clear deployed_payloads of %s: %v", deployed.ID, err)
	}
	if _, err = h.db.ExecContext(h.ctx, `UPDATE agents SET system_prompt = ? WHERE id = ?`, "edited and never deployed", agentID); err != nil {
		t.Fatalf("edit the agent after its deploy: %v", err)
	}

	listed := listedAgent(askForAgents(t, h), h.agentInstanceName(agentID))
	if listed == nil || listed.DeploymentID != deployed.ID {
		t.Fatalf("listed = %+v, want the deployment %s", listed, deployed.ID)
	}
	got, err := json.Marshal(listed.Payloads)
	if err != nil {
		t.Fatalf("encode the listed payloads: %v", err)
	}
	if string(got) != *row.DeployedPayloads {
		t.Fatalf("replayed payloads = %s, want what the deploy sent: %s", got, *row.DeployedPayloads)
	}
}

func requireRows(t *testing.T, h *e2eHarness, agentID string, want int) {
	t.Helper()
	var n int
	if err := h.db.QueryRowContext(h.ctx, `SELECT COUNT(*) FROM agent_deployments WHERE agent_id = ?`, agentID).Scan(&n); err != nil || n != want {
		t.Fatalf("deployment rows = %d, %v, want %d", n, err, want)
	}
}

func TestE2E_ADeployWithNoLiveAWEIsRefusedAndLeavesNothing(t *testing.T) {
	t.Parallel()
	workflowConfig := map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{start.output}}"},
			"nodes":          []any{map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test"}},
		},
	}
	cases := []struct {
		name   string
		kind   string
		deploy func(h *e2eHarness) (objectID, rows string, err error)
	}{
		{name: "agent", kind: store.IntentKindAgent, deploy: func(h *e2eHarness) (string, string, error) {
			agentID := h.createAgent("no_live_awe_agent", "prompt")
			_, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{AgentID: agentID, Action: DeployActionDeploy}, "e2e-user")
			return agentID, `SELECT COUNT(*) FROM agent_deployments WHERE agent_id = ?`, err
		}},
		{name: "workflow", kind: store.IntentKindWorkflow, deploy: func(h *e2eHarness) (string, string, error) {
			wfID := h.createWorkflow("no_live_awe_wf", workflowConfig)
			_, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionDeploy}, "e2e-user")
			return wfID, `SELECT COUNT(*) FROM workflow_deployments WHERE workflow_id = ?`, err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := setupE2E(t, nil, withAWERollout(func(live *aweStatusTracker) aweRollout {
				idle := newAWEStatusTracker(live.brk, live.namespace)
				idle.wait = 200 * time.Millisecond
				return idle
			}))

			objectID, rows, err := c.deploy(h)

			if _, ok := errors.AsType[*UnavailableError](err); !ok {
				t.Fatalf("deploy with no live AWE = %v, want an UnavailableError (try again)", err)
			}
			var n int
			if err := h.db.QueryRowContext(h.ctx, rows, objectID).Scan(&n); err != nil || n != 0 {
				t.Fatalf("deployment rows = %d, %v, want none", n, err)
			}
			if intent, err := store.NewDeploymentIntentRepo(h.db).For(h.ctx, c.kind, objectID); err != nil || intent != nil {
				t.Fatalf("intent in flight = %+v, %v, want none", intent, err)
			}
		})
	}
}

func TestE2E_TheLeaderSweepClearsTheStoredConfigOfOlderDeployments(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	agentID := h.createAgent("swept_payloads", "prompt")
	first := h.deploy(agentID)
	second := h.deploy(agentID)
	stored := func(id string) bool {
		row, err := h.deployRepo.GetByID(h.ctx, id)
		if err != nil || row == nil {
			t.Fatalf("read deployment %s: %v", id, err)
		}
		return row.DeployedPayloads != nil
	}
	leader := newDeployLeader(nil, nil, slog.Default())
	leader.payloads = h.deployRepo

	leader.clearSupersededPayloads(h.ctx)

	if stored(first.ID) || !stored(second.ID) {
		t.Fatalf("after the sweep: first keeps payloads %v, second %v; want only the deployment the AWEs run to keep them", stored(first.ID), stored(second.ID))
	}
}
