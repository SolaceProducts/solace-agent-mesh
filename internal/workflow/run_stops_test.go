// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type runStopCase struct {
	name       string
	body       string
	input      map[string]any
	harmless   bool
	outcome    string
	failedNode string
}

const runStopHeader = `
output_mapping: {done: "yes"}
tools:
  - {tool_type: builtin, tool_name: ok}
`

var runStopCases = []runStopCase{
	{
		name:     "fail_fast false: the failed node is skipped and the run finishes",
		body:     "fail_fast: false\nnodes:\n  - {id: bad, type: tool, tool_name: missing}\n",
		harmless: false, outcome: "success", failedNode: "bad",
	},
	{
		name:     "fail_fast false, behind a guard that reads data",
		body:     "fail_fast: false\nnodes:\n  - {id: bad, type: tool, tool_name: missing, when: \"{{workflow.input.go}} == true\"}\n",
		input:    map[string]any{"go": true},
		harmless: false, outcome: "success", failedNode: "bad",
	},
	{
		name: "fail_fast false: a map template",
		body: `
fail_fast: false
nodes:
  - {id: each, type: map, node: bad, items: [a, b]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [each]}
`,
		harmless: false, outcome: "success",
	},
	{
		name: "fail_fast false: a loop body",
		body: `
fail_fast: false
nodes:
  - {id: again, type: loop, node: bad, condition: "false", max_iterations: 1}
  - {id: bad, type: tool, tool_name: missing, depends_on: [again]}
`,
		harmless: false, outcome: "success", failedNode: "again",
	},
	{
		name: "fail_fast false: downstream of the failed node",
		body: `
fail_fast: false
nodes:
  - {id: first, type: tool, tool_name: ok}
  - {id: bad, type: tool, tool_name: missing, depends_on: [first]}
  - {id: after, type: tool, tool_name: missing, depends_on: [bad]}
`,
		harmless: false, outcome: "success", failedNode: "bad",
	},
	{
		name:     "an on_exit handler written as a string",
		body:     "on_exit: bad\nnodes:\n  - {id: main, type: tool, tool_name: ok}\n  - {id: bad, type: tool, tool_name: missing, depends_on: [main]}\n",
		harmless: true, outcome: "success", failedNode: "",
	},
	{
		name: "an on_exit handler for success, the run succeeding",
		body: `
on_exit: {on_success: bad}
nodes:
  - {id: main, type: tool, tool_name: ok}
  - {id: bad, type: tool, tool_name: missing, depends_on: [main]}
`,
		harmless: true, outcome: "success", failedNode: "",
	},
	{
		name: "an always handler beside a failing run",
		body: `
on_exit: {always: bad}
nodes:
  - {id: main, type: tool, tool_name: missing}
  - {id: bad, type: tool, tool_name: missing}
`,
		harmless: true, outcome: "failure", failedNode: "main",
	},
	{
		name:     "a guard that is the literal false",
		body:     "nodes:\n  - {id: bad, type: tool, tool_name: missing, when: false}\n",
		harmless: true, outcome: "success",
	},
	{
		name:     "a guard that is the string false",
		body:     "nodes:\n  - {id: bad, type: tool, tool_name: missing, when: \"false\"}\n",
		harmless: true, outcome: "success",
	},
	{
		name: "waiting only for a node that never runs",
		body: `
nodes:
  - {id: off, type: tool, tool_name: ok, when: false}
  - {id: bad, type: tool, tool_name: missing, depends_on: [off]}
`,
		harmless: true, outcome: "success",
	},
	{
		name: "waiting only for nodes that never run, two levels down",
		body: `
nodes:
  - {id: off, type: tool, tool_name: ok, when: false}
  - {id: mid, type: tool, tool_name: ok, depends_on: [off]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [mid, off]}
`,
		harmless: true, outcome: "success",
	},
	{
		name: "a map that iterates a literal empty list",
		body: `
nodes:
  - {id: each, type: map, node: bad, items: []}
  - {id: bad, type: tool, tool_name: missing, depends_on: [each]}
`,
		harmless: true, outcome: "success",
	},
	{
		name: "a map that never runs",
		body: `
nodes:
  - {id: off, type: tool, tool_name: ok, when: false}
  - {id: each, type: map, node: bad, items: [a], depends_on: [off]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [each]}
`,
		harmless: true, outcome: "success",
	},
	{
		name: "a loop that never runs",
		body: `
nodes:
  - {id: off, type: tool, tool_name: ok, when: false}
  - {id: again, type: loop, node: bad, condition: "false", depends_on: [off]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [again]}
`,
		harmless: true, outcome: "success",
	},
	{
		name: "a switch case that is the literal false",
		body: `
nodes:
  - {id: pick, type: switch, cases: [{condition: "false", node: bad}, {condition: "true", node: good}]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [pick]}
  - {id: good, type: tool, tool_name: ok, depends_on: [pick]}
`,
		harmless: true, outcome: "success",
	},
	{
		name: "a switch case shadowed by an earlier case that is the literal true",
		body: `
nodes:
  - {id: pick, type: switch, cases: [{condition: "true", node: good}, {condition: "{{workflow.input.x}} == 1", node: bad}]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [pick]}
  - {id: good, type: tool, tool_name: ok, depends_on: [pick]}
`,
		input:    map[string]any{"x": 1},
		harmless: true, outcome: "success",
	},
	{
		name: "a switch default that a literal true case always pre-empts",
		body: `
nodes:
  - {id: pick, type: switch, cases: [{condition: "true", node: good}], default: bad}
  - {id: bad, type: tool, tool_name: missing, depends_on: [pick]}
  - {id: good, type: tool, tool_name: ok, depends_on: [pick]}
`,
		harmless: true, outcome: "success",
	},
	{
		name: "a decision route that waits for a rerouting escalation that runs",
		body: `
nodes:
  - {id: live, type: tool, tool_name: ok}
  - {id: reclassify, type: tool, tool_name: ok, depends_on: [triage, live]}
  - id: triage
    type: decision
    when: "false"
    questions:
      department: {type: choice, instructions: "Which team owns the ticket?", criteria: {billing: x, technical: y}}
    route_on: department
    routes: {billing: bad, technical: bad}
    min_confidence: 0.9
    escalate: reclassify
  - {id: bad, type: tool, tool_name: missing, depends_on: [triage]}
`,
		harmless: false, outcome: "failure", failedNode: "bad",
	},
	{
		name: "a node that depends on a switch without being one of its targets",
		body: `
nodes:
  - {id: pick, type: switch, cases: [{condition: "true", node: good}]}
  - {id: good, type: tool, tool_name: ok, depends_on: [pick]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [pick]}
`,
		harmless: false, outcome: "failure", failedNode: "bad",
	},
	{
		name: "an on_exit handler written with the camelCase key",
		body: `
onExit: {onSuccess: bad}
nodes:
  - {id: main, type: tool, tool_name: ok}
  - {id: bad, type: tool, tool_name: missing, depends_on: [main]}
`,
		harmless: true, outcome: "success",
	},
	{
		name: "a loop body with its own guard under a loop that runs",
		body: `
nodes:
  - {id: again, type: loop, node: bad, condition: "false", max_iterations: 1}
  - {id: bad, type: tool, tool_name: missing, when: false, depends_on: [again]}
`,
		harmless: false, outcome: "failure", failedNode: "again",
	},
	{
		name: "a map template with its own guard under a map that runs",
		body: `
nodes:
  - {id: each, type: map, node: bad, items: [a]}
  - {id: bad, type: tool, tool_name: missing, when: false, depends_on: [each]}
`,
		harmless: false, outcome: "failure", failedNode: "each",
	},
	{
		name:     "no guard and no fail_fast: the node can stop the run",
		body:     "nodes:\n  - {id: bad, type: tool, tool_name: missing}\n",
		harmless: false, outcome: "failure", failedNode: "bad",
	},
	{
		name:     "fail_fast written as a string is not the boolean the engine reads",
		body:     "fail_fast: \"false\"\nnodes:\n  - {id: bad, type: tool, tool_name: missing}\n",
		harmless: false, outcome: "failure", failedNode: "bad",
	},
	{
		name:     "a guard that reads data may pass",
		body:     "nodes:\n  - {id: bad, type: tool, tool_name: missing, when: \"{{workflow.input.go}} == true\"}\n",
		input:    map[string]any{"go": true},
		harmless: false, outcome: "failure", failedNode: "bad",
	},
	{
		name:     "a guard that reads data may also not pass, which is why it stays refused",
		body:     "nodes:\n  - {id: bad, type: tool, tool_name: missing, when: \"{{workflow.input.go}} == true\"}\n",
		input:    map[string]any{"go": false},
		harmless: false, outcome: "success",
	},
	{
		name:     "a guard that is the literal true",
		body:     "nodes:\n  - {id: bad, type: tool, tool_name: missing, when: \"true\"}\n",
		harmless: false, outcome: "failure", failedNode: "bad",
	},
	{
		name: "a switch case that reads data",
		body: `
nodes:
  - {id: pick, type: switch, cases: [{condition: "{{workflow.input.x}} == 1", node: bad}], default: good}
  - {id: bad, type: tool, tool_name: missing, depends_on: [pick]}
  - {id: good, type: tool, tool_name: ok, depends_on: [pick]}
`,
		input:    map[string]any{"x": 1},
		harmless: false, outcome: "failure", failedNode: "bad",
	},
	{
		name: "a switch default with no case that is the literal true",
		body: `
nodes:
  - {id: pick, type: switch, cases: [{condition: "false", node: good}], default: bad}
  - {id: bad, type: tool, tool_name: missing, depends_on: [pick]}
  - {id: good, type: tool, tool_name: ok, depends_on: [pick]}
`,
		harmless: false, outcome: "failure", failedNode: "bad",
	},
	{
		name: "a switch target that also waits for a node that runs is not proven away",
		body: `
nodes:
  - {id: other, type: tool, tool_name: ok}
  - {id: pick, type: switch, cases: [{condition: "true", node: good}], default: bad, depends_on: [other]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [pick, other]}
  - {id: good, type: tool, tool_name: ok, depends_on: [pick]}
`,
		harmless: false, outcome: "success",
	},
	{
		name: "a node that waits for one that never runs and one that does",
		body: `
nodes:
  - {id: off, type: tool, tool_name: ok, when: false}
  - {id: live, type: tool, tool_name: ok}
  - {id: bad, type: tool, tool_name: missing, depends_on: [off, live]}
`,
		harmless: false, outcome: "failure", failedNode: "bad",
	},
	{
		name: "a map whose items are read from the input may have items",
		body: `
nodes:
  - {id: each, type: map, node: bad, items: "{{workflow.input.items}}"}
  - {id: bad, type: tool, tool_name: missing, depends_on: [each]}
`,
		input:    map[string]any{"items": []any{"a"}},
		harmless: false, outcome: "failure", failedNode: "each",
	},
	{
		name: "a map with items",
		body: `
nodes:
  - {id: each, type: map, node: bad, items: [a]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [each]}
`,
		harmless: false, outcome: "failure", failedNode: "each",
	},
	{
		name: "a loop body always runs once",
		body: `
nodes:
  - {id: again, type: loop, node: bad, condition: "false", max_iterations: 1}
  - {id: bad, type: tool, tool_name: missing, depends_on: [again]}
`,
		harmless: false, outcome: "failure", failedNode: "again",
	},
	{
		name: "an on_exit handler that is also the template of a map with items",
		body: `
on_exit: {always: bad}
nodes:
  - {id: each, type: map, node: bad, items: [a]}
  - {id: bad, type: tool, tool_name: missing, depends_on: [each]}
`,
		harmless: false, outcome: "failure", failedNode: "each",
	},
}

