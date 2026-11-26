// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestDeployLeader_SecondLeaderLeavesTheOperationToTheFirst(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)
	intents := store.NewDeploymentIntentRepo(h.db)
	serveDeploys(newDeployLeader(store.NewDeployRequestRepo(h.db), intents, slog.Default()), h.deploySvc, h.wfDeploySvc, h.gwDeploySvc)

	wfID := h.createWorkflow("two_leaders_wf", map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{start.output}}"},
			"nodes":          []any{map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test"}},
		},
	})
	gwID := h.createGateway("two_leaders_gw")
	cases := []struct {
		kind, objectID string
		run            func(id string) error
		recorded       func(id string) bool
	}{
		{store.IntentKindWorkflow, wfID, func(id string) error {
			_, err := h.wfDeploySvc.createHere(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionDeploy}, "e2e-user", id)
			return err
		}, func(id string) bool { row, _ := h.wfDeployRepo.GetByID(h.ctx, id); return row != nil }},
		{store.IntentKindEntrypoint, gwID, func(id string) error {
			_, err := h.gwDeploySvc.deployHere(h.ctx, CreateGatewayDeploymentRequest{GatewayID: gwID, Action: DeployActionDeploy}, "e2e-user", id)
			return err
		}, func(id string) bool { row, _ := h.gwDeployRepo.GetByID(h.ctx, id); return row != nil }},
	}
	for _, c := range cases {
		id := idgen.New()
		if err := intents.Put(h.ctx, &store.DeploymentIntent{ID: id, Kind: c.kind, ObjectID: c.objectID, Payload: "{}"}); err != nil {
			t.Fatal(err)
		}
		if err := c.run(id); !errors.Is(err, errOtherLeader) {
			t.Errorf("%s: got %v, want the operation left to the leader that recorded it", c.kind, err)
		}
		if c.recorded(id) {
			t.Errorf("%s: the second leader wrote a row", c.kind)
		}
		if kept, err := intents.For(h.ctx, c.kind, c.objectID); err != nil || kept == nil || kept.ID != id {
			t.Errorf("%s: record in flight = %+v, %v, want the first leader's kept", c.kind, kept, err)
		}
	}
}

