// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func evaluatorFixture(f func(map[string]any)) map[string]any {
	m := map[string]any{
		"type":           "llm_judge",
		"model":          "openai/gpt-4o-mini",
		"promptTemplate": "Score: {{ Response }}",
		"choiceScores":   map[string]any{"yes": 1.0, "no": 0.0},
	}
	if f != nil {
		f(m)
	}
	return m
}

func resolvedEvaluator(name string, spec map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:        "evaluator",
		Name:        name,
		SourceFile:  "evaluators/" + name + ".yaml",
		Spec:        spec,
		Description: "fixture",
	}
}

func TestPlanEvaluators_CreateAndDelete(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("strict-judge", evaluatorFixture(nil))}
	actual := []wire.EvaluatorResponse{{ID: "id-old", Name: "obsolete", EvaluatorType: "llm_judge"}}

	plan, err := planEvaluators(desired, actual)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "strict-judge" {
		t.Errorf("Creates = %+v", plan.Creates)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].ID != "id-old" {
		t.Errorf("Deletes = %+v", plan.Deletes)
	}
}

func TestPlanEvaluators_FiltersDefaults(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("strict-judge", evaluatorFixture(nil))}
	actual := []wire.EvaluatorResponse{
		{ID: "id-default", Name: "factuality", EvaluatorType: "llm_judge", IsDefault: true},
	}

	plan, err := planEvaluators(desired, actual)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Deletes) != 0 {
		t.Errorf("expected no deletes for default evaluators, got %+v", plan.Deletes)
	}
}

func TestPlanEvaluators_RejectsTypeMutation(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("judge", evaluatorFixture(func(m map[string]any) {
		m["type"] = "heuristic"
	}))}
	actual := []wire.EvaluatorResponse{{ID: "id-1", Name: "judge", EvaluatorType: "llm_judge"}}

	if _, err := planEvaluators(desired, actual); err == nil {
		t.Fatalf("expected error on type mutation, got nil")
	}
}

func TestPlanEvaluators_UnchangedWhenIdentical(t *testing.T) {
	desc := "fixture"
	model := "openai/gpt-4o-mini"
	prompt := "Score: {{ Response }}"
	heuristicType := ""
	desired := []resolve.ResolvedResource{resolvedEvaluator("strict-judge", evaluatorFixture(nil))}
	actual := []wire.EvaluatorResponse{{
		ID:             "id-1",
		Name:           "strict-judge",
		Description:    &desc,
		EvaluatorType:  "llm_judge",
		HeuristicType:  &heuristicType,
		Model:          &model,
		PromptTemplate: &prompt,
		ChoiceScores:   map[string]float64{"yes": 1.0, "no": 0.0},
	}}

	plan, err := planEvaluators(desired, actual)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "id-1" {
		t.Errorf("expected single unchanged, got %+v", plan)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("expected no updates, got %+v", plan.Updates)
	}
}

func TestPlanEvaluators_UnchangedWhenPlatformCanonicalizedTemplate(t *testing.T) {
	desc := "fixture"
	model := "openai/gpt-4o-mini"
	stored := "Score: {{Response}}"
	desired := []resolve.ResolvedResource{resolvedEvaluator("strict-judge", evaluatorFixture(nil))}
	actual := []wire.EvaluatorResponse{{
		ID:             "id-1",
		Name:           "strict-judge",
		Description:    &desc,
		EvaluatorType:  "llm_judge",
		Model:          &model,
		PromptTemplate: &stored,
		ChoiceScores:   map[string]float64{"yes": 1.0, "no": 0.0},
	}}

	plan, err := planEvaluators(desired, actual)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
		t.Errorf("expected single unchanged and no updates, got %+v", plan)
	}
}

func TestPlanEvaluators_UpdatePatchesWhatChanged(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("judge", evaluatorFixture(func(m map[string]any) {
		m["promptTemplate"] = "drift: {{ Response }}"
	}))}
	prevPrompt := "Score: {{ Response }}"
	model := "openai/gpt-4o-mini"
	desc := "fixture"
	actual := []wire.EvaluatorResponse{{
		ID:             "id-1",
		Name:           "judge",
		Description:    &desc,
		EvaluatorType:  "llm_judge",
		Model:          &model,
		PromptTemplate: &prevPrompt,
		ChoiceScores:   map[string]float64{"yes": 1.0, "no": 0.0},
	}}

	plan, err := planEvaluators(desired, actual)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("expected single update, got %+v", plan)
	}
	body, err := json.Marshal(plan.Updates[0].Patch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"promptTemplate":"drift: {{ Response }}"}`; string(body) != want {
		t.Errorf("PATCH body = %s, want %s", body, want)
	}
}

func TestPlanEvaluators_DecisionPatchClearsWhatTheManifestDrops(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("answers", decisionEvaluatorFixture(func(m map[string]any) {
		delete(m, "minConfidence")
		delete(m, "escalateToEvaluatorId")
	}))}
	plan, err := planEvaluators(desired, liveDecisionActual())
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("want one update; got %+v", plan)
	}
	body, err := json.Marshal(plan.Updates[0].Patch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"minConfidence":null,"escalateToEvaluatorId":null}`; string(body) != want {
		t.Errorf("PATCH body = %s, want %s", body, want)
	}
}

func TestPlanEvaluators_DecisionPatchResetsDefaultsOnlyWhenStoredDiffer(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("answers", decisionEvaluatorFixture(func(m map[string]any) {
		m["minConfidence"] = 0.9
	}))}
	actual := liveDecisionActual()
	actual[0].Scoring = new("expected")
	plan, err := planEvaluators(desired, actual)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	body, _ := json.Marshal(plan.Updates[0].Patch)
	if want := `{"scoring":"chosen","minConfidence":0.9}`; string(body) != want {
		t.Errorf("PATCH body = %s, want %s", body, want)
	}
}

func TestPlanEvaluators_DuplicateNameInDesired(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedEvaluator("dup", evaluatorFixture(nil)),
		resolvedEvaluator("dup", evaluatorFixture(nil)),
	}
	if _, err := planEvaluators(desired, nil); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}
