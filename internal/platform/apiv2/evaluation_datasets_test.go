// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newDataset(api *entrypointAPI, name string) Dataset {
	api.t.Helper()
	return expectCreated[Dataset](api.t, api.call(http.MethodPost, datasetsPath, map[string]any{"name": name}), "evaluations/datasets")
}

func TestDatasets_Lifecycle(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := expectCreated[Dataset](t, api.call(http.MethodPost, datasetsPath, map[string]any{
		"name": "lifecycle-dataset", "description": "Dataset the test creates.",
	}), "evaluations/datasets")
	if created.Type != "dataset" || created.ExampleCount != 0 || created.IsDefault || created.CreatedBy == "" {
		t.Errorf("created = %+v, want an empty user dataset", created)
	}

	got := api.call(http.MethodGet, datasetsPath+"/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	raw := rawData(t, got)
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	expectAbsent(t, raw, "createdAt", "updatedAt")

	listed := decodeList[Dataset](t, api.call(http.MethodGet, datasetsPath, nil))
	if !slices.ContainsFunc(listed.Data, func(d Dataset) bool { return d.ID == created.ID }) {
		t.Errorf("list is missing the created dataset: %+v", listed.Data)
	}

	expectStatus(t, api.call(http.MethodDelete, datasetsPath+"/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, datasetsPath+"/"+created.ID, nil), http.StatusNotFound)
}

func TestDatasets_InputDataExampleCount(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	created := newDataset(api, "input-count-dataset")
	path := datasetsPath + "/" + created.ID
	empty := api.call(http.MethodGet, path, nil)
	expectStatus(t, empty, http.StatusOK)
	if raw := rawData(t, empty); raw["inputDataExampleCount"] != float64(0) {
		t.Errorf("inputDataExampleCount of an empty dataset = %v, want 0", raw["inputDataExampleCount"])
	}

	newExample(api, created.ID, map[string]any{"prompt": "What is 2 + 2?"})
	newExample(api, created.ID, map[string]any{"inputData": map[string]any{"orderId": "O-1"}})
	newExample(api, created.ID, map[string]any{"prompt": "Ship it", "inputData": map[string]any{"orderId": "O-2"}})

	if got := decodeData[Dataset](t, api.call(http.MethodGet, path, nil)); got.ExampleCount != 3 || got.InputDataExampleCount != 2 {
		t.Errorf("dataset = %d examples, %d with input data; want 3 and 2", got.ExampleCount, got.InputDataExampleCount)
	}
	listed := decodeList[Dataset](t, api.call(http.MethodGet, datasetsPath, nil))
	i := slices.IndexFunc(listed.Data, func(d Dataset) bool { return d.ID == created.ID })
	if i < 0 || listed.Data[i].InputDataExampleCount != 2 {
		t.Errorf("listed datasets = %+v, want this one with 2 examples carrying input data", listed.Data)
	}
}

func TestDatasets_PatchMergesAndNullClears(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := expectCreated[Dataset](t, api.call(http.MethodPost, datasetsPath, map[string]any{
		"name": "patch-dataset", "description": "Dataset the test patches.",
	}), "evaluations/datasets")
	path := datasetsPath + "/" + created.ID

	rec := api.call(http.MethodPatch, path, map[string]any{"name": "renamed-dataset"})
	expectStatus(t, rec, http.StatusOK)
	if d := decodeData[Dataset](t, rec); d.Name != "renamed-dataset" || d.Description == nil || *d.Description != "Dataset the test patches." {
		t.Errorf("after renaming = %+v, want the new name and the description kept", d)
	}

	rec = api.call(http.MethodPatch, path, map[string]any{"description": nil})
	expectStatus(t, rec, http.StatusOK)
	expectNull(t, rawData(t, rec), "description")
	if d := decodeData[Dataset](t, api.call(http.MethodGet, path, nil)); d.Name != "renamed-dataset" || d.Description != nil {
		t.Errorf("after clearing = %+v, want the description null and the name kept", d)
	}
}

func TestDatasets_Validation(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := newDataset(api, "validation-dataset")
	path := datasetsPath + "/" + created.ID

	for name, body := range map[string]string{
		"truncated JSON": `{"name":`,
		"unknown field":  `{"name":"a","colour":"red"}`,
		"wrong type":     `{"name":7}`,
	} {
		if rec := api.call(http.MethodPost, datasetsPath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("create with %s = %d, want 400", name, rec.Code)
		}
		if rec := api.call(http.MethodPatch, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH with %s = %d, want 400", name, rec.Code)
		}
	}
	expectDetail(t, api.call(http.MethodPatch, path, `{"id":"`+created.ID+`"}`), http.StatusBadRequest, "id")

	rec := api.call(http.MethodPost, datasetsPath, map[string]any{"description": strings.Repeat("d", 1001)})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"name", "description"} {
		if len(details[field]) == 0 {
			t.Errorf("create validationDetails has nothing for %s: %v", field, details)
		}
	}
	expectDetail(t, api.call(http.MethodPost, datasetsPath, map[string]any{"name": strings.Repeat("n", 256)}), http.StatusUnprocessableEntity, "name")

	for _, body := range []map[string]any{{"name": nil}, {"name": ""}} {
		expectDetail(t, api.call(http.MethodPatch, path, body), http.StatusUnprocessableEntity, "name")
	}
	expectDetail(t, api.call(http.MethodPatch, path, map[string]any{"description": strings.Repeat("d", 1001)}), http.StatusUnprocessableEntity, "description")
	rec = api.call(http.MethodPatch, path, map[string]any{"name": "", "description": strings.Repeat("d", 1001)})
	expectDetail(t, rec, http.StatusUnprocessableEntity, "description")
	if details := decodeError(t, rec).ValidationDetails; len(details["name"]) == 0 {
		t.Errorf("validationDetails = %v, want name and description", details)
	}
}

func TestDatasets_UnknownIDs(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		expectDetail(t, api.call(method, datasetsPath+"/not-a-uuid", map[string]any{}), http.StatusBadRequest, "id")
		rec := api.call(method, datasetsPath+"/"+missingID, map[string]any{"name": "missing"})
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
	}
}

func TestDatasets_NameTakenIs409(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	newDataset(api, "taken-dataset")
	other := newDataset(api, "other-dataset")

	rec := api.call(http.MethodPost, datasetsPath, map[string]any{"name": "Taken-Dataset"})
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)
	rec = api.call(http.MethodPatch, datasetsPath+"/"+other.ID, map[string]any{"name": "taken-dataset"})
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)
}

