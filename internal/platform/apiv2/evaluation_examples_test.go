// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const examplesPath = evaluationsPath + "/examples"

func examplesIn(datasetID string) string { return datasetsPath + "/" + datasetID + "/examples" }

func newExample(api *entrypointAPI, datasetID string, body map[string]any) Example {
	api.t.Helper()
	return expectCreated[Example](api.t, api.call(http.MethodPost, examplesIn(datasetID), body), "evaluations/examples")
}

func listExamples(t *testing.T, api *entrypointAPI, datasetID string) []Example {
	t.Helper()
	return decodeList[Example](t, api.call(http.MethodGet, examplesIn(datasetID)+"?pageSize=100", nil)).Data
}

func examplePrompts(examples []Example) []string {
	prompts := make([]string, len(examples))
	for i, e := range examples {
		prompts[i] = e.Prompt
	}
	return prompts
}

func (a *entrypointAPI) uploadParts(method, path string, scopes []string, parts ...formPart) *httptest.ResponseRecorder {
	a.t.Helper()
	body, contentType := formBody(a.t, parts...)
	req := httptest.NewRequest(method, platpkg.APIV2Prefix+path, body)
	req.Header.Set("Content-Type", contentType)
	if len(scopes) > 0 {
		req.Header.Set("X-SAM-Scopes", strings.Join(scopes, ","))
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func (a *entrypointAPI) importFile(datasetID, filename, content string) *httptest.ResponseRecorder {
	a.t.Helper()
	return a.uploadParts(http.MethodPut, examplesIn(datasetID)+"/import", nil, formPart{name: "file", filename: filename, content: content})
}

func TestExamples_Lifecycle(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "lifecycle-examples")

	created := newExample(api, dataset.ID, map[string]any{"prompt": "What is 2 + 2?", "expectedResponse": "4"})
	if created.Type != "example" || created.DatasetID != dataset.ID || created.SequenceNumber != 1 ||
		created.Prompt != "What is 2 + 2?" || created.ExpectedResponse == nil || *created.ExpectedResponse != "4" ||
		created.InputArtifact != nil || created.ExpectedArtifact != nil {
		t.Errorf("created = %+v, want the first example of the dataset with its answer and no files", created)
	}
	second := newExample(api, dataset.ID, map[string]any{"prompt": "What is 3 + 3?"})
	if second.SequenceNumber != 2 || second.ExpectedResponse != nil {
		t.Errorf("second = %+v, want sequence number 2 and no expected response", second)
	}

	rec := api.call(http.MethodGet, examplesPath+"/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	expectAbsent(t, raw, "createdAt", "updatedAt", "createdBy", "updatedBy")
	expectNull(t, rawData(t, api.call(http.MethodGet, examplesPath+"/"+second.ID, nil)),
		"expectedResponse", "inputArtifact", "expectedArtifact")

	listed := listExamples(t, api, dataset.ID)
	if !slices.Equal(examplePrompts(listed), []string{"What is 2 + 2?", "What is 3 + 3?"}) {
		t.Errorf("list = %v, want both examples in sequence order", examplePrompts(listed))
	}
	if got := decodeData[Dataset](t, api.call(http.MethodGet, datasetsPath+"/"+dataset.ID, nil)); got.ExampleCount != 2 {
		t.Errorf("dataset exampleCount = %d, want 2", got.ExampleCount)
	}

	expectStatus(t, api.call(http.MethodDelete, examplesPath+"/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, examplesPath+"/"+created.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, examplesPath+"/"+created.ID, nil), http.StatusNotFound)
	if got := listExamples(t, api, dataset.ID); len(got) != 1 || got[0].ID != second.ID {
		t.Errorf("list after delete = %+v, want only the second example", got)
	}
}

func TestExamples_PatchMergesAndNullClears(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "patch-examples")
	created := newExample(api, dataset.ID, map[string]any{"prompt": "What is 2 + 2?", "expectedResponse": "4"})
	path := examplesPath + "/" + created.ID

	rec := api.call(http.MethodPatch, path, map[string]any{"prompt": "What is 5 + 5?"})
	expectStatus(t, rec, http.StatusOK)
	if e := decodeData[Example](t, rec); e.Prompt != "What is 5 + 5?" || e.ExpectedResponse == nil || *e.ExpectedResponse != "4" {
		t.Errorf("after changing the prompt = %+v, want the expected response kept", e)
	}

	rec = api.call(http.MethodPatch, path, map[string]any{"expectedResponse": "10"})
	expectStatus(t, rec, http.StatusOK)
	if e := decodeData[Example](t, rec); e.Prompt != "What is 5 + 5?" || e.ExpectedResponse == nil || *e.ExpectedResponse != "10" {
		t.Errorf("after changing the answer = %+v, want the prompt kept", e)
	}

	rec = api.call(http.MethodPatch, path, map[string]any{"expectedResponse": nil})
	expectStatus(t, rec, http.StatusOK)
	expectNull(t, rawData(t, rec), "expectedResponse")
	if e := decodeData[Example](t, api.call(http.MethodGet, path, nil)); e.Prompt != "What is 5 + 5?" || e.ExpectedResponse != nil {
		t.Errorf("after clearing = %+v, want the answer null and the prompt kept", e)
	}

	before := decodeData[Example](t, api.call(http.MethodGet, path, nil))
	rec = api.call(http.MethodPatch, path, map[string]any{})
	expectStatus(t, rec, http.StatusOK)
	if e := decodeData[Example](t, rec); e.Prompt != before.Prompt || e.SequenceNumber != before.SequenceNumber {
		t.Errorf("an empty patch changed the example: %+v, was %+v", e, before)
	}
}

func TestExamples_PatchValidation(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "patch-validation-examples")
	other := newDataset(api, "patch-validation-other")
	created := newExample(api, dataset.ID, map[string]any{"prompt": "What is 2 + 2?"})
	path := examplesPath + "/" + created.ID

	for name, body := range map[string]map[string]any{
		"null":       {"prompt": nil},
		"empty":      {"prompt": ""},
		"whitespace": {"prompt": "  \n"},
	} {
		t.Run("prompt "+name, func(t *testing.T) {
			expectDetail(t, api.call(http.MethodPatch, path, body), http.StatusUnprocessableEntity, "prompt")
		})
	}

	for field, body := range map[string]map[string]any{
		"sequenceNumber": {"sequenceNumber": 7},
		"datasetId":      {"datasetId": other.ID},
	} {
		t.Run(field+" is fixed", func(t *testing.T) {
			rec := api.call(http.MethodPatch, path, body)
			expectDetail(t, rec, http.StatusUnprocessableEntity, field)
			if got := decodeError(t, rec).ValidationDetails[field]; !slices.Equal(got, []string{"cannot be changed"}) {
				t.Errorf("%s details = %v, want cannot be changed", field, got)
			}
		})
	}

	rec := api.call(http.MethodPatch, path, map[string]any{"prompt": "", "sequenceNumber": 2, "datasetId": other.ID})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"prompt", "sequenceNumber", "datasetId"} {
		if len(details[field]) == 0 {
			t.Errorf("validationDetails has nothing for %s: %v", field, details)
		}
	}
	if got := decodeData[Example](t, api.call(http.MethodGet, path, nil)); got.Prompt != "What is 2 + 2?" || got.DatasetID != dataset.ID || got.SequenceNumber != 1 {
		t.Errorf("a refused patch changed the example: %+v", got)
	}

	for name, body := range map[string]string{
		"truncated JSON": `{"prompt":`,
		"wrong type":     `{"prompt":7}`,
		"unknown field":  `{"prompt":"a","colour":"red"}`,
		"the id":         `{"id":"` + created.ID + `"}`,
		"the type":       `{"type":"example"}`,
		"a time":         `{"createdTime":"2026-01-01T00:00:00.000Z"}`,
		"a file":         `{"inputArtifact":null}`,
	} {
		if rec := api.call(http.MethodPatch, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH with %s = %d, want 400", name, rec.Code)
		}
	}
	expectDetail(t, api.call(http.MethodPatch, path, `{"inputArtifact":null}`), http.StatusBadRequest, "inputArtifact")
}

func TestExamples_CreateValidation(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "create-validation-examples")
	path := examplesIn(dataset.ID)

	for name, body := range map[string]map[string]any{
		"no prompt":         {"expectedResponse": "4"},
		"an empty prompt":   {"prompt": ""},
		"a blank prompt":    {"prompt": "   "},
		"a zero number":     {"prompt": "p", "sequenceNumber": 0},
		"a negative number": {"prompt": "p", "sequenceNumber": -3},
	} {
		t.Run(name, func(t *testing.T) {
			rec := api.call(http.MethodPost, path, body)
			expectStatus(t, rec, http.StatusUnprocessableEntity)
			if len(decodeError(t, rec).ValidationDetails) == 0 {
				t.Errorf("no validationDetails in %s", rec.Body.String())
			}
		})
	}
	expectDetail(t, api.call(http.MethodPost, path, map[string]any{}), http.StatusUnprocessableEntity, "prompt")
	expectDetail(t, api.call(http.MethodPost, path, map[string]any{"prompt": "p", "sequenceNumber": 0}), http.StatusUnprocessableEntity, "sequenceNumber")

	for name, body := range map[string]string{
		"truncated JSON": `{"prompt":`,
		"wrong type":     `{"prompt":7}`,
		"unknown field":  `{"prompt":"a","colour":"red"}`,
		"a dataset id":   `{"prompt":"a","datasetId":"` + dataset.ID + `"}`,
		"a text number":  `{"prompt":"a","sequenceNumber":"1"}`,
	} {
		if rec := api.call(http.MethodPost, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("create with %s = %d, want 400", name, rec.Code)
		}
	}

	placed := newExample(api, dataset.ID, map[string]any{"prompt": "placed", "sequenceNumber": 9})
	if placed.SequenceNumber != 9 {
		t.Errorf("sequenceNumber = %d, want the 9 asked for", placed.SequenceNumber)
	}
	if next := newExample(api, dataset.ID, map[string]any{"prompt": "next"}); next.SequenceNumber != 10 {
		t.Errorf("next sequenceNumber = %d, want 10", next.SequenceNumber)
	}
	rec := api.call(http.MethodPost, path, map[string]any{"prompt": "taken", "sequenceNumber": 9})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	decodeError(t, rec)
}

