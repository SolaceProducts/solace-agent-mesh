// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func appConfigFromYAML(t *testing.T, doc string) map[string]any {
	t.Helper()
	var appConfig map[string]any
	if err := yaml.Unmarshal([]byte(doc), &appConfig); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return appConfig
}

const loadableDecisionBody = `
decision: {model_alias: ticket-router}
output_mapping: {department: "{{triage.output.answers.department.choice}}"}
nodes:
  - id: triage
    type: decision
    input: {ticket: "{{workflow.input.ticket}}"}
    questions:
      department: {type: choice, instructions: "Which team owns ` + "`ticket`" + `?", criteria: {billing: x, technical: y, general: z}}
    route_on: department
    routes: {billing: billing_desk, technical: tech_desk}
    default: general_desk
  - {id: billing_desk, type: agent, agent_name: Billing, depends_on: [triage]}
  - {id: tech_desk, type: agent, agent_name: Tech, depends_on: [triage]}
  - {id: general_desk, type: agent, agent_name: General, depends_on: [triage]}
`

func TestCheckLoadable(t *testing.T) {
	t.Run("a valid decision workflow loads, nested under workflow: or flat", func(t *testing.T) {
		body := appConfigFromYAML(t, loadableDecisionBody)
		if err := CheckLoadable("Triage", body); err != nil {
			t.Fatalf("flat body: %v", err)
		}
		if err := CheckLoadable("Triage", map[string]any{"name": "Triage", "workflow": body}); err != nil {
			t.Fatalf("nested body: %v", err)
		}
	})
	t.Run("an option with no route and no default fails the load", func(t *testing.T) {
		body := appConfigFromYAML(t, strings.Replace(loadableDecisionBody, "    default: general_desk\n", "", 1))
		err := CheckLoadable("Triage", body)
		if err == nil || !strings.Contains(err.Error(), "no route") {
			t.Fatalf("err = %v, want the uncovered option reported", err)
		}
	})
	t.Run("an escalate target nothing reaches fails the load", func(t *testing.T) {
		doc := strings.Replace(loadableDecisionBody, "    default: general_desk\n", "    default: general_desk\n    escalate: general_desk\n", 1)
		err := CheckLoadable("Triage", appConfigFromYAML(t, doc))
		if err == nil || !strings.Contains(err.Error(), "escalate") {
			t.Fatalf("err = %v, want the unreachable escalation reported", err)
		}
	})
	t.Run("a route target that does not depend on its decision fails the load", func(t *testing.T) {
		doc := strings.Replace(loadableDecisionBody,
			"{id: tech_desk, type: agent, agent_name: Tech, depends_on: [triage]}",
			"{id: tech_desk, type: agent, agent_name: Tech}", 1)
		err := CheckLoadable("Triage", appConfigFromYAML(t, doc))
		if err == nil || !strings.Contains(err.Error(), "build DAG") || !strings.Contains(err.Error(), "tech_desk") {
			t.Fatalf("err = %v, want the missing branch dependency reported", err)
		}
	})
	t.Run("a dependency cycle fails the load", func(t *testing.T) {
		doc := loadableDecisionBody + "  - {id: orphan, type: agent, agent_name: Orphan, depends_on: [orphan_parent]}\n" +
			"  - {id: orphan_parent, type: agent, agent_name: Parent, depends_on: [orphan]}\n"
		err := CheckLoadable("Triage", appConfigFromYAML(t, doc))
		if err == nil || !strings.Contains(err.Error(), "dependency cycle") {
			t.Fatalf("err = %v, want the cycle reported", err)
		}
	})
	t.Run("a decision node with no decision block fails the load", func(t *testing.T) {
		doc := strings.Replace(loadableDecisionBody, "decision: {model_alias: ticket-router}\n", "", 1)
		err := CheckLoadable("Triage", appConfigFromYAML(t, doc))
		if !errors.Is(err, errDecisionProviderMissing) {
			t.Fatalf("err = %v, want the missing decision block reported", err)
		}
	})
	t.Run("an app-level decision block beside a nested body counts, and is parsed", func(t *testing.T) {
		body := appConfigFromYAML(t, strings.Replace(loadableDecisionBody, "decision: {model_alias: ticket-router}\n", "", 1))
		operator := map[string]any{"model_alias": "ticket-router"}
		if err := CheckLoadable("Triage", map[string]any{"decision": operator, "workflow": body}); err != nil {
			t.Fatalf("err = %v, want the app-level block to satisfy the decision node", err)
		}
		err := CheckLoadable("Triage", map[string]any{"decision": map[string]any{"provider": "nope"}, "workflow": body})
		if err == nil || !strings.Contains(err.Error(), "unknown provider") {
			t.Fatalf("err = %v, want the bad app-level block reported", err)
		}
	})
	t.Run("a decision block that names no endpoint fails the load", func(t *testing.T) {
		for _, block := range []string{"decision: {}\n", "decision: {timeout_seconds: 5}\n", "decision: {api_base: \"https://jev.example\"}\n"} {
			doc := strings.Replace(loadableDecisionBody, "decision: {model_alias: ticket-router}\n", block, 1)
			if err := CheckLoadable("Triage", appConfigFromYAML(t, doc)); !errors.Is(err, errDecisionProviderNamesNoEndpoint) {
				t.Errorf("%s: err = %v, want the endpoint-less block reported", strings.TrimSpace(block), err)
			}
		}
		inline := strings.Replace(loadableDecisionBody, "decision: {model_alias: ticket-router}\n", "decision: {api_key: \"${TYPESAFE_API_KEY}\"}\n", 1)
		if err := CheckLoadable("Triage", appConfigFromYAML(t, inline)); err != nil {
			t.Errorf("inline block with a ${VAR} key: err = %v, want it accepted", err)
		}
	})
	t.Run("an app-level block is merged over the body's as the AWE merges it", func(t *testing.T) {
		body := appConfigFromYAML(t, loadableDecisionBody)
		if err := CheckLoadable("Triage", map[string]any{"decision": map[string]any{"timeout_seconds": 5}, "workflow": body}); err != nil {
			t.Errorf("tune-only app-level block: err = %v, want the body's model_alias kept", err)
		}
		err := CheckLoadable("Triage", map[string]any{"decision": map[string]any{"model": "jev-2"}, "workflow": body})
		if err == nil || !strings.Contains(err.Error(), "model_alias") {
			t.Errorf("app-level model over a body model_alias: err = %v, want the AWE's conflict reported", err)
		}
		if strings.Contains(err.Error(), "app_config the app_config") {
			t.Errorf("conflict error %q carries a doubled app_config prefix", err)
		}
		err = CheckLoadable("Triage", map[string]any{"decision": map[string]any{"api_base": "https://jev.example"}, "workflow": body})
		if !errors.Is(err, errDecisionProviderNamesNoEndpoint) || !strings.Contains(err.Error(), "replaced the workflow's decision block") {
			t.Errorf("app-level api_base replacing the body block: err = %v, want the endpoint-less app_config block named", err)
		}
	})
	t.Run("a missing dependency is tagged for the Builder and says what it said before", func(t *testing.T) {
		doc := strings.Replace(loadableDecisionBody, "agent_name: Tech, depends_on: [triage]}", "agent_name: Tech, depends_on: [triage, nope]}", 1)
		err := CheckLoadable("Triage", appConfigFromYAML(t, doc))
		if !errors.Is(err, tools.ErrWorkflowMissingDependency) || !strings.Contains(err.Error(), `node "tech_desk" depends on non-existent node "nope"`) {
			t.Fatalf("err = %v, want the tagged missing dependency", err)
		}
		uncovered := strings.Replace(loadableDecisionBody, "    default: general_desk\n", "", 1)
		if err := CheckLoadable("Triage", appConfigFromYAML(t, uncovered)); errors.Is(err, tools.ErrWorkflowMissingDependency) {
			t.Errorf("err = %v tagged as a missing dependency", err)
		}
		unwired := strings.Replace(loadableDecisionBody, "agent_name: Billing, depends_on: [triage]}", "agent_name: Billing}", 1)
		unwiredErr := CheckLoadable("Triage", appConfigFromYAML(t, unwired))
		if unwiredErr == nil || errors.Is(unwiredErr, tools.ErrWorkflowMissingDependency) {
			t.Errorf("route target that does not depend on its decision: err = %v, want a load error not tagged as a missing dependency", unwiredErr)
		}
	})
	t.Run("a workflow key that is not a map fails the load", func(t *testing.T) {
		appConfig := appConfigFromYAML(t, loadableDecisionBody)
		appConfig["workflow"] = nil
		if err := CheckLoadable("Triage", appConfig); err == nil {
			t.Fatal("workflow: null loaded, want it refused like the deploy-time wrap refuses it")
		}
	})
	t.Run("a connector-only tool node, hydrated at deploy, does not fail the check", func(t *testing.T) {
		doc := `
output_mapping: {rows: "{{query.output}}"}
nodes:
  - {id: query, type: tool, connector: Warehouse}
`
		if err := CheckLoadable("Report", appConfigFromYAML(t, doc)); err != nil {
			t.Fatalf("err = %v, want the connector node accepted pending hydration", err)
		}
	})
}

const mcpToolNodeBody = `
output_mapping: {result: "{{weather.output}}"}
tools:
  - tool_type: mcp
    %s
    connection_params: {type: streamable-http, url: "http://localhost:9999/mcp"}
nodes:
  - {id: weather, type: tool, tool_name: get_weather}
`

func TestCheckLoadable_UnknownToolNames(t *testing.T) {
	t.Run("a pinned entry that does not register the name is refused and tagged", func(t *testing.T) {
		err := CheckLoadable("Weather", appConfigFromYAML(t, strings.Replace(mcpToolNodeBody, "%s", "tool_name: get_forecast", 1)))
		if !errors.Is(err, tools.ErrWorkflowFailsAtRun) || !strings.Contains(err.Error(), "get_forecast") {
			t.Fatalf("err = %v, want a failure-at-run error listing the registered names", err)
		}
	})
	t.Run("an entry that names no tools is not refused", func(t *testing.T) {
		if err := CheckLoadable("Weather", appConfigFromYAML(t, strings.Replace(mcpToolNodeBody, "%s", "tool_name_prefix: wx", 1))); err != nil {
			t.Fatalf("err = %v, want the unpinned entry left alone", err)
		}
	})
}
