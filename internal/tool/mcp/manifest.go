// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type ManifestTool struct {
	Name string

	Description string

	InputSchema map[string]any

	OutputSchema map[string]any

	Annotations ToolHints
}

func loadManifestFile(path string) ([]ManifestTool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest file %q: %w", path, err)
	}
	var root any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse manifest file %q: %w", path, err)
	}
	list, ok := root.([]any)
	if !ok {
		return nil, fmt.Errorf("manifest file %q: top-level value must be a list of tool entries, got %T", path, root)
	}
	return parseManifestEntries(list)
}

func parseManifestEntries(raw []any) ([]ManifestTool, error) {
	tools := make([]ManifestTool, 0, len(raw))
	for i, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("manifest entry %d: expected a map, got %T", i, entry)
		}

		mt := ManifestTool{}
		idStr, _ := m["id"].(string)
		nameStr, _ := m["name"].(string)
		switch {
		case nameStr != "":
			mt.Name = nameStr
		case idStr != "":
			mt.Name = idStr
		default:
			return nil, fmt.Errorf("manifest entry %d: missing required field 'name' (or 'id' as fallback)", i)
		}

		if desc, ok := m["description"].(string); ok {
			mt.Description = desc
		}

		switch schema := m["inputSchema"].(type) {
		case map[string]any:
			mt.InputSchema = schema
		case nil:
			mt.InputSchema = map[string]any{"type": "object"}
		default:
			return nil, fmt.Errorf("manifest entry %d (%s): inputSchema must be a map, got %T", i, mt.Name, schema)
		}

		switch schema := m["outputSchema"].(type) {
		case map[string]any:
			mt.OutputSchema = schema
		case nil:
		default:
			return nil, fmt.Errorf("manifest entry %d (%s): outputSchema must be a map, got %T", i, mt.Name, schema)
		}

		hints, err := parseManifestHints(m["annotations"])
		if err != nil {
			return nil, fmt.Errorf("manifest entry %d (%s): %w", i, mt.Name, err)
		}
		mt.Annotations = hints

		tools = append(tools, mt)
	}
	return tools, nil
}
