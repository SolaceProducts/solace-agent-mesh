// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFilterSystemFields_BasicRemoval(t *testing.T) {
	input := `
type: object
required: [name, namespace]
properties:
  name:
    type: string
  namespace:
    type: string
    x-sam-system: true
`
	out, err := FilterSystemFields([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err := yaml.Unmarshal(out, &schema); err != nil {
		t.Fatal(err)
	}

	props := schema["properties"].(map[string]any)
	if _, ok := props["namespace"]; ok {
		t.Error("namespace should have been removed")
	}
	if _, ok := props["name"]; !ok {
		t.Error("name should still be present")
	}

	req := schema["required"].([]any)
	if len(req) != 1 || req[0] != "name" {
		t.Errorf("expected required=[name], got %v", req)
	}
}

func TestFilterSystemFields_EmptyRequiredRemoved(t *testing.T) {
	input := `
type: object
required: [namespace]
properties:
  namespace:
    type: string
    x-sam-system: true
  event_handlers:
    type: array
`
	out, err := FilterSystemFields([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err := yaml.Unmarshal(out, &schema); err != nil {
		t.Fatal(err)
	}

	if _, ok := schema["required"]; ok {
		t.Error("required should be removed when empty")
	}

	props := schema["properties"].(map[string]any)
	if _, ok := props["event_handlers"]; !ok {
		t.Error("event_handlers should still be present")
	}
}

func TestFilterSystemFields_NestedObjectRemoved(t *testing.T) {
	input := `
type: object
properties:
  session_service:
    type: object
    x-sam-system: true
    properties:
      type:
        type: string
      database_url:
        type: string
  description:
    type: string
`
	out, err := FilterSystemFields([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err := yaml.Unmarshal(out, &schema); err != nil {
		t.Fatal(err)
	}

	props := schema["properties"].(map[string]any)
	if _, ok := props["session_service"]; ok {
		t.Error("session_service (with nested props) should have been removed")
	}
	if _, ok := props["description"]; !ok {
		t.Error("description should still be present")
	}
}

func TestFilterSystemFields_NoSystemFields(t *testing.T) {
	input := `
type: object
required: [name, description]
properties:
  name:
    type: string
  description:
    type: string
`
	out, err := FilterSystemFields([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err := yaml.Unmarshal(out, &schema); err != nil {
		t.Fatal(err)
	}

	props := schema["properties"].(map[string]any)
	if len(props) != 2 {
		t.Errorf("expected 2 properties, got %d", len(props))
	}

	req := schema["required"].([]any)
	if len(req) != 2 {
		t.Errorf("expected 2 required fields, got %d", len(req))
	}
}

func TestFilterSystemFields_RecursesIntoItems(t *testing.T) {
	input := `
type: object
properties:
  handlers:
    type: array
    items:
      type: object
      required: [name, namespace]
      properties:
        name:
          type: string
        namespace:
          type: string
          x-sam-system: true
`
	out, err := FilterSystemFields([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err := yaml.Unmarshal(out, &schema); err != nil {
		t.Fatal(err)
	}

	handlers := schema["properties"].(map[string]any)["handlers"].(map[string]any)
	items := handlers["items"].(map[string]any)
	itemProps := items["properties"].(map[string]any)

	if _, ok := itemProps["namespace"]; ok {
		t.Error("namespace in items should have been removed")
	}
	if _, ok := itemProps["name"]; !ok {
		t.Error("name in items should still be present")
	}

	req := items["required"].([]any)
	if len(req) != 1 || req[0] != "name" {
		t.Errorf("expected items required=[name], got %v", req)
	}
}

func TestFilterSystemFields_RecursesIntoOneOf(t *testing.T) {
	input := `
type: object
properties:
  config:
    oneOf:
      - type: object
        properties:
          mode:
            type: string
          namespace:
            type: string
            x-sam-system: true
`
	out, err := FilterSystemFields([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err := yaml.Unmarshal(out, &schema); err != nil {
		t.Fatal(err)
	}

	config := schema["properties"].(map[string]any)["config"].(map[string]any)
	oneOf := config["oneOf"].([]any)
	variant := oneOf[0].(map[string]any)
	props := variant["properties"].(map[string]any)

	if _, ok := props["namespace"]; ok {
		t.Error("namespace in oneOf variant should have been removed")
	}
}

func TestFilterSystemFields_AdditionalPropertiesFalse(t *testing.T) {
	input := `
type: object
required: [name]
properties:
  name:
    type: string
  namespace:
    type: string
    x-sam-system: true
`
	out, err := FilterSystemFields([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err := yaml.Unmarshal(out, &schema); err != nil {
		t.Fatal(err)
	}

	ap, ok := schema["additionalProperties"]
	if !ok {
		t.Fatal("additionalProperties should be set after filtering")
	}
	if ap != false {
		t.Errorf("additionalProperties should be false, got %v", ap)
	}
}

func TestFilterSystemFields_SystemFieldNotInRequired(t *testing.T) {
	input := `
type: object
required: [name]
properties:
  name:
    type: string
  namespace:
    type: string
    x-sam-system: true
`
	out, err := FilterSystemFields([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err := yaml.Unmarshal(out, &schema); err != nil {
		t.Fatal(err)
	}

	props := schema["properties"].(map[string]any)
	if _, ok := props["namespace"]; ok {
		t.Error("namespace should have been removed")
	}

	req := schema["required"].([]any)
	if len(req) != 1 || req[0] != "name" {
		t.Errorf("required should be unchanged: [name], got %v", req)
	}
}