func TestExamples_CreateBlocksAtTheDatasetLimit(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "limit-examples")
	limit := eval.MaxExamplesPerDataset
	eval.MaxExamplesPerDataset = 2
	t.Cleanup(func() { eval.MaxExamplesPerDataset = limit })

	newExample(api, dataset.ID, map[string]any{"prompt": "one"})
	newExample(api, dataset.ID, map[string]any{"prompt": "two"})
	rec := api.call(http.MethodPost, examplesIn(dataset.ID), map[string]any{"prompt": "three"})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	decodeError(t, rec)
}

func TestExamples_UnknownIDs(t *testing.T) {
	api := newExampleArtifactAPI(t)
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		expectDetail(t, api.call(method, examplesPath+"/not-a-uuid", map[string]any{"prompt": "p"}), http.StatusBadRequest, "id")
		rec := api.call(method, examplesPath+"/"+missingID, map[string]any{"prompt": "p"})
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
	}

	for _, route := range []struct{ method, suffix string }{
		{http.MethodGet, "/examples"},
		{http.MethodPost, "/examples"},
		{http.MethodGet, "/examples/export"},
	} {
		expectDetail(t, api.call(route.method, datasetsPath+"/not-a-uuid"+route.suffix, map[string]any{"prompt": "p"}), http.StatusBadRequest, "datasetId")
		rec := api.call(route.method, datasetsPath+"/"+missingID+route.suffix, map[string]any{"prompt": "p"})
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
	}
	file := formPart{name: "file", filename: "examples.csv", content: "prompt\nhello\n"}
	expectDetail(t, api.uploadParts(http.MethodPut, datasetsPath+"/not-a-uuid/examples/import", nil, file), http.StatusBadRequest, "datasetId")
	expectStatus(t, api.uploadParts(http.MethodPut, examplesIn(missingID)+"/import", nil, file), http.StatusNotFound)

	for _, slot := range []string{"inputArtifact", "expectedArtifact"} {
		base := examplesPath + "/%s/" + slot
		upload := formPart{name: "file", filename: "a.txt", content: "a"}
		expectDetail(t, api.uploadParts(http.MethodPut, fmt.Sprintf(base, "not-a-uuid"), nil, upload), http.StatusBadRequest, "exampleId")
		expectDetail(t, api.call(http.MethodGet, fmt.Sprintf(base, "not-a-uuid")+"/a.txt", nil), http.StatusBadRequest, "exampleId")
		expectDetail(t, api.call(http.MethodDelete, fmt.Sprintf(base, "not-a-uuid"), nil), http.StatusBadRequest, "exampleId")
		expectStatus(t, api.uploadParts(http.MethodPut, fmt.Sprintf(base, missingID), nil, upload), http.StatusNotFound)
		expectStatus(t, api.call(http.MethodGet, fmt.Sprintf(base, missingID)+"/a.txt", nil), http.StatusNotFound)
		expectStatus(t, api.call(http.MethodDelete, fmt.Sprintf(base, missingID), nil), http.StatusNotFound)
	}
}

