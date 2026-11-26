// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func workingWorkflowAppConfig() map[string]any {
	return map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{start.output}}"},
			"nodes": []any{
				map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test"},
			},
		},
	}
}

func danglingDependencyWorkflowAppConfig() map[string]any {
	return map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{a.output}}"},
			"nodes": []any{
				map[string]any{"id": "a", "name": "a", "type": "agent", "agent_name": "test", "depends_on": []any{"ghost"}},
			},
		},
	}
}

func replaceAppConfig(t *testing.T, h *e2eHarness, workflowID string, appConfig map[string]any) {
	t.Helper()
	if _, err := h.workflowSvc.Update(h.ctx, workflowID, UpdateWorkflowRequest{
		Name:      "wf_rollback",
		AppConfig: appConfig,
	}, "e2e-user"); err != nil {
		t.Fatalf("update workflow: %v", err)
	}
}

func TestWorkflowDeployFailure_RollsBackToLastGood(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_rollback", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)
	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	replaceAppConfig(t, h, wfID, danglingDependencyWorkflowAppConfig())

	resp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID,
		Action:     DeployActionUpdate,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("deploy call itself errored: %v", err)
	}

	if resp.Status != DeployStatusFailed {
		t.Errorf("deploy response status = %q, want %q", resp.Status, DeployStatusFailed)
	}
	if !strings.Contains(resp.ErrorMessage, `depends on non-existent node "ghost"`) {
		t.Errorf("deploy was rejected for an unintended reason: %q", resp.ErrorMessage)
	}

	if _, instErr := h.aweExe.GetInstance(instanceName); instErr != nil {
		t.Fatalf("nothing is running after a failed deploy: %v", instErr)
	}

	if got := workflowDisplayStatus(t, h, wfID); got != DisplayStatusDeployed {
		t.Errorf("status = %q, want %q: a rolled-back deploy is not an outage", got, DisplayStatusDeployed)
	}

	latest, err := h.wfDeployRepo.GetLatestByWorkflowID(h.ctx, wfID)
	if err != nil {
		t.Fatalf("latest row: %v", err)
	}
	if latest.Status != DeployStatusSuccess || latest.CreatedBy != rollbackCreatedBy {
		t.Errorf("latest row = %s/%s, want a %s compensating row", latest.Status, latest.CreatedBy, rollbackCreatedBy)
	}
	if latest.WorkflowConfigSnapshot == nil {
		t.Fatal("compensating row carries no snapshot")
	}
	if strings.Contains(*latest.WorkflowConfigSnapshot, "depends_on") {
		t.Error("compensating row carries the FAILED config; reconciliation would replay it forever")
	}
	var sawFailure bool
	rows, _, err := h.wfDeployRepo.ListPaginated(h.ctx, 1, 200)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range rows {
		if rows[i].WorkflowID == wfID && rows[i].Status == DeployStatusFailed {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Error("the failed deploy attempt is absent from deployment history")
	}
}

func TestWorkflowDeployFailure_RollbackAlsoFails_LeavesFailedStatus(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_rollback_fails", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)

	unstartable := unstartableWorkflowSnapshot
	if err := h.wfDeployRepo.Create(h.ctx, &store.WorkflowDeployment{
		WorkflowID:             wfID,
		Status:                 DeployStatusSuccess,
		Action:                 DeployActionDeploy,
		WorkflowConfigSnapshot: &unstartable,
		CreatedBy:              "e2e-user",
	}); err != nil {
		t.Fatalf("seed unstartable last-good: %v", err)
	}

	replaceAppConfig(t, h, wfID, danglingDependencyWorkflowAppConfig())

	var logged bytes.Buffer
	svc := NewWorkflowDeploymentService(
		h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, nil, newMemResourceStore(),
		h.controlClient, h.namespace, slog.New(slog.NewTextHandler(&logged, nil)),
	)
	h.useHarnessAWEs(svc)
	resp, err := svc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID,
		Action:     DeployActionUpdate,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("deploy call errored: %v", err)
	}
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q", resp.Status, DeployStatusFailed)
	}

	if got := workflowDisplayStatus(t, h, wfID); got != DisplayStatusDeployFailed {
		t.Errorf("status = %q, want %q: nothing is running and nothing says so", got, DisplayStatusDeployFailed)
	}
	latest, err := h.wfDeployRepo.GetLatestByWorkflowID(h.ctx, wfID)
	if err != nil {
		t.Fatalf("latest row: %v", err)
	}
	if latest.CreatedBy != rollbackCreatedBy || latest.Status != DeployStatusFailed || derefStr(latest.WorkflowConfigSnapshot) != unstartable {
		t.Errorf("latest row = %s by %s with snapshot %q, want the failed restore of the last good config", latest.Status, latest.CreatedBy, derefStr(latest.WorkflowConfigSnapshot))
	}
	desired, err := h.wfDeployRepo.DesiredFor(h.ctx, wfID)
	if err != nil || desired == nil || desired.ID != latest.ID {
		t.Errorf("AWEs are told to run %+v (%v), want the restore %s and not the deploy that just failed", desired, err, latest.ID)
	}
	if !strings.Contains(logged.String(), "rollback not confirmed by every AWE") {
		t.Errorf("no rollback attempt was made; the failure branch was never reached: %q", logged.String())
	}
}

