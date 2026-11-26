// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func evalRefListServer(t *testing.T, datasets []wire.DatasetResponse, evaluators []wire.EvaluatorResponse, datasetsStatus, evaluatorsStatus int) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+evalDatasetsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		if datasetsStatus != 0 {
			writeV2Error(w, datasetsStatus, "boom", nil)
			return
		}
		writeV2Page(w, datasets)
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+evalEvaluatorsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		if evaluatorsStatus != 0 {
			writeV2Error(w, evaluatorsStatus, "boom", nil)
			return
		}
		writeV2Page(w, evaluators)
	})
	return newV2Platform(t, mux).URL
}

func TestResolvePlaceholderExperimentRefs_UpdatePath(t *testing.T) {
	const dsID = "11111111-1111-1111-1111-111111111111"
	const evID = "22222222-2222-2222-2222-222222222222"
	url := evalRefListServer(t,
		[]wire.DatasetResponse{{ID: dsID, Name: "new-dataset"}},
		[]wire.EvaluatorResponse{{ID: evID, Name: "new-judge", EvaluatorType: "llm_judge"}},
		0, 0)
	c := newClientForTest(t, url)

	plan := &experimentKindPlan{
		Updates: []experimentOp{{
			Name: "nightly",
			ID:   "exp-1",
			Create: wire.CreateExperimentRequest{
				DatasetID:          "new-dataset",
				EvaluatorIDs:       []string{"new-judge"},
				PrimaryEvaluatorID: new("new-judge"),
			},
			Patch: wire.UpdateExperimentRequest{
				DatasetID:          wire.Patch[string]{Set: true, Value: "new-dataset"},
				EvaluatorIDs:       wire.Patch[[]string]{Set: true, Value: []string{"new-judge"}},
				PrimaryEvaluatorID: wire.Patch[string]{Set: true, Value: "new-judge"},
			},
		}},
	}
	if err := resolvePlaceholderExperimentRefs(t.Context(), c, plan); err != nil {
		t.Fatalf("resolvePlaceholderExperimentRefs: %v", err)
	}
	create, patch := plan.Updates[0].Create, plan.Updates[0].Patch
	if create.DatasetID != dsID || patch.DatasetID.Value != dsID {
		t.Errorf("DatasetID = %q / %+v, want %s on both", create.DatasetID, patch.DatasetID, dsID)
	}
	if len(patch.EvaluatorIDs.Value) != 1 || patch.EvaluatorIDs.Value[0] != evID || create.EvaluatorIDs[0] != evID {
		t.Errorf("EvaluatorIDs = %v / %+v, want [%s] on both", create.EvaluatorIDs, patch.EvaluatorIDs, evID)
	}
	if ptrString(create.PrimaryEvaluatorID) != evID || patch.PrimaryEvaluatorID.Value != evID {
		t.Errorf("PrimaryEvaluatorID = %v / %+v, want %s on both", create.PrimaryEvaluatorID, patch.PrimaryEvaluatorID, evID)
	}
}

func TestResolvePlaceholderExperimentRefs_LeavesUnsentPatchFieldsOut(t *testing.T) {
	const dsID = "11111111-1111-1111-1111-111111111111"
	const evID = "22222222-2222-2222-2222-222222222222"
	c := newClientForTest(t, evalRefListServer(t,
		[]wire.DatasetResponse{{ID: dsID, Name: "new-dataset"}},
		[]wire.EvaluatorResponse{{ID: evID, Name: "new-judge", EvaluatorType: "llm_judge"}},
		0, 0))
	plan := &experimentKindPlan{Updates: []experimentOp{{
		Name:   "nightly",
		ID:     "exp-1",
		Create: wire.CreateExperimentRequest{DatasetID: "new-dataset", EvaluatorIDs: []string{"new-judge"}},
		Patch:  wire.UpdateExperimentRequest{Description: wire.Patch[string]{Set: true, Value: "d"}},
	}}}
	if err := resolvePlaceholderExperimentRefs(t.Context(), c, plan); err != nil {
		t.Fatalf("resolvePlaceholderExperimentRefs: %v", err)
	}
	patch := plan.Updates[0].Patch
	if patch.DatasetID.Set || patch.EvaluatorIDs.Set || patch.PrimaryEvaluatorID.Set {
		t.Errorf("Patch = %+v, want only the description it carried", patch)
	}
}

func TestResolvePlaceholderExperimentRefs_NoOpEmptyPlans(t *testing.T) {
	c := newClientForTest(t, "http://invalid.example")
	if err := resolvePlaceholderExperimentRefs(t.Context(), c, nil); err != nil {
		t.Errorf("nil plan: %v", err)
	}
	if err := resolvePlaceholderExperimentRefs(t.Context(), c, &experimentKindPlan{}); err != nil {
		t.Errorf("empty plan: %v", err)
	}
}

