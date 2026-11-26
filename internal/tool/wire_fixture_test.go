// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "tool_result_wire", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return data
}

func TestFixtures_UnmarshalWire_RoundtripsPythonOutput(t *testing.T) {
	cases := []struct {
		fixture    string
		wantStatus string
		wantMsg    string
		wantErrCd  string
		wantObjs   int
	}{
		{"ok_with_inline_data", "success", "Generated report", "", 0},
		{"ok_with_text_data_object", "success", "Generated report", "", 1},
		{"ok_with_binary_data_object", "success", "Rendered chart", "", 1},
		{"error_with_code", "error", "File not found: data.csv", "FILE_NOT_FOUND", 0},
		{"ok_with_preview_and_metadata", "success", "Large dataset prepared", "", 1},
		{"ok_with_multiple_data_objects", "success", "Two files", "", 2},
		{"partial_with_error_code", "partial", "Processed 2 of 3 files", "PARTIAL_FAILURE", 0},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			data := loadFixture(t, tc.fixture)
			r, err := UnmarshalWire(data)
			if err != nil {
				t.Fatalf("UnmarshalWire: %v", err)
			}
			if r.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", r.Status, tc.wantStatus)
			}
			if r.Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", r.Message, tc.wantMsg)
			}
			if r.ErrorCode != tc.wantErrCd {
				t.Errorf("error_code = %q, want %q", r.ErrorCode, tc.wantErrCd)
			}
			if len(r.DataObjects) != tc.wantObjs {
				t.Errorf("data_objects len = %d, want %d", len(r.DataObjects), tc.wantObjs)
			}
		})
	}
}

func TestFixtures_BinaryContentDecodes(t *testing.T) {
	data := loadFixture(t, "ok_with_binary_data_object")
	r, err := UnmarshalWire(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.DataObjects) != 1 {
		t.Fatalf("expected 1 data object, got %d", len(r.DataObjects))
	}
	got := r.DataObjects[0].Content
	want := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if len(got) != len(want) {
		t.Fatalf("content length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("content[%d] = 0x%02X, want 0x%02X", i, got[i], want[i])
		}
	}
	if r.DataObjects[0].Disposition != DispositionArtifact {
		t.Errorf("disposition = %q, want %q", r.DataObjects[0].Disposition, DispositionArtifact)
	}
}

func TestFixtures_PreviewAndMetadataStayOnDataObjectNotLLMData(t *testing.T) {
	data := loadFixture(t, "ok_with_preview_and_metadata")
	r, err := UnmarshalWire(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.DataObjects) != 1 {
		t.Fatalf("expected 1 data object")
	}
	do := r.DataObjects[0]
	if do.Preview == "" {
		t.Error("Preview should survive deserialization")
	}
	if do.Description != "Full dataset export" {
		t.Errorf("Description = %q", do.Description)
	}
	if do.Metadata == nil {
		t.Fatal("Metadata should survive deserialization")
	}
	if do.Metadata["source"] != "bigquery" || do.Metadata["query_id"] != "q-42" {
		t.Errorf("Metadata = %v", do.Metadata)
	}
	if do.Disposition != DispositionArtifactWithPreview {
		t.Errorf("Disposition = %q", do.Disposition)
	}
	stats, ok := r.Data["stats"].(map[string]any)
	if !ok {
		t.Fatalf("Data[stats] = %v (%T)", r.Data["stats"], r.Data["stats"])
	}
	if stats["rows"].(float64) != 250000 {
		t.Errorf("Data.stats.rows = %v", stats["rows"])
	}
}

func TestFixtures_UnmarshalToolResultMap_RoundtripsPythonOutput(t *testing.T) {
	cases := []struct {
		fixture    string
		wantStatus string
		wantObjs   int
		wantErrCd  string
	}{
		{"ok_with_inline_data", "success", 0, ""},
		{"ok_with_text_data_object", "success", 1, ""},
		{"ok_with_binary_data_object", "success", 1, ""},
		{"error_with_code", "error", 0, "FILE_NOT_FOUND"},
		{"ok_with_preview_and_metadata", "success", 1, ""},
		{"ok_with_multiple_data_objects", "success", 2, ""},
		{"partial_with_error_code", "partial", 0, "PARTIAL_FAILURE"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			raw := loadFixture(t, tc.fixture)
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			r, ok := UnmarshalToolResultMap(payload)
			if !ok {
				t.Fatal("IsSerializedToolResult should be true on Python-emitted fixture")
			}
			if r.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", r.Status, tc.wantStatus)
			}
			if len(r.DataObjects) != tc.wantObjs {
				t.Errorf("data_objects len = %d, want %d", len(r.DataObjects), tc.wantObjs)
			}
			if r.ErrorCode != tc.wantErrCd {
				t.Errorf("error_code = %q, want %q", r.ErrorCode, tc.wantErrCd)
			}
		})
	}
}

func TestFixtures_GoMarshalWire_IsPythonCompatible(t *testing.T) {
	r := OK("Generated report", WithDataObjects(
		DataObject{
			Name:        "report.csv",
			Content:     []byte("col1,col2\n1,2\n3,4"),
			MIMEType:    "text/csv",
			Disposition: DispositionAuto,
			Description: "Analysis report",
		},
	))

	goBytes, err := MarshalWire(r)
	if err != nil {
		t.Fatal(err)
	}
	var goJSON map[string]any
	if err := json.Unmarshal(goBytes, &goJSON); err != nil {
		t.Fatalf("decode go: %v", err)
	}

	var pyJSON map[string]any
	if err := json.Unmarshal(loadFixture(t, "ok_with_text_data_object"), &pyJSON); err != nil {
		t.Fatalf("decode python: %v", err)
	}

	for _, k := range []string{"_schema", "_schema_version", "status", "message"} {
		if goJSON[k] != pyJSON[k] {
			t.Errorf("%s: go=%v, py=%v", k, goJSON[k], pyJSON[k])
		}
	}

	goObjs := goJSON["data_objects"].([]any)
	pyObjs := pyJSON["data_objects"].([]any)
	if len(goObjs) != len(pyObjs) {
		t.Fatalf("data_objects lengths differ: go=%d, py=%d", len(goObjs), len(pyObjs))
	}
	goObj := goObjs[0].(map[string]any)
	pyObj := pyObjs[0].(map[string]any)
	for _, k := range []string{"name", "content", "is_binary", "mime_type", "disposition", "description"} {
		if goObj[k] != pyObj[k] {
			t.Errorf("data_objects[0].%s: go=%v, py=%v", k, goObj[k], pyObj[k])
		}
	}
	if _, ok := goObj["preview"]; !ok {
		t.Error("Go wire must include preview field (even if null) for Python parity")
	}
	if _, ok := goObj["metadata"]; !ok {
		t.Error("Go wire must include metadata field (even if null) for Python parity")
	}
}

func TestFixtures_GoWireDataObjectEmitsEveryFieldEvenWhenEmpty(t *testing.T) {
	r := OK("test", WithDataObjects(DataObject{
		Name:        "x.txt",
		Content:     []byte("hi"),
		MIMEType:    "text/plain",
		Disposition: DispositionInline,
	}))
	data, err := MarshalWire(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	objs := m["data_objects"].([]any)
	obj := objs[0].(map[string]any)
	pythonParityFields := []string{"name", "content", "is_binary", "mime_type", "disposition", "description", "preview", "metadata"}
	for _, k := range pythonParityFields {
		if _, ok := obj[k]; !ok {
			t.Errorf("Go wire data_objects[0] missing field %q", k)
		}
	}
}
