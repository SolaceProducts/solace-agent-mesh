// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestEvaluatorStatsAccumulator(t *testing.T) {
	confident, unsure := 0.9, 0.3
	acc := &evaluatorStatsAcc{isDecision: true}
	for _, d := range []*ScoreDetails{
		{DecidedBy: decidedByDecision, Answer: &DecisionAnswerDetails{Confidence: &confident},
			Usage: &JudgeUsageDetails{Decision: &JudgeTokenUsage{InputTokens: 10, OutputTokens: 1}}},
		{DecidedBy: decidedByLLMJudge, Escalated: true, EscalationReason: escalationNoConfidence, Answer: &DecisionAnswerDetails{},
			Usage: &JudgeUsageDetails{Decision: &JudgeTokenUsage{InputTokens: 10}, LLMJudge: &JudgeTokenUsage{InputTokens: 50, OutputTokens: 7}}},
		{Escalated: true, EscalationReason: escalationLowConfidence, Answer: &DecisionAnswerDetails{Confidence: &unsure}},
	} {
		acc.add(d, false)
	}
	got := acc.stats()

	if got.Scored != 3 || got.Escalated != 2 || got.WithConfidence != 2 {
		t.Fatalf("counts = scored %d escalated %d withConfidence %d, want 3 2 2", got.Scored, got.Escalated, got.WithConfidence)
	}
	if got.MeanConfidence == nil || math.Abs(*got.MeanConfidence-0.6) > 1e-9 {
		t.Fatalf("meanConfidence = %v, want 0.6 over the two answers with a confidence", optionalFloat(got.MeanConfidence))
	}
	if got.EscalatedPercent == nil || math.Abs(*got.EscalatedPercent-200.0/3) > 1e-9 {
		t.Fatalf("escalatedPercent = %v, want 66.67", optionalFloat(got.EscalatedPercent))
	}
	usage := got.JudgeUsage
	if usage.Decision == nil || usage.Decision.InputTokens != 20 || usage.LLMJudge == nil || usage.LLMJudge.OutputTokens != 7 {
		t.Fatalf("judgeUsage = %+v", usage)
	}
}

func TestEvaluatorStatsCountErroredUsageOnly(t *testing.T) {
	conf := 0.9
	acc := &evaluatorStatsAcc{isDecision: true}
	acc.add(&ScoreDetails{Escalated: true, Answer: &DecisionAnswerDetails{Confidence: &conf},
		Usage: &JudgeUsageDetails{Decision: &JudgeTokenUsage{InputTokens: 10}, LLMJudge: &JudgeTokenUsage{InputTokens: 40}}}, true)
	got := acc.stats()
	if got.Scored != 0 || got.Escalated != 0 || got.WithConfidence != 0 || got.MeanConfidence != nil {
		t.Fatalf("stats = %+v, want an errored score left out of the counts", got)
	}
	if u := got.JudgeUsage; u.Decision == nil || u.Decision.InputTokens != 10 || u.LLMJudge == nil || u.LLMJudge.InputTokens != 40 {
		t.Fatalf("judgeUsage = %+v, want the errored score's billed tokens", u)
	}
}

func TestEvaluatorStatsForLLMJudge(t *testing.T) {
	acc := &evaluatorStatsAcc{}
	acc.add(&ScoreDetails{DecidedBy: decidedByLLMJudge, Usage: &JudgeUsageDetails{LLMJudge: &JudgeTokenUsage{InputTokens: 5}}}, false)
	raw, err := json.Marshal(acc.stats())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"escalated", "withConfidence", "escalatedPercent", "meanConfidence"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("llm_judge stats %s carry %q", raw, key)
		}
	}
}
