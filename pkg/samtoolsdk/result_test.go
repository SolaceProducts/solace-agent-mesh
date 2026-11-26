// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"encoding/json"
	"testing"
)

func TestOK(t *testing.T) {
	r := OK("done")
	if r.Status != StatusSuccess {
		t.Errorf("status: got %q, want %q", r.Status, StatusSuccess)
	}
	if r.Message != "done" {
		t.Errorf("message: got %q, want 'done'", r.Message)
	}
}

func TestError(t *testing.T) {
	r := Error("failed", WithErrorCode("NOT_FOUND"))
	if r.Status != StatusError {
		t.Errorf("status: got %q, want %q", r.Status, StatusError)
	}
	if r.ErrorCode != "NOT_FOUND" {
		t.Errorf("error_code: got %q, want 'NOT_FOUND'", r.ErrorCode)
	}
}

func TestPartial(t *testing.T) {
	r := Partial("half done")
	if r.Status != StatusPartial {
		t.Errorf("status: got %q, want %q", r.Status, StatusPartial)
	}
}

func TestWithData(t *testing.T) {
	r := OK("done", WithData(map[string]any{"count": 42}))
	if r.Data["count"] != 42 {
		t.Errorf("data[count]: got %v, want 42", r.Data["count"])
	}
}

func TestWithDataObjects(t *testing.T) {
	do := DataObject{
		Name:        "output.txt",
		Content:     []byte("hello"),
		MIMEType:    "text/plain",
		Disposition: DispositionArtifact,
	}
	r := OK("done", WithDataObjects(do))
	if len(r.DataObjects) != 1 {
		t.Fatalf("expected 1 data object, got %d", len(r.DataObjects))
	}
	if r.DataObjects[0].Name != "output.txt" {
		t.Errorf("data_object name: got %q", r.DataObjects[0].Name)
	}
}

func TestMarshalWire_TextContent(t *testing.T) {
	r := OK("done", WithDataObjects(DataObject{
		Name:        "file.txt",
		Content:     []byte("hello world"),
		MIMEType:    "text/plain",
		Disposition: DispositionInline,
		Description: "A text file",
	}))

	data, err := marshalWire(r)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if wire["_schema"] != "ToolResult" {
		t.Errorf("_schema: got %v", wire["_schema"])
	}
	if wire["_schema_version"] != "1.0" {
		t.Errorf("_schema_version: got %v", wire["_schema_version"])
	}
	if wire["status"] != "success" {
		t.Errorf("status: got %v", wire["status"])
	}

	objects := wire["data_objects"].([]any)
	if len(objects) != 1 {
		t.Fatalf("expected 1 data_object, got %d", len(objects))
	}
	obj := objects[0].(map[string]any)
	if obj["content"] != "hello world" {
		t.Errorf("content: got %v", obj["content"])
	}
	if obj["is_binary"] != false {
		t.Errorf("is_binary: got %v, want false", obj["is_binary"])
	}
	if obj["disposition"] != "inline" {
		t.Errorf("disposition: got %v", obj["disposition"])
	}
}

func TestMarshalWire_InlineMaxBytes(t *testing.T) {
	r := OK("done", WithDataObjects(DataObject{
		Name:           "big.txt",
		Content:        []byte("payload"),
		MIMEType:       "text/plain",
		Disposition:    DispositionAuto,
		InlineMaxBytes: 50000,
	}))

	data, err := marshalWire(r)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}
	var wire map[string]any
	if err = json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	obj := wire["data_objects"].([]any)[0].(map[string]any)
	if obj["inline_max_bytes"] != float64(50000) {
		t.Errorf("inline_max_bytes: got %v, want 50000", obj["inline_max_bytes"])
	}

	r2 := OK("done", WithDataObjects(DataObject{Name: "x.txt", Content: []byte("y"), MIMEType: "text/plain"}))
	data2, err := marshalWire(r2)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}
	var wire2 map[string]any
	if err = json.Unmarshal(data2, &wire2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	obj2 := wire2["data_objects"].([]any)[0].(map[string]any)
	if _, ok := obj2["inline_max_bytes"]; ok {
		t.Errorf("inline_max_bytes should be omitted when zero, got %v", obj2["inline_max_bytes"])
	}
}

