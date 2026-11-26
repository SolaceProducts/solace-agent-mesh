// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"strings"
	"testing"
)

var getShipmentTarget = ToolInputTarget{
	NodeID:    "ship",
	Connector: "carrier-api",
	ToolName:  "get_shipment",
	Params: []ToolParam{
		{Name: "tracking", Required: true},
		{Name: "carrier_code"},
	},
}

func TestToolInputProblem_FatalWhenARequiredParameterIsNotNamed(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
		want  []string
	}{
		{
			name:  "an invented key in place of the declared one",
			input: map[string]any{"tracking_number": "{{workflow.input.tracking}}"},
			want: []string{
				`tool node "ship" calls operation "get_shipment" of connector "carrier-api"`,
				"required input parameter(s) tracking",
				"passes tracking_number, which it does not declare",
				"tracking (required)", "carrier_code (optional)",
				"mandatory input parameters are not present",
			},
		},
		{
			name:  "only optional parameters named",
			input: map[string]any{"carrier_code": "ups"},
			want:  []string{"required input parameter(s) tracking"},
		},
		{
			name:  "an empty input names nothing",
			input: map[string]any{},
			want:  []string{"required input parameter(s) tracking"},
		},
		{
			name:  "every undeclared key is named",
			input: map[string]any{"b_extra": 1, "a_extra": 2},
			want:  []string{"passes a_extra, b_extra, which it does not declare"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ToolInputProblem(getShipmentTarget, tc.input)
			if p == nil {
				t.Fatal("want a problem, got none")
			}
			if !p.Fatal {
				t.Error("a missing required parameter fails every run, so it must be Fatal")
			}
			for _, want := range tc.want {
				if !strings.Contains(p.Message, want) {
					t.Errorf("message %q is missing %q", p.Message, want)
				}
			}
		})
	}
}

func TestToolInputProblem_AnUndeclaredKeyAloneIsReportedNotFatal(t *testing.T) {
	p := ToolInputProblem(getShipmentTarget, map[string]any{"tracking": "{{workflow.input.tracking}}", "tracking_number": "x"})
	if p == nil {
		t.Fatal("want a problem, got none")
	}
	if p.Fatal {
		t.Error("a key the operation does not declare is dropped while the call goes out, so it must not be Fatal")
	}
	for _, want := range []string{"input key(s) tracking_number", "silently dropped", "tracking (required)"} {
		if !strings.Contains(p.Message, want) {
			t.Errorf("message %q is missing %q", p.Message, want)
		}
	}
}

func TestToolInputProblem_NothingToReport(t *testing.T) {
	cases := map[string]map[string]any{
		"no input: at all, so the node is fed its dependency's output": nil,
		"coalesce as the whole input":                                  {"coalesce": []any{"{{a.output}}", "{{b.output}}"}},
		"concat as the whole input":                                    {"concat": []any{"{{a.output.x}}", "-"}},
		"the declared required key with a template":                    {"tracking": "{{workflow.input.tracking}}"},
		"the declared required key with a literal":                     {"tracking": "1Z999"},
		"the declared required key with a null the engine may drop":    {"tracking": nil},
		"required and optional keys together":                          {"tracking": "{{workflow.input.t}}", "carrier_code": "ups"},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if p := ToolInputProblem(getShipmentTarget, input); p != nil {
				t.Fatalf("want nothing, got %q", p.Message)
			}
		})
	}
}

func TestToolInputProblem_OperationsWithABodyOrNoParameters(t *testing.T) {
	create := ToolInputTarget{
		NodeID: "create", Connector: "carrier-api", ToolName: "create_shipment",
		Params: []ToolParam{{Name: "destination", Required: true}, {Name: "note"}},
	}
	if p := ToolInputProblem(create, map[string]any{"destination": "{{workflow.input.to}}"}); p != nil {
		t.Errorf("a body's required property named, optional omitted: want nothing, got %q", p.Message)
	}
	if p := ToolInputProblem(create, map[string]any{"note": "x"}); p == nil || !p.Fatal {
		t.Errorf("a body's required property not named: want Fatal, got %v", p)
	}

	ping := ToolInputTarget{NodeID: "ping", Connector: "carrier-api", ToolName: "ping"}
	if p := ToolInputProblem(ping, map[string]any{}); p != nil {
		t.Errorf("no parameters and an empty input: want nothing, got %q", p.Message)
	}
	p := ToolInputProblem(ping, map[string]any{"x": 1})
	if p == nil || p.Fatal || !strings.Contains(p.Message, "none") {
		t.Errorf("a key for a parameterless operation: want a problem naming none, not fatal, got %v", p)
	}
}

func TestToolInputProblem_ToolDeclaredInToolsListHasNoConnectorInTheSubject(t *testing.T) {
	p := ToolInputProblem(ToolInputTarget{NodeID: "n", ToolName: "t", Params: []ToolParam{{Name: "a", Required: true}}}, map[string]any{})
	if p == nil || strings.Contains(p.Message, "connector") || !strings.Contains(p.Message, `calls tool "t"`) {
		t.Fatalf("message = %v", p)
	}
}
