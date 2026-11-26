// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRunStopAnalysis_AHostileOrNilInputIsNeverProvenAway(t *testing.T) {
	t.Run("a nil analysis can stop a run for every node", func(t *testing.T) {
		var analysis *RunStopAnalysis
		if why, harmless := analysis.cannotStopBecause("anything"); harmless {
			t.Errorf("nil analysis said harmless: %q", why)
		}
		if p := analysis.FailingNodeProblem("n", "t", "m"); !p.Fatal || p.Downgraded() {
			t.Errorf("nil analysis graded %+v, want Fatal", p)
		}
	})
	t.Run("a node on a dependency cycle is not judged", func(t *testing.T) {
		body := appConfigFromYAML(t, `
fail_fast: true
nodes:
  - {id: off, type: tool, tool_name: ok, when: false}
  - {id: bad, type: tool, tool_name: missing, depends_on: [off, other]}
  - {id: other, type: tool, tool_name: ok, depends_on: [bad]}
  - {id: self, type: tool, tool_name: missing, depends_on: [self]}
`)
		for _, id := range []string{"bad", "other", "self"} {
			if why, harmless := AnalyzeRunStops(body).cannotStopBecause(id); harmless {
				t.Errorf("%s on a cycle judged harmless: %q", id, why)
			}
		}
	})
	t.Run("a malformed on_exit or node does not stop the analysis", func(t *testing.T) {
		body := appConfigFromYAML(t, `
on_exit: [not, a, handler]
nodes:
  - {id: bad, type: tool, tool_name: missing, when: false}
  - "not a node"
  - {type: tool}
`)
		if _, harmless := AnalyzeRunStops(body).cannotStopBecause("bad"); !harmless {
			t.Error("a literal-false guard was not judged harmless beside malformed siblings")
		}
	})
	t.Run("a long chain is judged in linear time", func(t *testing.T) {
		var nodes strings.Builder
		nodes.WriteString("nodes:\n  - {id: n0, type: tool, tool_name: ok, when: false}\n")
		const length = 50000
		for i := 1; i < length; i++ {
			fmt.Fprintf(&nodes, "  - {id: n%d, type: tool, tool_name: ok, depends_on: [n%d]}\n", i, i-1)
		}
		body := appConfigFromYAML(t, nodes.String())
		start := time.Now()
		analysis := AnalyzeRunStops(body)
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("analysing %d chained nodes took %s", length, elapsed)
		}
		if _, harmless := analysis.cannotStopBecause(fmt.Sprintf("n%d", length-1)); !harmless {
			t.Error("the end of a chain of dead nodes was not judged dead")
		}
	})
}

func TestStaticToolNodeProblems_FatalFollowsTheAnalysis(t *testing.T) {
	ts := toolSetWith(t, webRequestResultTool())
	node := func() *NodeConfig {
		return &NodeConfig{ID: "fetch", Type: "tool", ToolName: "web_request",
			Input: map[string]any{"url": "https://example.com", "output_filename": "x.jpg"}}
	}
	for _, tc := range []struct {
		name     string
		mutate   func(*WorkflowConfig)
		wantFail bool
	}{
		{"fail_fast, nothing else", func(*WorkflowConfig) {}, true},
		{"fail_fast false", func(c *WorkflowConfig) { c.FailFast = false }, true},
		{"an on_exit handler", func(c *WorkflowConfig) {
			c.Nodes = []*NodeConfig{{ID: "main", Type: "tool", ToolName: "web_request", Input: map[string]any{"url": "u"}}, node()}
			c.Nodes[1].DependsOn = []string{"main"}
			c.OnExit = &ExitHandler{Always: "fetch"}
		}, false},
		{"a literal false guard", func(c *WorkflowConfig) { c.Nodes[0].When = "false" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := planCfg([]any{builtinEntry("web_request")}, nil, node())
			tc.mutate(cfg)
			problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{})
			var found *StaticProblem
			for i := range problems {
				if problems[i].NodeID == "fetch" && strings.Contains(problems[i].Message, "output_filename") {
					found = &problems[i]
				}
			}
			if found == nil {
				t.Fatalf("no problem for the undeclared argument: %+v", problems)
			}
			if found.Fatal != tc.wantFail || found.Downgraded() == tc.wantFail {
				t.Errorf("problem = %+v, want Fatal %v", found, tc.wantFail)
			}
			if found.ToolName != "web_request" {
				t.Errorf("ToolName = %q, want web_request", found.ToolName)
			}
		})
	}
}

func TestToolInputProblem_FatalFollowsTheAnalysis(t *testing.T) {
	missing := map[string]any{"carrier_code": "ups"}
	for _, tc := range []struct {
		name    string
		stops   *RunStopAnalysis
		wantMsg string
		fatal   bool
	}{
		{"no analysis", nil, "", true},
		{"a node that can stop a run", analyzeParsedRunStops(nil, []*NodeConfig{{ID: "ship", Type: "tool"}}), "", true},
		{"an on_exit handler", analyzeParsedRunStops(&ExitHandler{Always: "ship"}, []*NodeConfig{{ID: "ship", Type: "tool"}}), "on_exit handler", false},
		{"a literal false guard", analyzeParsedRunStops(nil, []*NodeConfig{{ID: "ship", Type: "tool", When: "false"}}), "never runs", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := getShipmentTarget
			target.RunStops = tc.stops
			finding := ToolInputProblem(target, missing)
			if finding == nil || finding.Fatal != tc.fatal || finding.Downgraded() == tc.fatal {
				t.Fatalf("finding = %+v, want Fatal %v", finding, tc.fatal)
			}
			if tc.wantMsg != "" && !strings.Contains(finding.Message, tc.wantMsg) {
				t.Errorf("message %q does not say why: want %q", finding.Message, tc.wantMsg)
			}
			if finding.NodeID != target.NodeID || finding.ToolName != target.ToolName {
				t.Errorf("finding names node %q tool %q, want %q and %q", finding.NodeID, finding.ToolName, target.NodeID, target.ToolName)
			}
		})
	}
	t.Run("an undeclared key alone never blocks, whatever the node", func(t *testing.T) {
		target := getShipmentTarget
		target.RunStops = nil
		finding := ToolInputProblem(target, map[string]any{"tracking": "x", "stray": "y"})
		if finding == nil || finding.Fatal || finding.Downgraded() {
			t.Errorf("finding = %+v, want an advisory that is not a downgrade", finding)
		}
	})
}

func TestRunStopAnalysis_ADuplicateIdIsNeverProvenAway(t *testing.T) {
	body := appConfigFromYAML(t, `
tools:
  - {tool_type: builtin, tool_name: ok}
nodes:
  - {id: x, type: tool, tool_name: ok, when: false}
  - {id: x, type: tool, tool_name: missing}
`)
	for _, p := range UnknownToolNodeProblems(body) {
		if p.NodeID == "x" && !p.Fatal {
			t.Errorf("a second node named x was downgraded: %+v", p)
		}
	}
	if _, harmless := AnalyzeRunStops(body).cannotStopBecause("x"); harmless {
		t.Error("a duplicated id was judged harmless")
	}
}

func TestStaticProblem_NeverFatalAndDowngraded(t *testing.T) {
	for _, tc := range runStopCases {
		_, raw := parseRunStopBody(t, tc.body)
		for _, p := range UnknownToolNodeProblems(raw) {
			if p.Fatal && p.Downgraded() {
				t.Errorf("%s: problem is both Fatal and downgraded: %+v", tc.name, p)
			}
		}
	}
}
