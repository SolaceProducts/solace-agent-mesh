// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func experimentFixture(f func(map[string]any)) map[string]any {
	m := map[string]any{
		"datasetId":          "acme-eval",
		"targetAgent":        "Orchestrator",
		"evaluatorIds":       []any{"strict-judge"},
		"primaryEvaluatorId": "strict-judge",
	}
	if f != nil {
		f(m)
	}
	return m
}

func experimentResponseFixture(f func(*wire.ExperimentResponse)) wire.ExperimentResponse {
	r := wire.ExperimentResponse{
		ID:                 "id-1",
		Name:               "nightly",
		Description:        new("fixture"),
		DatasetID:          new("ds-1"),
		TargetAgent:        "Orchestrator",
		Models:             []apiv2.ExperimentModel{},
		RunsPerExample:     1,
		MaxWorkers:         4,
		EvaluatorIDs:       []string{"ev-1"},
		PrimaryEvaluatorID: new("ev-1"),
	}
	if f != nil {
		f(&r)
	}
	return r
}

func marshalExperimentPatch(t *testing.T, patch any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return got
}

func resolvedExperiment(name string, spec map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:        "experiment",
		Name:        name,
		SourceFile:  "experiments/" + name + ".yaml",
		Spec:        spec,
		Description: "fixture",
	}
}

func experimentTestHooks() translate.ExperimentHooks {
	datasets := map[string]string{"acme-eval": "ds-1"}
	evaluators := map[string]string{"strict-judge": "ev-1"}
	return translate.ExperimentHooks{
		LookupDatasetID: func(name string) (string, error) {
			id, ok := datasets[name]
			if !ok {
				return "", &lookupErr{kind: "dataset", name: name}
			}
			return id, nil
		},
		LookupEvaluatorID: func(name string) (string, error) {
			id, ok := evaluators[name]
			if !ok {
				return "", &lookupErr{kind: "evaluator", name: name}
			}
			return id, nil
		},
	}
}

type lookupErr struct{ kind, name string }

func (e *lookupErr) Error() string { return e.kind + " " + e.name + " not found" }

func TestPlanExperiments_UnchangedWhenIdenticalDefaults(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(nil))}
	actual := []wire.ExperimentResponse{{
		ID:                 "id-1",
		Name:               "nightly",
		Description:        new("fixture"),
		DatasetID:          new("ds-1"),
		TargetAgent:        "Orchestrator",
		Models:             []apiv2.ExperimentModel{},
		RunsPerExample:     1,
		MaxWorkers:         4,
		EvaluatorIDs:       []string{"ev-1"},
		PrimaryEvaluatorID: new("ev-1"),
	}}

	plan, err := planExperiments(desired, actual, experimentTestHooks())
	if err != nil {
		t.Fatalf("planExperiments: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v (len=%d); want 1", plan.Unchanged, len(plan.Unchanged))
	}
	if len(plan.Creates)+len(plan.Updates)+len(plan.Deletes) != 0 {
		t.Errorf("expected no diff, got creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanExperiments_CreateResolvesNamesToIDs(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(nil))}

	plan, err := planExperiments(desired, nil, experimentTestHooks())
	if err != nil {
		t.Fatalf("planExperiments: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("Creates = %+v", plan.Creates)
	}
	op := plan.Creates[0]
	if op.Create.DatasetID != "ds-1" {
		t.Errorf("DatasetID = %q, want %q", op.Create.DatasetID, "ds-1")
	}
	if len(op.Create.EvaluatorIDs) != 1 || op.Create.EvaluatorIDs[0] != "ev-1" {
		t.Errorf("EvaluatorIDs = %v, want [ev-1]", op.Create.EvaluatorIDs)
	}
	if op.Create.PrimaryEvaluatorID == nil || *op.Create.PrimaryEvaluatorID != "ev-1" {
		t.Errorf("PrimaryEvaluatorID = %v, want ev-1", op.Create.PrimaryEvaluatorID)
	}
}

func TestPlanExperiments_DanglingDatasetRefHardErrors(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
		m["datasetId"] = "does-not-exist"
	}))}

	_, err := planExperiments(desired, nil, experimentTestHooks())
	if err == nil {
		t.Fatalf("expected error for dangling dataset reference, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error must name the missing dataset; got %q", err.Error())
	}
}

