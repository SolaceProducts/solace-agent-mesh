// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type experimentFixture struct {
	api        *entrypointAPI
	datasetID  string
	evaluators []string
}

func newExperimentFixture(t *testing.T) experimentFixture {
	t.Helper()
	api := newEntrypointAPI(t, nil)
	return experimentFixture{api: api, datasetID: createDataset(api, "experiment-fixture"), evaluators: builtinEvaluatorIDs(api)}
}

func (f experimentFixture) body(name, targetAgent string) map[string]any {
	return map[string]any{
		"name":         name,
		"datasetId":    f.datasetID,
		"targetAgent":  targetAgent,
		"evaluatorIds": []string{f.evaluators[0]},
	}
}

func (f experimentFixture) create(name, targetAgent string) Experiment {
	f.api.t.Helper()
	return expectCreated[Experiment](f.api.t, f.api.call(http.MethodPost, experimentsPath, f.body(name, targetAgent)), "evaluations/experiments")
}

func TestExperiments_Lifecycle(t *testing.T) {
	f := newExperimentFixture(t)
	body := f.body("lifecycle-experiment", "support_agent")
	body["models"] = []map[string]any{{"modelConfigId": missingID, "model": "gpt-4o", "alias": "general"}}
	created := expectCreated[Experiment](t, f.api.call(http.MethodPost, experimentsPath, body), "evaluations/experiments")
	if created.Type != "experiment" || created.RunsPerExample != 1 || len(created.Models) != 1 || *created.Models[0].ModelConfigID != missingID {
		t.Errorf("created = %+v, want type experiment, one run per example, the model as sent", created)
	}

	got := f.api.call(http.MethodGet, experimentsPath+"/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	raw := rawData(t, got)
	expectISOTimes(t, raw, "createdTime", "updatedTime", "datasetUpdatedTime")
	expectAbsent(t, raw, "createdAt", "updatedAt")
	expectNull(t, raw, "description", "primaryEvaluatorId", "runAs", "latestRunStatus", "latestRunTime")
	if raw["datasetName"] != "experiment-fixture" {
		t.Errorf("datasetName = %v, want the dataset's name", raw["datasetName"])
	}

	listed := decodeList[Experiment](t, f.api.call(http.MethodGet, experimentsPath, nil))
	if !slices.ContainsFunc(listed.Data, func(e Experiment) bool { return e.ID == created.ID }) {
		t.Errorf("list is missing the created experiment: %+v", listed.Data)
	}

	patched := f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"name": "renamed-experiment"})
	expectStatus(t, patched, http.StatusOK)
	if e := decodeData[Experiment](t, patched); e.Name != "renamed-experiment" || e.TargetAgent != "support_agent" || len(e.Models) != 1 {
		t.Errorf("after PATCH = %+v, want the new name and the rest kept", e)
	}

	expectStatus(t, f.api.call(http.MethodDelete, experimentsPath+"/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, f.api.call(http.MethodGet, experimentsPath+"/"+created.ID, nil), http.StatusNotFound)
}

func TestExperiments_PatchMergesAndNullClears(t *testing.T) {
	f := newExperimentFixture(t)
	body := f.body("patch-experiment", "support_agent")
	body["description"] = "Experiment the test patches."
	body["evaluatorIds"] = f.evaluators[:2]
	body["primaryEvaluatorId"] = f.evaluators[1]
	body["runAs"] = "nightly-evals"
	body["models"] = []map[string]any{{"modelConfigId": missingID, "model": "gpt-4o", "alias": "general"}}
	created := expectCreated[Experiment](t, f.api.call(http.MethodPost, experimentsPath, body), "evaluations/experiments")
	if created.RunAs == nil || *created.RunAs != "system:nightly-evals" {
		t.Fatalf("runAs = %v, want system:nightly-evals", created.RunAs)
	}

	rec := f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{
		"description": nil, "primaryEvaluatorId": nil, "runAs": nil, "models": nil, "maxWorkers": 7,
	})
	expectStatus(t, rec, http.StatusOK)
	expectNull(t, rawData(t, rec), "description", "primaryEvaluatorId", "runAs")
	got := decodeData[Experiment](t, rec)
	if len(got.Models) != 0 || got.MaxWorkers != 7 || got.Name != "patch-experiment" || !slices.Equal(got.EvaluatorIDs, f.evaluators[:2]) {
		t.Errorf("after PATCH = %+v, want models cleared, maxWorkers 7, name and evaluators kept", got)
	}
}

