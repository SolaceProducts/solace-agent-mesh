// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/connector"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/tool/email"
)

func boundNodes(connectorName string) []any {
	return []any{map[string]any{"id": "query_customer_data", "type": "tool", "connector": connectorName}}
}

func TestRestatedConnectorEntries_FlagsEntryRestatingNodeConnector(t *testing.T) {
	t.Parallel()
	sqlEntry := func() map[string]any {
		return map[string]any{"name": "Mysql-demo-db", "type": "sql", "subtype": "mysql"}
	}
	for _, tc := range []struct {
		name      string
		appConfig map[string]any
		wantPath  string
	}{
		{
			name:      "flat",
			appConfig: map[string]any{"connectors": []any{sqlEntry()}, "nodes": boundNodes("Mysql-demo-db")},
			wantPath:  "connectors[0]",
		},
		{
			name: "nested body",
			appConfig: map[string]any{"workflow": map[string]any{
				"connectors": []any{sqlEntry()}, "nodes": boundNodes("Mysql-demo-db"),
			}},
			wantPath: "workflow/connectors[0]",
		},
		{
			name: "nested app level",
			appConfig: map[string]any{
				"connectors": []any{sqlEntry()},
				"workflow":   map[string]any{"nodes": boundNodes("Mysql-demo-db")},
			},
			wantPath: "connectors[0]",
		},
		{
			name: "non-engine type carrying config",
			appConfig: map[string]any{
				"connectors": []any{map[string]any{
					"name": "Mysql-demo-db", "type": "sql", "subtype": "mysql",
					"config": map[string]any{"hostname": "db", "database": "demo"},
				}},
				"nodes": boundNodes("Mysql-demo-db"),
			},
			wantPath: "connectors[0]",
		},
		{
			name: "engine type with empty config",
			appConfig: map[string]any{
				"connectors": []any{map[string]any{"name": "Mysql-demo-db", "type": "email", "config": map[string]any{}}},
				"nodes":      boundNodes("Mysql-demo-db"),
			},
			wantPath: "connectors[0]",
		},
		{
			name: "engine type with null config",
			appConfig: map[string]any{
				"connectors": []any{map[string]any{"name": "Mysql-demo-db", "type": "email", "config": nil}},
				"nodes":      boundNodes("Mysql-demo-db"),
			},
			wantPath: "connectors[0]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := restatedConnectorEntries(tc.appConfig)
			want := []restatedConnectorEntry{{path: tc.wantPath, name: "Mysql-demo-db", nodeID: "query_customer_data"}}
			if !slices.Equal(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestRestatedConnectorEntries_IgnoresInlineAndUnboundEntries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		appConfig map[string]any
	}{
		{
			name: "inline engine connector a node calls by tool_name",
			appConfig: map[string]any{
				"connectors": []any{map[string]any{"name": "mailer", "type": "email", "config": map[string]any{"smtp_host": "x"}}},
				"nodes":      []any{map[string]any{"id": "send", "type": "tool", "tool_name": "send_email"}},
			},
		},
		{
			name: "configured inline engine connector sharing a bound name",
			appConfig: map[string]any{
				"connectors": []any{map[string]any{"name": "notify", "type": "email", "config": map[string]any{"smtp_host": "x"}}},
				"nodes": []any{
					map[string]any{"id": "a", "type": "tool", "connector": "notify"},
					map[string]any{"id": "b", "type": "tool", "tool_name": "send_email"},
				},
			},
		},
		{
			name: "node binds a different connector",
			appConfig: map[string]any{
				"connectors": []any{map[string]any{"name": "mailer", "type": "email"}},
				"nodes":      []any{map[string]any{"id": "q", "type": "tool", "connector": "warehouse"}},
			},
		},
		{
			name: "connector key on a non-tool node is not a binding",
			appConfig: map[string]any{
				"connectors": []any{map[string]any{"name": "warehouse", "type": "sql"}},
				"nodes":      []any{map[string]any{"id": "a", "type": "agent", "connector": "warehouse"}},
			},
		},
		{
			name:      "node binding without a workflow-level list",
			appConfig: map[string]any{"nodes": boundNodes("warehouse")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := restatedConnectorEntries(tc.appConfig); len(got) != 0 {
				t.Errorf("flagged %+v, want nothing", got)
			}
			if err := WorkflowPlatformConnectorEntryError(tc.appConfig); err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

func TestWorkflowPlatformConnectorEntryError_NamesEntryAndNode(t *testing.T) {
	t.Parallel()
	err := WorkflowPlatformConnectorEntryError(map[string]any{
		"connectors": []any{
			map[string]any{"name": "Mysql-demo-db", "type": "sql"},
			map[string]any{"name": "crm", "type": "sql"},
		},
		"nodes": []any{
			map[string]any{"id": "q1", "type": "tool", "connector": "Mysql-demo-db"},
			map[string]any{"id": "q2", "type": "tool", "connector": "crm"},
		},
	})
	if err == nil {
		t.Fatal("want an error for both restated connectors")
	}
	for _, want := range []string{`connectors[0]: "Mysql-demo-db"`, `tool node "q1"`, `connectors[1]: "crm"`, `tool node "q2"`, "remove it from the workflow-level `connectors:`"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

func TestDropWorkflowPlatformConnectorEntries_RemovesRestatementsOnly(t *testing.T) {
	t.Parallel()
	inline := func(name string) map[string]any {
		return map[string]any{"name": name, "type": "email", "config": map[string]any{"smtp_host": "x"}}
	}
	restated := func() map[string]any { return map[string]any{"name": "Mysql-demo-db", "type": "sql"} }
	body := map[string]any{
		"connectors": []any{restated(), inline("b"), inline("c")},
		"nodes":      boundNodes("Mysql-demo-db"),
	}
	appConfig := map[string]any{
		"connectors": []any{inline("a"), restated()},
		"workflow":   body,
	}

	dropped := DropWorkflowPlatformConnectorEntries(appConfig)
	if !slices.Equal(dropped, []string{"connectors[1]", "workflow/connectors[0]"}) {
		t.Fatalf("dropped %v, want the restatement's path at both levels", dropped)
	}
	names := func(list any) []string {
		var out []string
		for _, raw := range list.([]any) {
			out = append(out, raw.(map[string]any)["name"].(string))
		}
		return out
	}
	if got := names(appConfig["connectors"]); !slices.Equal(got, []string{"a"}) {
		t.Errorf("app-level connectors = %v, want [a]", got)
	}
	if got := names(body["connectors"]); !slices.Equal(got, []string{"b", "c"}) {
		t.Errorf("body connectors = %v, want [b c] in order", got)
	}
	if again := DropWorkflowPlatformConnectorEntries(appConfig); len(again) != 0 {
		t.Errorf("second drop removed %v, want nothing left to drop", again)
	}

	only := map[string]any{"connectors": []any{restated()}, "nodes": boundNodes("Mysql-demo-db")}
	DropWorkflowPlatformConnectorEntries(only)
	if got, present := only["connectors"]; present {
		t.Errorf("connectors = %v, want the key removed once empty", got)
	}
}

func TestValidateConfig_RejectsWorkflowLevelEntryForBoundPlatformConnector(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		src      string
		wantPath string
	}{
		{
			name: "flat",
			src: `apps:
  - name: fast-recommendation
    app_config:
      agent_name: fast-recommendation
      agent_type: workflow
      description: "Builder output that restates the node's platform connector"
      connectors:
        - name: Mysql-demo-db
          subtype: mysql
          type: sql
      nodes:
        - id: query_customer_data
          type: tool
          connector: Mysql-demo-db
          input:
            query: "SELECT 1"
      output_mapping:
        rows: "{{query_customer_data.output}}"
`,
			wantPath: "apps[0].app_config/connectors[0]",
		},
		{
			name: "nested",
			src: `apps:
  - name: fast-recommendation
    app_config:
      agent_name: fast-recommendation
      agent_type: workflow
      workflow:
        description: "Builder output that restates the node's platform connector"
        connectors:
          - name: Mysql-demo-db
            type: sql
        nodes:
          - id: query_customer_data
            type: tool
            connector: Mysql-demo-db
            input:
              query: "SELECT 1"
        output_mapping:
          rows: "{{query_customer_data.output}}"
`,
			wantPath: "apps[0].app_config/workflow/connectors[0]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := ValidateConfig([]byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range r.Errors {
				if e.Path != tc.wantPath {
					continue
				}
				if !strings.Contains(e.Message, "restates the platform connector") {
					t.Errorf("error at %s is %q, want the restated-connector message", e.Path, e.Message)
				}
				if strings.Contains(e.Message, "connectors[0]") {
					t.Errorf("message %q repeats the path already in Path", e.Message)
				}
				return
			}
			t.Fatalf("no error at %s; errors: %v", tc.wantPath, r.Errors)
		})
	}
}

func TestEngineConnectorTypesMatchRegistry(t *testing.T) {
	t.Parallel()
	got := connector.DefaultRegistry().RegisteredTypes()
	want := slices.Sorted(maps.Keys(engineConnectorTypes))
	if !slices.Equal(got, want) {
		t.Errorf("engine registry types = %v, engineConnectorTypes = %v; keep them in step", got, want)
	}
}
