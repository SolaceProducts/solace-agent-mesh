// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestWorkflowConnectors_HydratedToolBootsGatedWithTheNodesToolName(t *testing.T) {
	t.Parallel()
	gate := func(role string) string {
		return `"hil":{"tools":{"` + role + `":{"require_approval":true,"approval_message":"Run it?"}}}`
	}
	for name, tc := range map[string]struct {
		connType, subtype, values string
	}{
		"email": {"email", "smtp", `{"smtp_host":"smtp.example.com","smtp_port":"2525","smtp_tls":"none","smtp_auth_type":"none","envelope_from_none":"noreply@example.com","recipient_allowlist":"example.com",` + gate("send_email") + `}`},
		"sql":   {"sql", "postgres", `{"hostname":"db.example.com","port":5432,"database":"app","username":"u","password":"p",` + gate("sql_query") + `}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := store.NewConnectorRepo(testOpenDB(t))
			createHydrationConnector(t, repo, "gated", tc.connType, tc.subtype, tc.values)
			appConfig := wrappedAppConfig(map[string]any{
				"output_mapping": map[string]any{"result": "{{nodes.run.output}}"},
				"nodes":          []any{map[string]any{"id": "run", "type": "tool", "connector": "gated"}},
			})
			if err := resolveAndHydrate(t, appConfig, repo); err != nil {
				t.Fatalf("resolve+hydrate: %v", err)
			}
			nodeTool, _ := toolNodeList(appConfig, t)[0]["tool_name"].(string)
			hydrated, _ := appConfig["tools"].([]any)
			entries := make([]map[string]any, 0, len(hydrated))
			for _, raw := range hydrated {
				entries = append(entries, raw.(map[string]any))
			}

			gates := bootGateEntries(t, entries)

			if gate := gates[nodeTool]; !gate.RequireApproval || gate.ApprovalMessage != "Run it?" {
				t.Errorf("the node's tool %q is not gated; gates = %v", nodeTool, gates)
			}
		})
	}
}