func TestDeployLeader_BeginReplacesAnOlderOperationOnItsObject(t *testing.T) {
	t.Parallel()
	intents := store.NewDeploymentIntentRepo(testOpenDB(t))
	ctx := t.Context()
	left, other := idgen.New(), idgen.New()
	for id, object := range map[string]string{left: "a", other: "b"} {
		if err := intents.Put(ctx, &store.DeploymentIntent{ID: id, Kind: store.IntentKindAgent, ObjectID: object, Payload: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	l := newDeployLeader(nil, intents, slog.New(slog.DiscardHandler))
	next := idgen.New()
	if err := l.begin(ctx, store.IntentKindAgent, "a", next, agentIntent{}); err != nil {
		t.Fatal(err)
	}
	newest, err := intents.Newest(ctx, store.IntentKindAgent)
	if err != nil {
		t.Fatal(err)
	}
	if all, _ := intents.List(ctx, store.IntentKindAgent); len(all) != 2 || newest["a"].ID != next || newest["b"].ID != other {
		t.Fatalf("records in flight = %+v, want only the new one on a and b's untouched", all)
	}
}

func TestDeployLeader_TwoLeadersAnswerOneAgentDeployOnce(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	intents := store.NewDeploymentIntentRepo(h.db)
	requests := store.NewDeployRequestRepo(h.db)
	ctx, cancel := context.WithCancel(t.Context())
	var serving sync.WaitGroup
	for range 2 {
		l := newDeployLeader(requests, intents, slog.Default())
		l.poll = 5 * time.Millisecond
		serveDeploys(l, h.deploySvc, h.wfDeploySvc, nil)
		serving.Go(func() { _ = l.serve(ctx, leader.Term{Number: 1}) })
	}
	t.Cleanup(func() {
		cancel()
		serving.Wait()
	})

	agentID := h.createAgent("two_leaders_agent", "prompt")
	resp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{AgentID: agentID, Action: DeployActionDeploy}, "e2e-user")
	if err != nil || resp == nil || resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy = %+v, %v, want success", resp, err)
	}
	cancel()
	serving.Wait()
	requireOneRow(t, h, `SELECT COUNT(*) FROM agent_deployments WHERE agent_id = ?`, agentID)
	if left, _ := intents.List(h.ctx, store.IntentKindAgent); len(left) != 0 {
		t.Fatalf("operations still in flight = %+v", left)
	}
}

type recordedByTheOtherLeader struct {
	row           store.WorkflowDeployment
	ownRolloutErr error
}

type otherLeaderRecordsTheRollout struct {
	aweRollout
	deployments *store.WorkflowDeploymentRepo
	next        atomic.Pointer[recordedByTheOtherLeader]
}

func (r *otherLeaderRecordsTheRollout) rollOut(ctx context.Context, instance, deploymentID string) error {
	armed := r.next.Swap(nil)
	if armed == nil {
		return r.aweRollout.rollOut(ctx, instance, deploymentID)
	}
	row := armed.row
	row.ID = deploymentID
	if err := r.deployments.Create(ctx, &row); err != nil {
		return err
	}
	return armed.ownRolloutErr
}

func workflowsLedWhileTheOtherLeaderRecords(t *testing.T) (*e2eHarness, *otherLeaderRecordsTheRollout) {
	t.Helper()
	h := setupE2E(t, nil)
	l := newDeployLeader(store.NewDeployRequestRepo(h.db), store.NewDeploymentIntentRepo(h.db), slog.New(slog.DiscardHandler))
	l.poll = 5 * time.Millisecond
	rollout := &otherLeaderRecordsTheRollout{aweRollout: h.wfDeploySvc.awes, deployments: h.wfDeployRepo}
	h.wfDeploySvc.awes = rollout
	serveDeploys(l, nil, h.wfDeploySvc, nil)
	lead(t, l)
	return h, rollout
}

func TestDeployLeader_AWorkflowDeployTheOtherLeaderRecordedIsAnsweredWithItsRecord(t *testing.T) {
	t.Parallel()
	h, rollout := workflowsLedWhileTheOtherLeaderRecords(t)
	wfID := h.createWorkflow("other_leader_deploy_wf", workingWorkflowAppConfig())
	rollout.next.Store(&recordedByTheOtherLeader{row: store.WorkflowDeployment{WorkflowID: wfID, Action: "deploy", Status: "success", CreatedBy: "the-other-leader"}})

	got, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionDeploy}, "e2e-user")
	if err != nil || got == nil || got.CreatedBy != "the-other-leader" || got.Status != "success" {
		t.Fatalf("Create(deploy %s) = %+v, %v, want the other leader's successful record and no error", wfID, got, err)
	}
	requireOneRow(t, h, `SELECT COUNT(*) FROM workflow_deployments WHERE workflow_id = ?`, wfID)
}

func TestDeployLeader_AWorkflowUndeployTheOtherLeaderRecordedIsAnsweredWithItsRecord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		workflow      string
		ownRolloutErr error
	}{
		{name: "an undeploy the other leader recorded answers without an error", workflow: "other_leader_undeploy_wf", ownRolloutErr: nil},
		{name: "an undeploy whose own rollout failed answers from the other leader's record", workflow: "other_leader_failed_undeploy_wf", ownRolloutErr: errors.New("AWE refused the undeploy")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, rollout := workflowsLedWhileTheOtherLeaderRecords(t)
			wfID := h.createWorkflow(tc.workflow, workingWorkflowAppConfig())
			h.deployWorkflow(wfID)
			rollout.next.Store(&recordedByTheOtherLeader{
				row:           store.WorkflowDeployment{WorkflowID: wfID, Action: "undeploy", Status: "success", CreatedBy: "the-other-leader"},
				ownRolloutErr: tc.ownRolloutErr,
			})

			if err := h.wfDeploySvc.Undeploy(h.ctx, wfID, "e2e-user"); err != nil {
				t.Fatalf("Undeploy(%s) = %v, want the other leader's successful record answered without an error", wfID, err)
			}
			requireOneRow(t, h, `SELECT COUNT(*) FROM workflow_deployments WHERE workflow_id = ? AND action = 'undeploy'`, wfID)
		})
	}
}
