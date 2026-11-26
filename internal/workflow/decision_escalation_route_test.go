// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const escalationRoutedWorkflow = `
decision:
  api_key: test-key
  model: jev-1.13.0
output_mapping:
  result: {coalesce: ["{{billing.output}}", "{{tech.output}}"]}
nodes:
  - id: triage
    type: decision
    questions:
      department:
        type: choice
        instructions: "Which team owns ` + "`ticket`" + `?"
        criteria: {billing: Payments, tech: Bugs}
    route_on: department
    routes: {billing: billing, tech: tech}
    min_confidence: 0.8
    escalate: reclassify
  - {id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage]}
  - {id: billing, type: tool, tool_name: billing_tool, depends_on: [triage]}
  - {id: tech, type: tool, tool_name: tech_tool, depends_on: [triage]}
`

func newEscalationRoutedRun(t *testing.T, doc string, jev *fakeJev, reclassifiedAs string) *decisionRun {
	t.Helper()
	run := newDecisionRun(t, doc, jev, true)
	run.tools["reclassify"].returnResult = tool.OK("reclassified", tool.WithData(map[string]any{"department": reclassifiedAs}))
	return run
}

func (r *decisionRun) assertRanAndSkipped(t *testing.T, ran []string, skipped map[string]string) {
	t.Helper()
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	for _, id := range ran {
		if !r.e.state.IsNodeCompleted(id) {
			t.Errorf("%q should have run; completed=%v skipped=%v error=%+v", id, r.e.state.CompletedNodes, r.e.state.SkippedNodes, r.e.state.ErrorState)
		}
	}
	for id, wantReason := range skipped {
		if !r.e.state.IsNodeSkipped(id) {
			t.Errorf("%q should have been skipped; completed=%v", id, r.e.state.CompletedNodes)
			continue
		}
		if r.tools[id].lastToolCtx != nil {
			t.Errorf("%q was skipped but its tool still ran", id)
		}
		if reason := r.e.state.SkippedNodes[id]; !strings.Contains(reason, wantReason) {
			t.Errorf("%q skip reason = %q, want it to contain %q", id, reason, wantReason)
		}
	}
}

func TestDecisionNodeRoutesOnTheEscalationsAnswer(t *testing.T) {
	t.Run("a confident answer routes directly and skips the escalation", func(t *testing.T) {
		run := newEscalationRoutedRun(t, escalationRoutedWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.9)}, "tech")
		run.execute(t)
		run.assertRanAndSkipped(t, []string{"billing"}, map[string]string{
			"tech":       `not selected by decision "triage"`,
			"reclassify": `not selected by decision "triage"`,
		})
		if out := run.output(t, "triage"); out["selected_branch"] != "billing" || out["routed_branch"] != "billing" {
			t.Errorf("triage output = %v, want billing both selected and routed", out)
		}
	})
	t.Run("a low-confidence answer routes on what the escalation decided", func(t *testing.T) {
		run := newEscalationRoutedRun(t, escalationRoutedWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.5)}, "tech")
		run.execute(t)
		run.assertRanAndSkipped(t, []string{"reclassify", "tech"}, map[string]string{
			"billing": `not selected by tool "reclassify"`,
		})
		if out := run.output(t, "triage"); out["escalated"] != true || out["selected_branch"] != "reclassify" || out["routed_branch"] != "tech" {
			t.Errorf("triage output = %v, want an escalation to reclassify routed to tech", out)
		}
	})
	t.Run("a provider failure that escalates also routes on the escalation's answer", func(t *testing.T) {
		doc := strings.Replace(escalationRoutedWorkflow, "    min_confidence: 0.8\n", "    on_provider_error: escalate\n", 1)
		run := newEscalationRoutedRun(t, doc, &fakeJev{status: http.StatusServiceUnavailable}, "billing")
		run.execute(t)
		run.assertRanAndSkipped(t, []string{"reclassify", "billing"}, map[string]string{
			"tech": `not selected by tool "reclassify"`,
		})
	})
	t.Run("an escalation answer with no route fails the escalation node", func(t *testing.T) {
		run := newEscalationRoutedRun(t, escalationRoutedWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.5)}, "sales")
		run.execute(t)
		msg := run.errorMessage()
		if !strings.Contains(msg, "answered a 5-byte string") || !strings.Contains(msg, `"triage"`) || !strings.Contains(msg, "options: billing, tech") {
			t.Fatalf("workflow error = %q, want the unroutable answer described by type and size, with the options", msg)
		}
		if strings.Contains(msg, "sales") {
			t.Errorf("workflow error = %q echoes the escalation's answer", msg)
		}
		if !strings.Contains(msg, "escalation_mode: handoff") {
			t.Errorf("workflow error = %q, want it to name escalation_mode: handoff as the way out", msg)
		}
		if out := run.output(t, "triage"); out["routed_branch"] != "" {
			t.Errorf("routed_branch = %v, want it empty when no route ran", out["routed_branch"])
		}
		if run.tools["billing"].lastToolCtx != nil || run.tools["tech"].lastToolCtx != nil {
			t.Error("a route target ran although the escalation's answer had no route")
		}
	})
}

func TestAFailedEscalationRunsNoRoute(t *testing.T) {
	for name, failFast := range map[string]bool{"with fail_fast": true, "without fail_fast": false} {
		t.Run(name, func(t *testing.T) {
			doc := escalationRoutedWorkflow
			if !failFast {
				doc = "fail_fast: false\n" + doc
			}
			run := newEscalationRoutedRun(t, doc, &fakeJev{respond: departmentAnswer("billing", 0.5)}, "tech")
			run.tools["reclassify"].returnResult = tool.Error("reclassify unavailable")
			run.execute(t)
			for _, route := range []string{"billing", "tech"} {
				if run.tools[route].lastToolCtx != nil {
					t.Errorf("route %q ran although the escalation it waits for failed", route)
				}
			}
			if !failFast {
				run.assertRanAndSkipped(t, nil, map[string]string{
					"billing": `dependency "reclassify" failed`,
					"tech":    `dependency "reclassify" failed`,
				})
			}
		})
	}
}

func TestParseEscalationRerouteRejects(t *testing.T) {
	cases := map[string]struct {
		edit    func(string) string
		wantErr string
	}{
		"a leftover escalation_choice": {
			edit: func(doc string) string {
				return strings.Replace(doc, "    escalate: reclassify\n", "    escalate: reclassify\n    escalation_choice: \"{{reclassify.output.department}}\"\n", 1)
			},
			wantErr: "escalation_choice was replaced by escalation_mode",
		},
		"an unknown escalation_mode": {
			edit: func(doc string) string {
				return strings.Replace(doc, "    escalate: reclassify\n", "    escalate: reclassify\n    escalation_mode: retry\n", 1)
			},
			wantErr: `escalation_mode must be "reroute" or "handoff", got "retry"`,
		},
		"an escalation_mode on a node that does not route": {
			edit: func(doc string) string {
				for _, line := range []string{"    route_on: department\n", "    routes: {billing: billing, tech: tech}\n", "    min_confidence: 0.8\n"} {
					doc = strings.Replace(doc, line, "", 1)
				}
				return strings.Replace(doc, "    escalate: reclassify\n", "    escalation_mode: reroute\n", 1)
			},
			wantErr: "has no route_on",
		},
		"an escalation_mode with no escalate target": {
			edit: func(doc string) string {
				doc = strings.Replace(doc, "    min_confidence: 0.8\n", "", 1)
				return strings.Replace(doc, "    escalate: reclassify\n", "    escalation_mode: handoff\n", 1)
			},
			wantErr: "sets escalation_mode but names no escalate target",
		},
		"a route to the escalate target itself": {
			edit: func(doc string) string {
				return strings.Replace(doc, "routes: {billing: billing, tech: tech}", "routes: {billing: reclassify, tech: tech}", 1)
			},
			wantErr: "or set escalation_mode: handoff",
		},
		"an escalate target a later loop runs as its body": {
			edit: func(doc string) string {
				return doc + `  - {id: reloop, type: loop, node: reclassify, condition: "1 == 2", max_iterations: 1, depends_on: [triage]}` + "\n"
			},
			wantErr: `it is the body of loop node "reloop"`,
		},
		"an escalate target that depends on a route, which the implicit wait would turn into a cycle": {
			edit: func(doc string) string {
				return strings.Replace(doc, "{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage]}",
					"{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage, billing]}", 1)
			},
			wantErr: `which itself depends on "billing": that would be a dependency cycle; or set escalation_mode: handoff`,
		},
		"an escalate target that reaches a route only transitively": {
			edit: func(doc string) string {
				doc = strings.Replace(doc, "{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage]}",
					"{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage, prep]}", 1)
				return doc + "  - {id: prep, type: tool, tool_name: prep_tool, depends_on: [billing]}\n"
			},
			wantErr: `which itself depends on "billing": that would be a dependency cycle`,
		},
		"an escalate target that reaches a route only through another decision's reroute wait": {
			edit: func(doc string) string {
				doc = strings.Replace(doc, "{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage]}",
					"{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage, z]}", 1)
				return doc + `  - id: sorter
    type: decision
    depends_on: [triage]
    questions:
      speed:
        type: choice
        instructions: "How fast?"
        criteria: {fast: Quick, slow: Slow}
    route_on: speed
    routes: {fast: z, slow: w}
    min_confidence: 0.8
    escalate: e2
  - {id: e2, type: tool, tool_name: e2_tool, depends_on: [sorter, billing]}
  - {id: z, type: tool, tool_name: z_tool, depends_on: [sorter]}
  - {id: w, type: tool, tool_name: w_tool, depends_on: [sorter]}