func TestExperiments_Validation(t *testing.T) {
	f := newExperimentFixture(t)
	created := f.create("validation-experiment", "support_agent")
	path := experimentsPath + "/" + created.ID

	for name, body := range map[string]string{
		"truncated JSON": `{"name":`,
		"unknown field":  `{"name":"a","colour":"red"}`,
		"wrong type":     `{"name":7}`,
	} {
		if rec := f.api.call(http.MethodPost, experimentsPath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("create with %s = %d, want 400", name, rec.Code)
		}
	}
	expectDetail(t, f.api.call(http.MethodPatch, path, `{"maxWorkers":"many"}`), http.StatusBadRequest, "maxWorkers")
	for name, body := range map[string]string{
		"truncated JSON": `{"name":`,
		"unknown field":  `{"bogus":1}`,
		"the id":         `{"id":"` + created.ID + `"}`,
	} {
		if rec := f.api.call(http.MethodPatch, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH with %s = %d, want 400", name, rec.Code)
		}
	}

	rec := f.api.call(http.MethodPost, experimentsPath, map[string]any{"runsPerExample": 11})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"name", "datasetId", "targetAgent", "evaluatorIds", "runsPerExample"} {
		if len(details[field]) == 0 {
			t.Errorf("create validationDetails has nothing for %s: %v", field, details)
		}
	}

	rec = f.api.call(http.MethodPatch, path, map[string]any{
		"name": nil, "datasetId": "", "targetAgent": "", "runsPerExample": nil, "evaluatorIds": []string{}, "primaryEvaluatorId": "x",
	})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details = decodeError(t, rec).ValidationDetails
	for _, field := range []string{"name", "datasetId", "targetAgent", "runsPerExample", "evaluatorIds", "primaryEvaluatorId"} {
		if len(details[field]) == 0 {
			t.Errorf("PATCH validationDetails has nothing for %s: %v", field, details)
		}
	}
}

func TestExperiments_EvaluatorCap(t *testing.T) {
	f := newExperimentFixture(t)
	if len(f.evaluators) < 6 {
		t.Fatalf("fixture has %d builtin evaluators, want at least 6", len(f.evaluators))
	}
	five, six := f.evaluators[:5], f.evaluators[:6]

	body := f.body("five-evaluators", "support_agent")
	body["evaluatorIds"] = five
	created := expectCreated[Experiment](t, f.api.call(http.MethodPost, experimentsPath, body), "evaluations/experiments")
	if len(created.EvaluatorIDs) != 5 {
		t.Errorf("created with %d evaluators, want 5", len(created.EvaluatorIDs))
	}
	body = f.body("six-evaluators", "support_agent")
	body["evaluatorIds"] = six
	expectDetail(t, f.api.call(http.MethodPost, experimentsPath, body), http.StatusUnprocessableEntity, "evaluatorIds")

	other := f.create("patched-evaluators", "support_agent")
	expectStatus(t, f.api.call(http.MethodPatch, experimentsPath+"/"+other.ID, map[string]any{"evaluatorIds": five}), http.StatusOK)
	expectDetail(t, f.api.call(http.MethodPatch, experimentsPath+"/"+other.ID, map[string]any{"evaluatorIds": six}),
		http.StatusUnprocessableEntity, "evaluatorIds")
}

