// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type fakeWorkflows map[string]*WorkflowTarget

func (f fakeWorkflows) ResolveWorkflow(_ context.Context, name string) (*WorkflowTarget, error) {
	return f[name], nil
}

type fakeInvoke map[string]bool

func (f fakeInvoke) CanInvokeWorkflow(_ context.Context, principal, _ string) (bool, error) {
	return f[principal], nil
}

func orderSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"required":   []any{"order_id"},
		"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
	}
}

func orderHandler() fakeWorkflows {
	return fakeWorkflows{"order_handler": {InstanceName: "workflow_1234", InputSchema: orderSchema()}}
}

type recordingInvoke struct {
	allowed fakeInvoke
	asked   []string
}

func (r *recordingInvoke) CanInvokeWorkflow(ctx context.Context, principal, instanceName string) (bool, error) {
	r.asked = append(r.asked, principal)
	return r.allowed.CanInvokeWorkflow(ctx, principal, instanceName)
}

func newWorkflowTargetSvcs(t *testing.T, workflows fakeWorkflows) (context.Context, *evalSvcSet, *WorkflowTargetValidator) {
	t.Helper()
	return newWorkflowTargetSvcsWith(t, workflows, fakeInvoke{rbac.EvalSystemUserSub: true}, nil)
}

func newWorkflowTargetSvcsWith(t *testing.T, workflows fakeWorkflows, invoke WorkflowInvokeChecker, logger *slog.Logger) (context.Context, *evalSvcSet, *WorkflowTargetValidator) {
	t.Helper()
	ctx, svcs := newEvalSvcs(t)
	validator := NewWorkflowTargetValidator(workflows, invoke, svcs.examples, logger)
	svcs.experiment.SetFeatureRegistry(workflowEvalsFlag(true))
	withWorkflowEvals(svcs)
	svcs.experiment.SetWorkflowTargetValidator(validator)
	return ctx, svcs, validator
}

func mustCreatePromptExample(ctx context.Context, t *testing.T, svcs *evalSvcSet, datasetID, prompt string) {
	t.Helper()
	if _, err := svcs.example.Create(ctx, datasetID, CreateExampleRequest{Prompt: prompt}); err != nil {
		t.Fatalf("create prompt example: %v", err)
	}
}

func asAgentExperiment(req CreateExperimentRequest, name string) CreateExperimentRequest {
	req.Name, req.TargetWorkflow, req.TargetAgent = name, "", "OrderDesk"
	return req
}

func workflowExperimentRequest(ctx context.Context, t *testing.T, svcs *evalSvcSet, datasetID string) CreateExperimentRequest {
	t.Helper()
	return CreateExperimentRequest{
		Name:           "order-handler-regression",
		DatasetID:      datasetID,
		TargetWorkflow: "order_handler",
		EvaluatorIDs:   []string{mustCreateLLMJudge(ctx, t, svcs.evaluator, "judge")},
	}
}

