// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type queueRemovalRecorder struct {
	aweRollout
	running func(instance string) bool
	mu      sync.Mutex
	removed []string
	early   []string
}

func (r *queueRemovalRecorder) removeQueues(instance string) error {
	r.mu.Lock()
	r.removed = append(r.removed, instance)
	if r.running != nil && r.running(instance) {
		r.early = append(r.early, instance)
	}
	r.mu.Unlock()
	return r.aweRollout.removeQueues(instance)
}

func (r *queueRemovalRecorder) removedWhileRunning() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.early)
}

func recordQueueRemovals(rec *queueRemovalRecorder) e2eOption {
	return withAWERollout(func(live *aweStatusTracker) aweRollout {
		rec.aweRollout = live
		return rec
	})
}

func runningOn(h *e2eHarness) func(string) bool {
	return func(instance string) bool {
		_, err := h.aweExe.GetInstance(instance)
		return err == nil
	}
}

func (r *queueRemovalRecorder) removals() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.removed)
}

func deployThenUndeploy(h *e2eHarness, kind string) string {
	h.t.Helper()
	switch kind {
	case "agent":
		agentID := h.createAgent("undeploy_queue_agent", "prompt")
		h.deploy(agentID)
		h.undeploy(agentID)
		return h.agentInstanceName(agentID)
	case "remote agent":
		a2aID := h.createA2AAgent("undeploy_queue_remote", "https://remote.example.com")
		h.deployA2AAgent(a2aID)
		h.undeployA2AAgent(a2aID)
		return h.agentInstanceName(a2aID)
	default:
		wfID := h.createWorkflow("undeploy_queue_wf", map[string]any{
			"agent_type": "workflow",
			"namespace":  "${NAMESPACE, solace-agent-mesh}",
			"workflow": map[string]any{
				"output_mapping": map[string]any{"result": "{{start.output}}"},
				"nodes":          []any{map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test"}},
			},
		})
		h.deployWorkflow(wfID)
		if _, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionUndeploy}, "e2e-user"); err != nil {
			h.t.Fatalf("undeploy workflow %s: %v", wfID, err)
		}
		return workflowInstanceName(wfID)
	}
}

func TestE2E_TheLeaderRemovesAnUndeployedQueueOnceTheAWEsConfirm(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"agent", "remote agent", "workflow"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			rec := &queueRemovalRecorder{}
			h := setupE2E(t, nil, recordQueueRemovals(rec))
			rec.running = runningOn(h)

			instance := deployThenUndeploy(h, kind)

			if got := rec.removals(); !slices.Equal(got, []string{instance}) {
				t.Fatalf("leader queue removals %v, want once for %s after the AWEs confirmed the undeploy", got, instance)
			}
			if early := rec.removedWhileRunning(); len(early) != 0 {
				t.Fatalf("the leader removed the queue of %v while the AWE still ran it, want only after the AWEs confirmed the undeploy", early)
			}
		})
	}
}

func TestDeployLeader_AnUndeployCleanupRunsOnlyWhileItsIntentIsInFlight(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	leader := newDeployLeader(nil, store.NewDeploymentIntentRepo(h.db), slog.Default())
	body, err := json.Marshal(agentIntent{Instance: "agent_x"})
	if err != nil {
		t.Fatalf("encode the intent: %v", err)
	}
	undeployID, redeployID := idgen.New(), idgen.New()
	if err = leader.begin(h.ctx, store.IntentKindAgent, "x", undeployID, json.RawMessage(body)); err != nil {
		t.Fatalf("begin the undeploy: %v", err)
	}
	ran := 0
	cleanup := func() error { ran++; return nil }

	if err = leader.whileInFlight(h.ctx, undeployID, cleanup); err != nil || ran != 1 {
		t.Fatalf("cleanup while the undeploy is in flight: ran %d, %v, want once", ran, err)
	}
	if err = leader.begin(h.ctx, store.IntentKindAgent, "x", redeployID, json.RawMessage(body)); err != nil {
		t.Fatalf("begin the redeploy: %v", err)
	}
	if err = leader.whileInFlight(h.ctx, undeployID, cleanup); err != nil || ran != 1 {
		t.Fatalf("cleanup after a redeploy replaced the undeploy: ran %d, %v, want it skipped", ran, err)
	}
}

func TestE2E_AnUndeployInFlightOfADeletedWorkflowRemovesItsQueue(t *testing.T) {
	t.Parallel()
	rec := &queueRemovalRecorder{}
	h := setupE2E(t, nil, recordQueueRemovals(rec))
	wfID := h.createWorkflow("deleted_wf", map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{start.output}}"},
			"nodes":          []any{map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test"}},
		},
	})
	h.deployWorkflow(wfID)
	leader := newDeployLeader(nil, store.NewDeploymentIntentRepo(h.db), slog.Default())
	finishWith(leader, nil, h.wfDeploySvc, nil)
	h.wfDeploySvc.leader = leader
	d := store.WorkflowDeployment{ID: idgen.New(), WorkflowID: wfID, Action: DeployActionUndeploy, CreatedBy: "e2e-user"}
	if err := leader.begin(h.ctx, store.IntentKindWorkflow, wfID, d.ID, workflowIntent{Deployment: d}); err != nil {
		t.Fatalf("begin the undeploy: %v", err)
	}
	if err := h.workflowRepo.Delete(h.ctx, wfID); err != nil {
		t.Fatalf("delete the workflow: %v", err)
	}

	if done, err := leader.finishOne(h.ctx, store.IntentKindWorkflow, wfID, d.ID); err != nil || !done {
		t.Fatalf("finish the undeploy in flight: done %v, %v", done, err)
	}

	if got, want := rec.removals(), []string{workflowInstanceName(wfID)}; !slices.Equal(got, want) {
		t.Fatalf("queue removals %v, want %v once the AWEs confirmed the undeploy of the deleted workflow", got, want)
	}
}