func TestExperiments_ServiceRejectionsNameTheV2Field(t *testing.T) {
	f := newExperimentFixture(t)
	created := f.create("rejection-experiment", "support_agent")

	missingDataset := f.body("missing-dataset", "support_agent")
	missingDataset["datasetId"] = missingID
	expectDetail(t, f.api.call(http.MethodPost, experimentsPath, missingDataset), http.StatusUnprocessableEntity, "datasetId")
	expectDetail(t, f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"datasetId": missingID}), http.StatusUnprocessableEntity, "datasetId")

	missingEvaluator := f.body("missing-evaluator", "support_agent")
	missingEvaluator["evaluatorIds"] = []string{missingID}
	expectDetail(t, f.api.call(http.MethodPost, experimentsPath, missingEvaluator), http.StatusUnprocessableEntity, "evaluatorIds")

	expectDetail(t, f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"primaryEvaluatorId": f.evaluators[1]}),
		http.StatusUnprocessableEntity, "primaryEvaluatorId")
	expectDetail(t, f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"runAs": "default"}),
		http.StatusUnprocessableEntity, "runAs")
}

func TestExperiments_UnknownIDs(t *testing.T) {
	f := newExperimentFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		expectDetail(t, f.api.call(method, experimentsPath+"/not-a-uuid", map[string]any{}), http.StatusBadRequest, "id")
		rec := f.api.call(method, experimentsPath+"/"+missingID, map[string]any{"name": "missing"})
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
	}
}

func TestExperiments_NameTakenIs409(t *testing.T) {
	f := newExperimentFixture(t)
	f.create("taken-experiment", "support_agent")
	other := f.create("other-experiment", "support_agent")

	rec := f.api.call(http.MethodPost, experimentsPath, f.body("taken-experiment", "support_agent"))
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)
	rec = f.api.call(http.MethodPatch, experimentsPath+"/"+other.ID, map[string]any{"name": "taken-experiment"})
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)
}

