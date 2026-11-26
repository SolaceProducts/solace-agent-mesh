// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func decisionEvaluatorFixture(f func(map[string]any)) map[string]any {
	m := map[string]any{
		"type":  "decision",
		"model": "eval-judge",
		"question": map[string]any{
			"type":         "choice",
			"instructions": "Does the response answer the question?",
			"criteria": []any{
				map[string]any{"key": "fully", "description": "every part"},
				map[string]any{"key": "not_at_all"},
			},
		},
		"choiceScores":          map[string]any{"fully": 1.0, "not_at_all": 0.0},
		"minConfidence":         0.8,
		"escalateToEvaluatorId": "quality-judge",
	}
	if f != nil {
		f(m)
	}
	return m
}

func liveDecisionEvaluator() wire.EvaluatorResponse {
	desc := "fixture"
	return wire.EvaluatorResponse{
		ID: "dec-id", Name: "answers", Description: &desc, EvaluatorType: "decision", Model: new("eval-judge"),
		ChoiceScores: map[string]float64{"fully": 1.0, "not_at_all": 0.0},
		Question: &apiv2.EvaluatorQuestion{Type: "choice", Instructions: "Does the response answer the question?",
			Criteria: []apiv2.EvaluatorCriterion{{Key: "fully", Description: new("every part")}, {Key: "not_at_all"}}},
		Scoring:               new("chosen"),
		Reads:                 []string{"prompt", "response", "expected_response"},
		MinConfidence:         new(0.8),
		EscalateToEvaluatorID: new("judge-id"),
	}
}

func TestPlanEvaluators_DecisionEvaluatorMatchesItsLiveState(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("answers", decisionEvaluatorFixture(nil))}
	actual := []wire.EvaluatorResponse{
		liveDecisionEvaluator(),
		{ID: "judge-id", Name: "quality-judge", EvaluatorType: "llm_judge", IsDefault: true},
	}
	plan, err := planEvaluators(desired, actual)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 || len(plan.Creates) != 0 {
		t.Fatalf("want one unchanged evaluator (defaults and the escalation name must not diff); got %+v", plan)
	}
}

func TestPlanEvaluators_DecisionEscalationTargetMustExist(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("answers", decisionEvaluatorFixture(nil))}
	_, err := planEvaluators(desired, nil)
	if err == nil || !strings.Contains(err.Error(), `"quality-judge"`) {
		t.Fatalf("planEvaluators = %v, want an error naming the missing target", err)
	}
}

func TestPlanEvaluators_DecisionEscalationTargetMustBeAnLLMJudge(t *testing.T) {
	t.Run("a decision evaluator in the manifest", func(t *testing.T) {
		desired := []resolve.ResolvedResource{
			resolvedEvaluator("answers", decisionEvaluatorFixture(func(m map[string]any) { m["escalateToEvaluatorId"] = "triage" })),
			resolvedEvaluator("triage", decisionEvaluatorFixture(nil)),
			resolvedEvaluator("quality-judge", evaluatorFixture(nil)),
		}
		_, err := planEvaluators(desired, nil)
		if err == nil || !strings.Contains(err.Error(), `"triage" must name an llm_judge evaluator; it is a decision evaluator`) {
			t.Fatalf("planEvaluators = %v, want the decision target refused", err)
		}
	})
	t.Run("a live heuristic default", func(t *testing.T) {
		desired := []resolve.ResolvedResource{
			resolvedEvaluator("answers", decisionEvaluatorFixture(func(m map[string]any) { m["escalateToEvaluatorId"] = "Valid JSON" })),
		}
		actual := []wire.EvaluatorResponse{{ID: "json-id", Name: "Valid JSON", EvaluatorType: "heuristic", IsDefault: true}}
		_, err := planEvaluators(desired, actual)
		if err == nil || !strings.Contains(err.Error(), `"Valid JSON" must name an llm_judge evaluator; it is a heuristic evaluator`) {
			t.Fatalf("planEvaluators = %v, want the heuristic target refused", err)
		}
	})
}

