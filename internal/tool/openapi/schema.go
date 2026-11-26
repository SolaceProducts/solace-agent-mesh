// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"maps"
	"slices"

	"github.com/getkin/kin-openapi/openapi3"
)

const maxSchemaDepth = 100

const maxSchemaNodes = 20000

const (
	circularSchemaDescription = "circular schema reference"
	schemaTooLargeDescription = "schema too large"
)

type schemaRenderState struct {
	visited map[*openapi3.Schema]bool
	nodes   int
}

func renderSchema(s *openapi3.Schema, fallbackDesc string) map[string]any {
	return renderSchemaGuarded(s, fallbackDesc, &schemaRenderState{visited: map[*openapi3.Schema]bool{}}, 0)
}

func renderSchemaGuarded(s *openapi3.Schema, fallbackDesc string, state *schemaRenderState, depth int) map[string]any {
	if s == nil {
		return map[string]any{"type": "string"}
	}
	if depth >= maxSchemaDepth || state.visited[s] {
		return schemaStub(s, circularSchemaDescription)
	}
	state.nodes++
	if state.nodes > maxSchemaNodes {
		return schemaStub(s, schemaTooLargeDescription)
	}
	state.visited[s] = true
	defer delete(state.visited, s)

	out := make(map[string]any)

	if typeList := s.Type.Slice(); len(typeList) > 0 {
		if len(typeList) == 1 {
			out["type"] = typeList[0]
		} else {
			out["type"] = typeList
		}
	}

	if s.Format != "" {
		out["format"] = s.Format
	}
	if s.Description != "" {
		out["description"] = s.Description
	} else if fallbackDesc != "" {
		out["description"] = fallbackDesc
	}
	if s.Title != "" {
		out["title"] = s.Title
	}
	if s.Default != nil {
		out["default"] = s.Default
	}
	if s.Example != nil {
		out["example"] = s.Example
	}
	if len(s.Enum) > 0 {
		out["enum"] = s.Enum
	}
	if s.Nullable {
		out["nullable"] = true
	}

	renderNumericConstraints(s, out)
	renderStringConstraints(s, out)
	renderArrayConstraints(s, out)

	if s.Items != nil && s.Items.Value != nil {
		out["items"] = renderSchemaGuarded(s.Items.Value, "", state, depth+1)
	}

	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		names := slices.Sorted(maps.Keys(s.Properties))
		short := shortPropertyNames(names)
		for _, name := range names {
			propRef := s.Properties[name]
			if propRef == nil || propRef.Value == nil {
				continue
			}
			props[short[name]] = renderSchemaGuarded(propRef.Value, "", state, depth+1)
		}
		out["properties"] = props
	}
	if len(s.Required) > 0 {
		short := shortPropertyNames(slices.Sorted(maps.Keys(s.Properties)))
		req := make([]string, 0, len(s.Required))
		for _, r := range s.Required {
			if name, ok := short[r]; ok {
				req = append(req, name)
				continue
			}
			req = append(req, truncateTo60(r))
		}
		out["required"] = req
	}
	if s.MinProps > 0 {
		out["minProperties"] = s.MinProps
	}
	if s.MaxProps != nil {
		out["maxProperties"] = *s.MaxProps
	}

	if s.AdditionalProperties.Has != nil {
		out["additionalProperties"] = *s.AdditionalProperties.Has
	} else if s.AdditionalProperties.Schema != nil && s.AdditionalProperties.Schema.Value != nil {
		out["additionalProperties"] = renderSchemaGuarded(s.AdditionalProperties.Schema.Value, "", state, depth+1)
	}

	renderCombinatorsGuarded(s, out, state, depth)

	return out
}

type originalProperty struct {
	name   string
	schema *openapi3.Schema
}

func restoreNestedKeys(v any, s *openapi3.Schema) any {
	return restoreNestedKeysGuarded(v, s, 0)
}

func restoreNestedKeysGuarded(v any, s *openapi3.Schema, depth int) any {
	if s == nil || depth >= maxSchemaDepth {
		return v
	}
	switch val := v.(type) {
	case map[string]any:
		originals := map[string]originalProperty{}
		collectOriginalProperties(s, originals, map[*openapi3.Schema]bool{}, 0)
		var extra *openapi3.Schema
		if s.AdditionalProperties.Schema != nil {
			extra = s.AdditionalProperties.Schema.Value
		}
		out := make(map[string]any, len(val))
		for k, x := range val {
			if orig, ok := originals[k]; ok {
				out[orig.name] = restoreNestedKeysGuarded(x, orig.schema, depth+1)
				continue
			}
			out[k] = restoreNestedKeysGuarded(x, extra, depth+1)
		}
		return out
	case []any:
		if s.Items == nil || s.Items.Value == nil {
			return v
		}
		out := make([]any, len(val))
		for i, x := range val {
			out[i] = restoreNestedKeysGuarded(x, s.Items.Value, depth+1)
		}
		return out
	}
	return v
}

