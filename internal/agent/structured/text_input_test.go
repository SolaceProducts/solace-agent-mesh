// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"reflect"
	"testing"
)

func TestCompileInputSchema_EmptyDeclaresNone(t *testing.T) {
	for _, schema := range []map[string]any{nil, {}} {
		compiled, err := CompileInputSchema(schema)
		if err != nil || compiled != nil {
			t.Errorf("CompileInputSchema(%v) = %v, %v; want no schema and no error", schema, compiled, err)
		}
	}
	if _, err := CompileInputSchema(map[string]any{"type": "strng"}); err == nil {
		t.Error("an uncompilable schema compiled")
	}
}

func TestTextRunInput(t *testing.T) {
	orderSchema, err := CompileInputSchema(map[string]any{
		"type":       "object",
		"required":   []any{"order_id"},
		"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		schema *Schema
		text   string
		want   map[string]any
	}{
		{"no schema keeps a JSON object as text", nil, `{"order_id":"O-1"}`, map[string]any{"text": `{"order_id":"O-1"}`}},
		{"a satisfying object is the input", orderSchema, `{"order_id":"O-1"}`, map[string]any{"order_id": "O-1"}},
		{"prose stays text", orderSchema, "order O-1", map[string]any{"text": "order O-1"}},
		{"an object failing the schema, like its wrapper, is returned so its rejection names its fields", orderSchema, `{"order_id":7}`, map[string]any{"order_id": float64(7)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TextRunInput(tc.text, tc.schema); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("TextRunInput(%q) = %#v, want %#v", tc.text, got, tc.want)
			}
		})
	}
}