func TestExamples_PagingFilterAndOrder(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "paging-examples")
	other := newDataset(api, "paging-other")
	newExample(api, other.ID, map[string]any{"prompt": "Refund in another dataset"})
	newExample(api, dataset.ID, map[string]any{"prompt": "Refund policy", "sequenceNumber": 30})
	newExample(api, dataset.ID, map[string]any{"prompt": "Shipping times", "sequenceNumber": 10, "expectedResponse": "Two days; no REFUND needed"})
	newExample(api, dataset.ID, map[string]any{"prompt": "Opening hours", "sequenceNumber": 20})
	newExample(api, dataset.ID, map[string]any{"prompt": "Warranty", "sequenceNumber": 40})
	newExample(api, dataset.ID, map[string]any{"prompt": "Contact", "sequenceNumber": 50})
	path := examplesIn(dataset.ID)

	all := decodeList[Example](t, api.call(http.MethodGet, path, nil))
	if p := all.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != 5 {
		t.Errorf("default pagination = %+v, want page 1 of size %d with count 5", p, defaultPageSize)
	}
	if got := examplePrompts(all.Data); !slices.Equal(got, []string{"Shipping times", "Opening hours", "Refund policy", "Warranty", "Contact"}) {
		t.Errorf("order = %v, want ascending sequenceNumber", got)
	}

	second := decodeList[Example](t, api.call(http.MethodGet, path+"?pageSize=2&pageNumber=2", nil))
	if got := examplePrompts(second.Data); !slices.Equal(got, []string{"Refund policy", "Warranty"}) || second.Meta.Pagination.TotalPages != 3 {
		t.Errorf("page 2 of size 2 = %v, pagination %+v", got, second.Meta.Pagination)
	}
	if beyond := decodeList[Example](t, api.call(http.MethodGet, path+"?pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}

	filtered := decodeList[Example](t, api.call(http.MethodGet, path+"?content=rEfUnD", nil))
	if got := examplePrompts(filtered.Data); !slices.Equal(got, []string{"Shipping times", "Refund policy"}) || filtered.Meta.Pagination.Count != 2 {
		t.Errorf("content=rEfUnD = %v, count %d; want the prompt and the expected response matches, in order", got, filtered.Meta.Pagination.Count)
	}
	if none := decodeList[Example](t, api.call(http.MethodGet, path+"?content=nothing-matches", nil)); len(none.Data) != 0 || none.Meta.Pagination.Count != 0 {
		t.Errorf("a filter matching nothing = %+v", none)
	}

	expectDetail(t, api.call(http.MethodGet, path+"?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, path+"?pageNumber=0", nil), http.StatusUnprocessableEntity, "pageNumber")
	expectStatus(t, api.call(http.MethodGet, path+"?pageSize=ten", nil), http.StatusBadRequest)
}

func TestExamples_Sort(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "sorted-examples")
	var ids []string
	for i, prompt := range []string{"third", "first", "second"} {
		ids = append(ids, newExample(api, dataset.ID, map[string]any{"prompt": prompt, "sequenceNumber": []int{3, 1, 2}[i]}).ID)
		time.Sleep(3 * time.Millisecond)
	}
	path := examplesIn(dataset.ID)
	expectStatus(t, api.call(http.MethodPatch, examplesPath+"/"+ids[1], map[string]any{"expectedResponse": "touched"}), http.StatusOK)

	for sort, want := range map[string][]string{
		"sequenceNumber:desc": {"third", "second", "first"},
		"createdTime:asc":     {"third", "first", "second"},
		"createdTime:desc":    {"second", "first", "third"},
		"updatedTime:desc":    {"first", "second", "third"},
	} {
		if got := examplePrompts(decodeList[Example](t, api.call(http.MethodGet, path+"?sort="+sort, nil)).Data); !slices.Equal(got, want) {
			t.Errorf("sort=%s = %v, want %v", sort, got, want)
		}
	}
	expectDetail(t, api.call(http.MethodGet, path+"?sort=prompt:asc", nil), http.StatusUnprocessableEntity, "sort")
}

func TestExamples_ImportCSV(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "import-csv-examples")
	newExample(api, dataset.ID, map[string]any{"prompt": "existing"})

	csv := "sequence_number,prompt,expected_response,notes\n" +
		"9,\"Say \"\"hi\"\", then\nwave\",Hello,ignored\n" +
		"3,   ,skipped because the prompt is blank,\n" +
		"1,Second,,\n"
	rec := api.importFile(dataset.ID, "examples.CSV", csv)
	expectStatus(t, rec, http.StatusOK)
	result := decodeData[ImportExamplesResult](t, rec)
	if result.Created != 2 || result.Skipped != 1 || result.Dropped != 0 || result.Limit != eval.MaxExamplesPerDataset {
		t.Errorf("result = %+v, want 2 created, 1 skipped and none dropped", result)
	}
	if _, ok := rawData(t, rec)["created"]; !ok {
		t.Errorf("result has no created key: %s", rec.Body.String())
	}

	got := listExamples(t, api, dataset.ID)
	if prompts := examplePrompts(got); !slices.Equal(prompts, []string{"existing", "Say \"hi\", then\nwave", "Second"}) {
		t.Fatalf("prompts = %q, want the imported rows after the existing one", prompts)
	}
	if got[1].ExpectedResponse == nil || *got[1].ExpectedResponse != "Hello" || got[2].ExpectedResponse != nil {
		t.Errorf("expected responses = %v, %v; want Hello and null", got[1].ExpectedResponse, got[2].ExpectedResponse)
	}
	if got[1].SequenceNumber != 2 || got[2].SequenceNumber != 3 {
		t.Errorf("sequence numbers = %d, %d; want the next free numbers, not the file's", got[1].SequenceNumber, got[2].SequenceNumber)
	}
}

func TestExamples_ImportJSON(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "import-json-examples")

	body := `[
		{"sequenceNumber": 7, "prompt": "  First  ", "expectedResponse": "One"},
		{"prompt": "Second", "expectedResponse": null},
		{"prompt": "Third", "expectedResponse": ""},
		{"expectedResponse": "no prompt"},
		{"prompt": "   "}
	]`
	rec := api.importFile(dataset.ID, "examples.json", body)
	expectStatus(t, rec, http.StatusOK)
	if result := decodeData[ImportExamplesResult](t, rec); result.Created != 3 || result.Skipped != 2 || result.Dropped != 0 {
		t.Errorf("result = %+v, want 3 created and 2 skipped", result)
	}
	got := listExamples(t, api, dataset.ID)
	if prompts := examplePrompts(got); !slices.Equal(prompts, []string{"First", "Second", "Third"}) {
		t.Fatalf("prompts = %q", prompts)
	}
	if got[0].ExpectedResponse == nil || *got[0].ExpectedResponse != "One" || got[1].ExpectedResponse != nil || got[2].ExpectedResponse != nil {
		t.Errorf("expected responses = %v %v %v, want One, null, null", got[0].ExpectedResponse, got[1].ExpectedResponse, got[2].ExpectedResponse)
	}

	empty := newDataset(api, "import-empty-array")
	rec = api.importFile(empty.ID, "empty.json", "[]")
	expectStatus(t, rec, http.StatusOK)
	if result := decodeData[ImportExamplesResult](t, rec); result.Created != 0 || result.Skipped != 0 {
		t.Errorf("result = %+v, want nothing imported", result)
	}

	older := newDataset(api, "import-older-export")
	rec = api.importFile(older.ID, "older.json", `[{"sequence_number": 1, "prompt": "Old", "expected_response": "Kept"}]`)
	expectStatus(t, rec, http.StatusOK)
	if got := listExamples(t, api, older.ID); len(got) != 1 || got[0].ExpectedResponse == nil || *got[0].ExpectedResponse != "Kept" {
		t.Errorf("examples = %+v, want an older export's expected_response read", got)
	}
}

