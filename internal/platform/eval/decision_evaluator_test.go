// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func decisionFlag(on bool) *features.Registry {
	r := features.NewRegistry()
	r.Register(features.Definition{Key: features.KeyEvalDecisionEvaluator, Default: on})
	return r
}

func fakeModelTypes(types map[string]string) ModelTypeLookup {
	return func(_ context.Context, alias string) (string, bool, error) {
		t, ok := types[alias]
		return t, ok, nil
	}
}

func newDecisionEvaluatorService(t *testing.T) (context.Context, *evalSvcSet) {
	t.Helper()
	ctx, svcs := newEvalSvcs(t)
	svcs.evaluator.SetFeatureRegistry(decisionFlag(true))
	svcs.evaluator.SetModelTypeLookup(fakeModelTypes(map[string]string{
		"jev": store.ModelTypeDecision,
		"gpt": store.ModelTypeChat,
	}))
	return ctx, svcs
}

func createLLMJudge(ctx context.Context, t *testing.T, svcs *evalSvcSet, name string) *EvaluatorResponse {
	t.Helper()
	resp, err := svcs.evaluator.Create(ctx, CreateEvaluatorRequest{
		Name: name, Description: "judge", Model: new("gpt"),
		PromptTemplate: new("Grade {{Response}}"), ChoiceScores: map[string]any{"Yes": 1.0, "No": 0.0},
	}, "u")
	if err != nil {
		t.Fatalf("create llm_judge: %v", err)
	}
	return resp
}

func choiceQuestion() *EvaluatorQuestion {
	return &EvaluatorQuestion{
		Type:         "choice",
		Instructions: "Does the response answer every part of the question?",
		Criteria: []EvaluatorCriterion{
			{Key: "fully", Description: "every part"},
			{Key: "partially", Description: "some parts"},
			{Key: "not_at_all", Description: "none"},
		},
	}
}

func validDecisionRequest() CreateEvaluatorRequest {
	return CreateEvaluatorRequest{
		Name: "answers", Description: "Did the agent answer?", Type: "decision", Model: new("jev"),
		Question:     choiceQuestion(),
		ChoiceScores: map[string]any{"fully": 1.0, "partially": 0.5, "not_at_all": 0.0},
	}
}

