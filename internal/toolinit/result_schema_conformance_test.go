// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var nilFactoryNames = []string{"sub_task"}

func TestBuiltinToolFactories_AllDeclareAuthoredSchemas(t *testing.T) {
	for name, bt := range constructedBuiltins(t) {
		if !tool.HasAuthoredSchemas(bt) {
			t.Errorf("%s: missing the authoredSchemas marker — embed it so the workflow "+
				"validators reject an undeclared name instead of warning", name)
		}
		if tool.ResultSchemaOf(bt) == nil {
			t.Errorf("%s (%T): carries the authored marker but declares no ResultSchema — "+
				"declare one, or emptyResultSchema() if the tool returns no Result.Data", name, bt)
		}
	}
}

func TestBuiltinToolFactories_ResultSchemasAreWellFormed(t *testing.T) {
	for name, bt := range constructedBuiltins(t) {
		schema := tool.ResultSchemaOf(bt)
		if schema == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if schema.Type != "object" {
				t.Errorf("Type = %q, want \"object\" (Result.Data is always a map)", schema.Type)
			}
			if schema.Properties == nil {
				t.Fatal("Properties = nil; declare emptyResultSchema() for a tool that returns no Data")
			}
			for _, req := range schema.Required {
				if _, ok := schema.Properties[req]; !ok {
					t.Errorf("required field %q is not present in Properties", req)
				}
			}
			for field, raw := range schema.Properties {
				prop, ok := raw.(map[string]any)
				if !ok {
					t.Errorf("property %q = %T, want map[string]any", field, raw)
					continue
				}
				if raw, present := prop["type"]; present {
					if typ, _ := raw.(string); typ == "" {
						t.Errorf("property %q declares an empty type; omit it entirely to mean \"any\"", field)
					}
				}
				if desc, _ := prop["description"].(string); desc == "" {
					t.Errorf("property %q declares no description", field)
				}
			}
		})
	}
}

func TestBuiltinToolFactories_NilFactoriesAreDeclared(t *testing.T) {
	var got []string
	for name, factory := range builtinToolFactories {
		if factory(map[string]any{}) == nil {
			got = append(got, name)
		}
	}
	slices.Sort(got)
	want := slices.Clone(nilFactoryNames)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("factories returning nil = %v, want %v — a new nil factory silently "+
			"exempts its tool from the schema checks, so add it here with the reason "+
			"or make the factory build something", got, want)
	}
}

func constructedBuiltins(t *testing.T) map[string]tool.BuiltinTool {
	t.Helper()

	if len(builtinToolFactories) < 50 {
		t.Fatalf("builtinToolFactories holds %d entries; expected the full built-in set, so "+
			"these checks are no longer covering what they were written for", len(builtinToolFactories))
	}

	out := make(map[string]tool.BuiltinTool, len(builtinToolFactories))
	for name, factory := range builtinToolFactories {
		if bt := factory(map[string]any{}); bt != nil {
			out[name] = bt
		}
	}
	return out
}