func TestExamples_ImportDropsRowsPastTheLimit(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "import-limit-examples")
	limit := eval.MaxExamplesPerDataset
	eval.MaxExamplesPerDataset = 3
	t.Cleanup(func() { eval.MaxExamplesPerDataset = limit })
	newExample(api, dataset.ID, map[string]any{"prompt": "existing"})

	rec := api.importFile(dataset.ID, "examples.csv", "prompt\nb\nc\nd\ne\n")
	expectStatus(t, rec, http.StatusOK)
	if result := decodeData[ImportExamplesResult](t, rec); result.Created != 2 || result.Dropped != 2 || result.Limit != 3 {
		t.Errorf("result = %+v, want 2 created, 2 dropped and a limit of 3", result)
	}
	if got := examplePrompts(listExamples(t, api, dataset.ID)); !slices.Equal(got, []string{"existing", "b", "c"}) {
		t.Errorf("prompts = %v", got)
	}
}

func TestExamples_ImportRefusesWhatItCannotReadWhole(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "import-strict-examples")
	newExample(api, dataset.ID, map[string]any{"prompt": "existing"})

	for name, tc := range map[string]struct {
		filename, content string
		status            int
		detail            string
	}{
		"an unknown JSON key":         {"a.json", `[{"prompt":"ok"},{"prompt":"x","colour":"red"}]`, http.StatusUnprocessableEntity, `row 2 has the unknown field "colour"`},
		"a JSON number prompt":        {"a.json", `[{"prompt":7}]`, http.StatusUnprocessableEntity, "row 1 has a prompt that must be a string"},
		"a text sequence number":      {"a.json", `[{"prompt":"x","sequenceNumber":"1"}]`, http.StatusUnprocessableEntity, "row 1 has a sequenceNumber that must be an integer"},
		"a row that is not an object": {"a.json", `[{"prompt":"x"},"y"]`, http.StatusUnprocessableEntity, "row 2 must be an object"},
		"a null row":                  {"a.json", `[null]`, http.StatusUnprocessableEntity, "row 1 must be an object"},
		"an object, not an array":     {"a.json", `{"prompt":"x"}`, http.StatusUnprocessableEntity, "must be a JSON array"},
		"truncated JSON":              {"a.json", `[{"prompt":"x"}`, http.StatusUnprocessableEntity, "not valid JSON"},
		"an empty JSON file":          {"a.json", ``, http.StatusUnprocessableEntity, "not valid JSON"},
		"a JSON null":                 {"a.json", `null`, http.StatusUnprocessableEntity, "must be a JSON array"},
		"trailing JSON":               {"a.json", `[] []`, http.StatusUnprocessableEntity, "not valid JSON"},
		"a bare quote in a CSV row":   {"a.csv", "prompt\nfine\nbad \"quote\n", http.StatusUnprocessableEntity, "row 2 is not valid CSV"},
		"an unclosed CSV quote":       {"a.csv", "prompt\nfine\n\"never closed\n", http.StatusUnprocessableEntity, "row 2 is not valid CSV"},
		"a CSV without prompt":        {"a.csv", "expected_response\nfoo\n", http.StatusUnprocessableEntity, "prompt"},
		"an empty CSV":                {"a.csv", "", http.StatusUnprocessableEntity, "empty"},
		"a bad CSV header":            {"a.csv", "pro\"mpt\nfoo\n", http.StatusUnprocessableEntity, "header row"},
		"a text file":                 {"a.txt", "prompt\nfoo\n", http.StatusUnprocessableEntity, ".csv or .json"},
		"no extension":                {"examples", "prompt\nfoo\n", http.StatusUnprocessableEntity, ".csv or .json"},
		"a zip":                       {"a.csv.zip", "prompt\nfoo\n", http.StatusUnprocessableEntity, ".csv or .json"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := api.importFile(dataset.ID, tc.filename, tc.content)
			expectDetail(t, rec, tc.status, "file")
			if got := decodeError(t, rec).ValidationDetails["file"]; !strings.Contains(strings.Join(got, " "), tc.detail) {
				t.Errorf("file details = %v, want one containing %q", got, tc.detail)
			}
			if got := examplePrompts(listExamples(t, api, dataset.ID)); !slices.Equal(got, []string{"existing"}) {
				t.Errorf("a refused import left %v in the dataset, want only the existing example", got)
			}
		})
	}
}

