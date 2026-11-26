// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"encoding/json"
	"fmt"
)

func ValidatePayload(data any, schema map[string]any) error {
	if len(schema) == 0 {
		return nil
	}

	expectedType, _ := schema["type"].(string)
	if expectedType != "" {
		if err := checkType(data, expectedType); err != nil {
			return err
		}
	}

	if expectedType == "object" {
		m, ok := data.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object, got %T", data)
		}
		if required, ok := schema["required"].([]any); ok {
			for _, r := range required {
				key, _ := r.(string)
				if key == "" {
					continue
				}
				if _, exists := m[key]; !exists {
					return fmt.Errorf("missing required field: %q", key)
				}
			}
		}
	}

	return nil
}

func checkType(data any, expected string) error {
	switch expected {
	case "object":
		if _, ok := data.(map[string]any); !ok {
			return fmt.Errorf("expected object, got %T", data)
		}
	case "array":
		if _, ok := data.([]any); !ok {
			return fmt.Errorf("expected array, got %T", data)
		}
	case "string":
		if _, ok := data.(string); !ok {
			return fmt.Errorf("expected string, got %T", data)
		}
	case "number", "integer":
		switch data.(type) {
		case int, int64, float64, float32:
		case json.Number:
		default:
			return fmt.Errorf("expected %s, got %T", expected, data)
		}
	case "boolean":
		if _, ok := data.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", data)
		}
	}
	return nil
}