func TestDecisionEvaluatorCreate(t *testing.T) {
	t.Run("a valid choice evaluator is stored with its defaults", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		judge := createLLMJudge(ctx, t, svcs, "quality")
		req := validDecisionRequest()
		req.MinConfidence = new(0.8)
		req.EscalateToEvaluatorID = &judge.ID

		resp, err := svcs.evaluator.Create(ctx, req, "u")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if resp.Type != "decision" || resp.Scoring != "chosen" ||
			!slices.Equal(resp.Reads, []string{"prompt", "response", "expected_response"}) {
			t.Fatalf("defaults not applied: type=%q scoring=%q reads=%v", resp.Type, resp.Scoring, resp.Reads)
		}
		if resp.Question == nil || len(resp.Question.Criteria) != 3 || resp.EscalateToEvaluatorID == nil ||
			*resp.EscalateToEvaluatorID != judge.ID || resp.MinConfidence == nil || *resp.MinConfidence != 0.8 {
			t.Fatalf("settings not echoed: %+v", resp)
		}
		got, err := svcs.evaluator.Get(ctx, resp.ID)
		if err != nil || got.Question == nil || got.Question.Criteria[0].Key != "fully" {
			t.Fatalf("get after create = %+v, %v", got, err)
		}
	})

	t.Run("the API refuses decision evaluators while the flag is off", func(t *testing.T) {
		ctx, svcs := newEvalSvcs(t)
		svcs.evaluator.SetFeatureRegistry(decisionFlag(false))
		_, err := svcs.evaluator.Create(ctx, validDecisionRequest(), "u")
		ve := asValidationError(t, err)
		if ve.Field != "type" || !strings.Contains(ve.Message, "SAM_FEATURE_EVAL_DECISION_EVALUATOR") {
			t.Fatalf("got %+v, want a type error naming the flag", ve)
		}
	})

	t.Run("every field error comes back at once", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		_, err := svcs.evaluator.Create(ctx, CreateEvaluatorRequest{
			Name: "bad", Description: "d", Type: "decision",
			PromptTemplate: new("{{Response}}"),
			Scoring:        "median",
			Reads:          []string{"prompt"},
			MinConfidence:  new(0.8),
		}, "u")
		ve := asValidationError(t, err)
		for _, field := range []string{"model", "question", "promptTemplate", "scoring", "reads", "minConfidence"} {
			if len(ve.Details[field]) == 0 {
				t.Errorf("no error for %s; details = %v", field, ve.Details)
			}
		}
	})

	cases := map[string]struct {
		mutate func(*CreateEvaluatorRequest, *evalSvcSet, context.Context, *testing.T)
		field  string
		want   string
	}{
		"choiceScores must cover every option": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.ChoiceScores = map[string]any{"fully": 1.0, "partially": 0.5, "maybe": 0.2}
			},
			field: "choiceScores", want: "not_at_all",
		},
		"a boolean question takes no choiceScores": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.Question = &EvaluatorQuestion{Type: "boolean", Instructions: "Grounded?"}
			},
			field: "choiceScores", want: "only used by choice questions",
		},
		"the question follows the decision-node rules": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.Question = &EvaluatorQuestion{Type: "score", Instructions: "Rate", Criteria: []EvaluatorCriterion{{Key: "low"}}}
				r.ChoiceScores = nil
			},
			field: "question", want: "at least two",
		},
		"a chat model cannot back a decision evaluator": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.Model = new("gpt")
			},
			field: "model", want: "modelType decision",
		},
		"escalation must name an existing evaluator": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.EscalateToEvaluatorID = new("00000000-0000-0000-0000-000000000000")
			},
			field: "escalateToEvaluatorId", want: "no evaluator",
		},
		"escalation never chains to another decision evaluator": {
			mutate: func(r *CreateEvaluatorRequest, svcs *evalSvcSet, ctx context.Context, t *testing.T) {
				other := validDecisionRequest()
				other.Name = "other"
				created, err := svcs.evaluator.Create(ctx, other, "u")
				if err != nil {
					t.Fatalf("create other: %v", err)
				}
				r.EscalateToEvaluatorID = &created.ID
			},
			field: "escalateToEvaluatorId", want: "must name an llm_judge",
		},
		"minConfidence must be above 0": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.MinConfidence = new(0.0)
			},
			field: "minConfidence", want: "greater than 0 and at most 1",
		},
		"minConfidence must be at most 1": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.MinConfidence = new(1.5)
			},
			field: "minConfidence", want: "greater than 0 and at most 1",
		},
		"an unknown read is refused": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.Reads = []string{"response", "screenshot"}
			},
			field: "reads", want: `unknown read "screenshot"`,
		},
		"a read cannot be listed twice": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.Reads = []string{"response", "response"}
			},
			field: "reads", want: "listed more than once",
		},
		"reads are snake_case, so camelCase is an unknown read": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.Reads = []string{"prompt", "response", "expectedResponse"}
			},
			field: "reads", want: `unknown read "expectedResponse"`,
		},
		"a choice score must be from 0 to 1": {
			mutate: func(r *CreateEvaluatorRequest, _ *evalSvcSet, _ context.Context, _ *testing.T) {
				r.ChoiceScores = map[string]any{"fully": 1.5, "partially": 0.5, "not_at_all": 0.0}
			},
			field: "choiceScores", want: "choiceScores.fully must be a number from 0 to 1",
		},
		"a boolean question's minConfidence must be above 0.5": {
			mutate: func(r *CreateEvaluatorRequest, svcs *evalSvcSet, ctx context.Context, t *testing.T) {
				judge := createLLMJudge(ctx, t, svcs, "quality")
				r.Question = &EvaluatorQuestion{Type: "boolean", Instructions: "Is the answer grounded?"}
				r.ChoiceScores = nil
				r.MinConfidence = new(0.5)
				r.EscalateToEvaluatorID = &judge.ID
			},
			field: "minConfidence", want: "above 0.5",
		},
		"escalation takes an evaluator id, not a name": {
			mutate: func(r *CreateEvaluatorRequest, svcs *evalSvcSet, ctx context.Context, t *testing.T) {
				createLLMJudge(ctx, t, svcs, "answer-quality-judge")
				r.EscalateToEvaluatorID = new("answer-quality-judge")
			},
			field: "escalateToEvaluatorId", want: "must be an evaluator id",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, svcs := newDecisionEvaluatorService(t)
			req := validDecisionRequest()
			tc.mutate(&req, svcs, ctx, t)
			_, err := svcs.evaluator.Create(ctx, req, "u")
			ve := asValidationError(t, err)
			msgs := strings.Join(ve.Details[tc.field], " | ")
			if !strings.Contains(msgs, tc.want) {
				t.Fatalf("%s errors = %q, want one containing %q (all: %v)", tc.field, msgs, tc.want, ve.Details)
			}
		})
	}

	t.Run("a boolean question escalates with minConfidence above 0.5", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		judge := createLLMJudge(ctx, t, svcs, "quality")
		req := validDecisionRequest()
		req.Question = &EvaluatorQuestion{Type: "boolean", Instructions: "Is the answer grounded?"}
		req.ChoiceScores = nil
		req.MinConfidence = new(0.6)
		req.EscalateToEvaluatorID = &judge.ID
		resp, err := svcs.evaluator.Create(ctx, req, "u")
		if err != nil {
			t.Fatalf("create with minConfidence 0.6: %v", err)
		}
		if resp.MinConfidence == nil || *resp.MinConfidence != 0.6 {
			t.Fatalf("minConfidence = %v, want 0.6", resp.MinConfidence)
		}
	})

	t.Run("reads take snake_case case fields and are echoed", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		req := validDecisionRequest()
		reads := []string{"prompt", "response", "expected_response"}
		req.Reads = reads
		resp, err := svcs.evaluator.Create(ctx, req, "u")
		if err != nil {
			t.Fatalf("create with snake_case reads: %v", err)
		}
		if !slices.Equal(resp.Reads, reads) {
			t.Fatalf("reads = %v, want %v", resp.Reads, reads)
		}
	})

	t.Run("identity errors are collected with the decision settings' errors", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		req := validDecisionRequest()
		req.Name = ""
		req.Question = &EvaluatorQuestion{Type: "score", Instructions: "Rate", Criteria: []EvaluatorCriterion{{Key: "low"}}}
		req.ChoiceScores = nil
		_, err := svcs.evaluator.Create(ctx, req, "u")
		ve := asValidationError(t, err)
		if !slices.Contains(ve.Details["name"], "name is required") || len(ve.Details["question"]) == 0 {
			t.Fatalf("want both name and question errors, got details %v", ve.Details)
		}
	})

	t.Run("an llm_judge still reports one identity error at a time", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		_, err := svcs.evaluator.Create(ctx, CreateEvaluatorRequest{
			Name: "", Description: "", Model: new("gpt"),
			PromptTemplate: new("Grade {{Response}}"), ChoiceScores: map[string]any{"Yes": 1.0, "No": 0.0},
		}, "u")
		ve := asValidationError(t, err)
		if ve.Field != "name" || ve.Message != "name is required" || len(ve.Details) != 0 {
			t.Fatalf("got %+v, want the single-field name error", ve)
		}
	})

	t.Run("an unregistered model is left for the run to check", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		req := validDecisionRequest()
		req.Model = new("not-yet-applied")
		if _, err := svcs.evaluator.Create(ctx, req, "u"); err != nil {
			t.Fatalf("create with an unknown model: %v", err)
		}
	})
}