func TestResolvePlaceholderExperimentRefs_ListErrors(t *testing.T) {
	plan := func() *experimentKindPlan {
		return &experimentKindPlan{Creates: []experimentOp{{Name: "n", Create: wire.CreateExperimentRequest{DatasetID: "d"}}}}
	}
	t.Run("datasets list fails", func(t *testing.T) {
		c := newClientForTest(t, evalRefListServer(t, nil, nil, http.StatusInternalServerError, 0))
		err := resolvePlaceholderExperimentRefs(t.Context(), c, plan())
		if err == nil || !strings.Contains(err.Error(), "list datasets") {
			t.Errorf("want list-datasets error, got %v", err)
		}
	})
	t.Run("evaluators list fails", func(t *testing.T) {
		c := newClientForTest(t, evalRefListServer(t,
			[]wire.DatasetResponse{{ID: "11111111-1111-1111-1111-111111111111", Name: "d"}},
			nil, 0, http.StatusInternalServerError))
		err := resolvePlaceholderExperimentRefs(t.Context(), c, plan())
		if err == nil || !strings.Contains(err.Error(), "list evaluators") {
			t.Errorf("want list-evaluators error, got %v", err)
		}
	})
}

func TestResolvePlaceholderExperimentRefs_DanglingAfterApply(t *testing.T) {
	const dsID = "11111111-1111-1111-1111-111111111111"
	const evID = "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name    string
		create  wire.CreateExperimentRequest
		missing string
	}{
		{
			name:    "dataset",
			create:  wire.CreateExperimentRequest{DatasetID: "ghost-dataset", EvaluatorIDs: []string{"new-judge"}},
			missing: "dataset",
		},
		{
			name:    "evaluatorIds",
			create:  wire.CreateExperimentRequest{DatasetID: "new-dataset", EvaluatorIDs: []string{"ghost-judge"}},
			missing: "evaluator",
		},
		{
			name:    "primaryEvaluatorId",
			create:  wire.CreateExperimentRequest{DatasetID: "new-dataset", EvaluatorIDs: []string{"new-judge"}, PrimaryEvaluatorID: new("ghost-judge")},
			missing: "evaluator",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClientForTest(t, evalRefListServer(t,
				[]wire.DatasetResponse{{ID: dsID, Name: "new-dataset"}},
				[]wire.EvaluatorResponse{{ID: evID, Name: "new-judge", EvaluatorType: "llm_judge"}},
				0, 0))
			plan := &experimentKindPlan{Creates: []experimentOp{{Name: "nightly", Create: tc.create}}}
			err := resolvePlaceholderExperimentRefs(t.Context(), c, plan)
			if err == nil {
				t.Fatalf("expected dangling-%s error", tc.name)
			}
			if !strings.Contains(err.Error(), `experiment "nightly"`) || !strings.Contains(err.Error(), tc.missing) {
				t.Errorf("error should name the experiment and the missing %s kind; got %v", tc.missing, err)
			}
		})
	}
}

func TestRewritePlaceholderID_Scalar(t *testing.T) {
	nameToID := map[string]string{"new-judge": "22222222-2222-2222-2222-222222222222"}
	got, err := rewritePlaceholderID("", nameToID, "experiment", "x", "evaluator")
	if err != nil || got != "" {
		t.Errorf("empty id should pass through: (%q, %v)", got, err)
	}
	got, err = rewritePlaceholderID("new-judge", nameToID, "experiment", "x", "evaluator")
	if err != nil || got != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("sentinel should resolve: (%q, %v)", got, err)
	}
	if _, err := rewritePlaceholderID("missing", nameToID, "experiment", "x", "evaluator"); err == nil {
		t.Error("dangling scalar ref should error")
	}
}

func TestExperimentCreateNameSets(t *testing.T) {
	if datasetCreateNameSet(nil) != nil {
		t.Error("datasetCreateNameSet(nil) should be nil")
	}
	if evaluatorCreateNameSet(nil) != nil {
		t.Error("evaluatorCreateNameSet(nil) should be nil")
	}
	ds := datasetCreateNameSet(&datasetKindPlan{Creates: []datasetOp{{Name: "a"}, {Name: "b"}}})
	if _, ok := ds["a"]; !ok {
		t.Errorf("datasetCreateNameSet missing 'a': %v", ds)
	}
	ev := evaluatorCreateNameSet(&evaluatorKindPlan{Creates: []evaluatorOp{{Name: "j"}}})
	if _, ok := ev["j"]; !ok {
		t.Errorf("evaluatorCreateNameSet missing 'j': %v", ev)
	}
}
