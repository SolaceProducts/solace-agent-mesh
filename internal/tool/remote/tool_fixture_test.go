// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func loadFixtureMap(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join("..", "testdata", "tool_result_wire", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return m
}

func buildResultFromFixture(t *testing.T, fixture string, artifacts []remotetool.CreatedArtifact) *tool.Result {
	t.Helper()
	rt := NewTool(&ToolConfig{ToolName: "fixture_tool"}, nil)
	resp := &InvokeResponse{
		Result: remotetool.InvokeResult{
			ToolResult:       loadFixtureMap(t, fixture),
			CreatedArtifacts: artifacts,
		},
	}
	return rt.buildResult(resp)
}

func TestBuildResult_SchemaMarkedPayload_ExtractsDataObjects(t *testing.T) {
	r := buildResultFromFixture(t, "ok_with_text_data_object", nil)

	if r.Status != tool.StatusSuccess {
		t.Errorf("status = %q", r.Status)
	}
	if r.Message != "Generated report" {
		t.Errorf("message = %q", r.Message)
	}
	if len(r.DataObjects) != 1 {
		t.Fatalf("DataObjects len = %d, want 1 (reconstructed from wire)", len(r.DataObjects))
	}

	do := r.DataObjects[0]
	if do.Name != "report.csv" {
		t.Errorf("DataObject.Name = %q", do.Name)
	}
	if string(do.Content) != "col1,col2\n1,2\n3,4" {
		t.Errorf("DataObject.Content = %q", do.Content)
	}
	if do.MIMEType != "text/csv" {
		t.Errorf("DataObject.MIMEType = %q", do.MIMEType)
	}
	if do.Description != "Analysis report" {
		t.Errorf("DataObject.Description = %q", do.Description)
	}
	if do.Disposition != tool.DispositionAuto {
		t.Errorf("DataObject.Disposition = %q", do.Disposition)
	}

	if _, ok := r.Data["_schema"]; ok {
		t.Error("_schema must not leak into tool.Result.Data (LLM-visible)")
	}
	if _, ok := r.Data["_schema_version"]; ok {
		t.Error("_schema_version must not leak into tool.Result.Data (LLM-visible)")
	}
	if _, ok := r.Data["data_objects"]; ok {
		t.Error("data_objects must not leak into tool.Result.Data (LLM-visible)")
	}
	if _, ok := r.Data["error_code"]; ok {
		t.Error("error_code envelope key must not leak into tool.Result.Data")
	}
}

func TestBuildResult_BinaryDataObject_Base64Decoded(t *testing.T) {
	r := buildResultFromFixture(t, "ok_with_binary_data_object", nil)
	if len(r.DataObjects) != 1 {
		t.Fatalf("DataObjects len = %d", len(r.DataObjects))
	}
	want := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	got := r.DataObjects[0].Content
	if len(got) != len(want) {
		t.Fatalf("content bytes len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("content[%d] = 0x%02X, want 0x%02X", i, got[i], want[i])
		}
	}
	if r.DataObjects[0].Disposition != tool.DispositionArtifact {
		t.Errorf("Disposition = %q", r.DataObjects[0].Disposition)
	}
}

func TestBuildResult_PreviewAndMetadata_Preserved(t *testing.T) {
	r := buildResultFromFixture(t, "ok_with_preview_and_metadata", nil)
	if len(r.DataObjects) != 1 {
		t.Fatalf("DataObjects len = %d", len(r.DataObjects))
	}
	do := r.DataObjects[0]
	if do.Preview == "" {
		t.Error("Preview must survive buildResult when schema-marked")
	}
	if do.Metadata == nil {
		t.Fatal("Metadata must survive buildResult")
	}
	if do.Metadata["source"] != "bigquery" || do.Metadata["query_id"] != "q-42" {
		t.Errorf("Metadata = %v", do.Metadata)
	}
	if do.Disposition != tool.DispositionArtifactWithPreview {
		t.Errorf("Disposition = %q", do.Disposition)
	}
	if stats, ok := r.Data["stats"].(map[string]any); !ok {
		t.Error("inline Data['stats'] missing")
	} else if stats["rows"].(float64) != 250000 {
		t.Errorf("Data.stats.rows = %v", stats["rows"])
	}
}

func TestBuildResult_ErrorWithCode_ExtractsErrorCode(t *testing.T) {
	r := buildResultFromFixture(t, "error_with_code", nil)
	if r.Status != tool.StatusError {
		t.Errorf("status = %q, want error", r.Status)
	}
	if r.ErrorCode != "FILE_NOT_FOUND" {
		t.Errorf("ErrorCode = %q, want FILE_NOT_FOUND", r.ErrorCode)
	}
}

func TestBuildResult_PartialWithErrorCode(t *testing.T) {
	r := buildResultFromFixture(t, "partial_with_error_code", nil)
	if r.Status != tool.StatusPartial {
		t.Errorf("status = %q, want partial", r.Status)
	}
	if r.ErrorCode != "PARTIAL_FAILURE" {
		t.Errorf("ErrorCode = %q", r.ErrorCode)
	}
	if r.Data["succeeded"].(float64) != 2 {
		t.Errorf("Data.succeeded = %v", r.Data["succeeded"])
	}
}

func TestBuildResult_MultipleDataObjects(t *testing.T) {
	r := buildResultFromFixture(t, "ok_with_multiple_data_objects", nil)
	if len(r.DataObjects) != 2 {
		t.Fatalf("DataObjects len = %d, want 2", len(r.DataObjects))
	}
	if r.DataObjects[0].Name != "first.json" || r.DataObjects[1].Name != "second.json" {
		t.Errorf("names = %q, %q", r.DataObjects[0].Name, r.DataObjects[1].Name)
	}
}

