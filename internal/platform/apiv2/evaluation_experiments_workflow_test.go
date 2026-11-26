// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

func newWorkflowEvalsFixture(t *testing.T, workflowEvals bool) experimentFixture {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyWorkflowEvals), strconv.FormatBool(workflowEvals))
	yamlState, err := entplatform.BuildRBACYAMLState(oidcauth.AuthorizationServiceConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("build rbac yaml state: %v", err)
	}
	core, deps := newTestCoreWithConfig(t, nil, func(d *entplatform.RouteDeps) { d.RBACYAMLState = yamlState })
	if err := deps.RBACComposer.Rebuild(t.Context()); err != nil {
		t.Fatalf("build rbac composer: %v", err)
	}
	api := &entrypointAPI{apiClient: newAPIClient(t, core), gateway: &fakeGateway{}}
	f := experimentFixture{api: api, datasetID: createDataset(api, "experiment-fixture"), evaluators: builtinEvaluatorIDs(api)}
	workflows := store.NewWorkflowRepo(f.api.core.DB())
	for _, name := range []string{"order_flow", "order_flow_v2"} {
		if err := workflows.Create(t.Context(), &store.Workflow{Name: name, AppConfig: `{}`, CreatedBy: "alice", UpdatedBy: "alice"}, store.WorkflowBindings{}); err != nil {
			t.Fatalf("create workflow %s: %v", name, err)
		}
	}
	return f
}

func (f experimentFixture) workflowBody(name, workflow string) map[string]any {
	body := f.body(name, "")
	delete(body, "targetAgent")
	body["targetWorkflow"] = workflow
	return body
}

func expectTarget(t *testing.T, rec *httptest.ResponseRecorder, kind, name string) {
	t.Helper()
	raw := rawData(t, rec)
	if raw["targetType"] != kind {
		t.Errorf("targetType = %v, want %s", raw["targetType"], kind)
	}
	if kind == "workflow" {
		if raw["targetWorkflow"] != name || raw["targetAgent"] != "" {
			t.Errorf("targetWorkflow = %v, targetAgent = %v; want %q and \"\"", raw["targetWorkflow"], raw["targetAgent"], name)
		}
		return
	}
	if raw["targetAgent"] != name {
		t.Errorf("targetAgent = %v, want %q", raw["targetAgent"], name)
	}
	expectNull(t, raw, "targetWorkflow")
}

func TestExperiments_WorkflowTarget(t *testing.T) {
	f := newWorkflowEvalsFixture(t, true)
	created := expectCreated[Experiment](t, f.api.call(http.MethodPost, experimentsPath, f.workflowBody("workflow-experiment", "order_flow")), "evaluations/experiments")

	expectTarget(t, f.api.call(http.MethodGet, experimentsPath+"/"+created.ID, nil), "workflow", "order_flow")

	renamed := f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"name": "renamed-workflow-experiment"})
	expectStatus(t, renamed, http.StatusOK)
	expectTarget(t, renamed, "workflow", "order_flow")

	retargeted := f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"targetWorkflow": "order_flow_v2"})
	expectStatus(t, retargeted, http.StatusOK)
	expectTarget(t, retargeted, "workflow", "order_flow_v2")

	toAgent := f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"targetAgent": "support_agent"})
	expectStatus(t, toAgent, http.StatusOK)
	expectTarget(t, toAgent, "agent", "support_agent")
}

func TestExperiments_AgentTargetUnchanged(t *testing.T) {
	f := newWorkflowEvalsFixture(t, true)
	created := f.create("agent-experiment", "support_agent")

	expectTarget(t, f.api.call(http.MethodGet, experimentsPath+"/"+created.ID, nil), "agent", "support_agent")

	toWorkflow := f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"targetWorkflow": "order_flow"})
	expectStatus(t, toWorkflow, http.StatusOK)
	expectTarget(t, toWorkflow, "workflow", "order_flow")
}

func TestExperiments_WorkflowTargetRefusedWithTheFlagOff(t *testing.T) {
	f := newWorkflowEvalsFixture(t, false)
	expectDetailSaying(t, f.api.call(http.MethodPost, experimentsPath, f.workflowBody("workflow-experiment", "order_flow")),
		"targetWorkflow", "SAM_FEATURE_WORKFLOW_EVALS")

	created := f.create("agent-experiment", "support_agent")
	expectDetailSaying(t, f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"targetWorkflow": "order_flow"}),
		"targetWorkflow", "SAM_FEATURE_WORKFLOW_EVALS")
	expectTarget(t, f.api.call(http.MethodGet, experimentsPath+"/"+created.ID, nil), "agent", "support_agent")
}

func TestExperiments_TargetValidation(t *testing.T) {
	f := newWorkflowEvalsFixture(t, true)
	both := f.body("both-targets", "support_agent")
	both["targetWorkflow"] = "order_flow"
	expectDetailSaying(t, f.api.call(http.MethodPost, experimentsPath, both), "targetAgent", "not both")

	created := f.create("target-validation", "support_agent")
	path := experimentsPath + "/" + created.ID
	expectDetailSaying(t, f.api.call(http.MethodPatch, path, map[string]any{"targetAgent": "a", "targetWorkflow": "w"}), "targetAgent", "not both")
	expectDetailSaying(t, f.api.call(http.MethodPatch, path, map[string]any{"targetWorkflow": nil}), "targetWorkflow", "cannot be empty")
	expectDetailSaying(t, f.api.call(http.MethodPatch, path, map[string]any{"targetWorkflow": ""}), "targetWorkflow", "cannot be empty")
}

