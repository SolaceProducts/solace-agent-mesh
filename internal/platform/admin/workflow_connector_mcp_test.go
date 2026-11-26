// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const mcpServerURL = "http://localhost:9999/mcp"

func mcpValues(extra string) string {
	if extra == "" {
		return `{"server_url":"` + mcpServerURL + `"}`
	}
	return `{"server_url":"` + mcpServerURL + `",` + extra + `}`
}

const crmManifestJSON = `"manifest":[{"name":"find_customer"},{"name":"list_orders"},{"name":"get_order"}]`

func mcpToolNodes(triples ...string) map[string]any {
	nodes := make([]any, 0, len(triples)/3)
	for i := 0; i+2 < len(triples); i += 3 {
		node := map[string]any{"id": triples[i], "type": "tool", "connector": triples[i+1]}
		if triples[i+2] != "" {
			node["tool"] = triples[i+2]
		}
		nodes = append(nodes, node)
	}
	return wrappedAppConfig(map[string]any{"nodes": nodes})
}

func emittedMCPEntries(t *testing.T, appConfig map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	tools, _ := appConfig["tools"].([]any)
	for _, raw := range tools {
		if entry, ok := raw.(map[string]any); ok && entry["tool_type"] == "mcp" {
			out = append(out, entry)
		}
	}
	return out
}

func TestMCPConnectorBinding_RewritesNodesAndEmitsTheStoredToolList(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", mcpValues(crmManifestJSON))

	appConfig := mcpToolNodes("find", "acme-crm", "find_customer", "orders", "acme-crm", "list_orders")
	if err := resolveAndHydrate(t, appConfig, repo); err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}

	nodes := toolNodeList(appConfig, t)
	for i, want := range []string{"find_customer", "list_orders"} {
		if nodes[i]["tool_name"] != want {
			t.Errorf("node %d tool_name = %v, want %q", i, nodes[i]["tool_name"], want)
		}
		if _, present := nodes[i]["connector"]; present {
			t.Errorf("node %d still carries the symbolic connector field", i)
		}
		if _, present := nodes[i]["tool"]; present {
			t.Errorf("node %d still carries the symbolic tool field", i)
		}
	}

	entries := emittedMCPEntries(t, appConfig)
	if len(entries) != 1 {
		t.Fatalf("emitted %d mcp entries for one connector, want 1", len(entries))
	}
	manifest, _ := entries[0]["manifest"].([]any)
	if len(manifest) != 3 {
		t.Errorf("emitted manifest has %d tools, want the connector's 3 stored tools", len(manifest))
	}
	if entries[0]["source_connector_id"] == "" {
		t.Error("the entry lost the connector id the runtime reports status against")
	}
}

func TestMCPConnectorBinding_PrefixAndSanitising(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "acme-crm", "mcp", "remote",
		mcpValues(`"tool_name_prefix":"crm","manifest":[{"name":"find-customer"}]`))

	appConfig := mcpToolNodes("find", "acme-crm", "find-customer")
	if err := resolveAndHydrate(t, appConfig, repo); err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}
	if got := toolNodeList(appConfig, t)[0]["tool_name"]; got != "crm_find_customer" {
		t.Errorf("tool_name = %v, want the name the runtime registers (prefix applied, name sanitised)", got)
	}
}

