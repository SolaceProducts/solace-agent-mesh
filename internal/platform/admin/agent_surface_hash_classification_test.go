// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func connectorValueFor(field connectorFieldDef) any {
	switch field.Type {
	case "number":
		return float64(5)
	case "key_value":
		return map[string]any{"X-Tenant": "tenant-1"}
	case "operations":
		return []any{map[string]any{"tool_name": "op", "topic": "orders/new", "tool_description": "Publish an order."}}
	}
	if strings.Contains(field.Name, "url") {
		return "https://host.example.com/path"
	}
	return "value-" + field.Name
}

func connectorValueVariants(subtype connectorSubtypeDef) []map[string]any {
	base := make(map[string]any, len(subtype.FieldSchema))
	var selects []connectorFieldDef
	for _, field := range subtype.FieldSchema {
		if field.Type == "select" && len(field.Options) > 0 {
			base[field.Name] = field.Options[0].Value
			selects = append(selects, field)
			continue
		}
		base[field.Name] = connectorValueFor(field)
	}
	requiredOnly := make(map[string]any, len(base))
	for _, field := range subtype.FieldSchema {
		if field.Required {
			requiredOnly[field.Name] = base[field.Name]
		}
	}
	variants := []map[string]any{base, requiredOnly}
	for _, field := range selects {
		for _, option := range field.Options[1:] {
			variant := maps.Clone(base)
			variant[field.Name] = option.Value
			variants = append(variants, variant)
		}
	}
	return variants
}

func TestB03_EveryEmittedToolEntryKeyIsClassified(t *testing.T) {
	t.Parallel()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("load connector types: %v", err)
	}

	emitted := map[string]string{}
	record := func(source string, entries []map[string]any) {
		for _, entry := range entries {
			for key := range entry {
				if _, seen := emitted[key]; !seen {
					emitted[key] = source
				}
			}
		}
	}
	for _, def := range defs {
		for _, subtype := range def.Subtypes {
			for i, values := range connectorValueVariants(subtype) {
				conn := ConnectorConfig{ID: "11111111-2222-3333-4444-555555555555", Type: def.Type, Subtype: subtype.Subtype, Values: values}
				record(fmt.Sprintf("%s/%s variant %d", def.Type, subtype.Subtype, i), buildConnectorTools(conn))
			}
		}
	}

	pkgFile := store.ToolPackageFile{ToolName: "forecast"}
	record("package tool", []map[string]any{buildPackageToolEntry("weather", pkgFile, ToolPackageInfo{
		ToolFiles: []store.ToolPackageFile{pkgFile},
		ConfigValues: map[string]map[string]any{"forecast": {
			"hil":             map[string]any{"require_approval": true},
			"required_scopes": []any{"tool:weather:invoke"},
		}},
	})})
	for _, toolset := range slices.Sorted(maps.Keys(builtinToolsetTools)) {
		names := builtinToolsetTools[toolset]
		if len(names) == 0 {
			continue
		}
		record("builtin toolset "+toolset, builtinToolsetEntries(toolset, ToolEmission{
			BuiltinToolConfigs:   map[string]map[string]map[string]any{toolset: {names[0]: {"setting": "v"}}},
			BuiltinExcludedTools: map[string][]string{toolset: {"none"}},
			BuiltinToolHIL:       map[string]map[string]any{toolset: {names[0]: map[string]any{"require_approval": true}}},
		}))
	}
	record("builtin group", builtinToolsetEntries("artifact_management", ToolEmission{}))

	if len(emitted) < 8 {
		t.Fatalf("only %d distinct keys emitted (%v); the builders were not exercised", len(emitted), slices.Sorted(maps.Keys(emitted)))
	}
	for _, key := range slices.Sorted(maps.Keys(emitted)) {
		if _, ok := toolEntryKeyRoles[key]; !ok {
			t.Errorf("tool entry key %q (first emitted by %s) has no role in toolEntryKeyRoles", key, emitted[key])
		}
	}
}
