// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const orderInput = `{"options":{"notes":"say \"hi\", then go","rush":true},"order_id":"O-5003"}`

const (
	exactInputAuthored  = `{"id": 9007199254740993, "big": 12345678901234567890, "f": 1.0, "e": 1e21, "html": "<a&b>"}`
	exactInputCanonical = `{"big":12345678901234567890,"e":1e21,"f":1.0,"html":"<a&b>","id":9007199254740993}`
)

func withWorkflowEvals(svcs *evalSvcSet) {
	svcs.example.SetFeatureRegistry(workflowEvalsFlag(true))
	svcs.dataset.SetFeatureRegistry(workflowEvalsFlag(true))
}

func newInputEvalSvcs(t *testing.T) (context.Context, *evalSvcSet, string) {
	t.Helper()
	ctx, svcs := newEvalSvcs(t)
	withWorkflowEvals(svcs)
	return ctx, svcs, mustCreateDataset(ctx, t, svcs.dataset, "orders")
}

func storedInputs(t *testing.T, svcs *evalSvcSet, datasetID string) []string {
	t.Helper()
	rows, err := svcs.examples.ListByDataset(t.Context(), datasetID)
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}
	inputs := make([]string, len(rows))
	for i, row := range rows {
		inputs[i] = derefString(row.InputData)
	}
	return inputs
}

