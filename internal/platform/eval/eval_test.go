// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	apierr "github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type evalSvcSet struct {
	dataset    *DatasetService
	example    *ExampleService
	evaluator  *EvaluatorService
	experiment *ExperimentService
	datasets   *store.EvalDatasetRepo
	examples   *store.EvalExampleRepo
	artifacts  *store.EvalExampleArtifactRepo
	evaluators *store.EvalEvaluatorRepo
	runs       *store.EvalRunRepo
	exps       *store.EvalExperimentRepo
}

func newEvalSvcs(t *testing.T) (context.Context, *evalSvcSet) {
	t.Helper()
	db := store.TestDB(t)
	datasets := store.NewEvalDatasetRepo(db)
	examples := store.NewEvalExampleRepo(db)
	evaluators := store.NewEvalEvaluatorRepo(db)
	exps := store.NewEvalExperimentRepo(db)
	runs := store.NewEvalRunRepo(db)
	return t.Context(), &evalSvcSet{
		dataset:    NewDatasetService(datasets, examples, runs, nil),
		example:    NewExampleService(examples, datasets, nil),
		evaluator:  NewEvaluatorService(evaluators, nil),
		experiment: NewExperimentService(exps, datasets, evaluators, runs, nil),
		datasets:   datasets,
		examples:   examples,
		artifacts:  store.NewEvalExampleArtifactRepo(db),
		evaluators: evaluators,
		runs:       runs,
		exps:       exps,
	}
}

func withMaxExamples(t *testing.T, svcs *evalSvcSet, n int) {
	t.Helper()
	svcs.dataset.WithMaxExamples(n)
	svcs.example.WithMaxExamples(n)
}

func asValidationError(t *testing.T, err error) *ValidationError {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	return ve
}

func asBusinessRuleErr(t *testing.T, err error) *BusinessRuleViolationError {
	t.Helper()
	var br *BusinessRuleViolationError
	if !errors.As(err, &br) {
		t.Fatalf("expected *BusinessRuleViolationError, got %T: %v", err, err)
	}
	return br
}

func asNotFoundError(t *testing.T, err error) *NotFoundError {
	t.Helper()
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected *NotFoundError, got %T: %v", err, err)
	}
	return nf
}

func mustCreateDataset(ctx context.Context, t *testing.T, svc *DatasetService, name string) string {
	t.Helper()
	resp, err := svc.Create(ctx, CreateDatasetRequest{Name: name}, "alice")
	if err != nil {
		t.Fatalf("create dataset %q: %v", name, err)
	}
	return resp.ID
}

func mustCreateLLMJudge(ctx context.Context, t *testing.T, svc *EvaluatorService, name string) string {
	t.Helper()
	model := "gpt-4o"
	tmpl := "Score the response: {{Response}}"
	resp, err := svc.Create(ctx, CreateEvaluatorRequest{
		Name:           name,
		Description:    "test evaluator",
		Type:           "llm_judge",
		Model:          &model,
		PromptTemplate: &tmpl,
		ChoiceScores:   map[string]any{"Yes": 1.0, "No": 0.0},
	}, "alice")
	if err != nil {
		t.Fatalf("create evaluator %q: %v", name, err)
	}
	return resp.ID
}

func seedActiveRun(ctx context.Context, t *testing.T, svc *evalSvcSet, experimentID string) {
	t.Helper()
	run := &store.EvalRun{
		ExperimentID: experimentID,
		RunGroupID:   idgen.New(),
		Status:       "pending",
		Model:        "gpt-4o",
		Source:       "manual",
		StartedBy:    "alice",
	}
	if err := svc.runs.Create(ctx, run); err != nil {
		t.Fatalf("seed active run: %v", err)
	}
}

func TestDatasetService_CreateAndGet(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)

	desc := "first dataset"
	resp, err := svcs.dataset.Create(ctx, CreateDatasetRequest{Name: "Dataset A", Description: desc}, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.Name != "Dataset A" || resp.CreatedBy != "alice" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Description == nil || *resp.Description != desc {
		t.Fatalf("description not persisted: %+v", resp.Description)
	}
	if resp.ExampleCount != 0 || resp.ExperimentCount != 0 {
		t.Fatalf("expected zero counts, got %d/%d", resp.ExampleCount, resp.ExperimentCount)
	}

	got, err := svcs.dataset.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Dataset A" {
		t.Fatalf("get name: %q", got.Name)
	}
}

func TestDatasetService_CreateValidation(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)

	if _, err := svcs.dataset.Create(ctx, CreateDatasetRequest{Name: ""}, "alice"); err == nil {
		t.Fatal("expected error for empty name")
	} else {
		ve := asValidationError(t, err)
		if ve.Field != "name" {
			t.Fatalf("field = %q", ve.Field)
		}
	}

	long := strings.Repeat("x", maxDatasetNameLen+1)
	if _, err := svcs.dataset.Create(ctx, CreateDatasetRequest{Name: long}, "alice"); err == nil {
		t.Fatal("expected error for over-long name")
	}

	if _, err := svcs.dataset.Create(ctx, CreateDatasetRequest{Name: "ok", Description: strings.Repeat("d", 1001)}, "alice"); err == nil {
		t.Fatal("expected error for over-long description")
	}
}

func TestDatasetService_DuplicateNameConflict(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	mustCreateDataset(ctx, t, svcs.dataset, "DupName")
	_, err := svcs.dataset.Create(ctx, CreateDatasetRequest{Name: "DupName"}, "alice")
	if err == nil {
		t.Fatal("expected conflict on duplicate name")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConflictError, got %T: %v", err, err)
	}
}

func TestDatasetService_UpdatePartial(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	id := mustCreateDataset(ctx, t, svcs.dataset, "Orig")
	newName := "Renamed"
	updated, err := svcs.dataset.Update(ctx, id, UpdateDatasetRequest{Name: &newName}, "bob")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Renamed" || updated.UpdatedBy != "bob" {
		t.Fatalf("unexpected update: %+v", updated)
	}
}

func TestDatasetService_DeleteBlockedByActiveRun(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Dataset for delete")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-delete")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name:               "Exp1",
		DatasetID:          dsID,
		TargetAgent:        "TargetAgent",
		EvaluatorIDs:       []string{evID},
		PrimaryEvaluatorID: new(evID),
	}, "alice")
	if err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	seedActiveRun(ctx, t, svcs, exp.ID)

	err = svcs.dataset.Delete(ctx, dsID, "alice")
	if err == nil {
		t.Fatal("expected DATASET_HAS_ACTIVE_RUNS")
	}
	br := asBusinessRuleErr(t, err)
	if br.Code != "DATASET_HAS_ACTIVE_RUNS" {
		t.Fatalf("code = %q", br.Code)
	}
}