func TestCreateWorkflowExperimentIsCheckedAgainstTheWorkflow(t *testing.T) {
	t.Run("examples that fit the schema are accepted", func(t *testing.T) {
		ctx, svcs, _ := newWorkflowTargetSvcs(t, orderHandler())
		dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")
		mustCreateInputExample(ctx, t, svcs, dsID, map[string]any{"order_id": "O-5003"})

		if _, err := svcs.experiment.Create(ctx, workflowExperimentRequest(ctx, t, svcs, dsID), "u"); err != nil {
			t.Fatalf("create: %v", err)
		}
	})

	t.Run("an unknown workflow is refused", func(t *testing.T) {
		ctx, svcs, _ := newWorkflowTargetSvcs(t, fakeWorkflows{})
		dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")

		_, err := svcs.experiment.Create(ctx, workflowExperimentRequest(ctx, t, svcs, dsID), "u")

		if ve := asValidationError(t, err); ve.Field != "target_workflow" || !strings.Contains(ve.Message, "order_handler") {
			t.Errorf("error = %s: %q, want target_workflow naming the workflow", ve.Field, ve.Message)
		}
	})

	t.Run("a principal that cannot invoke the workflow is refused", func(t *testing.T) {
		ctx, svcs, _ := newWorkflowTargetSvcs(t, orderHandler())
		dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")
		req := workflowExperimentRequest(ctx, t, svcs, dsID)
		req.RunAs = new("orders-bot")

		_, err := svcs.experiment.Create(ctx, req, "u")

		ve := asValidationError(t, err)
		if ve.Field != "run_as" || !strings.Contains(ve.Message, "system:orders-bot") || !strings.Contains(ve.Message, "workflow:") {
			t.Errorf("error = %s: %q, want run_as naming the principal and the scope it lacks", ve.Field, ve.Message)
		}
	})

	t.Run("every example that does not fit is named with its field", func(t *testing.T) {
		ctx, svcs, _ := newWorkflowTargetSvcs(t, orderHandler())
		dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")
		mustCreateInputExample(ctx, t, svcs, dsID, map[string]any{"order_id": "O-1"})
		mustCreateInputExample(ctx, t, svcs, dsID, map[string]any{"order_id": 42})
		if _, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "no structured input"}); err != nil {
			t.Fatalf("create prompt example: %v", err)
		}

		_, err := svcs.experiment.Create(ctx, workflowExperimentRequest(ctx, t, svcs, dsID), "u")

		ve := asValidationError(t, err)
		for _, field := range []string{"examples[2].inputData", "examples[3].inputData"} {
			if len(ve.Details[field]) == 0 {
				t.Errorf("details %v missing %s", ve.Details, field)
			}
		}
		if _, ok := ve.Details["examples[1].inputData"]; ok {
			t.Errorf("the example that fits was reported: %v", ve.Details["examples[1].inputData"])
		}
		if !strings.Contains(strings.Join(ve.Details["examples[3].inputData"], " "), "order_id") {
			t.Errorf("a missing required field is not named: %v", ve.Details["examples[3].inputData"])
		}
		if ve.Message != "2 examples cannot run against this workflow" {
			t.Errorf("message = %q, want a count, with the problems left to the details", ve.Message)
		}
	})

	t.Run("an example with an input file is refused", func(t *testing.T) {
		ctx, svcs, _ := newWorkflowTargetSvcs(t, fakeWorkflows{"order_handler": {InstanceName: "workflow_1234"}})
		dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")
		ex := mustCreateInputExample(ctx, t, svcs, dsID, map[string]any{"order_id": "O-1"})
		if err := svcs.artifacts.Upsert(ctx, &store.EvalExampleArtifact{
			ExampleID: ex.ID, Role: store.EvalArtifactRoleInput, Filename: "order.pdf", MIMEType: "application/pdf",
			SessionID: "s", ContentHash: "sha256:00",
		}, nil); err != nil {
			t.Fatalf("attach input file: %v", err)
		}

		_, err := svcs.experiment.Create(ctx, workflowExperimentRequest(ctx, t, svcs, dsID), "u")

		if ve := asValidationError(t, err); len(ve.Details["examples[1].inputArtifact"]) == 0 {
			t.Errorf("details = %v, want examples[1].inputArtifact", ve.Details)
		}
	})
}

func TestUpdateWorkflowExperimentRechecksWhatTheChecksDependOn(t *testing.T) {
	ctx, svcs, _ := newWorkflowTargetSvcs(t, orderHandler())
	good := mustCreateDataset(ctx, t, svcs.dataset, "orders")
	mustCreateInputExample(ctx, t, svcs, good, map[string]any{"order_id": "O-1"})
	bad := mustCreateDataset(ctx, t, svcs.dataset, "free-text")
	if _, err := svcs.example.Create(ctx, bad, CreateExampleRequest{Prompt: "no structured input"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	req := workflowExperimentRequest(ctx, t, svcs, bad)
	req.TargetWorkflow, req.TargetAgent = "", "OrderDesk"
	exp, err := svcs.experiment.Create(ctx, req, "u")
	if err != nil {
		t.Fatalf("create the agent experiment: %v", err)
	}

	_, err = svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{TargetWorkflow: new("order_handler")}, "u")
	if ve := asValidationError(t, err); len(ve.Details["examples[1].inputData"]) == 0 {
		t.Errorf("retargeting onto a dataset that does not fit: details %v", ve.Details)
	}

	if _, retargetErr := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{TargetWorkflow: new("order_handler"), DatasetID: &good}, "u"); retargetErr != nil {
		t.Fatalf("retarget with a fitting dataset: %v", retargetErr)
	}

	_, err = svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{DatasetID: &bad}, "u")
	if ve := asValidationError(t, err); len(ve.Details["examples[1].inputData"]) == 0 {
		t.Errorf("switching a workflow experiment's dataset: details %v", ve.Details)
	}

	_, err = svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{RunAs: new("orders-bot"), RunAsSet: true}, "u")
	if ve := asValidationError(t, err); ve.Field != "run_as" {
		t.Errorf("switching to a principal that cannot invoke: field %q", ve.Field)
	}

	if _, err := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{Name: new("renamed")}, "u"); err != nil {
		t.Errorf("a rename touches nothing the checks read: %v", err)
	}
}

