// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func importedPrompts(t *testing.T, svcs *evalSvcSet, datasetID string) []string {
	t.Helper()
	rows, err := svcs.examples.ListByDataset(t.Context(), datasetID)
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}
	prompts := make([]string, len(rows))
	for i, row := range rows {
		prompts[i] = row.Prompt
	}
	return prompts
}

func TestImportExamplesStrict_CSV(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "StrictCSV")

	body := "prompt,expected_response,notes\nA,a,x\n  ,skipped,\nB,,y\n"
	res, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatCSV, []byte(body))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 2 || res.Skipped != 1 || res.Dropped != 0 {
		t.Fatalf("result = %+v, want 2 created and 1 skipped", res)
	}
	if got := importedPrompts(t, svcs, dsID); !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("prompts = %v", got)
	}
}

func TestImportExamplesStrict_CSVRefusesWhatItCannotReadWhole(t *testing.T) {
	for name, tc := range map[string]struct{ body, detail string }{
		"bare quote":     {"prompt\nfine\nbad \"quote\n", "row 2 is not valid CSV"},
		"unclosed quote": {"prompt\nfine\n\"open\n", "row 2 is not valid CSV"},
		"bad header":     {"pro\"mpt\nfoo\n", "header row"},
		"no prompt":      {"expected_response\nfoo\n", "missing the required column prompt"},
		"array input":    {"prompt,input_data\nok,{}\nbad,[1]\n", "row 2: input_data must be a JSON object"},
		"empty":          {"", "empty"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, svcs := newEvalSvcs(t)
			dsID := mustCreateDataset(ctx, t, svcs.dataset, "StrictCSVBad")
			_, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatCSV, []byte(tc.body))
			ve := asValidationError(t, err)
			if got := strings.Join(ve.Details["file"], " "); !strings.Contains(got, tc.detail) {
				t.Errorf("file details = %q, want one containing %q", got, tc.detail)
			}
			if got := importedPrompts(t, svcs, dsID); len(got) != 0 {
				t.Errorf("a refused import left %v behind", got)
			}
		})
	}
}

func TestImportExamplesStrict_JSON(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "StrictJSON")

	body := `[{"sequenceNumber":4,"prompt":" A ","expectedResponse":"a"},{"prompt":"B","expectedResponse":null},{"prompt":"C","expectedResponse":""},{"expectedResponse":"no prompt"}]`
	res, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatJSON, []byte(body))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 3 || res.Skipped != 1 {
		t.Fatalf("result = %+v, want 3 created and 1 skipped", res)
	}
	rows, err := svcs.examples.ListByDataset(ctx, dsID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Prompt != "A" || rows[0].ExpectedResponse == nil || *rows[0].ExpectedResponse != "a" ||
		rows[1].ExpectedResponse != nil || rows[2].ExpectedResponse != nil {
		t.Errorf("rows = %+v, want trimmed prompts and nil for a null or empty answer", rows)
	}
	if rows[0].SequenceNumber != 1 || rows[2].SequenceNumber != 3 {
		t.Errorf("sequence numbers = %d..%d, want the next free numbers from 1", rows[0].SequenceNumber, rows[2].SequenceNumber)
	}
}

func TestImportExamplesStrict_ReadsTheSnakeCaseKeysOfOlderExports(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "OlderExport")

	body := `[{"sequence_number":7,"prompt":"A","expected_response":"a"},{"sequence_number":8,"prompt":"B","expected_response":null}]`
	res, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatJSON, []byte(body))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 2 {
		t.Fatalf("result = %+v, want 2 created", res)
	}
	rows, err := svcs.examples.ListByDataset(ctx, dsID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].ExpectedResponse == nil || *rows[0].ExpectedResponse != "a" || rows[1].ExpectedResponse != nil || rows[0].SequenceNumber != 1 {
		t.Errorf("rows = %+v, want expected_response read and sequence_number ignored", rows)
	}
}

func TestImportExamplesStrict_JSONRefusesWhatItCannotReadWhole(t *testing.T) {
	for name, tc := range map[string]struct{ body, detail string }{
		"unknown key":      {`[{"prompt":"ok"},{"prompt":"x","colour":"red"}]`, `row 2 has the unknown field "colour"`},
		"number prompt":    {`[{"prompt":7}]`, "row 1 has a prompt that must be a string"},
		"text sequence":    {`[{"prompt":"x","sequenceNumber":"1"}]`, "row 1 has a sequenceNumber that must be an integer"},
		"non-object row":   {`[{"prompt":"x"},"y"]`, "row 2 must be an object"},
		"null row":         {`[null]`, "row 1 must be an object"},
		"array input":      {`[{"prompt":"x","inputData":[1]}]`, "row 1 has an inputData that must be a JSON object or null"},
		"object not array": {`{"prompt":"x"}`, "must be a JSON array"},
		"null":             {`null`, "must be a JSON array"},
		"two arrays":       {`[{"prompt":"x"}] []`, "not valid JSON"},
		"truncated":        {`[{"prompt":"x"}`, "not valid JSON"},
		"empty":            {``, "not valid JSON"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, svcs := newEvalSvcs(t)
			dsID := mustCreateDataset(ctx, t, svcs.dataset, "StrictJSONBad")
			_, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatJSON, []byte(tc.body))
			ve := asValidationError(t, err)
			if got := strings.Join(ve.Details["file"], " "); !strings.Contains(got, tc.detail) {
				t.Errorf("file details = %q, want one containing %q", got, tc.detail)
			}
			if got := importedPrompts(t, svcs, dsID); len(got) != 0 {
				t.Errorf("a refused import left %v behind", got)
			}
		})
	}
}