func TestWorkflowDeployFailure_DoesNotResurrectAnUndeployedWorkflow(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_undeployed", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)
	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	if _, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID,
		Action:     DeployActionUndeploy,
	}, "e2e-user"); err != nil {
		t.Fatalf("undeploy: %v", err)
	}
	if got := workflowDisplayStatus(t, h, wfID); got != DisplayStatusNotDeployed {
		t.Fatalf("precondition: status = %q, want %q", got, DisplayStatusNotDeployed)
	}

	replaceAppConfig(t, h, wfID, danglingDependencyWorkflowAppConfig())
	resp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID,
		Action:     DeployActionDeploy,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("deploy call errored: %v", err)
	}
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q", resp.Status, DeployStatusFailed)
	}

	if _, err := h.aweExe.GetInstance(instanceName); err == nil {
		t.Error("a failed deploy restarted a workflow the operator had undeployed")
	}
	if got := workflowDisplayStatus(t, h, wfID); got == DisplayStatusDeployed {
		t.Error("an undeployed workflow reads as deployed after a failed deploy")
	}
}

func TestWorkflowDeployFailure_RetryAfterUndeploy_StillDoesNotResurrect(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_undeploy_retry", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)
	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	if _, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID, Action: DeployActionUndeploy,
	}, "e2e-user"); err != nil {
		t.Fatalf("undeploy: %v", err)
	}

	replaceAppConfig(t, h, wfID, danglingDependencyWorkflowAppConfig())

	for attempt := 1; attempt <= 2; attempt++ {
		resp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
			WorkflowID: wfID, Action: DeployActionDeploy,
		}, "e2e-user")
		if err != nil {
			t.Fatalf("attempt %d errored: %v", attempt, err)
		}
		if resp.Status != DeployStatusFailed {
			t.Fatalf("attempt %d status = %q, want %q", attempt, resp.Status, DeployStatusFailed)
		}
	}

	if _, err := h.aweExe.GetInstance(instanceName); err == nil {
		t.Error("a retried failed deploy resurrected a workflow the operator had undeployed")
	}
	if got := workflowDisplayStatus(t, h, wfID); got == DisplayStatusDeployed {
		t.Error("an undeployed workflow reads as deployed after a retried failed deploy")
	}
}

func TestWorkflowDeployFailure_UndeployInSameMillisecond_StillBlocksRollback(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_same_ms_undeploy", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)

	prior, err := h.wfDeployRepo.GetLatestSuccessfulDeploy(h.ctx, wfID)
	if err != nil || prior == nil {
		t.Fatalf("prior deploy: %v", err)
	}
	if seedErr := h.wfDeployRepo.Create(h.ctx, &store.WorkflowDeployment{
		WorkflowID:  wfID,
		Status:      DeployStatusSuccess,
		Action:      DeployActionUndeploy,
		CreatedBy:   "e2e-user",
		CreatedTime: prior.CreatedTime,
	}); seedErr != nil {
		t.Fatalf("seed same-millisecond undeploy: %v", seedErr)
	}

	replaceAppConfig(t, h, wfID, danglingDependencyWorkflowAppConfig())
	resp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID, Action: DeployActionDeploy,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("deploy call errored: %v", err)
	}
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q", resp.Status, DeployStatusFailed)
	}

	rows, _, err := h.wfDeployRepo.ListPaginated(h.ctx, 1, 200)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range rows {
		if rows[i].WorkflowID == wfID && rows[i].CreatedBy == rollbackCreatedBy {
			t.Fatal("rolled back onto a snapshot superseded by a same-millisecond undeploy")
		}
	}
}

