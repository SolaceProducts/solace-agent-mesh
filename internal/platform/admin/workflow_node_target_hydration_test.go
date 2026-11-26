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
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type fakeWorkflowResolver map[string]*store.Workflow

func (f fakeWorkflowResolver) GetByName(_ context.Context, name string) (*store.Workflow, error) {
	return f[name], nil
}

func nodeByID(t *testing.T, appConfig map[string]any, id string) map[string]any {
	t.Helper()
	wfCfg, _ := appConfig["workflow"].(map[string]any)
	nodes, _ := wfCfg["nodes"].([]any)
	for _, raw := range nodes {
		n, _ := raw.(map[string]any)
		if n != nil && n["id"] == id {
			return n
		}
	}
	t.Fatalf("node %q not found", id)
	return nil
}

func wrappedWith(nodes ...map[string]any) map[string]any {
	anyNodes := make([]any, len(nodes))
	for i, n := range nodes {
		anyNodes[i] = n
	}
	return map[string]any{"workflow": map[string]any{"nodes": anyNodes}}
}

func TestHydrateWorkflowNodeTargets_MissLogIdentifiesParentAndNode(t *testing.T) {
	t.Parallel()
	appConfig := wrappedWith(
		map[string]any{"id": "detect_1", "type": "workflow", "workflow_name": "MissingChild"},
	)
	appConfig["display_name"] = "Four Cams"

	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	if err := hydrateWorkflowNodeTargets(context.Background(), appConfig, fakeWorkflowResolver{}, logger); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	logged := logBuf.String()
	for _, want := range []string{"MissingChild", "detect_1", "Four Cams"} {
		if !strings.Contains(logged, want) {
			t.Errorf("miss warning must name %q so the operator can find it; got:\n%s", want, logged)
		}
	}
}

