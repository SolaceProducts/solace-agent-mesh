// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sentExpectedFacts(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	raw, sent := body.Data["expectedFacts"]
	if !sent {
		t.Fatalf("no expectedFacts key in %s", rec.Body.String())
	}
	return string(raw)
}

func TestExamples_ExpectedFacts(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	dataset := newDataset(api, "facts-examples")

	rec := api.call(http.MethodPost, examplesIn(dataset.ID), `{"prompt":"refund O-1","expectedFacts":{"required":["O-1","refund approved"]}}`)
	created := expectCreated[Example](t, rec, "evaluations/examples")
	const first = `{"required":["O-1","refund approved"],"forbidden":[]}`
	if got := sentExpectedFacts(t, rec); got != first {
		t.Fatalf("created expectedFacts = %s, want %s (forbidden an empty list)", got, first)
	}
	path := examplesPath + "/" + created.ID
	if got := sentExpectedFacts(t, api.call(http.MethodGet, path, nil)); got != first {
		t.Errorf("GET expectedFacts = %s, want %s", got, first)
	}
	if got := sentExpectedFacts(t, api.call(http.MethodPost, examplesIn(dataset.ID), `{"prompt":"none"}`)); got != "null" {
		t.Errorf("an example without facts carries %s, want null", got)
	}

	steps := []struct {
		name string
		body string
		want string
	}{
		{"absent keeps them", `{"prompt":"refund O-2"}`, first},
		{"facts replace them", `{"expectedFacts":{"forbidden":["escalated"]}}`, `{"required":[],"forbidden":["escalated"]}`},
		{"null clears them", `{"expectedFacts":null}`, "null"},
	}
	for _, step := range steps {
		rec = api.call(http.MethodPatch, path, step.body)
		expectStatus(t, rec, http.StatusOK)
		if got := sentExpectedFacts(t, rec); got != step.want {
			t.Errorf("%s: expectedFacts = %s, want %s", step.name, got, step.want)
		}
		if got := sentExpectedFacts(t, api.call(http.MethodGet, path, nil)); got != step.want {
			t.Errorf("GET after %s: expectedFacts = %s, want %s", step.name, got, step.want)
		}
	}

	refusals := []struct {
		name, body, field, problem string
	}{
		{"a value listed twice", `{"prompt":"x","expectedFacts":{"required":["a","A"]}}`, "expectedFacts.required", `"A" is listed more than once`},
		{"a value both required and forbidden", `{"prompt":"x","expectedFacts":{"required":["a"],"forbidden":["a"]}}`, "expectedFacts.forbidden", `"a" is both required and forbidden`},
		{"a blank value", `{"prompt":"x","expectedFacts":{"forbidden":[" "]}}`, "expectedFacts.forbidden", "must not be blank"},
		{"no values", `{"prompt":"x","expectedFacts":{"required":[],"forbidden":[]}}`, "expectedFacts", "needs at least one required or forbidden value"},
	}
	for _, r := range refusals {
		t.Run(r.name, func(t *testing.T) {
			rec := api.call(http.MethodPost, examplesIn(dataset.ID), r.body)
			expectStatus(t, rec, http.StatusUnprocessableEntity)
			if !strings.Contains(rec.Body.String(), `"`+r.field+`"`) || !strings.Contains(rec.Body.String(), jsonText(t, r.problem)) {
				t.Errorf("body %s, want %s naming %q", rec.Body.String(), r.field, r.problem)
			}
		})
	}
}

func TestExamples_ExpectedFactsPatchRefusalKeepsTheStoredFacts(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	dataset := newDataset(api, "facts-patch")
	created := expectCreated[Example](t, api.call(http.MethodPost, examplesIn(dataset.ID),
		`{"prompt":"refund O-1","expectedFacts":{"required":["O-1"]}}`), "evaluations/examples")
	path := examplesPath + "/" + created.ID
	const stored = `{"required":["O-1"],"forbidden":[]}`

	expectStatus(t, api.call(http.MethodPatch, path, `{"expectedFacts":{"required":["a","a"]}}`), http.StatusUnprocessableEntity)
	if got := sentExpectedFacts(t, api.call(http.MethodGet, path, nil)); got != stored {
		t.Errorf("stored expectedFacts = %s, want it kept as %s", got, stored)
	}
}

func TestExamples_ExpectedFactsNeedWorkflowEvals(t *testing.T) {
	api := newWorkflowEvalsAPI(t, false)
	dataset := newDataset(api, "facts-off")
	const withFacts = `"expectedFacts":{"required":["O-1"]}`

	expectStatus(t, api.call(http.MethodPost, examplesIn(dataset.ID), `{"prompt":"x",`+withFacts+`}`), http.StatusUnprocessableEntity)
	created := expectCreated[Example](t, api.call(http.MethodPost, examplesIn(dataset.ID), `{"prompt":"x"}`), "evaluations/examples")
	path := examplesPath + "/" + created.ID
	expectStatus(t, api.call(http.MethodPatch, path, `{`+withFacts+`}`), http.StatusUnprocessableEntity)
	rec := api.call(http.MethodPatch, path, `{"expectedFacts":null}`)
	expectStatus(t, rec, http.StatusOK)
	if got := sentExpectedFacts(t, rec); got != "null" {
		t.Errorf("cleared expectedFacts = %s, want null", got)
	}
	for name, file := range map[string]string{
		"facts.json": `[{"prompt":"x",` + withFacts + `}]`,
		"facts.csv":  "prompt,expected_facts\n" + `"x","{""required"":[""O-1""]}"` + "\n",
	} {
		importRec := api.importFile(dataset.ID, name, file)
		expectStatus(t, importRec, http.StatusUnprocessableEntity)
		if !strings.Contains(importRec.Body.String(), "workflow evaluations are turned off") {
			t.Errorf("%s refusal %s does not name the flag", name, importRec.Body.String())
		}
	}
	if len(listExamples(t, api, dataset.ID)) != 1 {
		t.Error("a refused import added examples")
	}
}

func TestExamples_ExpectedFactsImportAndExport(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	source := newDataset(api, "facts-export-source")
	csvFile := "prompt,expected_facts\n" +
		`"with facts","{""required"":[""O-1""],""forbidden"":[""escalated""]}"` + "\n" +
		`"blank",` + "\n" +
		`"null",null` + "\n"
	expectStatus(t, api.importFile(source.ID, "facts.csv", csvFile), http.StatusOK)
	want := []string{`{"required":["O-1"],"forbidden":["escalated"]}`, "null", "null"}
	checkFacts := func(t *testing.T, datasetID string) {
		t.Helper()
		got := listExamples(t, api, datasetID)
		if len(got) != len(want) {
			t.Fatalf("examples = %d, want %d", len(got), len(want))
		}
		for i, example := range got {
			encoded, err := json.Marshal(example.ExpectedFacts)
			if err != nil || string(encoded) != want[i] {
				t.Errorf("example %d expectedFacts = %s, want %s", i+1, encoded, want[i])
			}
		}
	}
	checkFacts(t, source.ID)

	for _, format := range []string{"json", "csv"} {
		t.Run(format, func(t *testing.T) {
			exported := api.call(http.MethodGet, examplesIn(source.ID)+"/export?format="+format, nil)
			expectStatus(t, exported, http.StatusOK)
			if !strings.Contains(exported.Body.String(), "escalated") {
				t.Fatalf("export %s does not carry the facts", exported.Body.String())
			}
			target := newDataset(api, "facts-export-target-"+format)
			expectStatus(t, api.importFile(target.ID, "export."+format, exported.Body.String()), http.StatusOK)
			checkFacts(t, target.ID)
		})
	}
}