func TestWorkflowDeployFailure_LastGoodHasNoSnapshot_DeclinesAndSaysWhy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_no_snapshot", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)

	empty := ""
	if err := h.wfDeployRepo.Create(h.ctx, &store.WorkflowDeployment{
		WorkflowID:             wfID,
		Status:                 DeployStatusSuccess,
		Action:                 DeployActionDeploy,
		WorkflowConfigSnapshot: &empty,
		CreatedBy:              "e2e-user",
	}); err != nil {
		t.Fatalf("seed snapshot-less row: %v", err)
	}

	replaceAppConfig(t, h, wfID, danglingDependencyWorkflowAppConfig())
	var logged bytes.Buffer
	svc := NewWorkflowDeploymentService(
		h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, nil, newMemResourceStore(),
		h.controlClient, h.namespace, slog.New(slog.NewTextHandler(&logged, nil)),
	)
	h.useHarnessAWEs(svc)
	resp, err := svc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID, Action: DeployActionUpdate,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("deploy call errored: %v", err)
	}
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q", resp.Status, DeployStatusFailed)
	}
	if !strings.Contains(logged.String(), "no config snapshot") {
		t.Errorf("nothing said rollback was skipped for want of a snapshot: %q", logged.String())
	}
	rows, _, err := h.wfDeployRepo.ListPaginated(h.ctx, 1, 200)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := range rows {
		if rows[i].WorkflowID == wfID && rows[i].CreatedBy == rollbackCreatedBy {
			t.Error("claimed a rollback with no snapshot to roll back to")
		}
	}
}

func TestWorkflowDeployFailure_LastGoodNoLongerParses_DeclinesAndSaysWhy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_unparseable_last_good", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)

	malformed := "apps: [this is not valid yaml"
	if err := h.wfDeployRepo.Create(h.ctx, &store.WorkflowDeployment{
		WorkflowID:             wfID,
		Status:                 DeployStatusSuccess,
		Action:                 DeployActionDeploy,
		WorkflowConfigSnapshot: &malformed,
		CreatedBy:              "e2e-user",
	}); err != nil {
		t.Fatalf("seed malformed last-good: %v", err)
	}

	replaceAppConfig(t, h, wfID, danglingDependencyWorkflowAppConfig())
	var logged bytes.Buffer
	svc := NewWorkflowDeploymentService(
		h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, nil, newMemResourceStore(),
		h.controlClient, h.namespace, slog.New(slog.NewTextHandler(&logged, nil)),
	)
	h.useHarnessAWEs(svc)
	resp, err := svc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID, Action: DeployActionUpdate,
	}, "e2e-user")
	if err != nil {
		t.Fatalf("deploy call errored: %v", err)
	}
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q", resp.Status, DeployStatusFailed)
	}
	if !strings.Contains(logged.String(), "no longer parses") {
		t.Errorf("nothing reported the unparseable last-good snapshot: %q", logged.String())
	}
	if got := workflowDisplayStatus(t, h, wfID); got != DisplayStatusDeployFailed {
		t.Errorf("status = %q, want %q: nothing is running and nothing says so", got, DisplayStatusDeployFailed)
	}
}

func TestWorkflowRollback_UndeployLandedMidFlight_DeclinesAndSaysWhy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_undeploy_midflight", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)
	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	wf, err := h.workflowRepo.GetByID(h.ctx, wfID)
	if err != nil || wf == nil {
		t.Fatalf("load workflow: %v", err)
	}

	var logged bytes.Buffer
	svc := NewWorkflowDeploymentService(
		h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, nil, newMemResourceStore(),
		h.controlClient, h.namespace, slog.New(slog.NewTextHandler(&logged, nil)),
	)
	h.useHarnessAWEs(svc)

	lastGood := svc.lastGoodDeploy(h.ctx, wf)
	if lastGood == nil {
		t.Fatal("precondition: there should be a last-good deploy to roll back to")
	}

	if seedErr := h.wfDeployRepo.Create(h.ctx, &store.WorkflowDeployment{
		WorkflowID: wfID,
		Status:     DeployStatusSuccess,
		Action:     DeployActionUndeploy,
		CreatedBy:  "e2e-user",
	}); seedErr != nil {
		t.Fatalf("seed mid-flight undeploy: %v", seedErr)
	}

	if restored := svc.rollback(h.ctx, wf, lastGood); restored != nil {
		t.Errorf("rolled back onto a workflow the operator undeployed mid-flight: restored %+v", restored)
	}
	h.nudgeAWEs()
	h.waitForInstanceGone(instanceName, 10*time.Second)
	if !strings.Contains(logged.String(), "undeployed while the deploy was in flight") {
		t.Errorf("nothing said why the rollback was declined: %q", logged.String())
	}
}