func TestPlanEvaluators_DecisionNullMinConfidenceAndTargetAreAbsent(t *testing.T) {
	spec := decisionEvaluatorFixture(func(m map[string]any) {
		m["minConfidence"] = nil
		m["escalateToEvaluatorId"] = nil
	})
	create, err := translateEvaluator(resolvedEvaluator("answers", spec))
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if create.MinConfidence != nil || create.EscalateToEvaluatorID != nil {
		t.Fatalf("minConfidence = %v, escalateToEvaluatorId = %v; want both unset", create.MinConfidence, create.EscalateToEvaluatorID)
	}
	if _, err := planEvaluators([]resolve.ResolvedResource{resolvedEvaluator("answers", spec)}, nil); err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
}

func TestApplyEvaluators_DecisionEscalatesToAJudgeCreatedInTheSameApply(t *testing.T) {
	var created []wire.CreateEvaluatorRequest
	ids := map[string]string{"answers": "dec-id", "quality-judge": "judge-id"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+platform.APIV2Prefix+evalEvaluatorsResource.path, func(w http.ResponseWriter, r *http.Request) {
		var req wire.CreateEvaluatorRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		created = append(created, req)
		writeV2Single(w, http.StatusCreated, wire.EvaluatorResponse{ID: ids[req.Name], Name: req.Name, EvaluatorType: req.EvaluatorType})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+evalEvaluatorsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.EvaluatorResponse{})
	})
	c := newV2PlatformClient(t, mux)

	plan, err := planEvaluators([]resolve.ResolvedResource{
		resolvedEvaluator("answers", decisionEvaluatorFixture(nil)),
		resolvedEvaluator("quality-judge", evaluatorFixture(nil)),
	}, nil)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	out := applyEvaluators(t.Context(), c, plan, ApplyOptions{})
	if len(out.Failures) != 0 {
		t.Fatalf("apply failures: %+v", out.Failures)
	}
	if len(created) != 2 || created[0].Name != "quality-judge" || created[1].Name != "answers" {
		t.Fatalf("create order = %+v, want quality-judge then answers", created)
	}
	if got := created[1].EscalateToEvaluatorID; got == nil || *got != "judge-id" {
		t.Fatalf("decision evaluator created with escalateToEvaluatorId %v, want the judge's id", got)
	}
}

func TestPlanEvaluators_BooleanQuestionPatchClearsChoiceScores(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("answers", decisionEvaluatorFixture(func(m map[string]any) {
		m["question"] = map[string]any{"type": "boolean", "instructions": "Grounded?"}
		delete(m, "choiceScores")
	}))}
	plan, err := planEvaluators(desired, liveDecisionActual())
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("want one update; got %+v", plan)
	}
	body, _ := json.Marshal(plan.Updates[0].Patch)
	var sent map[string]any
	_ = json.Unmarshal(body, &sent)
	if v, present := sent["choiceScores"]; !present || v != nil {
		t.Fatalf("choiceScores should be an explicit null in the PATCH: %s", body)
	}
}

func TestPlanEvaluators_DecisionBlockScalarQuestionIsNotADiff(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("answers", decisionEvaluatorFixture(func(m map[string]any) {
		m["question"] = map[string]any{
			"type":         "choice",
			"instructions": "Does the response answer the question?\n",
			"criteria": []any{
				map[string]any{"key": "fully", "description": "every part\n"},
				map[string]any{"key": "not_at_all"},
			},
		}
		m["reads"] = []any{"prompt ", "response", "expected_response"}
	}))}
	actual := []wire.EvaluatorResponse{
		liveDecisionEvaluator(),
		{ID: "judge-id", Name: "quality-judge", EvaluatorType: "llm_judge", IsDefault: true},
	}
	plan, err := planEvaluators(desired, actual)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Updates) != 0 {
		t.Fatalf("a block-scalar question must match the trimmed live one; got updates %+v", plan.Updates)
	}
}