func TestDatasets_PagingFilterAndSort(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	for _, name := range []string{"paging-bravo", "paging-alpha", "paging-charlie"} {
		newDataset(api, name)
	}
	createDataset(api, "paging-delta")
	const paging = datasetsPath + "?name=PAGING-"

	defaults := decodeList[Dataset](t, api.call(http.MethodGet, paging, nil))
	if p := defaults.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != 4 {
		t.Errorf("default pagination = %+v, want page 1 of size %d with count 4", p, defaultPageSize)
	}
	if !slices.IsSortedFunc(defaults.Data, func(a, b Dataset) int { return cmp.Compare(b.CreatedTime, a.CreatedTime) }) {
		t.Errorf("default order = %v, want newest first", defaults.Data)
	}
	sorted := decodeList[Dataset](t, api.call(http.MethodGet, paging+"&sort=name:asc&pageSize=3&pageNumber=2", nil))
	if names := datasetNames(sorted.Data); !slices.Equal(names, []string{"paging-delta"}) || sorted.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 2 of size 3 by name = %v, pagination %+v", names, sorted.Meta.Pagination)
	}
	byExamples := decodeList[Dataset](t, api.call(http.MethodGet, paging+"&sort=exampleCount:desc&pageSize=1", nil))
	if names := datasetNames(byExamples.Data); !slices.Equal(names, []string{"paging-delta"}) {
		t.Errorf("most examples first = %v, want the dataset with an example", names)
	}
	if beyond := decodeList[Dataset](t, api.call(http.MethodGet, paging+"&pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}

	expectDetail(t, api.call(http.MethodGet, datasetsPath+"?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, datasetsPath+"?pageSize=0", nil), http.StatusUnprocessableEntity, "pageSize")
	expectStatus(t, api.call(http.MethodGet, datasetsPath+"?pageNumber=ten", nil), http.StatusBadRequest)
	expectDetail(t, api.call(http.MethodGet, datasetsPath+"?sort=description:asc", nil), http.StatusUnprocessableEntity, "sort")
}

func datasetNames(datasets []Dataset) []string {
	names := make([]string, len(datasets))
	for i, d := range datasets {
		names[i] = d.Name
	}
	return names
}

func TestDatasets_DeleteWithActiveRunIs422(t *testing.T) {
	f := newExperimentFixture(t)
	created := f.create("active-dataset-experiment", "support_agent")
	f.trigger(created.ID, map[string]any{})

	rec := f.api.call(http.MethodDelete, datasetsPath+"/"+f.datasetID, nil)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	decodeError(t, rec)
	expectStatus(t, f.api.call(http.MethodGet, datasetsPath+"/"+f.datasetID, nil), http.StatusOK)
}

func TestDatasets_Scopes(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := newDataset(api, "scoped-dataset")
	path := datasetsPath + "/" + created.ID

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, datasetsPath, map[string]any{"name": "denied"}},
		{http.MethodPatch, path, map[string]any{"name": "denied"}},
		{http.MethodDelete, path, nil},
		{http.MethodPut, datasetsPath + "/generate", map[string]any{"targetAgent": "support_agent", "numExamples": 1}},
		{http.MethodPut, path + "/examples/generate", map[string]any{"targetAgent": "support_agent", "numExamples": 1}},
	} {
		if rec := api.call(call.method, call.path, call.body, rbac.ScopeEvaluationRead); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a reader = %d, want 403", call.method, call.path, rec.Code)
		}
	}
	expectStatus(t, api.call(http.MethodGet, datasetsPath, nil, rbac.ScopeEvaluationRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, path+"/operations", nil, rbac.ScopeEvaluationRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
}

func TestDatasets_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, datasetsTag)
	want := []string{
		"DELETE /evaluations/datasets/{id}", "GET /evaluations/datasets", "GET /evaluations/datasets/{id}",
		"PATCH /evaluations/datasets/{id}", "POST /evaluations/datasets",
	}
	if !slices.Equal(got, want) {
		t.Errorf("API key routes = %v, want %v", got, want)
	}
}