func TestExamples_ImportRefusesABadRequest(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "import-request-examples")
	path := examplesIn(dataset.ID) + "/import"

	expectStatus(t, api.call(http.MethodPut, path, `{"file":"prompt\nfoo\n"}`), http.StatusBadRequest)
	expectDetail(t, api.uploadParts(http.MethodPut, path, nil), http.StatusUnprocessableEntity, "file")
	expectDetail(t, api.uploadParts(http.MethodPut, path, nil,
		formPart{name: "file", filename: "a.csv", content: "prompt\nfoo\n"}, formPart{name: "format", content: "csv"}),
		http.StatusBadRequest, "format")
	if got := listExamples(t, api, dataset.ID); len(got) != 0 {
		t.Errorf("refused imports left %d examples", len(got))
	}
}

func TestExamples_ExportCSVIsV1sCSV(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "export-csv-examples")
	newExample(api, dataset.ID, map[string]any{"prompt": "Plain", "expectedResponse": "answer"})
	newExample(api, dataset.ID, map[string]any{"prompt": "Commas, \"quotes\" and\nnewlines"})

	const v1 = "sequence_number,prompt,expected_response\n1,Plain,answer\n2,\"Commas, \"\"quotes\"\" and\nnewlines\",\n"
	for _, query := range []string{"", "?format=csv"} {
		rec := api.call(http.MethodGet, examplesIn(dataset.ID)+"/export"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		if rec.Body.String() != v1 {
			t.Errorf("CSV for %q differs from v1's:\n%s\nv1:\n%s", query, rec.Body.String(), v1)
		}
		if got := rec.Header().Get("Content-Type"); got != "text/csv" {
			t.Errorf("Content-Type = %q, want text/csv", got)
		}
		expectAttachment(t, rec, fmt.Sprintf("dataset-%s-examples.csv", dataset.ID))
	}
}

