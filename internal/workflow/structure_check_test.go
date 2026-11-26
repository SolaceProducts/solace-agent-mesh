// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func structureAppConfig(t *testing.T, doc string) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return cfg
}

func TestStructureProblems(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "a single node",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A}
`,
		},
		{
			name: "a chain",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A}
  - {id: b, type: agent, agent_name: B, depends_on: [a]}
  - {id: c, type: agent, agent_name: C, depends_on: [b]}
`,
		},
		{
			name: "a diamond",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A}
  - {id: b, type: agent, agent_name: B, depends_on: [a]}
  - {id: c, type: agent, agent_name: C, depends_on: [a]}
  - {id: d, type: agent, agent_name: D, depends_on: [b, c]}
`,
		},
		{
			name: "a node depending on a node that is not defined is left to the schema warning",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A, depends_on: [ghost]}
`,
		},
		{
			name: "no nodes",
			doc:  `output_mapping: {result: x}`,
		},
		{
			name: "nodes with no id are left to the parser",
			doc: `
nodes:
  - {type: agent, agent_name: A}
  - {id: "", type: agent, agent_name: B}
  - not a node
`,
		},
		{
			name: "a two-node cycle",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A, depends_on: [b]}
  - {id: b, type: agent, agent_name: B, depends_on: [a]}
`,
			want: []string{`dependency cycle among nodes ["a", "b"]`},
		},
		{
			name: "a longer cycle names every member in declaration order",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A, depends_on: [d]}
  - {id: b, type: agent, agent_name: B, depends_on: [a]}
  - {id: c, type: agent, agent_name: C, depends_on: [b]}
  - {id: d, type: agent, agent_name: D, depends_on: [c]}
`,
			want: []string{`dependency cycle among nodes ["a", "b", "c", "d"]`},
		},
		{
			name: "a cycle excludes the nodes that are merely downstream of it and the ones upstream",
			doc: `
nodes:
  - {id: root, type: agent, agent_name: R}
  - {id: a, type: agent, agent_name: A, depends_on: [root, b]}
  - {id: b, type: agent, agent_name: B, depends_on: [a]}
  - {id: tail, type: agent, agent_name: T, depends_on: [b]}
`,
			want: []string{`dependency cycle among nodes ["a", "b"]`},
		},
		{
			name: "two separate cycles are both reported",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A, depends_on: [b]}
  - {id: b, type: agent, agent_name: B, depends_on: [a]}
  - {id: x, type: agent, agent_name: X, depends_on: [y]}
  - {id: y, type: agent, agent_name: Y, depends_on: [x]}
`,
			want: []string{
				`dependency cycle among nodes ["a", "b"]`,
				`dependency cycle among nodes ["x", "y"]`,
			},
		},
		{
			name: "a cycle declared out of order",
			doc: `
nodes:
  - {id: b, type: agent, agent_name: B, depends_on: [c]}
  - {id: c, type: agent, agent_name: C, depends_on: [a]}
  - {id: a, type: agent, agent_name: A, depends_on: [b]}
`,
			want: []string{`dependency cycle among nodes ["b", "c", "a"]`},
		},
		{
			name: "a self-dependency",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A, depends_on: [a]}
`,
			want: []string{`node "a" depends on itself`},
		},
		{
			name: "a self-dependency listed twice is reported once",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A, depends_on: [a, a]}
`,
			want: []string{`node "a" depends on itself`},
		},
		{
			name: "a self-dependency beside valid nodes",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A}
  - {id: b, type: agent, agent_name: B, depends_on: [a, b]}
`,
			want: []string{`node "b" depends on itself`},
		},
		{
			name: "a self-dependency inside a larger cycle is reported with it",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A, depends_on: [a, b]}
  - {id: b, type: agent, agent_name: B, depends_on: [a]}
`,
			want: []string{`node "a" depends on itself`, `dependency cycle among nodes ["a", "b"]`},
		},
		{
			name: "the dependencies alias is read like depends_on",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A, dependencies: [b]}
  - {id: b, type: agent, agent_name: B, dependencies: [a]}
`,
			want: []string{`dependency cycle among nodes ["a", "b"]`},
		},
		{
			name: "duplicate ids",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A}
  - {id: a, type: agent, agent_name: B}