func TestMCPConnectorBinding_SaveChecksTheStoredToolList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		values  string
		tool    string
		wantErr []string
		wantOK  bool
	}{
		{name: "manifest has the tool", values: mcpValues(crmManifestJSON), tool: "get_order", wantOK: true},
		{name: "manifest lacks the tool", values: mcpValues(crmManifestJSON), tool: "list_invoices",
			wantErr: []string{`emits no tool named "list_invoices"`, "available: find_customer, list_orders, get_order"}},
		{name: "operator deselected the tool", values: mcpValues(crmManifestJSON + `,"selected_tools":["find_customer"]`), tool: "get_order",
			wantErr: []string{`emits no tool named "get_order"`, "available: find_customer"}},
		{name: "deny_list removes the tool", values: mcpValues(crmManifestJSON + `,"deny_list":"get_order"`), tool: "get_order",
			wantErr: []string{`emits no tool named "get_order"`, "available: find_customer, list_orders"}},
		{name: "no tool on a multi-tool manifest", values: mcpValues(crmManifestJSON), tool: "",
			wantErr: []string{"emits 3 tools", "set 'tool' to one of: find_customer, list_orders, get_order"}},
		{name: "no tool on a one-tool manifest", values: mcpValues(`"manifest":[{"name":"find_customer"}]`), tool: "", wantOK: true},
		{name: "tool_name outside a manifest binds nothing", values: mcpValues(crmManifestJSON + `,"tool_name":"zzz"`), tool: "zzz",
			wantErr: []string{"leave none of its stored tools"}},
		{name: "oauth connector pinned by tool_name with no manifest", values: mcpValues(`"auth_type":"oauth","tool_name":"find_customer"`), tool: "find_customer",
			wantErr: []string{"uses OAuth", "add a manifest", "remove its tool_name"}},
		{name: "oauth connector with a manifest", values: mcpValues(`"auth_type":"oauth",` + crmManifestJSON), tool: "find_customer", wantOK: true},
		{name: "oauth connector over sse pinned by tool_name", values: mcpValues(`"connection_type":"sse","auth_type":"oauth","tool_name":"find_customer"`), tool: "", wantOK: true},
		{name: "no stored tool list", values: mcpValues(""), tool: "find_customer",
			wantErr: []string{"no stored tool list", "re-discover"}},
		{name: "no stored tool list and an allow_list is not a tool list", values: mcpValues(`"allow_list":"find_customer"`), tool: "find_customer",
			wantErr: []string{"no stored tool list", "re-discover"}},
		{name: "tool_name filter narrows the manifest", values: mcpValues(crmManifestJSON + `,"tool_name":"find_customer"`), tool: "list_orders",
			wantErr: []string{`emits no tool named "list_orders"`, "available: find_customer"}},
		{name: "tool_name filter over a manifest selects its one tool", values: mcpValues(crmManifestJSON + `,"tool_name":"find_customer"`), tool: "", wantOK: true},
		{name: "allow_list narrows the manifest", values: mcpValues(crmManifestJSON + `,"allow_list":"find_customer,get_order"`), tool: "list_orders",
			wantErr: []string{`emits no tool named "list_orders"`, "available: find_customer, get_order"}},
		{name: "allow_list given as a JSON array narrows the manifest", values: mcpValues(crmManifestJSON + `,"allow_list":["find_customer","get_order"]`), tool: "list_orders",
			wantErr: []string{`emits no tool named "list_orders"`, "available: find_customer, get_order"}},
		{name: "allow_list keeps a tool the manifest has", values: mcpValues(crmManifestJSON + `,"allow_list":"find_customer,get_order"`), tool: "get_order", wantOK: true},
		{name: "single-tool filter with no manifest still binds", values: mcpValues(`"tool_name":"find_customer"`), tool: "", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := store.NewConnectorRepo(testOpenDB(t))
			createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", tc.values)
			appConfig := mcpToolNodes("call", "acme-crm", tc.tool)

			_, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("save refused a binding the connector allows: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("save accepted a binding the connector's stored tool list rules out")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to mention %q", err, want)
				}
			}
			if _, ok := errors.AsType[*ValidationError](err); !ok {
				t.Errorf("err is %T, want *ValidationError so the caller answers 422", err)
			}
			if !strings.Contains(err.Error(), `node "call"`) {
				t.Errorf("err = %v, want the offending node named", err)
			}
		})
	}
}

func TestMCPConnectorBinding_SingleToolFilterStillBinds(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", mcpValues(`"tool_name":"find_customer"`))

	appConfig := mcpToolNodes("find", "acme-crm", "")
	if err := resolveAndHydrate(t, appConfig, repo); err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}
	if got := toolNodeList(appConfig, t)[0]["tool_name"]; got != "find_customer" {
		t.Errorf("tool_name = %v, want find_customer", got)
	}
}