func TestExamples_ExportJSON(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "export-json-examples")
	newExample(api, dataset.ID, map[string]any{"prompt": "Plain", "expectedResponse": "answer"})
	newExample(api, dataset.ID, map[string]any{"prompt": "No answer"})

	rec := api.call(http.MethodGet, examplesIn(dataset.ID)+"/export?format=json", nil)
	expectStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	expectAttachment(t, rec, fmt.Sprintf("dataset-%s-examples.json", dataset.ID))
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if len(rows) != 2 || rows[0]["prompt"] != "Plain" || rows[0]["expectedResponse"] != "answer" ||
		rows[0]["sequenceNumber"] != float64(1) || rows[1]["expectedResponse"] != nil {
		t.Errorf("rows = %v, want camelCase keys with a null for the missing answer", rows)
	}
	if len(rows[1]) != 3 {
		t.Errorf("row 2 = %v, want sequenceNumber, prompt and a null expectedResponse, and no inputData for a text example", rows[1])
	}
	expectNull(t, rows[1], "expectedResponse")

	empty := newDataset(api, "export-json-empty")
	rec = api.call(http.MethodGet, examplesIn(empty.ID)+"/export?format=json", nil)
	expectStatus(t, rec, http.StatusOK)
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("an empty dataset exports %q, want []", rec.Body.String())
	}
}

func TestExamples_ExportRefusesABadFormat(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "export-format-examples")
	for _, format := range []string{"xml", "CSV", "csv,json"} {
		rec := api.call(http.MethodGet, examplesIn(dataset.ID)+"/export?format="+format, nil)
		expectDetail(t, rec, http.StatusUnprocessableEntity, "format")
		body := decodeError(t, rec)
		if body.Message != msgInvalidQueryParam || !slices.Equal(body.ValidationDetails["format"], []string{"must be csv or json"}) {
			t.Errorf("format=%s error = %+v", format, body)
		}
	}
	expectDetail(t, api.call(http.MethodGet, examplesIn(missingID)+"/export?format=xml", nil), http.StatusUnprocessableEntity, "format")
}