`,
			want: []string{`node id "a" is defined 2 times; node ids must be unique`},
		},
		{
			name: "an id defined three times is reported once with its count",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A}
  - {id: a, type: agent, agent_name: B}
  - {id: a, type: agent, agent_name: C}
`,
			want: []string{`node id "a" is defined 3 times; node ids must be unique`},
		},
		{
			name: "every duplicated id is reported in first-seen order",
			doc: `
nodes:
  - {id: b, type: agent, agent_name: B}
  - {id: a, type: agent, agent_name: A}
  - {id: b, type: agent, agent_name: B2}
  - {id: a, type: agent, agent_name: A2}
`,
			want: []string{
				`node id "b" is defined 2 times; node ids must be unique`,
				`node id "a" is defined 2 times; node ids must be unique`,
			},
		},
		{
			name: "a loop body id shared with another node",
			doc: `
nodes:
  - {id: start, type: agent, agent_name: S}
  - {id: body, type: agent, agent_name: B}
  - {id: again, type: loop, node: body, condition: "1 == 2", depends_on: [start]}
  - {id: body, type: agent, agent_name: B2}
`,
			want: []string{`node id "body" is defined 2 times; node ids must be unique`},
		},
		{
			name: "a map template id shared with another node",
			doc: `
nodes:
  - {id: fan, type: map, node: item, items: "{{workflow.input.list}}"}
  - {id: item, type: agent, agent_name: I}
  - {id: item, type: agent, agent_name: I2}
`,
			want: []string{`node id "item" is defined 2 times; node ids must be unique`},
		},
		{
			name: "a loop body that depends on its own loop",
			doc: `
nodes:
  - {id: again, type: loop, node: body, condition: "1 == 2", depends_on: [body]}
  - {id: body, type: agent, agent_name: B, depends_on: [again]}
`,
			want: []string{`dependency cycle among nodes ["again", "body"]`},
		},
		{
			name: "a duplicated id does not hide the cycle through its first definition",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A, depends_on: [b]}
  - {id: b, type: agent, agent_name: B, depends_on: [a]}
  - {id: b, type: agent, agent_name: B2}
`,
			want: []string{
				`node id "b" is defined 2 times; node ids must be unique`,
				`dependency cycle among nodes ["a", "b"]`,
			},
		},
		{
			name: "duplicates, a self-dependency and a cycle are all collected",
			doc: `
nodes:
  - {id: a, type: agent, agent_name: A}
  - {id: a, type: agent, agent_name: A2}
  - {id: s, type: agent, agent_name: S, depends_on: [s]}
  - {id: x, type: agent, agent_name: X, depends_on: [y]}
  - {id: y, type: agent, agent_name: Y, depends_on: [x]}
`,
			want: []string{
				`node id "a" is defined 2 times; node ids must be unique`,
				`node "s" depends on itself`,
				`dependency cycle among nodes ["x", "y"]`,
			},
		},
		{
			name: "a node the parser would reject still takes part in a cycle",
			doc: `
nodes:
  - {id: a, type: agent, depends_on: [b]}
  - {id: b, type: agent, agent_name: B, depends_on: [a]}
`,
			want: []string{`dependency cycle among nodes ["a", "b"]`},
		},
		{
			name: "a parser-rejected node still takes part in a cycle through the dependencies alias",
			doc: `
nodes:
  - {id: a, type: agent, dependencies: [b]}
  - {id: b, type: agent, agent_name: B, dependencies: [a]}
`,
			want: []string{`dependency cycle among nodes ["a", "b"]`},
		},
		{
			name: "a node with no type still takes part in a cycle",
			doc: `
nodes:
  - {id: a, depends_on: [a]}
`,
			want: []string{`node "a" depends on itself`},
		},
		{
			name: "a connector-bound tool node without a tool_name takes part",
			doc: `
nodes:
  - {id: q, type: tool, connector: db, depends_on: [q]}
`,
			want: []string{`node "q" depends on itself`},
		},
		{
			name: "the nested shape is read under workflow",
			doc: `
agent_type: workflow
workflow:
  nodes:
    - {id: a, type: agent, agent_name: A, depends_on: [b]}
    - {id: b, type: agent, agent_name: B, depends_on: [a]}
`,
			want: []string{`dependency cycle among nodes ["a", "b"]`},
		},
		{
			name: "a nested body that is not a map is left to the other checks",
			doc: `
workflow: not-a-map
`,
		},
		{
			name: "a rerouting decision whose escalate target depends on a route",
			doc: `
decision: {api_key: k, model: m}
nodes:
  - id: triage
    type: decision
    questions:
      department: {type: choice, instructions: "Which team?", criteria: {billing: Payments, tech: Bugs}}
    route_on: department
    routes: {billing: billing, tech: tech}
    min_confidence: 0.8
    escalate: reclassify
  - {id: reclassify, type: tool, tool_name: t, depends_on: [triage, billing]}
  - {id: billing, type: tool, tool_name: t, depends_on: [triage]}
  - {id: tech, type: tool, tool_name: t, depends_on: [triage]}
`,
			want: []string{`dependency cycle among nodes ["reclassify", "billing"]`},
		},
		{
			name: "a handoff decision adds no wait, so the same shape is acyclic",
			doc: `