func TestApplyEvaluators_PruneDeletesReferrerBeforeTarget(t *testing.T) {
	var deleted []string
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE "+platform.APIV2Prefix+evalEvaluatorsResource.path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		deleted = append(deleted, r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
	c := newV2PlatformClient(t, mux)

	plan, err := planEvaluators(nil, []wire.EvaluatorResponse{
		{ID: "judge-id", Name: "a-judge", EvaluatorType: "llm_judge"},
		{ID: "dec-id", Name: "z-decision", EvaluatorType: "decision"},
	})
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	out := applyEvaluators(t.Context(), c, plan, ApplyOptions{Prune: true})
	if len(out.Failures) != 0 {
		t.Fatalf("apply failures: %+v", out.Failures)
	}
	if len(deleted) != 2 || deleted[0] != "dec-id" {
		t.Fatalf("delete order = %v, want the decision evaluator first", deleted)
	}
}

func liveDecisionActual() []wire.EvaluatorResponse {
	return []wire.EvaluatorResponse{
		liveDecisionEvaluator(),
		{ID: "judge-id", Name: "quality-judge", EvaluatorType: "llm_judge", IsDefault: true},
	}
}

func TestPlanEvaluators_DecisionManifestDroppingTypeNeedsTypeDecision(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("answers", decisionEvaluatorFixture(func(m map[string]any) {
		delete(m, "type")
	}))}
	for name, actual := range map[string][]wire.EvaluatorResponse{
		"existing": liveDecisionActual(),
		"new":      {{ID: "judge-id", Name: "quality-judge", EvaluatorType: "llm_judge"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := planEvaluators(desired, actual)
			if err == nil || !strings.Contains(err.Error(), "only for type: decision") || !strings.Contains(err.Error(), `"answers"`) {
				t.Fatalf("planEvaluators = %v, want an error telling the author the settings of \"answers\" are only for type: decision", err)
			}
		})
	}
}

func TestPlanEvaluators_DecisionManifestSpellings(t *testing.T) {
	t.Run("a blank escalation target means no escalation", func(t *testing.T) {
		for _, blank := range []any{nil, "", "  "} {
			spec := decisionEvaluatorFixture(func(m map[string]any) {
				m["escalateToEvaluatorId"] = blank
				delete(m, "minConfidence")
			})
			create, err := translateEvaluator(resolvedEvaluator("answers", spec))
			if err != nil || create.EscalateToEvaluatorID != nil {
				t.Fatalf("escalateToEvaluatorId %#v: create.EscalateToEvaluatorID = %v, err = %v; want none", blank, create.EscalateToEvaluatorID, err)
			}
		}
	})
	t.Run("an unknown type fails at plan", func(t *testing.T) {
		spec := decisionEvaluatorFixture(func(m map[string]any) { m["type"] = "Decision" })
		_, err := planEvaluators([]resolve.ResolvedResource{resolvedEvaluator("answers", spec)}, liveDecisionActual())
		if err == nil || !strings.Contains(err.Error(), `type "Decision" is not`) {
			t.Fatalf("planEvaluators = %v, want an unknown-type error", err)
		}
	})
	t.Run("padded scoring matches the live evaluator", func(t *testing.T) {
		spec := decisionEvaluatorFixture(func(m map[string]any) { m["scoring"] = " chosen " })
		plan, err := planEvaluators([]resolve.ResolvedResource{resolvedEvaluator("answers", spec)}, liveDecisionActual())
		if err != nil || len(plan.Unchanged) != 1 {
			t.Fatalf("plan = %+v, err = %v; want the evaluator unchanged", plan, err)
		}
	})
}

