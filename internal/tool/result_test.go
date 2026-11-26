// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestOK(t *testing.T) {
	r := OK("done")
	if r.Status != StatusSuccess || r.Message != "done" {
		t.Errorf("OK() = %+v", r)
	}
}

func TestError(t *testing.T) {
	r := Error("failed", WithErrorCode("NOT_FOUND"))
	if r.Status != StatusError || r.Message != "failed" || r.ErrorCode != "NOT_FOUND" {
		t.Errorf("Error() = %+v", r)
	}
}

func TestOKWithData(t *testing.T) {
	r := OK("done", WithData(map[string]any{"count": 42}))
	if r.Data["count"] != 42 {
		t.Errorf("data = %v", r.Data)
	}
}

func TestOKWithDataObjects(t *testing.T) {
	r := OK("done", WithDataObjects(
		DataObject{Name: "file.csv", Content: []byte("a,b"), MIMEType: "text/csv"},
	))
	if len(r.DataObjects) != 1 || r.DataObjects[0].Name != "file.csv" {
		t.Errorf("data objects = %v", r.DataObjects)
	}
}

func TestMarshalWireBasic(t *testing.T) {
	r := OK("Generated report", WithData(map[string]any{"rows": 100}))
	data, err := MarshalWire(r)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if raw["_schema"] != WireSchema {
		t.Errorf("_schema = %v", raw["_schema"])
	}
	if raw["_schema_version"] != WireSchemaVersion {
		t.Errorf("_schema_version = %v", raw["_schema_version"])
	}
	if raw["status"] != "success" {
		t.Errorf("status = %v", raw["status"])
	}
	if _, ok := raw["error_code"]; !ok {
		t.Error("error_code should be present (null)")
	}
}

func TestMarshalWireTextContent(t *testing.T) {
	r := OK("done", WithDataObjects(
		DataObject{
			Name:        "data.csv",
			Content:     []byte("col1,col2\n1,2"),
			MIMEType:    "text/csv",
			Disposition: DispositionAuto,
		},
	))

	data, err := MarshalWire(r)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	objs := raw["data_objects"].([]any)
	obj := objs[0].(map[string]any)
	if obj["content"] != "col1,col2\n1,2" {
		t.Errorf("content = %v", obj["content"])
	}
	if obj["is_binary"] != false {
		t.Errorf("is_binary = %v", obj["is_binary"])
	}
}

func TestMarshalWireBinaryContent(t *testing.T) {
	binary := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	r := OK("done", WithDataObjects(
		DataObject{Name: "image.jpg", Content: binary, MIMEType: "image/jpeg"},
	))

	data, err := MarshalWire(r)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	objs := raw["data_objects"].([]any)
	obj := objs[0].(map[string]any)
	if obj["is_binary"] != true {
		t.Errorf("is_binary = %v", obj["is_binary"])
	}
}

func TestMarshalWireArtifactRef(t *testing.T) {
	r := OK("done", WithDataObjects(
		DataObject{
			Name:        "report.csv",
			ArtifactRef: &ArtifactRef{Filename: "report.csv", Version: 3, SizeBytes: 12345},
			MIMEType:    "text/csv",
			Disposition: DispositionArtifactWithPreview,
			Preview:     "col1,col2\n1,2\n...",
		},
	))

	data, err := MarshalWire(r)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	objs := raw["data_objects"].([]any)
	obj := objs[0].(map[string]any)
	if obj["content"] != nil {
		t.Errorf("content should be nil with artifact_ref, got %v", obj["content"])
	}
	ref := obj["artifact_ref"].(map[string]any)
	if ref["filename"] != "report.csv" {
		t.Errorf("filename = %v", ref["filename"])
	}
}

func TestUnmarshalWireToolResult(t *testing.T) {
	pyJSON := `{
		"_schema": "ToolResult",
		"_schema_version": "1.0",
		"status": "success",
		"message": "Generated report",
		"data": {"row_count": 1000},
		"data_objects": [
			{
				"name": "report.csv",
				"content": "col1,col2\n1,2\n3,4",
				"is_binary": false,
				"artifact_ref": null,
				"mime_type": "text/csv",
				"disposition": "auto",
				"description": "Analysis report",
				"preview": null,
				"metadata": null
			}
		],
		"error_code": null
	}`

	r, err := UnmarshalWire([]byte(pyJSON))
	if err != nil {
		t.Fatal(err)
	}

	if r.Status != StatusSuccess || r.Message != "Generated report" {
		t.Errorf("result = %+v", r)
	}
	if r.Data["row_count"].(float64) != 1000 {
		t.Errorf("data = %v", r.Data)
	}
	if len(r.DataObjects) != 1 {
		t.Fatalf("data_objects length = %d", len(r.DataObjects))
	}

	do := r.DataObjects[0]
	if do.Name != "report.csv" {
		t.Errorf("name = %q", do.Name)
	}
	if string(do.Content) != "col1,col2\n1,2\n3,4" {
		t.Errorf("content = %q", do.Content)
	}
	if do.Description != "Analysis report" {
		t.Errorf("description = %q", do.Description)
	}
	if do.ArtifactRef != nil {
		t.Errorf("artifact_ref should be nil")
	}
}