func TestTargetSnapshotResolvesTheWorkflow(t *testing.T) {
	f := newRunFixture(t, 1)
	workflowType := store.EvalTargetTypeWorkflow
	if _, err := f.expRepo.Update(f.ctx, f.experimentID, store.EvalExperimentUpdate{TargetAgent: new("order_handler"), TargetType: &workflowType, UpdatedBy: "u"}); err != nil {
		t.Fatalf("retarget fixture: %v", err)
	}
	f.svc.featureRegistry = workflowEvalsFlag(true)
	workflows := orderHandler()
	f.svc.workflowTargets = NewWorkflowTargetValidator(workflows, fakeInvoke{rbac.EvalSystemUserSub: true}, f.exampleRepo, nil)
	exp, err := f.expRepo.GetByID(f.ctx, f.experimentID)
	if err != nil {
		t.Fatalf("get experiment: %v", err)
	}
	examples, err := f.exampleRepo.ListByDataset(f.ctx, f.datasetID)
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}

	cfg, err := f.svc.buildTargetConfigSnapshot(f.ctx, exp, "fixture", examples, nil)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	workflow, _ := cfg["workflow"].(map[string]any)
	if workflow["name"] != "order_handler" || workflow["instance_name"] != "workflow_1234" || workflow["input_schema"] == nil {
		t.Errorf("workflow snapshot = %v, want the name, instance and schema", workflow)
	}
	if cfg["agent"] != nil {
		t.Errorf("a workflow snapshot recorded an agent: %v", cfg["agent"])
	}

	delete(workflows, "order_handler")
	_, err = f.svc.buildTargetConfigSnapshot(f.ctx, exp, "fixture", examples, nil)
	if ve := asValidationError(t, err); ve.Field != "target_workflow" {
		t.Errorf("snapshot after the workflow was deleted: field %q", ve.Field)
	}

	_, err = f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if ve := asValidationError(t, err); ve.Field != "target_workflow" {
		t.Errorf("trigger after the workflow was deleted: field %q", ve.Field)
	}

	workflows["order_handler"] = orderHandler()["order_handler"]
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(runs) != 1 || runs[0].ConfigSnapshot == nil || !strings.Contains(*runs[0].ConfigSnapshot, `"instance_name":"workflow_1234"`) {
		t.Errorf("runs = %d, want one whose snapshot names the workflow's instance", len(runs))
	}
}

