// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"reflect"
	"testing"
)

func TestJSONSchemaMap(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"type": "object", "anyOf": []any{}}
	cases := []struct {
		name   string
		schema *JSONSchema
		want   map[string]any
	}{
		{"nil is unknown", nil, nil},
		{"raw passes through", &JSONSchema{Type: "object", Raw: raw}, raw},
		{"empty schema keeps an empty properties object", &JSONSchema{Type: "object"}, map[string]any{"type": "object", "properties": map[string]any{}}},
		{"properties and required are rendered", &JSONSchema{Type: "object", Properties: map[string]any{"a": map[string]any{"type": "string"}}, Required: []string{"a"}},
			map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []string{"a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.schema.Map(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Map() = %v, want %v", got, tc.want)
			}
		})
	}
}