func csvCell(text string) string {
	return `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
}

func TestCreateExampleWithInputData(t *testing.T) {
	ctx, svcs, dsID := newInputEvalSvcs(t)

	created, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{InputData: json.RawMessage(orderInput), ExpectedResponse: new("urgent")})
	if err != nil {
		t.Fatalf("create an input-only example: %v", err)
	}
	if created.Prompt != "" || string(created.InputData) != orderInput {
		t.Errorf("created = prompt %q inputData %s, want the input and no prompt", created.Prompt, created.InputData)
	}
	got, err := svcs.example.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.InputData) != orderInput {
		t.Errorf("stored inputData = %s, want %s", got.InputData, orderInput)
	}

	_, err = svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "  "})
	if ve := asValidationError(t, err); ve.Field != "prompt" {
		t.Errorf("an example with neither prompt nor inputData: field %q, want prompt", ve.Field)
	}
	_, err = svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "  ", InputData: json.RawMessage("null")})
	if ve := asValidationError(t, err); ve.Field != "prompt" {
		t.Errorf("a null inputData counted as input: field %q, want prompt", ve.Field)
	}
	for _, notObject := range []string{`[1]`, `"O-1"`, `7`, `{"a":1} {"b":2}`} {
		_, err = svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "p", InputData: json.RawMessage(notObject)})
		if ve := asValidationError(t, err); ve.Field != "input_data" {
			t.Errorf("inputData %s: field %q, want input_data", notObject, ve.Field)
		}
	}
}

func TestUpdateExampleInputData(t *testing.T) {
	ctx, svcs, dsID := newInputEvalSvcs(t)
	inputOnly, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{InputData: json.RawMessage(orderInput)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svcs.example.Update(ctx, inputOnly.ID, UpdateExampleRequest{InputDataSet: true})
	if ve := asValidationError(t, err); ve.Field != "input_data" {
		t.Errorf("clearing the only input: field %q, want input_data", ve.Field)
	}

	withPrompt, err := svcs.example.Update(ctx, inputOnly.ID, UpdateExampleRequest{Prompt: new("Handle order O-5003")})
	if err != nil {
		t.Fatalf("add a prompt: %v", err)
	}
	if string(withPrompt.InputData) != orderInput {
		t.Errorf("an update naming only the prompt changed inputData to %s", withPrompt.InputData)
	}

	cleared, err := svcs.example.Update(ctx, inputOnly.ID, UpdateExampleRequest{InputData: json.RawMessage("null"), InputDataSet: true})
	if err != nil {
		t.Fatalf("clear inputData once a prompt exists: %v", err)
	}
	if cleared.InputData != nil {
		t.Errorf("inputData = %s after an explicit null, want cleared", cleared.InputData)
	}

	_, err = svcs.example.Update(ctx, inputOnly.ID, UpdateExampleRequest{Prompt: new("")})
	if ve := asValidationError(t, err); ve.Field != "prompt" {
		t.Errorf("emptying the only input: field %q, want prompt", ve.Field)
	}

	replaced, err := svcs.example.Update(ctx, inputOnly.ID, UpdateExampleRequest{InputData: json.RawMessage(`{"order_id": "O-1"}`), InputDataSet: true})
	if err != nil {
		t.Fatalf("replace inputData: %v", err)
	}
	if string(replaced.InputData) != `{"order_id":"O-1"}` {
		t.Errorf("inputData = %s, want the replacement in canonical form", replaced.InputData)
	}
}

func TestCSVRoundTripsInputData(t *testing.T) {
	ctx, svcs, dsID := newInputEvalSvcs(t)
	if _, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "text only"}); err != nil {
		t.Fatalf("create a text example: %v", err)
	}

	plain, err := svcs.dataset.ExportExamplesCSV(ctx, dsID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if strings.Contains(plain, inputDataColumn) {
		t.Errorf("a dataset with no structured input exported an input_data column:\n%s", plain)
	}

	if _, createErr := svcs.example.Create(ctx, dsID, CreateExampleRequest{InputData: json.RawMessage(orderInput)}); createErr != nil {
		t.Fatalf("create an input example: %v", createErr)
	}
	exported, err := svcs.dataset.ExportExamplesCSV(ctx, dsID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.HasPrefix(exported, "sequence_number,prompt,expected_response,input_data\n") {
		t.Fatalf("header = %q, want the input_data column", strings.SplitN(exported, "\n", 2)[0])
	}

	target := mustCreateDataset(ctx, t, svcs.dataset, "orders-copy")
	result, err := svcs.dataset.ImportExamples(ctx, target, exported, "text/csv", "alice")
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if result.Created != 2 || result.Skipped != 0 {
		t.Fatalf("re-import created %d skipped %d, want 2 and 0", result.Created, result.Skipped)
	}
	if got := storedInputs(t, svcs, target); got[0] != "" || got[1] != orderInput {
		t.Errorf("re-imported inputs = %q, want none then the order input", got)
	}
}

func TestCSVImportInputDataColumn(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantCreated int
		wantSkipped int
		wantErr     string
	}{
		{"input only, no prompt column", "input_data\n\"{\"\"order_id\"\":\"\"O-1\"\"}\"\n", 1, 0, ""},
		{"blank row skipped", "prompt,input_data\n,\n", 0, 1, ""},
		{"a non-object cell names its row", "prompt,input_data\nok,{}\nbad,[1]\n", 0, 0, "row 3"},
		{"a null cell is not an object", "prompt,input_data\nbad,null\n", 0, 0, "row 2"},
		{"invalid JSON names its row", "prompt,input_data\nbad,{oops\n", 0, 0, "row 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, svcs, dsID := newInputEvalSvcs(t)

			result, err := svcs.dataset.ImportExamples(ctx, dsID, tc.body, "text/csv", "alice")

			if tc.wantErr != "" {
				if ve := asValidationError(t, err); !strings.Contains(ve.Message, tc.wantErr) {
					t.Errorf("error %q, want it to name %s", ve.Message, tc.wantErr)
				}
				if n, _ := svcs.examples.CountByDataset(ctx, dsID); n != 0 {
					t.Errorf("a rejected import still created %d examples", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if result.Created != tc.wantCreated || result.Skipped != tc.wantSkipped {
				t.Errorf("created %d skipped %d, want %d and %d", result.Created, result.Skipped, tc.wantCreated, tc.wantSkipped)
			}
		})
	}
}

func TestJSONImportAndExportInputData(t *testing.T) {
	ctx, svcs, dsID := newInputEvalSvcs(t)
	body := `[{"inputData":{"order_id":"O-1"}},{"input_data":{"order_id":"O-2"},"prompt":"p"},{"prompt":"plain"}]`

	result, err := svcs.dataset.ImportExamples(ctx, dsID, body, "application/json", "alice")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Created != 3 {
		t.Fatalf("created %d, want 3", result.Created)
	}
	exported, err := svcs.dataset.ExportExamplesJSON(ctx, dsID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	got := []string{string(exported[0].InputData), string(exported[1].InputData), string(exported[2].InputData)}
	if got[0] != `{"order_id":"O-1"}` || got[1] != `{"order_id":"O-2"}` || got[2] != "" {
		t.Errorf("exported inputs = %q", got)
	}

	_, err = svcs.dataset.ImportExamples(ctx, dsID, `[{"inputData":"O-3"}]`, "application/json", "alice")
	if ve := asValidationError(t, err); !strings.Contains(ve.Message, "example 0") {
		t.Errorf("a non-object inputData: %q, want it to name example 0", ve.Message)
	}
}

func TestInputDataKeepsNumbersExact(t *testing.T) {
	writes := map[string]func(ctx context.Context, svcs *evalSvcSet, dsID string) error{
		"create": func(ctx context.Context, svcs *evalSvcSet, dsID string) error {
			_, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{InputData: json.RawMessage(exactInputAuthored)})
			return err
		},
		"update": func(ctx context.Context, svcs *evalSvcSet, dsID string) error {
			created, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "p"})
			if err != nil {
				return err
			}
			_, err = svcs.example.Update(ctx, created.ID, UpdateExampleRequest{InputData: json.RawMessage(exactInputAuthored), InputDataSet: true})
			return err
		},
		"reconcile": func(ctx context.Context, svcs *evalSvcSet, dsID string) error {
			_, err := svcs.dataset.ReconcileExamples(ctx, dsID, []DesiredExample{{SequenceNumber: 1, InputData: json.RawMessage(exactInputAuthored)}}, "alice")
			return err
		},
		"lenient csv": func(ctx context.Context, svcs *evalSvcSet, dsID string) error {
			_, err := svcs.dataset.ImportExamples(ctx, dsID, "input_data\n"+csvCell(exactInputAuthored)+"\n", "text/csv", "alice")
			return err
		},
		"strict csv": func(ctx context.Context, svcs *evalSvcSet, dsID string) error {
			_, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatCSV, []byte("input_data\n"+csvCell(exactInputAuthored)+"\n"))
			return err
		},
		"lenient json": func(ctx context.Context, svcs *evalSvcSet, dsID string) error {
			_, err := svcs.dataset.ImportExamples(ctx, dsID, `[{"inputData":`+exactInputAuthored+`}]`, "application/json", "alice")
			return err
		},
		"strict json": func(ctx context.Context, svcs *evalSvcSet, dsID string) error {
			_, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatJSON, []byte(`[{"inputData":`+exactInputAuthored+`}]`))
			return err
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			ctx, svcs, dsID := newInputEvalSvcs(t)
			if err := write(ctx, svcs, dsID); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := storedInputs(t, svcs, dsID); len(got) != 1 || got[0] != exactInputCanonical {
				t.Fatalf("stored = %q, want %s", got, exactInputCanonical)
			}
			expectExactExports(ctx, t, svcs, dsID)
		})
	}
}

func expectExactExports(ctx context.Context, t *testing.T, svcs *evalSvcSet, dsID string) {
	t.Helper()
	page, err := svcs.example.List(ctx, dsID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if string(page[0].InputData) != exactInputCanonical {
		t.Errorf("response inputData = %s, want %s", page[0].InputData, exactInputCanonical)
	}

	csvText, err := svcs.dataset.ExportExamplesCSV(ctx, dsID)
	if err != nil {
		t.Fatalf("csv export: %v", err)
	}
	records, err := csv.NewReader(strings.NewReader(csvText)).ReadAll()
	if err != nil || len(records) != 2 || records[1][3] != exactInputCanonical {
		t.Errorf("csv export = %q, %v, want the input cell %s", records, err, exactInputCanonical)
	}

	jsonRecords, err := svcs.dataset.ExportExamplesJSON(ctx, dsID)
	if err != nil {
		t.Fatalf("json export: %v", err)
	}
	if string(jsonRecords[0].InputData) != exactInputCanonical {
		t.Errorf("json export inputData = %s, want %s", jsonRecords[0].InputData, exactInputCanonical)
	}

	exported, err := json.Marshal(jsonRecords)
	if err != nil {
		t.Fatal(err)
	}
	copyID := mustCreateDataset(ctx, t, svcs.dataset, "orders-copy")
	if _, err := svcs.dataset.ImportExamplesStrict(ctx, copyID, ImportFormatJSON, exported); err != nil {
		t.Fatalf("re-import the json export: %v", err)
	}
	if _, err := svcs.dataset.ImportExamples(ctx, copyID, csvText, "text/csv", "alice"); err != nil {
		t.Fatalf("re-import the csv export: %v", err)
	}
	for i, got := range storedInputs(t, svcs, copyID) {
		if got != exactInputCanonical {
			t.Errorf("re-imported example %d input = %s, want %s", i+1, got, exactInputCanonical)
		}
	}
}

func TestJSONExportImportKeepsEveryInput(t *testing.T) {
	ctx, svcs, dsID := newInputEvalSvcs(t)
	for _, req := range []CreateExampleRequest{
		{Prompt: "every field optional", InputData: json.RawMessage(`{}`)},
		{InputData: json.RawMessage(`{"order_id":"O-1"}`)},
		{Prompt: "text only"},
	} {
		if _, err := svcs.example.Create(ctx, dsID, req); err != nil {
			t.Fatalf("create %+v: %v", req, err)
		}
	}
	records, err := svcs.dataset.ExportExamplesJSON(ctx, dsID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	exported, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"inputData":{}`, `"prompt":"","expectedResponse":null,"inputData":{"order_id":"O-1"}`, `"prompt":"text only","expectedResponse":null}`} {
		if !strings.Contains(string(exported), want) {
			t.Errorf("export lacks %s:\n%s", want, exported)
		}
	}

	imports := map[string]func(copyID string) (*ImportExamplesResult, error){
		"lenient": func(copyID string) (*ImportExamplesResult, error) {
			return svcs.dataset.ImportExamples(ctx, copyID, string(exported), "application/json", "alice")
		},
		"strict": func(copyID string) (*ImportExamplesResult, error) {
			return svcs.dataset.ImportExamplesStrict(ctx, copyID, ImportFormatJSON, exported)
		},
	}
	for name, importJSON := range imports {
		t.Run(name, func(t *testing.T) {
			copyID := mustCreateDataset(ctx, t, svcs.dataset, "orders-"+name)
			result, err := importJSON(copyID)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if result.Created != 3 || result.Skipped != 0 {
				t.Errorf("created %d skipped %d, want 3 and 0", result.Created, result.Skipped)
			}
			got := storedInputs(t, svcs, copyID)
			if strings.Join(got, "|") != `{}|{"order_id":"O-1"}|` {
				t.Errorf("re-imported inputs = %q", got)
			}
		})
	}
}

