// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
)

func GenerateConnectorJSONSchema(typeDefs []connectorTypeDef) ([]byte, error) {
	var typeEnums []any
	var oneOfBranches []any

	for _, td := range typeDefs {
		typeEnums = append(typeEnums, td.Type)
		for _, st := range td.Subtypes {
			branch := buildOneOfBranch(td.Type, st)
			oneOfBranches = append(oneOfBranches, branch)
		}
	}

	schema := map[string]any{
		"$schema":  "https://json-schema.org/draft/2020-12/schema",
		"title":    "SAM Connector Config",
		"type":     "object",
		"required": []string{"name", "type", "subtype", "values"},
		"properties": map[string]any{
			"name": map[string]any{
				"type":      "string",
				"minLength": 3,
				"maxLength": 255,
			},
			"description": map[string]any{
				"type":      "string",
				"minLength": 10,
				"maxLength": 1000,
			},
			"type": map[string]any{
				"type": "string",
				"enum": typeEnums,
			},
			"subtype": map[string]any{
				"type": "string",
			},
			"icon": map[string]any{
				"type":        "string",
				"description": "Service icon identifier for UI display. Use a known value (e.g. jira, github, postgresql) or 'other' for generic icon.",
			},
			"values": map[string]any{
				"type": "object",
			},
		},
		"oneOf":                oneOfBranches,
		"additionalProperties": false,
	}

	return json.MarshalIndent(schema, "", "  ")
}

func buildOneOfBranch(typeName string, st connectorSubtypeDef) map[string]any {
	valuesProps := make(map[string]any)
	var valuesRequired []string

	for _, f := range st.FieldSchema {
		prop := fieldToJSONSchemaProperty(f)
		valuesProps[f.Name] = prop

		if f.Required && len(f.VisibleWhen) == 0 {
			valuesRequired = append(valuesRequired, f.Name)
		}
	}

	for _, key := range connectorRuntimeValueKeys[typeName] {
		if _, declared := valuesProps[key]; !declared {
			valuesProps[key] = map[string]any{}
		}
	}

	valuesSchema := map[string]any{
		"type":                 "object",
		"properties":           valuesProps,
		"additionalProperties": false,
	}
	if len(valuesRequired) > 0 {
		valuesSchema["required"] = valuesRequired
	}

	return map[string]any{
		"properties": map[string]any{
			"type":    map[string]any{"const": typeName},
			"subtype": map[string]any{"const": st.Subtype},
			"values":  valuesSchema,
		},
	}
}

func fieldToJSONSchemaProperty(f connectorFieldDef) map[string]any {
	prop := make(map[string]any)

	switch f.Type {
	case "number":
		prop["type"] = "number"
		if f.Validation != nil {
			if f.Validation.Min != nil {
				prop["minimum"] = *f.Validation.Min
			}
			if f.Validation.Max != nil {
				prop["maximum"] = *f.Validation.Max
			}
		}
	case "select":
		prop["type"] = "string"
		if len(f.Options) > 0 {
			var enums []any
			for _, opt := range f.Options {
				enums = append(enums, opt.Value)
			}
			prop["enum"] = enums
		}
	case "key_value":
		prop["type"] = "object"
	case "hil":
		prop["type"] = []any{"object", "null"}
		prop["properties"] = map[string]any{"tools": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "object"}}}
	case "operations":
		prop["type"] = "array"
		prop["minItems"] = 1
		prop["items"] = map[string]any{
			"type": "object",
			"required": []string{
				"tool_name",
				"tool_description",
				"topic",
				"wait_for_response",
				"payload_format",
				"input_schema",
			},
			"properties": map[string]any{
				"tool_name": map[string]any{
					"type":      "string",
					"maxLength": 64,
					"pattern":   "^[a-z][a-z0-9_]*$",
				},
				"tool_description": map[string]any{
					"type":      "string",
					"minLength": 10,
					"maxLength": 2048,
				},
				"topic": map[string]any{
					"type":      "string",
					"minLength": 1,
					"maxLength": 512,
				},
				"wait_for_response": map[string]any{"type": "boolean"},
				"request_expiry_ms": map[string]any{
					"type":    "number",
					"minimum": 1000,
					"maximum": 300000,
				},
				"reply_mode": map[string]any{
					"type": "string",
					"enum": []string{"temp_queue", "p2p_inbox"},
				},
				"qos": map[string]any{
					"type": "integer",
					"enum": []int{0, 1},
				},
				"payload_format": map[string]any{
					"type": "string",
					"enum": []string{"json", "yaml", "text"},
				},
				"input_schema": map[string]any{
					"type":     "object",
					"required": []string{"type", "properties"},
					"properties": map[string]any{
						"type":       map[string]any{"const": "object"},
						"properties": map[string]any{"type": "object"},
						"required":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
				},
			},
		}
	default:
		prop["type"] = "string"
		if f.Validation != nil {
			isSecret := f.Secret || f.Type == "password"
			if f.Validation.MinLength != nil && !isSecret {
				prop["minLength"] = *f.Validation.MinLength
			}
			if f.Validation.MaxLength != nil {
				prop["maxLength"] = *f.Validation.MaxLength
			}
			if f.Validation.Pattern != "" {
				prop["pattern"] = f.Validation.Pattern
			}
		}
	}

	if f.Description != "" {
		prop["description"] = f.Description
	}

	return prop
}

func GenerateConnectorJSONSchemaFromEmbedded() ([]byte, error) {
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		return nil, fmt.Errorf("loading connector type definitions: %w", err)
	}
	return GenerateConnectorJSONSchema(defs)
}
