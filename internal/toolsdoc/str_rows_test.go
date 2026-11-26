// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolsdoc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

func requireSTRRegistry(t *testing.T) {
	t.Helper()
	if len(str.InProcessToolNames()) == 0 {
		t.Skip("no in-process STR tools are registered (sam_lean build)")
	}
}

func generatedTable(t *testing.T) string {
	t.Helper()
	table, err := GenerateBuiltinToolFieldsTable()
	if err != nil {
		t.Fatalf("GenerateBuiltinToolFieldsTable: %v", err)
	}
	return table
}

func hasRow(table, name string) bool {
	return strings.Contains(table, "| `"+name+"` |")
}

func TestTableCoversTheSTRBuiltinsTheGroupsName(t *testing.T) {
	requireSTRRegistry(t)
	table := generatedTable(t)

	for _, group := range []string{"builtin_document_tools", "builtin_media_tools", "builtin_diagram_tools", "builtin_file_tools", "builtin_volume_tools", "builtin_code_execution"} {
		for _, name := range toolinit.LookupBuiltinGroup(group, toolinit.Options{}) {
			if !hasRow(table, name) {
				t.Errorf("group %s member %q has no row in the generated table", group, name)
			}
		}
	}
}

func TestSTRRowsAreDrivenByTheGroupsNotByTheRegistry(t *testing.T) {
	requireSTRRegistry(t)
	table := generatedTable(t)

	for _, name := range []string{"test_harness", "build_apply", "execute_sql_query", "event_mesh_request", "slack", "send_email"} {
		if hasRow(table, name) {
			t.Errorf("%q is registered in-process but no built-in group names it, so it must not have a row", name)
		}
	}
}

func TestSTRRowCarriesParametersAndDeclaresNoResult(t *testing.T) {
	requireSTRRegistry(t)
	inputs, results := splitCells(t, rowFor(t, "pdf_ops"))

	if !strings.Contains(inputs, "`operation` (string) **req**") {
		t.Errorf("pdf_ops inputs must come from its schema; got %q", inputs)
	}
	if !strings.Contains(results, "not declared") {
		t.Errorf("an STR built-in declares no result schema; got %q", results)
	}
}

func TestSTRBuiltinWithoutAnInProcessDefinitionSaysSo(t *testing.T) {
	requireSTRRegistry(t)
	for _, name := range []string{"execute_python_code", "convert_pdf_to_markdown", "volume_read"} {
		inputs, results := splitCells(t, rowFor(t, name))
		if !strings.Contains(inputs, "not generated") {
			t.Errorf("%s: a tool with no in-process definition must not invent parameters; got %q", name, inputs)
		}
		if !strings.Contains(results, "not declared") {
			t.Errorf("%s: result cell = %q, want not declared", name, results)
		}
	}
}

func TestSTRInputCellFailsOnAMalformedSchemaButNotOnAMissingDefinition(t *testing.T) {
	if _, err := strInputCell("pdf_ops", json.RawMessage(`{"type":"object","properties":`), true); err == nil || !strings.Contains(err.Error(), "pdf_ops") {
		t.Errorf("a malformed schema must fail the generator and name the tool, got err = %v", err)
	}

	cell, err := strInputCell("execute_python_code", nil, false)
	if err != nil || !strings.Contains(cell, "not generated") {
		t.Errorf("a tool with no compiled-in definition is \"not generated\"; got %q, err = %v", cell, err)
	}
}