func TestDiffExamplesComparesInputData(t *testing.T) {
	stored := orderInput
	actual := []*store.EvalExample{{ID: "a", SequenceNumber: 1, Prompt: "p", InputData: &stored}}
	reordered := json.RawMessage(`{ "order_id": "O-5003", "options": {"rush": true, "notes": "say \"hi\", then go"} }`)

	if diff := DiffExamples(actual, []DesiredExample{{SequenceNumber: 1, Prompt: "p", InputData: reordered}}); diff.Updates() != 0 {
		t.Errorf("an equal input planned %d updates", diff.Updates())
	}
	changed := DiffExamples(actual, []DesiredExample{{SequenceNumber: 1, Prompt: "p", InputData: json.RawMessage(`{"order_id":"O-1"}`)}})
	if changed.Updates() != 1 || string(changed.UpdateOps[0].InputData) != `{"order_id":"O-1"}` {
		t.Errorf("a changed input planned %+v, want one update carrying it", changed.UpdateOps)
	}
	if removed := DiffExamples(actual, []DesiredExample{{SequenceNumber: 1, Prompt: "p"}}); removed.Updates() != 1 {
		t.Errorf("a removed input planned %d updates, want 1", removed.Updates())
	}

	big := `{"id":9007199254740993}`
	bigRow := []*store.EvalExample{{ID: "b", SequenceNumber: 1, Prompt: "p", InputData: &big}}
	if diff := DiffExamples(bigRow, []DesiredExample{{SequenceNumber: 1, Prompt: "p", InputData: json.RawMessage(`{"id":9007199254740992}`)}}); diff.Updates() != 1 {
		t.Errorf("ids one apart past 2^53 planned %d updates, want 1", diff.Updates())
	}
}

