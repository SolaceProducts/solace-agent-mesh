// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func compileToolSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	content, err := SchemaContent("tool")
	if err != nil {
		t.Fatalf("SchemaContent(tool): %v", err)
	}
	var parsed any
	if parseErr := yaml.Unmarshal(content, &parsed); parseErr != nil {
		t.Fatalf("parse tool schema: %v", parseErr)
	}
	compiler := jsonschema.NewCompiler()
	if addErr := compiler.AddResource("tool.json", normalizeForJSON(parsed)); addErr != nil {
		t.Fatalf("add tool schema: %v", addErr)
	}
	compiled, err := compiler.Compile("tool.json")
	if err != nil {
		t.Fatalf("compile tool schema: %v", err)
	}
	return compiled
}

func effectOverrideEntries(effect any) map[string]map[string]any {
	return map[string]map[string]any{
		"builtin-group": {"tool_type": "builtin-group", "group_name": "artifact_management", "effect": effect},
		"builtin":       {"tool_type": "builtin", "tool_name": "get_current_time", "effect": effect},
		"mcp": {
			"tool_type": "mcp", "effect": effect,
			"connection_params": map[string]any{"type": "streamable-http", "url": "https://mcp.example.com/mcp"},
		},
		"openapi": {"tool_type": "openapi", "specification_url": "https://api.example.com/openapi.json", "effect": effect},
	}
}

func TestEffectOverrideAcceptsOnlySideEffectingOnEveryEntryType(t *testing.T) {
	t.Parallel()
	toolSchema := compileToolSchema(t)
	for _, tc := range []struct {
		name   string
		effect any
		valid  bool
	}{
		{"side_effecting", "side_effecting", true},
		{"read_only", "read_only", false},
		{"an unknown value", "maybe", false},
		{"a boolean", true, false},
	} {
		for entryType, entry := range effectOverrideEntries(tc.effect) {
			t.Run(tc.name+"/"+entryType, func(t *testing.T) {
				t.Parallel()
				var instance any
				doc, err := json.Marshal(entry)
				if err != nil {
					t.Fatalf("marshal %s entry: %v", entryType, err)
				}
				if unmarshalErr := json.Unmarshal(doc, &instance); unmarshalErr != nil {
					t.Fatalf("unmarshal %s entry: %v", entryType, unmarshalErr)
				}
				if validationErr := toolSchema.Validate(instance); (validationErr == nil) != tc.valid {
					t.Errorf("effect %v on a %s entry: valid = %v, want %v; error: %v", tc.effect, entryType, validationErr == nil, tc.valid, validationErr)
				}
			})
		}
	}
}
