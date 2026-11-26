// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

const (
	exactInput       = `{"orderId": 9007199254740993, "total": 1.0, "big": 12345678901234567890}`
	exactInputStored = `{"big":12345678901234567890,"orderId":9007199254740993,"total":1.0}`
)

func newWorkflowEvalsAPI(t *testing.T, on bool) *entrypointAPI {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyWorkflowEvals), strconv.FormatBool(on))
	return newEntrypointAPI(t, nil)
}

func sentInputData(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	raw, sent := body.Data["inputData"]
	if !sent {
		t.Fatalf("no inputData key in %s", rec.Body.String())
	}
	canonical, err := eval.CanonicalInputData(raw)
	if err != nil {
		t.Fatalf("inputData %s is not an object: %v", raw, err)
	}
	if canonical == nil {
		return "null"
	}
	return string(canonical)
}

func TestExamples_InputData(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	dataset := newDataset(api, "input-examples")

	rec := api.call(http.MethodPost, examplesIn(dataset.ID), `{"inputData":`+exactInput+`}`)
	created := expectCreated[Example](t, rec, "evaluations/examples")
	if created.Prompt != "" || sentInputData(t, rec) != exactInputStored {
		t.Fatalf("created = prompt %q inputData %s, want no prompt and %s", created.Prompt, created.InputData, exactInputStored)
	}
	path := examplesPath + "/" + created.ID
	if got := sentInputData(t, api.call(http.MethodGet, path, nil)); got != exactInputStored {
		t.Errorf("GET inputData = %s, want %s", got, exactInputStored)
	}

	steps := []struct {
		name string
		body string
		want string
	}{
		{"an empty prompt is kept with an input", `{"prompt":""}`, exactInputStored},
		{"absent keeps it", `{"prompt":"Handle the order"}`, exactInputStored},
		{"null clears it", `{"inputData":null}`, "null"},
		{"an object sets it", `{"inputData":{"orderId":"O-2"}}`, `{"orderId":"O-2"}`},
		{"an empty object sets it", `{"inputData":{}}`, `{}`},
	}
	for _, step := range steps {
		rec = api.call(http.MethodPatch, path, step.body)
		expectStatus(t, rec, http.StatusOK)
		if got := sentInputData(t, rec); got != step.want {
			t.Errorf("PATCH %s: inputData = %s, want %s", step.name, got, step.want)
		}
		if got := sentInputData(t, api.call(http.MethodGet, path, nil)); got != step.want {
			t.Errorf("GET after PATCH %s: inputData = %s, want %s", step.name, got, step.want)
		}
	}

	inputOnly := newExample(api, dataset.ID, map[string]any{"inputData": map[string]any{"orderId": "O-3"}})
	inputOnlyPath := examplesPath + "/" + inputOnly.ID
	expectDetail(t, api.call(http.MethodPatch, inputOnlyPath, `{"inputData":null}`), http.StatusUnprocessableEntity, "inputData")
	expectDetail(t, api.call(http.MethodPatch, inputOnlyPath, `{"prompt":"","inputData":null}`), http.StatusUnprocessableEntity, "prompt")
	if got := sentInputData(t, api.call(http.MethodGet, inputOnlyPath, nil)); got != `{"orderId":"O-3"}` {
		t.Errorf("a refused clear changed inputData to %s", got)
	}
}

func TestExamples_InputDataValidation(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	dataset := newDataset(api, "input-validation-examples")
	path := examplesIn(dataset.ID)
	agentExample := newExample(api, dataset.ID, map[string]any{"prompt": "What is 2 + 2?"})
	examplePath := examplesPath + "/" + agentExample.ID

	for name, value := range map[string]string{"an array": `[1]`, "a string": `"O-1"`, "a number": `7`} {
		t.Run(name, func(t *testing.T) {
			expectDetail(t, api.call(http.MethodPost, path, `{"prompt":"p","inputData":`+value+`}`), http.StatusBadRequest, "inputData")
			expectDetail(t, api.call(http.MethodPatch, examplePath, `{"inputData":`+value+`}`), http.StatusBadRequest, "inputData")
		})
	}
	expectDetail(t, api.call(http.MethodPost, path, `{"inputData":null}`), http.StatusUnprocessableEntity, "prompt")
	expectDetail(t, api.call(http.MethodPost, path, `{"prompt":"  ","inputData":null}`), http.StatusUnprocessableEntity, "prompt")

	atLimit := `{"b":"` + strings.Repeat("x", 256*1024-len(`{"b":""}`)) + `"}`
	overLimit := `{"b":"` + strings.Repeat("x", 256*1024+1-len(`{"b":""}`)) + `"}`
	expectDetailSaying(t, api.call(http.MethodPost, path, `{"inputData":`+overLimit+`}`), "inputData", "262144")
	expectDetailSaying(t, api.call(http.MethodPatch, examplePath, `{"inputData":`+overLimit+`}`), "inputData", "262144")
	if got := listExamples(t, api, dataset.ID); len(got) != 1 || got[0].InputData != nil {
		t.Errorf("refused inputs left examples %+v, want only the agent example without input", got)
	}
	expectStatus(t, api.call(http.MethodPost, path, `{"inputData":`+atLimit+`}`), http.StatusCreated)
	expectStatus(t, api.call(http.MethodPatch, examplePath, `{"inputData":`+atLimit+`}`), http.StatusOK)
}

