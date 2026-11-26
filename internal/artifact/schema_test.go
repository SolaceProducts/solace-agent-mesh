// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"encoding/json"
	"testing"
)

func TestInferSchema_CSV(t *testing.T) {
	content := []byte("name,age,city\nAlice,30,NYC\nBob,25,LA\n")
	schema := InferSchema(content, "text/csv", 4, 20)

	if schema["type"] != "text/csv" {
		t.Errorf("expected type text/csv, got %v", schema["type"])
	}
	if schema["inferred"] != true {
		t.Error("expected inferred=true for CSV")
	}
	if _, hasErr := schema["error"]; hasErr {
		t.Errorf("expected no error key, got %v", schema["error"])
	}
	cols, ok := schema["columns"].([]string)
	if !ok {
		t.Fatalf("expected columns to be []string, got %T", schema["columns"])
	}
	if len(cols) != 3 || cols[0] != "name" || cols[1] != "age" || cols[2] != "city" {
		t.Errorf("unexpected columns: %v", cols)
	}
}

func TestInferSchema_CSV_ParseError(t *testing.T) {
	schema := InferSchema([]byte(""), "text/csv", 4, 20)

	if schema["inferred"] != false {
		t.Error("expected inferred=false for empty CSV")
	}
	if schema["error"] == nil {
		t.Error("expected error for empty CSV")
	}
}

func TestInferSchema_JSON_Simple(t *testing.T) {
	content := []byte(`{"name": "Alice", "age": 30, "active": true}`)
	schema := InferSchema(content, "application/json", 4, 20)

	if schema["type"] != "application/json" {
		t.Errorf("expected type application/json, got %v", schema["type"])
	}
	if schema["inferred"] != true {
		t.Error("expected inferred=true")
	}

	structure, ok := schema["structure"].(map[string]any)
	if !ok {
		t.Fatalf("expected structure to be map, got %T", schema["structure"])
	}
	if structure["name"] != "str" {
		t.Errorf("expected name=str, got %v", structure["name"])
	}
	if structure["age"] != "int" {
		t.Errorf("expected age=int, got %v", structure["age"])
	}
	if structure["active"] != "bool" {
		t.Errorf("expected active=bool, got %v", structure["active"])
	}
}

func TestInferSchema_JSON_Nested(t *testing.T) {
	content := []byte(`{"user": {"name": "Alice", "scores": [95.5]}, "count": 1}`)
	schema := InferSchema(content, "application/json", 4, 20)

	structure := schema["structure"].(map[string]any)
	user := structure["user"].(map[string]any)
	if user["name"] != "str" {
		t.Errorf("expected user.name=str, got %v", user["name"])
	}
	scores := user["scores"].([]any)
	if len(scores) != 1 || scores[0] != "float" {
		t.Errorf("expected scores=[float], got %v", scores)
	}
}

func TestInferSchema_JSON_NullValue(t *testing.T) {
	content := []byte(`{"value": null}`)
	schema := InferSchema(content, "application/json", 4, 20)

	structure := schema["structure"].(map[string]any)
	if structure["value"] != "NoneType" {
		t.Errorf("expected NoneType for null, got %v", structure["value"])
	}
}

func TestInferSchema_JSON_EmptyArray(t *testing.T) {
	content := []byte(`{"items": []}`)
	schema := InferSchema(content, "application/json", 4, 20)

	structure := schema["structure"].(map[string]any)
	items := structure["items"].([]any)
	if len(items) != 1 || items[0] != "list" {
		t.Errorf("expected [list] for empty array, got %v", items)
	}
}

func TestInferSchema_JSON_IntegerFloat(t *testing.T) {
	content := []byte(`{"count": 42, "ratio": 3.14}`)
	schema := InferSchema(content, "application/json", 4, 20)

	structure := schema["structure"].(map[string]any)
	if structure["count"] != "int" {
		t.Errorf("expected count=int, got %v", structure["count"])
	}
	if structure["ratio"] != "float" {
		t.Errorf("expected ratio=float, got %v", structure["ratio"])
	}
}

func TestInferSchema_JSON_MaxKeys(t *testing.T) {
	content := []byte(`{"a":1,"b":2,"c":3,"d":4,"e":5}`)
	schema := InferSchema(content, "application/json", 4, 3)

	structure := schema["structure"].(map[string]any)
	if structure["..."] != "2 more keys" {
		t.Errorf("expected truncation marker, got %v", structure["..."])
	}
	if len(structure) != 4 {
		t.Errorf("expected 4 entries (3 keys + truncation), got %d", len(structure))
	}
}

func TestInferSchema_JSON_MaxDepth(t *testing.T) {
	content := []byte(`{"outer": {"inner": "value"}}`)
	schema := InferSchema(content, "application/json", 1, 20)

	structure := schema["structure"].(map[string]any)
	if structure["outer"] != "dict" {
		t.Errorf("expected outer=dict at depth 0, got %v", structure["outer"])
	}
}

func TestInferSchema_JSON_ParseError(t *testing.T) {
	content := []byte(`{invalid json}`)
	schema := InferSchema(content, "application/json", 4, 20)

	if schema["inferred"] != false {
		t.Error("expected inferred=false for invalid JSON")
	}
	errStr, ok := schema["error"].(string)
	if !ok || errStr == "" {
		t.Error("expected non-empty error string")
	}
}

func TestInferSchema_YAML(t *testing.T) {
	content := []byte("name: Alice\nage: 30\nactive: true\n")
	schema := InferSchema(content, "application/yaml", 4, 20)

	if schema["type"] != "application/yaml" {
		t.Errorf("expected type application/yaml, got %v", schema["type"])
	}
	if schema["inferred"] != true {
		t.Error("expected inferred=true")
	}

	structure := schema["structure"].(map[string]any)
	if structure["name"] != "str" {
		t.Errorf("expected name=str, got %v", structure["name"])
	}
	if structure["age"] != "int" {
		t.Errorf("expected age=int, got %v", structure["age"])
	}
}

func TestInferSchema_Binary(t *testing.T) {
	schema := InferSchema([]byte{0xFF, 0xD8}, "image/png", 4, 20)

	if schema["type"] != "image/png" {
		t.Errorf("expected type image/png, got %v", schema["type"])
	}
	if schema["inferred"] != false {
		t.Error("expected inferred=false for binary")
	}
	if _, hasErr := schema["error"]; hasErr {
		t.Errorf("expected no error key, got %v", schema["error"])
	}
}

func TestInferSchema_PythonTypeNames(t *testing.T) {
	content := []byte(`{
		"s": "hello",
		"i": 42,
		"f": 3.14,
		"b": true,
		"n": null,
		"a": [1],
		"d": {"x": 1}
	}`)
	schema := InferSchema(content, "application/json", 4, 20)
	structure := schema["structure"].(map[string]any)

	expected := map[string]any{
		"s": "str",
		"i": "int",
		"f": "float",
		"b": "bool",
		"n": "NoneType",
		"a": []any{"int"},
		"d": map[string]any{"x": "int"},
	}

	gotJSON, _ := json.Marshal(structure)
	wantJSON, _ := json.Marshal(expected)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("structure mismatch:\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestInferSchema_SortedKeys(t *testing.T) {
	content := []byte(`{"zebra": 1, "alpha": 2, "middle": 3}`)
	schema := InferSchema(content, "application/json", 4, 20)

	structure := schema["structure"].(map[string]any)
	data, _ := json.Marshal(structure)
	expected := `{"alpha":"int","middle":"int","zebra":"int"}`
	if string(data) != expected {
		t.Errorf("expected sorted keys %s, got %s", expected, data)
	}
}
