// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestProvenanceOf(t *testing.T) {
	cases := []struct {
		name     string
		toolMap  map[string]any
		toolName string
		want     Provenance
	}{
		{
			name:     "a shipped tool",
			toolMap:  map[string]any{"tool_type": "builtin", "tool_name": "web_request"},
			toolName: "web_request",
			want: Provenance{
				Category:        taxonomy.ToolCategoryBuiltin,
				BuiltinCategory: "builtin_web_request_tools",
			},
		},
		{
			name:     "a shipped toolset",
			toolMap:  map[string]any{"tool_type": "builtin-group", "group_name": "web_tools"},
			toolName: "web_request",
			want: Provenance{
				Category:        taxonomy.ToolCategoryBuiltin,
				BuiltinCategory: "builtin_web_request_tools",
			},
		},
		{
			name: "a connector that compiles down to a builtin entry",
			toolMap: map[string]any{
				"tool_type":                 "builtin",
				"tool_name":                 "acme_ledger_sql_query_1a2b3c4d",
				"source_connector_category": "sql",
			},
			toolName: "acme_ledger_sql_query_1a2b3c4d",
			want:     Provenance{Category: taxonomy.ToolCategoryConnector, ConnectorCategory: "sql"},
		},
		{
			name: "a connector openapi entry",
			toolMap: map[string]any{
				"tool_type":                 "openapi",
				"source_connector_id":       "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000",
				"source_connector_category": "api",
			},
			toolName: "getInvoices",
			want:     Provenance{Category: taxonomy.ToolCategoryConnector, ConnectorCategory: "api"},
		},
		{
			name: "a connector entry generated before the category was stamped",
			toolMap: map[string]any{
				"tool_type":           "mcp",
				"source_connector_id": "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000",
			},
			toolName: "search_docs",
			want:     Provenance{Category: taxonomy.ToolCategoryConnector},
		},
		{
			name: "a connector stamped with a category the catalog does not approve",
			toolMap: map[string]any{
				"tool_type":                 "mcp",
				"source_connector_id":       "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000",
				"source_connector_category": "acme_internal_crm",
			},
			toolName: "search_docs",
			want:     Provenance{Category: taxonomy.ToolCategoryConnector},
		},
		{
			name:     "an uploaded tool package",
			toolMap:  map[string]any{"tool_type": "builtin", "tool_name": "acme_payroll__lookup"},
			toolName: "acme_payroll__lookup",
			want:     Provenance{Category: taxonomy.ToolCategoryCustom},
		},
		{
			name:     "an operator's own openapi spec",
			toolMap:  map[string]any{"tool_type": "openapi"},
			toolName: "getInvoices",
			want:     Provenance{Category: taxonomy.ToolCategoryCustom},
		},
		{
			name:     "an operator's own mcp server",
			toolMap:  map[string]any{"tool_type": "mcp"},
			toolName: "search_docs",
			want:     Provenance{Category: taxonomy.ToolCategoryCustom},
		},
		{
			name:     "an entry naming no type at all",
			toolMap:  map[string]any{},
			toolName: "mystery",
			want:     Provenance{Category: taxonomy.ToolCategoryCustom},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ProvenanceOf(c.toolMap, c.toolName); got != c.want {
				t.Errorf("ProvenanceOf = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestProvenanceUsesTheRealSkillSeparator(t *testing.T) {
	name := "acme_payroll" + str.SkillToolSep + "lookup"
	got := ProvenanceOf(map[string]any{"tool_type": ToolTypeBuiltin, "tool_name": name}, name)
	if got.Category != taxonomy.ToolCategoryCustom {
		t.Errorf("category = %q, want %q", got.Category, taxonomy.ToolCategoryCustom)
	}
}

func TestToolCategoriesMatchTheCatalog(t *testing.T) {
	produced := []string{
		ProvenanceOf(map[string]any{"tool_type": ToolTypeBuiltin}, "web_request").Category,
		ProvenanceOf(map[string]any{"source_connector_category": "sql"}, "q").Category,
		ProvenanceOf(map[string]any{"tool_type": "openapi"}, "q").Category,
		SkillProvenance().Category,
	}
	approved := slices.Clone(taxonomy.ToolCategories)
	slices.Sort(approved)
	slices.Sort(produced)
	produced = slices.Compact(produced)
	if !slices.Equal(produced, approved) {
		t.Errorf("classifier produces %v, the catalog approves %v; keep them in step", produced, approved)
	}
}

func TestSourceConnectorCategory(t *testing.T) {
	cases := []struct {
		name            string
		toolMap         map[string]any
		wantCategory    string
		wantIsConnector bool
	}{
		{"a hand-authored entry", map[string]any{"tool_type": "builtin"}, "", false},
		{"a stamped entry", map[string]any{"source_connector_category": "slack"}, "slack", true},
		{"an id with no category", map[string]any{"source_connector_id": "abc"}, "", true},
		{"a non-string category", map[string]any{"source_connector_category": 7}, "", false},
		{
			"a category the catalog does not approve",
			map[string]any{
				"source_connector_category": "acme_internal_crm",
				"source_connector_id":       "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000",
			},
			"", true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			category, isConnector := SourceConnectorCategory(c.toolMap)
			if category != c.wantCategory || isConnector != c.wantIsConnector {
				t.Errorf("SourceConnectorCategory = (%q, %v), want (%q, %v)",
					category, isConnector, c.wantCategory, c.wantIsConnector)
			}
		})
	}
}
