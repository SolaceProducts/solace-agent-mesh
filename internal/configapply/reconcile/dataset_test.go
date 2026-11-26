// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func resolvedDataset(name, description string) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:        "dataset",
		Name:        name,
		SourceFile:  "datasets/" + name + ".yaml",
		Spec:        map[string]any{},
		Description: description,
	}
}

func TestPlanDatasets_CreateAndDelete(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedDataset("acme-eval", "ACME corpus")}
	descObsolete := "old"
	actual := []wire.DatasetResponse{{ID: "id-old", Name: "obsolete", Description: &descObsolete}}

	plan, err := planDatasets(desired, actual)
	if err != nil {
		t.Fatalf("planDatasets: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "acme-eval" {
		t.Errorf("Creates = %+v", plan.Creates)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].ID != "id-old" {
		t.Errorf("Deletes = %+v", plan.Deletes)
	}
	if len(plan.Updates) != 0 || len(plan.Unchanged) != 0 {
		t.Errorf("expected no updates/unchanged, got updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
}

func TestPlanDatasets_UnchangedWhenIdentical(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedDataset("acme-eval", "ACME corpus")}
	desc := "ACME corpus"
	actual := []wire.DatasetResponse{{ID: "id-1", Name: "acme-eval", Description: &desc}}

	plan, err := planDatasets(desired, actual)
	if err != nil {
		t.Fatalf("planDatasets: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v", plan.Unchanged)
	}
	if len(plan.Creates)+len(plan.Updates)+len(plan.Deletes) != 0 {
		t.Errorf("expected no diff, got creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanDatasets_UpdateOnDescriptionChange(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedDataset("acme-eval", "new description")}
	desc := "old description"
	actual := []wire.DatasetResponse{{ID: "id-1", Name: "acme-eval", Description: &desc}}

	plan, err := planDatasets(desired, actual)
	if err != nil {
		t.Fatalf("planDatasets: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-1" {
		t.Errorf("Updates = %+v", plan.Updates)
	}
	if d := plan.Updates[0].Update.Description; !d.Set || d.Value != "new description" {
		t.Errorf("Update.Description = %+v, want %q", d, "new description")
	}
	if plan.Updates[0].Update.Name.Set {
		t.Errorf("Update.Name = %+v, want it left out of the PATCH, since it did not change", plan.Updates[0].Update.Name)
	}
	if plan.Updates[0].Create.Description != "new description" {
		t.Errorf("Create.Description = %q, want the desired state the v1 PUT is built from", plan.Updates[0].Create.Description)
	}
}

func TestPlanDatasetExamples_CSVOnlyEditIsPendingUpdate(t *testing.T) {
	stored := []wire.ExampleResponse{{ID: "ex-1", SequenceNumber: 1, Prompt: "hi"}}
	plan := &datasetKindPlan{Kind: "dataset", Unchanged: []datasetOp{
		{Name: "edited", ID: "id-edited", ExamplesPresent: true, Examples: []translate.ExampleSpec{{SequenceNumber: 1, Prompt: "hello"}, {SequenceNumber: 2, Prompt: "new"}}},
		{Name: "same", ID: "id-same", ExamplesPresent: true, Examples: []translate.ExampleSpec{{SequenceNumber: 1, Prompt: "hi"}}},
		{Name: "no-file", ID: "id-no-file"},
	}}
	listed := map[string]bool{}
	list := func(_ context.Context, datasetID string) ([]wire.ExampleResponse, error) {
		listed[datasetID] = true
		return stored, nil
	}

	if err := planDatasetExamples(t.Context(), plan, list); err != nil {
		t.Fatalf("planDatasetExamples: %v", err)
	}
	if listed["id-no-file"] {
		t.Error("a dataset without examples_file must not list its examples")
	}
	if got := plan.Unchanged[0].ExampleChanges; got != (exampleCounts{Inserts: 1, Updates: 1}) {
		t.Errorf("edited ExampleChanges = %+v", got)
	}

	want := []PlanOp{
		{Kind: "dataset", Name: "edited", Action: ActionUpdate},
		{Kind: "dataset", Name: "same", Action: ActionUnchanged},
		{Kind: "dataset", Name: "no-file", Action: ActionUnchanged},
	}
	result := &PlanResult{Datasets: plan}
	if ops := result.Ops(); !reflect.DeepEqual(ops, want) {
		t.Errorf("Ops() = %+v, want %+v", ops, want)
	}
	if !result.Pending(false, false) {
		t.Error("a CSV-only edit must count as pending")
	}

	var buf bytes.Buffer
	renderDatasetPlan(&buf, plan, FormatOptions{NoColor: true})
	for _, line := range []string{"~ edited", "update (examples: +1 ~1 -0)", "3 datasets: 0 create, 1 update, 0 delete, 2 unchanged"} {
		if !strings.Contains(buf.String(), line) {
			t.Errorf("expected %q in output\n%s", line, buf.String())
		}
	}
}

func TestPlanDatasetExamples_ListErrorFailsPlan(t *testing.T) {
	plan := &datasetKindPlan{Kind: "dataset", Unchanged: []datasetOp{{Name: "acme", ID: "id-1", ExamplesPresent: true}}}
	list := func(context.Context, string) ([]wire.ExampleResponse, error) { return nil, errors.New("boom") }
	if err := planDatasetExamples(t.Context(), plan, list); err == nil || !strings.Contains(err.Error(), `"acme"`) {
		t.Errorf("err = %v, want a list failure naming the dataset", err)
	}
}
