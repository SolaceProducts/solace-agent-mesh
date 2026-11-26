// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type applyMockServer struct {
	creates  []wire.CreateExampleRequest
	updates  []wire.UpdateExampleRequest
	deletes  []string
	listHits atomic.Int32
}

func datasetExamplesV2Path(datasetID string) string {
	return platform.APIV2Prefix + evalDatasetsResource.path + "/" + datasetID + "/examples"
}

func newApplyMockClient(t *testing.T, datasetID string, initial []wire.ExampleResponse, hooks *applyMockServer) *Client {
	t.Helper()
	if hooks == nil {
		hooks = &applyMockServer{}
	}
	mux := http.NewServeMux()
	state := append([]wire.ExampleResponse(nil), initial...)
	mux.HandleFunc("GET "+datasetExamplesV2Path(datasetID), func(w http.ResponseWriter, _ *http.Request) {
		hooks.listHits.Add(1)
		writeV2Page(w, state)
	})
	mux.HandleFunc("POST "+datasetExamplesV2Path(datasetID), func(w http.ResponseWriter, r *http.Request) {
		var req wire.CreateExampleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeV2Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		hooks.creates = append(hooks.creates, req)
		seq := 0
		if req.SequenceNumber != nil {
			seq = *req.SequenceNumber
		}
		writeV2Single(w, http.StatusCreated, wire.ExampleResponse{
			ID: "new-id", DatasetID: datasetID, SequenceNumber: seq, Prompt: req.Prompt, ExpectedResponse: req.ExpectedResponse,
		})
	})
	mux.HandleFunc("PATCH "+platform.APIV2Prefix+evalExamplesResource.path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req wire.UpdateExampleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeV2Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		hooks.updates = append(hooks.updates, req)
		writeV2Single(w, http.StatusOK, wire.ExampleResponse{ID: r.PathValue("id")})
	})
	mux.HandleFunc("DELETE "+platform.APIV2Prefix+evalExamplesResource.path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		hooks.deletes = append(hooks.deletes, r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
	return newV2PlatformClient(t, mux)
}

func TestApplyExampleDiff_DriveCRUD(t *testing.T) {
	hooks := &applyMockServer{}
	initial := []wire.ExampleResponse{
		{ID: "p1", DatasetID: "ds-1", SequenceNumber: 1, Prompt: "old"},
		{ID: "p3", DatasetID: "ds-1", SequenceNumber: 3, Prompt: "to-delete"},
	}
	c := newApplyMockClient(t, "ds-1", initial, hooks)

	op := datasetOp{
		Name: "ds", ID: "ds-1", ExamplesPresent: true,
		Examples: []translate.ExampleSpec{
			{SequenceNumber: 1, Prompt: "new"},
			{SequenceNumber: 2, Prompt: "fresh"},
		},
	}
	out := applyExampleDiff(t.Context(), c, op)
	if out.Err != nil {
		t.Fatalf("err: %v", out.Err)
	}
	if out.Inserts != 1 || out.Updates != 1 || out.Deletes != 1 {
		t.Errorf("counts = %+v", out)
	}
	if len(hooks.creates) != 1 || hooks.creates[0].SequenceNumber == nil || *hooks.creates[0].SequenceNumber != 2 {
		t.Errorf("creates = %+v", hooks.creates)
	}
	if len(hooks.updates) != 1 {
		t.Errorf("updates = %+v", hooks.updates)
	}
	if len(hooks.deletes) != 1 || hooks.deletes[0] != "p3" {
		t.Errorf("deletes = %+v", hooks.deletes)
	}
}

func TestApplyExampleDiff_NoOpWhenInSync(t *testing.T) {
	hooks := &applyMockServer{}
	initial := []wire.ExampleResponse{
		{ID: "p1", SequenceNumber: 1, Prompt: "a"},
	}
	c := newApplyMockClient(t, "ds-1", initial, hooks)

	op := datasetOp{
		Name: "ds", ID: "ds-1", ExamplesPresent: true,
		Examples: []translate.ExampleSpec{{SequenceNumber: 1, Prompt: "a"}},
	}
	out := applyExampleDiff(t.Context(), c, op)
	if out.Err != nil {
		t.Fatalf("err: %v", out.Err)
	}
	if out.Inserts+out.Updates+out.Deletes != 0 {
		t.Errorf("expected no-op, got %+v", out)
	}
	if len(hooks.creates)+len(hooks.updates)+len(hooks.deletes) != 0 {
		t.Errorf("no CRUD should have fired")
	}
	if out.Unchanged != 1 {
		t.Errorf("unchanged = %d, want 1", out.Unchanged)
	}
}

func TestApplyDatasets_RunsExampleReconcile(t *testing.T) {
	const dsID = "ds-1"
	mux := http.NewServeMux()
	hooks := &applyMockServer{}
	mux.HandleFunc("PATCH "+platform.APIV2Prefix+evalDatasetsResource.path+"/"+dsID, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, wire.DatasetResponse{ID: dsID, Name: "ds"})
	})
	mux.HandleFunc("GET "+datasetExamplesV2Path(dsID), func(w http.ResponseWriter, _ *http.Request) {
		hooks.listHits.Add(1)
		writeV2Page(w, []wire.ExampleResponse{})
	})
	mux.HandleFunc("POST "+datasetExamplesV2Path(dsID), func(w http.ResponseWriter, r *http.Request) {
		var req wire.CreateExampleRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		hooks.creates = append(hooks.creates, req)
		writeV2Single(w, http.StatusCreated, wire.ExampleResponse{ID: "x"})
	})
	c := newV2PlatformClient(t, mux)

	plan := &datasetKindPlan{
		Kind: "dataset",
		Updates: []datasetOp{{
			Name: "ds", ID: dsID,
			ExamplesPresent: true,
			Examples: []translate.ExampleSpec{
				{SequenceNumber: 1, Prompt: "x"},
			},
		}},
	}
	outcome := applyDatasets(t.Context(), c, plan, ApplyOptions{})
	if len(outcome.Failures) != 0 {
		t.Fatalf("unexpected failures: %+v", outcome.Failures)
	}
	if len(outcome.Examples) != 1 || outcome.Examples[0].Inserts != 1 {
		t.Errorf("examples outcome = %+v", outcome.Examples)
	}
	if len(hooks.creates) != 1 {
		t.Errorf("expected 1 example create, got %+v", hooks.creates)
	}
}