func TestReconcileExamplesWritesInputData(t *testing.T) {
	ctx, svcs, dsID := newInputEvalSvcs(t)
	desired := []DesiredExample{{SequenceNumber: 1, InputData: json.RawMessage(orderInput)}}

	if _, err := svcs.dataset.ReconcileExamples(ctx, dsID, desired, "alice"); err != nil {
		t.Fatalf("reconcile insert: %v", err)
	}
	again, err := svcs.dataset.ReconcileExamples(ctx, dsID, desired, "alice")
	if err != nil {
		t.Fatalf("reconcile again: %v", err)
	}
	if again.Updates+again.Inserts+again.Deletes != 0 {
		t.Errorf("a converged dataset reconciled %+v, want no changes", again)
	}
	desired[0].InputData = json.RawMessage(`{"order_id":"O-9"}`)
	if _, updateErr := svcs.dataset.ReconcileExamples(ctx, dsID, desired, "alice"); updateErr != nil {
		t.Fatalf("reconcile update: %v", updateErr)
	}
	if got := storedInputs(t, svcs, dsID); got[0] != `{"order_id":"O-9"}` {
		t.Errorf("stored input = %s, want the reconciled one", got[0])
	}

	_, err = svcs.dataset.ReconcileExamples(ctx, dsID, []DesiredExample{{SequenceNumber: 1}}, "alice")
	if ve := asValidationError(t, err); !strings.Contains(ve.Message, "inputData") {
		t.Errorf("an example with no prompt and no input: %q", ve.Message)
	}
}

