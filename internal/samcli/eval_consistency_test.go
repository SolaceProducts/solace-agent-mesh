// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"

	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

type consistencyVerdict struct {
	evaluatorID string
	passed      bool
}

func consistencyRow(exampleID string, verdicts ...consistencyVerdict) evalplatform.RunResultResponse {
	r := evalplatform.RunResultResponse{ExampleID: exampleID, Scores: []evalplatform.ScoreResponse{}}
	for _, v := range verdicts {
		r.Scores = append(r.Scores, evalplatform.ScoreResponse{EvaluatorID: v.evaluatorID, Passed: &v.passed})
	}
	return r
}

func passedBy(id string) consistencyVerdict { return consistencyVerdict{id, true} }
func failedBy(id string) consistencyVerdict { return consistencyVerdict{id, false} }

func withAgentError(r evalplatform.RunResultResponse) evalplatform.RunResultResponse {
	r.ErrorMessage = new("Agent error: boom")
	return r
}

func unscoredRow(exampleID string) evalplatform.RunResultResponse {
	return evalplatform.RunResultResponse{ExampleID: exampleID, Scores: []evalplatform.ScoreResponse{}}
}

func TestConsistencyByEvaluator(t *testing.T) {
	scoreOnly := evalplatform.RunResultResponse{ExampleID: "ex-score-only", Scores: []evalplatform.ScoreResponse{{EvaluatorID: "ev-score", Score: 0.4}}}
	results := []evalplatform.RunResultResponse{
		consistencyRow("ex-steady", passedBy("ev-b"), passedBy("ev-a")), consistencyRow("ex-steady", passedBy("ev-b"), passedBy("ev-a")), consistencyRow("ex-steady", passedBy("ev-b"), passedBy("ev-a")),
		consistencyRow("ex-flaky", passedBy("ev-b"), failedBy("ev-a")), consistencyRow("ex-flaky", failedBy("ev-b"), failedBy("ev-a")), consistencyRow("ex-flaky", passedBy("ev-b"), failedBy("ev-a")),
		consistencyRow("ex-short", passedBy("ev-a")), consistencyRow("ex-short", passedBy("ev-a")),
		withAgentError(consistencyRow("ex-errored", passedBy("ev-a"))), withAgentError(consistencyRow("ex-errored", passedBy("ev-a"))), withAgentError(consistencyRow("ex-errored", passedBy("ev-a"))),
		unscoredRow("ex-walled"), unscoredRow("ex-walled"), unscoredRow("ex-walled"),
		consistencyRow("ex-one-evaluator", passedBy("ev-b")), consistencyRow("ex-one-evaluator", passedBy("ev-b")), consistencyRow("ex-one-evaluator", passedBy("ev-b")),
		scoreOnly, scoreOnly,
	}
	names := map[string]string{"ev-a": "Zeta", "ev-b": "Alpha"}

	got := consistencyByEvaluator(results, names, runShape{trialsPerExample: 3, examples: 9})

	want := []evaluatorConsistency{
		{EvaluatorID: "ev-b", EvaluatorName: "Alpha", Examples: 9, PassedEvery: 2, FailedEvery: 6, Inconsistent: 1, TrialsPerExample: 3},
		{EvaluatorID: "ev-a", EvaluatorName: "Zeta", Examples: 9, PassedEvery: 1, FailedEvery: 7, Inconsistent: 1, TrialsPerExample: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("consistency = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("consistency[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	var out bytes.Buffer
	writeConsistency(&lineLogger{mu: &sync.Mutex{}, w: &out, resultW: &out}, got)
	wantText := "Consistency across trials:\n" +
		"  Alpha: pass^3 2/9 examples, 1 inconsistent, 6 failed every trial\n" +
		"  Zeta: pass^3 1/9 examples, 1 inconsistent, 7 failed every trial\n"
	if out.String() != wantText {
		t.Errorf("summary =\n%s\nwant\n%s", out.String(), wantText)
	}
}

func TestConsistencyPassesWhatTheGatePasses(t *testing.T) {
	cases := []struct {
		name string
		row  evalplatform.RunResultResponse
	}{
		{"passed", consistencyRow("ex-1", passedBy("ev-a"))},
		{"failed", consistencyRow("ex-1", failedBy("ev-a"))},
		{"agent errored, passed", withAgentError(consistencyRow("ex-1", passedBy("ev-a")))},
		{"agent errored, failed", withAgentError(consistencyRow("ex-1", failedBy("ev-a")))},
		{"no scores", unscoredRow("ex-1")},
		{"score only", evalplatform.RunResultResponse{ExampleID: "ex-1", Scores: []evalplatform.ScoreResponse{{EvaluatorID: "ev-a", Score: 0.9}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := []evalplatform.RunResultResponse{consistencyRow("ex-1", failedBy("ev-a")), tc.row}
			report := consistencyByEvaluator(results, nil, runShape{trialsPerExample: 2, examples: 1})
			reportPassed := 0
			if len(report) == 1 && report[0].Inconsistent == 1 {
				reportPassed = 1
			}
			gate := countForGate(&evalplatform.RunResponse{ProgressTotal: len(results)}, results, true)
			if reportPassed != gate.pass {
				t.Errorf("the report passed %d trial(s), the gate %d", reportPassed, gate.pass)
			}
		})
	}
}

func TestConsistencyByEvaluatorEdges(t *testing.T) {
	steady := []evalplatform.RunResultResponse{consistencyRow("ex-1", passedBy("ev-a"), passedBy("ev-b")), consistencyRow("ex-1", passedBy("ev-a"), passedBy("ev-b"))}

	t.Run("one trial per example says nothing", func(t *testing.T) {
		if got := consistencyByEvaluator(steady[:1], nil, runShape{trialsPerExample: 1}); len(got) != 0 {
			t.Errorf("consistency = %+v, want none", got)
		}
	})
	t.Run("no pass/fail verdict anywhere says nothing", func(t *testing.T) {
		rows := []evalplatform.RunResultResponse{unscoredRow("ex-1"), unscoredRow("ex-1")}
		if got := consistencyByEvaluator(rows, nil, runShape{trialsPerExample: 2}); len(got) != 0 {
			t.Errorf("consistency = %+v, want none", got)
		}
	})
	t.Run("an unknown shape falls back to the most results", func(t *testing.T) {
		got := consistencyByEvaluator(steady, nil, runShape{})
		if len(got) != 2 || got[0].TrialsPerExample != 2 || got[0].PassedEvery != 1 || got[0].Examples != 1 {
			t.Errorf("consistency = %+v, want one example passing both of its trials", got)
		}
	})
	t.Run("more results than recorded raise the trial count", func(t *testing.T) {
		if got := consistencyByEvaluator(steady, nil, runShape{trialsPerExample: 1}); len(got) != 2 || got[0].TrialsPerExample != 2 {
			t.Errorf("consistency = %+v, want two trials per example", got)
		}
	})
	t.Run("a trial whose result never arrived fails", func(t *testing.T) {
		got := consistencyByEvaluator(steady, nil, runShape{trialsPerExample: 3})
		if len(got) != 2 || got[0].TrialsPerExample != 3 || got[0].PassedEvery != 0 || got[0].Inconsistent != 1 {
			t.Errorf("consistency = %+v, want pass^3 0/1 with the example inconsistent", got)
		}
	})
	t.Run("an example that returned nothing fails every trial", func(t *testing.T) {
		got := consistencyByEvaluator(steady, nil, runShape{trialsPerExample: 2, examples: 3})
		if len(got) != 2 || got[0].Examples != 3 || got[0].PassedEvery != 1 || got[0].FailedEvery != 2 {
			t.Errorf("consistency = %+v, want pass^2 1/3 with two examples failing every trial", got)
		}
	})
	t.Run("unknown names fall back to ids, sorted", func(t *testing.T) {
		got := consistencyByEvaluator(steady, nil, runShape{trialsPerExample: 2})
		if len(got) != 2 || got[0].EvaluatorName != "ev-a" || got[1].EvaluatorName != "ev-b" {
			t.Errorf("consistency = %+v, want ev-a then ev-b", got)
		}
	})
	t.Run("a shared name sorts by id", func(t *testing.T) {
		ids := []string{"ev-1", "ev-2", "ev-3", "ev-4", "ev-5", "ev-6"}
		names := map[string]string{}
		var verdicts []consistencyVerdict
		for _, id := range ids {
			names[id] = "Same"
			verdicts = append(verdicts, passedBy(id))
		}
		results := []evalplatform.RunResultResponse{consistencyRow("ex-1", verdicts...), consistencyRow("ex-1", verdicts...)}
		for range 20 {
			got := consistencyByEvaluator(results, names, runShape{trialsPerExample: 2})
			for i, c := range got {
				if c.EvaluatorID != ids[i] {
					t.Fatalf("consistency order = %+v, want ids in order", got)
				}
			}
		}
	})
	t.Run("an empty report writes nothing", func(t *testing.T) {
		var out bytes.Buffer
		writeConsistency(&lineLogger{mu: &sync.Mutex{}, w: &out, resultW: &out}, nil)
		if out.Len() != 0 {
			t.Errorf("wrote %q", out.String())
		}
	})
}

func TestShapeOfRun(t *testing.T) {
	snapshot := func(trials, examples any) map[string]any {
		return map[string]any{"experiment": map[string]any{"runs_per_example": trials}, "dataset": map[string]any{"example_count": examples}}
	}
	cases := []struct {
		name string
		run  *evalplatform.RunResponse
		want runShape
	}{
		{"recorded", &evalplatform.RunResponse{ConfigSnapshot: snapshot(float64(3), float64(16)), ProgressTotal: 48}, runShape{3, 16}},
		{"json numbers", &evalplatform.RunResponse{ConfigSnapshot: snapshot(json.Number("2"), json.Number("5"))}, runShape{2, 5}},
		{"examples from progress", &evalplatform.RunResponse{ConfigSnapshot: snapshot(float64(3), nil), ProgressTotal: 12}, runShape{3, 4}},
		{"uneven progress", &evalplatform.RunResponse{ConfigSnapshot: snapshot(float64(3), nil), ProgressTotal: 13}, runShape{3, 0}},
		{"no snapshot", &evalplatform.RunResponse{ProgressTotal: 12}, runShape{}},
		{"not a count", &evalplatform.RunResponse{ConfigSnapshot: snapshot(2.5, "16")}, runShape{}},
		{"no run", nil, runShape{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shapeOfRun(tc.run); got != tc.want {
				t.Errorf("shape = %+v, want %+v", got, tc.want)
			}
		})
	}
}