decision: {api_key: k, model: m}
nodes:
  - id: triage
    type: decision
    questions:
      department: {type: choice, instructions: "Which team?", criteria: {billing: Payments, tech: Bugs}}
    route_on: department
    routes: {billing: billing, tech: tech}
    min_confidence: 0.8
    escalate: reclassify
    escalation_mode: handoff
  - {id: reclassify, type: tool, tool_name: t, depends_on: [triage, billing]}
  - {id: billing, type: tool, tool_name: t, depends_on: [triage]}
  - {id: tech, type: tool, tool_name: t, depends_on: [triage]}
`,
		},
		{
			name: "a rerouting decision that is valid",
			doc: `
decision: {api_key: k, model: m}
nodes:
  - id: triage
    type: decision
    questions:
      department: {type: choice, instructions: "Which team?", criteria: {billing: Payments, tech: Bugs}}
    route_on: department
    routes: {billing: billing, tech: tech}
    min_confidence: 0.8
    escalate: reclassify
  - {id: reclassify, type: tool, tool_name: t, depends_on: [triage]}
  - {id: billing, type: tool, tool_name: t, depends_on: [triage]}
  - {id: tech, type: tool, tool_name: t, depends_on: [triage]}
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StructureProblems(structureAppConfig(t, tc.doc))
			if !reflect.DeepEqual(got, tc.want) && (len(got) != 0 || len(tc.want) != 0) {
				t.Fatalf("StructureProblems =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}

func TestStructureProblemsAgreeWithTheEngine(t *testing.T) {
	const output = "output_mapping: {result: x}\n"
	tests := []struct {
		name          string
		nodes         string
		wantProblem   bool
		wantEngineErr string
	}{
		{"a cycle", "nodes:\n  - {id: a, type: agent, agent_name: A, depends_on: [b]}\n  - {id: b, type: agent, agent_name: B, depends_on: [a]}\n", true, "dependency cycle"},
		{"a self-dependency", "nodes:\n  - {id: a, type: agent, agent_name: A, depends_on: [a]}\n", true, "dependency cycle"},
		{"duplicate ids", "nodes:\n  - {id: a, type: agent, agent_name: A}\n  - {id: a, type: agent, agent_name: B}\n", true, "duplicate node ID"},
		{"a cycle through the dependencies alias", "nodes:\n  - {id: a, type: agent, agent_name: A, dependencies: [b]}\n  - {id: b, type: agent, agent_name: B, dependencies: [a]}\n", true, "dependency cycle"},
		{"a loop body that depends on its own loop", "nodes:\n  - {id: again, type: loop, node: body, condition: \"1 == 2\", depends_on: [body]}\n  - {id: body, type: agent, agent_name: B, depends_on: [again]}\n", true, "dependency cycle"},
		{"a loop body defined twice", "nodes:\n  - {id: again, type: loop, node: body, condition: \"1 == 2\"}\n  - {id: body, type: agent, agent_name: B}\n  - {id: body, type: agent, agent_name: B2}\n", true, "duplicate node ID"},
		{"a rerouting decision whose escalate target waits on a route", "decision: {api_key: k, model: m}\nnodes:\n  - id: triage\n    type: decision\n    questions:\n      department: {type: choice, instructions: \"Which team?\", criteria: {billing: Payments, tech: Bugs}}\n    route_on: department\n    routes: {billing: billing, tech: tech}\n    min_confidence: 0.8\n    escalate: reclassify\n  - {id: reclassify, type: tool, tool_name: t, depends_on: [triage, billing]}\n  - {id: billing, type: tool, tool_name: t, depends_on: [triage]}\n  - {id: tech, type: tool, tool_name: t, depends_on: [triage]}\n", true, "dependency cycle"},
		{"a diamond", "nodes:\n  - {id: a, type: agent, agent_name: A}\n  - {id: b, type: agent, agent_name: B, depends_on: [a]}\n  - {id: c, type: agent, agent_name: C, depends_on: [a]}\n  - {id: d, type: agent, agent_name: D, depends_on: [b, c]}\n", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			appConfig := structureAppConfig(t, output+tc.nodes)
			problems := StructureProblems(appConfig)
			if (len(problems) > 0) != tc.wantProblem {
				t.Fatalf("StructureProblems = %q, want a problem: %v", problems, tc.wantProblem)
			}
			err := CheckLoadable("wf", appConfig)
			if tc.wantEngineErr == "" {
				if err != nil {
					t.Fatalf("the engine refused a workflow the check passed: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantEngineErr) {
				t.Fatalf("CheckLoadable = %v, want it to contain %q", err, tc.wantEngineErr)
			}
		})
	}
}
