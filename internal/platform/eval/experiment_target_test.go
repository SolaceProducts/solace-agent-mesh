// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func workflowEvalsFlag(on bool) *features.Registry {
	r := features.NewRegistry()
	r.Register(features.Definition{Key: features.KeyWorkflowEvals, Default: on})
	return r
}

func newTargetExperimentRequest(ctx context.Context, t *testing.T, svcs *evalSvcSet) CreateExperimentRequest {
	t.Helper()
	return CreateExperimentRequest{
		Name:         "order-handler-regression",
		DatasetID:    mustCreateDataset(ctx, t, svcs.dataset, "orders"),
		EvaluatorIDs: []string{mustCreateLLMJudge(ctx, t, svcs.evaluator, "judge")},
	}
}

func TestCreateExperimentTarget(t *testing.T) {
	cases := []struct {
		name      string
		flagOn    bool
		agent     string
		workflow  string
		wantField string
		wantMsg   string
		wantType  string
	}{
		{name: "agent target", agent: "OrderDesk", wantType: store.EvalTargetTypeAgent},
		{name: "workflow target", flagOn: true, workflow: "order_exception_handler", wantType: store.EvalTargetTypeWorkflow},
		{name: "workflow target with the flag off", workflow: "order_exception_handler", wantField: "target_workflow", wantMsg: "SAM_FEATURE_WORKFLOW_EVALS"},
		{name: "both targets", flagOn: true, agent: "OrderDesk", workflow: "order_exception_handler", wantField: "target_agent", wantMsg: "not both"},
		{name: "neither target", flagOn: true, agent: "  ", wantField: "target_agent", wantMsg: "required"},
		{name: "overlong workflow name", flagOn: true, workflow: strings.Repeat("w", maxExperimentNameLen+1), wantField: "target_workflow", wantMsg: "targetWorkflow must be at most"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, svcs := newEvalSvcs(t)
			svcs.experiment.SetFeatureRegistry(workflowEvalsFlag(tc.flagOn))
			req := newTargetExperimentRequest(ctx, t, svcs)
			req.TargetAgent, req.TargetWorkflow = tc.agent, tc.workflow

			resp, err := svcs.experiment.Create(ctx, req, "u")

			if tc.wantField != "" {
				ve := asValidationError(t, err)
				if ve.Field != tc.wantField || !strings.Contains(ve.Message, tc.wantMsg) {
					t.Fatalf("error = %s: %q, want %s mentioning %q", ve.Field, ve.Message, tc.wantField, tc.wantMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if resp.TargetType != tc.wantType {
				t.Errorf("targetType = %q, want %q", resp.TargetType, tc.wantType)
			}
			if tc.wantType == store.EvalTargetTypeWorkflow {
				if resp.TargetWorkflow == nil || *resp.TargetWorkflow != tc.workflow || resp.TargetAgent != "" {
					t.Errorf("targetWorkflow = %v targetAgent = %q, want only the workflow named", resp.TargetWorkflow, resp.TargetAgent)
				}
				return
			}
			if resp.TargetAgent != tc.agent || resp.TargetWorkflow != nil {
				t.Errorf("targetAgent = %q targetWorkflow = %v, want only the agent named", resp.TargetAgent, resp.TargetWorkflow)
			}
		})
	}
}

func TestUpdateExperimentRetargets(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	svcs.experiment.SetFeatureRegistry(workflowEvalsFlag(true))
	req := newTargetExperimentRequest(ctx, t, svcs)
	req.TargetAgent = "OrderDesk"
	created, err := svcs.experiment.Create(ctx, req, "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	toWorkflow, err := svcs.experiment.Update(ctx, created.ID, UpdateExperimentRequest{TargetWorkflow: new(" order_exception_handler ")}, "u")
	if err != nil {
		t.Fatalf("retarget to workflow: %v", err)
	}
	if toWorkflow.TargetType != store.EvalTargetTypeWorkflow || toWorkflow.TargetWorkflow == nil || *toWorkflow.TargetWorkflow != "order_exception_handler" {
		t.Fatalf("after retarget: type %q workflow %v, want the trimmed workflow", toWorkflow.TargetType, toWorkflow.TargetWorkflow)
	}

	renamed, err := svcs.experiment.Update(ctx, created.ID, UpdateExperimentRequest{Name: new("renamed")}, "u")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.TargetType != store.EvalTargetTypeWorkflow {
		t.Errorf("an update naming no target changed the target type to %q", renamed.TargetType)
	}

	back, err := svcs.experiment.Update(ctx, created.ID, UpdateExperimentRequest{TargetAgent: new("OrderDesk")}, "u")
	if err != nil {
		t.Fatalf("retarget to agent: %v", err)
	}
	if back.TargetType != store.EvalTargetTypeAgent || back.TargetAgent != "OrderDesk" || back.TargetWorkflow != nil {
		t.Errorf("after retarget back: type %q agent %q workflow %v", back.TargetType, back.TargetAgent, back.TargetWorkflow)
	}
}

func TestUpdateExperimentTargetValidation(t *testing.T) {
	cases := []struct {
		name      string
		flagOn    bool
		req       UpdateExperimentRequest
		wantField string
	}{
		{"both targets", true, UpdateExperimentRequest{TargetAgent: new("a"), TargetWorkflow: new("w")}, "target_agent"},
		{"blank workflow", true, UpdateExperimentRequest{TargetWorkflow: new(" ")}, "target_workflow"},
		{"blank agent", true, UpdateExperimentRequest{TargetAgent: new("")}, "target_agent"},
		{"workflow with the flag off", false, UpdateExperimentRequest{TargetWorkflow: new("w")}, "target_workflow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, svcs := newEvalSvcs(t)
			svcs.experiment.SetFeatureRegistry(workflowEvalsFlag(tc.flagOn))
			req := newTargetExperimentRequest(ctx, t, svcs)
			req.TargetAgent = "OrderDesk"
			created, err := svcs.experiment.Create(ctx, req, "u")
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			_, err = svcs.experiment.Update(ctx, created.ID, tc.req, "u")

			if ve := asValidationError(t, err); ve.Field != tc.wantField {
				t.Errorf("field = %q (%s), want %q", ve.Field, ve.Message, tc.wantField)
			}
		})
	}
}

func retargetFixtureAtWorkflow(t *testing.T, f *runServiceFixture) {
	t.Helper()
	workflowType := store.EvalTargetTypeWorkflow
	if _, err := f.expRepo.Update(f.ctx, f.experimentID, store.EvalExperimentUpdate{TargetType: &workflowType, UpdatedBy: "alice"}); err != nil {
		t.Fatalf("retarget fixture: %v", err)
	}
}

func TestTriggerRunsOfAWorkflowExperimentFollowsTheFlag(t *testing.T) {
	triggers := map[string]func(f *runServiceFixture) error{
		"TriggerRuns": func(f *runServiceFixture) error {
			_, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
			return err
		},
		"TriggerRunsIfIdle": func(f *runServiceFixture) error {
			_, err := f.svc.TriggerRunsIfIdle(f.ctx, f.experimentID, "alice", "", "scheduled")
			return err
		},
	}
	for triggerName, trigger := range triggers {
		t.Run(triggerName+"/flag off", func(t *testing.T) {
			f := newRunFixture(t, 1)
			retargetFixtureAtWorkflow(t, f)
			f.svc.featureRegistry = workflowEvalsFlag(false)

			err := trigger(f)

			ve := asValidationError(t, err)
			if ve.Field != "target_workflow" || !strings.Contains(ve.Message, "SAM_FEATURE_WORKFLOW_EVALS") {
				t.Errorf("error = %s: %q, want target_workflow naming the flag", ve.Field, ve.Message)
			}
			runs, listErr := f.runRepo.ListByExperiment(f.ctx, f.experimentID)
			if listErr != nil {
				t.Fatalf("list runs: %v", listErr)
			}
			if len(runs) != 0 {
				t.Errorf("a refused trigger created %d runs", len(runs))
			}
		})
		t.Run(triggerName+"/flag on", func(t *testing.T) {
			f := newRunFixture(t, 1)
			retargetFixtureAtWorkflow(t, f)
			f.svc.featureRegistry = workflowEvalsFlag(true)
			f.withBroker(&publishLimitBroker{accept: 1})

			if err := trigger(f); err != nil {
				t.Fatalf("trigger: %v", err)
			}
			runs, listErr := f.runRepo.ListByExperiment(f.ctx, f.experimentID)
			if listErr != nil {
				t.Fatalf("list runs: %v", listErr)
			}
			if len(runs) != 1 || runs[0].ConfigSnapshot == nil || !strings.Contains(*runs[0].ConfigSnapshot, `"workflow"`) {
				t.Errorf("runs = %d, want one whose snapshot records the workflow", len(runs))
			}
		})
	}
}

func TestWorkflowEvalsEnabledTreatsANilRegistryAsOff(t *testing.T) {
	if workflowEvalsEnabled(nil) {
		t.Error("a nil feature registry turned workflow evaluations on")
	}
	if !workflowEvalsEnabled(workflowEvalsFlag(true)) {
		t.Error("a registry with workflow_evals on reads as off")
	}
}

func TestBuildTargetConfigSnapshot(t *testing.T) {
	f := newRunFixture(t, 1)
	f.svc.agentRegistry = &fakeRegistry{cards: map[string]*a2a.AgentCard{"Orchestrator": {Name: "Orchestrator"}}}
	snapshot := func(t *testing.T) (cfg, experiment map[string]any) {
		t.Helper()
		exp, err := f.expRepo.GetByID(f.ctx, f.experimentID)
		if err != nil {
			t.Fatalf("get experiment: %v", err)
		}
		examples, err := f.exampleRepo.ListByDataset(f.ctx, f.datasetID)
		if err != nil {
			t.Fatalf("list examples: %v", err)
		}
		cfg, err = f.svc.buildTargetConfigSnapshot(f.ctx, exp, "fixture", examples, nil)
		if err != nil {
			t.Fatalf("build snapshot: %v", err)
		}
		experiment, _ = cfg["experiment"].(map[string]any)
		return cfg, experiment
	}

	t.Run("agent target", func(t *testing.T) {
		cfg, experiment := snapshot(t)
		if experiment["target_type"] != store.EvalTargetTypeAgent {
			t.Errorf("experiment.target_type = %v, want agent", experiment["target_type"])
		}
		if agent, _ := cfg["agent"].(map[string]any); agent == nil || agent["name"] != "Orchestrator" {
			t.Errorf("agent = %v, want the hydrated Orchestrator", cfg["agent"])
		}
		if _, ok := cfg["workflow"]; ok {
			t.Errorf("an agent snapshot carries workflow %v", cfg["workflow"])
		}
	})

	t.Run("workflow target", func(t *testing.T) {
		retargetFixtureAtWorkflow(t, f)
		cfg, experiment := snapshot(t)
		if experiment["target_type"] != store.EvalTargetTypeWorkflow {
			t.Errorf("experiment.target_type = %v, want workflow", experiment["target_type"])
		}
		if workflow, _ := cfg["workflow"].(map[string]any); workflow == nil || workflow["name"] != "Orchestrator" {
			t.Errorf("workflow = %v, want the experiment's target name", cfg["workflow"])
		}
		if cfg["agent"] != nil {
			t.Errorf("a workflow snapshot hydrated agent %v", cfg["agent"])
		}
	})
}
