// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestRegisterToolsClassifiesEachRegisteredTool(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "web_request"},
		map[string]any{"tool_type": "builtin-group", "group_name": "web_tools"},
		map[string]any{"tool_type": "builtin-group", "group_name": "builder_tools"},
		map[string]any{
			"tool_type":                 "builtin",
			"tool_name":                 "acme_ledger_sql_query_1a2b3c4d",
			"source_connector_category": "sql",
		},
		map[string]any{
			"tool_type":                 "builtin",
			"tool_name":                 "acme_crm_lookup_5e6f7g8h",
			"source_connector_category": "acme_internal_crm",
			"source_connector_id":       "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000",
		},
		map[string]any{"tool_type": "builtin", "tool_name": "acme_payroll__lookup"},
	}
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatal(err)
	}

	want := map[string]agent.ToolProvenance{
		"web_request": {
			Category:        taxonomy.ToolCategoryBuiltin,
			BuiltinCategory: "builtin_web_request_tools",
		},
		"platform_list_components": {
			Category:        taxonomy.ToolCategoryBuiltin,
			BuiltinCategory: "builder_tools",
		},
		"acme_ledger_sql_query_1a2b3c4d": {Category: taxonomy.ToolCategoryConnector, ConnectorCategory: "sql"},
		"acme_crm_lookup_5e6f7g8h":       {Category: taxonomy.ToolCategoryConnector},
		"acme_payroll__lookup":           {Category: taxonomy.ToolCategoryCustom},
	}
	for name, expected := range want {
		got, classified := ag.ToolProvenanceFor(name)
		if !classified {
			t.Errorf("%s was registered but never classified", name)
			continue
		}
		if got != expected {
			t.Errorf("%s provenance = %+v, want %+v", name, got, expected)
		}
	}
}

func TestRegisterToolsClassifiesTheAutoInjectedIndexSearch(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	if _, err := RegisterTools(ag, []any{
		map[string]any{"tool_type": "builtin", "tool_name": "web_request"},
	}, svc); err != nil {
		t.Fatal(err)
	}

	got, classified := ag.ToolProvenanceFor(indexSearchToolName)
	if !classified {
		t.Fatal("index_search is registered on every agent but classified on none, so its adoption is reported by nothing")
	}
	want := agent.ToolProvenance{
		Category:        taxonomy.ToolCategoryBuiltin,
		BuiltinCategory: taxonomy.BuiltinCategoryUngrouped,
	}
	if got != want {
		t.Errorf("index_search provenance = %+v, want %+v", got, want)
	}
}

func TestAgentYAMLConnectorToolsAreClassified(t *testing.T) {
	t.Parallel()
	withTestConnectorType(t, "fake-provenance")

	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "conn-provenance"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	appCfg := config.FromMap(map[string]any{
		"connectors": []any{
			map[string]any{"name": "acme-payroll-mailbox", "type": "fake-provenance"},
		},
	})
	if err := LoadAndRegisterConnectors(t.Context(), ag, appCfg, svc); err != nil {
		t.Fatal(err)
	}

	got, classified := ag.ToolProvenanceFor("tool_acme-payroll-mailbox")
	if !classified {
		t.Fatal("an agent-declared connector tool was registered but never classified, so an absent connectorCategory cannot be read")
	}
	want := agent.ToolProvenance{
		Category:          taxonomy.ToolCategoryConnector,
		ConnectorCategory: "fake-provenance",
	}
	if got != want {
		t.Errorf("connector tool provenance = %+v, want %+v", got, want)
	}
	if strings.Contains(got.ConnectorCategory, "acme-payroll-mailbox") {
		t.Errorf("connectorCategory = %q, which carries the connector's own name", got.ConnectorCategory)
	}
}

func TestConnectorsAndTheToolsListAreBothClassifiedOnOneAgent(t *testing.T) {
	t.Parallel()
	withTestConnectorType(t, "fake-both")

	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "conn-and-tools"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	appCfg := config.FromMap(map[string]any{
		"connectors": []any{map[string]any{"name": "ledger", "type": "fake-both"}},
	})
	if err := LoadAndRegisterConnectors(t.Context(), ag, appCfg, svc); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterTools(ag, []any{
		map[string]any{"tool_type": "builtin", "tool_name": "web_request"},
	}, svc); err != nil {
		t.Fatal(err)
	}

	want := map[string]agent.ToolProvenance{
		"tool_ledger": {
			Category:          taxonomy.ToolCategoryConnector,
			ConnectorCategory: "fake-both",
		},
		"web_request": {
			Category:        taxonomy.ToolCategoryBuiltin,
			BuiltinCategory: "builtin_web_request_tools",
		},
	}
	for name, expected := range want {
		got, classified := ag.ToolProvenanceFor(name)
		if !classified {
			t.Errorf("%s lost its classification to the other registration path", name)
			continue
		}
		if got != expected {
			t.Errorf("%s provenance = %+v, want %+v", name, got, expected)
		}
	}
}
