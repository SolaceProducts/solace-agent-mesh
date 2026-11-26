// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const unstartableWorkflowSnapshot = `apps:
  - name: wf_broken
    app_config:
      agent_type: workflow
      namespace: test
      workflow:
        output_mapping:
          result: "{{a.output}}"
        nodes:
          - id: a
            name: a
            type: agent
            agent_name: test
            depends_on: [b]
          - id: b
            name: b
            type: agent
            agent_name: test
            depends_on: [a]
`

func newWorkflowReconciler(t *testing.T, h *e2eHarness) *Reconciler {
	t.Helper()
	rec := NewReconciler(
		h.agentRepo, h.deployRepo, h.connectorRepo,
		h.workflowRepo, h.gatewayRepo, h.gwDeployRepo,
		h.yamlSvc, nil, h.controlClient, h.brk, h.namespace, slog.Default(),
	)
	rec.SetDeployerRegistry(NewDefaultDeployerRegistry(h.yamlSvc, h.connectorRepo, nil, nil, h.a2aRepo, h.namespace, slog.Default()))
	rec.SetWorkflowDeploymentRepo(h.wfDeployRepo)
	return rec
}

func workflowDisplayStatus(t *testing.T, h *e2eHarness, workflowID string) string {
	t.Helper()
	got, err := h.workflowRepo.GetDeploymentStatus(h.ctx, workflowID)
	if err != nil {
		t.Fatalf("deployment status: %v", err)
	}
	return got
}

func TestReconcileWorkflow_DoesNotResurrectAnUndeployedWorkflowAfterAFailedDeploy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	appConfig := map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{start.output}}"},
			"nodes": []any{
				map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test"},
			},
		},
	}
	wfID := h.createWorkflow("wf_reconcile_undeployed", appConfig)
	h.deployWorkflow(wfID)
	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	if _, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID, Action: DeployActionUndeploy,
	}, "e2e-user"); err != nil {
		t.Fatalf("undeploy: %v", err)
	}

	failMsg := "control error: rejected"
	if err := h.wfDeployRepo.Create(h.ctx, &store.WorkflowDeployment{
		WorkflowID:   wfID,
		Status:       DeployStatusFailed,
		Action:       DeployActionDeploy,
		ErrorMessage: &failMsg,
		CreatedBy:    "e2e-user",
	}); err != nil {
		t.Fatalf("seed failed deploy row: %v", err)
	}

	if got := listedAgent(askForAgents(t, h), instanceName); got == nil || !got.Undeploy {
		t.Errorf("AWEs are told %+v for a workflow the operator had undeployed, want an undeploy", got)
	}
	if _, err := h.aweExe.GetInstance(instanceName); err == nil {
		t.Error("reconciliation resurrected a workflow the operator had undeployed")
	}
	if got := workflowDisplayStatus(t, h, wfID); got == DisplayStatusDeployed {
		t.Error("an undeployed workflow reads as deployed after reconciliation")
	}
}

func TestReconcileAgent_DoesNotResurrectAnUndeployedAgentAfterAFailedDeploy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	agentID := h.createAgent("recon_undeployed_agent", "prompt")
	h.deploy(agentID)
	instanceName := h.agentInstanceName(agentID)
	h.waitForInstance(instanceName, 5*time.Second)

	h.undeploy(agentID)

	failMsg := "control error: rejected"
	if err := h.deployRepo.Create(h.ctx, &store.AgentDeployment{
		AgentID:      agentID,
		Status:       DeployStatusFailed,
		Action:       DeployActionDeploy,
		ErrorMessage: &failMsg,
		CreatedBy:    "e2e-user",
	}); err != nil {
		t.Fatalf("seed failed deploy row: %v", err)
	}

	if got := listedAgent(askForAgents(t, h), instanceName); got == nil || !got.Undeploy {
		t.Errorf("AWEs are told %+v for an agent the operator had undeployed, want an undeploy", got)
	}
	if _, err := h.aweExe.GetInstance(instanceName); err == nil {
		t.Error("reconciliation resurrected an agent the operator had undeployed")
	}
}

func TestGetLatestSuccessfulDeploys_SameMillisecondSuccessesResolveToTheLater(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	agentID := h.createAgent("recon_same_ms", "prompt")
	stamp := time.Now().UnixMilli()
	older, newer := "apps:\n  - name: older\n", "apps:\n  - name: newer\n"
	for _, snap := range []string{older, newer} {
		s := snap
		if err := h.deployRepo.Create(h.ctx, &store.AgentDeployment{
			AgentID:             agentID,
			Status:              DeployStatusSuccess,
			Action:              DeployActionDeploy,
			AgentConfigSnapshot: &s,
			CreatedBy:           "e2e-user",
			CreatedTime:         stamp,
		}); err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}

	got, err := h.deployRepo.GetLatestSuccessfulDeploys(h.ctx, []string{agentID})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	d := got[agentID]
	if d == nil || d.AgentConfigSnapshot == nil {
		t.Fatal("no latest successful deploy returned")
	}
	if !strings.Contains(*d.AgentConfigSnapshot, "newer") {
		t.Errorf("same-millisecond tie resolved to the older row: %q", *d.AgentConfigSnapshot)
	}
}

func TestDesiredAWEAgents_UnreadListIsAnError(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	rec := newWorkflowReconciler(t, h)
	if err := h.db.Close(); err != nil {
		t.Fatalf("close the database: %v", err)
	}
	if agents, err := rec.desiredAWEAgents(h.ctx); err == nil {
		t.Fatalf("listed %d agents with the agent and workflow lists unread, want an error", len(agents))
	}
}

func TestAnswerAWE_AGWEThatCannotReadStaysSilent(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	rec := newWorkflowReconciler(t, h)
	replyTo := a2a.AWEAgentsReplyTopic(h.namespace, "silent")
	q, err := h.brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the reply queue: %v", err)
	}
	if err := h.brk.Subscribe(q, replyTo); err != nil {
		t.Fatalf("subscribe the reply queue: %v", err)
	}
	if err := h.db.Close(); err != nil {
		t.Fatalf("close the database: %v", err)
	}

	rec.answerAWE(h.ctx, &broker.Message{ReplyTo: replyTo, UserProperties: map[string]string{discovery.ProcessIDProperty: "test-awe"}})

	wait, cancel := context.WithTimeout(h.ctx, time.Second)
	defer cancel()
	if msg, err := h.brk.Receive(wait, q); err == nil && msg != nil {
		t.Fatalf("a GWE that cannot read the tables answered %s, want no answer so the AWE asks again", msg.Payload)
	}
}

func TestAnswerAWE_AnAnswerLargerThanOneBrokerMessageIsNotSent(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	rec := newWorkflowReconciler(t, h)
	agentID := h.createAgent("oversized_answer", "prompt")
	payloads, err := json.Marshal([]map[string]any{{"name": h.agentInstanceName(agentID), "pad": strings.Repeat("x", broker.MaxMessageSize)}})
	if err != nil {
		t.Fatalf("encode the oversized payloads: %v", err)
	}
	stored := string(payloads)
	if err = h.deployRepo.Create(h.ctx, &store.AgentDeployment{ID: idgen.New(), AgentID: agentID, Action: DeployActionDeploy, Status: DeployStatusSuccess, DeployedPayloads: &stored, CreatedBy: "e2e-user"}); err != nil {
		t.Fatalf("record the oversized deployment: %v", err)
	}
	replyTo := a2a.AWEAgentsReplyTopic(h.namespace, "oversized")
	q, err := h.brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the reply queue: %v", err)
	}
	if err = h.brk.Subscribe(q, replyTo); err != nil {
		t.Fatalf("subscribe the reply queue: %v", err)
	}

	rec.answerAWE(h.ctx, &broker.Message{ReplyTo: replyTo, UserProperties: map[string]string{discovery.ProcessIDProperty: "test-awe"}})

	wait, cancel := context.WithTimeout(h.ctx, time.Second)
	defer cancel()
	if msg, err := h.brk.Receive(wait, q); err == nil && msg != nil {
		t.Fatalf("an answer of %d bytes was sent, want none over the %d byte limit", len(msg.Payload), broker.MaxMessageSize)
	}
}

func TestAnswerAWE_ARequestThatIsNotAnAWEAskGetsNoAnswer(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	rec := newWorkflowReconciler(t, h)
	cases := []struct {
		name    string
		replyTo string
		process string
		want    bool
	}{
		{name: "an AWE's ask", replyTo: a2a.AWEAgentsReplyTopic(h.namespace, "ok"), process: "awe-1", want: true},
		{name: "no AWE process", replyTo: a2a.AWEAgentsReplyTopic(h.namespace, "anon"), want: false},
		{name: "a reply topic outside the AWE reply prefix", replyTo: h.namespace + "/listener/inbox", process: "awe-1", want: false},
		{name: "a reply topic with a wildcard id", replyTo: a2a.AWEAgentsReplyTopic(h.namespace, "a/b"), process: "awe-1", want: false},
	}
	for _, c := range cases {
		q, err := h.brk.CreateDirectSubscription()
		if err != nil {
			t.Fatalf("%s: create the reply queue: %v", c.name, err)
		}
		if err = h.brk.Subscribe(q, c.replyTo); err != nil {
			t.Fatalf("%s: subscribe the reply queue: %v", c.name, err)
		}
		props := map[string]string{}
		if c.process != "" {
			props[discovery.ProcessIDProperty] = c.process
		}

		rec.answerAWE(h.ctx, &broker.Message{ReplyTo: c.replyTo, UserProperties: props})

		wait, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
		msg, err := h.brk.Receive(wait, q)
		cancel()
		if got := err == nil && msg != nil; got != c.want {
			t.Fatalf("%s: answered %v, want %v", c.name, got, c.want)
		}
	}
}