func TestMCPConnectorBinding_TwoConnectorsRegisteringOneNameAreRefused(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	const search = `"manifest":[{"name":"search"}]`
	createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", mcpValues(search))
	createHydrationConnector(t, repo, "acme-billing", "mcp", "remote",
		`{"server_url":"http://localhost:9998/mcp",`+search+`}`)

	clash := mcpToolNodes("a", "acme-crm", "search", "b", "acme-billing", "search")
	_, err := resolveWorkflowConnectorBindings(t.Context(), clash, nil, repo)
	if err == nil || !strings.Contains(err.Error(), `register as "search"`) {
		t.Fatalf("save should refuse two connectors registering one name, got %v", err)
	}

	createHydrationConnector(t, repo, "acme-billing-prefixed", "mcp", "remote",
		`{"server_url":"http://localhost:9997/mcp","tool_name_prefix":"billing",`+search+`}`)
	apart := mcpToolNodes("a", "acme-crm", "search", "b", "acme-billing-prefixed", "search")
	if err := resolveAndHydrate(t, apart, repo); err != nil {
		t.Fatalf("a prefix keeps the names apart and must deploy: %v", err)
	}
	nodes := toolNodeList(apart, t)
	if nodes[0]["tool_name"] != "search" || nodes[1]["tool_name"] != "billing_search" {
		t.Errorf("tool_names = %v, %v", nodes[0]["tool_name"], nodes[1]["tool_name"])
	}
}

func TestMCPConnectorBinding_UnboundToolOfAnotherConnectorStillClashes(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "alpha", "mcp", "remote",
		mcpValues(`"manifest":[{"name":"search"},{"name":"get"}]`))
	createHydrationConnector(t, repo, "beta", "mcp", "remote",
		`{"server_url":"http://localhost:9998/mcp","manifest":[{"name":"search"}]}`)

	appConfig := mcpToolNodes("n1", "alpha", "get", "n2", "beta", "search")
	_, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
	if err == nil || !strings.Contains(err.Error(), `register as "search"`) {
		t.Fatalf("a clash on a tool no node binds must still be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), `"alpha"`) || !strings.Contains(err.Error(), `"beta"`) {
		t.Errorf("err = %v, want both connectors named", err)
	}
}