func TestPlanEvaluators_LLMJudgeRefusesDecisionSettings(t *testing.T) {
	fields := map[string]any{
		"question":              map[string]any{"type": "boolean", "instructions": "Correct?"},
		"scoring":               "expected",
		"reads":                 []any{"response"},
		"minConfidence":         0.8,
		"escalateToEvaluatorId": "other-judge",
	}
	live := []wire.EvaluatorResponse{{
		ID: "id-1", Name: "strict-judge", Description: new("fixture"), EvaluatorType: "llm_judge",
		Model: new("openai/gpt-4o-mini"), PromptTemplate: new("Score: {{Response}}"),
		ChoiceScores: map[string]float64{"yes": 1.0, "no": 0.0},
	}, {ID: "id-2", Name: "other-judge", EvaluatorType: "llm_judge"}}
	for field, value := range fields {
		for name, actual := range map[string][]wire.EvaluatorResponse{"existing": live, "new": live[1:]} {
			t.Run(field+"/"+name, func(t *testing.T) {
				spec := evaluatorFixture(func(m map[string]any) { m[field] = value })
				_, err := planEvaluators([]resolve.ResolvedResource{resolvedEvaluator("strict-judge", spec)}, actual)
				if err == nil || !strings.Contains(err.Error(), "only for type: decision") {
					t.Fatalf("planEvaluators = %v, want the decision-only settings refused", err)
				}
			})
		}
	}

	t.Run("an empty reads list is no setting", func(t *testing.T) {
		spec := evaluatorFixture(func(m map[string]any) { m["reads"] = []any{} })
		plan, err := planEvaluators([]resolve.ResolvedResource{resolvedEvaluator("strict-judge", spec)}, live)
		if err != nil || len(plan.Unchanged) != 1 {
			t.Fatalf("plan = %+v, err = %v; want the judge unchanged", plan, err)
		}
	})
}

func TestPlanEvaluators_DecisionNeedsAModel(t *testing.T) {
	for _, model := range []any{nil, "", "  "} {
		spec := decisionEvaluatorFixture(func(m map[string]any) {
			if model == nil {
				delete(m, "model")
			} else {
				m["model"] = model
			}
		})
		_, err := planEvaluators([]resolve.ResolvedResource{resolvedEvaluator("answers", spec)}, liveDecisionActual())
		if err == nil || !strings.Contains(err.Error(), "model is required for type: decision") {
			t.Fatalf("model %#v: planEvaluators = %v, want a missing-model error", model, err)
		}
	}
}

func TestPlanEvaluators_DecisionMinConfidenceNeedsATarget(t *testing.T) {
	spec := decisionEvaluatorFixture(func(m map[string]any) { m["escalateToEvaluatorId"] = "" })
	_, err := planEvaluators([]resolve.ResolvedResource{resolvedEvaluator("answers", spec)}, liveDecisionActual())
	if err == nil || !strings.Contains(err.Error(), "minConfidence needs escalateToEvaluatorId") {
		t.Fatalf("planEvaluators = %v, want minConfidence refused without a target", err)
	}
}

func TestPlanEvaluators_DecisionToLLMJudgeIsATypeChange(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("answers", evaluatorFixture(nil))}
	_, err := planEvaluators(desired, liveDecisionActual())
	if err == nil || !strings.Contains(err.Error(), `type change ("decision" -> "llm_judge")`) {
		t.Fatalf("planEvaluators = %v, want a type change error naming both types", err)
	}
}

func TestPlanEvaluators_LLMJudgeWithTypeOmittedIsUnchanged(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedEvaluator("strict-judge", evaluatorFixture(func(m map[string]any) {
		delete(m, "type")
	}))}
	actual := []wire.EvaluatorResponse{{
		ID: "id-1", Name: "strict-judge", Description: new("fixture"), EvaluatorType: "llm_judge",
		Model: new("openai/gpt-4o-mini"), PromptTemplate: new("Score: {{Response}}"),
		ChoiceScores: map[string]float64{"yes": 1.0, "no": 0.0},
	}}
	plan, err := planEvaluators(desired, actual)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 || len(plan.Creates) != 0 {
		t.Fatalf("want one unchanged evaluator; got %+v", plan)
	}
}