func TestWorkflowRollback_NewerDeploySucceededMidFlight_DeclinesAndSaysWhy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_newer_deploy_midflight", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)

	wf, err := h.workflowRepo.GetByID(h.ctx, wfID)
	if err != nil || wf == nil {
		t.Fatalf("load workflow: %v", err)
	}

	var logged bytes.Buffer
	svc := NewWorkflowDeploymentService(
		h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, nil, newMemResourceStore(),
		h.controlClient, h.namespace, slog.New(slog.NewTextHandler(&logged, nil)),
	)
	h.useHarnessAWEs(svc)
	lastGood := svc.lastGoodDeploy(h.ctx, wf)
	if lastGood == nil {
		t.Fatal("precondition: there should be a last-good deploy to roll back to")
	}

	newer := "apps:\n  - name: newer\n    app_config:\n      agent_type: workflow\n"
	if seedErr := h.wfDeployRepo.Create(h.ctx, &store.WorkflowDeployment{
		WorkflowID:             wfID,
		Status:                 DeployStatusSuccess,
		Action:                 DeployActionDeploy,
		WorkflowConfigSnapshot: &newer,
		CreatedBy:              "other-user",
	}); seedErr != nil {
		t.Fatalf("seed newer successful deploy: %v", seedErr)
	}

	if restored := svc.rollback(h.ctx, wf, lastGood); restored != nil {
		t.Errorf("rolled back over a newer successful deploy: restored %+v", restored)
	}
	if !strings.Contains(logged.String(), "a newer deploy succeeded") {
		t.Errorf("nothing said why the rollback was declined: %q", logged.String())
	}
}

func TestWorkflowRollback_WorkflowDeletedMidFlight_DeclinesAndSaysWhy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_deleted_midflight", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)
	instanceName := workflowInstanceName(wfID)
	h.waitForInstance(instanceName, 5*time.Second)

	wf, err := h.workflowRepo.GetByID(h.ctx, wfID)
	if err != nil || wf == nil {
		t.Fatalf("load workflow: %v", err)
	}

	var logged bytes.Buffer
	svc := NewWorkflowDeploymentService(
		h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, nil, newMemResourceStore(),
		h.controlClient, h.namespace, slog.New(slog.NewTextHandler(&logged, nil)),
	)
	h.useHarnessAWEs(svc)
	lastGood := svc.lastGoodDeploy(h.ctx, wf)
	if lastGood == nil {
		t.Fatal("precondition: there should be a last-good deploy to roll back to")
	}

	if _, undErr := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: wfID, Action: DeployActionUndeploy,
	}, "e2e-user"); undErr != nil {
		t.Fatalf("undeploy before delete: %v", undErr)
	}
	if _, instErr := h.aweExe.GetInstance(instanceName); instErr == nil {
		t.Fatal("precondition: the undeploy returned with the instance still running")
	}
	if undeployed, uErr := h.wfDeployRepo.GetLatestSuccessfulUndeploy(h.ctx, wfID); uErr != nil || undeployed == nil {
		t.Fatalf("precondition: the undeploy row should exist before the delete, got %+v (err %v)", undeployed, uErr)
	}
	if delErr := h.workflowRepo.Delete(h.ctx, wfID); delErr != nil {
		t.Fatalf("delete workflow: %v", delErr)
	}
	if undeployed, uErr := h.wfDeployRepo.GetLatestSuccessfulUndeploy(h.ctx, wfID); uErr != nil || undeployed != nil {
		t.Fatalf("precondition: the cascade should have removed the undeploy row, got %+v (err %v)", undeployed, uErr)
	}

	if restored := svc.rollback(h.ctx, wf, lastGood); restored != nil {
		t.Errorf("rolled back a workflow that no longer exists: restored %+v", restored)
	}
	if _, instErr := h.aweExe.GetInstance(instanceName); instErr == nil {
		t.Error("resurrected an instance for a deleted workflow: it is invisible to the platform")
	}
	if !strings.Contains(logged.String(), "deleted while the deploy was in flight") {
		t.Errorf("nothing said why the rollback was declined: %q", logged.String())
	}
}

func TestWorkflowRollback_ReturnsTheLastGoodSnapshotUnderANewDeploymentID(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("wf_already_exists", workingWorkflowAppConfig())
	h.deployWorkflow(wfID)

	wf, err := h.workflowRepo.GetByID(h.ctx, wfID)
	if err != nil || wf == nil {
		t.Fatalf("load workflow: %v", err)
	}
	svc := NewWorkflowDeploymentService(
		h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, nil, newMemResourceStore(),
		h.controlClient, h.namespace, slog.New(slog.DiscardHandler),
	)
	h.useHarnessAWEs(svc)
	lastGood := svc.lastGoodDeploy(h.ctx, wf)
	if lastGood == nil {
		t.Fatal("precondition: there should be a last-good deploy to roll back to")
	}

	restored := svc.rollback(h.ctx, wf, lastGood)
	if restored == nil {
		t.Fatal("no restore for a workflow with a last good deploy")
	}
	if restored.runningSnapshot != *lastGood.WorkflowConfigSnapshot || restored.priorDeploymentID != lastGood.ID || restored.deploymentID == lastGood.ID || restored.deploymentID == "" {
		t.Errorf("restore = %+v, want the snapshot of %s under a new deployment id", restored, lastGood.ID)
	}
}
