// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type modelRefsEnv struct {
	svc            *ModelConfigService
	db             *store.DB
	agentRepo      *store.AgentRepo
	workflowRepo   *store.WorkflowRepo
	deploymentRepo *store.WorkflowDeploymentRepo
	undeploys      *recordingUndeployer
	cascade        *ModelDependentsHandler
}

type recordingUndeployer struct {
	mu     sync.Mutex
	ids    []string
	refuse bool
}

func (r *recordingUndeployer) Create(_ context.Context, req CreateWorkflowDeploymentRequest, _ string) (*WorkflowDeploymentResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.Action == DeployActionUndeploy {
		r.ids = append(r.ids, req.WorkflowID)
	}
	if r.refuse {
		return &WorkflowDeploymentResponse{WorkflowID: req.WorkflowID, Status: DeployStatusFailed, ErrorMessage: "control plane unavailable"}, nil
	}
	return &WorkflowDeploymentResponse{WorkflowID: req.WorkflowID, Status: DeployStatusSuccess}, nil
}

func newModelRefsEnv(t *testing.T) *modelRefsEnv {
	t.Helper()
	db := testOpenDB(t)
	modelRepo := store.NewModelConfigRepo(db)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	deploymentRepo := store.NewWorkflowDeploymentRepo(db)

	svc := NewModelConfigService(modelRepo, slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	svc.SetWorkflows(workflowRepo, deploymentRepo)
	undeploys := &recordingUndeployer{}
	dependents := NewModelDependentsHandler(agentRepo, workflowRepo, deploymentRepo, modelRepo, nil, slog.Default())
	dependents.SetWorkflowUndeployer(undeploys)
	return &modelRefsEnv{
		svc:            svc,
		db:             db,
		agentRepo:      agentRepo,
		workflowRepo:   workflowRepo,
		deploymentRepo: deploymentRepo,
		undeploys:      undeploys,
		cascade:        dependents,
	}
}

func (e *modelRefsEnv) createModel(t *testing.T, req *CreateModelConfigRequest) *ModelConfigResponse {
	t.Helper()
	created, err := e.svc.Create(t.Context(), req, "tester")
	if err != nil {
		t.Fatalf("create model %q: %v", req.Alias, err)
	}
	return created
}

func (e *modelRefsEnv) saveWorkflow(t *testing.T, name string, appConfig map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(appConfig)
	if err != nil {
		t.Fatalf("marshal workflow body: %v", err)
	}
	wf := &store.Workflow{Name: name, AppConfig: string(raw), CreatedBy: "tester", UpdatedBy: "tester"}
	if err := e.workflowRepo.Create(t.Context(), wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow %q: %v", name, err)
	}
	return wf.ID
}

func (e *modelRefsEnv) markDeployed(t *testing.T, workflowID string) {
	t.Helper()
	if err := e.deploymentRepo.Create(t.Context(), &store.WorkflowDeployment{
		WorkflowID: workflowID, Status: "success", Action: "deploy", CreatedBy: "tester",
	}); err != nil {
		t.Fatalf("record deployment: %v", err)
	}
}

func flatDecisionBody(alias string) map[string]any {
	return map[string]any{"decision": map[string]any{"model_alias": alias}}
}

func TestUndeployDependentsReportsAWorkflowWhoseUndeployWasRefused(t *testing.T) {
	t.Parallel()
	env := newModelRefsEnv(t)
	env.undeploys.refuse = true
	env.createModel(t, decisionModelRequest("jev"))
	deployed := env.saveWorkflow(t, "triage", flatDecisionBody("jev"))
	env.markDeployed(t, deployed)

	failed, err := env.cascade.UndeployDependents(t.Context(), "jev")
	if err != nil {
		t.Fatalf("UndeployDependents: %v", err)
	}
	if len(failed) != 1 || failed[0].ID != deployed {
		t.Errorf("failed = %+v, want the workflow %s whose undeploy was refused", failed, deployed)
	}
}

func TestUndeployDependentsReportsNothingWhenAWorkflowUndeploys(t *testing.T) {
	t.Parallel()
	env := newModelRefsEnv(t)
	env.createModel(t, decisionModelRequest("jev"))
	env.markDeployed(t, env.saveWorkflow(t, "triage", flatDecisionBody("jev")))

	failed, err := env.cascade.UndeployDependents(t.Context(), "jev")
	if err != nil {
		t.Fatalf("UndeployDependents: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("failed = %+v, want none", failed)
	}
}