func TestBuildResult_SchemaMarkedPayload_CreatedArtifactsAppend(t *testing.T) {
	artifacts := []remotetool.CreatedArtifact{
		{Filename: "extra.pdf", Version: 0, MIMEType: "application/pdf", SizeBytes: 2048},
	}
	r := buildResultFromFixture(t, "ok_with_text_data_object", artifacts)

	if len(r.DataObjects) != 2 {
		t.Fatalf("DataObjects len = %d, want 2 (1 from wire + 1 from CreatedArtifacts)", len(r.DataObjects))
	}
	if r.DataObjects[0].Name != "report.csv" {
		t.Errorf("DataObjects[0].Name = %q", r.DataObjects[0].Name)
	}
	if r.DataObjects[1].Name != "extra.pdf" {
		t.Errorf("DataObjects[1].Name = %q", r.DataObjects[1].Name)
	}
	if r.DataObjects[1].ArtifactRef == nil {
		t.Fatal("DataObjects[1] should have ArtifactRef from CreatedArtifact")
	}
	if r.DataObjects[1].ArtifactRef.Filename != "extra.pdf" {
		t.Errorf("ArtifactRef.Filename = %q", r.DataObjects[1].ArtifactRef.Filename)
	}
}

func TestBuildResult_ErrorWithDataObjects_ExtractsDataObjects(t *testing.T) {
	r := buildResultFromFixture(t, "error_with_data_objects", nil)

	if r.Status != tool.StatusError {
		t.Errorf("status = %q, want error", r.Status)
	}
	if r.ErrorCode != "PARTIAL_FAILURE" {
		t.Errorf("ErrorCode = %q, want PARTIAL_FAILURE", r.ErrorCode)
	}
	if r.Message != "remote tool returned error: Aggregation failed on row 4; partial output saved" {
		t.Errorf("Message = %q", r.Message)
	}
	if len(r.DataObjects) != 2 {
		t.Fatalf("DataObjects len = %d, want 2 (both must survive error path)", len(r.DataObjects))
	}

	if r.DataObjects[0].Name != "partial_rows.csv" {
		t.Errorf("DataObjects[0].Name = %q", r.DataObjects[0].Name)
	}
	if string(r.DataObjects[0].Content) != "error diagnostic\ncol1,col2\n1,2" {
		t.Errorf("DataObjects[0].Content = %q", r.DataObjects[0].Content)
	}

	if r.DataObjects[1].Name != "run.log" {
		t.Errorf("DataObjects[1].Name = %q", r.DataObjects[1].Name)
	}
	if r.DataObjects[1].ArtifactRef == nil {
		t.Fatal("DataObjects[1].ArtifactRef missing — error path dropped it")
	}
	if r.DataObjects[1].ArtifactRef.Filename != "run.log" || r.DataObjects[1].ArtifactRef.Version != 3 {
		t.Errorf("ArtifactRef = %+v", r.DataObjects[1].ArtifactRef)
	}
}

func TestBuildResult_ErrorWithCreatedArtifacts_Append(t *testing.T) {
	artifacts := []remotetool.CreatedArtifact{
		{Filename: "debug.log", Version: 0, MIMEType: "text/plain", SizeBytes: 512},
	}
	r := buildResultFromFixture(t, "error_with_code", artifacts)

	if r.Status != tool.StatusError {
		t.Errorf("status = %q", r.Status)
	}
	if r.ErrorCode != "FILE_NOT_FOUND" {
		t.Errorf("ErrorCode = %q", r.ErrorCode)
	}
	if len(r.DataObjects) != 1 {
		t.Fatalf("DataObjects len = %d, want 1 (CreatedArtifact must append on error)", len(r.DataObjects))
	}
	if r.DataObjects[0].Name != "debug.log" {
		t.Errorf("DataObjects[0].Name = %q", r.DataObjects[0].Name)
	}
	if r.DataObjects[0].ArtifactRef == nil {
		t.Fatal("ArtifactRef missing")
	}
}

func TestBuildResult_LegacyPlainDict_BackwardCompat(t *testing.T) {
	rt := NewTool(&ToolConfig{ToolName: "legacy_tool"}, nil)
	resp := &InvokeResponse{
		Result: remotetool.InvokeResult{
			ToolResult: map[string]any{
				"result": "legacy output",
				"count":  42,
			},
		},
	}
	r := rt.buildResult(resp)
	if r.Status != tool.StatusSuccess {
		t.Errorf("status = %q", r.Status)
	}
	if r.Data["result"] != "legacy output" {
		t.Errorf("Data.result = %v", r.Data["result"])
	}
	if r.Data["count"] != 42 {
		t.Errorf("Data.count = %v", r.Data["count"])
	}
	if len(r.DataObjects) != 0 {
		t.Errorf("DataObjects should be empty for plain dict, got %d", len(r.DataObjects))
	}
}

func TestBuildResult_LegacyPlainDict_WithCreatedArtifacts(t *testing.T) {
	rt := NewTool(&ToolConfig{ToolName: "legacy_tool"}, nil)
	resp := &InvokeResponse{
		Result: remotetool.InvokeResult{
			ToolResult: map[string]any{"status": "success", "message": "done"},
			CreatedArtifacts: []remotetool.CreatedArtifact{
				{Filename: "out.pdf", Version: 0, MIMEType: "application/pdf", SizeBytes: 100},
			},
		},
	}
	r := rt.buildResult(resp)
	if len(r.DataObjects) != 1 {
		t.Fatalf("DataObjects len = %d, want 1", len(r.DataObjects))
	}
	if r.DataObjects[0].ArtifactRef == nil || r.DataObjects[0].ArtifactRef.Filename != "out.pdf" {
		t.Errorf("ArtifactRef = %v", r.DataObjects[0].ArtifactRef)
	}
}
