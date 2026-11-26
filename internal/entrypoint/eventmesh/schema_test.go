// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"testing"
)

func TestValidatePayload_NilSchema(t *testing.T) {
	if err := ValidatePayload("anything", nil); err != nil {
		t.Errorf("nil schema should pass: %v", err)
	}
}

func TestValidatePayload_EmptySchema(t *testing.T) {
	if err := ValidatePayload("anything", map[string]any{}); err != nil {
		t.Errorf("empty schema should pass: %v", err)
	}
}

func TestValidatePayload_TypeObject(t *testing.T) {
	schema := map[string]any{"type": "object"}

	if err := ValidatePayload(map[string]any{"a": 1}, schema); err != nil {
		t.Errorf("object should pass: %v", err)
	}

	if err := ValidatePayload("string", schema); err == nil {
		t.Error("string should fail object type check")
	}
}

func TestValidatePayload_TypeString(t *testing.T) {
	schema := map[string]any{"type": "string"}

	if err := ValidatePayload("hello", schema); err != nil {
		t.Errorf("string should pass: %v", err)
	}

	if err := ValidatePayload(42, schema); err == nil {
		t.Error("int should fail string type check")
	}
}

func TestValidatePayload_TypeArray(t *testing.T) {
	schema := map[string]any{"type": "array"}

	if err := ValidatePayload([]any{1, 2}, schema); err != nil {
		t.Errorf("array should pass: %v", err)
	}

	if err := ValidatePayload("not-array", schema); err == nil {
		t.Error("string should fail array type check")
	}
}

func TestValidatePayload_TypeNumber(t *testing.T) {
	schema := map[string]any{"type": "number"}

	if err := ValidatePayload(3.14, schema); err != nil {
		t.Errorf("float64 should pass: %v", err)
	}
	if err := ValidatePayload(42, schema); err != nil {
		t.Errorf("int should pass: %v", err)
	}

	if err := ValidatePayload("nan", schema); err == nil {
		t.Error("string should fail number type check")
	}
}

func TestValidatePayload_TypeBoolean(t *testing.T) {
	schema := map[string]any{"type": "boolean"}

	if err := ValidatePayload(true, schema); err != nil {
		t.Errorf("bool should pass: %v", err)
	}

	if err := ValidatePayload(1, schema); err == nil {
		t.Error("int should fail boolean type check")
	}
}

func TestValidatePayload_RequiredFields(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"name", "age"},
	}

	data := map[string]any{"name": "alice", "age": 30, "extra": true}
	if err := ValidatePayload(data, schema); err != nil {
		t.Errorf("should pass with all required: %v", err)
	}

	incomplete := map[string]any{"name": "bob"}
	if err := ValidatePayload(incomplete, schema); err == nil {
		t.Error("should fail with missing required field")
	}
}