func TestMarshalWire_Tags(t *testing.T) {
	r := OK("done", WithDataObjects(DataObject{
		Name:        "deck-spec.yaml",
		Content:     []byte("- layout: title"),
		MIMEType:    "application/yaml",
		Disposition: DispositionArtifact,
		Tags:        []string{TagWorking},
	}))

	data, err := marshalWire(r)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}
	var wire map[string]any
	if err = json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	obj := wire["data_objects"].([]any)[0].(map[string]any)
	tags, ok := obj["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != TagWorking {
		t.Errorf("tags: got %v, want [%s]", obj["tags"], TagWorking)
	}

	r2 := OK("done", WithDataObjects(DataObject{Name: "x.txt", Content: []byte("y"), MIMEType: "text/plain"}))
	data2, err := marshalWire(r2)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}
	var wire2 map[string]any
	if err = json.Unmarshal(data2, &wire2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	obj2 := wire2["data_objects"].([]any)[0].(map[string]any)
	if _, ok := obj2["tags"]; ok {
		t.Errorf("tags should be omitted when empty, got %v", obj2["tags"])
	}
}

func TestMarshalWire_BinaryContent(t *testing.T) {
	r := OK("done", WithDataObjects(DataObject{
		Name:        "image.png",
		Content:     []byte{0x89, 0x50, 0x4E, 0x47, 0xFF, 0xFE},
		MIMEType:    "image/png",
		Disposition: DispositionArtifact,
	}))

	data, err := marshalWire(r)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	objects := wire["data_objects"].([]any)
	obj := objects[0].(map[string]any)
	if obj["is_binary"] != true {
		t.Errorf("is_binary: got %v, want true", obj["is_binary"])
	}
}

func TestPending(t *testing.T) {
	r := Pending("Waiting for approval...")
	if r.Status != StatusPending {
		t.Errorf("status: got %q, want %q", r.Status, StatusPending)
	}
	if r.Message != "Waiting for approval..." {
		t.Errorf("message: got %q", r.Message)
	}
}

func TestPendingWithData(t *testing.T) {
	r := Pending("Waiting...", WithData(map[string]any{
		"hil_request_id": "hil-123",
		"pending_kind":   "hil_approval",
	}))
	if r.Status != StatusPending {
		t.Errorf("status: got %q, want %q", r.Status, StatusPending)
	}
	if r.Data["hil_request_id"] != "hil-123" {
		t.Errorf("hil_request_id: got %v", r.Data["hil_request_id"])
	}
	if r.Data["pending_kind"] != "hil_approval" {
		t.Errorf("pending_kind: got %v", r.Data["pending_kind"])
	}
}

func TestMarshalWire_PendingStatus(t *testing.T) {
	r := Pending("Waiting...", WithData(map[string]any{
		"hil_request_id": "hil-abc",
	}))
	data, err := marshalWire(r)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if wire["status"] != "pending" {
		t.Errorf("status: got %v, want pending", wire["status"])
	}
	if wire["_schema"] != "ToolResult" {
		t.Errorf("_schema: got %v, want ToolResult", wire["_schema"])
	}
	wireData, ok := wire["data"].(map[string]any)
	if !ok {
		t.Fatal("data not present in wire format")
	}
	if wireData["hil_request_id"] != "hil-abc" {
		t.Errorf("data.hil_request_id: got %v", wireData["hil_request_id"])
	}
}

func TestMarshalWire_NilErrorCode(t *testing.T) {
	r := OK("done")
	data, err := marshalWire(r)
	if err != nil {
		t.Fatalf("marshalWire: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if wire["error_code"] != nil {
		t.Errorf("error_code: got %v, want nil", wire["error_code"])
	}
}