func TestDatasetService_DeleteSucceedsWithoutActiveRuns(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Empty")
	if err := svcs.dataset.Delete(ctx, dsID, "alice"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svcs.dataset.Get(ctx, dsID); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestDatasetService_ListPaginatedDefaultsAndFilter(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	mustCreateDataset(ctx, t, svcs.dataset, "Apple")
	mustCreateDataset(ctx, t, svcs.dataset, "Banana")
	mustCreateDataset(ctx, t, svcs.dataset, "Cherry")

	page, err := svcs.dataset.ListPaginated(ctx, 0, 0, "", "", "")
	if err != nil {
		t.Fatalf("list paginated: %v", err)
	}
	if page.Meta.Pagination.PageNumber != 1 || page.Meta.Pagination.PageSize != 20 {
		t.Fatalf("defaults wrong: %+v", page.Meta.Pagination)
	}
	if page.Meta.Pagination.Count != 3 || len(page.Data) != 3 {
		t.Fatalf("count = %d, data len = %d", page.Meta.Pagination.Count, len(page.Data))
	}

	page, err = svcs.dataset.ListPaginated(ctx, 1, 10, "ana", "", "")
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if page.Meta.Pagination.Count != 1 || page.Data[0].Name != "Banana" {
		t.Fatalf("filter result unexpected: %+v", page.Data)
	}
}

func TestDatasetService_ListPaginatedNextPage(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	for i := range 5 {
		mustCreateDataset(ctx, t, svcs.dataset, strings.Repeat("x", i+1))
	}
	page, err := svcs.dataset.ListPaginated(ctx, 1, 2, "", "", "")
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if page.Meta.Pagination.NextPage == nil || *page.Meta.Pagination.NextPage != 2 {
		t.Fatalf("next page = %v", page.Meta.Pagination.NextPage)
	}
	if page.Meta.Pagination.TotalPages != 3 {
		t.Fatalf("total pages = %d", page.Meta.Pagination.TotalPages)
	}
}

func TestDatasetService_ImportExamplesCSVCap(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	withMaxExamples(t, svcs, 100)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Imports")

	preFill := make([]*store.EvalExample, 0, 97)
	for i := range 97 {
		preFill = append(preFill, &store.EvalExample{
			DatasetID: dsID, SequenceNumber: i + 1, Prompt: "preexisting",
		})
	}
	if err := svcs.examples.BulkCreate(ctx, preFill); err != nil {
		t.Fatalf("seed: %v", err)
	}

	csvBody := "prompt,expected_response\nA,a\nB,b\nC,c\nD,d\nE,e\nF,f\nG,g\nH,h\nI,i\nJ,j\n"
	res, err := svcs.dataset.ImportExamples(ctx, dsID, csvBody, "text/csv", "alice")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 3 || res.Dropped != 7 || res.Limit != 100 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestDatasetService_ImportExamplesCSVUnderCap(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Imports2")
	csvBody := "prompt,expected_response\nA,a\nB,b\nC,c\n"
	res, err := svcs.dataset.ImportExamples(ctx, dsID, csvBody, "text/csv", "alice")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 3 || res.Skipped != 0 || res.Dropped != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestDatasetService_ImportExamplesCSVSkipsEmptyPrompt(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Imports3")
	csvBody := "prompt,expected_response\nValid,a\n,still_dropped\n   ,whitespace_only\nAnother,b\n"
	res, err := svcs.dataset.ImportExamples(ctx, dsID, csvBody, "text/csv", "alice")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 2 || res.Skipped != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestDatasetService_ImportExamplesCSVRejectsMissingColumn(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Imports4")
	_, err := svcs.dataset.ImportExamples(ctx, dsID, "expected_response\nfoo\n", "text/csv", "alice")
	if err == nil {
		t.Fatal("expected validation error for missing prompt column")
	}
	_ = asValidationError(t, err)
}

func TestDatasetService_ImportExamplesJSONCap(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	withMaxExamples(t, svcs, 100)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "ImportsJSON")
	preFill := make([]*store.EvalExample, 0, 97)
	for i := range 97 {
		preFill = append(preFill, &store.EvalExample{
			DatasetID: dsID, SequenceNumber: i + 1, Prompt: "preexisting",
		})
	}
	if err := svcs.examples.BulkCreate(ctx, preFill); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := `[{"prompt":"A"},{"prompt":"B"},{"prompt":"C"},{"prompt":"D"},{"prompt":"E"},{"prompt":"F"},{"prompt":"G"},{"prompt":"H"},{"prompt":"I"},{"prompt":"J"}]`
	res, err := svcs.dataset.ImportExamples(ctx, dsID, body, "application/json", "alice")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 3 || res.Dropped != 7 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestDatasetService_ImportExamplesJSONInvalidShape(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "ImportsJSON2")
	_, err := svcs.dataset.ImportExamples(ctx, dsID, `{"prompt":"oops"}`, "application/json", "alice")
	if err == nil {
		t.Fatal("expected error for non-array JSON body")
	}
	_ = asValidationError(t, err)

	_, err = svcs.dataset.ImportExamples(ctx, dsID, "not json", "application/json", "alice")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	_ = asValidationError(t, err)
}

func TestDatasetService_ExportExamples(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Export")
	if _, err := svcs.dataset.ImportExamples(ctx, dsID, "prompt,expected_response\nA,1\nB,2\n", "text/csv", "alice"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	csvOut, err := svcs.dataset.ExportExamplesCSV(ctx, dsID)
	if err != nil {
		t.Fatalf("export csv: %v", err)
	}
	if !strings.Contains(csvOut, "sequence_number,prompt,expected_response") || !strings.Contains(csvOut, "1,A,1") {
		t.Fatalf("csv output:\n%s", csvOut)
	}

	jsonOut, err := svcs.dataset.ExportExamplesJSON(ctx, dsID)
	if err != nil {
		t.Fatalf("export json: %v", err)
	}
	if len(jsonOut) != 2 || jsonOut[0].Prompt != "A" {
		t.Fatalf("json output: %+v", jsonOut)
	}
}

func TestDatasetService_NotFoundOnMissingDataset(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	_, err := svcs.dataset.Get(ctx, idgen.New())
	_ = asNotFoundError(t, err)
}

func TestExampleService_CreateAndPaginate(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Examples")
	for i := range 3 {
		_, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: strings.Repeat("p", i+1)})
		if err != nil {
			t.Fatalf("create example %d: %v", i, err)
		}
	}

	page, err := svcs.example.ListPaginated(ctx, dsID, 1, 10, "", "", false)
	if err != nil {
		t.Fatalf("list paginated: %v", err)
	}
	if page.Meta.Pagination.Count != 3 {
		t.Fatalf("count = %d", page.Meta.Pagination.Count)
	}
}

func TestExampleService_ListMissingDataset(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	_, err := svcs.example.List(ctx, idgen.New())
	_ = asNotFoundError(t, err)
	_, err = svcs.example.ListPaginated(ctx, idgen.New(), 1, 10, "", "", false)
	_ = asNotFoundError(t, err)
}

func TestExampleService_CreateBlocksAtCap(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Cap")
	preFill := make([]*store.EvalExample, 0, MaxExamplesPerDataset)
	for i := range MaxExamplesPerDataset {
		preFill = append(preFill, &store.EvalExample{
			DatasetID: dsID, SequenceNumber: i + 1, Prompt: "x",
		})
	}
	if err := svcs.examples.BulkCreate(ctx, preFill); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "one too many"})
	if err == nil {
		t.Fatal("expected DATASET_EXAMPLE_CAP_REACHED")
	}
	br := asBusinessRuleErr(t, err)
	if br.Code != "DATASET_EXAMPLE_CAP_REACHED" {
		t.Fatalf("code = %q", br.Code)
	}
}

func TestExampleService_CreateSucceedsJustBelowCap(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "JustBelow")
	preFill := make([]*store.EvalExample, 0, MaxExamplesPerDataset-1)
	for i := range MaxExamplesPerDataset - 1 {
		preFill = append(preFill, &store.EvalExample{
			DatasetID: dsID, SequenceNumber: i + 1, Prompt: "x",
		})
	}
	if err := svcs.examples.BulkCreate(ctx, preFill); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "fits"}); err != nil {
		t.Fatalf("expected success at cap-1: %v", err)
	}
}

