// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func TestParseMaxCost(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		watch   bool
		want    string
		wantErr string
	}{
		{"no ceiling", "", true, "", ""},
		{"no ceiling without watching", "", false, "", ""},
		{"a decimal", "2.50", true, "5/2", ""},
		{"zero", "0", true, "0", ""},
		{"a malformed amount", "2,50", true, "", "not a non-negative decimal"},
		{"a currency sign", "$2", true, "", "not a non-negative decimal"},
		{"a negative amount", "-1", true, "", "not a non-negative decimal"},
		{"without watching", "1", false, "", "--max-cost needs --watch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMaxCost(tc.raw, tc.raw != "", tc.watch)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if tc.want == "" {
				if got != nil {
					t.Errorf("ceiling = %v, want none", got.amount)
				}
				return
			}
			if got == nil || got.amount.RatString() != tc.want || got.text != tc.raw {
				t.Errorf("ceiling = %+v, want %s from %q", got, tc.want, tc.raw)
			}
		})
	}

	if _, err := parseMaxCost("", true, true); err == nil || !strings.Contains(err.Error(), "not a non-negative decimal") {
		t.Errorf("an empty value given: err = %v, want it refused rather than read as no ceiling", err)
	}
}

func TestSumKnownCosts(t *testing.T) {
	results := []evalplatform.RunResultResponse{
		{Cost: new("0.0012")}, {Cost: new("0.4")}, {}, {Cost: new("lots")},
	}

	got := sumKnownCosts(results)

	if got.known.FloatString(4) != "0.4012" || got.unknown != 2 {
		t.Errorf("sum = %s with %d unknown, want 0.4012 with 2 (one absent, one unreadable)", got.known.FloatString(4), got.unknown)
	}
}

