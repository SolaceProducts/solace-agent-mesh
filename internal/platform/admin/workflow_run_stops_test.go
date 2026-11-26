// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

func failingNodeShapes(ship, main map[string]any) []struct {
	name    string
	body    map[string]any
	refused bool
} {
	shipAfter := func(extra map[string]any) map[string]any {
		node := map[string]any{}
		for k, v := range ship {
			node[k] = v
		}
		for k, v := range extra {
			node[k] = v
		}
		return node
	}
	return []struct {
		name    string
		body    map[string]any
		refused bool
	}{
		{"nothing keeps it from stopping the run", map[string]any{"nodes": []any{ship}}, true},
		{"fail_fast is false, which does not keep the node from stopping a run", map[string]any{"fail_fast": false, "nodes": []any{ship}}, true},
		{"fail_fast is a string, which the engine does not read", map[string]any{"fail_fast": "false", "nodes": []any{ship}}, true},
		{"an on_exit handler", map[string]any{
			"on_exit": map[string]any{"always": "ship"},
			"nodes":   []any{main, shipAfter(map[string]any{"depends_on": []any{"main"}})},
		}, false},
		{"a when: that is the literal false", map[string]any{"nodes": []any{shipAfter(map[string]any{"when": "false"})}}, false},
		{"a when: that reads data", map[string]any{"nodes": []any{shipAfter(map[string]any{"when": "{{workflow.input.go}} == true"})}}, true},
		{"the template of a map over a literal empty list", map[string]any{"nodes": []any{
			map[string]any{"id": "each", "type": "map", "node": "ship", "items": []any{}},
			shipAfter(map[string]any{"depends_on": []any{"each"}}),
		}}, false},
		{"the template of a map over a list that is not empty", map[string]any{"nodes": []any{
			map[string]any{"id": "each", "type": "map", "node": "ship", "items": []any{"a"}},
			shipAfter(map[string]any{"depends_on": []any{"each"}}),
		}}, true},
	}
}

func connectorMainNode() map[string]any {
	node := shipNode("get_shipment", map[string]any{"tracking": "x"})
	node["id"] = "main"
	return node
}

func (f *inputCheckFixture) createBody(t *testing.T, body map[string]any) error {
	t.Helper()
	f.counter++
	body["output_mapping"] = map[string]any{"result": "done"}
	_, err := f.svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "run-stops-flow-" + string(rune('a'+f.counter)),
		Description: "a workflow with a node that cannot run",
		AppConfig:   body,
	}, "u")
	return err
}

func TestWorkflowSave_ARequiredParameterLeftOutRefusesOnlyWhereItCanStopARun(t *testing.T) {
	t.Parallel()
	ship := shipNode("get_shipment", map[string]any{"carrier_code": "ups"})
	for _, tc := range failingNodeShapes(ship, connectorMainNode()) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			f.shipmentConnector(t, "carrier-api")

			err := f.createBody(t, tc.body)
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "required input parameter(s) tracking") {
					t.Fatalf("save err = %v, want the missing required parameter refused", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("save refused a node whose failure cannot stop a run: %v", err)
			}
			line := findLog(t, f.logs, "cannot stop a run")
			assertLogFields(t, line, map[string]any{"level": "WARN", "nodeID": "ship", "connector": "carrier-api"})
			if why, _ := line["why"].(string); why == "" {
				t.Errorf("the log names no reason the node cannot stop a run: %v", line)
			}
		})
	}
}

func TestWorkflowSave_AnAllowListEntryTheSpecLacksRefusesOnlyWhereItCanStopARun(t *testing.T) {
	t.Parallel()
	ship := shipNode("ghost_op", nil)
	for _, tc := range failingNodeShapes(ship, connectorMainNode()) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			f.allowListConnector(t, allowListNamesGhost)

			err := f.createBody(t, tc.body)
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), `allow_list entry "ghostOp"`) {
					t.Fatalf("save err = %v, want the allow_list entry refused", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("save refused a node whose failure cannot stop a run: %v", err)
			}
			findLog(t, f.logs, "cannot stop a run")
		})
	}
}

func TestControlAction_ReportsAFailingNodeThatCannotStopARunAsAWarning(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")
	ship := shipNode("get_shipment", map[string]any{"carrier_code": "ups"})

	for _, tc := range failingNodeShapes(ship, connectorMainNode()) {
		t.Run(tc.name, func(t *testing.T) {
			report := f.svc.CheckConnectorToolInputs(t.Context(), tc.body)
			var found bool
			for _, p := range report.Problems {
				if p.NodeID != "ship" {
					continue
				}
				found = true
				if p.Fatal != tc.refused {
					t.Errorf("Fatal = %v, want %v: %+v", p.Fatal, tc.refused, p)
				}
				if !p.Fatal && !strings.Contains(p.Message, "does not refuse the workflow") {
					t.Errorf("message %q does not say the workflow is not refused for it", p.Message)
				}
				if !strings.Contains(p.Message, "tracking") {
					t.Errorf("message %q does not name the missing parameter", p.Message)
				}
			}
			if !found {
				t.Fatalf("no problem reported for ship: %+v", report.Problems)
			}
		})
	}
}

func TestUnknownToolNodeMessages_RefuseOnlyWhereTheNodeCanStopARun(t *testing.T) {
	t.Parallel()
	ship := map[string]any{"id": "ship", "type": "tool", "tool_name": "track_package"}
	tools := []any{map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"}}
	for _, tc := range failingNodeShapes(ship, map[string]any{"id": "main", "type": "tool", "tool_name": "get_current_time"}) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.body["tools"] = tools
			messages := unknownToolNodeMessages("flow", tc.body)
			if refused := len(messages) > 0; refused != tc.refused {
				t.Fatalf("refused = %v, want %v: %v", refused, tc.refused, messages)
			}
			problems := workflow.UnknownToolNodeProblems(tc.body)
			if len(problems) != 1 || problems[0].NodeID != "ship" {
				t.Fatalf("problems = %+v, want one on ship", problems)
			}
			if !tc.refused && problems[0].NotFatalBecause == "" {
				t.Errorf("problem %+v does not say why it is let through", problems[0])
			}
		})
	}
}
