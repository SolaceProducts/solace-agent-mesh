// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func declaredWorkflowOutput(card a2a.AgentCard) string {
	var schema, mapping map[string]any
	for _, ext := range card.Capabilities.Extensions {
		switch ext.URI {
		case ExtensionURISchemas:
			schema, _ = ext.Params["output_schema"].(map[string]any)
		case a2a.ExtensionURIWorkflowVisualization:
			cfg, _ := ext.Params["workflow_config"].(map[string]any)
			mapping, _ = cfg["output_mapping"].(map[string]any)
		}
	}
	if props, _ := schema["properties"].(map[string]any); len(props) > 0 {
		return a2a.FormatDeclaredOutput(fieldsOfSchemaProperties(props))
	}
	fields := make([]a2a.OutputField, 0, len(mapping))
	for name := range mapping {
		fields = append(fields, a2a.OutputField{Name: name})
	}
	return a2a.FormatDeclaredOutput(fields)
}

func fieldsOfSchemaProperties(props map[string]any) []a2a.OutputField {
	fields := make([]a2a.OutputField, 0, len(props))
	for name, def := range props {
		field := a2a.OutputField{Name: name}
		if d, ok := def.(map[string]any); ok {
			field.Type, _ = d["type"].(string)
		}
		fields = append(fields, field)
	}
	return fields
}
