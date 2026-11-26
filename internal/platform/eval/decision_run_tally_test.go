// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestDecisionRunTallyPartitionsTheCases(t *testing.T) {
	tally := newDecisionRunTally([]*store.EvalEvaluator{
		{Type: evaluatorTypeDecision}, {Type: evaluatorTypeLLMJudge}, {Type: evaluatorTypeDecision},
	})
	for _, out := range []ScoreOutput{
		{Details: &ScoreDetails{DecidedBy: decidedByDecision}},
		{Details: &ScoreDetails{DecidedBy: decidedByDecision}},
		{Details: &ScoreDetails{DecidedBy: decidedByLLMJudge, Escalated: true, EscalationReason: escalationLowConfidence}},
		{Details: &ScoreDetails{DecidedBy: decidedByLLMJudge, Escalated: true, EscalationReason: escalationProviderError}},
		{Errored: true, Details: &ScoreDetails{ProviderError: string(decision.FailureUnauthorized)}},
		{Errored: true, Details: &ScoreDetails{DecidedBy: decidedByDecision}},
		{},
		{Details: &ScoreDetails{DecidedBy: "a judge from the future"}},
	} {
		tally.add(out)
	}

	got := tally.snapshot()
	if got.Evaluators != 2 {
		t.Errorf("evaluators = %d, want the 2 decision-model evaluators of 3", got.Evaluators)
	}
	if got.Decided != 2 || got.Escalated != 2 || got.Errored != 4 {
		t.Errorf("decided/escalated/errored = %d/%d/%d, want 2/2/4", got.Decided, got.Escalated, got.Errored)
	}
	if got.EscalatedBy[escalationLowConfidence] != 1 || got.EscalatedBy[escalationProviderError] != 1 {
		t.Errorf("escalated by = %v, want one low_confidence and one provider_error", got.EscalatedBy)
	}

	tally.add(ScoreOutput{Details: &ScoreDetails{DecidedBy: decidedByLLMJudge, Escalated: true, EscalationReason: escalationLowConfidence}})
	if got.EscalatedBy[escalationLowConfidence] != 1 {
		t.Error("a snapshot shares its reason counts with the live tally")
	}
}

func TestNilDecisionRunTallyIsUnknown(t *testing.T) {
	var tally *decisionRunTally
	tally.add(ScoreOutput{Details: &ScoreDetails{DecidedBy: decidedByDecision}})
	if got := tally.snapshot(); got != nil {
		t.Errorf("snapshot = %+v, want nil", got)
	}
}

func TestDecisionRunTallyIsSafeForConcurrentUse(t *testing.T) {
	const scorers, perScorer = 8, 50
	tally := newDecisionRunTally([]*store.EvalEvaluator{{Type: evaluatorTypeDecision}})
	var wg sync.WaitGroup
	for range scorers {
		wg.Go(func() {
			for range perScorer {
				tally.add(ScoreOutput{Details: &ScoreDetails{DecidedBy: decidedByLLMJudge, Escalated: true, EscalationReason: escalationNoConfidence}})
				_ = tally.snapshot()
			}
		})
	}
	wg.Wait()
	if got := tally.snapshot(); got.Escalated != scorers*perScorer || got.EscalatedBy[escalationNoConfidence] != scorers*perScorer {
		t.Errorf("snapshot = %+v, want %d escalations", got, scorers*perScorer)
	}
}

func TestDecisionVocabulariesMatchTheCatalog(t *testing.T) {
	types := []string{evaluatorTypeLLMJudge, evaluatorTypeDecision}
	if !slices.Equal(types, taxonomy.EvaluatorTypes) {
		t.Errorf("evaluator types %v, catalog %v", types, taxonomy.EvaluatorTypes)
	}
	questions := []string{string(decision.TypeBoolean), string(decision.TypeChoice), string(decision.TypeScore)}
	if !slices.Equal(questions, taxonomy.DecisionQuestionTypes) {
		t.Errorf("question types %v, catalog %v", questions, taxonomy.DecisionQuestionTypes)
	}
	reasons := []string{escalationLowConfidence, escalationNoConfidence, escalationProviderError, escalationModelLookup}
	if !slices.Equal(reasons, taxonomy.EscalationReasons) {
		t.Errorf("escalation reasons %v, catalog %v", reasons, taxonomy.EscalationReasons)
	}
	kinds := []string{store.ModelTypeChat, store.ModelTypeDecision}
	if !slices.Equal(kinds, taxonomy.ModelKinds) {
		t.Errorf("model types %v, catalog model kinds %v", kinds, taxonomy.ModelKinds)
	}
}

func TestEvalRunEvaluatorTypesMatchWhatAnEvaluatorCanBe(t *testing.T) {
	field, _ := reflect.TypeFor[CreateEvaluatorRequest]().FieldByName("Type")
	var creatable []string
	for rule := range strings.SplitSeq(field.Tag.Get("validate"), ",") {
		if values, ok := strings.CutPrefix(rule, "oneof="); ok {
			creatable = strings.Fields(values)
		}
	}
	if len(creatable) == 0 {
		t.Fatalf("CreateEvaluatorRequest.Type has no oneof rule: %q", field.Tag.Get("validate"))
	}
	want := append(slices.Clone(creatable), "heuristic")
	slices.Sort(want)
	got := slices.Clone(taxonomy.EvalRunEvaluatorTypes)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("eval:run evaluator types %v, want the creatable types and heuristic %v", got, want)
	}
}