func TestExamples_ExportsImportBackIn(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	source := newDataset(api, "round-trip-source")
	newExample(api, source.ID, map[string]any{"prompt": "Plain", "expectedResponse": "answer"})
	newExample(api, source.ID, map[string]any{"prompt": "Commas, \"quotes\" and\nnewlines", "expectedResponse": "multi\nline"})
	newExample(api, source.ID, map[string]any{"prompt": "No answer", "sequenceNumber": 40})
	want := listExamples(t, api, source.ID)

	for _, format := range []string{"json", "csv"} {
		t.Run(format, func(t *testing.T) {
			exported := api.call(http.MethodGet, examplesIn(source.ID)+"/export?format="+format, nil)
			expectStatus(t, exported, http.StatusOK)
			target := newDataset(api, "round-trip-target-"+format)
			rec := api.importFile(target.ID, "export."+format, exported.Body.String())
			expectStatus(t, rec, http.StatusOK)
			if result := decodeData[ImportExamplesResult](t, rec); result.Created != 3 || result.Skipped != 0 || result.Dropped != 0 {
				t.Fatalf("result = %+v, want all 3 examples created", result)
			}
			got := listExamples(t, api, target.ID)
			if len(got) != len(want) {
				t.Fatalf("imported %d examples, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i].Prompt != want[i].Prompt || fmt.Sprint(deref(got[i].ExpectedResponse)) != fmt.Sprint(deref(want[i].ExpectedResponse)) {
					t.Errorf("example %d = %q / %v, want %q / %v", i+1, got[i].Prompt, deref(got[i].ExpectedResponse), want[i].Prompt, deref(want[i].ExpectedResponse))
				}
			}
		})
	}
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func newExampleArtifactAPI(t *testing.T) *entrypointAPI {
	t.Helper()
	return newEntrypointAPI(t, map[string]any{"artifact_service": map[string]any{"type": "filesystem", "base_path": t.TempDir()}})
}

func TestExamples_Artifacts(t *testing.T) {
	api := newExampleArtifactAPI(t)
	dataset := newDataset(api, "artifact-examples")
	example := newExample(api, dataset.ID, map[string]any{"prompt": "Summarise the file"})

	for _, slot := range []struct {
		segment, filename, content, mimeType string
		field                                string
	}{
		{"inputArtifact", "notes.json", "{\"note\": \"input\"}", "application/json", "inputArtifact"},
		{"expectedArtifact", "answer.md", "# Expected\n", "text/markdown", "expectedArtifact"},
	} {
		t.Run(slot.segment, func(t *testing.T) {
			path := examplesPath + "/" + example.ID + "/" + slot.segment
			rec := api.uploadParts(http.MethodPut, path, nil,
				formPart{name: "file", filename: slot.filename, content: slot.content}, formPart{name: "description", content: "Notes for the test"})
			expectStatus(t, rec, http.StatusOK)
			stored := decodeData[ExampleArtifact](t, rec)
			if stored.Filename != slot.filename || stored.MimeType != slot.mimeType || stored.Size != int64(len(slot.content)) ||
				stored.Version != 0 || len(stored.ContentHash) != 64 {
				t.Errorf("stored = %+v, want %s of %d bytes at version 0", stored, slot.filename, len(slot.content))
			}
			raw := rawData(t, rec)
			for _, key := range []string{"filename", "mimeType", "size", "version", "contentHash"} {
				if _, ok := raw[key]; !ok {
					t.Errorf("artifact has no %s: %s", key, rec.Body.String())
				}
			}

			listed := rawData(t, api.call(http.MethodGet, examplesPath+"/"+example.ID, nil))
			if got, _ := listed[slot.field].(map[string]any); got["filename"] != slot.filename {
				t.Errorf("example %s = %v, want the stored file", slot.field, listed[slot.field])
			}

			again := api.uploadParts(http.MethodPut, path, nil, formPart{name: "file", filename: slot.filename, content: slot.content})
			expectStatus(t, again, http.StatusOK)
			if got := decodeData[ExampleArtifact](t, again); got != stored {
				t.Errorf("uploading the same file again = %+v, want %+v", got, stored)
			}

			for _, query := range []string{"", "?version=0"} {
				got := api.call(http.MethodGet, path+"/"+slot.filename+query, nil)
				expectStatus(t, got, http.StatusOK)
				if got.Body.String() != slot.content {
					t.Errorf("download%s = %q, want %q", query, got.Body.String(), slot.content)
				}
				if !strings.HasPrefix(got.Header().Get("Content-Type"), slot.mimeType) {
					t.Errorf("Content-Type = %q, want %s", got.Header().Get("Content-Type"), slot.mimeType)
				}
				expectAttachment(t, got, slot.filename)
			}
			expectStatus(t, api.call(http.MethodGet, path+"/"+slot.filename+"?version=1", nil), http.StatusNotFound)
			expectStatus(t, api.call(http.MethodGet, path+"/other.txt", nil), http.StatusNotFound)
			expectDetail(t, api.call(http.MethodGet, path+"/"+slot.filename+"?version=latest", nil), http.StatusBadRequest, "version")

			expectStatus(t, api.call(http.MethodDelete, path, nil), http.StatusNoContent)
			expectStatus(t, api.call(http.MethodDelete, path, nil), http.StatusNoContent)
			expectStatus(t, api.call(http.MethodGet, path+"/"+slot.filename, nil), http.StatusNotFound)
			expectNull(t, rawData(t, api.call(http.MethodGet, examplesPath+"/"+example.ID, nil)), slot.field)
		})
	}
}

func TestExamples_ReplacingAFileStartsOver(t *testing.T) {
	api := newExampleArtifactAPI(t)
	dataset := newDataset(api, "replace-artifact-examples")
	example := newExample(api, dataset.ID, map[string]any{"prompt": "p"})
	path := examplesPath + "/" + example.ID + "/inputArtifact"

	first := decodeData[ExampleArtifact](t, api.uploadParts(http.MethodPut, path, nil, formPart{name: "file", filename: "first.txt", content: "one"}))
	second := api.uploadParts(http.MethodPut, path, nil, formPart{name: "file", filename: "second.txt", content: "two"})
	expectStatus(t, second, http.StatusOK)
	if got := decodeData[ExampleArtifact](t, second); got.Filename != "second.txt" || got.ContentHash == first.ContentHash {
		t.Errorf("replacement = %+v, want second.txt with its own hash", got)
	}
	expectStatus(t, api.call(http.MethodGet, path+"/first.txt", nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, path+"/second.txt", nil), http.StatusOK)
}

func TestExamples_ArtifactRefusals(t *testing.T) {
	t.Setenv("EVAL_EXAMPLE_ARTIFACT_MAX_BYTES", "100")
	api := newExampleArtifactAPI(t)
	dataset := newDataset(api, "artifact-refusal-examples")
	example := newExample(api, dataset.ID, map[string]any{"prompt": "p"})
	input := examplesPath + "/" + example.ID + "/inputArtifact"
	expected := examplesPath + "/" + example.ID + "/expectedArtifact"

	expectDetail(t, api.uploadParts(http.MethodPut, input, nil, formPart{name: "file", filename: "big.txt", content: strings.Repeat("x", 101)}),
		http.StatusRequestEntityTooLarge, "file")
	expectStatus(t, api.uploadParts(http.MethodPut, examplesPath+"/"+missingID+"/inputArtifact", nil,
		formPart{name: "file", filename: "big.txt", content: strings.Repeat("x", 101)}), http.StatusNotFound)

	png := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	expectDetail(t, api.uploadParts(http.MethodPut, expected, nil, formPart{name: "file", filename: "picture.png", content: png}), http.StatusUnprocessableEntity, "file")
	expectStatus(t, api.uploadParts(http.MethodPut, input, nil, formPart{name: "file", filename: "picture.png", content: png}), http.StatusOK)

	expectDetail(t, api.uploadParts(http.MethodPut, input, nil, formPart{name: "description", content: "no file"}), http.StatusUnprocessableEntity, "file")
	expectStatus(t, api.call(http.MethodPut, input, `{"file":"x"}`), http.StatusBadRequest)
	expectDetail(t, api.uploadParts(http.MethodPut, input, nil,
		formPart{name: "file", filename: "a.txt", content: "a"}, formPart{name: "colour", content: "red"}), http.StatusBadRequest, "colour")
}

func TestExamples_ArtifactsPastTheDatasetTotalAre422(t *testing.T) {
	t.Setenv("EVAL_DATASET_ARTIFACT_MAX_TOTAL_BYTES", "10")
	api := newExampleArtifactAPI(t)
	dataset := newDataset(api, "artifact-total-examples")
	example := newExample(api, dataset.ID, map[string]any{"prompt": "p"})
	slot := examplesPath + "/" + example.ID + "/"

	expectStatus(t, api.uploadParts(http.MethodPut, slot+"inputArtifact", nil, formPart{name: "file", filename: "a.txt", content: "123456"}), http.StatusOK)
	expectDetail(t, api.uploadParts(http.MethodPut, slot+"expectedArtifact", nil, formPart{name: "file", filename: "b.txt", content: "123456"}),
		http.StatusUnprocessableEntity, "file")
	expectStatus(t, api.call(http.MethodGet, slot+"expectedArtifact/b.txt", nil), http.StatusNotFound)
}

func TestExamples_ArtifactsWithoutAnArtifactServiceAreRefused(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	dataset := newDataset(api, "no-artifact-service-examples")
	example := newExample(api, dataset.ID, map[string]any{"prompt": "p"})
	path := examplesPath + "/" + example.ID + "/inputArtifact"

	rec := api.uploadParts(http.MethodPut, path, nil, formPart{name: "file", filename: "a.txt", content: "a"})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	decodeError(t, rec)
	expectStatus(t, api.call(http.MethodGet, path+"/a.txt", nil), http.StatusUnprocessableEntity)
	expectStatus(t, api.call(http.MethodDelete, path, nil), http.StatusUnprocessableEntity)
}

func TestExamples_Scopes(t *testing.T) {
	api := newExampleArtifactAPI(t)
	dataset := newDataset(api, "scoped-examples")
	example := newExample(api, dataset.ID, map[string]any{"prompt": "p"})
	path := examplesPath + "/" + example.ID
	file := formPart{name: "file", filename: "a.txt", content: "a"}
	reader := []string{rbac.ScopeEvaluationRead}

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, examplesIn(dataset.ID), map[string]any{"prompt": "denied"}},
		{http.MethodPatch, path, map[string]any{"prompt": "denied"}},
		{http.MethodDelete, path, nil},
	} {
		if rec := api.call(call.method, call.path, call.body, rbac.ScopeEvaluationRead); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a reader = %d, want 403", call.method, call.path, rec.Code)
		}
	}
	for _, path := range []string{examplesIn(dataset.ID) + "/import", path + "/inputArtifact", path + "/expectedArtifact"} {
		if rec := api.uploadParts(http.MethodPut, path, reader, file); rec.Code != http.StatusForbidden {
			t.Errorf("PUT %s as a reader = %d, want 403", path, rec.Code)
		}
	}
	for _, slot := range []string{"inputArtifact", "expectedArtifact"} {
		if rec := api.call(http.MethodDelete, path+"/"+slot, nil, rbac.ScopeEvaluationRead); rec.Code != http.StatusForbidden {
			t.Errorf("DELETE %s as a reader = %d, want 403", slot, rec.Code)
		}
	}
	expectStatus(t, api.uploadParts(http.MethodPut, path+"/inputArtifact", []string{rbac.ScopeEvaluationCreate}, file), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, path+"/inputArtifact", nil, rbac.ScopeEvaluationCreate), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodPatch, path, map[string]any{"prompt": "q"}, rbac.ScopeEvaluationUpdate), http.StatusOK)

	for _, read := range []string{
		examplesIn(dataset.ID), examplesIn(dataset.ID) + "/export", path, path + "/inputArtifact/a.txt",
	} {
		expectStatus(t, api.call(http.MethodGet, read, nil, rbac.ScopeEvaluationRead), http.StatusOK)
		expectStatus(t, api.call(http.MethodGet, read, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	}
}

func TestExamples_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, examplesTag)
	want := []string{
		"DELETE /evaluations/examples/{id}", "GET /evaluations/datasets/{datasetId}/examples", "GET /evaluations/examples/{id}",
		"PATCH /evaluations/examples/{id}", "POST /evaluations/datasets/{datasetId}/examples",
	}
	if !slices.Equal(got, want) {
		t.Errorf("API key routes = %v, want %v", got, want)
	}
}

type formPart struct {
	name, filename, content string
}

func formBody(t *testing.T, parts ...formPart) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range parts {
		if p.filename == "" {
			if err := mw.WriteField(p.name, p.content); err != nil {
				t.Fatal(err)
			}
			continue
		}
		w, err := mw.CreateFormFile(p.name, p.filename)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(p.content))
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}