func TestExperiments_PagingFiltersAndSort(t *testing.T) {
	f := newExperimentFixture(t)
	for _, name := range []string{"bravo", "alpha", "charlie"} {
		f.create(name, "paging_agent")
		time.Sleep(2 * time.Millisecond)
	}
	f.create("other-agent", "other_agent")
	const paging = experimentsPath + "?targetAgent=paging_agent"

	defaults := decodeList[Experiment](t, f.api.call(http.MethodGet, paging, nil))
	if p := defaults.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != 3 {
		t.Errorf("default pagination = %+v, want page 1 of size %d with count 3", p, defaultPageSize)
	}
	if names := experimentNames(defaults.Data); !slices.Equal(names, []string{"charlie", "alpha", "bravo"}) {
		t.Errorf("default order = %v, want newest first", names)
	}
	sorted := decodeList[Experiment](t, f.api.call(http.MethodGet, paging+"&sort=name:asc&pageSize=2&pageNumber=2", nil))
	if names := experimentNames(sorted.Data); !slices.Equal(names, []string{"charlie"}) || sorted.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 2 of size 2 by name = %v, pagination %+v", names, sorted.Meta.Pagination)
	}
	if beyond := decodeList[Experiment](t, f.api.call(http.MethodGet, paging+"&pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}
	byDataset := decodeList[Experiment](t, f.api.call(http.MethodGet, experimentsPath+"?datasetId="+f.datasetID+"&evaluatorId="+f.evaluators[0], nil))
	if byDataset.Meta.Pagination.Count != 4 {
		t.Errorf("filtered by dataset and evaluator = %d, want the fixture's 4", byDataset.Meta.Pagination.Count)
	}

	expectDetail(t, f.api.call(http.MethodGet, experimentsPath+"?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectStatus(t, f.api.call(http.MethodGet, experimentsPath+"?pageNumber=ten", nil), http.StatusBadRequest)
	expectDetail(t, f.api.call(http.MethodGet, experimentsPath+"?sort=targetAgent:asc", nil), http.StatusUnprocessableEntity, "sort")
	expectDetail(t, f.api.call(http.MethodGet, experimentsPath+"?datasetId=abc", nil), http.StatusBadRequest, "datasetId")
	expectDetail(t, f.api.call(http.MethodGet, experimentsPath+"?evaluatorId=abc", nil), http.StatusBadRequest, "evaluatorId")
}

func experimentNames(experiments []Experiment) []string {
	names := make([]string, len(experiments))
	for i, e := range experiments {
		names[i] = e.Name
	}
	return names
}

func TestExperiments_Sample(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.api.call(http.MethodGet, evaluationsPath+"/sampleExperiment", nil)
	expectStatus(t, rec, http.StatusOK)
	sample := decodeData[Experiment](t, rec)
	if !sample.IsDefault || len(sample.Models) == 0 || sample.Models[0].ModelConfigID != nil || sample.Models[0].Alias == "" {
		t.Errorf("sample = %+v, want the seeded default with a model named by alias alone", sample)
	}

	expectStatus(t, f.api.call(http.MethodDelete, experimentsPath+"/"+sample.ID, nil), http.StatusNoContent)
	rec = f.api.call(http.MethodGet, evaluationsPath+"/sampleExperiment", nil)
	expectStatus(t, rec, http.StatusNotFound)
	decodeError(t, rec)
}

func TestExperiments_DeleteWithActiveRunIs422(t *testing.T) {
	f := newExperimentFixture(t)
	created := f.create("active-experiment", "support_agent")
	f.trigger(created.ID, map[string]any{})

	rec := f.api.call(http.MethodDelete, experimentsPath+"/"+created.ID, nil)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	decodeError(t, rec)
	expectStatus(t, f.api.call(http.MethodGet, experimentsPath+"/"+created.ID, nil), http.StatusOK)
}

func TestExperiments_Scopes(t *testing.T) {
	f := newExperimentFixture(t)
	created := f.create("scoped-experiment", "support_agent")
	path := experimentsPath + "/" + created.ID

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, experimentsPath, f.body("denied", "support_agent")},
		{http.MethodPatch, path, map[string]any{"name": "denied"}},
		{http.MethodDelete, path, nil},
	} {
		if rec := f.api.call(call.method, call.path, call.body, rbac.ScopeEvaluationRead); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a reader = %d, want 403", call.method, call.path, rec.Code)
		}
	}
	expectStatus(t, f.api.call(http.MethodGet, experimentsPath, nil, rbac.ScopeEvaluationRead), http.StatusOK)
	expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
}

func TestExperiments_ModelsAreValidated(t *testing.T) {
	f := newExperimentFixture(t)
	created := f.create("models-validation", "support_agent")
	for name, tc := range map[string]struct {
		model map[string]any
		want  string
	}{
		"modelConfigId not a UUID": {map[string]any{"modelConfigId": "not-a-uuid", "model": "gpt-4o"}, "modelConfigId must be a UUID"},
		"no way to name the model": {map[string]any{}, "needs a modelConfigId, a model or an alias"},
	} {
		t.Run(name, func(t *testing.T) {
			bad := []map[string]any{tc.model}
			body := f.body("models-validation-2", "support_agent")
			body["models"] = bad
			expectDetailSaying(t, f.api.call(http.MethodPost, experimentsPath, body), "models", tc.want)
			expectDetailSaying(t, f.api.call(http.MethodPatch, experimentsPath+"/"+created.ID, map[string]any{"models": bad}), "models", tc.want)
		})
	}
}

func TestExperiments_ModelNamedByModelConfigIDAlone(t *testing.T) {
	f := newExperimentFixture(t)
	body := f.body("model-config-only", "support_agent")
	body["models"] = []map[string]any{{"modelConfigId": missingID}}
	expectStatus(t, f.api.call(http.MethodPost, experimentsPath, body), http.StatusCreated)
}
