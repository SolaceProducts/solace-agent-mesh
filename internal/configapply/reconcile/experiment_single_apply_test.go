// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestApplyEval_SingleApplyCreatesExperimentWithDatasetAndEvaluator(t *testing.T) {
	const dsID = "11111111-1111-1111-1111-111111111111"
	const evID = "22222222-2222-2222-2222-222222222222"

	datasets := map[string]string{}
	evaluators := map[string]string{}
	var createdExperiments []wire.CreateExperimentRequest

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+platform.APIV2Prefix+evalDatasetsResource.path, func(w http.ResponseWriter, r *http.Request) {
		var req wire.CreateDatasetRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		datasets[req.Name] = dsID
		writeV2Single(w, http.StatusCreated, wire.DatasetResponse{ID: dsID, Name: req.Name})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+evalDatasetsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		data := make([]wire.DatasetResponse, 0, len(datasets))
		for name, id := range datasets {
			data = append(data, wire.DatasetResponse{ID: id, Name: name})
		}
		writeV2Page(w, data)
	})
	mux.HandleFunc("POST "+platform.APIV2Prefix+evalEvaluatorsResource.path, func(w http.ResponseWriter, r *http.Request) {
		var req wire.CreateEvaluatorRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		evaluators[req.Name] = evID
		writeV2Single(w, http.StatusCreated, wire.EvaluatorResponse{ID: evID, Name: req.Name, EvaluatorType: req.EvaluatorType})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+evalEvaluatorsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		data := make([]wire.EvaluatorResponse, 0, len(evaluators))
		for name, id := range evaluators {
			data = append(data, wire.EvaluatorResponse{ID: id, Name: name, EvaluatorType: "llm_judge"})
		}
		writeV2Page(w, data)
	})
	mux.HandleFunc("POST "+platform.APIV2Prefix+evalExperimentsResource.path, func(w http.ResponseWriter, r *http.Request) {
		var req wire.CreateExperimentRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		createdExperiments = append(createdExperiments, req)
		writeV2Single(w, http.StatusCreated, wire.ExperimentResponse{ID: "exp-1", Name: req.Name})
	})
	c := newV2PlatformClient(t, mux)

	dsPlan, err := planDatasets([]resolve.ResolvedResource{resolvedDataset("new-dataset", "corpus")}, nil)
	if err != nil {
		t.Fatalf("planDatasets: %v", err)
	}
	evPlan, err := planEvaluators([]resolve.ResolvedResource{resolvedEvaluator("new-judge", evaluatorFixture(nil))}, nil)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	expSpec := experimentFixture(func(m map[string]any) {
		m["datasetId"] = "new-dataset"
		m["evaluatorIds"] = []any{"new-judge"}
		m["primaryEvaluatorId"] = "new-judge"
	})
	hooks := evalExperimentHooks(nil, nil, datasetCreateNameSet(dsPlan), evaluatorCreateNameSet(evPlan))
	expPlan, err := planExperiments([]resolve.ResolvedResource{resolvedExperiment("nightly", expSpec)}, nil, hooks)
	if err != nil {
		t.Fatalf("planExperiments (single-pass) should not error on in-flight refs: %v", err)
	}
	if len(expPlan.Creates) != 1 {
		t.Fatalf("expected one experiment create, got %+v", expPlan.Creates)
	}
	if got := expPlan.Creates[0].Create.DatasetID; got != "new-dataset" {
		t.Fatalf("plan-time DatasetID = %q, want name sentinel %q", got, "new-dataset")
	}

	opts := ApplyOptions{}
	dsOut := applyDatasets(t.Context(), c, dsPlan, opts)
	if len(dsOut.Failures) != 0 {
		t.Fatalf("dataset apply failures: %+v", dsOut.Failures)
	}
	evOut := applyEvaluators(t.Context(), c, evPlan, opts)
	if len(evOut.Failures) != 0 {
		t.Fatalf("evaluator apply failures: %+v", evOut.Failures)
	}
	if err := resolvePlaceholderExperimentRefs(t.Context(), c, expPlan); err != nil {
		t.Fatalf("resolvePlaceholderExperimentRefs: %v", err)
	}
	expOut := applyExperiments(t.Context(), c, expPlan, opts)
	if len(expOut.Failures) != 0 {
		t.Fatalf("experiment apply failures: %+v", expOut.Failures)
	}

	if len(createdExperiments) != 1 {
		t.Fatalf("expected one CreateExperiment call, got %d", len(createdExperiments))
	}
	got := createdExperiments[0]
	if got.DatasetID != dsID {
		t.Errorf("CreateExperiment.DatasetID = %q, want resolved %q", got.DatasetID, dsID)
	}
	if len(got.EvaluatorIDs) != 1 || got.EvaluatorIDs[0] != evID {
		t.Errorf("CreateExperiment.EvaluatorIDs = %v, want [%s]", got.EvaluatorIDs, evID)
	}
	if got.PrimaryEvaluatorID == nil || *got.PrimaryEvaluatorID != evID {
		t.Errorf("CreateExperiment.PrimaryEvaluatorID = %v, want %s", got.PrimaryEvaluatorID, evID)
	}
}