func TestImportExamplesStrict_DropsRowsPastTheCap(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "StrictCap")
	withMaxExamples(t, svcs, 3)

	res, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatCSV, []byte("prompt\na\nb\nc\nd\n"))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 3 || res.Dropped != 1 || res.Limit != 3 {
		t.Fatalf("result = %+v, want 3 created, 1 dropped and a limit of 3", res)
	}
}

func TestImportExamplesStrict_JSONDropsRowsPastTheCap(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "StrictJSONCap")
	withMaxExamples(t, svcs, 2)

	body := `[{"prompt":"a"},{"prompt":"b"},{"prompt":"c"},{"prompt":" "}]`
	res, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatJSON, []byte(body))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 2 || res.Dropped != 1 || res.Skipped != 1 {
		t.Fatalf("result = %+v, want 2 created, 1 dropped and 1 skipped", res)
	}
}

func TestImportExamplesStrict_IgnoresAByteOrderMark(t *testing.T) {
	for format, body := range map[ImportFormat]string{
		ImportFormatCSV:  "\ufeffprompt\na\n",
		ImportFormatJSON: "\ufeff[{\"prompt\":\"a\"}]",
	} {
		t.Run(string(format), func(t *testing.T) {
			ctx, svcs := newEvalSvcs(t)
			dsID := mustCreateDataset(ctx, t, svcs.dataset, "StrictBOM")
			res, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, format, []byte(body))
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if res.Created != 1 {
				t.Errorf("result = %+v, want the one example created", res)
			}
		})
	}
}

func TestImportExamplesStrict_UnknownDataset(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	_, err := svcs.dataset.ImportExamplesStrict(ctx, "00000000-0000-4000-8000-000000000000", ImportFormatCSV, []byte("prompt\na\n"))
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %T %v, want *NotFoundError", err, err)
	}
}

func TestImportExamplesStrict_ReadsInputData(t *testing.T) {
	t.Run("csv with only an input_data column", func(t *testing.T) {
		ctx, svcs := newEvalSvcs(t)
		dsID := mustCreateDataset(ctx, t, svcs.dataset, "StrictCSVInput")
		withWorkflowEvals(svcs)

		body := "input_data,expected_response\n\"{\"\"orderId\"\":\"\"A1\"\"}\",ok\n,no input\n"
		res, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatCSV, []byte(body))
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if res.Created != 1 || res.Skipped != 1 {
			t.Fatalf("result = %+v, want 1 created and 1 skipped", res)
		}
		rows, err := svcs.examples.ListByDataset(ctx, dsID)
		if err != nil {
			t.Fatal(err)
		}
		if rows[0].Prompt != "" || derefString(rows[0].InputData) != `{"orderId":"A1"}` {
			t.Errorf("row = %q %q, want no prompt and the input", rows[0].Prompt, derefString(rows[0].InputData))
		}
	})

	t.Run("json under either key, null as none", func(t *testing.T) {
		ctx, svcs := newEvalSvcs(t)
		dsID := mustCreateDataset(ctx, t, svcs.dataset, "StrictJSONInput")
		withWorkflowEvals(svcs)

		body := `[{"inputData":{"orderId":"A1"}},{"input_data":{"orderId":"B2"}},{"prompt":"p","inputData":null},{"expectedResponse":"no input"}]`
		res, err := svcs.dataset.ImportExamplesStrict(ctx, dsID, ImportFormatJSON, []byte(body))
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if res.Created != 3 || res.Skipped != 1 {
			t.Fatalf("result = %+v, want 3 created and 1 skipped", res)
		}
		rows, err := svcs.examples.ListByDataset(ctx, dsID)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{derefString(rows[0].InputData), derefString(rows[1].InputData), derefString(rows[2].InputData)}
		if !slices.Equal(got, []string{`{"orderId":"A1"}`, `{"orderId":"B2"}`, ""}) {
			t.Errorf("inputs = %q", got)
		}
	})
}
