// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

const decisionWorkflowYAML = `
description: triage
decision:
  api_key: test-key
  model: jev-1.13.0
output_mapping:
  answer: {coalesce: ["{{billing.output}}", "{{tech.output}}", "{{fallback.output}}"]}
nodes:
  - id: triage
    type: decision
    questions:
      department:
        type: choice
        instructions: "Which team owns ` + "`ticket`" + `?"
        criteria:
          billing: Payment issues
          tech: Bugs
          sales: Pricing
      urgent:
        type: boolean
        instructions: "The ticket is urgent"
      severity:
        type: score
        instructions: Customer impact
        criteria:
          - low: cosmetic
          - medium: degraded
          - high: down
    route_on: department
    routes:
      billing: billing
      tech: tech
    min_confidence: 0.8
    escalate: fallback
    escalation_mode: handoff
    default: fallback
  - id: billing
    type: agent
    agent_name: Billing
    depends_on: [triage]
  - id: tech
    type: agent
    agent_name: Tech
    depends_on: [triage]
  - id: fallback
    type: agent
    agent_name: Classifier
    depends_on: [triage]
`

func parseYAMLWorkflow(t *testing.T, doc string) (*WorkflowConfig, error) {
	t.Helper()
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(doc), &raw); err != nil {
		t.Fatalf("fixture is not YAML: %v", err)
	}
	return ParseWorkflowConfig("triage-wf", "default", raw)
}

func TestParseDecisionNode(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, decisionWorkflowYAML)
	if err != nil {
		t.Fatalf("ParseWorkflowConfig: %v", err)
	}
	if cfg.Decision == nil || cfg.Decision.Provider != DecisionProviderTypeSafe || cfg.Decision.APIKey != "test-key" || cfg.Decision.Model != "jev-1.13.0" {
		t.Fatalf("provider block = %+v", cfg.Decision)
	}

	node := cfg.Nodes[0]
	if node.RouteOn != "department" || node.Escalate != "fallback" || node.EscalationMode != EscalationModeHandoff || node.Default != "fallback" || node.MinConfidence != 0.8 {
		t.Errorf("routing fields = %+v", node)
	}
	if node.Routes["billing"] != "billing" || node.Routes["tech"] != "tech" {
		t.Errorf("routes = %v", node.Routes)
	}

	keys := make([]string, len(node.Questions))
	for i, q := range node.Questions {
		keys[i] = q.Key
	}
	if strings.Join(keys, ",") != "department,severity,urgent" {
		t.Errorf("questions must be sorted by key for a stable request, got %v", keys)
	}
	severity, _ := node.decisionQuestion("severity")
	if got := strings.Join(severity.CriterionKeys(), ","); got != "low,medium,high" {
		t.Errorf("score levels must keep their authored order, got %s", got)
	}
	urgent, _ := node.decisionQuestion("urgent")
	if urgent.Type != decision.TypeBoolean {
		t.Errorf("urgent type = %s", urgent.Type)
	}

	if _, err := BuildDependencyGraph(cfg.Nodes); err != nil {
		t.Errorf("BuildDependencyGraph: %v", err)
	}
}

func TestParseDecisionNodeClassifyModeNeedsNoRouting(t *testing.T) {
	_, err := parseYAMLWorkflow(t, `
output_mapping: {urgent: "{{triage.output.answers.urgent.value}}"}
nodes:
  - id: triage
    type: decision
    questions:
      urgent: {type: boolean, instructions: "The ticket is urgent"}
`)
	if err != nil {
		t.Fatalf("a decision node without routing must parse: %v", err)
	}
}

func scoreQuestionYAML(levels int) string {
	var b strings.Builder
	b.WriteString("    questions:\n      q:\n        type: score\n        instructions: x\n        criteria:\n")
	for i := range levels {
		fmt.Fprintf(&b, "          - level%d: description %d\n", i, i)
	}
	return b.String()
}

func TestParseDecisionNodeAcceptsCriteriaEdgeShapes(t *testing.T) {
	cases := map[string]struct {
		questions string
		wantKeys  string
	}{
		"boolean criteria with unquoted true and false keys": {
			questions: "    questions:\n      q:\n        type: boolean\n        instructions: x\n        criteria:\n          true: needs a human\n          false: routine\n",
			wantKeys:  "false,true",
		},
		"score with exactly ten levels": {
			questions: scoreQuestionYAML(decision.MaxScoreLevels),
			wantKeys:  "level0,level1,level2,level3,level4,level5,level6,level7,level8,level9",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := parseYAMLWorkflow(t, "output_mapping: {r: x}\nnodes:\n  - id: triage\n    type: decision\n"+tc.questions)
			if err != nil {
				t.Fatalf("ParseWorkflowConfig: %v", err)
			}
			q, _ := cfg.Nodes[0].decisionQuestion("q")
			if got := strings.Join(q.CriterionKeys(), ","); got != tc.wantKeys {
				t.Errorf("criteria keys = %s, want %s", got, tc.wantKeys)
			}
		})
	}
}