func TestExampleService_UpdatePartial(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Updates")
	exp := "expected"
	resp, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "orig", ExpectedResponse: &exp})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	newPrompt := "updated"
	upd, err := svcs.example.Update(ctx, resp.ID, UpdateExampleRequest{Prompt: &newPrompt})
	if err != nil {
		t.Fatalf("update prompt: %v", err)
	}
	if upd.Prompt != "updated" || upd.ExpectedResponse == nil || *upd.ExpectedResponse != "expected" {
		t.Fatalf("partial update lost field: %+v", upd)
	}

	upd2, err := svcs.example.Update(ctx, resp.ID, UpdateExampleRequest{})
	if err != nil {
		t.Fatalf("noop update: %v", err)
	}
	if upd2.Prompt != "updated" {
		t.Fatalf("noop changed state: %+v", upd2)
	}

	upd3, err := svcs.example.Update(ctx, resp.ID, UpdateExampleRequest{ExpectedResponse: nil, ExpectedResponseSet: true})
	if err != nil {
		t.Fatalf("clear update: %v", err)
	}
	if upd3.ExpectedResponse != nil {
		t.Fatalf("expected SQL NULL after explicit clear, got %v (*=%q)", upd3.ExpectedResponse, *upd3.ExpectedResponse)
	}

	empty := ""
	upd4, err := svcs.example.Update(ctx, resp.ID, UpdateExampleRequest{ExpectedResponse: &empty, ExpectedResponseSet: true})
	if err != nil {
		t.Fatalf("set empty: %v", err)
	}
	if upd4.ExpectedResponse == nil || *upd4.ExpectedResponse != "" {
		t.Fatalf("expected literal empty string, got %v", upd4.ExpectedResponse)
	}

	bothPrompt := "p2"
	bothExp := "e2"
	upd5, err := svcs.example.Update(ctx, resp.ID, UpdateExampleRequest{
		Prompt:              &bothPrompt,
		ExpectedResponse:    &bothExp,
		ExpectedResponseSet: true,
	})
	if err != nil {
		t.Fatalf("update both: %v", err)
	}
	if upd5.Prompt != "p2" || upd5.ExpectedResponse == nil || *upd5.ExpectedResponse != "e2" {
		t.Fatalf("both-fields update wrong: %+v", upd5)
	}

	onlyExp := "e3"
	upd6, err := svcs.example.Update(ctx, resp.ID, UpdateExampleRequest{
		ExpectedResponse: &onlyExp, ExpectedResponseSet: true,
	})
	if err != nil {
		t.Fatalf("update expected only: %v", err)
	}
	if upd6.Prompt != "p2" || upd6.ExpectedResponse == nil || *upd6.ExpectedResponse != "e3" {
		t.Fatalf("expected-only update wrong: %+v", upd6)
	}
}

func TestExampleService_ListPaginatedContentFilter(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Filter")
	for _, p := range []string{"alpha needle", "beta ", "gamma needle"} {
		_, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: p})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	page, err := svcs.example.ListPaginated(ctx, dsID, 1, 10, "needle", "", false)
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if page.Meta.Pagination.Count != 2 {
		t.Fatalf("filter count = %d, want 2", page.Meta.Pagination.Count)
	}
	for _, ex := range page.Data {
		if !strings.Contains(strings.ToLower(ex.Prompt), "needle") {
			t.Fatalf("filter leaked: %q", ex.Prompt)
		}
	}
}

func TestDatasetService_ImportExamplesAllDroppedAtCap(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "AtCap")
	preFill := make([]*store.EvalExample, 0, MaxExamplesPerDataset)
	for i := range MaxExamplesPerDataset {
		preFill = append(preFill, &store.EvalExample{
			DatasetID: dsID, SequenceNumber: i + 1, Prompt: "x",
		})
	}
	if err := svcs.examples.BulkCreate(ctx, preFill); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := svcs.dataset.ImportExamples(ctx, dsID, "prompt\nA\nB\nC\nD\nE\n", "text/csv", "alice")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 0 || res.Dropped != 5 {
		t.Fatalf("at-cap result: %+v", res)
	}
}

func TestExperimentService_ListByDatasetFilter(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	ds1 := mustCreateDataset(ctx, t, svcs.dataset, "Bucket-A")
	ds2 := mustCreateDataset(ctx, t, svcs.dataset, "Bucket-B")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-bucket")
	for _, ds := range []string{ds1, ds2} {
		if _, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
			Name: "Exp-" + ds, DatasetID: ds, TargetAgent: "T",
			EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
		}, "alice"); err != nil {
			t.Fatalf("create %s: %v", ds, err)
		}
	}
	got, err := svcs.experiment.List(ctx, ds1, "", "")
	if err != nil {
		t.Fatalf("list filter: %v", err)
	}
	if len(got) != 1 || got[0].DatasetID == nil || *got[0].DatasetID != ds1 {
		t.Fatalf("dataset filter result: %+v", got)
	}
}

func TestExperimentService_UpdateAllProvidedFieldsForwarded(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsA := mustCreateDataset(ctx, t, svcs.dataset, "DS-Full-A")
	dsB := mustCreateDataset(ctx, t, svcs.dataset, "DS-Full-B")
	evA := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-full-a")
	evB := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-full-b")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "Original", DatasetID: dsA, TargetAgent: "agent-orig",
		EvaluatorIDs: []string{evA}, PrimaryEvaluatorID: new(evA),
	}, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newName := "Renamed"
	newAgent := "agent-new"
	runs := 5
	maxw := 8
	upd, err := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{
		Name:                  &newName,
		NameSet:               true,
		DatasetID:             &dsB,
		TargetAgent:           &newAgent,
		Models:                []map[string]any{{"alias": "m1"}, {"alias": "m2"}},
		ModelsSet:             true,
		RunsPerExample:        &runs,
		MaxWorkers:            &maxw,
		EvaluatorIDs:          []string{evB},
		EvaluatorIDsSet:       true,
		PrimaryEvaluatorID:    &evB,
		PrimaryEvaluatorIDSet: true,
	}, "bob")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "Renamed" || upd.TargetAgent != "agent-new" || upd.RunsPerExample != 5 || upd.MaxWorkers != 8 {
		t.Fatalf("scalar fields not forwarded: %+v", upd)
	}
	if upd.DatasetID == nil || *upd.DatasetID != dsB {
		t.Fatalf("dataset_id not switched: %+v", upd.DatasetID)
	}
	if len(upd.Models) != 2 {
		t.Fatalf("models not forwarded: %+v", upd.Models)
	}
	if len(upd.EvaluatorIDs) != 1 || upd.EvaluatorIDs[0] != evB {
		t.Fatalf("evaluator_ids not replaced: %+v", upd.EvaluatorIDs)
	}
	if upd.PrimaryEvaluatorID == nil || *upd.PrimaryEvaluatorID != evB {
		t.Fatalf("primary not switched: %+v", upd.PrimaryEvaluatorID)
	}
}