`
			},
			wantErr: "that would be a dependency cycle; or set escalation_mode: handoff",
		},
		"an escalate target that depends on the default target": {
			edit: func(doc string) string {
				doc = strings.Replace(doc, "routes: {billing: billing, tech: tech}", "routes: {billing: billing}\n    default: tech", 1)
				return strings.Replace(doc, "{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage]}",
					"{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage, tech]}", 1)
			},
			wantErr: `which itself depends on "tech": that would be a dependency cycle`,
		},
		"a misspelled escalate target, reported as itself rather than as a route's dependency": {
			edit: func(doc string) string {
				const billing = "  - {id: billing, type: tool, tool_name: billing_tool, depends_on: [triage]}\n"
				doc = strings.Replace(doc, billing, "", 1)
				doc = strings.Replace(doc, "nodes:\n", "nodes:\n"+billing, 1)
				return strings.Replace(doc, "    escalate: reclassify\n", "    escalate: reclasify\n", 1)
			},
			wantErr: "reclasify",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := parseYAMLWorkflow(t, tc.edit(escalationRoutedWorkflow))
			if err == nil {
				_, err = BuildDependencyGraph(cfg.Nodes)
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), `"billing" depends on non-existent node`) {
				t.Errorf("err = %v blames a route for a dependency its author never wrote", err)
			}
		})
	}
}

func TestEscalationModeDecidesTheRouteDependencies(t *testing.T) {
	graphNode := func(t *testing.T, doc, nodeID string) *NodeConfig {
		t.Helper()
		cfg, err := parseYAMLWorkflow(t, doc)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		dag, err := BuildDependencyGraph(cfg.Nodes)
		if err != nil {
			t.Fatalf("BuildDependencyGraph: %v", err)
		}
		return dag.Nodes[nodeID]
	}
	t.Run("rerouting, the default, makes every route wait for the escalate target without rewriting its depends_on", func(t *testing.T) {
		for _, id := range []string{"billing", "tech"} {
			node := graphNode(t, escalationRoutedWorkflow, id)
			if !slices.Equal(node.waitsFor(), []string{"triage", "reclassify"}) {
				t.Errorf("%s waits for %v, want [triage reclassify]", id, node.waitsFor())
			}
			if !slices.Equal(node.DependsOn, []string{"triage"}) {
				t.Errorf("%s depends_on = %v, want the authored [triage]", id, node.DependsOn)
			}
		}
	})
	t.Run("an author-listed escalate dependency is not repeated", func(t *testing.T) {
		doc := strings.Replace(escalationRoutedWorkflow, "tool_name: tech_tool, depends_on: [triage]", "tool_name: tech_tool, depends_on: [triage, reclassify]", 1)
		if waits := graphNode(t, doc, "tech").waitsFor(); !slices.Equal(waits, []string{"triage", "reclassify"}) {
			t.Errorf("tech waits for %v, want [triage reclassify]", waits)
		}
	})
	t.Run("handoff leaves the routes waiting on the decision only, and may route to the escalate target", func(t *testing.T) {
		doc := strings.Replace(escalationRoutedWorkflow, "    escalate: reclassify\n", "    escalate: reclassify\n    escalation_mode: handoff\n", 1)
		doc = strings.Replace(doc, "routes: {billing: billing, tech: tech}", "routes: {billing: billing, tech: reclassify}", 1)
		if waits := graphNode(t, doc, "billing").waitsFor(); !slices.Equal(waits, []string{"triage"}) {
			t.Errorf("billing waits for %v, want [triage]", waits)
		}
	})
}

func TestARouteWithoutInputReceivesTheDecisionsOutputInEitherMode(t *testing.T) {
	handoff := strings.Replace(escalationRoutedWorkflow, "    escalate: reclassify\n", "    escalate: reclassify\n    escalation_mode: handoff\n", 1)
	for name, doc := range map[string]string{"reroute": escalationRoutedWorkflow, "handoff": handoff} {
		t.Run(name, func(t *testing.T) {
			run := newEscalationRoutedRun(t, doc, &fakeJev{respond: departmentAnswer("billing", 0.9)}, "tech")
			run.execute(t)
			if got := run.tools["billing"].lastArgs["selected_branch"]; got != "billing" {
				t.Errorf("billing received %v, want the decision's output (selected_branch billing)", run.tools["billing"].lastArgs)
			}
		})
	}
}

func TestEscalationRerouteGraphRejects(t *testing.T) {
	triage := func() *NodeConfig {
		return &NodeConfig{ID: "triage", Type: "decision", RouteOn: "department", Escalate: "reclassify",
			Routes:    map[string]string{"billing": "billing"},
			Questions: []decision.Question{{Key: "department", Type: decision.TypeChoice, Criteria: []decision.Criterion{{Key: "billing"}, {Key: "tech"}}}}}
	}
	cases := map[string]struct {
		nodes   map[string]*NodeConfig
		wantErr string
	}{
		"an escalate target whose completion never reaches the routing": {
			nodes: map[string]*NodeConfig{
				"reclassify": {ID: "reclassify", Type: "map", DependsOn: []string{"triage"}},
				"billing":    {ID: "billing", Type: "agent", DependsOn: []string{"triage"}},
			},
			wantErr: "must be one of agent, workflow, tool, decision, got map",
		},
		"an escalate decision node that does not ask the route_on question": {
			nodes: map[string]*NodeConfig{
				"reclassify": {ID: "reclassify", Type: "decision", DependsOn: []string{"triage"},
					Questions: []decision.Question{{Key: "team", Type: decision.TypeChoice}}},
				"billing": {ID: "billing", Type: "agent", DependsOn: []string{"triage"}},
			},
			wantErr: `must ask a choice question named "department"`,
		},
		"an escalate decision node that can answer an option route_on lacks": {
			nodes: map[string]*NodeConfig{
				"reclassify": {ID: "reclassify", Type: "decision", DependsOn: []string{"triage"},
					Questions: []decision.Question{{Key: "department", Type: decision.TypeChoice, Criteria: []decision.Criterion{{Key: "billing"}, {Key: "sales"}}}}},
				"billing": {ID: "billing", Type: "agent", DependsOn: []string{"triage"}},
			},
			wantErr: `can answer "sales", which is not an option of "department"`,
		},
		"an escalate decision node that escalates itself": {
			nodes: map[string]*NodeConfig{
				"reclassify": {ID: "reclassify", Type: "decision", DependsOn: []string{"triage"}, Escalate: "human", MinConfidence: 0.9,
					Questions: []decision.Question{{Key: "department", Type: decision.TypeChoice, Criteria: []decision.Criterion{{Key: "billing"}}}}},
				"human":   {ID: "human", Type: "agent", DependsOn: []string{"reclassify"}},
				"billing": {ID: "billing", Type: "agent", DependsOn: []string{"triage"}},
			},
			wantErr: `must not escalate itself (it names escalate "human")`,
		},
		"an escalate target that is a loop body": {
			nodes: map[string]*NodeConfig{
				"reclassify": {ID: "reclassify", Type: "agent", DependsOn: []string{"triage"}},
				"reloop":     {ID: "reloop", Type: "loop", BodyNode: "reclassify", DependsOn: []string{"triage"}},
				"billing":    {ID: "billing", Type: "agent", DependsOn: []string{"triage"}},
			},
			wantErr: `it is the body of loop node "reloop"`,
		},
		"an escalate target that is a map template": {
			nodes: map[string]*NodeConfig{
				"reclassify": {ID: "reclassify", Type: "agent", DependsOn: []string{"triage"}},
				"remap":      {ID: "remap", Type: "map", TemplateNode: "reclassify", DependsOn: []string{"triage"}},
				"billing":    {ID: "billing", Type: "agent", DependsOn: []string{"triage"}},
			},
			wantErr: `it is the template of map node "remap"`,
		},
		"an escalate target another node can route to and skip": {
			nodes: map[string]*NodeConfig{
				"reclassify": {ID: "reclassify", Type: "agent", DependsOn: []string{"triage", "other"}},
				"billing":    {ID: "billing", Type: "agent", DependsOn: []string{"triage"}},
				"other":      {ID: "other", Type: "decision", Escalate: "reclassify", EscalationMode: EscalationModeHandoff, Routes: map[string]string{"x": "billing"}},
			},
			wantErr: `node "other" also routes to it`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			node := triage()
			tc.nodes["triage"] = node
			err := validateEscalationReroute(tc.nodes, node)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "escalation_mode: handoff") {
				t.Errorf("err = %v, want it to name escalation_mode: handoff as the way out", err)
			}
		})
	}
	t.Run("handoff accepts an escalate target that cannot answer", func(t *testing.T) {
		node := triage()
		node.EscalationMode = EscalationModeHandoff
		nodes := map[string]*NodeConfig{"triage": node, "reclassify": {ID: "reclassify", Type: "map", DependsOn: []string{"triage"}}}
		if err := validateEscalationReroute(nodes, node); err != nil {
			t.Fatalf("err = %v, want none under handoff", err)
		}
	})
}

func TestADecisionEscalateTargetReroutesOnItsOwnAnswer(t *testing.T) {
	doc := strings.Replace(escalationRoutedWorkflow,
		"  - {id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage]}\n",
		`  - id: reclassify
    type: decision
    depends_on: [triage]
    input: {asker: reclassify}
    questions:
      department:
        type: choice
        instructions: "Which team owns this ticket? Read it closely."
        criteria: {billing: Payments, tech: Bugs}
