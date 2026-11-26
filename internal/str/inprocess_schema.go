// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const autoDetectSchemaKey = "auto_detect_schema"

var pureDynamicSchemaBinaries = map[string]func(config map[string]any) map[string]any{
	"event_mesh_request": copyConfig,
	"document_db_query": func(config map[string]any) map[string]any {
		safe := copyConfig(config)
		safe[autoDetectSchemaKey] = false
		return safe
	},
}

func copyConfig(config map[string]any) map[string]any {
	out := make(map[string]any, len(config)+1)
	maps.Copy(out, config)
	return out
}

func InProcessToolSchema(binary string, config map[string]any) (params json.RawMessage, overrideApplied bool, ok bool) {
	td := lookupGlobalInProcessTool(binary)
	if td == nil {
		return nil, false, false
	}
	prepare, pure := pureDynamicSchemaBinaries[binary]
	if !pure {
		return schemaOutcome(td, nil, true)
	}
	safe := prepare(config)
	if !td.DynamicSchemaApplies(safe) {
		return schemaOutcome(td, nil, false)
	}
	return schemaOutcome(td, safe, true)
}

func schemaOutcome(td *samtoolsdk.ToolDef, config map[string]any, overrideApplied bool) (json.RawMessage, bool, bool) {
	params, err := toolParameters(td, config)
	if err != nil {
		return nil, false, false
	}
	return params, overrideApplied, true
}

func toolParameters(td *samtoolsdk.ToolDef, config map[string]any) (json.RawMessage, error) {
	data, err := samtoolsdk.SchemaJSON([]*samtoolsdk.ToolDef{td}, config)
	if err != nil {
		return nil, fmt.Errorf("schema for %q: %w", td.Name(), err)
	}
	var doc struct {
		Tools map[string]struct {
			Parameters json.RawMessage `json:"parameters"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse schema for %q: %w", td.Name(), err)
	}
	entries := slices.Collect(maps.Values(doc.Tools))
	if len(entries) != 1 {
		return nil, fmt.Errorf("schema for %q has %d tool entries, want 1", td.Name(), len(entries))
	}
	return entries[0].Parameters, nil
}