func TestUnmarshalWireArtifactRef(t *testing.T) {
	pyJSON := `{
		"_schema": "ToolResult",
		"_schema_version": "1.0",
		"status": "success",
		"message": "Generated report",
		"data_objects": [
			{
				"name": "report.csv",
				"content": null,
				"is_binary": false,
				"artifact_ref": {"filename": "report.csv", "version": 3, "size_bytes": 245678},
				"mime_type": "text/csv",
				"disposition": "artifact_with_preview",
				"description": "Analysis report",
				"preview": "col1,col2\n1,2\n...",
				"metadata": null
			}
		],
		"error_code": null
	}`

	r, err := UnmarshalWire([]byte(pyJSON))
	if err != nil {
		t.Fatal(err)
	}

	do := r.DataObjects[0]
	if do.Content != nil {
		t.Errorf("content should be nil, got %d bytes", len(do.Content))
	}
	if do.ArtifactRef == nil {
		t.Fatal("artifact_ref is nil")
	}
	if do.ArtifactRef.Filename != "report.csv" || do.ArtifactRef.Version != 3 {
		t.Errorf("artifact_ref = %+v", do.ArtifactRef)
	}
	if do.Preview != "col1,col2\n1,2\n..." {
		t.Errorf("preview = %q", do.Preview)
	}
}

func TestUnmarshalWirePlainDict(t *testing.T) {
	pyJSON := `{"result": "some data", "count": 42}`

	r, err := UnmarshalWire([]byte(pyJSON))
	if err != nil {
		t.Fatal(err)
	}

	if r.Status != StatusSuccess {
		t.Errorf("status = %q", r.Status)
	}
	if r.Data["result"] != "some data" {
		t.Errorf("data = %v", r.Data)
	}
	if r.Data["count"].(float64) != 42 {
		t.Errorf("count = %v", r.Data["count"])
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	original := OK("done",
		WithData(map[string]any{"key": "value"}),
		WithDataObjects(
			DataObject{
				Name:        "file.txt",
				Content:     []byte("hello world"),
				MIMEType:    "text/plain",
				Disposition: DispositionInline,
				Description: "A text file",
			},
		),
	)

	data, err := MarshalWire(original)
	if err != nil {
		t.Fatal(err)
	}

	got, err := UnmarshalWire(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Status != original.Status || got.Message != original.Message {
		t.Errorf("header mismatch: got %+v", got)
	}
	if len(got.DataObjects) != 1 {
		t.Fatalf("data objects length = %d", len(got.DataObjects))
	}
	if string(got.DataObjects[0].Content) != "hello world" {
		t.Errorf("content = %q", got.DataObjects[0].Content)
	}
}

func TestInlineMaxBytesRoundTrip(t *testing.T) {
	original := OK("done", WithDataObjects(
		DataObject{
			Name:           "big.txt",
			Content:        []byte("payload"),
			MIMEType:       "text/plain",
			Disposition:    DispositionAuto,
			InlineMaxBytes: 50000,
		},
	))

	data, err := MarshalWire(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalWire(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DataObjects) != 1 || got.DataObjects[0].InlineMaxBytes != 50000 {
		t.Fatalf("InlineMaxBytes did not round-trip via UnmarshalWire: %+v", got.DataObjects)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	mapped, ok := UnmarshalToolResultMap(payload)
	if !ok {
		t.Fatal("UnmarshalToolResultMap did not recognize the schema marker")
	}
	if len(mapped.DataObjects) != 1 || mapped.DataObjects[0].InlineMaxBytes != 50000 {
		t.Fatalf("InlineMaxBytes did not round-trip via UnmarshalToolResultMap: %+v", mapped.DataObjects)
	}
}

func TestTagsRoundTrip(t *testing.T) {
	original := OK("done", WithDataObjects(
		DataObject{
			Name:        "deck-spec.yaml",
			Content:     []byte("- layout: title"),
			MIMEType:    "application/yaml",
			Disposition: DispositionArtifact,
			Tags:        []string{"__working"},
		},
	))

	data, err := MarshalWire(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalWire(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DataObjects) != 1 || !slices.Equal(got.DataObjects[0].Tags, []string{"__working"}) {
		t.Fatalf("Tags did not round-trip via UnmarshalWire: %+v", got.DataObjects)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	mapped, ok := UnmarshalToolResultMap(payload)
	if !ok {
		t.Fatal("UnmarshalToolResultMap did not recognize the schema marker")
	}
	if len(mapped.DataObjects) != 1 || !slices.Equal(mapped.DataObjects[0].Tags, []string{"__working"}) {
		t.Fatalf("Tags did not round-trip via UnmarshalToolResultMap: %+v", mapped.DataObjects)
	}

	obj := payload["data_objects"].([]any)[0].(map[string]any)
	if _, ok := obj["tags"]; !ok {
		t.Errorf("marshaled wire object missing literal %q key: %v", "tags", obj)
	}
}

func TestArtifactAsText(t *testing.T) {
	a := &Artifact{Content: []byte("hello world")}
	if a.AsText() != "hello world" {
		t.Errorf("AsText() = %q", a.AsText())
	}
}
