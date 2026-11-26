// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"
)

func TestGenerateConnectorJSONSchema(t *testing.T) {
	t.Parallel()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("LoadConnectorTypeDefinitions() error: %v", err)
	}

	schemaJSON, err := GenerateConnectorJSONSchema(defs)
	if err != nil {
		t.Fatalf("GenerateConnectorJSONSchema() error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(schemaJSON, &parsed); err != nil {
		t.Fatalf("generated schema is not valid JSON: %v", err)
	}

	if parsed["$schema"] == nil {
		t.Error("missing $schema")
	}
	if parsed["title"] != "SAM Connector Config" {
		t.Errorf("title = %v, want %q", parsed["title"], "SAM Connector Config")
	}
	if parsed["type"] != "object" {
		t.Errorf("type = %v, want %q", parsed["type"], "object")
	}

	required, ok := parsed["required"].([]any)
	if !ok {
		t.Fatal("missing or invalid 'required' field")
	}
	requiredSet := make(map[string]bool)
	for _, r := range required {
		requiredSet[r.(string)] = true
	}
	for _, field := range []string{"name", "type", "subtype", "values"} {
		if !requiredSet[field] {
			t.Errorf("missing required field: %s", field)
		}
	}

	oneOf, ok := parsed["oneOf"].([]any)
	if !ok {
		t.Fatal("missing or invalid 'oneOf'")
	}
	if len(oneOf) != 19 {
		t.Errorf("expected 19 oneOf branches, got %d", len(oneOf))
	}

	props, _ := parsed["properties"].(map[string]any)
	typeProp, _ := props["type"].(map[string]any)
	typeEnum, _ := typeProp["enum"].([]any)
	if len(typeEnum) != 11 {
		t.Errorf("expected 11 type enum values, got %d", len(typeEnum))
	}
}

func TestGenerateConnectorJSONSchemaFromEmbedded(t *testing.T) {
	t.Parallel()
	data, err := GenerateConnectorJSONSchemaFromEmbedded()
	if err != nil {
		t.Fatalf("GenerateConnectorJSONSchemaFromEmbedded() error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("generated schema is empty")
	}
}

func TestGenerateConnectorJSONSchema_ValuesAreClosed(t *testing.T) {
	t.Parallel()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("LoadConnectorTypeDefinitions() error: %v", err)
	}
	for _, def := range defs {
		for _, st := range def.Subtypes {
			branch := buildOneOfBranch(def.Type, st)
			values := branch["properties"].(map[string]any)["values"].(map[string]any)
			if values["additionalProperties"] != false {
				t.Errorf("%s/%s: values is not closed: additionalProperties = %v", def.Type, st.Subtype, values["additionalProperties"])
			}
			properties := values["properties"].(map[string]any)
			for _, key := range connectorRuntimeValueKeys[def.Type] {
				if _, ok := properties[key]; !ok {
					t.Errorf("%s/%s: runtime key %q is not allowed by the schema", def.Type, st.Subtype, key)
				}
			}
			for _, f := range st.FieldSchema {
				prop := properties[f.Name].(map[string]any)
				_, hasMin := prop["minLength"]
				isSecret := f.Secret || f.Type == "password"
				wantMin := f.Validation != nil && f.Validation.MinLength != nil && !isSecret && f.Type != "number" && f.Type != "select" && f.Type != "key_value" && f.Type != "operations"
				if hasMin != wantMin {
					t.Errorf("%s/%s.%s: minLength present = %v, want %v (secret = %v)", def.Type, st.Subtype, f.Name, hasMin, wantMin, isSecret)
				}
			}
		}
	}
}