func parseRunStopBody(t *testing.T, body string) (*WorkflowConfig, map[string]any) {
	t.Helper()
	raw := appConfigFromYAML(t, runStopHeader+body)
	cfg, err := ParseWorkflowConfig("wf", "ns", raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg, raw
}

func runRunStopCase(t *testing.T, tc runStopCase) (outcome string, failed []string) {
	t.Helper()
	cfg, _ := parseRunStopBody(t, tc.body)
	dag, err := buildValidatedDAG(cfg)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	state := NewExecutionState("wf", "exec-run-stops")
	if tc.input != nil {
		state.NodeOutputs["workflow_input"] = map[string]any{"output": tc.input}
	}
	e, _ := newCapturingExecutor(t, cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)
	e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "ok"}, result: tool.OK("fine")})
	runToCompletion(t, e)
	for _, f := range e.state.FailedNodes {
		failed = append(failed, f.FailedNodeID)
	}
	return e.state.PendingOutcome, failed
}

func TestRunStopAnalysis_MirrorsTheExecutor(t *testing.T) {
	for _, tc := range runStopCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, raw := parseRunStopBody(t, tc.body)

			_, harmless := AnalyzeRunStops(raw).cannotStopBecause("bad")
			if harmless != tc.harmless {
				t.Errorf("AnalyzeRunStops: harmless = %v, want %v", harmless, tc.harmless)
			}
			_, harmlessFromCfg := analyzeParsedRunStops(cfg.OnExit, cfg.Nodes).cannotStopBecause("bad")
			if harmlessFromCfg != harmless {
				t.Errorf("the parsed-config path says harmless = %v and the raw path %v: the two must agree", harmlessFromCfg, harmless)
			}

			outcome, failed := runRunStopCase(t, tc)
			if outcome != tc.outcome {
				t.Errorf("run outcome = %q, want %q (failed nodes %v)", outcome, tc.outcome, failed)
			}
			var wantFailed []string
			if tc.failedNode != "" {
				wantFailed = []string{tc.failedNode}
			}
			if !slices.Equal(failed, wantFailed) {
				t.Errorf("failed nodes = %v, want %v", failed, wantFailed)
			}
		})
	}
}

func TestUnknownToolNodeProblems_FatalFollowsTheAnalysis(t *testing.T) {
	for _, tc := range runStopCases {
		t.Run(tc.name, func(t *testing.T) {
			_, raw := parseRunStopBody(t, tc.body)
			var bad []StaticProblem
			for _, p := range UnknownToolNodeProblems(raw) {
				if p.NodeID == "bad" {
					bad = append(bad, p)
				}
			}
			if len(bad) != 1 {
				t.Fatalf("problems for bad = %d, want 1: %+v", len(bad), bad)
			}
			if bad[0].Fatal == tc.harmless {
				t.Errorf("Fatal = %v for a node whose harmless = %v", bad[0].Fatal, tc.harmless)
			}
			if tc.harmless && bad[0].NotFatalBecause == "" {
				t.Errorf("a problem that does not block carries no reason: %+v", bad[0])
			}
			if !tc.harmless && bad[0].NotFatalBecause != "" {
				t.Errorf("a blocking problem carries a reason it does not block: %+v", bad[0])
			}
		})
	}
}