type inputWritePath struct {
	name  string
	seed  bool
	write func(ctx context.Context, svcs *evalSvcSet, dsID, exampleID, input string) error
}

func inputWritePaths() []inputWritePath {
	return []inputWritePath{
		{name: "create", write: func(ctx context.Context, svcs *evalSvcSet, dsID, _, input string) error {
			_, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "p", InputData: json.RawMessage(input)})
			return err
		}},
		{name: "update", seed: true, write: func(ctx context.Context, svcs *evalSvcSet, _, exampleID, input string) error {
			_, err := svcs.example.Update(ctx, exampleID, UpdateExampleRequest{InputData: json.RawMessage(input), InputDataSet: true})
			return err
		}},
		{name: "reconcile insert", write: func(ctx context.Context, svcs *evalSvcSet, dsID, _, input string) error {
			_, err := svcs.dataset.ReconcileExamples(ctx, dsID, []DesiredExample{{SequenceNumber: 1, Prompt: "p", InputData: json.RawMessage(input)}}, "alice")
			return err
		}},
		{name: "reconcile update", seed: true, write: func(ctx context.Context, svcs *evalSvcSet, dsID, _, input string) error {
			_, err := svcs.dataset.ReconcileExamples(ctx, dsID, []DesiredExample{{SequenceNumber: 1, Prompt: "p", InputData: json.RawMessage(input)}}, "alice")
			return err
		}},
		{name: "lenient csv", write: func(ctx context.Context, svcs *evalSvcSet, dsID, _, input string) error {
			_, err := svcs.dataset.ImportExamples(ctx, dsID, "prompt,input_data\np,"+csvCell(input)+"\n", "text/csv", "alice")
			return err
		}},
		{name: "strict csv", write: func(ctx context.Context, svcs *evalSvcSet, dsID, _, input string) error {
			_, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatCSV, []byte("prompt,input_data\np,"+csvCell(input)+"\n"))
			return err
		}},
		{name: "lenient json", write: func(ctx context.Context, svcs *evalSvcSet, dsID, _, input string) error {
			_, err := svcs.dataset.ImportExamples(ctx, dsID, `[{"prompt":"p","inputData":`+input+`}]`, "application/json", "alice")
			return err
		}},
		{name: "strict json", write: func(ctx context.Context, svcs *evalSvcSet, dsID, _, input string) error {
			_, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatJSON, []byte(`[{"prompt":"p","inputData":`+input+`}]`))
			return err
		}},
	}
}

