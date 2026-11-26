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

func sentExpectedPath(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	raw, sent := body.Data["expectedPath"]
	if !sent {
		t.Fatalf("no expectedPath key in %s", rec.Body.String())
	}
	return string(raw)
}

func TestExamples_ExpectedPath(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	dataset := newDataset(api, "path-examples")

	rec := api.call(http.MethodPost, examplesIn(dataset.ID), `{"prompt":"refund O-1","expectedPath":{"nodes":[`+
		`{"nodeId":"ship","expectation":"skipped"},{"nodeId":"approve","expectation":"succeeded"},{"nodeId":"notify","expectation":"ran"},`+
		`{"nodeId":"bill","expectation":"failed"},{"nodeId":"escalate","expectation":"not_ran"}]}}`)
	created := expectCreated[Example](t, rec, "evaluations/examples")
	const sorted = `{"nodes":[{"nodeId":"approve","expectation":"succeeded"},{"nodeId":"bill","expectation":"failed"},` +
		`{"nodeId":"escalate","expectation":"not_ran"},{"nodeId":"notify","expectation":"ran"},{"nodeId":"ship","expectation":"skipped"}],"tools":[]}`
	if got := sentExpectedPath(t, rec); got != sorted {
		t.Fatalf("created expectedPath = %s, want %s (nodes in id order, tools an empty list)", got, sorted)
	}
	path := examplesPath + "/" + created.ID
	if got := sentExpectedPath(t, api.call(http.MethodGet, path, nil)); got != sorted {
		t.Errorf("GET expectedPath = %s, want %s", got, sorted)
	}

	steps := []struct {
		name string
		body string
		want string
	}{
		{"absent keeps it", `{"prompt":"refund O-2"}`, sorted},
		{"a path replaces it", `{"expectedPath":{"tools":[{"toolName":"issue_refund","expectation":"called"}]}}`,
			`{"nodes":[],"tools":[{"toolName":"issue_refund","expectation":"called"}]}`},
		{"null clears it", `{"expectedPath":null}`, "null"},
	}
	for _, step := range steps {
		rec = api.call(http.MethodPatch, path, step.body)
		expectStatus(t, rec, http.StatusOK)
		if got := sentExpectedPath(t, rec); got != step.want {
			t.Errorf("%s: expectedPath = %s, want %s", step.name, got, step.want)
		}
		if got := sentExpectedPath(t, api.call(http.MethodGet, path, nil)); got != step.want {
			t.Errorf("GET after %s: expectedPath = %s, want %s", step.name, got, step.want)
		}
	}

	refusals := []struct {
		name, body, field, problem string
	}{
		{"a node named twice", `{"prompt":"x","expectedPath":{"nodes":[{"nodeId":"a","expectation":"ran"},{"nodeId":"a","expectation":"failed"}]}}`,
			"expectedPath.nodes", `names node "a" more than once`},
		{"an unknown expectation", `{"prompt":"x","expectedPath":{"tools":[{"toolName":"issue_refund","expectation":"done"}]}}`,
			"expectedPath.tools", `"issue_refund": "done" is not one of called, not_called`},
		{"an empty path", `{"prompt":"x","expectedPath":{"nodes":[],"tools":[]}}`,
			"expectedPath", "needs at least one node or tool assertion"},
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

func jsonText(t *testing.T, s string) string {
	t.Helper()
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Trim(string(encoded), `"`)
}

func TestExamples_ExpectedPathNeedsWorkflowEvals(t *testing.T) {
	api := newWorkflowEvalsAPI(t, false)
	dataset := newDataset(api, "path-off")
	const withPath = `"expectedPath":{"nodes":[{"nodeId":"approve","expectation":"ran"}]}`

	expectStatus(t, api.call(http.MethodPost, examplesIn(dataset.ID), `{"prompt":"x",`+withPath+`}`), http.StatusUnprocessableEntity)

	created := expectCreated[Example](t, api.call(http.MethodPost, examplesIn(dataset.ID), `{"prompt":"x"}`), "evaluations/examples")
	path := examplesPath + "/" + created.ID
	expectStatus(t, api.call(http.MethodPatch, path, `{`+withPath+`}`), http.StatusUnprocessableEntity)
	rec := api.call(http.MethodPatch, path, `{"expectedPath":null}`)
	expectStatus(t, rec, http.StatusOK)
	if got := sentExpectedPath(t, rec); got != "null" {
		t.Errorf("cleared expectedPath = %s, want null", got)
	}
	importRec := api.importFile(dataset.ID, "paths.json", `[{"prompt":"x","expectedPath":{"nodes":{"approve":"ran"}}}]`)
	expectStatus(t, importRec, http.StatusUnprocessableEntity)
	if !strings.Contains(importRec.Body.String(), "workflow evaluations are turned off") {
		t.Errorf("import refusal %s does not name the flag", importRec.Body.String())
	}
	if len(listExamples(t, api, dataset.ID)) != 1 {
		t.Error("a refused import added examples")
	}
}

func TestExamples_ExpectedPathPatchRefusalKeepsTheStoredPath(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	dataset := newDataset(api, "path-patch")
	created := expectCreated[Example](t, api.call(http.MethodPost, examplesIn(dataset.ID),
		`{"prompt":"refund O-1","expectedPath":{"nodes":[{"nodeId":"approve","expectation":"succeeded"}]}}`), "evaluations/examples")
	path := examplesPath + "/" + created.ID
	const stored = `{"nodes":[{"nodeId":"approve","expectation":"succeeded"}],"tools":[]}`

	for name, body := range map[string]string{
		"a node named twice":     `{"expectedPath":{"nodes":[{"nodeId":"a","expectation":"ran"},{"nodeId":"a","expectation":"failed"}]}}`,
		"an unknown expectation": `{"expectedPath":{"tools":[{"toolName":"issue_refund","expectation":"done"}]}}`,
		"an empty path":          `{"expectedPath":{"nodes":[],"tools":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, api.call(http.MethodPatch, path, body), http.StatusUnprocessableEntity)
			if got := sentExpectedPath(t, api.call(http.MethodGet, path, nil)); got != stored {
				t.Errorf("stored expectedPath = %s, want it kept as %s", got, stored)
			}
		})
	}
}

func TestExamples_ExpectedPathImportsAndExports(t *testing.T) {
	api := newWorkflowEvalsAPI(t, true)
	source := newDataset(api, "path-export-source")
	csvFile := "prompt,expected_path\n" +
		`"with a path","{""nodes"":{""approve"":""succeeded""},""tools"":{""issue_refund"":""called""}}"` + "\n" +
		`"blank",` + "\n" +
		`"null",null` + "\n"
	expectStatus(t, api.importFile(source.ID, "paths.csv", csvFile), http.StatusOK)
	want := []string{
		`{"nodes":[{"nodeId":"approve","expectation":"succeeded"}],"tools":[{"toolName":"issue_refund","expectation":"called"}]}`,
		"null",
		"null",
	}
	checkPaths := func(t *testing.T, datasetID string) {
		t.Helper()
		got := listExamples(t, api, datasetID)
		if len(got) != len(want) {
			t.Fatalf("examples = %d, want %d", len(got), len(want))
		}
		for i, example := range got {
			encoded, err := json.Marshal(example.ExpectedPath)
			if err != nil || string(encoded) != want[i] {
				t.Errorf("example %d expectedPath = %s, want %s", i+1, encoded, want[i])
			}
		}
	}
	checkPaths(t, source.ID)

	for _, format := range []string{"json", "csv"} {
		t.Run(format, func(t *testing.T) {
			exported := api.call(http.MethodGet, examplesIn(source.ID)+"/export?format="+format, nil)
			expectStatus(t, exported, http.StatusOK)
			if !strings.Contains(exported.Body.String(), "approve") {
				t.Fatalf("export %s does not carry the path", exported.Body.String())
			}
			target := newDataset(api, "path-export-target-"+format)
			expectStatus(t, api.importFile(target.ID, "export."+format, exported.Body.String()), http.StatusOK)
			checkPaths(t, target.ID)
		})
	}
}