func TestExamples_InputDataRefusedWithTheFlagOff(t *testing.T) {
	api := newWorkflowEvalsAPI(t, false)
	dataset := newDataset(api, "input-flag-off-examples")
	agentExample := newExample(api, dataset.ID, map[string]any{"prompt": "What is 2 + 2?"})
	examplePath := examplesPath + "/" + agentExample.ID
	const flag = "SAM_FEATURE_WORKFLOW_EVALS"

	expectDetailSaying(t, api.call(http.MethodPost, examplesIn(dataset.ID), `{"prompt":"p","inputData":{"orderId":"O-1"}}`), "inputData", flag)
	expectDetailSaying(t, api.call(http.MethodPatch, examplePath, `{"inputData":{"orderId":"O-1"}}`), "inputData", flag)
	expectDetailSaying(t, api.importFile(dataset.ID, "input.csv", "prompt,input_data\np,\"{\"\"orderId\"\":\"\"O-1\"\"}\"\n"), "inputData", flag)
	expectDetailSaying(t, api.importFile(dataset.ID, "input.json", `[{"prompt":"p","inputData":{"orderId":"O-1"}}]`), "inputData", flag)
	if got := listExamples(t, api, dataset.ID); len(got) != 1 {
		t.Errorf("refused writes left %d examples, want only the agent example", len(got))
	}

	rec := api.call(http.MethodPatch, examplePath, `{"inputData":null}`)
	expectStatus(t, rec, http.StatusOK)
	expectNull(t, rawData(t, rec), "inputData")
}

func TestExamples_AgentExamplesCarryANullInputData(t *testing.T) {
	api := newWorkflowEvalsAPI(t, false)
	dataset := newDataset(api, "agent-input-examples")

	rec := api.call(http.MethodPost, examplesIn(dataset.ID), map[string]any{"prompt": "What is 2 + 2?", "expectedResponse": "4"})
	created := expectCreated[Example](t, rec, "evaluations/examples")
	expectNull(t, rawData(t, rec), "inputData")
	expectNull(t, rawData(t, api.call(http.MethodGet, examplesPath+"/"+created.ID, nil)), "inputData")

	rec = api.call(http.MethodPatch, examplesPath+"/"+created.ID, map[string]any{"prompt": "What is 3 + 3?"})
	expectStatus(t, rec, http.StatusOK)
	if raw := rawData(t, rec); raw["prompt"] != "What is 3 + 3?" || raw["expectedResponse"] != "4" {
		t.Errorf("after a prompt change = %v", raw)
	}
	expectNull(t, rawData(t, rec), "inputData")
}

func TestExamples_InputDataImportsAndExports(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	source := newDataset(api, "input-export-source")
	newExample(api, source.ID, map[string]any{"prompt": "Plain"})
	expectStatus(t, api.call(http.MethodPost, examplesIn(source.ID), `{"inputData":`+exactInput+`}`), http.StatusCreated)
	expectStatus(t, api.call(http.MethodPost, examplesIn(source.ID), `{"prompt":"every input optional","inputData":{}}`), http.StatusCreated)

	for _, format := range []string{"json", "csv"} {
		t.Run(format, func(t *testing.T) {
			exported := api.call(http.MethodGet, examplesIn(source.ID)+"/export?format="+format, nil)
			expectStatus(t, exported, http.StatusOK)
			target := newDataset(api, "input-export-target-"+format)
			rec := api.importFile(target.ID, "export."+format, exported.Body.String())
			expectStatus(t, rec, http.StatusOK)
			if result := decodeData[ImportExamplesResult](t, rec); result.Created != 3 || result.Skipped != 0 {
				t.Fatalf("result = %+v, want all 3 examples created", result)
			}
			got := listExamples(t, api, target.ID)
			want := []string{"", exactInputStored, `{}`}
			for i, example := range got {
				canonical, err := eval.CanonicalInputData(example.InputData)
				if err != nil || string(canonical) != want[i] {
					t.Errorf("example %d inputData = %s, want %s", i+1, example.InputData, want[i])
				}
			}
		})
	}
}