func TestPlanEvaluators_DecisionFieldChangesPlanAnUpdate(t *testing.T) {
	question := func(m map[string]any) map[string]any { return m["question"].(map[string]any) }
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"question instructions", func(m map[string]any) {
			question(m)["instructions"] = "Is the response polite?"
		}},
		{"criterion key", func(m map[string]any) {
			question(m)["criteria"].([]any)[1].(map[string]any)["key"] = "partly"
		}},
		{"choiceScores value", func(m map[string]any) {
			m["choiceScores"] = map[string]any{"fully": 1.0, "not_at_all": 0.25}
		}},
		{"scoring", func(m map[string]any) { m["scoring"] = "expected" }},
		{"reads", func(m map[string]any) { m["reads"] = []any{"prompt", "response"} }},
		{"minConfidence", func(m map[string]any) { m["minConfidence"] = 0.9 }},
		{"escalation target", func(m map[string]any) { m["escalateToEvaluatorId"] = "other-judge" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := append(liveDecisionActual(),
				wire.EvaluatorResponse{ID: "other-id", Name: "other-judge", EvaluatorType: "llm_judge", IsDefault: true})
			desired := []resolve.ResolvedResource{resolvedEvaluator("answers", decisionEvaluatorFixture(tc.mutate))}
			plan, err := planEvaluators(desired, actual)
			if err != nil {
				t.Fatalf("planEvaluators: %v", err)
			}
			if len(plan.Updates) != 1 || plan.Updates[0].ID != "dec-id" || len(plan.Unchanged) != 0 || len(plan.Creates) != 0 {
				t.Fatalf("want exactly one update of answers; got %+v", plan)
			}
		})
	}
}

func TestApplyEvaluators_DecisionUpdateSendsTheJudgesID(t *testing.T) {
	escalatingElsewhere := liveDecisionEvaluator()
	escalatingElsewhere.EscalateToEvaluatorID = new("other-judge-id")
	live := append(liveDecisionActual(), wire.EvaluatorResponse{ID: "other-judge-id", Name: "other-judge", EvaluatorType: "llm_judge"})
	live[0] = escalatingElsewhere
	var gotID string
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+evalEvaluatorsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, live)
	})
	mux.HandleFunc("PATCH "+platform.APIV2Prefix+evalEvaluatorsResource.path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		gotID = r.PathValue("id")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		writeV2Single(w, http.StatusOK, liveDecisionEvaluator())
	})
	c := newV2PlatformClient(t, mux)

	plan, err := planEvaluators([]resolve.ResolvedResource{
		resolvedEvaluator("answers", decisionEvaluatorFixture(nil)),
	}, live)
	if err != nil {
		t.Fatalf("planEvaluators: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("want one update; got %+v", plan)
	}
	out := applyEvaluators(t.Context(), c, plan, ApplyOptions{})
	if len(out.Failures) != 0 || len(out.Updated) != 1 {
		t.Fatalf("apply outcome = %+v", out)
	}
	if gotID != "dec-id" {
		t.Fatalf("PATCH went to %q, want dec-id", gotID)
	}
	if got := gotBody["escalateToEvaluatorId"]; got != "judge-id" {
		t.Fatalf("PATCH escalateToEvaluatorId = %v, want the judge's id judge-id; body %v", got, gotBody)
	}
}

func TestCheckPrunedModelReferences_DecisionEvaluator(t *testing.T) {
	op := evaluatorOp{Name: "answers", Create: wire.CreateEvaluatorRequest{EvaluatorType: "decision", Model: new("eval-judge")}}
	cases := []struct {
		name string
		plan *evaluatorKindPlan
	}{
		{"create", &evaluatorKindPlan{Creates: []evaluatorOp{op}}},
		{"update", &evaluatorKindPlan{Updates: []evaluatorOp{op}}},
		{"unchanged", &evaluatorKindPlan{Unchanged: []evaluatorOp{op}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := &PlanResult{
				Models:     &kindPlan{Kind: "model", Deletes: []modelOp{{Name: "eval-judge", ID: "id-eval-judge"}}},
				Evaluators: tc.plan,
			}
			err := checkPrunedModelReferences(plan)
			want := `model "eval-judge" is deleted by this apply but evaluator "answers" still uses it; point it at another model or keep the model`
			if err == nil || err.Error() != want {
				t.Fatalf("checkPrunedModelReferences = %v, want %q", err, want)
			}
			if w := prunedModelWarnings(plan); len(w) != 1 || !strings.Contains(w[0], `evaluator "answers"`) {
				t.Errorf("prunedModelWarnings = %q", w)
			}
		})
	}

	t.Run("evaluator on a kept model", func(t *testing.T) {
		plan := &PlanResult{
			Models:     &kindPlan{Kind: "model", Deletes: []modelOp{{Name: "old-m", ID: "id-old-m"}}},
			Evaluators: &evaluatorKindPlan{Unchanged: []evaluatorOp{op}},
		}
		if err := checkPrunedModelReferences(plan); err != nil {
			t.Fatalf("checkPrunedModelReferences: %v", err)
		}
	})
}

