// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolsdoc

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

const strSchemaNotGeneratedCell = "*not generated — read the tool's own description*"

type toolRow struct {
	name, inputs, results string
}

func GenerateBuiltinToolFieldsTable() (string, error) {
	rows, err := tableRows()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("| Tool | `input:` parameter names | Result field names (`{{<id>.output.<field>}}`) |\n")
	b.WriteString("|---|---|---|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", row.name, row.inputs, row.results)
	}
	return b.String(), nil
}

func tableRows() ([]toolRow, error) {
	strTableRows, err := strRows()
	if err != nil {
		return nil, err
	}
	rows := append(inProcessRows(), strTableRows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows, nil
}

func strRows() ([]toolRow, error) {
	names := toolinit.STRBuiltinNames()
	rows := make([]toolRow, len(names))
	for i, name := range names {
		params, _, ok := str.InProcessToolSchema(name, nil)
		inputs, err := strInputCell(name, params, ok)
		if err != nil {
			return nil, err
		}
		rows[i] = toolRow{name: name, inputs: inputs, results: resultCell(nil)}
	}
	return rows, nil
}

func strInputCell(name string, params json.RawMessage, found bool) (string, error) {
	if !found {
		return strSchemaNotGeneratedCell, nil
	}
	var parsed tool.JSONSchema
	if err := json.Unmarshal(params, &parsed); err != nil {
		return "", fmt.Errorf("parameter schema of STR tool %q is not valid JSON Schema: %w", name, err)
	}
	return inputCell(&parsed), nil
}

func inProcessRows() []toolRow {
	var rows []toolRow
	for _, name := range toolinit.BuiltinToolNames() {
		if !toolinit.AgentBindableBuiltin(name) {
			continue
		}
		t := toolinit.LookupBuiltinTool(name, nil, toolinit.Options{})
		if t == nil {
			continue
		}
		rows = append(rows, toolRow{name: name, inputs: inputCell(t.Parameters()), results: resultCell(tool.ResultSchemaOf(t))})
	}
	return rows
}

func inputCell(schema *tool.JSONSchema) string {
	if schema == nil || len(schema.Properties) == 0 {
		return "*none*"
	}
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	names := propertyNames(schema)
	rendered := make([]string, 0, len(names))
	for _, name := range names {
		entry := fmt.Sprintf("`%s` (%s)", name, propertyType(schema, name))
		if required[name] {
			entry += " **req**"
		}
		rendered = append(rendered, entry)
	}
	return strings.Join(rendered, ", ")
}

func resultCell(schema *tool.JSONSchema) string {
	if schema == nil {
		return "*not declared — verify against the tool before binding*"
	}
	if len(schema.Properties) == 0 {
		return "*no result fields — the output is an artifact*"
	}
	always := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		always[name] = true
	}
	names := propertyNames(schema)
	rendered := make([]string, 0, len(names))
	for _, name := range names {
		entry := fmt.Sprintf("`%s` (%s)", name, propertyType(schema, name))
		if !always[name] {
			entry += " *opt*"
		}
		rendered = append(rendered, entry)
	}
	return strings.Join(rendered, ", ")
}

func propertyNames(schema *tool.JSONSchema) []string {
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func propertyType(schema *tool.JSONSchema, name string) string {
	prop, ok := schema.Properties[name].(map[string]any)
	if !ok {
		return "any"
	}
	typ, ok := prop["type"].(string)
	if !ok || typ == "" {
		return "any"
	}
	return typ
}