func TestWorkflowInputOfAnExample(t *testing.T) {
	structuredInput := `{"order_id":"O-1"}`
	bigID := `{"order_id":9007199254740993}`
	schema, err := structured.CompileInputSchema(orderSchema())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		ex     *store.EvalExample
		schema *structured.Schema
		want   string
	}{
		{"structured input", &store.EvalExample{Prompt: "ignored", InputData: &structuredInput}, schema, `{"order_id":"O-1"}`},
		{"prompt only", &store.EvalExample{Prompt: " order O-1 "}, nil, `{"text":"order O-1"}`},
		{"an id past 2^53 kept exact", &store.EvalExample{InputData: &bigID}, nil, bigID},
		{"a JSON-object prompt without a schema stays text", &store.EvalExample{Prompt: structuredInput}, nil, `{"text":"{\"order_id\":\"O-1\"}"}`},
		{"a JSON-object prompt the schema takes is the input", &store.EvalExample{Prompt: structuredInput}, schema, structuredInput},
		{"a prose prompt under a schema is the text wrapper", &store.EvalExample{Prompt: "order O-1"}, schema, `{"text":"order O-1"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := json.Marshal(WorkflowInput(tc.ex, tc.schema))
			if string(got) != tc.want {
				t.Errorf("WorkflowInput = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCreateWorkflowExperiment_PromptsAreReadAsTheWorkflowReadsText(t *testing.T) {
	cases := []struct {
		name     string
		prompt   string
		accepted bool
	}{
		{"a JSON-object prompt the schema takes", `{"order_id":"O-1"}`, true},
		{"a JSON-object prompt that, like its text wrapper, fails the schema", `{"order_id":7}`, false},
		{"a prose prompt, read as a text wrapper without order_id", "order O-1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, svcs, _ := newWorkflowTargetSvcs(t, orderHandler())
			dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")
			mustCreatePromptExample(ctx, t, svcs, dsID, tc.prompt)

			_, err := svcs.experiment.Create(ctx, workflowExperimentRequest(ctx, t, svcs, dsID), "u")

			if tc.accepted {
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				return
			}
			if ve := asValidationError(t, err); len(ve.Details["examples[1].inputData"]) == 0 {
				t.Errorf("details = %v, want examples[1].inputData", ve.Details)
			}
		})
	}
}

func TestCreateWorkflowExperiment_UncompilableSchemaIsNotEnforced(t *testing.T) {
	var logs bytes.Buffer
	workflows := fakeWorkflows{"order_handler": {InstanceName: "workflow_1234", InputSchema: map[string]any{"type": "strng"}}}
	ctx, svcs, _ := newWorkflowTargetSvcsWith(t, workflows, fakeInvoke{rbac.EvalSystemUserSub: true}, slog.New(slog.NewTextHandler(&logs, nil)))
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")
	mustCreatePromptExample(ctx, t, svcs, dsID, "free text")
	mustCreateInputExample(ctx, t, svcs, dsID, map[string]any{"anything": 1})

	if _, err := svcs.experiment.Create(ctx, workflowExperimentRequest(ctx, t, svcs, dsID), "u"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := strings.Count(logs.String(), "level=WARN"); got != 1 {
		t.Errorf("warnings = %d, want one per save:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), "workflowInstance=workflow_1234") {
		t.Errorf("the warning does not name the workflow:\n%s", logs.String())
	}
}

func TestUpdateExperiment_WorkflowChecksFollowTheResultingTarget(t *testing.T) {
	ctx, svcs, _ := newWorkflowTargetSvcs(t, orderHandler())
	good := mustCreateDataset(ctx, t, svcs.dataset, "orders")
	mustCreateInputExample(ctx, t, svcs, good, map[string]any{"order_id": "O-1"})
	bad := mustCreateDataset(ctx, t, svcs.dataset, "free-text")
	mustCreatePromptExample(ctx, t, svcs, bad, "no structured input")
	base := workflowExperimentRequest(ctx, t, svcs, good)

	t.Run("an agent experiment takes a misfitting dataset and any principal", func(t *testing.T) {
		exp, err := svcs.experiment.Create(ctx, asAgentExperiment(base, "agent-unchecked"), "u")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{DatasetID: &bad}, "u"); err != nil {
			t.Errorf("switch dataset: %v", err)
		}
		if _, err := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{RunAs: new("orders-bot"), RunAsSet: true}, "u"); err != nil {
			t.Errorf("switch principal: %v", err)
		}
	})

	t.Run("a workflow experiment retargeted to an agent leaves its dataset unchecked", func(t *testing.T) {
		req := base
		req.Name = "workflow-to-agent"
		exp, err := svcs.experiment.Create(ctx, req, "u")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{TargetAgent: new("OrderDesk"), DatasetID: &bad}, "u"); err != nil {
			t.Errorf("retarget to an agent: %v", err)
		}
	})

	t.Run("an agent experiment retargeted to the workflow is checked with its stored principal", func(t *testing.T) {
		req := asAgentExperiment(base, "agent-to-workflow")
		req.RunAs = new("orders-bot")
		exp, err := svcs.experiment.Create(ctx, req, "u")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		_, err = svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{TargetWorkflow: new("order_handler")}, "u")
		if ve := asValidationError(t, err); ve.Field != "run_as" || !strings.Contains(ve.Message, "system:orders-bot") {
			t.Errorf("error = %s: %q, want run_as naming the stored principal", ve.Field, ve.Message)
		}
	})
}

func TestUpdateWorkflowExperiment_ClearedRunAsIsCheckedAsTheEvalSystemUser(t *testing.T) {
	for name, cleared := range map[string]*string{"null": nil, "empty": new("")} {
		t.Run(name, func(t *testing.T) {
			invoke := &recordingInvoke{allowed: fakeInvoke{rbac.EvalSystemUserSub: true, "system:orders-bot": true}}
			ctx, svcs, _ := newWorkflowTargetSvcsWith(t, orderHandler(), invoke, nil)
			dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")
			mustCreateInputExample(ctx, t, svcs, dsID, map[string]any{"order_id": "O-1"})
			req := workflowExperimentRequest(ctx, t, svcs, dsID)
			req.RunAs = new("orders-bot")
			exp, err := svcs.experiment.Create(ctx, req, "u")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			invoke.asked = nil

			if _, err := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{RunAs: cleared, RunAsSet: true}, "u"); err != nil {
				t.Fatalf("clear run_as: %v", err)
			}
			if !slices.Equal(invoke.asked, []string{rbac.EvalSystemUserSub}) {
				t.Errorf("checked principals = %v, want [%s]", invoke.asked, rbac.EvalSystemUserSub)
			}
		})
	}
}

func TestRunAsPrincipal(t *testing.T) {
	cases := []struct {
		name  string
		runAs *string
		want  string
	}{
		{"unset", nil, rbac.EvalSystemUserSub},
		{"empty", new(""), rbac.EvalSystemUserSub},
		{"named", new("system:orders-bot"), "system:orders-bot"},
	}
	for _, tc := range cases {
		if got := runAsPrincipal(tc.runAs); got != tc.want {
			t.Errorf("%s: runAsPrincipal = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func mustCreateInputExample(ctx context.Context, t *testing.T, svcs *evalSvcSet, datasetID string, input map[string]any) *ExampleResponse {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	ex, err := svcs.example.Create(ctx, datasetID, CreateExampleRequest{InputData: raw})
	if err != nil {
		t.Fatalf("create example: %v", err)
	}
	return ex
}