func TestCheckPrunedEscalationTargets(t *testing.T) {
	escalating := evaluatorOp{Name: "answers", Create: wire.CreateEvaluatorRequest{EvaluatorType: "decision", EscalateToEvaluatorID: new("quality-judge")}}
	judgeDelete := evaluatorOp{Name: "quality-judge", ID: "judge-id", Type: "llm_judge"}
	conflicts := []struct {
		name string
		plan *evaluatorKindPlan
	}{
		{"create", &evaluatorKindPlan{Creates: []evaluatorOp{escalating}, Deletes: []evaluatorOp{judgeDelete}}},
		{"update", &evaluatorKindPlan{Updates: []evaluatorOp{escalating}, Deletes: []evaluatorOp{judgeDelete}}},
		{"unchanged", &evaluatorKindPlan{Unchanged: []evaluatorOp{escalating}, Deletes: []evaluatorOp{judgeDelete}}},
	}
	for _, tc := range conflicts {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPrunedEscalationTargets(tc.plan)
			if err == nil || !strings.Contains(err.Error(), `"answers"`) || !strings.Contains(err.Error(), `"quality-judge"`) {
				t.Fatalf("checkPrunedEscalationTargets = %v, want an error naming answers and quality-judge", err)
			}
		})
	}

	t.Run("planned from a manifest that drops the judge", func(t *testing.T) {
		live := liveDecisionActual()
		live[1].IsDefault = false
		plan, err := planEvaluators([]resolve.ResolvedResource{resolvedEvaluator("answers", decisionEvaluatorFixture(nil))}, live)
		if err != nil {
			t.Fatalf("planEvaluators: %v", err)
		}
		if err := checkPrunedEscalationTargets(plan); err == nil || !strings.Contains(err.Error(), `"quality-judge"`) {
			t.Fatalf("checkPrunedEscalationTargets = %v, want an error naming quality-judge", err)
		}
		if w := prunedEscalationWarnings(plan); len(w) != 1 || !strings.Contains(w[0], `"quality-judge"`) || !strings.Contains(w[0], "apply --prune fails") {
			t.Fatalf("prunedEscalationWarnings = %q, want one warning naming quality-judge", w)
		}
	})

	noConflicts := []struct {
		name string
		plan *evaluatorKindPlan
	}{
		{"nil plan", nil},
		{"no deletes", &evaluatorKindPlan{Creates: []evaluatorOp{escalating}}},
		{"the deleted evaluator is not a target", &evaluatorKindPlan{
			Unchanged: []evaluatorOp{escalating},
			Deletes:   []evaluatorOp{{Name: "other-judge", ID: "other-id", Type: "llm_judge"}},
		}},
		{"the referrer is deleted too", &evaluatorKindPlan{
			Deletes: []evaluatorOp{judgeDelete, {Name: "answers", ID: "dec-id", Type: "decision"}},
		}},
	}
	for _, tc := range noConflicts {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkPrunedEscalationTargets(tc.plan); err != nil {
				t.Fatalf("checkPrunedEscalationTargets: %v", err)
			}
		})
	}
}

func TestTranslateEvaluator_LLMJudgeNeedsAModel(t *testing.T) {
	for name, spec := range map[string]map[string]any{
		"no model":    evaluatorFixture(func(m map[string]any) { delete(m, "model") }),
		"blank model": evaluatorFixture(func(m map[string]any) { m["model"] = " " }),
		"no type":     evaluatorFixture(func(m map[string]any) { delete(m, "type"); delete(m, "model") }),
	} {
		if _, err := translateEvaluator(resolvedEvaluator("judge", spec)); err == nil || !strings.Contains(err.Error(), "model is required") {
			t.Errorf("%s: translate error = %v, want model is required", name, err)
		}
	}
}