func TestMCPConnectorBinding_ClashCountsWhatTheOtherConnectorRegisters(t *testing.T) {
	t.Parallel()
	const manifest = `"manifest":[{"name":"get_issue"},{"name":"other"}]`
	cases := []struct {
		name      string
		other     string
		wantClash bool
	}{
		{"unfiltered manifest", manifest, true},
		{"deny_list removes the name", manifest + `,"deny_list":"get_issue"`, false},
		{"allow_list without the name", manifest + `,"allow_list":"other"`, false},
		{"allow_list array with the name", manifest + `,"allow_list":["get_issue","other"]`, true},
		{"selected_tools without the name", manifest + `,"selected_tools":["other"]`, false},
		{"selected_tools with the name", manifest + `,"selected_tools":["get_issue","other"]`, true},
		{"tool_name pin elsewhere", manifest + `,"tool_name":"other"`, false},
		{"prefix moves the name", manifest + `,"tool_name_prefix":"docs"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := store.NewConnectorRepo(testOpenDB(t))
			createHydrationConnector(t, repo, "kb-mcp", "mcp", "remote", `{"server_url":"http://localhost:9999/mcp","manifest":[{"name":"get_issue"}]}`)
			createHydrationConnector(t, repo, "other-mcp", "mcp", "remote", `{"server_url":"http://localhost:9998/mcp",`+tc.other+`}`)
			appConfig := mcpToolNodes("n", "kb-mcp", "get_issue", "o", "other-mcp", "other")

			bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
			if !tc.wantClash {
				if err != nil {
					t.Fatalf("save refused a pair that registers different names: %v", err)
				}
				if err = hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil); err != nil {
					t.Fatalf("deploy refused a pair that registers different names: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), `register as "get_issue"`) {
				t.Fatalf("save should refuse an unbound tool that registers a bound name, got %v", err)
			}
		})
	}
}

func TestMCPConnectorBinding_SameToolTwiceFromOneConnectorBinds(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", mcpValues(crmManifestJSON))

	appConfig := mcpToolNodes("a", "acme-crm", "find_customer", "b", "acme-crm", "find_customer")
	if err := resolveAndHydrate(t, appConfig, repo); err != nil {
		t.Fatalf("two nodes calling one tool of one connector must save and deploy: %v", err)
	}
	nodes := toolNodeList(appConfig, t)
	if nodes[0]["tool_name"] != "find_customer" || nodes[1]["tool_name"] != "find_customer" {
		t.Errorf("tool_names = %v, %v", nodes[0]["tool_name"], nodes[1]["tool_name"])
	}
}

func TestMCPConnectorBinding_DeployRefusesAClashThatAppearedAfterSave(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	const search = `"manifest":[{"name":"search"}]`
	createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", mcpValues(search))
	billing := createHydrationConnector(t, repo, "acme-billing", "mcp", "remote",
		`{"server_url":"http://localhost:9998/mcp","tool_name_prefix":"billing",`+search+`}`)

	appConfig := mcpToolNodes("a", "acme-crm", "search", "b", "acme-billing", "search")
	bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err = repo.UpdateValues(t.Context(), billing.ID, `{"server_url":"http://localhost:9998/mcp",`+search+`}`); err != nil {
		t.Fatalf("update connector: %v", err)
	}
	err = hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil)
	if err == nil || !strings.Contains(err.Error(), `register as "search"`) {
		t.Fatalf("deploy should refuse two connectors that now register one name, got %v", err)
	}
}

func TestMCPConnectorBinding_NeverOpensAnInlineEntrysManifestFile(t *testing.T) {
	t.Parallel()
	listing := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(listing, []byte("- name: search\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{listing, "/does/not/exist.yaml"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			repo := store.NewConnectorRepo(testOpenDB(t))
			createHydrationConnector(t, repo, "alpha", "mcp", "remote", mcpValues(`"manifest":[{"name":"search"}]`))
			appConfig := mcpToolNodes("a", "alpha", "search")
			appConfig["tools"] = []any{map[string]any{
				"tool_type":         "mcp",
				"connection_params": map[string]any{"type": "streamable-http", "url": "http://localhost:9997/mcp"},
				"manifest_file":     path,
			}}
			if err := resolveAndHydrate(t, appConfig, repo); err != nil {
				t.Fatalf("binding beside an inline entry with manifest_file must save and deploy: %v", err)
			}
		})
	}
}

func TestMCPConnectorBinding_DeployRefusesAnUnboundToolThatAppearedAfterSave(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "alpha", "mcp", "remote", `{"server_url":"http://localhost:9999/mcp","manifest":[{"name":"search"}]}`)
	beta := createHydrationConnector(t, repo, "beta", "mcp", "remote", `{"server_url":"http://localhost:9998/mcp","manifest":[{"name":"other"}]}`)
	appConfig := mcpToolNodes("a", "alpha", "search", "b", "beta", "other")
	bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err = repo.UpdateValues(t.Context(), beta.ID, `{"server_url":"http://localhost:9998/mcp","manifest":[{"name":"other"},{"name":"search"}]}`); err != nil {
		t.Fatalf("update connector: %v", err)
	}
	err = hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil)
	if err == nil || !strings.Contains(err.Error(), `register as "search"`) {
		t.Fatalf("deploy should refuse a tool no node binds that takes a bound name, got %v", err)
	}
}

func TestMCPConnectorBinding_InlineMCPEntriesAreUntouched(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", mcpValues(crmManifestJSON))

	appConfig := mcpToolNodes("find", "acme-crm", "find_customer")
	nodes := appConfig["workflow"].(map[string]any)["nodes"].([]any)
	appConfig["workflow"].(map[string]any)["nodes"] = append(nodes,
		map[string]any{"id": "inline", "type": "tool", "tool_name": "search_repos"})
	inline := map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "streamable-http", "url": mcpServerURL},
	}
	appConfig["tools"] = []any{inline}
	if err := resolveAndHydrate(t, appConfig, repo); err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}
	if len(inline) != 2 {
		t.Errorf("the inline mcp entry was modified: %v", inline)
	}
	got := toolNodeList(appConfig, t)
	if got[0]["tool_name"] != "find_customer" || got[1]["tool_name"] != "search_repos" {
		t.Errorf("tool_names = %v, %v", got[0]["tool_name"], got[1]["tool_name"])
	}
}

func TestMCPConnectorBinding_OtherTypesKeepTheirRules(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	conn := createHydrationConnector(t, repo, "mailer", "email", "smtp", emailConnectorValues())

	appConfig := mcpToolNodes("send", "mailer", "send_email")
	if err := resolveAndHydrate(t, appConfig, repo); err != nil {
		t.Fatalf("an email connector bound by its base tool name must still deploy: %v", err)
	}
	if got := toolNodeList(appConfig, t)[0]["tool_name"]; got != "send_email_"+connectorSuffix(conn.ID) {
		t.Errorf("tool_name = %v", got)
	}
	if len(emittedMCPEntries(t, appConfig)) != 0 {
		t.Error("an email binding emitted an mcp entry")
	}

	wrong := mcpToolNodes("send", "mailer", "nonexistent")
	if err := resolveAndHydrate(t, wrong, repo); err == nil || !strings.Contains(err.Error(), "emits no tool named") {
		t.Fatalf("an email connector's unknown tool must keep its error, got %v", err)
	}
}

func TestMCPConnectorBinding_DeployRefusesWhenStoredToolListIsGone(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	conn := createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", mcpValues(crmManifestJSON))

	appConfig := mcpToolNodes("find", "acme-crm", "find_customer")
	bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err = repo.UpdateValues(t.Context(), conn.ID, mcpValues("")); err != nil {
		t.Fatalf("update connector: %v", err)
	}
	err = hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "re-discover") {
		t.Fatalf("deploy should refuse a connector with no stored tool list and say to re-discover, got %v", err)
	}
}

func TestMCPConnectorBinding_StoredToolListMustBeOneTheRuntimeAccepts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		values  string
		tool    string
		wantOK  bool
		wantErr []string
	}{
		{name: "manifest stored as a JSON string", values: mcpValues(`"manifest":"[{\"name\":\"find_customer\"}]"`), tool: "find_customer",
			wantErr: []string{`connector "acme-crm"`, "stored settings the runtime rejects", "manifest"}},
		{name: "manifest that is a map", values: mcpValues(`"manifest":{"name":"find_customer"}`), tool: "find_customer",
			wantErr: []string{"stored settings the runtime rejects"}},
		{name: "manifest entry that is not a map", values: mcpValues(`"manifest":["find_customer"]`), tool: "find_customer",
			wantErr: []string{"stored settings the runtime rejects"}},
		{name: "manifest entry with neither name nor id", values: mcpValues(`"manifest":[{"description":"x"}]`), tool: "x",
			wantErr: []string{"stored settings the runtime rejects"}},
		{name: "manifest entries naming tools by id", values: mcpValues(`"manifest":[{"id":"find_customer"},{"id":"list_orders"}]`), tool: "list_orders", wantOK: true},
		{name: "tool_name together with allow_list", values: mcpValues(`"manifest":[{"name":"a"}],"tool_name":"a","allow_list":"a"`), tool: "a",
			wantErr: []string{"stored settings the runtime rejects", "mutually exclusive"}},
		{name: "unsupported connection type", values: `{"server_url":"http://localhost:9999/mcp","connection_type":"carrier-pigeon","manifest":[{"name":"a"}]}`, tool: "a",
			wantErr: []string{"stored settings the runtime rejects"}},
		{name: "duplicate manifest names list once", values: mcpValues(`"manifest":[{"name":"a"},{"name":"a"}]`), tool: "", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := store.NewConnectorRepo(testOpenDB(t))
			createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", tc.values)
			appConfig := mcpToolNodes("n", "acme-crm", tc.tool)
			bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("save refused a stored list the runtime accepts: %v", err)
				}
				if err = hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil); err != nil {
					t.Fatalf("deploy refused a stored list the runtime accepts: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("save accepted a stored list the runtime rejects")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to mention %q", err, want)
				}
			}
		})
	}
}

func TestMCPConnectorBinding_NoToolListNamesTheRemedyThatWorks(t *testing.T) {
	t.Parallel()
	const manifest = `"manifest":[{"name":"a"},{"name":"b"}]`
	cases := []struct {
		name    string
		values  string
		wantErr []string
	}{
		{name: "oauth", values: mcpValues(`"auth_type":"oauth"`), wantErr: []string{"uses OAuth", "add a manifest"}},
		{name: "env references", values: `{"server_url":"${CRM_URL}"}`, wantErr: []string{"${...} references", "add a manifest"}},
		{name: "oauth with a filter set", values: mcpValues(`"auth_type":"oauth","allow_list":"a"`), wantErr: []string{"uses OAuth", "remove its tool_name, allow_list and deny_list"}},
		{name: "never discovered", values: mcpValues(""), wantErr: []string{"no stored tool list", "re-saving the connector while its MCP server is reachable"}},
		{name: "selection leaves nothing", values: mcpValues(manifest + `,"deny_list":"a,b"`), wantErr: []string{"leave none of its stored tools", "change that selection"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := store.NewConnectorRepo(testOpenDB(t))
			createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", tc.values)
			_, err := resolveWorkflowConnectorBindings(t.Context(), mcpToolNodes("n", "acme-crm", "a"), nil, repo)
			if err == nil {
				t.Fatal("save accepted a connector with no usable tool list")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to mention %q", err, want)
				}
			}
			if tc.name != "never discovered" && strings.Contains(err.Error(), "re-saving") {
				t.Errorf("err = %v, offers a remedy that cannot work for this cause", err)
			}
		})
	}
}

func TestMCPConnectorBinding_PinnedToolWithPrefixAgreesAtSaveAndDeploy(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "pin-a", "mcp", "remote", `{"server_url":"http://localhost:9999/mcp","tool_name":"search"}`)
	createHydrationConnector(t, repo, "pin-b", "mcp", "remote", `{"server_url":"http://localhost:9998/mcp","tool_name":"search"}`)
	pinB, err := repo.GetAllByName(t.Context(), "pin-b")
	if err != nil || len(pinB) != 1 {
		t.Fatalf("lookup: %v", err)
	}

	clash := mcpToolNodes("a", "pin-a", "", "b", "pin-b", "")
	if _, err = resolveWorkflowConnectorBindings(t.Context(), clash, nil, repo); err == nil || !strings.Contains(err.Error(), `register as "search"`) {
		t.Fatalf("two unprefixed pins on one name must be refused at save, got %v", err)
	}

	if err = repo.UpdateValues(t.Context(), pinB[0].ID, `{"server_url":"http://localhost:9998/mcp","tool_name":"search","tool_name_prefix":"billing"}`); err != nil {
		t.Fatalf("update connector: %v", err)
	}
	apart := mcpToolNodes("a", "pin-a", "", "b", "pin-b", "")
	if err = resolveAndHydrate(t, apart, repo); err != nil {
		t.Fatalf("the prefix the clash error asks for must be enough at save and at deploy: %v", err)
	}
	nodes := toolNodeList(apart, t)
	if nodes[0]["tool_name"] != "search" || nodes[1]["tool_name"] != "billing_search" {
		t.Errorf("tool_names = %v, %v", nodes[0]["tool_name"], nodes[1]["tool_name"])
	}
}

func TestMCPConnectorBinding_ClashNamesAStablePartnerAndCatchesSanitisedTwins(t *testing.T) {
	t.Parallel()
	t.Run("three connectors", func(t *testing.T) {
		t.Parallel()
		repo := store.NewConnectorRepo(testOpenDB(t))
		const search = `"manifest":[{"name":"search"}]`
		for _, name := range []string{"zeta", "alpha", "mid"} {
			createHydrationConnector(t, repo, name, "mcp", "remote", `{"server_url":"http://localhost:9990/`+name+`",`+search+`}`)
		}
		_, err := resolveWorkflowConnectorBindings(t.Context(), mcpToolNodes("n", "zeta", "search", "a", "alpha", "search", "m", "mid", "search"), nil, repo)
		if err == nil || !strings.Contains(err.Error(), `connector "zeta"'s tool "search" and connector "alpha"'s tool "search"`) {
			t.Fatalf("the clash must name the first partner in sorted order, got %v", err)
		}
	})
	t.Run("owner side sanitising", func(t *testing.T) {
		t.Parallel()
		repo := store.NewConnectorRepo(testOpenDB(t))
		createHydrationConnector(t, repo, "kb-mcp", "mcp", "remote", mcpValues(`"manifest":[{"name":"get_issue"}]`))
		createHydrationConnector(t, repo, "other", "mcp", "remote", `{"server_url":"http://localhost:9998/mcp","manifest":[{"name":"get-issue"},{"name":"x"}]}`)
		_, err := resolveWorkflowConnectorBindings(t.Context(), mcpToolNodes("n", "kb-mcp", "get_issue", "o", "other", "x"), nil, repo)
		if err == nil || !strings.Contains(err.Error(), `register as "get_issue"`) {
			t.Fatalf("a server name that sanitises onto a bound name must clash, got %v", err)
		}
	})
	t.Run("twins in one connector", func(t *testing.T) {
		t.Parallel()
		repo := store.NewConnectorRepo(testOpenDB(t))
		createHydrationConnector(t, repo, "twin", "mcp", "remote", mcpValues(`"manifest":[{"name":"get-item"},{"name":"get_item"}]`))
		_, err := resolveWorkflowConnectorBindings(t.Context(), mcpToolNodes("n", "twin", "get-item"), nil, repo)
		if err == nil || !strings.Contains(err.Error(), `register as "get_item"`) {
			t.Fatalf("two tools of one connector that register under one name must clash, got %v", err)
		}
		if !strings.Contains(err.Error(), "deselect one with selected_tools or deny_list") || strings.Contains(err.Error(), "set tool_name_prefix") {
			t.Errorf("err = %v, want the remedy that works for twins, which a prefix cannot separate", err)
		}
	})
}

func TestMCPConnectorBinding_ClashIsAValidationErrorNamingTheNode(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	const search = `"manifest":[{"name":"search"}]`
	createHydrationConnector(t, repo, "alpha", "mcp", "remote", mcpValues(search))
	createHydrationConnector(t, repo, "beta", "mcp", "remote", `{"server_url":"http://localhost:9998/mcp",`+search+`}`)
	_, err := resolveWorkflowConnectorBindings(t.Context(), mcpToolNodes("the-node", "alpha", "search"), nil, repo)
	if err != nil {
		t.Fatalf("a single bound connector must save: %v", err)
	}
	_, err = resolveWorkflowConnectorBindings(t.Context(), mcpToolNodes("the-node", "alpha", "search", "other", "beta", "search"), nil, repo)
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Fatalf("err is %T, want *ValidationError so the caller answers 422", err)
	}
	if !strings.Contains(err.Error(), `node "the-node"`) {
		t.Errorf("err = %v, want the node that would call the wrong tool named", err)
	}
}

func TestMCPConnectorBinding_DeployNamesTheRemedyThatWorks(t *testing.T) {
	t.Parallel()
	const manifest = `"manifest":[{"name":"a"},{"name":"b"}]`
	cases := []struct {
		name    string
		after   string
		wantErr []string
	}{
		{"oauth", mcpValues(`"auth_type":"oauth"`), []string{"uses OAuth", "add a manifest"}},
		{"oauth pinned by tool_name", mcpValues(`"auth_type":"oauth","tool_name":"a"`), []string{"uses OAuth", "remove its tool_name"}},
		{"env references", `{"server_url":"${CRM_URL}"}`, []string{"${...} references"}},
		{"selection leaves nothing", mcpValues(manifest + `,"deny_list":"a,b"`), []string{"leave none of its stored tools"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := store.NewConnectorRepo(testOpenDB(t))
			conn := createHydrationConnector(t, repo, "acme-crm", "mcp", "remote", mcpValues(manifest))
			appConfig := mcpToolNodes("n", "acme-crm", "a")
			bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
			if err != nil {
				t.Fatalf("save: %v", err)
			}
			if err = repo.UpdateValues(t.Context(), conn.ID, tc.after); err != nil {
				t.Fatalf("update connector: %v", err)
			}
			err = hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil)
			if err == nil {
				t.Fatal("deploy accepted a connector with no usable tool list")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to mention %q", err, want)
				}
			}
		})
	}
}