func TestHydrateWorkflowNodeTargets_RewritesResourceNameToInstanceName(t *testing.T) {
	t.Parallel()
	const childID = "019f910e-7e42-737a-87eb-c300c9672a4e"
	resolver := fakeWorkflowResolver{
		"DetectCountsFromUrl": &store.Workflow{ID: childID, Name: "DetectCountsFromUrl"},
	}
	appConfig := wrappedWith(
		map[string]any{"id": "detect_1", "type": "workflow", "workflow_name": "DetectCountsFromUrl"},
	)

	if err := hydrateWorkflowNodeTargets(context.Background(), appConfig, resolver, slog.Default()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	want := a2a.InstanceName(a2a.KindWorkflow, childID)
	if got := nodeByID(t, appConfig, "detect_1")["workflow_name"]; got != want {
		t.Errorf("workflow_name = %v, want %v", got, want)
	}
}

func TestHydrateWorkflowNodeTargets_UnknownNameLeftVerbatim(t *testing.T) {
	t.Parallel()
	appConfig := wrappedWith(
		map[string]any{"id": "detect_1", "type": "workflow", "workflow_name": "NotDeployedYet"},
	)

	if err := hydrateWorkflowNodeTargets(context.Background(), appConfig, fakeWorkflowResolver{}, slog.Default()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	if got := nodeByID(t, appConfig, "detect_1")["workflow_name"]; got != "NotDeployedYet" {
		t.Errorf("workflow_name = %v, want unchanged NotDeployedYet", got)
	}
}

func TestHydrateWorkflowNodeTargets_Idempotent(t *testing.T) {
	t.Parallel()
	const childID = "019f910e-7e42-737a-87eb-c300c9672a4e"
	already := a2a.InstanceName(a2a.KindWorkflow, childID)
	appConfig := wrappedWith(
		map[string]any{"id": "detect_1", "type": "workflow", "workflow_name": already},
	)

	if err := hydrateWorkflowNodeTargets(context.Background(), appConfig, fakeWorkflowResolver{}, slog.Default()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	if got := nodeByID(t, appConfig, "detect_1")["workflow_name"]; got != already {
		t.Errorf("workflow_name = %v, want unchanged %v", got, already)
	}
}

func TestHydrateWorkflowNodeTargets_NonWorkflowNodesUntouched(t *testing.T) {
	t.Parallel()
	const childID = "019f910e-7e42-737a-87eb-c300c9672a4e"
	resolver := fakeWorkflowResolver{
		"DetectCountsFromUrl": &store.Workflow{ID: childID, Name: "DetectCountsFromUrl"},
	}
	appConfig := wrappedWith(
		map[string]any{"id": "a", "type": "agent", "agent_name": "DetectCountsFromUrl"},
		map[string]any{"id": "t", "type": "tool", "tool_name": "detect_objects"},
		map[string]any{"id": "w", "type": "workflow", "workflow_name": "DetectCountsFromUrl"},
	)

	if err := hydrateWorkflowNodeTargets(context.Background(), appConfig, resolver, slog.Default()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	if got := nodeByID(t, appConfig, "a")["agent_name"]; got != "DetectCountsFromUrl" {
		t.Errorf("agent node agent_name rewritten to %v; must be untouched", got)
	}
	if got := nodeByID(t, appConfig, "t")["tool_name"]; got != "detect_objects" {
		t.Errorf("tool node tool_name changed to %v; must be untouched", got)
	}
	want := a2a.InstanceName(a2a.KindWorkflow, childID)
	if got := nodeByID(t, appConfig, "w")["workflow_name"]; got != want {
		t.Errorf("workflow node workflow_name = %v, want %v", got, want)
	}
}

type erroringWorkflowResolver struct{ err error }

func (e erroringWorkflowResolver) GetByName(_ context.Context, _ string) (*store.Workflow, error) {
	return nil, e.err
}

func TestHydrateWorkflowNodeTargets_StoreErrorIsFatal(t *testing.T) {
	t.Parallel()
	resolver := erroringWorkflowResolver{err: errors.New("connection reset by peer")}
	appConfig := wrappedWith(
		map[string]any{"id": "detect_1", "type": "workflow", "workflow_name": "DetectCountsFromUrl"},
	)

	err := hydrateWorkflowNodeTargets(context.Background(), appConfig, resolver, slog.Default())

	if err == nil {
		t.Fatal("a store lookup failure must be returned so the deploy fails rather than baking in an unresolved target")
	}
	if !strings.Contains(err.Error(), "DetectCountsFromUrl") {
		t.Errorf("error should name the target being resolved, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "connection reset by peer") {
		t.Errorf("error should wrap the underlying store error, got %q", err.Error())
	}
	if got := nodeByID(t, appConfig, "detect_1")["workflow_name"]; got != "DetectCountsFromUrl" {
		t.Errorf("workflow_name should be left untouched on a failed lookup, got %v", got)
	}
}

func TestHydrateWorkflowNodeTargets_NotFoundIsNotFatal(t *testing.T) {
	t.Parallel()
	resolver := fakeWorkflowResolver{}
	appConfig := wrappedWith(
		map[string]any{"id": "detect_1", "type": "workflow", "workflow_name": "NotCreatedYet"},
	)

	if err := hydrateWorkflowNodeTargets(context.Background(), appConfig, resolver, slog.Default()); err != nil {
		t.Fatalf("a not-yet-created target must not fail the deploy, got %v", err)
	}
	if got := nodeByID(t, appConfig, "detect_1")["workflow_name"]; got != "NotCreatedYet" {
		t.Errorf("workflow_name should be left verbatim, got %v", got)
	}
}

func TestReconcilerGenerateDeployableWorkflowYAML_HydratesWorkflowNodeTarget(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)

	child := &store.Workflow{Name: "DetectCountsFromUrl", AppConfig: `{"nodes":[]}`, CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(t.Context(), child, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create child workflow: %v", err)
	}

	stored, _ := json.Marshal(map[string]any{
		"output_mapping": map[string]any{"result": "x"},
		"nodes": []any{
			map[string]any{"id": "detect_1", "type": "workflow", "workflow_name": "DetectCountsFromUrl"},
		},
	})
	parent := &store.Workflow{Name: "DetectFourImages", AppConfig: string(stored), CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(t.Context(), parent, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create parent workflow: %v", err)
	}

	r := &Reconciler{workflowRepo: workflowRepo, namespace: "ns"}
	yamlStr, err := r.generateDeployableWorkflowYAML(t.Context(), parent)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	want := a2a.InstanceName(a2a.KindWorkflow, child.ID)
	if !strings.Contains(yamlStr, want) {
		t.Errorf("reconciler YAML missing the hydrated instance name %q:\n%s", want, yamlStr)
	}
	if strings.Contains(yamlStr, "workflow_name: DetectCountsFromUrl") {
		t.Error("reconciler YAML still carries the authored resource name; the child subscribes under its instance name")
	}
}

func TestE2E_WorkflowDeploySnapshotHydratesWorkflowNodeTarget(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	const childName = "e2e_hydration_child_workflow"
	childID := h.createWorkflow(childName, map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.start.output}}"},
		"nodes": []any{
			map[string]any{"id": "start", "type": "agent", "agent_name": "test_agent"},
		},
	})

	parentID := h.createWorkflow("e2e_hydration_parent_workflow", map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.detect_1.output}}"},
		"nodes": []any{
			map[string]any{"id": "detect_1", "type": "workflow", "workflow_name": childName},
		},
	})

	deployed := h.deployWorkflow(parentID)

	dep, err := h.wfDeployRepo.GetByID(h.ctx, deployed.ID)
	if err != nil {
		t.Fatalf("get workflow deployment: %v", err)
	}
	if dep == nil || dep.WorkflowConfigSnapshot == nil {
		t.Fatal("deploy recorded no config snapshot; the snapshot must be frozen before the control-plane RPC")
	}
	snapshot := *dep.WorkflowConfigSnapshot

	want := a2a.InstanceName(a2a.KindWorkflow, childID)
	if !strings.Contains(snapshot, "workflow_name: "+want) {
		t.Errorf("deploy snapshot missing the hydrated child instance name %q:\n%s", want, snapshot)
	}
	if strings.Contains(snapshot, "workflow_name: "+childName) {
		t.Errorf("deploy snapshot froze the authored resource name %q; nothing subscribes to it and the node fails at dispatch:\n%s", childName, snapshot)
	}
}