func TestExampleService_UpdateNotFound(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	_, err := svcs.example.Update(ctx, idgen.New(), UpdateExampleRequest{Prompt: new("x")})
	_ = asNotFoundError(t, err)
}

func TestEvaluatorService_CreateValidation(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)

	cases := []struct {
		name  string
		req   CreateEvaluatorRequest
		field string
	}{
		{
			name:  "empty name",
			req:   CreateEvaluatorRequest{Name: "", Description: "d", Type: "llm_judge"},
			field: "name",
		},
		{
			name:  "missing description",
			req:   CreateEvaluatorRequest{Name: "n", Description: "", Type: "llm_judge"},
			field: "description",
		},
		{
			name:  "non-llm-judge type rejected",
			req:   CreateEvaluatorRequest{Name: "n", Description: "d", Type: "heuristic"},
			field: "type",
		},
		{
			name:  "missing model",
			req:   CreateEvaluatorRequest{Name: "n", Description: "d", Type: "llm_judge", PromptTemplate: new("{{Response}}"), ChoiceScores: map[string]any{"Y": 1.0, "N": 0.0}},
			field: "model",
		},
		{
			name:  "missing prompt_template",
			req:   CreateEvaluatorRequest{Name: "n", Description: "d", Type: "llm_judge", Model: new("gpt"), ChoiceScores: map[string]any{"Y": 1.0, "N": 0.0}},
			field: "prompt_template",
		},
		{
			name:  "prompt_template missing placeholder",
			req:   CreateEvaluatorRequest{Name: "n", Description: "d", Type: "llm_judge", Model: new("gpt"), PromptTemplate: new("no placeholder"), ChoiceScores: map[string]any{"Y": 1.0, "N": 0.0}},
			field: "prompt_template",
		},
		{
			name:  "single choice score rejected",
			req:   CreateEvaluatorRequest{Name: "n", Description: "d", Type: "llm_judge", Model: new("gpt"), PromptTemplate: new("{{Response}}"), ChoiceScores: map[string]any{"Yes": 1.0}},
			field: "choice_scores",
		},
		{
			name: "too many choice scores",
			req: CreateEvaluatorRequest{
				Name: "n", Description: "d", Type: "llm_judge",
				Model: new("gpt"), PromptTemplate: new("{{Response}}"),
				ChoiceScores: tooManyChoices(),
			},
			field: "choice_scores",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svcs.evaluator.Create(ctx, tc.req, "alice")
			if err == nil {
				t.Fatal("expected validation error")
			}
			ve := asValidationError(t, err)
			if ve.Field != tc.field {
				t.Fatalf("field = %q, want %q", ve.Field, tc.field)
			}
		})
	}
}

func TestEvaluatorService_CreateMaxAllowedChoices(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	scores := make(map[string]any, 26)
	for i := range 26 {
		scores[string(rune('A'+i))] = float64(i)
	}
	if _, err := svcs.evaluator.Create(ctx, CreateEvaluatorRequest{
		Name: "max", Description: "d", Type: "llm_judge",
		Model: new("gpt"), PromptTemplate: new("{{Response}}"), ChoiceScores: scores,
	}, "alice"); err != nil {
		t.Fatalf("expected success at 26 choices: %v", err)
	}
}

func TestEvaluatorService_PlaceholderCaseInsensitiveAndCanonicalized(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	resp, err := svcs.evaluator.Create(ctx, CreateEvaluatorRequest{
		Name: "case", Description: "d", Type: "llm_judge",
		Model: new("gpt"), PromptTemplate: new("Judge {{response}} vs {{expected response}}"), ChoiceScores: map[string]any{"Y": 1.0, "N": 0.0},
	}, "alice")
	if err != nil {
		t.Fatalf("expected case-insensitive placeholder to be accepted: %v", err)
	}
	if resp.PromptTemplate == nil || *resp.PromptTemplate != "Judge {{Response}} vs {{Expected Response}}" {
		t.Errorf("prompt_template = %v, want canonicalized %q", resp.PromptTemplate, "Judge {{Response}} vs {{Expected Response}}")
	}
}

func TestEvaluatorService_UpdatePartial(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	id := mustCreateLLMJudge(ctx, t, svcs.evaluator, "u1")

	newName := "renamed"
	resp, err := svcs.evaluator.Update(ctx, id, UpdateEvaluatorRequest{Name: &newName, NameSet: true}, "bob")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if resp.Name != "renamed" || resp.UpdatedBy != "bob" {
		t.Fatalf("unexpected update: %+v", resp)
	}
	if resp.ChoiceScores == nil || len(resp.ChoiceScores) != 2 {
		t.Fatalf("choice_scores lost: %+v", resp.ChoiceScores)
	}
}

func TestEvaluatorService_UpdateRejectsExplicitNullCriticalFields(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	id := mustCreateLLMJudge(ctx, t, svcs.evaluator, "u2")

	cases := []struct {
		name  string
		req   UpdateEvaluatorRequest
		field string
	}{
		{
			name:  "explicit null name",
			req:   UpdateEvaluatorRequest{NameSet: true, Name: nil},
			field: "name",
		},
		{
			name:  "explicit null prompt_template",
			req:   UpdateEvaluatorRequest{PromptTemplateSet: true, PromptTemplate: nil},
			field: "prompt_template",
		},
		{
			name:  "explicit null model",
			req:   UpdateEvaluatorRequest{ModelSet: true, Model: nil},
			field: "model",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svcs.evaluator.Update(ctx, id, tc.req, "bob")
			if err == nil {
				t.Fatal("expected validation error")
			}
			ve := asValidationError(t, err)
			if ve.Field != tc.field {
				t.Fatalf("field = %q, want %q", ve.Field, tc.field)
			}
		})
	}
}

func TestEvaluatorService_UpdatePromptTemplatePlaceholder(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	id := mustCreateLLMJudge(ctx, t, svcs.evaluator, "u3")
	bad := "no placeholder"
	_, err := svcs.evaluator.Update(ctx, id, UpdateEvaluatorRequest{PromptTemplateSet: true, PromptTemplate: &bad}, "bob")
	if err == nil {
		t.Fatal("expected placeholder error")
	}
	_ = asValidationError(t, err)
}

func TestEvaluatorService_UpdateChoiceScoresOmittedKeepsExisting(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	id := mustCreateLLMJudge(ctx, t, svcs.evaluator, "u4")
	desc := "new"
	resp, err := svcs.evaluator.Update(ctx, id, UpdateEvaluatorRequest{Description: &desc, DescriptionSet: true}, "bob")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if resp.ChoiceScores == nil || len(resp.ChoiceScores) != 2 {
		t.Fatalf("choice_scores lost: %+v", resp.ChoiceScores)
	}
}

func TestEvaluatorService_UpdateRejectsBadChoiceScores(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	id := mustCreateLLMJudge(ctx, t, svcs.evaluator, "u5")
	_, err := svcs.evaluator.Update(ctx, id, UpdateEvaluatorRequest{ChoiceScoresSet: true, ChoiceScores: map[string]any{"only": 1.0}}, "bob")
	if err == nil {
		t.Fatal("expected single-choice rejection")
	}
	ve := asValidationError(t, err)
	if ve.Field != "choice_scores" {
		t.Fatalf("field = %q", ve.Field)
	}
}