func TestLLMJudgeRefusesDecisionSettings(t *testing.T) {
	ctx, svcs := newDecisionEvaluatorService(t)

	_, err := svcs.evaluator.Create(ctx, CreateEvaluatorRequest{
		Name: "j", Description: "d", Model: new("jev"),
		PromptTemplate: new("{{Response}}"), ChoiceScores: map[string]any{"Y": 1.0, "N": 0.0},
	}, "u")
	if ve := asValidationError(t, err); ve.Field != "model" || !strings.Contains(ve.Message, "needs a chat model") {
		t.Fatalf("decision model on llm_judge: %+v", ve)
	}

	_, err = svcs.evaluator.Create(ctx, CreateEvaluatorRequest{
		Name: "j", Description: "d", Model: new("gpt"), Question: choiceQuestion(),
		PromptTemplate: new("{{Response}}"), ChoiceScores: map[string]any{"Y": 1.0, "N": 0.0},
	}, "u")
	if ve := asValidationError(t, err); ve.Field != "question" {
		t.Fatalf("question on llm_judge: %+v", ve)
	}

	judge := createLLMJudge(ctx, t, svcs, "quality")
	_, err = svcs.evaluator.Update(ctx, judge.ID, UpdateEvaluatorRequest{Scoring: new("expected"), ScoringSet: true}, "u")
	if ve := asValidationError(t, err); ve.Field != "scoring" {
		t.Fatalf("scoring on llm_judge update: %+v", ve)
	}
}