func collectOriginalProperties(s *openapi3.Schema, into map[string]originalProperty, visited map[*openapi3.Schema]bool, depth int) {
	if s == nil || depth >= maxSchemaDepth || visited[s] {
		return
	}
	visited[s] = true
	defer delete(visited, s)
	names := slices.Sorted(maps.Keys(s.Properties))
	short := shortPropertyNames(names)
	for _, name := range names {
		propRef := s.Properties[name]
		if propRef == nil || propRef.Value == nil {
			continue
		}
		if _, exists := into[short[name]]; !exists {
			into[short[name]] = originalProperty{name: name, schema: propRef.Value}
		}
	}
	for _, refs := range []openapi3.SchemaRefs{s.AllOf, s.OneOf, s.AnyOf} {
		for _, ref := range refs {
			if ref != nil {
				collectOriginalProperties(ref.Value, into, visited, depth+1)
			}
		}
	}
}

func schemaStub(s *openapi3.Schema, description string) map[string]any {
	stub := map[string]any{"type": "object", "description": description}
	if s.Title != "" {
		stub["title"] = s.Title
	}
	if typeList := s.Type.Slice(); len(typeList) == 1 {
		stub["type"] = typeList[0]
	}
	return stub
}

func renderCombinatorsGuarded(s *openapi3.Schema, out map[string]any, state *schemaRenderState, depth int) {
	if len(s.OneOf) > 0 {
		out["oneOf"] = renderSchemaListGuarded(s.OneOf, state, depth+1)
	}
	if len(s.AnyOf) > 0 {
		out["anyOf"] = renderSchemaListGuarded(s.AnyOf, state, depth+1)
	}
	if len(s.AllOf) > 0 {
		out["allOf"] = renderSchemaListGuarded(s.AllOf, state, depth+1)
	}
	if s.Not != nil && s.Not.Value != nil {
		out["not"] = renderSchemaGuarded(s.Not.Value, "", state, depth+1)
	}
}

func renderSchemaListGuarded(refs openapi3.SchemaRefs, state *schemaRenderState, depth int) []any {
	out := make([]any, 0, len(refs))
	for _, ref := range refs {
		if ref == nil || ref.Value == nil {
			continue
		}
		out = append(out, renderSchemaGuarded(ref.Value, "", state, depth))
	}
	return out
}

func renderNumericConstraints(s *openapi3.Schema, out map[string]any) {
	if s.Min != nil {
		out["minimum"] = *s.Min
	}
	if s.Max != nil {
		out["maximum"] = *s.Max
	}
	if v, ok := renderExclusiveBound(s.ExclusiveMin); ok {
		out["exclusiveMinimum"] = v
	}
	if v, ok := renderExclusiveBound(s.ExclusiveMax); ok {
		out["exclusiveMaximum"] = v
	}
	if s.MultipleOf != nil {
		out["multipleOf"] = *s.MultipleOf
	}
}

func renderStringConstraints(s *openapi3.Schema, out map[string]any) {
	if s.MinLength > 0 {
		out["minLength"] = s.MinLength
	}
	if s.MaxLength != nil {
		out["maxLength"] = *s.MaxLength
	}
	if s.Pattern != "" {
		out["pattern"] = s.Pattern
	}
}

func renderArrayConstraints(s *openapi3.Schema, out map[string]any) {
	if s.MinItems > 0 {
		out["minItems"] = s.MinItems
	}
	if s.MaxItems != nil {
		out["maxItems"] = *s.MaxItems
	}
	if s.UniqueItems {
		out["uniqueItems"] = true
	}
}

func renderExclusiveBound(bound openapi3.ExclusiveBound) (any, bool) {
	if bound.Value != nil {
		return *bound.Value, true
	}
	if bound.IsTrue() {
		return true, true
	}
	return nil, false
}

func hasBinaryField(s *openapi3.Schema) bool {
	return hasBinaryFieldGuarded(s, map[*openapi3.Schema]bool{}, 0)
}

func hasBinaryFieldGuarded(s *openapi3.Schema, visited map[*openapi3.Schema]bool, depth int) bool {
	if s == nil {
		return false
	}
	if depth >= maxSchemaDepth || visited[s] {
		return false
	}
	visited[s] = true
	defer delete(visited, s)

	if s.Format == "binary" {
		return true
	}
	for _, propRef := range s.Properties {
		if propRef == nil || propRef.Value == nil {
			continue
		}
		if hasBinaryFieldGuarded(propRef.Value, visited, depth+1) {
			return true
		}
	}
	if s.Items != nil && s.Items.Value != nil && hasBinaryFieldGuarded(s.Items.Value, visited, depth+1) {
		return true
	}
	return false
}

func isBinaryProperty(s *openapi3.Schema) bool {
	if s == nil {
		return false
	}
	return s.Format == "binary"
}