func TestEvaluatorService_DefaultProtected(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	tmpl := "{{Response}}"
	choices := `{"Yes":1.0,"No":0.0}`
	def := &store.EvalEvaluator{
		Name: "BuiltIn", Type: "heuristic", IsDefault: true,
		PromptTemplate: &tmpl, ChoiceScores: &choices,
		CreatedBy: "system", UpdatedBy: "system",
	}
	if err := svcs.evaluators.Create(ctx, def); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	newName := "x"
	if _, err := svcs.evaluator.Update(ctx, def.ID, UpdateEvaluatorRequest{Name: &newName, NameSet: true}, "alice"); err == nil {
		t.Fatal("expected protected update rejection")
	} else {
		br := asBusinessRuleErr(t, err)
		if br.Code != "EVALUATOR_IS_SYSTEM_DEFAULT" {
			t.Fatalf("code = %q", br.Code)
		}
	}
	if err := svcs.evaluator.Delete(ctx, def.ID); err == nil {
		t.Fatal("expected protected delete rejection")
	} else {
		_ = asBusinessRuleErr(t, err)
	}
}

func TestEvaluatorService_BuiltinJudgeKeepsAModelWhoseConfigIsGone(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	svcs.evaluator.SetModelTypeLookup(fakeModelTypes(map[string]string{"gpt": store.ModelTypeChat}))
	tmpl := "{{Response}}"
	choices := `{"Yes":1.0,"No":0.0}`
	gone := "deleted-model"
	judge := &store.EvalEvaluator{
		Name: "BuiltInJudge", Type: evaluatorTypeLLMJudge, IsDefault: true, Model: &gone,
		PromptTemplate: &tmpl, ChoiceScores: &choices,
		CreatedBy: "system", UpdatedBy: "system",
	}
	if err := svcs.evaluators.Create(ctx, judge); err != nil {
		t.Fatalf("seed built-in judge: %v", err)
	}

	got, err := svcs.evaluator.Update(ctx, judge.ID, UpdateEvaluatorRequest{Model: &gone, ModelSet: true}, "alice")
	if err != nil {
		t.Fatalf("resaving the unchanged model: %v", err)
	}
	if got.Model == nil || *got.Model != gone {
		t.Errorf("model = %v, want %q", got.Model, gone)
	}

	other := "also-gone"
	if _, err := svcs.evaluator.Update(ctx, judge.ID, UpdateEvaluatorRequest{Model: &other, ModelSet: true}, "alice"); err == nil {
		t.Error("switching to an unconfigured model was accepted, want a validation error")
	}
}

func TestEvaluatorService_DeleteNotFound(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	if err := svcs.evaluator.Delete(ctx, idgen.New()); err == nil {
		t.Fatal("expected not found")
	} else {
		_ = asNotFoundError(t, err)
	}
}

func TestEvaluatorService_DeleteWhileAttachedSucceedsAndCascadesJunction(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-delete-in-use")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-delete-in-use")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "Exp-delete-in-use", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
		Models: []map[string]any{{"alias": "a", "provider_id": "p", "model_name": "m"}},
	}, "alice")
	if err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	if err = svcs.evaluator.Delete(ctx, evID); err != nil {
		t.Fatalf("delete attached evaluator: %v", err)
	}
	ids, err := svcs.exps.GetEvaluatorIDs(ctx, exp.ID)
	if err != nil {
		t.Fatalf("get evaluator ids: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("junction rows after evaluator delete = %v, want []", ids)
	}
}

func TestExperimentService_CreateValidatesDatasetAndEvaluators(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "ExpDS")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev1")

	_, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "x", DatasetID: idgen.New(), TargetAgent: "T",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
	}, "alice")
	_ = asNotFoundError(t, err)

	_, err = svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "x", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{},
	}, "alice")
	if err == nil {
		t.Fatal("expected validation error for empty evaluator_ids")
	}
	_ = asValidationError(t, err)

	_, err = svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "x", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{idgen.New()},
	}, "alice")
	_ = asValidationError(t, err)

	other := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev2")
	_ = other
	_, err = svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "x", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs:       []string{evID},
		PrimaryEvaluatorID: new("not-in-set"),
	}, "alice")
	if err == nil {
		t.Fatal("expected primary-not-in-set rejection")
	}
	ve := asValidationError(t, err)
	if ve.Field != "primary_evaluator_id" {
		t.Fatalf("field = %q", ve.Field)
	}
}

func TestExperimentService_CreateGet(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "ExpDS2")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "evX")

	resp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "Exp1", DatasetID: dsID, TargetAgent: "TargetAgent",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
		Models: []map[string]any{{"alias": "default", "provider_id": "openai", "model_name": "gpt-4o"}},
	}, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.RunsPerExample != 1 || resp.MaxWorkers != 4 {
		t.Fatalf("defaults wrong: %+v", resp)
	}
	if resp.DatasetID == nil || *resp.DatasetID != dsID || resp.DatasetName == nil || *resp.DatasetName != "ExpDS2" {
		t.Fatalf("dataset enrichment missing: %+v", resp)
	}
	if len(resp.EvaluatorIDs) != 1 || resp.EvaluatorIDs[0] != evID {
		t.Fatalf("evaluator_ids = %v", resp.EvaluatorIDs)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("models lost: %+v", resp.Models)
	}
}

func TestExperimentService_UpdatePartialOnlyForwardsSet(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-U")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-U")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpU", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
		Models: []map[string]any{{"alias": "a", "provider_id": "p", "model_name": "m"}},
	}, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newModels := []map[string]any{{"alias": "b", "provider_id": "p", "model_name": "m"}}
	upd, err := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{Models: newModels, ModelsSet: true}, "bob")
	if err != nil {
		t.Fatalf("update models: %v", err)
	}
	if upd.Name != "ExpU" || upd.TargetAgent != "T" || upd.RunsPerExample != 1 {
		t.Fatalf("unrelated fields drifted: %+v", upd)
	}
	if len(upd.Models) != 1 || upd.Models[0]["alias"] != "b" {
		t.Fatalf("models not updated: %+v", upd.Models)
	}
}

func TestExperimentService_UpdateEvaluatorIDsReplacesAttachments(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-Re")
	evA := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-a")
	evB := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-b")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpRe", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evA}, PrimaryEvaluatorID: new(evA),
	}, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newIDs := []string{evB}
	upd, err := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{
		EvaluatorIDs: newIDs, EvaluatorIDsSet: true,
		PrimaryEvaluatorID: &evB, PrimaryEvaluatorIDSet: true,
	}, "bob")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(upd.EvaluatorIDs) != 1 || upd.EvaluatorIDs[0] != evB {
		t.Fatalf("evaluator_ids = %v", upd.EvaluatorIDs)
	}
}

func TestExperimentService_UpdatePrimaryAloneValidatesAgainstExistingSet(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-PA")
	evA := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-pa-a")
	evB := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-pa-b")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpPA", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evA}, PrimaryEvaluatorID: new(evA),
	}, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{
		PrimaryEvaluatorID: &evB, PrimaryEvaluatorIDSet: true,
	}, "bob")
	if err == nil {
		t.Fatal("expected primary-not-in-set rejection")
	}
	_ = asValidationError(t, err)
}