func TestParseDecisionNodeRejects(t *testing.T) {
	base := func(fields string) string {
		return `
output_mapping: {r: x}
nodes:
  - id: triage
    type: decision
` + fields
	}
	questions := `
    questions:
      department:
        type: choice
        instructions: route it
        criteria: {billing: b, tech: t}
      urgent: {type: boolean, instructions: is it urgent}
`
	cases := map[string]struct {
		doc     string
		wantErr string
	}{
		"no questions": {
			doc:     base(""),
			wantErr: "requires at least one entry under 'questions'",
		},
		"unknown question type": {
			doc:     base("    questions:\n      q: {type: essay, instructions: x}\n"),
			wantErr: "'type' must be one of boolean, choice, score",
		},
		"missing instructions": {
			doc:     base("    questions:\n      q: {type: boolean}\n"),
			wantErr: "'instructions' is required",
		},
		"boolean criteria describing only one side": {
			doc:     base("    questions:\n      q:\n        type: boolean\n        instructions: x\n        criteria:\n          \"true\": needs a human\n"),
			wantErr: "must describe both",
		},
		"a blank criterion key": {
			doc:     base("    questions:\n      q: {type: choice, instructions: x, criteria: {\"\": blank, billing: b}}\n"),
			wantErr: "needs an option key",
		},
		"criterion keys that repeat once trimmed": {
			doc:     base("    questions:\n      q: {type: choice, instructions: x, criteria: {billing: b, \" billing \": again}}\n"),
			wantErr: "repeats option",
		},
		"boolean criteria with a key other than true or false": {
			doc:     base("    questions:\n      q: {type: boolean, instructions: x, criteria: {urgent: u, calm: c}}\n"),
			wantErr: `may only describe "true" and "false"`,
		},
		"score with more than ten levels": {
			doc:     base(scoreQuestionYAML(11)),
			wantErr: "at most 10 levels, got 11",
		},
		"choice with one option": {
			doc:     base("    questions:\n      q: {type: choice, instructions: x, criteria: {a: a}}\n"),
			wantErr: "needs at least two 'criteria'",
		},
		"score criteria as a map": {
			doc:     base("    questions:\n      q: {type: score, instructions: x, criteria: {low: l, high: h}}\n"),
			wantErr: "score 'criteria' must be a list",
		},
		"duplicate list option": {
			doc:     base("    questions:\n      q: {type: choice, instructions: x, criteria: [{a: a}, {a: b}]}\n"),
			wantErr: `repeats option "a"`,
		},
		"routes without route_on": {
			doc:     base(questions + "    routes: {billing: b}\n"),
			wantErr: "has no route_on",
		},
		"route_on names a missing question": {
			doc:     base(questions + "    route_on: nope\n    routes: {billing: b}\n"),
			wantErr: `route_on "nope" is not one of its questions`,
		},
		"route_on names a boolean": {
			doc:     base(questions + "    route_on: urgent\n    routes: {billing: b}\n"),
			wantErr: "only a choice question can select a branch",
		},
		"route key is not an option": {
			doc:     base(questions + "    route_on: department\n    routes: {sales: b}\n"),
			wantErr: "routes.sales is not an option",
		},
		"route_on without routes": {
			doc:     base(questions + "    route_on: department\n    default: b\n"),
			wantErr: "has route_on but no routes",
		},
		"route_on alone": {
			doc:     base(questions + "    route_on: department\n"),
			wantErr: "has route_on but no routes",
		},
		"route_on alone naming a missing question": {
			doc:     base(questions + "    route_on: nope\n"),
			wantErr: `route_on "nope" is not one of its questions`,
		},
		"min_confidence out of range": {
			doc:     base(questions + "    route_on: department\n    routes: {billing: b}\n    min_confidence: 1.5\n"),
			wantErr: "min_confidence must be in (0, 1]",
		},
		"min_confidence without escalate": {
			doc:     base(questions + "    route_on: department\n    routes: {billing: b}\n    min_confidence: 0.8\n"),
			wantErr: "names no escalate target",
		},
		"escalate that nothing reaches": {
			doc:     base(questions + "    route_on: department\n    routes: {billing: b}\n    escalate: b\n"),
			wantErr: "which nothing reaches",
		},
		"a route_on option with no route and no default": {
			doc:     base(questions + "    route_on: department\n    routes: {billing: b}\n"),
			wantErr: `question "department" has no route for option(s) tech and the node has no default`,
		},
		"several unrouted options are all named": {
			doc: base(`
    questions:
      department:
        type: choice
        instructions: route it
        criteria: {billing: b, sales: s, tech: t}
    route_on: department
    routes: {billing: b}
`),
			wantErr: "no route for option(s) sales, tech",
		},
		"bad on_provider_error": {
			doc:     base(questions + "    on_provider_error: retry\n"),
			wantErr: "on_provider_error must be",
		},
		"unknown provider": {
			doc: `
decision: {provider: gpt}
output_mapping: {r: x}
nodes:
  - {id: a, type: agent, agent_name: A}
`,
			wantErr: `unknown provider "gpt"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseYAMLWorkflow(t, tc.doc)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseDecisionNodeRoutingCoverage(t *testing.T) {
	doc := func(routing string) string {
		return `
output_mapping: {r: x}
nodes:
  - id: triage
    type: decision
    questions:
      department:
        type: choice
        instructions: route it
        criteria: {billing: b, tech: t}
    route_on: department
` + routing
	}
	cases := map[string]string{
		"every option routed":                doc("    routes: {billing: b, tech: t}\n"),
		"a default covers the unrouted rest": doc("    routes: {billing: b}\n    default: d\n"),
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseYAMLWorkflow(t, d); err != nil {
				t.Fatalf("a decision node that routes every option must parse: %v", err)
			}
		})
	}
}

func TestParseDecisionNodeRoutesWithUnquotedNonStringKeys(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, `
output_mapping: {r: x}
nodes:
  - id: triage
    type: decision
    questions:
      tier:
        type: choice
        instructions: pick a tier
        criteria:
          1: first line
          2: second line
          true: escalated
    route_on: tier
    routes:
      1: first
      2: second
      true: manager
`)
	if err != nil {
		t.Fatalf("ParseWorkflowConfig: %v", err)
	}
	routes := cfg.Nodes[0].Routes
	if routes["1"] != "first" || routes["2"] != "second" || routes["true"] != "manager" || len(routes) != 3 {
		t.Errorf("routes = %v, want every unquoted option key kept as a string", routes)
	}
}

func TestDecisionNodeCannotBeAnExitHandler(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, `
output_mapping: {r: "{{work.output}}"}
on_exit:
  always: audit
nodes:
  - {id: work, type: agent, agent_name: A}
  - id: audit
    type: decision
    questions:
      ok: {type: boolean, instructions: "The run went well"}
`)
	if err != nil {
		t.Fatalf("ParseWorkflowConfig: %v", err)
	}
	g, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("BuildDependencyGraph: %v", err)
	}
	err = ValidateExitHandler(cfg.OnExit, g)
	if err == nil || !strings.Contains(err.Error(), `onExit.always node "audit" is a decision node`) ||
		!strings.Contains(err.Error(), "agent, workflow and tool") {
		t.Fatalf("err = %v, want the decision exit-handler rejection naming the supported types", err)
	}
}

func TestDecisionTargetsMustDependOnTheNode(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, strings.Replace(decisionWorkflowYAML,
		"    agent_name: Tech\n    depends_on: [triage]", "    agent_name: Tech", 1))
	if err != nil {
		t.Fatalf("ParseWorkflowConfig: %v", err)
	}
	_, err = BuildDependencyGraph(cfg.Nodes)
	if err == nil || !strings.Contains(err.Error(), `routes to "tech" (routes.tech)`) {
		t.Fatalf("err = %v, want the routes.tech dependency error", err)
	}
}

func TestRoutingDecisionCannotBeAnIterationBody(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, `
output_mapping: {r: "{{each.output}}"}
nodes:
  - id: each
    type: map
    items: "{{workflow.input.pages}}"
    node: classify
  - id: classify
    type: decision
    questions:
      kind: {type: choice, instructions: x, criteria: {a: a, b: b}}
    route_on: kind
    routes: {a: sink, b: sink}
  - id: sink
    type: agent
    agent_name: A
    depends_on: [classify]
`)
	if err != nil {
		t.Fatalf("ParseWorkflowConfig: %v", err)
	}
	_, err = BuildDependencyGraph(cfg.Nodes)
	if err == nil || !strings.Contains(err.Error(), "cannot select branches") {
		t.Fatalf("err = %v, want the iteration-body routing error", err)
	}
}
