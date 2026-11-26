// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalize_MapAnyAny(t *testing.T) {
	input := map[string]any{
		"outer": map[any]any{
			"key": "val",
		},
	}
	got := normalizeForJSON(input)
	want := map[string]any{
		"outer": map[string]any{
			"key": "val",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeForJSON(map[any]any) =\n  %#v\nwant\n  %#v", got, want)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Errorf("result is not JSON-marshalable: %v", err)
	}
}

func TestNormalize_MapStringAny(t *testing.T) {
	input := map[string]any{
		"key": "val",
		"num": 42,
	}
	got := normalizeForJSON(input)
	want := map[string]any{
		"key": "val",
		"num": 42,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeForJSON(map[string]any) =\n  %#v\nwant\n  %#v", got, want)
	}
}

func TestNormalize_NestedMaps(t *testing.T) {
	input := map[string]any{
		"level1": map[any]any{
			"level2": map[any]any{
				"level3": "deep-value",
			},
		},
	}
	got := normalizeForJSON(input)
	want := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"level3": "deep-value",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeForJSON(nested) =\n  %#v\nwant\n  %#v", got, want)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Errorf("nested result is not JSON-marshalable: %v", err)
	}
}

func TestNormalize_SliceWithMaps(t *testing.T) {
	input := map[string]any{
		"items": []any{
			map[any]any{"name": "alice"},
			map[any]any{"name": "bob"},
			"plain-string",
		},
	}
	got := normalizeForJSON(input)
	want := map[string]any{
		"items": []any{
			map[string]any{"name": "alice"},
			map[string]any{"name": "bob"},
			"plain-string",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeForJSON(slice with maps) =\n  %#v\nwant\n  %#v", got, want)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Errorf("slice result is not JSON-marshalable: %v", err)
	}
}

func TestNormalize_Primitives(t *testing.T) {
	input := map[string]any{
		"str":   "hello",
		"int":   42,
		"bool":  true,
		"float": 3.14,
		"nil":   nil,
	}
	got := normalizeForJSON(input)
	if !reflect.DeepEqual(got, input) {
		t.Errorf("normalizeForJSON(primitives) =\n  %#v\nwant\n  %#v", got, input)
	}
}

func TestNormalize_NonStringKeys(t *testing.T) {
	input := map[string]any{
		"mixed": map[any]any{
			42:  "int-key",
			nil: "nil-key",
		},
	}
	got := normalizeForJSON(input)

	inner, ok := got["mixed"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any for 'mixed', got %T", got["mixed"])
	}
	if inner["42"] != "int-key" {
		t.Errorf("inner[\"42\"] = %v, want \"int-key\"", inner["42"])
	}
	if inner["<nil>"] != "nil-key" {
		t.Errorf("inner[\"<nil>\"] = %v, want \"nil-key\"", inner["<nil>"])
	}
	if _, err := json.Marshal(got); err != nil {
		t.Errorf("result with non-string keys is not JSON-marshalable: %v", err)
	}
}

func TestNormalize_EmptyMap(t *testing.T) {
	got := normalizeForJSON(map[string]any{})
	if got == nil {
		t.Fatal("expected non-nil result for empty map")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %#v", got)
	}
}

func TestNormalize_NilMap(t *testing.T) {
	got := normalizeForJSON(nil)
	if got == nil {
		t.Fatal("expected non-nil result for nil input")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %#v", got)
	}
	got["test"] = "value"
	if got["test"] != "value" {
		t.Error("returned map should be writable")
	}
}
