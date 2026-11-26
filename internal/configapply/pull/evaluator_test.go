// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func TestNameEscalationTarget(t *testing.T) {
	nameByID := map[string]string{"judge-id": "quality-judge"}

	spec := map[string]any{"escalateToEvaluatorId": "judge-id"}
	if err := nameEscalationTarget(spec, "answers", nameByID); err != nil || spec["escalateToEvaluatorId"] != "quality-judge" {
		t.Fatalf("spec = %v, err = %v; want the id rewritten to the evaluator name", spec, err)
	}

	untouched := map[string]any{"model": "gpt"}
	if err := nameEscalationTarget(untouched, "judge", nameByID); err != nil || len(untouched) != 1 {
		t.Fatalf("an evaluator without escalation must be left alone: %v, %v", untouched, err)
	}

	if err := nameEscalationTarget(map[string]any{"escalateToEvaluatorId": "gone"}, "answers", nameByID); err == nil {
		t.Fatal("an unknown escalation id must be an error, not a silently dangling reference")
	}
}

func TestEvaluatorSpecPullsDecisionSettings(t *testing.T) {
	judge := wire.EvaluatorResponse{ID: "judge-id", Name: "quality-judge", EvaluatorType: "llm_judge"}
	dec := wire.EvaluatorResponse{
		ID: "dec-id", Name: "answers", EvaluatorType: "decision", Model: new("eval-judge"),
		ChoiceScores: map[string]float64{"fully": 1.0, "not_at_all": 0.0},
		Question: &apiv2.EvaluatorQuestion{Type: "choice", Instructions: "Answered?",
			Criteria: []apiv2.EvaluatorCriterion{{Key: "fully"}, {Key: "not_at_all"}}},
		Scoring: new("chosen"), Reads: []string{"prompt", "response"},
		MinConfidence: new(0.8), EscalateToEvaluatorID: new("judge-id"),
	}
	spec, err := evaluatorSpec(dec, evaluatorNamesByID([]wire.EvaluatorResponse{judge, dec}))
	if err != nil {
		t.Fatalf("evaluatorSpec: %v", err)
	}
	if spec["escalateToEvaluatorId"] != "quality-judge" {
		t.Errorf("escalateToEvaluatorId = %v, want the judge's name", spec["escalateToEvaluatorId"])
	}
	if spec["minConfidence"] != 0.8 || spec["scoring"] != "chosen" || spec["type"] != "decision" {
		t.Errorf("spec = %v, want type, scoring and minConfidence kept", spec)
	}
	if reads, ok := spec["reads"].([]string); !ok || !slices.Equal(reads, []string{"prompt", "response"}) {
		t.Errorf("reads = %#v, want [prompt response]", spec["reads"])
	}
	question, ok := spec["question"].(map[string]any)
	if !ok || question["instructions"] != "Answered?" {
		t.Errorf("question = %v, want the question kept", spec["question"])
	}
}