func TestExperiments_WorkflowTargetCheckedOnSave(t *testing.T) {
	f := newWorkflowEvalsFixture(t, true)

	expectDetailSaying(t, f.api.call(http.MethodPost, experimentsPath, f.workflowBody("unknown-workflow", "nope")),
		"targetWorkflow", `no workflow named "nope"`)

	noGrant := f.workflowBody("no-grant", "order_flow")
	noGrant["runAs"] = "orders-bot"
	expectDetailSaying(t, f.api.call(http.MethodPost, experimentsPath, noGrant), "runAs", "cannot invoke workflow")

	f.api.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "yaml_flow", Capabilities: a2a.AgentCapabilities{
		Extensions: []a2a.AgentExtension{{URI: a2a.ExtensionURIAgentType, Params: map[string]any{"type": "workflow"}}},
	}})
	created := f.api.call(http.MethodPost, experimentsPath, f.workflowBody("yaml-workflow", "yaml_flow"))
	expectStatus(t, created, http.StatusCreated)
	expectTarget(t, created, "workflow", "yaml_flow")
}

func TestRuns_WorkflowConfigSnapshot(t *testing.T) {
	const inputSchema = `{"properties":{"orderId":{"type":"string"}},"required":["orderId"],"type":"object"}`
	f := newWorkflowEvalsFixture(t, true)
	f.exec(`UPDATE workflows SET app_config = $1 WHERE name = 'order_flow'`, `{"input_schema":`+inputSchema+`}`)
	dataset := newDataset(f.api, "workflow-snapshot-examples")
	newExample(f.api, dataset.ID, map[string]any{
		"inputData":     map[string]any{"orderId": "O-5003"},
		"expectedPath":  map[string]any{"nodes": []map[string]any{{"nodeId": "approve", "expectation": "succeeded"}}, "tools": []map[string]any{{"toolName": "issue_refund", "expectation": "called"}}},
		"expectedFacts": map[string]any{"required": []string{"O-5003"}},
	})
	body := f.workflowBody("workflow-snapshot", "order_flow")
	body["datasetId"] = dataset.ID
	experiment := expectCreated[Experiment](t, f.api.call(http.MethodPost, experimentsPath, body), "evaluations/experiments")
	run := f.groupRuns(f.trigger(experiment.ID, map[string]any{}).ID)[0]

	rec := f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	expectNull(t, rawData(t, rec)["configSnapshot"].(map[string]any), "agent")

	snapshot := decodeData[Run](t, rec).ConfigSnapshot
	if snapshot.Workflow == nil || snapshot.Workflow.Name != "order_flow" || snapshot.Workflow.InstanceName == nil || *snapshot.Workflow.InstanceName == "" ||
		string(snapshot.Workflow.InputSchema) != inputSchema {
		t.Fatalf("workflow snapshot = %+v, want order_flow with the name it listens on and its input schema", snapshot.Workflow)
	}
	examples := snapshot.Dataset.Examples
	if len(examples) != 1 {
		t.Fatalf("examples = %+v, want the workflow example", examples)
	}
	workflow := examples[0]
	wantPath := ExpectedPath{Nodes: []ExpectedNode{{NodeID: "approve", Expectation: "succeeded"}}, Tools: []ExpectedTool{{ToolName: "issue_refund", Expectation: "called"}}}
	if string(workflow.InputData) != `{"orderId":"O-5003"}` || workflow.ExpectedPath == nil || !reflect.DeepEqual(*workflow.ExpectedPath, wantPath) ||
		workflow.ExpectedFacts == nil || !slices.Equal(workflow.ExpectedFacts.Required, []string{"O-5003"}) || workflow.ExpectedFacts.Forbidden == nil {
		t.Errorf("workflow example = input %s path %+v facts %+v, want what the example holds, with lists never null", workflow.InputData, workflow.ExpectedPath, workflow.ExpectedFacts)
	}
}

func TestWatchlist_WorkflowEntry(t *testing.T) {
	f := newWorkflowEvalsFixture(t, true)
	replaced := replaceWatchlist(f.api, "order_flow")
	expectStatus(t, replaced, http.StatusOK)

	for path, rec := range map[string]*httptest.ResponseRecorder{
		"PUT " + watchlistPath: replaced,
		watchlistPath:          f.api.call(http.MethodGet, watchlistPath, nil),
		watchlistChartPath:     f.api.call(http.MethodGet, watchlistChartPath, nil),
	} {
		expectStatus(t, rec, http.StatusOK)
		entry := decodeList[map[string]any](t, rec).Data[0]
		if entry["agentName"] != "order_flow" || entry["targetType"] != "workflow" || entry["isBuiltIn"] != false {
			t.Errorf("%s entry = %v, want the workflow order_flow", path, entry)
		}
		expectNull(t, entry, "resolvedModelProvider")
	}
}

func TestWatchlist_WorkflowNameWithTheFlagOff(t *testing.T) {
	f := newWorkflowEvalsFixture(t, false)
	expectStatus(t, replaceWatchlist(f.api, "order_flow"), http.StatusOK)

	for _, path := range []string{watchlistPath, watchlistChartPath} {
		rec := f.api.call(http.MethodGet, path, nil)
		expectStatus(t, rec, http.StatusOK)
		entry := decodeList[map[string]any](t, rec).Data[0]
		if entry["agentName"] != "order_flow" || entry["targetType"] != "agent" || entry["resolvedModelProvider"] != "general" {
			t.Errorf("%s entry = %v, want the agent order_flow under general", path, entry)
		}
	}
}