func runInputWrite(ctx context.Context, t *testing.T, svcs *evalSvcSet, dsID string, path inputWritePath, input string) ([]string, error) {
	t.Helper()
	exampleID := ""
	if path.seed {
		seeded, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "p"})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		exampleID = seeded.ID
	}
	err := path.write(ctx, svcs, dsID, exampleID, input)
	return storedInputs(t, svcs, dsID), err
}

func sizedInput(n int) string {
	const overhead = len(`{"b":""}`)
	return `{"b":"` + strings.Repeat("x", n-overhead) + `"}`
}

func TestInputDataSizeLimitOnEveryWritePath(t *testing.T) {
	atLimit, overLimit := sizedInput(maxExampleInputDataBytes), sizedInput(maxExampleInputDataBytes+1)
	for _, path := range inputWritePaths() {
		t.Run(path.name+"/at the limit", func(t *testing.T) {
			ctx, svcs, dsID := newInputEvalSvcs(t)
			stored, err := runInputWrite(ctx, t, svcs, dsID, path, atLimit)
			if err != nil {
				t.Fatalf("a %d-byte input: %v", maxExampleInputDataBytes, err)
			}
			if len(stored) != 1 || stored[0] != atLimit {
				t.Errorf("stored %d inputs, want the one at the limit", len(stored))
			}
		})
		t.Run(path.name+"/one byte over", func(t *testing.T) {
			ctx, svcs, dsID := newInputEvalSvcs(t)
			stored, err := runInputWrite(ctx, t, svcs, dsID, path, overLimit)
			if ve := asValidationError(t, err); !strings.Contains(ve.Error(), "262144") {
				t.Errorf("error %q does not name the limit", ve.Error())
			}
			wantStored := 0
			if path.seed {
				wantStored = 1
			}
			if len(stored) != wantStored || (wantStored == 1 && stored[0] != "") {
				t.Errorf("a refused write left inputs %q, want %d examples and no input", stored, wantStored)
			}
		})
	}
}

