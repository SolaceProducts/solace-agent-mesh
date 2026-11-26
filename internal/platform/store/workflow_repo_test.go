// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"slices"
	"testing"
)

func TestWorkflowRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewWorkflowRepo(db)
	ctx := context.Background()

	desc := "A test workflow for data processing"
	workflow := &Workflow{
		Name:        "test-workflow",
		Description: &desc,
		AppConfig:   `{"workflow_name":"test-workflow","nodes":[]}`,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	err := repo.Create(ctx, workflow, WorkflowBindings{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if workflow.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, err := repo.GetByID(ctx, workflow.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected workflow")
	}
	if got.Name != "test-workflow" {
		t.Errorf("name = %q, want %q", got.Name, "test-workflow")
	}
	if got.AppConfig != `{"workflow_name":"test-workflow","nodes":[]}` {
		t.Errorf("app_config = %q, want stored JSON", got.AppConfig)
	}

	notFound, err := repo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("get not found: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for nonexistent workflow")
	}

	nonUUID, err := repo.GetByID(ctx, "not-a-uuid")
	if err != nil {
		t.Fatalf("get non-uuid: %v", err)
	}
	if nonUUID != nil {
		t.Error("expected nil for non-uuid lookup")
	}

	newDesc := "Updated description for testing"
	got.Name = "renamed-workflow"
	got.Description = &newDesc
	got.AppConfig = `{"workflow_name":"renamed-workflow","nodes":[{"name":"step1"}]}`
	got.UpdatedBy = "user2"
	err = repo.Update(ctx, got, WorkflowBindings{})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	updated, err := repo.GetByID(ctx, workflow.ID)
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if updated.Name != "renamed-workflow" {
		t.Errorf("updated name = %q, want %q", updated.Name, "renamed-workflow")
	}
	if updated.UpdatedBy != "user2" {
		t.Errorf("updated_by = %q, want %q", updated.UpdatedBy, "user2")
	}

	w2 := &Workflow{
		Name:      "second-workflow",
		AppConfig: `{"workflow_name":"second-workflow"}`,
		CreatedBy: "user1",
		UpdatedBy: "user1",
	}
	err = repo.Create(ctx, w2, WorkflowBindings{})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	workflows, total, err := repo.ListPaginated(ctx, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(workflows) != 2 {
		t.Errorf("count = %d, want 2", len(workflows))
	}

	workflows, total, err = repo.ListPaginated(ctx, 1, 1)
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(workflows) != 1 {
		t.Errorf("page 1 count = %d, want 1", len(workflows))
	}

	err = repo.Delete(ctx, w2.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	deleted, err := repo.GetByID(ctx, w2.ID)
	if err != nil {
		t.Fatalf("get deleted: %v", err)
	}
	if deleted != nil {
		t.Error("expected nil after delete")
	}

	err = repo.Delete(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err == nil {
		t.Error("expected error for nonexistent delete")
	}
}

func TestWorkflowDeploymentRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	workflowRepo := NewWorkflowRepo(db)
	deployRepo := NewWorkflowDeploymentRepo(db)
	ctx := context.Background()

	workflow := &Workflow{
		Name:      "deploy-test-workflow",
		AppConfig: `{"workflow_name":"deploy-test-workflow"}`,
		CreatedBy: "user1",
		UpdatedBy: "user1",
	}
	err := workflowRepo.Create(ctx, workflow, WorkflowBindings{})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	deployment := &WorkflowDeployment{
		WorkflowID: workflow.ID,
		Status:     "success",
		Action:     "deploy",
		CreatedBy:  "user1",
	}
	err = deployRepo.Create(ctx, deployment)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if deployment.ID == "" {
		t.Fatal("expected deployment ID to be set")
	}

	got, err := deployRepo.GetByID(ctx, deployment.ID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if got == nil {
		t.Fatal("expected deployment")
	}
	if got.Status != "success" {
		t.Errorf("status = %q, want %q", got.Status, "success")
	}

	deployments, total, err := deployRepo.ListPaginated(ctx, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(deployments) != 1 {
		t.Errorf("count = %d, want 1", len(deployments))
	}

	notFound, err := deployRepo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("get not found: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for nonexistent deployment")
	}
}

func TestWorkflowRepo_DeploymentStatus(t *testing.T) {
	db := TestDB(t)
	workflowRepo := NewWorkflowRepo(db)
	deployRepo := NewWorkflowDeploymentRepo(db)
	ctx := context.Background()

	w1 := &Workflow{Name: "w1", AppConfig: `{}`, CreatedBy: "user", UpdatedBy: "user"}
	w2 := &Workflow{Name: "w2", AppConfig: `{}`, CreatedBy: "user", UpdatedBy: "user"}
	err := workflowRepo.Create(ctx, w1, WorkflowBindings{})
	if err != nil {
		t.Fatalf("create w1: %v", err)
	}
	err = workflowRepo.Create(ctx, w2, WorkflowBindings{})
	if err != nil {
		t.Fatalf("create w2: %v", err)
	}

	status, err := workflowRepo.GetDeploymentStatus(ctx, w1.ID)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status != "not_deployed" {
		t.Errorf("status = %q, want %q", status, "not_deployed")
	}

	d := &WorkflowDeployment{WorkflowID: w1.ID, Status: "success", Action: "deploy", CreatedBy: "user"}
	err = deployRepo.Create(ctx, d)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	status, err = workflowRepo.GetDeploymentStatus(ctx, w1.ID)
	if err != nil {
		t.Fatalf("get status after deploy: %v", err)
	}
	if status != "deployed" {
		t.Errorf("status = %q, want %q", status, "deployed")
	}

	statuses, err := workflowRepo.GetDeploymentStatuses(ctx, []string{w1.ID, w2.ID})
	if err != nil {
		t.Fatalf("get statuses: %v", err)
	}
	if statuses[w1.ID] != "deployed" {
		t.Errorf("w1 status = %q, want %q", statuses[w1.ID], "deployed")
	}
	if statuses[w2.ID] != "" {
		t.Errorf("w2 status = %q, want empty", statuses[w2.ID])
	}

	empty, err := workflowRepo.GetDeploymentStatuses(ctx, nil)
	if err != nil {
		t.Fatalf("get empty statuses: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected empty map, got %v", empty)
	}
}

func TestWorkflowRepo_DeploymentStatus_CreatedTimeTieBreak(t *testing.T) {
	db := TestDB(t)
	workflowRepo := NewWorkflowRepo(db)
	deployRepo := NewWorkflowDeploymentRepo(db)
	ctx := t.Context()

	w := &Workflow{Name: "w-tiebreak", AppConfig: `{}`, CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(ctx, w, WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	tiedTime := int64(1_000_000_000)
	undeploy := &WorkflowDeployment{
		WorkflowID:  w.ID,
		Status:      "success",
		Action:      "undeploy",
		CreatedBy:   "u",
		CreatedTime: tiedTime,
	}
	if err := deployRepo.Create(ctx, undeploy); err != nil {
		t.Fatalf("create undeploy row: %v", err)
	}
	deploy := &WorkflowDeployment{
		WorkflowID:  w.ID,
		Status:      "success",
		Action:      "deploy",
		CreatedBy:   "u",
		CreatedTime: tiedTime,
	}
	if err := deployRepo.Create(ctx, deploy); err != nil {
		t.Fatalf("create deploy row: %v", err)
	}

	status, err := workflowRepo.GetDeploymentStatus(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetDeploymentStatus: %v", err)
	}
	if status != "deployed" {
		t.Errorf("GetDeploymentStatus = %q, want %q (later deploy row must win)", status, "deployed")
	}

	statuses, err := workflowRepo.GetDeploymentStatuses(ctx, []string{w.ID})
	if err != nil {
		t.Fatalf("GetDeploymentStatuses: %v", err)
	}
	if statuses[w.ID] != "deployed" {
		t.Errorf("GetDeploymentStatuses[%s] = %q, want %q", w.ID, statuses[w.ID], "deployed")
	}
}

func TestWorkflowRepo_CascadeDelete(t *testing.T) {
	db := TestDB(t)
	workflowRepo := NewWorkflowRepo(db)
	deployRepo := NewWorkflowDeploymentRepo(db)
	ctx := context.Background()

	w := &Workflow{Name: "cascade-test", AppConfig: `{}`, CreatedBy: "user", UpdatedBy: "user"}
	err := workflowRepo.Create(ctx, w, WorkflowBindings{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	d := &WorkflowDeployment{WorkflowID: w.ID, Status: "success", Action: "deploy", CreatedBy: "user"}
	err = deployRepo.Create(ctx, d)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	err = workflowRepo.Delete(ctx, w.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := deployRepo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get deployment after cascade: %v", err)
	}
	if got != nil {
		t.Error("expected deployment to be cascade-deleted")
	}
}

func TestWorkflowRepo_ConnectorBindings(t *testing.T) {
	db := TestDB(t)
	workflowRepo := NewWorkflowRepo(db)
	connectorRepo := NewConnectorRepo(db)
	ctx := context.Background()

	newConnector := func(name string) *Connector {
		sub := "smtp"
		vals := `{"smtp_host":"h"}`
		c := &Connector{Name: name, Description: new("test connector"), Type: "email", Subtype: &sub, Values: &vals, CreatedBy: "u", UpdatedBy: "u"}
		if err := connectorRepo.Create(ctx, c); err != nil {
			t.Fatalf("create connector %q: %v", name, err)
		}
		return c
	}
	connA := newConnector("conn-a")
	connB := newConnector("conn-b")

	wf := &Workflow{Name: "bound-workflow", AppConfig: "{}", CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(ctx, wf, WorkflowBindings{Connectors: []WorkflowConnectorBinding{
		{ConnectorID: connA.ID, RefName: "conn-a"},
	}}); err != nil {
		t.Fatalf("create workflow with binding: %v", err)
	}

	bindings, err := workflowRepo.GetConnectorBindings(ctx, wf.ID)
	if err != nil {
		t.Fatalf("get bindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].ConnectorID != connA.ID || bindings[0].RefName != "conn-a" {
		t.Fatalf("bindings = %+v, want one binding to conn-a", bindings)
	}

	ids, err := workflowRepo.ListIDsByConnectorID(ctx, connA.ID)
	if err != nil {
		t.Fatalf("list by connector: %v", err)
	}
	if len(ids) != 1 || ids[0] != wf.ID {
		t.Fatalf("ListIDsByConnectorID = %v, want [%s]", ids, wf.ID)
	}

	if updateErr := workflowRepo.Update(ctx, wf, WorkflowBindings{Connectors: []WorkflowConnectorBinding{
		{ConnectorID: connB.ID, RefName: "conn-b"},
	}}); updateErr != nil {
		t.Fatalf("update workflow bindings: %v", updateErr)
	}
	bindings, err = workflowRepo.GetConnectorBindings(ctx, wf.ID)
	if err != nil {
		t.Fatalf("get bindings after update: %v", err)
	}
	if len(bindings) != 1 || bindings[0].ConnectorID != connB.ID {
		t.Fatalf("bindings after update = %+v, want only conn-b", bindings)
	}
	if ids, _ := workflowRepo.ListIDsByConnectorID(ctx, connA.ID); len(ids) != 0 {
		t.Fatalf("conn-a still lists workflows after rebind: %v", ids)
	}

	if delErr := connectorRepo.Delete(ctx, connB.ID); delErr != nil {
		t.Fatalf("delete connector: %v", delErr)
	}
	bindings, err = workflowRepo.GetConnectorBindings(ctx, wf.ID)
	if err != nil {
		t.Fatalf("get bindings after connector delete: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("bindings after connector delete = %+v, want none (FK cascade)", bindings)
	}

	if rebindErr := workflowRepo.Update(ctx, wf, WorkflowBindings{Connectors: []WorkflowConnectorBinding{{ConnectorID: connA.ID, RefName: "conn-a"}}}); rebindErr != nil {
		t.Fatalf("rebind: %v", rebindErr)
	}
	if delErr := workflowRepo.Delete(ctx, wf.ID); delErr != nil {
		t.Fatalf("delete workflow: %v", delErr)
	}
	if ids, _ := workflowRepo.ListIDsByConnectorID(ctx, connA.ID); len(ids) != 0 {
		t.Fatalf("workflow delete left junction rows: %v", ids)
	}
}

func TestWorkflowRepo_ToolsetBindings(t *testing.T) {
	db := TestDB(t)
	workflowRepo := NewWorkflowRepo(db)
	toolPackageRepo := NewToolPackageRepo(db)
	ctx := context.Background()

	wf := &Workflow{Name: "toolset-bound", AppConfig: `{"toolsets":["beta-tools","alpha-tools"]}`, CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(ctx, wf, WorkflowBindings{Toolsets: []string{"beta-tools", "alpha-tools"}}); err != nil {
		t.Fatalf("create workflow with toolsets: %v", err)
	}

	toolsets, err := workflowRepo.GetToolsets(ctx, wf.ID)
	if err != nil {
		t.Fatalf("get toolsets: %v", err)
	}
	if len(toolsets) != 2 || toolsets[0] != "alpha-tools" || toolsets[1] != "beta-tools" {
		t.Fatalf("toolsets = %v, want sorted [alpha-tools beta-tools]", toolsets)
	}

	ids, err := toolPackageRepo.GetWorkflowIDsUsingPackage(ctx, "alpha-tools")
	if err != nil {
		t.Fatalf("get workflows using package: %v", err)
	}
	if len(ids) != 1 || ids[0] != wf.ID {
		t.Fatalf("GetWorkflowIDsUsingPackage = %v, want [%s]", ids, wf.ID)
	}

	if updateErr := workflowRepo.Update(ctx, wf, WorkflowBindings{Toolsets: []string{"gamma-tools"}}); updateErr != nil {
		t.Fatalf("update toolset bindings: %v", updateErr)
	}
	toolsets, err = workflowRepo.GetToolsets(ctx, wf.ID)
	if err != nil {
		t.Fatalf("get toolsets after update: %v", err)
	}
	if len(toolsets) != 1 || toolsets[0] != "gamma-tools" {
		t.Fatalf("toolsets after update = %v, want [gamma-tools]", toolsets)
	}
	if ids, _ := toolPackageRepo.GetWorkflowIDsUsingPackage(ctx, "alpha-tools"); len(ids) != 0 {
		t.Fatalf("alpha-tools still lists workflows after rebind: %v", ids)
	}

	if err := workflowRepo.Delete(ctx, wf.ID); err != nil {
		t.Fatalf("delete workflow: %v", err)
	}
	if ids, _ := toolPackageRepo.GetWorkflowIDsUsingPackage(ctx, "gamma-tools"); len(ids) != 0 {
		t.Fatalf("gamma-tools still lists workflows after workflow delete: %v", ids)
	}
}

func TestWorkflowRepo_ListAll(t *testing.T) {
	db := TestDB(t)
	repo := NewWorkflowRepo(db)
	ctx := t.Context()

	empty, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty table listed %d workflows", len(empty))
	}

	for i, name := range []string{"first", "second", "third"} {
		w := &Workflow{Name: name, AppConfig: `{}`, CreatedBy: "u", UpdatedBy: "u", CreatedTime: int64(1000 + i)}
		if createErr := repo.Create(ctx, w, WorkflowBindings{}); createErr != nil {
			t.Fatalf("create %s: %v", name, createErr)
		}
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var names []string
	for _, w := range all {
		names = append(names, w.Name)
	}
	if want := []string{"first", "second", "third"}; !slices.Equal(names, want) {
		t.Errorf("ListAll names = %v, want %v", names, want)
	}
}