func TestExperimentService_DeleteBlockedByActiveRuns(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-D")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-d")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpD", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
	}, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	seedActiveRun(ctx, t, svcs, exp.ID)
	err = svcs.experiment.Delete(ctx, exp.ID, "alice")
	if err == nil {
		t.Fatal("expected EXPERIMENT_HAS_ACTIVE_RUNS")
	}
	br := asBusinessRuleErr(t, err)
	if br.Code != "EXPERIMENT_HAS_ACTIVE_RUNS" {
		t.Fatalf("code = %q", br.Code)
	}
}

func TestExperimentService_ListFilters(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-L")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-l")
	if _, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpL1", DatasetID: dsID, TargetAgent: "agent-a",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
	}, "alice"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpL2", DatasetID: dsID, TargetAgent: "agent-b",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
	}, "alice"); err != nil {
		t.Fatalf("create: %v", err)
	}
	all, err := svcs.experiment.List(ctx, "", "", "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 experiments, got %d", len(all))
	}
	filtered, err := svcs.experiment.List(ctx, "", "", "agent-a")
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].TargetAgent != "agent-a" {
		t.Fatalf("filter result: %+v", filtered)
	}
}

func tooManyChoices() map[string]any {
	out := make(map[string]any, 27)
	for i := range 27 {
		out[strings.Repeat("x", i+1)] = float64(i)
	}
	return out
}

var _ error = (*apierr.ValidationError)(nil)
var _ error = (*apierr.BusinessRuleViolationError)(nil)

func TestDatasetService_ListNonPaginated(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	mustCreateDataset(ctx, t, svcs.dataset, "DS-A")
	mustCreateDataset(ctx, t, svcs.dataset, "DS-B")
	out, err := svcs.dataset.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
}

func TestEvaluatorService_ListAndGet(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	id := mustCreateLLMJudge(ctx, t, svcs.evaluator, "lg-1")
	got, err := svcs.evaluator.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != id {
		t.Fatalf("id = %q", got.ID)
	}
	out, err := svcs.evaluator.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 || out[0].ID != id {
		t.Fatalf("list result: %+v", out)
	}
	if _, err := svcs.evaluator.Get(ctx, idgen.New()); err == nil {
		t.Fatal("expected not found")
	} else {
		_ = asNotFoundError(t, err)
	}
}

func TestExampleService_GetAndDelete(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-GD")
	resp, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svcs.example.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != resp.ID {
		t.Fatalf("id mismatch")
	}
	if err := svcs.example.Delete(ctx, resp.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svcs.example.Get(ctx, resp.ID); err == nil {
		t.Fatal("expected NotFound after delete")
	} else {
		_ = asNotFoundError(t, err)
	}
	if err := svcs.example.Delete(ctx, idgen.New()); err == nil {
		t.Fatal("expected NotFound on second delete")
	}
}

func TestExperimentService_DeleteSucceeds(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-DelOk")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-delok")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "Del-OK", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
	}, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svcs.experiment.Delete(ctx, exp.ID, "alice"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svcs.experiment.Delete(ctx, exp.ID, "alice"); err == nil {
		t.Fatal("expected not found on second delete")
	}
	if _, err := svcs.experiment.Get(ctx, exp.ID); err == nil {
		t.Fatal("expected not found after delete")
	}
}

func TestExperimentService_UpdateNotFoundAndConflict(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-NF")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-nf")
	expA, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpA", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
	}, "alice")
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, errB := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpB", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
	}, "alice"); errB != nil {
		t.Fatalf("create B: %v", errB)
	}

	dup := "ExpB"
	_, err = svcs.experiment.Update(ctx, expA.ID, UpdateExperimentRequest{Name: &dup}, "alice")
	if err == nil {
		t.Fatal("expected name-taken conflict")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConflictError, got %T: %v", err, err)
	}

	rename := "Whatever"
	_, err = svcs.experiment.Update(ctx, idgen.New(), UpdateExperimentRequest{Name: &rename}, "alice")
	if err == nil {
		t.Fatal("expected not found")
	}
	_ = asNotFoundError(t, err)
}

func TestDatasetService_UpdateNotFound(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	rename := "x"
	_, err := svcs.dataset.Update(ctx, idgen.New(), UpdateDatasetRequest{Name: &rename}, "alice")
	_ = asNotFoundError(t, err)
}

func TestDatasetService_UpdateDuplicateNameConflict(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	a := mustCreateDataset(ctx, t, svcs.dataset, "DS-Dup-A")
	mustCreateDataset(ctx, t, svcs.dataset, "DS-Dup-B")
	dup := "DS-Dup-B"
	_, err := svcs.dataset.Update(ctx, a, UpdateDatasetRequest{Name: &dup}, "alice")
	if err == nil {
		t.Fatal("expected conflict")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConflictError, got %T: %v", err, err)
	}
}

func TestExperimentService_CreateUnknownEvaluatorID(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-Unk")
	_, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "x", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{"00000000-0000-0000-0000-000000000000"},
	}, "alice")
	if err == nil {
		t.Fatal("expected validation error")
	}
	ve := asValidationError(t, err)
	if ve.Field != "evaluator_ids" {
		t.Fatalf("field = %q", ve.Field)
	}
}

func TestTimestamp_RoundTrip(t *testing.T) {
	t.Parallel()
	original := Timestamp(1714600000000)
	out, err := original.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "2024-05-01T") {
		t.Fatalf("marshal output: %s", out)
	}
	var rt Timestamp
	if err := rt.UnmarshalJSON(out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rt != original {
		t.Fatalf("roundtrip drift: %d != %d", rt, original)
	}
	zero := Timestamp(0)
	out, _ = zero.MarshalJSON()
	if string(out) != `""` {
		t.Fatalf("zero marshal: %s", out)
	}
	var z Timestamp
	if err := z.UnmarshalJSON([]byte(`""`)); err != nil {
		t.Fatalf("zero unmarshal: %v", err)
	}
	if z != 0 {
		t.Fatalf("zero rt: %d", z)
	}
	var n Timestamp
	if err := n.UnmarshalJSON([]byte(`1234567890123`)); err != nil {
		t.Fatalf("numeric unmarshal: %v", err)
	}
	if n != Timestamp(1234567890123) {
		t.Fatalf("numeric: %d", n)
	}
}