func TestInputDataNeedsWorkflowEvals(t *testing.T) {
	for _, path := range inputWritePaths() {
		t.Run(path.name, func(t *testing.T) {
			ctx, svcs := newEvalSvcs(t)
			dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")

			stored, err := runInputWrite(ctx, t, svcs, dsID, path, orderInput)

			ve := asValidationError(t, err)
			if ve.Field != "input_data" || !strings.Contains(ve.Message, "SAM_FEATURE_WORKFLOW_EVALS") {
				t.Errorf("error = %s: %q, want input_data naming the flag", ve.Field, ve.Message)
			}
			for _, input := range stored {
				if input != "" {
					t.Errorf("a refused write stored input %s", input)
				}
			}
			if !path.seed && len(stored) != 0 {
				t.Errorf("a refused write created %d examples", len(stored))
			}
		})
	}

	t.Run("clearing stays allowed", func(t *testing.T) {
		ctx, svcs := newEvalSvcs(t)
		dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")
		input := orderInput
		row := &store.EvalExample{DatasetID: dsID, SequenceNumber: 1, Prompt: "p", InputData: &input}
		if err := svcs.examples.Create(ctx, row); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cleared, err := svcs.example.Update(ctx, row.ID, UpdateExampleRequest{InputData: json.RawMessage("null"), InputDataSet: true})
		if err != nil {
			t.Fatalf("clear with the flag off: %v", err)
		}
		if cleared.InputData != nil {
			t.Errorf("inputData = %s, want cleared", cleared.InputData)
		}
	})

	t.Run("null and blank inputs stay allowed", func(t *testing.T) {
		ctx, svcs := newEvalSvcs(t)
		dsID := mustCreateDataset(ctx, t, svcs.dataset, "orders")
		if _, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "p", InputData: json.RawMessage("null")}); err != nil {
			t.Errorf("create with a null inputData: %v", err)
		}
		if _, err := svcs.dataset.ImportExamples(ctx, dsID, "prompt,input_data\nq,\n", "text/csv", "alice"); err != nil {
			t.Errorf("import with a blank input_data cell: %v", err)
		}
		if _, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatJSON, []byte(`[{"prompt":"r","inputData":null}]`)); err != nil {
			t.Errorf("import with a null inputData: %v", err)
		}
	})
}

func TestTriggerRunsRefusesAnAgentRunOverPromptlessExamples(t *testing.T) {
	cases := []struct {
		name       string
		promptless int
		wantSeqs   string
	}{
		{"one", 1, "sequence numbers 3)"},
		{"past the listed cap", maxListedPromptlessExamples + 2, "sequence numbers 3, 4, 5, 6, 7, 8, 9, 10, 11, 12 and 2 more)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRunFixture(t, 2)
			for i := range tc.promptless {
				input := `{"order_id":"O-1"}`
				ex := &store.EvalExample{DatasetID: f.datasetID, SequenceNumber: 3 + i, InputData: &input}
				if err := f.exampleRepo.Create(f.ctx, ex); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}

			_, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")

			ve := asValidationError(t, err)
			if ve.Field != "dataset_id" || !strings.Contains(ve.Message, tc.wantSeqs) {
				t.Errorf("error = %s: %q, want dataset_id naming %q", ve.Field, ve.Message, tc.wantSeqs)
			}
			runs, listErr := f.runRepo.ListByExperiment(f.ctx, f.experimentID)
			if listErr != nil {
				t.Fatalf("list runs: %v", listErr)
			}
			if len(runs) != 0 {
				t.Errorf("a refused trigger created %d runs", len(runs))
			}
		})
	}
}

func TestBuildConfigSnapshotCarriesInputData(t *testing.T) {
	input := `{"order_id":"O-1"}`
	examples := []*store.EvalExample{
		{ID: "a", SequenceNumber: 1, InputData: &input},
		{ID: "b", SequenceNumber: 2, Prompt: "text only"},
	}
	snapshot := buildConfigSnapshot(&store.EvalExperiment{ID: "e"}, "d", "orders", examples, nil)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Dataset struct {
			Examples []map[string]json.RawMessage `json:"examples"`
		} `json:"dataset"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	withInput, textOnly := decoded.Dataset.Examples[0], decoded.Dataset.Examples[1]
	if got := string(withInput["input_data"]); got != input {
		t.Errorf("input example's input_data = %s, want the object %s", got, input)
	}
	if got, ok := textOnly["input_data"]; !ok || string(got) != "null" {
		t.Errorf("text example's input_data = %s (present %v), want present and null", got, ok)
	}
	if v, ok := snapshot["dataset"].(map[string]any)["examples"].([]map[string]any)[1]["input_data"]; !ok || v != nil {
		t.Errorf("text example's input_data = %#v (present %v), want an untyped nil", v, ok)
	}
}