func TestLLMJudgeUpdateRefusesEachDecisionField(t *testing.T) {
	cases := map[string]UpdateEvaluatorRequest{
		"question":              {Question: choiceQuestion(), QuestionSet: true},
		"scoring":               {Scoring: new("expected"), ScoringSet: true},
		"reads":                 {Reads: []string{"response"}, ReadsSet: true},
		"minConfidence":         {MinConfidence: new(0.8), MinConfidenceSet: true},
		"escalateToEvaluatorId": {EscalateToEvaluatorID: new("00000000-0000-0000-0000-000000000001"), EscalateToEvaluatorIDSet: true},
	}
	cases["reads (empty)"] = UpdateEvaluatorRequest{Reads: []string{}, ReadsSet: true}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, svcs := newDecisionEvaluatorService(t)
			judge := createLLMJudge(ctx, t, svcs, "quality")
			_, err := svcs.evaluator.Update(ctx, judge.ID, req, "u")
			field, _, _ := strings.Cut(name, " ")
			if ve := asValidationError(t, err); ve.Field != field {
				t.Fatalf("validation error = %+v, want it on %s", ve, field)
			}
		})
	}
}

func TestDecisionEvaluatorUpdate(t *testing.T) {
	t.Run("switching to a boolean question clears choiceScores", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		created, err := svcs.evaluator.Create(ctx, validDecisionRequest(), "u")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		updated, err := svcs.evaluator.Update(ctx, created.ID, UpdateEvaluatorRequest{
			Question:        &EvaluatorQuestion{Type: "boolean", Instructions: "Is the answer grounded?"},
			QuestionSet:     true,
			ChoiceScoresSet: true,
			Scoring:         new("expected"),
			ScoringSet:      true,
		}, "u")
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.Question.Type != "boolean" || updated.ChoiceScores != nil || updated.Scoring != "expected" {
			t.Fatalf("after update: question=%+v choiceScores=%v scoring=%q", updated.Question, updated.ChoiceScores, updated.Scoring)
		}
	})

	t.Run("a question change without matching choiceScores is refused", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		created, err := svcs.evaluator.Create(ctx, validDecisionRequest(), "u")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		_, err = svcs.evaluator.Update(ctx, created.ID, UpdateEvaluatorRequest{
			Question: &EvaluatorQuestion{Type: "choice", Instructions: "Tone?",
				Criteria: []EvaluatorCriterion{{Key: "warm"}, {Key: "cold"}}},
			QuestionSet: true,
		}, "u")
		if ve := asValidationError(t, err); len(ve.Details["choiceScores"]) == 0 {
			t.Fatalf("want a choiceScores error, got %+v", ve)
		}
	})

	t.Run("the question cannot be cleared", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		created, err := svcs.evaluator.Create(ctx, validDecisionRequest(), "u")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		_, err = svcs.evaluator.Update(ctx, created.ID, UpdateEvaluatorRequest{QuestionSet: true}, "u")
		if ve := asValidationError(t, err); ve.Field != "question" {
			t.Fatalf("got %+v", ve)
		}
	})

	t.Run("an existing decision evaluator cannot be edited while the flag is off", func(t *testing.T) {
		ctx, svcs := newDecisionEvaluatorService(t)
		created, err := svcs.evaluator.Create(ctx, validDecisionRequest(), "u")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		svcs.evaluator.SetFeatureRegistry(decisionFlag(false))
		_, err = svcs.evaluator.Update(ctx, created.ID, UpdateEvaluatorRequest{Description: new("x"), DescriptionSet: true}, "u")
		if ve := asValidationError(t, err); ve.Field != "type" {
			t.Fatalf("got %+v", ve)
		}
	})
}