func TestExperimentService_CreateValidationMatrix(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-VM")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-vm")

	tooLong := strings.Repeat("x", maxExperimentNameLen+1)
	bigInt := 999
	tinyInt := 0
	cases := []struct {
		name  string
		req   CreateExperimentRequest
		field string
	}{
		{"empty name", CreateExperimentRequest{Name: "", DatasetID: dsID, TargetAgent: "T", EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID)}, "name"},
		{"long name", CreateExperimentRequest{Name: tooLong, DatasetID: dsID, TargetAgent: "T", EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID)}, "name"},
		{"empty dataset", CreateExperimentRequest{Name: "x", DatasetID: " ", TargetAgent: "T", EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID)}, "dataset_id"},
		{"empty target", CreateExperimentRequest{Name: "x", DatasetID: dsID, TargetAgent: " ", EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID)}, "target_agent"},
		{"long target", CreateExperimentRequest{Name: "x", DatasetID: dsID, TargetAgent: tooLong, EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID)}, "target_agent"},
		{"too many models", CreateExperimentRequest{Name: "x", DatasetID: dsID, TargetAgent: "T", Models: []map[string]any{{}, {}, {}, {}}, EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID)}, "models"},
		{"runs out of range", CreateExperimentRequest{Name: "x", DatasetID: dsID, TargetAgent: "T", RunsPerExample: &bigInt, EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID)}, "runs_per_example"},
		{"workers below min", CreateExperimentRequest{Name: "x", DatasetID: dsID, TargetAgent: "T", MaxWorkers: &tinyInt, EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID)}, "max_workers"},
		{"too many evaluators", CreateExperimentRequest{Name: "x", DatasetID: dsID, TargetAgent: "T", EvaluatorIDs: slices.Repeat([]string{evID}, maxExperimentEvaluators+1)}, "evaluator_ids"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svcs.experiment.Create(ctx, tc.req, "alice")
			if err == nil {
				t.Fatal("expected validation error")
			}
			ve := asValidationError(t, err)
			if ve.Field != tc.field {
				t.Fatalf("field = %q want %q", ve.Field, tc.field)
			}
		})
	}
}

func TestExperimentService_AcceptsFiveEvaluators(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-FIVE")
	var evIDs []string
	for i := range 6 {
		evIDs = append(evIDs, mustCreateLLMJudge(ctx, t, svcs.evaluator, fmt.Sprintf("ev-five-%d", i)))
	}

	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "five-evaluators", DatasetID: dsID, TargetAgent: "T", EvaluatorIDs: evIDs[:5],
	}, "alice")
	if err != nil {
		t.Fatalf("create with 5 evaluators: %v", err)
	}
	if ids, getErr := svcs.exps.GetEvaluatorIDs(ctx, exp.ID); getErr != nil || len(ids) != 5 {
		t.Fatalf("evaluators after create = %v, %v; want 5", ids, getErr)
	}
	_, err = svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "six-evaluators", DatasetID: dsID, TargetAgent: "T", EvaluatorIDs: evIDs,
	}, "alice")
	if ve := asValidationError(t, err); ve.Field != "evaluator_ids" || !strings.Contains(ve.Message, "at most 5") {
		t.Errorf("create with 6 evaluators: %+v, want evaluator_ids at most 5", ve)
	}

	other, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "updated-evaluators", DatasetID: dsID, TargetAgent: "T", EvaluatorIDs: evIDs[:1],
	}, "alice")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, updErr := svcs.experiment.Update(ctx, other.ID, UpdateExperimentRequest{EvaluatorIDs: evIDs[1:6], EvaluatorIDsSet: true}, "alice"); updErr != nil {
		t.Fatalf("update to 5 evaluators: %v", updErr)
	}
	if ids, getErr := svcs.exps.GetEvaluatorIDs(ctx, other.ID); getErr != nil || len(ids) != 5 {
		t.Fatalf("evaluators after update = %v, %v; want 5", ids, getErr)
	}
	_, err = svcs.experiment.Update(ctx, other.ID, UpdateExperimentRequest{EvaluatorIDs: evIDs, EvaluatorIDsSet: true}, "alice")
	if ve := asValidationError(t, err); ve.Field != "evaluator_ids" || !strings.Contains(ve.Message, "at most 5") {
		t.Errorf("update to 6 evaluators: %+v, want evaluator_ids at most 5", ve)
	}
}

func TestExperimentService_UpdateValidationMatrix(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-UVM")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-uvm")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpUVM", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
	}, "alice")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	tooLong := strings.Repeat("x", maxExperimentNameLen+1)
	bigInt := 999
	tinyInt := 0
	emptyStr := ""
	cases := []struct {
		name  string
		req   UpdateExperimentRequest
		field string
	}{
		{"empty name", UpdateExperimentRequest{Name: &emptyStr}, "name"},
		{"long name", UpdateExperimentRequest{Name: &tooLong}, "name"},
		{"empty target", UpdateExperimentRequest{TargetAgent: &emptyStr}, "target_agent"},
		{"long target", UpdateExperimentRequest{TargetAgent: &tooLong}, "target_agent"},
		{"too many models", UpdateExperimentRequest{ModelsSet: true, Models: []map[string]any{{}, {}, {}, {}}}, "models"},
		{"runs above max", UpdateExperimentRequest{RunsPerExample: &bigInt}, "runs_per_example"},
		{"workers below min", UpdateExperimentRequest{MaxWorkers: &tinyInt}, "max_workers"},
		{"too many evaluators", UpdateExperimentRequest{EvaluatorIDsSet: true, EvaluatorIDs: slices.Repeat([]string{evID}, maxExperimentEvaluators+1)}, "evaluator_ids"},
		{"empty dataset", UpdateExperimentRequest{DatasetID: &emptyStr}, "dataset_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svcs.experiment.Update(ctx, exp.ID, tc.req, "bob")
			if err == nil {
				t.Fatal("expected validation error")
			}
			ve := asValidationError(t, err)
			if ve.Field != tc.field {
				t.Fatalf("field = %q want %q", ve.Field, tc.field)
			}
		})
	}
}

func TestExperimentService_UpdateMissingDataset(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "DS-UMD")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "ev-umd")
	exp, err := svcs.experiment.Create(ctx, CreateExperimentRequest{
		Name: "ExpUMD", DatasetID: dsID, TargetAgent: "T",
		EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
	}, "alice")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	bogus := idgen.New()
	_, err = svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{DatasetID: &bogus}, "bob")
	_ = asNotFoundError(t, err)
}

func TestEvaluatorService_UpdateValidationMatrix(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	id := mustCreateLLMJudge(ctx, t, svcs.evaluator, "uvm")

	tooLong := strings.Repeat("x", maxEvaluatorNameLen+1)
	emptyName := ""
	emptyDesc := ""
	tooLongDesc := strings.Repeat("d", maxEvaluatorDescriptionLen+1)
	cases := []struct {
		name  string
		req   UpdateEvaluatorRequest
		field string
	}{
		{"empty name", UpdateEvaluatorRequest{NameSet: true, Name: &emptyName}, "name"},
		{"long name", UpdateEvaluatorRequest{NameSet: true, Name: &tooLong}, "name"},
		{"empty description", UpdateEvaluatorRequest{DescriptionSet: true, Description: &emptyDesc}, "description"},
		{"long description", UpdateEvaluatorRequest{DescriptionSet: true, Description: &tooLongDesc}, "description"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svcs.evaluator.Update(ctx, id, tc.req, "bob")
			if err == nil {
				t.Fatal("expected validation error")
			}
			ve := asValidationError(t, err)
			if ve.Field != tc.field {
				t.Fatalf("field = %q want %q", ve.Field, tc.field)
			}
		})
	}
}

func TestEvaluatorService_UpdateMissingEvaluator(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	x := "x"
	_, err := svcs.evaluator.Update(ctx, idgen.New(), UpdateEvaluatorRequest{NameSet: true, Name: &x}, "bob")
	_ = asNotFoundError(t, err)
}

