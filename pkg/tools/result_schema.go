// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import "github.com/SolaceProducts/solace-agent-mesh/internal/tool"

type authoredSchemas struct{}

func (authoredSchemas) AuthoredSchemas() bool { return true }

type resultField struct {
	name        string
	typ         string
	description string

	optional bool

	anyType bool
}

func resultSchema(fields ...resultField) *tool.JSONSchema {
	props := make(map[string]any, len(fields))
	required := make([]string, 0, len(fields))
	for _, f := range fields {
		prop := map[string]any{}
		if !f.anyType {
			prop["type"] = f.typ
		}
		if f.description != "" {
			prop["description"] = f.description
		}
		props[f.name] = prop
		if !f.optional {
			required = append(required, f.name)
		}
	}
	return &tool.JSONSchema{Type: "object", Properties: props, Required: required}
}

func emptyResultSchema() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object", Properties: map[string]any{}}
}