func TestPlanExperiments_DanglingEvaluatorRefHardErrors(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
		m["evaluatorIds"] = []any{"missing-judge"}
		m["primaryEvaluatorId"] = "missing-judge"
	}))}

	_, err := planExperiments(desired, nil, experimentTestHooks())
	if err == nil {
		t.Fatalf("expected error for dangling evaluator reference, got nil")
	}
	if !strings.Contains(err.Error(), "missing-judge") {
		t.Errorf("error must name the missing evaluator; got %q", err.Error())
	}
}

func TestPlanExperiments_UnchangedWhenEvaluatorIDsReordered(t *testing.T) {
	hooks := translate.ExperimentHooks{
		LookupDatasetID: func(name string) (string, error) {
			if name == "acme-eval" {
				return "ds-1", nil
			}
			return "", &lookupErr{kind: "dataset", name: name}
		},
		LookupEvaluatorID: func(name string) (string, error) {
			switch name {
			case "judge-a":
				return "ev-a", nil
			case "judge-b":
				return "ev-b", nil
			}
			return "", &lookupErr{kind: "evaluator", name: name}
		},
	}
	desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
		m["evaluatorIds"] = []any{"judge-b", "judge-a"}
		m["primaryEvaluatorId"] = "judge-a"
	}))}
	actual := []wire.ExperimentResponse{{
		ID:                 "id-1",
		Name:               "nightly",
		Description:        new("fixture"),
		DatasetID:          new("ds-1"),
		TargetAgent:        "Orchestrator",
		Models:             []apiv2.ExperimentModel{},
		RunsPerExample:     1,
		MaxWorkers:         4,
		EvaluatorIDs:       []string{"ev-a", "ev-b"},
		PrimaryEvaluatorID: new("ev-a"),
	}}

	plan, err := planExperiments(desired, actual, hooks)
	if err != nil {
		t.Fatalf("planExperiments: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("expected single unchanged on reordered evaluatorIds, got %+v (creates=%d updates=%d)", plan, len(plan.Creates), len(plan.Updates))
	}
}

