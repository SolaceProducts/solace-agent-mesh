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

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

type fakeAgentResolver map[string]*store.Agent

func (f fakeAgentResolver) GetByName(_ context.Context, name string) (*store.Agent, error) {
	return f[name], nil
}

type erroringAgentResolver struct{}

func (erroringAgentResolver) GetByName(context.Context, string) (*store.Agent, error) {
	return nil, errors.New("database is down")
}

const briefingAgentID = "019f910e-7e42-737a-87eb-c300c9672a4e"

func TestHydrateAgentNodeTargets_StampsInstanceName(t *testing.T) {
	t.Parallel()
	resolver := fakeAgentResolver{
		"Account Briefer": &store.Agent{ID: briefingAgentID, Name: "Account Briefer"},
	}
	appConfig := wrappedWith(
		map[string]any{"id": "write_up", "type": "agent", "agent_name": "Account Briefer"},
	)

	if err := hydrateAgentNodeTargets(context.Background(), appConfig, resolver, slog.Default()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	node := nodeByID(t, appConfig, "write_up")
	if want := a2a.InstanceName(a2a.KindAgent, briefingAgentID); node["agent_instance_name"] != want {
		t.Errorf("agent_instance_name = %v, want %v", node["agent_instance_name"], want)
	}
	if node["agent_name"] != "Account Briefer" {
		t.Errorf("agent_name = %v, want it left as authored", node["agent_name"])
	}
}

func TestHydrateAgentNodeTargets_UnknownNameGetsNoHint(t *testing.T) {
	t.Parallel()
	appConfig := wrappedWith(
		map[string]any{"id": "write_up", "type": "agent", "agent_name": "Not Created Yet"},
	)
	appConfig["display_name"] = "AccountHealthOne"
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	if err := hydrateAgentNodeTargets(context.Background(), appConfig, fakeAgentResolver{}, logger); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	if _, ok := nodeByID(t, appConfig, "write_up")["agent_instance_name"]; ok {
		t.Error("an unresolved target must not carry a hint")
	}
	for _, want := range []string{"Not Created Yet", "write_up", "AccountHealthOne"} {
		if !strings.Contains(logBuf.String(), want) {
			t.Errorf("miss warning must name %q; got:\n%s", want, logBuf.String())
		}
	}
}

func TestHydrateAgentNodeTargets_InstanceNameTargetSkipped(t *testing.T) {
	t.Parallel()
	already := a2a.InstanceName(a2a.KindAgent, briefingAgentID)
	appConfig := wrappedWith(
		map[string]any{"id": "write_up", "type": "agent", "agent_name": already},
	)

	if err := hydrateAgentNodeTargets(context.Background(), appConfig, erroringAgentResolver{}, slog.Default()); err != nil {
		t.Fatalf("hydrate looked up an instance-name target: %v", err)
	}

	node := nodeByID(t, appConfig, "write_up")
	if _, ok := node["agent_instance_name"]; ok || node["agent_name"] != already {
		t.Errorf("instance-name target must be left untouched; got %v", node)
	}
}

func TestHydrateAgentNodeTargets_NonAgentNodesUntouched(t *testing.T) {
	t.Parallel()
	resolver := fakeAgentResolver{
		"Account Briefer": &store.Agent{ID: briefingAgentID, Name: "Account Briefer"},
	}
	appConfig := wrappedWith(
		map[string]any{"id": "child", "type": "workflow", "workflow_name": "Account Briefer", "agent_name": "Account Briefer"},
		map[string]any{"id": "lookup", "type": "tool", "tool_name": "Account Briefer", "agent_name": "Account Briefer"},
	)

	if err := hydrateAgentNodeTargets(context.Background(), appConfig, resolver, slog.Default()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	for _, id := range []string{"child", "lookup"} {
		if _, ok := nodeByID(t, appConfig, id)["agent_instance_name"]; ok {
			t.Errorf("node %q is not an agent node and must not be stamped", id)
		}
	}
}

func TestHydrateAgentNodeTargets_StampsEveryAgentNode(t *testing.T) {
	t.Parallel()
	const otherAgentID = "019f910e-7e42-737a-87eb-c300c9672a4f"
	resolver := fakeAgentResolver{
		"Account Briefer": &store.Agent{ID: briefingAgentID, Name: "Account Briefer"},
		"Risk Scorer":     &store.Agent{ID: otherAgentID, Name: "Risk Scorer"},
	}
	brokerName := a2a.InstanceName(a2a.KindAgent, otherAgentID)
	appConfig := wrappedWith(
		map[string]any{"id": "missing", "type": "agent", "agent_name": "Not Created Yet"},
		map[string]any{"id": "routable", "type": "agent", "agent_name": brokerName},
		map[string]any{"id": "fan_out", "type": "map", "node": "brief_each"},
		map[string]any{"id": "brief_each", "type": "agent", "agent_name": "Account Briefer"},
		map[string]any{"id": "retry", "type": "loop", "node": "score"},
		map[string]any{"id": "score", "type": "agent", "agent_name": "Risk Scorer"},
		map[string]any{"id": "write_up", "type": "agent", "agent_name": "Account Briefer"},
	)

	if err := hydrateAgentNodeTargets(context.Background(), appConfig, resolver, slog.Default()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	want := map[string]any{
		"missing":    nil,
		"routable":   nil,
		"brief_each": a2a.InstanceName(a2a.KindAgent, briefingAgentID),
		"score":      brokerName,
		"write_up":   a2a.InstanceName(a2a.KindAgent, briefingAgentID),
	}
	for id, w := range want {
		if got := nodeByID(t, appConfig, id)["agent_instance_name"]; got != w {
			t.Errorf("node %q agent_instance_name = %v, want %v", id, got, w)
		}
	}
}

func TestHydrateAgentNodeTargets_ClearsAuthoredHint(t *testing.T) {
	t.Parallel()
	forged := a2a.InstanceName(a2a.KindAgent, "019f910e-7e42-737a-87eb-c300c9672a50")
	appConfig := wrappedWith(
		map[string]any{"id": "missing", "type": "agent", "agent_name": "Not Created Yet", "agent_instance_name": forged},
		map[string]any{"id": "routable", "type": "agent", "agent_name": a2a.InstanceName(a2a.KindAgent, briefingAgentID), "agent_instance_name": forged},
	)

	if err := hydrateAgentNodeTargets(context.Background(), appConfig, fakeAgentResolver{}, slog.Default()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	for _, id := range []string{"missing", "routable"} {
		if got, ok := nodeByID(t, appConfig, id)["agent_instance_name"]; ok {
			t.Errorf("node %q kept authored agent_instance_name %v", id, got)
		}
	}
}

func TestHydrateAgentNodeTargets_StoreErrorIsFatal(t *testing.T) {
	t.Parallel()
	appConfig := wrappedWith(
		map[string]any{"id": "write_up", "type": "agent", "agent_name": "Account Briefer"},
	)

	if err := hydrateAgentNodeTargets(context.Background(), appConfig, erroringAgentResolver{}, slog.Default()); err == nil {
		t.Fatal("store error must fail hydration")
	}
}

func TestReconcilerGenerateDeployableWorkflowYAML_StampsAgentNodeTarget(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)
	agentRepo := store.NewAgentRepo(db)

	agent := &store.Agent{Name: "Account Briefer", Type: "standard", CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(t.Context(), agent, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	stored, _ := json.Marshal(map[string]any{
		"output_mapping": map[string]any{"result": "x"},
		"nodes": []any{
			map[string]any{"id": "write_up", "type": "agent", "agent_name": "Account Briefer"},
		},
	})
	wf := &store.Workflow{Name: "AccountHealthOne", AppConfig: string(stored), CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(t.Context(), wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	r := &Reconciler{workflowRepo: workflowRepo, agentRepo: agentRepo, namespace: "ns"}
	yamlStr, err := r.generateDeployableWorkflowYAML(t.Context(), wf)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if want := "agent_instance_name: " + AgentInstanceName(agent); !strings.Contains(yamlStr, want) {
		t.Errorf("reconciler YAML missing %q:\n%s", want, yamlStr)
	}
}

func TestReconcilerGenerateDeployableWorkflowYAML_AgentStoreErrorFails(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)
	stored, _ := json.Marshal(map[string]any{
		"output_mapping": map[string]any{"result": "x"},
		"nodes": []any{
			map[string]any{"id": "write_up", "type": "agent", "agent_name": "Account Briefer"},
		},
	})
	wf := &store.Workflow{Name: "AccountHealthOne", AppConfig: string(stored), CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(t.Context(), wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	r := &Reconciler{workflowRepo: workflowRepo, agentRepo: store.NewAgentRepo(closedTestDB(t)), namespace: "ns"}
	if _, err := r.generateDeployableWorkflowYAML(t.Context(), wf); err == nil || !strings.Contains(err.Error(), "resolve agent node targets") {
		t.Fatalf("generate err = %v, want an agent-target resolution error", err)
	}
}

func TestWorkflowDeploy_AgentStoreErrorFailsDeploy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	wfID := h.createWorkflow("e2e_agent_hint_store_error", map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.write_up.output}}"},
		"nodes": []any{
			map[string]any{"id": "write_up", "type": "agent", "agent_name": "Account Briefer"},
		},
	})
	svc := NewWorkflowDeploymentService(
		h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, store.NewAgentRepo(closedTestDB(t)),
		newMemResourceStore(), h.controlClient, h.namespace, slog.Default(),
	)
	h.useHarnessAWEs(svc)

	resp, err := svc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: wfID, Action: DeployActionDeploy}, "e2e-user")
	if err != nil {
		t.Fatalf("deploy call errored: %v", err)
	}
	if resp.Status != DeployStatusFailed || !strings.Contains(resp.ErrorMessage, "resolve agent node targets") {
		t.Fatalf("deploy status = %q (%s), want %q with an agent-target resolution error", resp.Status, resp.ErrorMessage, DeployStatusFailed)
	}
}

func closedTestDB(t *testing.T) *store.DB {
	t.Helper()
	db := testOpenDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	return db
}

func TestE2E_WorkflowDeploySnapshotStampsAgentNodeTarget(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	agentID := h.createAgent("Account Briefer", "Write a short account briefing.")
	wfID := h.createWorkflow("e2e_agent_hint_workflow", map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.write_up.output}}"},
		"nodes": []any{
			map[string]any{"id": "write_up", "type": "agent", "agent_name": "Account Briefer"},
		},
	})

	deployed := h.deployWorkflow(wfID)

	dep, err := h.wfDeployRepo.GetByID(h.ctx, deployed.ID)
	if err != nil {
		t.Fatalf("get workflow deployment: %v", err)
	}
	if dep == nil || dep.WorkflowConfigSnapshot == nil {
		t.Fatal("deploy recorded no config snapshot")
	}
	snapshot := *dep.WorkflowConfigSnapshot

	if want := "agent_instance_name: " + a2a.InstanceName(a2a.KindAgent, agentID); !strings.Contains(snapshot, want) {
		t.Errorf("deploy snapshot missing %q:\n%s", want, snapshot)
	}
	if !strings.Contains(snapshot, "agent_name: Account Briefer") {
		t.Errorf("deploy snapshot must keep the authored agent_name:\n%s", snapshot)
	}

	var doc struct {
		Apps []struct {
			AppConfig map[string]any `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err = yaml.Unmarshal([]byte(snapshot), &doc); err != nil || len(doc.Apps) != 1 {
		t.Fatalf("unmarshal snapshot (apps=%d): %v", len(doc.Apps), err)
	}
	wfCfg, _ := doc.Apps[0].AppConfig["workflow"].(map[string]any)
	cfg, err := workflow.ParseWorkflowConfig("e2e_agent_hint_workflow", "ns", wfCfg)
	if err != nil {
		t.Fatalf("runtime parse of deploy snapshot: %v", err)
	}
	if got, want := cfg.Nodes[0].AgentInstanceName, a2a.InstanceName(a2a.KindAgent, agentID); got != want {
		t.Errorf("runtime parsed AgentInstanceName = %q, want %q", got, want)
	}
}