func TestEvaluatorService_DescriptionLongOnCreate(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	long := strings.Repeat("d", maxEvaluatorDescriptionLen+1)
	_, err := svcs.evaluator.Create(ctx, CreateEvaluatorRequest{
		Name: "ld", Description: long, Type: "llm_judge",
		Model: new("gpt"), PromptTemplate: new("{{Response}}"),
		ChoiceScores: map[string]any{"Y": 1.0, "N": 0.0},
	}, "alice")
	if err == nil {
		t.Fatal("expected description-too-long error")
	}
	ve := asValidationError(t, err)
	if ve.Field != "description" {
		t.Fatalf("field = %q", ve.Field)
	}
}

func TestEvaluatorService_DuplicateNameConflict(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	mustCreateLLMJudge(ctx, t, svcs.evaluator, "DupEv")
	_, err := svcs.evaluator.Create(ctx, CreateEvaluatorRequest{
		Name: "DupEv", Description: "x", Type: "llm_judge",
		Model: new("gpt"), PromptTemplate: new("{{Response}}"),
		ChoiceScores: map[string]any{"Y": 1.0, "N": 0.0},
	}, "alice")
	if err == nil {
		t.Fatal("expected conflict")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConflictError, got %T: %v", err, err)
	}
}

func TestImportExamples_NotFound(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	if _, err := svcs.dataset.ImportExamples(ctx, idgen.New(), "prompt\nA\n", "text/csv", "alice"); err == nil {
		t.Fatal("expected NotFound for missing dataset")
	}
	if _, err := svcs.dataset.ExportExamplesCSV(ctx, idgen.New()); err == nil {
		t.Fatal("expected NotFound on export csv")
	}
	if _, err := svcs.dataset.ExportExamplesJSON(ctx, idgen.New()); err == nil {
		t.Fatal("expected NotFound on export json")
	}
}

func TestExperimentService_RunAsValidation(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "RunAsDS")
	evID := mustCreateLLMJudge(ctx, t, svcs.evaluator, "runas-ev")

	baseReq := func(name string, runAs *string) CreateExperimentRequest {
		return CreateExperimentRequest{
			Name: name, DatasetID: dsID, TargetAgent: "T",
			EvaluatorIDs: []string{evID}, PrimaryEvaluatorID: new(evID),
			RunAs: runAs,
		}
	}

	t.Run("create_rejects_unresolvable_values", func(t *testing.T) {
		for _, bad := range []string{
			"system:",
			"has space",
			"system:has space",
			"has\ttab",
			"has\rcarriage",
			"has\vvertical",
			"has\fform",
			"has\u00a0nbsp",
			"has\u2028lineSep",
			"default",
			"system:default",
			"channel",
			"system:channel",
			strings.Repeat("a", maxExperimentNameLen+1),
		} {
			_, err := svcs.experiment.Create(ctx, baseReq("bad", new(bad)), "alice")
			ve := asValidationError(t, err)
			if ve.Field != "run_as" {
				t.Errorf("field for %q = %q, want run_as", bad, ve.Field)
			}
		}
	})

	t.Run("create_normalizes_to_the_system_subject", func(t *testing.T) {
		cases := map[string]string{
			"  eval  ":    rbac.EvalSystemUserSub,
			"system:eval": rbac.EvalSystemUserSub,
			"reporting":   "system:reporting",
			"eval":        rbac.EvalSystemUserSub,
		}
		i := 0
		for authored, want := range cases {
			i++
			resp, err := svcs.experiment.Create(ctx, baseReq(fmt.Sprintf("runas-good-%d", i), new(authored)), "alice")
			if err != nil {
				t.Fatalf("create %q: %v", authored, err)
			}
			if resp.RunAs == nil || *resp.RunAs != want {
				t.Errorf("runAs for %q = %v, want %q", authored, resp.RunAs, want)
			}
		}
	})

	t.Run("create_empty_stores_null", func(t *testing.T) {
		resp, err := svcs.experiment.Create(ctx, baseReq("runas-empty", new("   ")), "alice")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if resp.RunAs != nil {
			t.Errorf("runAs = %v, want nil so it resolves to the default eval system user", resp.RunAs)
		}
	})

	t.Run("update", func(t *testing.T) {
		resp, err := svcs.experiment.Create(ctx, baseReq("runas-update", new("eval")), "alice")
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		t.Run("rejects_unresolvable", func(t *testing.T) {
			_, err := svcs.experiment.Update(ctx, resp.ID, UpdateExperimentRequest{
				RunAs: new("system:"), RunAsSet: true,
			}, "bob")
			ve := asValidationError(t, err)
			if ve.Field != "run_as" {
				t.Errorf("field = %q, want run_as", ve.Field)
			}
		})

		t.Run("empty_string_clears", func(t *testing.T) {
			upd, err := svcs.experiment.Update(ctx, resp.ID, UpdateExperimentRequest{
				RunAs: new(""), RunAsSet: true,
			}, "bob")
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if upd.RunAs != nil {
				t.Errorf("runAs = %q, want cleared", *upd.RunAs)
			}
		})

		t.Run("explicit_null_clears", func(t *testing.T) {
			reset, err := svcs.experiment.Update(ctx, resp.ID, UpdateExperimentRequest{
				RunAs: new("reporting"), RunAsSet: true,
			}, "bob")
			if err != nil {
				t.Fatalf("re-set: %v", err)
			}
			if reset.RunAs == nil || *reset.RunAs != "system:reporting" {
				t.Fatalf("runAs = %v, want system:reporting before the explicit-null case", reset.RunAs)
			}
			upd, err := svcs.experiment.Update(ctx, resp.ID, UpdateExperimentRequest{
				RunAs: nil, RunAsSet: true,
			}, "bob")
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if upd.RunAs != nil {
				t.Errorf("runAs = %q, want cleared", *upd.RunAs)
			}
		})

		t.Run("unset_leaves_stored_value", func(t *testing.T) {
			exp, err := svcs.experiment.Create(ctx, baseReq("runas-preserve", new("reporting")), "alice")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			upd, err := svcs.experiment.Update(ctx, exp.ID, UpdateExperimentRequest{
				Description: new("touched"), DescriptionSet: true,
			}, "bob")
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if upd.RunAs == nil || *upd.RunAs != "system:reporting" {
				t.Errorf("runAs = %v after an unrelated update, want preserved system:reporting", upd.RunAs)
			}
		})
	})
}

func TestDatasetService_ImportExamplesStaysLenient(t *testing.T) {
	for name, tc := range map[string]struct {
		body, contentType string
		want              map[string]string
	}{
		"JSON":    {`[{"prompt":" A ","expected_response":"a"},{"prompt":"B","expectedResponse":"b","colour":"x"}]`, "application/json", map[string]string{"A": "a", "B": "b"}},
		"bad CSV": {"prompt,expected_response\nA,a\nbad \"q\nC,c\n", "text/csv", map[string]string{"A": "a"}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, svcs := newEvalSvcs(t)
			dsID := mustCreateDataset(ctx, t, svcs.dataset, "Lenient "+name)
			if _, err := svcs.dataset.ImportExamples(ctx, dsID, tc.body, tc.contentType, "alice"); err != nil {
				t.Fatalf("import: %v", err)
			}
			rows, err := svcs.examples.ListByDataset(ctx, dsID)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, r := range rows {
				got[r.Prompt] = ptrToStr(r.ExpectedResponse)
			}
			if !maps.Equal(got, tc.want) {
				t.Errorf("imported %v, want %v", got, tc.want)
			}
		})
	}
}