func TestPlanExperiments_UpdatePatchesWhatChanged(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
		m["targetAgent"] = "OtherAgent"
	}))}
	actual := []wire.ExperimentResponse{{
		ID:                 "id-1",
		Name:               "nightly",
		Description:        new("fixture"),
		DatasetID:          new("ds-1"),
		TargetAgent:        "Orchestrator",
		Models:             []apiv2.ExperimentModel{},
		RunsPerExample:     1,
		MaxWorkers:         4,
		EvaluatorIDs:       []string{"ev-1"},
		PrimaryEvaluatorID: new("ev-1"),
	}}

	plan, err := planExperiments(desired, actual, experimentTestHooks())
	if err != nil {
		t.Fatalf("planExperiments: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("expected single update, got %+v", plan)
	}
	body, err := json.Marshal(plan.Updates[0].Patch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"targetAgent":"OtherAgent"}`; string(body) != want {
		t.Errorf("PATCH body = %s, want %s", body, want)
	}
	if plan.Updates[0].Create.TargetAgent != "OtherAgent" {
		t.Errorf("Create = %+v, want the desired state the v1 PUT is built from", plan.Updates[0].Create)
	}
}

func TestPlanExperiments_PatchResetsDefaultsTheManifestLeavesOut(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(nil))}
	actual := []wire.ExperimentResponse{experimentResponseFixture(func(r *wire.ExperimentResponse) {
		r.RunsPerExample, r.MaxWorkers = 3, 8
	})}
	plan, err := planExperiments(desired, actual, experimentTestHooks())
	if err != nil {
		t.Fatalf("planExperiments: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("expected single update, got %+v", plan)
	}
	body, _ := json.Marshal(plan.Updates[0].Patch)
	if want := `{"runsPerExample":1,"maxWorkers":4}`; string(body) != want {
		t.Errorf("PATCH body = %s, want %s", body, want)
	}
}

func TestPlanExperiments_PatchClearsRemovedFields(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
		delete(m, "primaryEvaluatorId")
	}))}
	desired[0].Description = ""
	actual := []wire.ExperimentResponse{experimentResponseFixture(func(r *wire.ExperimentResponse) {
		r.Models = []apiv2.ExperimentModel{{ModelConfigID: new("mc-1")}}
	})}
	plan, err := planExperiments(desired, actual, experimentTestHooks())
	if err != nil {
		t.Fatalf("planExperiments: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("expected single update, got %+v", plan)
	}
	body, _ := json.Marshal(plan.Updates[0].Patch)
	if want := `{"description":null,"models":null,"primaryEvaluatorId":null}`; string(body) != want {
		t.Errorf("PATCH body = %s, want %s", body, want)
	}
}

func TestPlanExperiments_ModelConfigIDAloneMatchesTheStoredModel(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
		m["models"] = []any{map[string]any{"modelConfigId": "mc-1"}}
	}))}
	actual := []wire.ExperimentResponse{experimentResponseFixture(func(r *wire.ExperimentResponse) {
		r.Models = []apiv2.ExperimentModel{{ModelConfigID: new("mc-1")}}
	})}
	plan, err := planExperiments(desired, actual, experimentTestHooks())
	if err != nil {
		t.Fatalf("planExperiments: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("plan = %+v, want unchanged", plan)
	}
}

func TestPlanExperiments_RunAsPatch(t *testing.T) {
	tests := []struct {
		name     string
		authored any
		want     any
	}{
		{name: "unauthored clears to the default", authored: nil, want: nil},
		{name: "authored bare name", authored: "eval", want: "eval"},
		{name: "authored prefixed subject", authored: "system:eval", want: "system:eval"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
				if tc.authored != nil {
					m["runAs"] = tc.authored
				}
			}))}
			stored := experimentResponseFixture(func(r *wire.ExperimentResponse) { r.RunAs = new("system:other") })
			plan, err := planExperiments(desired, []wire.ExperimentResponse{stored}, experimentTestHooks())
			if err != nil {
				t.Fatalf("planExperiments: %v", err)
			}
			if len(plan.Updates) != 1 {
				t.Fatalf("plan = %+v, want one update", plan)
			}
			got := marshalExperimentPatch(t, plan.Updates[0].Patch)
			v, ok := got["runAs"]
			if !ok {
				t.Fatalf("runAs must be in the PATCH, got %v", got)
			}
			if v != tc.want {
				t.Errorf("runAs = %v, want %v", v, tc.want)
			}
		})
	}
}

func TestPlanExperiments_UnchangedWhenRunAsSpellingDiffers(t *testing.T) {
	for _, authored := range []string{" eval ", "eval", "system:eval"} {
		t.Run(authored, func(t *testing.T) {
			desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
				m["runAs"] = authored
			}))}
			actual := []wire.ExperimentResponse{experimentResponseFixture(func(r *wire.ExperimentResponse) {
				r.RunAs = new("system:eval")
			})}

			plan, err := planExperiments(desired, actual, experimentTestHooks())
			if err != nil {
				t.Fatalf("planExperiments: %v", err)
			}
			if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
				t.Errorf("expected unchanged, got unchanged=%d updates=%d", len(plan.Unchanged), len(plan.Updates))
			}
		})
	}
}

func TestPlanExperiments_RejectsUnresolvableRunAs(t *testing.T) {
	for _, authored := range []string{"system:", "has space", "has\rcarriage", "has\u00a0nbsp"} {
		t.Run(authored, func(t *testing.T) {
			desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
				m["runAs"] = authored
			}))}

			_, err := planExperiments(desired, nil, experimentTestHooks())
			if err == nil {
				t.Fatalf("planExperiments accepted runAs %q; the platform would 400 mid-apply", authored)
			}
			if !strings.Contains(err.Error(), "runAs") {
				t.Errorf("error %q does not name the offending field", err)
			}
		})
	}
}

func TestPlanExperiments_RunAsIsManifestOwned(t *testing.T) {
	tests := []struct {
		name       string
		authored   any
		live       *string
		wantUpdate bool
		wantWire   any
	}{
		{name: "unauthored reverts to the default", live: new("system:eval"), wantUpdate: true, wantWire: nil},
		{name: "unauthored and already default", live: nil},
		{name: "authored value equal to live", authored: "eval", live: new("system:eval")},
		{name: "authored value differs", authored: "eval", live: new("system:other"), wantUpdate: true, wantWire: "eval"},
		{name: "authored empty clears", authored: "", live: new("system:eval"), wantUpdate: true, wantWire: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			desired := []resolve.ResolvedResource{resolvedExperiment("nightly", experimentFixture(func(m map[string]any) {
				if tc.authored != nil {
					m["runAs"] = tc.authored
				}
			}))}
			actual := []wire.ExperimentResponse{experimentResponseFixture(func(r *wire.ExperimentResponse) {
				r.RunAs = tc.live
			})}

			plan, err := planExperiments(desired, actual, experimentTestHooks())
			if err != nil {
				t.Fatalf("planExperiments: %v", err)
			}
			if !tc.wantUpdate {
				if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
					t.Fatalf("expected unchanged, got unchanged=%d updates=%d", len(plan.Unchanged), len(plan.Updates))
				}
				return
			}
			if len(plan.Updates) != 1 {
				t.Fatalf("expected single update, got updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
			}
			got := marshalExperimentPatch(t, plan.Updates[0].Patch)
			if v, ok := got["runAs"]; !ok || v != tc.wantWire {
				t.Errorf("runAs on wire = %v (present=%v), want %v", v, ok, tc.wantWire)
			}
		})
	}
}

func TestEvalExperimentHooks_ResolvesAndErrorsOnMissing(t *testing.T) {
	datasets := []wire.DatasetResponse{{ID: "ds-1", Name: "acme-eval"}}
	evaluators := []wire.EvaluatorResponse{{ID: "ev-1", Name: "strict-judge"}}
	hooks := evalExperimentHooks(datasets, evaluators, nil, nil)

	id, err := hooks.LookupDatasetID("acme-eval")
	if err != nil || id != "ds-1" {
		t.Errorf("LookupDatasetID = (%q, %v), want (ds-1, nil)", id, err)
	}
	if _, lookupErr := hooks.LookupDatasetID("missing"); lookupErr == nil {
		t.Errorf("expected error for missing dataset")
	}

	id, err = hooks.LookupEvaluatorID("strict-judge")
	if err != nil || id != "ev-1" {
		t.Errorf("LookupEvaluatorID = (%q, %v), want (ev-1, nil)", id, err)
	}
	if _, lookupErr := hooks.LookupEvaluatorID("missing"); lookupErr == nil {
		t.Errorf("expected error for missing evaluator")
	}
}

func TestEvalExperimentHooks_ResolvesDesiredCreatesToSentinel(t *testing.T) {
	desiredDatasets := map[string]struct{}{"new-dataset": {}}
	desiredEvaluators := map[string]struct{}{"new-judge": {}}
	hooks := evalExperimentHooks(nil, nil, desiredDatasets, desiredEvaluators)

	id, err := hooks.LookupDatasetID("new-dataset")
	if err != nil || id != "new-dataset" {
		t.Errorf("LookupDatasetID = (%q, %v), want (new-dataset, nil) sentinel", id, err)
	}
	id, err = hooks.LookupEvaluatorID("new-judge")
	if err != nil || id != "new-judge" {
		t.Errorf("LookupEvaluatorID = (%q, %v), want (new-judge, nil) sentinel", id, err)
	}
	if _, err := hooks.LookupDatasetID("neither-live-nor-desired"); err == nil {
		t.Errorf("expected hard error for a ref that is neither live nor desired")
	}
}
