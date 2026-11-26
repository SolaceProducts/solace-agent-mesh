// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func seedE2EToolset(h *e2eHarness, name, status string, toolNames ...string) *store.ToolPackage {
	h.t.Helper()
	pkg := &store.ToolPackage{Name: name, DiscoveryStatus: status, CreatedBy: "u", UpdatedBy: "u"}
	if err := h.toolPackageRepo.Create(h.ctx, pkg); err != nil {
		h.t.Fatalf("seed tool package %q: %v", name, err)
	}
	for _, tn := range toolNames {
		desc := tn + " desc"
		if err := h.toolPackageRepo.UpsertToolFile(h.ctx, &store.ToolPackageFile{
			PackageID:       pkg.ID,
			ToolName:        tn,
			ToolDescription: &desc,
			TimeoutSeconds:  30,
			SandboxProfile:  "default",
		}); err != nil {
			h.t.Fatalf("seed tool file %q: %v", tn, err)
		}
	}
	return pkg
}

func (h *e2eHarness) latestWorkflowDeployment(workflowID string) *store.WorkflowDeployment {
	h.t.Helper()
	deployments, _, err := h.wfDeployRepo.ListPaginated(h.ctx, 1, 50)
	if err != nil {
		h.t.Fatalf("list workflow deployments: %v", err)
	}
	var latest *store.WorkflowDeployment
	for i := range deployments {
		d := &deployments[i]
		if d.WorkflowID != workflowID {
			continue
		}
		if latest == nil || d.CreatedTime > latest.CreatedTime || (d.CreatedTime == latest.CreatedTime && d.ID > latest.ID) {
			latest = d
		}
	}
	return latest
}

func TestE2E_ToolsetUploadRedeploysWorkflow(t *testing.T) {
	t.Parallel()

	h := setupE2E(t, nil)
	pkg := seedE2EToolset(h, "e2e_wf_tools", "ready", "run")

	appConfig := map[string]any{
		"toolsets":       []any{"e2e_wf_tools"},
		"output_mapping": map[string]any{"result": "{{run.output}}"},
		"nodes": []any{
			map[string]any{"id": "run", "type": "tool", "tool_name": "e2e_wf_tools__run", "input": map[string]any{}},
		},
	}
	wfID := h.createWorkflow("e2e_toolset_workflow", appConfig)
	h.deployWorkflow(wfID)

	baseline := h.latestWorkflowDeployment(wfID)
	if baseline == nil || baseline.Status != DeployStatusSuccess {
		t.Fatalf("baseline deployment = %+v, want success", baseline)
	}
	if baseline.WorkflowConfigSnapshot == nil || !strings.Contains(*baseline.WorkflowConfigSnapshot, "e2e_wf_tools__run") {
		t.Fatal("deployed snapshot missing the hydrated toolset tool entry")
	}
	if strings.Contains(*baseline.WorkflowConfigSnapshot, "toolsets") {
		t.Fatal("deployed snapshot still carries the symbolic toolsets key")
	}

	if err := h.toolPackageRepo.UpdateDiscoveryStatus(h.ctx, pkg.ID, "pending", nil); err != nil {
		t.Fatalf("set package pending: %v", err)
	}

	pub := NewRedeployPublisher(h.deploySvc, h.agentRepo, testLogger()).
		WithWorkflowRedeploy(h.wfDeploySvc, h.workflowRepo)
	event := store.OutboxEvent{
		EventType:   "tool.uploaded",
		AggregateID: pkg.ID,
		Payload:     fmt.Sprintf(`{"agentIds":[],"workflowIds":[%q],"name":"e2e_wf_tools","userId":"uploader"}`, wfID),
	}

	if pubErr := pub.PublishOutboxEvent(h.ctx, event); pubErr == nil {
		t.Fatal("publish while the package is mid-discovery must return a retryable error, got nil (the event would commit and the workflow would never pick up the new code)")
	}
	afterPending := h.latestWorkflowDeployment(wfID)
	if afterPending.ID != baseline.ID {
		t.Fatalf("mid-discovery redeploy attempt recorded a deployment row (status %q) — the not-ready failure must stay off the row so the workflow remains deployed and retryable", afterPending.Status)
	}
	if status, _ := h.workflowRepo.GetDeploymentStatus(h.ctx, wfID); status != DisplayStatusDeployed {
		t.Fatalf("workflow status = %q mid-discovery, want %q so outbox retries are not skipped", status, DisplayStatusDeployed)
	}

	if err := h.toolPackageRepo.UpdateDiscoveryStatus(h.ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatalf("set package ready: %v", err)
	}
	if pubErr := pub.PublishOutboxEvent(h.ctx, event); pubErr != nil {
		t.Fatalf("publish after discovery completed: %v", pubErr)
	}
	healed := h.latestWorkflowDeployment(wfID)
	if healed.ID == baseline.ID {
		t.Fatal("no redeploy recorded after the package became ready")
	}
	if healed.Status != DeployStatusSuccess || healed.CreatedBy != "uploader" {
		t.Fatalf("healed redeploy = status %q by %q, want success by the outbox event's user", healed.Status, healed.CreatedBy)
	}
	if healed.WorkflowConfigSnapshot == nil || !strings.Contains(*healed.WorkflowConfigSnapshot, "e2e_wf_tools__run") {
		t.Fatal("healed snapshot missing the hydrated toolset tool entry")
	}
}