`, 1)
	jev := &fakeJev{respond: func(state map[string]any) string {
		if state["asker"] == "reclassify" {
			return departmentAnswer("tech", 0.95)(state)
		}
		return departmentAnswer("billing", 0.5)(state)
	}}
	run := newDecisionRun(t, doc, jev, true)
	run.execute(t)
	run.assertRanAndSkipped(t, []string{"reclassify", "tech"}, map[string]string{
		"billing": `not selected by decision "reclassify"`,
	})
	if out := run.output(t, "triage"); out["routed_branch"] != "tech" {
		t.Errorf("routed_branch = %v, want the escalate decision's answer to route to tech", out["routed_branch"])
	}
}

func TestDecisionNodeEscalationRoutingSafety(t *testing.T) {
	t.Run("a skipped escalation runs no route", func(t *testing.T) {
		doc := strings.Replace(escalationRoutedWorkflow,
			"{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage]}",
			`{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage], when: "{{triage.output.escalated}} == false"}`, 1)
		run := newEscalationRoutedRun(t, doc, &fakeJev{respond: departmentAnswer("billing", 0.5)}, "tech")
		run.execute(t)
		run.assertRanAndSkipped(t, nil, map[string]string{
			"billing": `escalate target "reclassify" of decision "triage" was skipped`,
			"tech":    `escalate target "reclassify" of decision "triage" was skipped`,
		})
	})
	for name, answer := range map[string]any{
		"an answer in the wrong case": "Tech",
		"a number":                    7,
		"no answer":                   nil,
	} {
		t.Run(name+" fails instead of taking a route", func(t *testing.T) {
			run := newDecisionRun(t, escalationRoutedWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.5)}, true)
			run.tools["reclassify"].returnResult = tool.OK("reclassified", tool.WithData(map[string]any{"department": answer}))
			run.execute(t)
			if msg := run.errorMessage(); !strings.Contains(msg, "which is not an option of") {
				t.Fatalf("workflow error = %q, want the non-option escalation answer rejected", msg)
			}
			if run.tools["billing"].lastToolCtx != nil || run.tools["tech"].lastToolCtx != nil {
				t.Error("a route target ran on an answer that is not an option")
			}
		})
	}
}

func newPublishingDecisionRun(t *testing.T, doc string, jev *fakeJev) (*decisionRun, *capturingBroker) {
	t.Helper()
	brk := &capturingBroker{}
	run := newDecisionRun(t, doc, jev, true, func(_ string, svc *stubServices) { svc.brk = brk })
	run.e.execCtx.UserProps.A2AStatusTopic = "test/status"
	return run, brk
}

func TestDecisionResultEventCarriesTheRouting(t *testing.T) {
	cases := map[string]struct {
		doc        string
		confidence float64
		want       map[string]any
		absent     []string
	}{
		"a direct route": {
			doc: escalationRoutedWorkflow, confidence: 0.9,
			want: map[string]any{"selected_branch": "billing", "routed_branch": "billing", "escalated": false},
		},
		"an escalation routed on its answer, which comes after the event": {
			doc: escalationRoutedWorkflow, confidence: 0.5,
			want:   map[string]any{"selected_branch": "reclassify", "escalated": true, "escalation_reason": escalationLowConfidence},
			absent: []string{"routed_branch"},
		},
		"a plain escalation": {
			doc: routedDecisionWorkflow, confidence: 0.5,
			want:   map[string]any{"selected_branch": "fallback", "escalated": true, "escalation_reason": escalationLowConfidence},
			absent: []string{"routed_branch"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			run, brk := newPublishingDecisionRun(t, tc.doc, &fakeJev{respond: departmentAnswer("billing", tc.confidence)})
			if reclassify := run.tools["reclassify"]; reclassify != nil {
				reclassify.returnResult = tool.OK("reclassified", tool.WithData(map[string]any{"department": "tech"}))
			}
			run.execute(t)

			metadata, _ := nodeResultData(t, brk, "triage")["metadata"].(map[string]any)
			for key, want := range tc.want {
				if metadata[key] != want {
					t.Errorf("published %s = %#v, want %#v; metadata = %#v", key, metadata[key], want, metadata)
				}
			}
			for _, key := range tc.absent {
				if _, present := metadata[key]; present {
					t.Errorf("published metadata carries %s: %#v", key, metadata)
				}
			}
		})
	}
}

func TestAnUnroutableEscalationAnswerIsRetried(t *testing.T) {
	doc := strings.Replace(escalationRoutedWorkflow,
		"{id: reclassify, type: tool, tool_name: reclassify_tool, depends_on: [triage]}",
		"{id: reclassify, type: agent, agent_name: Reclassifier, depends_on: [triage], retry_strategy: {limit: 1, backoff: {duration: 1ms}}}", 1)
	run, brk := newPublishingDecisionRun(t, doc, &fakeJev{respond: departmentAnswer("billing", 0.5)})
	requestTopic := a2a.AgentRequestTopic(run.e.cfg.Namespace, "Reclassifier")
	answer := func(req agentRequest, department string) {
		t.Helper()
		if err := run.e.Advance(run.e.execCtx.Ctx, &EventNodeComplete{
			NodeID: "reclassify", SubTaskID: req.ID, Output: map[string]any{"department": department},
		}); err != nil {
			t.Fatalf("advance: %v", err)
		}
	}

	run.start(t)
	answer(waitForAgentRequests(t, brk, requestTopic, 1)[0], "sales")
	answer(waitForAgentRequests(t, brk, requestTopic, 2)[1], "tech")
	run.wait(t)

	run.assertRanAndSkipped(t, []string{"reclassify", "tech"}, map[string]string{
		"billing": `not selected by agent "reclassify"`,
	})
	if out := run.output(t, "triage"); out["routed_branch"] != "tech" {
		t.Errorf("routed_branch = %v, want the retried answer's route", out["routed_branch"])
	}
	run.e.mu.Lock()
	retries := run.e.state.RetryCounts["reclassify"]
	run.e.mu.Unlock()
	if retries != 1 {
		t.Errorf("reclassify retried %d times, want 1", retries)
	}
}

func TestDecisionNodeCannotRouteToAnExitHandler(t *testing.T) {
	initWorkflow := func(t *testing.T, doc string) error {
		t.Helper()
		cfg, err := parseYAMLWorkflow(t, doc)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		return NewWorkflowInstance(cfg).Init(t.Context(), nil, &stubServices{brk: stubBroker{}})
	}
	cases := map[string]struct {
		onExit  string
		wantErr string
	}{
		"an escalate target": {
			onExit:  "on_exit:\n  always: reclassify\n",
			wantErr: `decision node "triage": escalate "reclassify" is an on_exit handler`,
		},
		"a route target": {
			onExit:  "on_exit:\n  on_failure: tech\n",
			wantErr: `decision node "triage": routes.tech "tech" is an on_exit handler`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := initWorkflow(t, tc.onExit+escalationRoutedWorkflow); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Init err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
	t.Run("an exit handler the decision does not route to", func(t *testing.T) {
		doc := "on_exit:\n  always: audit\n" + escalationRoutedWorkflow + "  - {id: audit, type: tool, tool_name: audit_tool}\n"
		if err := initWorkflow(t, doc); err != nil {
			t.Fatalf("Init: %v", err)
		}
	})
}
