// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestDecisionFieldsReachTheAPI(t *testing.T) {
	stats := `{"dec":{"scored":4,"escalatedPercent":25,"meanConfidence":0.9,"judgeUsage":{"decision":{"inputTokens":10,"outputTokens":0}}}}`
	run := runResponseFromRow(&store.EvalRun{ID: "r", EvaluatorStats: &stats}, nil)
	st := run.EvaluatorStats["dec"]
	if st.Scored != 4 || st.EscalatedPercent == nil || *st.EscalatedPercent != 25 || st.JudgeUsage.Decision == nil {
		t.Fatalf("evaluatorStats = %+v", run.EvaluatorStats)
	}
	bad := "{not json"
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if got := runResponseFromRow(&store.EvalRun{ID: "r", EvaluatorStats: &bad}, logger); got.EvaluatorStats != nil {
		t.Fatalf("unreadable stats should be dropped, got %+v", got.EvaluatorStats)
	}
	if strings.Count(buf.String(), "unreadable evaluator stats") != 1 || !strings.Contains(buf.String(), "runID=r") {
		t.Fatalf("log = %q, want one WARN naming the run", buf.String())
	}

	details := `{"decidedBy":"decision","answer":{"choice":"yes","confidence":0.8},"escalated":false}`
	res := runResultResponseFromRow(&store.EvalRunResultWithScores{
		EvalRunResult: &store.EvalRunResult{ID: "res"},
		Scores: []*store.EvalRunScore{
			{ID: "s1", EvaluatorID: "dec", Details: &details},
			{ID: "s2", EvaluatorID: "old"},
			{ID: "s3", EvaluatorID: "bad", Details: &bad},
		},
	}, nil)
	if d := res.Scores[0].Details; d == nil || d.DecidedBy != "decision" || d.Answer == nil || d.Answer.Choice != "yes" {
		t.Fatalf("details = %+v", res.Scores[0].Details)
	}
	if res.Scores[1].Details != nil || res.Scores[2].Details != nil {
		t.Fatalf("rows without readable details must read as nil: %+v %+v", res.Scores[1].Details, res.Scores[2].Details)
	}
}

func TestEvaluatorServiceLogsUnreadableSettings(t *testing.T) {
	var buf strings.Builder
	repo := store.NewEvalEvaluatorRepo(store.TestDB(t))
	svc := NewEvaluatorService(repo, slog.New(slog.NewTextHandler(&buf, nil)))
	row := &store.EvalEvaluator{Name: "broken", Type: evaluatorTypeDecision, Model: new("jev"),
		DecisionConfig: new("{bad"), CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.Get(t.Context(), row.ID); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := svc.List(t.Context()); err != nil {
		t.Fatalf("list: %v", err)
	}
	if n := strings.Count(buf.String(), "unreadable decision settings"); n != 2 {
		t.Fatalf("log has %d unreadable-settings WARNs, want one each from Get and List:\n%s", n, buf.String())
	}
}

func TestEvaluatorResponseLogsUnreadableSettings(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	resp := evaluatorResponseFromRow(&store.EvalEvaluator{
		ID: "ev1", Name: "answers", Type: evaluatorTypeDecision,
		ChoiceScores: new("{bad"), DecisionConfig: new("{bad"),
	}, 0, logger)
	if resp.Name != "answers" || resp.ChoiceScores != nil || resp.Question != nil {
		t.Fatalf("resp = %+v, want the evaluator without its unreadable settings", resp)
	}
	out := buf.String()
	if strings.Count(out, "unreadable choice scores") != 1 || strings.Count(out, "unreadable decision settings") != 1 ||
		strings.Count(out, "evaluatorID=ev1") != 2 {
		t.Fatalf("log = %q, want one WARN each naming the evaluator", out)
	}
}