func TestEscalationTargetCannotBeDeleted(t *testing.T) {
	ctx, svcs := newDecisionEvaluatorService(t)
	judge := createLLMJudge(ctx, t, svcs, "quality")
	req := validDecisionRequest()
	req.EscalateToEvaluatorID = &judge.ID
	dec, err := svcs.evaluator.Create(ctx, req, "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	err = svcs.evaluator.Delete(ctx, judge.ID)
	br, ok := errors.AsType[*BusinessRuleViolationError](err)
	if !ok || br.Code != "EVALUATOR_IS_ESCALATION_TARGET" || !strings.Contains(br.Message, `"answers"`) {
		t.Fatalf("delete escalation target = %v, want EVALUATOR_IS_ESCALATION_TARGET naming the referrer", err)
	}

	if _, err := svcs.evaluator.Update(ctx, dec.ID, UpdateEvaluatorRequest{EscalateToEvaluatorIDSet: true}, "u"); err != nil {
		t.Fatalf("clear escalation: %v", err)
	}
	if err := svcs.evaluator.Delete(ctx, judge.ID); err != nil {
		t.Fatalf("delete after clearing the reference: %v", err)
	}
}

func TestEscalationTargetIDIsStoredCanonical(t *testing.T) {
	ctx, svcs := newDecisionEvaluatorService(t)
	judge := createLLMJudge(ctx, t, svcs, "quality")

	req := validDecisionRequest()
	req.EscalateToEvaluatorID = new("{" + strings.ToUpper(judge.ID) + "}")
	dec, err := svcs.evaluator.Create(ctx, req, "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := derefString(dec.EscalateToEvaluatorID); got != judge.ID {
		t.Fatalf("stored escalateToEvaluatorId = %q, want the canonical %q", got, judge.ID)
	}

	legacy := &store.EvalEvaluator{
		Name: "legacy", Type: evaluatorTypeDecision, Model: new("jev"),
		DecisionConfig: new(`{"question":{"type":"boolean","instructions":"x"},"escalate_to_evaluator_id":"` + strings.ToUpper(judge.ID) + `"}`),
		CreatedBy:      "u", UpdatedBy: "u",
	}
	if err = svcs.evaluators.Create(ctx, legacy); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := svcs.evaluator.Get(ctx, legacy.ID)
	if err != nil || derefString(got.EscalateToEvaluatorID) != judge.ID {
		t.Fatalf("legacy escalateToEvaluatorId reads %q (err %v), want the canonical %q", derefString(got.EscalateToEvaluatorID), err, judge.ID)
	}
	if _, err := svcs.evaluator.Update(ctx, dec.ID, UpdateEvaluatorRequest{EscalateToEvaluatorIDSet: true}, "u"); err != nil {
		t.Fatalf("clear escalation: %v", err)
	}
	br, ok := errors.AsType[*BusinessRuleViolationError](svcs.evaluator.Delete(ctx, judge.ID))
	if !ok || !strings.Contains(br.Message, `"legacy"`) {
		t.Fatalf("delete = %v, want the non-canonical referrer to block it", br)
	}
}

func TestDecisionUpdateCollectsEveryFieldError(t *testing.T) {
	ctx, svcs := newDecisionEvaluatorService(t)
	dec, err := svcs.evaluator.Create(ctx, validDecisionRequest(), "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = svcs.evaluator.Update(ctx, dec.ID, UpdateEvaluatorRequest{
		Name: new("  "), NameSet: true,
		Question: &EvaluatorQuestion{Type: "choice"}, QuestionSet: true,
	}, "u")
	ve := asValidationError(t, err)
	if len(ve.Details["name"]) == 0 || len(ve.Details["question"]) == 0 {
		t.Fatalf("validation details = %v, want both name and question errors", ve.Details)
	}
}

func TestEscalationTargetDeleteFailsSafeOnUnreadableSettings(t *testing.T) {
	ctx, svcs := newDecisionEvaluatorService(t)
	judge := createLLMJudge(ctx, t, svcs, "quality")
	broken := &store.EvalEvaluator{
		Name: "broken", Type: evaluatorTypeDecision, Model: new("jev"),
		DecisionConfig: new("{bad"), CreatedBy: "u", UpdatedBy: "u",
	}
	if err := svcs.evaluators.Create(ctx, broken); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := svcs.evaluator.Delete(ctx, judge.ID)
	br, ok := errors.AsType[*BusinessRuleViolationError](err)
	if !ok || br.Code != "EVALUATOR_IS_ESCALATION_TARGET" || !strings.Contains(br.Message, `"broken" (unreadable settings)`) {
		t.Fatalf("delete = %v, want EVALUATOR_IS_ESCALATION_TARGET naming the unreadable evaluator", err)
	}
}
