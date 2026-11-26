// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildStructuredInvocationPart_BothSchemas(t *testing.T) {
	dir := t.TempDir()

	inputSchema := `{"type": "object", "properties": {"name": {"type": "string"}}}`
	outputSchema := `{"type": "object", "properties": {"result": {"type": "integer"}}}`

	inputFile := filepath.Join(dir, "input.json")
	outputFile := filepath.Join(dir, "output.json")
	if err := os.WriteFile(inputFile, []byte(inputSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputFile, []byte(outputSchema), 0o644); err != nil {
		t.Fatal(err)
	}

	part, err := buildStructuredInvocationPart(inputFile, outputFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if part["kind"] != "data" {
		t.Errorf("kind = %q, want data", part["kind"])
	}

	data, ok := part["data"].(map[string]any)
	if !ok {
		t.Fatal("data field is not map[string]any")
	}

	if data["type"] != "structured_invocation_request" {
		t.Errorf("type = %q, want structured_invocation_request", data["type"])
	}
	if data["workflow_name"] != "cli-test" {
		t.Errorf("workflow_name = %q, want cli-test", data["workflow_name"])
	}

	nodeID, _ := data["node_id"].(string)
	if !strings.HasPrefix(nodeID, "cli-") {
		t.Errorf("node_id = %q, want cli-* prefix", nodeID)
	}

	inSchema, ok := data["input_schema"].(map[string]any)
	if !ok {
		t.Fatal("input_schema missing or wrong type")
	}
	if inSchema["type"] != "object" {
		t.Errorf("input_schema.type = %v, want object", inSchema["type"])
	}

	outSchema, ok := data["output_schema"].(map[string]any)
	if !ok {
		t.Fatal("output_schema missing or wrong type")
	}
	if outSchema["type"] != "object" {
		t.Errorf("output_schema.type = %v, want object", outSchema["type"])
	}
}

func TestBuildStructuredInvocationPart_OutputOnly(t *testing.T) {
	dir := t.TempDir()
	outputFile := filepath.Join(dir, "output.json")
	if err := os.WriteFile(outputFile, []byte(`{"type": "string"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	part, err := buildStructuredInvocationPart("", outputFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := part["data"].(map[string]any)
	if _, ok := data["input_schema"]; ok {
		t.Error("input_schema should not be present when no input file given")
	}
	if _, ok := data["output_schema"]; !ok {
		t.Error("output_schema should be present")
	}
}

func TestBuildStructuredInvocationPart_InputOnly(t *testing.T) {
	dir := t.TempDir()
	inputFile := filepath.Join(dir, "input.json")
	if err := os.WriteFile(inputFile, []byte(`{"type": "array"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	part, err := buildStructuredInvocationPart(inputFile, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := part["data"].(map[string]any)
	if _, ok := data["input_schema"]; !ok {
		t.Error("input_schema should be present")
	}
	if _, ok := data["output_schema"]; ok {
		t.Error("output_schema should not be present when no output file given")
	}
}

func TestBuildStructuredInvocationPart_MissingFile(t *testing.T) {
	_, err := buildStructuredInvocationPart("/nonexistent/path.json", "")
	if err == nil {
		t.Error("expected error for missing input file")
	}

	_, err = buildStructuredInvocationPart("", "/nonexistent/path.json")
	if err == nil {
		t.Error("expected error for missing output file")
	}
}

func TestBuildStructuredInvocationPart_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	badFile := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badFile, []byte("not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := buildStructuredInvocationPart(badFile, "")
	if err == nil {
		t.Error("expected error for invalid input JSON")
	}

	_, err = buildStructuredInvocationPart("", badFile)
	if err == nil {
		t.Error("expected error for invalid output JSON")
	}
}

func TestBuildStructuredInvocationPart_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	schemaFile := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaFile, []byte(`{"type": "object", "required": ["x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	part, err := buildStructuredInvocationPart(schemaFile, schemaFile)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(part)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	data := decoded["data"].(map[string]any)
	if data["type"] != "structured_invocation_request" {
		t.Error("type lost after round-trip")
	}
}
