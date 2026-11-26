// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

func eventMeshOperations(values map[string]any) ([]map[string]any, error) {
	raw, exists := values["operations"]
	if !exists {
		return nil, &ValidationError{Message: "operations is required"}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal operations: %w", err)
	}
	var operations []map[string]any
	if err := json.Unmarshal(encoded, &operations); err != nil {
		return nil, &ValidationError{Message: "operations must be an array of operation objects"}
	}
	return operations, nil
}

func eventMeshOperationValues(operation map[string]any) map[string]any {
	values := make(map[string]any, len(operation))
	for key, value := range operation {
		values[key] = value
	}
	if parameters, ok := operation["parameters"]; ok {
		switch typed := parameters.(type) {
		case string:
			values["parameters"] = typed
		default:
			if encoded, err := json.Marshal(typed); err == nil {
				values["parameters"] = string(encoded)
			}
		}
	} else if inputSchema, ok := operation["input_schema"]; ok {
		if parameters, err := eventMeshParametersFromInputSchema(inputSchema); err == nil {
			if encoded, err := json.Marshal(parameters); err == nil {
				values["parameters"] = string(encoded)
			}
		}
	}
	return values
}

func eventMeshParametersFromInputSchema(raw any) ([]map[string]any, error) {
	schema, ok := raw.(map[string]any)
	if !ok {
		return nil, &ValidationError{Message: "input_schema must be a JSON Schema object"}
	}
	if schemaType, _ := schema["type"].(string); schemaType != "object" {
		return nil, &ValidationError{Message: "input_schema.type must be \"object\""}
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil, &ValidationError{Message: "input_schema.properties must be an object"}
	}
	required := map[string]struct{}{}
	if rawRequired, exists := schema["required"]; exists {
		var items []any
		switch typed := rawRequired.(type) {
		case []any:
			items = typed
		case []string:
			items = make([]any, len(typed))
			for index, item := range typed {
				items[index] = item
			}
		default:
			return nil, &ValidationError{Message: "input_schema.required must be an array of property names"}
		}
		for _, item := range items {
			name, ok := item.(string)
			if !ok {
				return nil, &ValidationError{Message: "input_schema.required must contain only property names"}
			}
			required[name] = struct{}{}
		}
	}
	for name := range required {
		if _, exists := properties[name]; !exists {
			return nil, &ValidationError{Message: fmt.Sprintf("input_schema.required references undefined property %q", name)}
		}
	}

	parameters := make([]map[string]any, 0, len(properties))
	for _, name := range slices.Sorted(maps.Keys(properties)) {
		property, ok := properties[name].(map[string]any)
		if !ok {
			return nil, &ValidationError{Message: fmt.Sprintf("input_schema property %q must be an object", name)}
		}
		parameter := map[string]any{"name": name}
		for _, key := range []string{"type", "description", "enum", "default"} {
			if value, exists := property[key]; exists {
				parameter[key] = value
			}
		}
		if _, ok := required[name]; ok {
			parameter["required"] = true
		}
		if value, ok := property["x-solace-payload-path"].(string); ok && value != "" {
			parameter["payload_path"] = value
		}
		if value, ok := property["x-solace-context-expression"].(string); ok && value != "" {
			parameter["context_expression"] = value
			delete(parameter, "required")
		}
		parameters = append(parameters, parameter)
	}
	return parameters, nil
}
