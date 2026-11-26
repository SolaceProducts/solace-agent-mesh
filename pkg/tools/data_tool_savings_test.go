// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDataTools_ReportTheResultTheyWithheldNetOfThePreview(t *testing.T) {
	for _, tc := range previewToolCases() {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			ctx := t.Context()
			saveDataToolArtifact(t, ctx, facade, "accounts.json", previewTestAccountsJSON(t), mimeTypeJSON)

			result, err := tc.newTool(nil).Execute(ctx, tc.args, toolCtx)
			if err != nil || result.Status != "success" || result.Data["result_truncated"] != true {
				t.Fatalf("Execute: %v, %+v; want a success with a truncated preview", err, result)
			}
			saved, _, err := facade.LoadArtifact(ctx, result.Data["output_filename"].(string), nil)
			if err != nil {
				t.Fatalf("load the saved result: %v", err)
			}
			var compact bytes.Buffer
			if compactErr := json.Compact(&compact, saved); compactErr != nil {
				t.Fatalf("the saved result is not JSON: %v", compactErr)
			}
			if compact.Len() == len(saved) {
				t.Fatal("the saved result is compact; this test wants the indentation the tool writes")
			}
			preview, err := json.Marshal(result.Data["result_preview"])
			if err != nil {
				t.Fatal(err)
			}
			want := int64(compact.Len() - len(preview))
			if result.WithheldBytes != want || want <= 0 {
				t.Errorf("withheld = %d, want the %d-byte compact result net of its %d-byte preview, not the %d indented bytes", result.WithheldBytes, compact.Len(), len(preview), len(saved))
			}

			whole := map[string]any{"max_result_preview_rows": 1_000, "max_result_preview_bytes": 1 << 20}
			result, err = tc.newTool(whole).Execute(ctx, tc.args, toolCtx)
			if err != nil || result.Status != "success" || result.Data["result_truncated"] != false {
				t.Fatalf("Execute with a preview that fits: %v, %+v", err, result)
			}
			if result.WithheldBytes != 0 {
				t.Errorf("withheld = %d with the whole result in the preview, want 0", result.WithheldBytes)
			}
		})
	}
}

func TestWithheldBytes_CountsCSVAtItsOwnSizeAndAnUnencodablePreviewAsNothing(t *testing.T) {
	csv := []byte("a,b\n1,2\n3,4\n")
	if got := withheldBytes(csv, []any{"1"}, true); got != int64(len(csv)-len(`["1"]`)) {
		t.Errorf("csv = %d, want its own size net of the preview", got)
	}
	if got := withheldBytes([]byte(`[1, 2]`), func() {}, true); got != 0 {
		t.Errorf("unencodable preview = %d, want 0", got)
	}
	if got := withheldBytes([]byte(`[1]`), []any{1, 2, 3, 4, 5, 6, 7, 8, 9}, true); got != 0 {
		t.Errorf("a preview larger than the result = %d, want 0, never negative", got)
	}
}