func TestRunVerdictGatesOnTheKnownCosts(t *testing.T) {
	run := &evalplatform.RunResponse{ID: "run-1", Status: evalplatform.StatusCompleted}
	ceiling := func(raw string) *costCeiling {
		c, err := parseMaxCost(raw, true, true)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	priced := func(costs ...string) []evalplatform.RunResultResponse {
		out := make([]evalplatform.RunResultResponse, 0, len(costs))
		for _, c := range costs {
			out = append(out, evalplatform.RunResultResponse{Cost: new(c)})
		}
		return out
	}
	unknown := evalplatform.RunResultResponse{}
	cases := []struct {
		name        string
		results     []evalplatform.RunResultResponse
		ceiling     *costCeiling
		wantErr     string
		wantUnknown int
	}{
		{"under the ceiling", priced("0.0012", "0.4"), ceiling("0.50"), "", 0},
		{"exactly the ceiling", priced("0.25", "0.25"), ceiling("0.50"), "", 0},
		{"over the ceiling", priced("0.3", "0.3000000001"), ceiling("0.60"), "cost at least 0.600000, over --max-cost 0.60", 0},
		{"an unknown cost under the ceiling", append(priced("0.1"), unknown), ceiling("1"), "", 1},
		{"known costs over the ceiling beside an unknown one", append(priced("0.6", "0.6"), unknown), ceiling("1"), "cost at least 1.200000", 1},
		{"every cost unknown", []evalplatform.RunResultResponse{unknown, unknown}, ceiling("0"), "", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cost, err := runVerdict(run, tc.results, runGate{ceiling: tc.ceiling})

			if cost == nil || cost.unknown != tc.wantUnknown {
				t.Errorf("cost = %+v, want %d unknown", cost, tc.wantUnknown)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("err = %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}

	if cost, err := runVerdict(run, []evalplatform.RunResultResponse{unknown}, runGate{}); cost != nil || err != nil {
		t.Errorf("no ceiling: cost %+v, err %v; want neither", cost, err)
	}
}

func TestRunVerdictReportsCostEvenWhenTheRunHadWarnings(t *testing.T) {
	run := &evalplatform.RunResponse{ID: "run-1", Status: evalplatform.StatusCompletedWithWarnings}
	results := []evalplatform.RunResultResponse{{Cost: new("0.04")}, {Cost: new("0")}, {}}
	ceiling, err := parseMaxCost("0.01", true, true)
	if err != nil {
		t.Fatal(err)
	}

	cost, err := runVerdict(run, results, runGate{threshold: 0.9, ceiling: ceiling})

	if err == nil || !strings.Contains(err.Error(), "completed with warnings") || !strings.Contains(err.Error(), "over --max-cost") {
		t.Errorf("err = %v, want both the warnings and the cost breach", err)
	}
	if cost == nil || cost.unknown != 1 {
		t.Errorf("cost = %+v, want the trial with no cost counted as unknown", cost)
	}
}

func TestExperimentTargetColumns(t *testing.T) {
	agent := wire.ExperimentResponse{TargetAgent: "OrderDesk"}
	workflow := wire.ExperimentResponse{TargetType: "workflow", TargetWorkflow: new("order_flow")}

	if experimentTargetType(agent) != "agent" || experimentTargetName(agent) != "OrderDesk" {
		t.Errorf("agent experiment = %s %s", experimentTargetType(agent), experimentTargetName(agent))
	}
	if experimentTargetType(workflow) != "workflow" || experimentTargetName(workflow) != "order_flow" {
		t.Errorf("workflow experiment = %s %s", experimentTargetType(workflow), experimentTargetName(workflow))
	}
}

func TestRunVerdictAllowWarnings(t *testing.T) {
	warned := &evalplatform.RunResponse{ID: "run-1", Status: evalplatform.StatusCompletedWithWarnings}
	clean := &evalplatform.RunResponse{ID: "run-4", Status: evalplatform.StatusCompleted}
	passed, failed := true, false
	scored := func(verdicts ...*bool) evalplatform.RunResultResponse {
		r := evalplatform.RunResultResponse{Cost: new("0.04")}
		for _, v := range verdicts {
			r.Scores = append(r.Scores, evalplatform.ScoreResponse{Passed: v})
		}
		return r
	}
	endedEarly := evalplatform.RunResultResponse{FailureKind: new("node_failed")}
	agentErrored := scored(&passed)
	agentErrored.ErrorMessage = new("Agent error: boom")
	trials := func(n int, r evalplatform.RunResultResponse) []evalplatform.RunResultResponse {
		return slices.Repeat([]evalplatform.RunResultResponse{r}, n)
	}
	ceiling, err := parseMaxCost("0.05", true, true)
	if err != nil {
		t.Fatal(err)
	}
	allow := runGate{threshold: 0.9, allowWarnings: true}
	cases := []struct {
		name    string
		run     *evalplatform.RunResponse
		results []evalplatform.RunResultResponse
		gate    runGate
		wantErr string
	}{
		{"every trial scored and passed", warned, trials(2, scored(&passed)), allow, ""},
		{"one of ten ended early, at 0.9", warned, append(trials(9, scored(&passed)), endedEarly), allow, ""},
		{"two of ten ended early, at 0.9", warned, append(trials(8, scored(&passed)), endedEarly, endedEarly), allow, "80.00% below threshold 90.00% (8/10 passed; 2 trial(s) with no score count as failures)"},
		{"one scored trial among a hundred", warned, append(trials(99, endedEarly), scored(&passed)), runGate{threshold: 1, allowWarnings: true}, "(1/100 passed"},
		{"an ended-early trial fails once per evaluator", warned, append(trials(3, scored(&passed, &passed)), endedEarly), runGate{threshold: 0.8, allowWarnings: true}, "(6/8 passed"},
		{"a trial the agent errored on but the judge passed", warned, append(trials(4, scored(&passed)), agentErrored), allow, "(4/5 passed; 1 errored trial(s) count as failures)"},
		{"a run whose rows did not all land", &evalplatform.RunResponse{ID: "run-5", Status: evalplatform.StatusCompletedWithWarnings, ProgressTotal: 100}, append(trials(55, scored(&passed)), trials(5, endedEarly)...), allow, "(55/100 passed; 45 trial(s) with no score"},
		{"score-only evaluators leave nothing to check", warned, []evalplatform.RunResultResponse{scored(nil), endedEarly}, allow, "has no pass/fail score to check"},
		{"nothing scored", warned, trials(2, endedEarly), allow, "has no pass/fail score to check"},
		{"no results at all", warned, nil, allow, "has no pass/fail score to check"},
		{"warnings not allowed", warned, trials(2, scored(&passed)), runGate{threshold: 0.9}, "completed with warnings"},
		{"a failed score", warned, []evalplatform.RunResultResponse{scored(&passed), scored(&failed)}, allow, "below threshold"},
		{"over the ceiling", warned, trials(2, scored(&passed)), runGate{threshold: 0.9, ceiling: ceiling, allowWarnings: true}, "over --max-cost"},
		{"a failed run", &evalplatform.RunResponse{ID: "run-2", Status: evalplatform.StatusFailed}, trials(1, scored(&passed)), allow, "failed"},
		{"a cancelled run", &evalplatform.RunResponse{ID: "run-3", Status: evalplatform.StatusCancelled}, trials(1, scored(&passed)), allow, "cancelled"},
		{"a completed run with a scoreless trial, flag on", clean, append(trials(9, scored(&passed)), evalplatform.RunResultResponse{}), allow, ""},
		{"a completed run with a scoreless trial, flag off", clean, append(trials(9, scored(&passed)), evalplatform.RunResultResponse{}), runGate{threshold: 0.9}, ""},
		{"a completed run missing rows, flag on", &evalplatform.RunResponse{ID: "run-6", Status: evalplatform.StatusCompleted, ProgressTotal: 20}, trials(9, scored(&passed)), allow, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runVerdict(tc.run, tc.results, tc.gate)

			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("err = %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCountForGateCountsBrokenTrialsOnlyWhenAsked(t *testing.T) {
	passed := true
	run := &evalplatform.RunResponse{ProgressTotal: 9}
	results := []evalplatform.RunResultResponse{
		{Scores: []evalplatform.ScoreResponse{{Passed: &passed}}},
		{Scores: []evalplatform.ScoreResponse{{Passed: &passed}, {Passed: &passed}}},
		{Scores: []evalplatform.ScoreResponse{{Passed: &passed}, {Passed: nil}}},
		{FailureKind: new("timeout")},
		{FailureKind: new("auth_required")},
		{FailureKind: new("node_failed")},
		{FailureKind: new("paused_for_input")},
		{},
	}

	if c := countForGate(run, results, false); c.pass != 4 || c.total != 4 || c.counted {
		t.Errorf("not counting: %+v, want 4/4 and nothing counted", c)
	}
	c := countForGate(run, results, true)
	if c.pass != 4 || c.total != 4+6*2 || c.ungraded != 6 {
		t.Errorf("counting: %+v, want 4 passing of 16 (6 ungraded at the most verdicts any scored trial got, 2)", c)
	}
	if got := c.ungradedKinds(); got != "auth_required 1, no_result 1, node_failed 1, paused_for_input 1, timeout 1, unclassified 1" {
		t.Errorf("kinds = %q, want them sorted, with the missing row and the unclassified trial named", got)
	}
}

func TestRunGateCountsBrokenTrialsOnlyForAWarnedRunItLetsThrough(t *testing.T) {
	statuses := []string{evalplatform.StatusCompleted, evalplatform.StatusCompletedWithWarnings, evalplatform.StatusFailed}
	for _, allow := range []bool{false, true} {
		for _, status := range statuses {
			want := allow && status == evalplatform.StatusCompletedWithWarnings
			if got := (runGate{allowWarnings: allow}).countsBroken(&evalplatform.RunResponse{Status: status}); got != want {
				t.Errorf("allowWarnings %v, status %s: counts broken = %v, want %v", allow, status, got, want)
			}
		}
	}
}

func TestEvalRunHelpDocumentsAllowWarnings(t *testing.T) {
	cmd := newEvalRunCmd()
	if f := cmd.Flags().Lookup("allow-warnings"); f == nil || f.DefValue != "false" {
		t.Fatalf("--allow-warnings flag = %+v, want a flag defaulting to false", f)
	}
	if !strings.Contains(cmd.Long, "--allow-warnings") || !strings.Contains(cmd.Example, "--allow-warnings") {
		t.Error("the help text or examples do not mention --allow-warnings")
	}
}
