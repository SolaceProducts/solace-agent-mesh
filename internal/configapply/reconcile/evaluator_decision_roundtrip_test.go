// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const decisionManifestYAML = `
type: decision
model: eval-judge
question:
  type: choice
  instructions: |
    Does the response answer the question?
  criteria:
    - key: fully
      description: every part
    - key: not_at_all
choiceScores:
  fully: 1.0
  not_at_all: 0.0
minConfidence: 0.8
escalateToEvaluatorId: quality-judge
`

func decisionManifestSpec(t *testing.T, mutate func(map[string]any)) map[string]any {
	t.Helper()
	var spec map[string]any
	if err := yaml.Unmarshal([]byte(decisionManifestYAML), &spec); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if mutate != nil {
		mutate(spec)
	}
	return spec
}

func newEvaluatorPlatform(t *testing.T) *Client {
	t.Helper()
	svc := evalplatform.NewEvaluatorService(store.NewEvalEvaluatorRepo(store.TestDB(t)), nil)
	registry := features.NewRegistry()
	registry.Register(features.Definition{Key: features.KeyEvalDecisionEvaluator, Default: true})
	svc.SetFeatureRegistry(registry)
	svc.SetModelTypeLookup(func(_ context.Context, alias string) (string, bool, error) {
		types := map[string]string{"eval-judge": store.ModelTypeDecision, "openai/gpt-4o-mini": store.ModelTypeChat}
		typ, ok := types[alias]
		return typ, ok, nil
	})

	reply := func(w http.ResponseWriter, status int, resp *evalplatform.EvaluatorResponse, err error) {
		if err != nil {
			writeV2Error(w, http.StatusUnprocessableEntity, err.Error(), nil)
			return
		}
		writeV2Single(w, status, evaluatorAsV2(t, resp))
	}
	path := platform.APIV2Prefix + evalEvaluatorsResource.path
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		rows, err := svc.List(r.Context())
		if err != nil {
			writeV2Error(w, http.StatusInternalServerError, err.Error(), nil)
			return
		}
		out := make([]wire.EvaluatorResponse, len(rows))
		for i := range rows {
			out[i] = evaluatorAsV2(t, &rows[i])
		}
		writeV2Page(w, out)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		var body wire.CreateEvaluatorRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeV2Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		req := evalplatform.CreateEvaluatorRequest{
			Name: body.Name, Description: body.Description, Type: body.EvaluatorType, Model: body.Model,
			PromptTemplate: body.PromptTemplate, ChoiceScores: evaluatorServiceScores(body.ChoiceScores),
			Question: evaluatorQuestionAs[evalplatform.EvaluatorQuestion](t, body.Question), Scoring: body.Scoring,
			Reads: body.Reads, MinConfidence: body.MinConfidence, EscalateToEvaluatorID: body.EscalateToEvaluatorID,
		}
		resp, err := svc.Create(r.Context(), req, "tester")
		reply(w, http.StatusCreated, resp, err)
	})
	mux.HandleFunc("PATCH "+path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeV2Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		body, _ := json.Marshal(raw)
		var req evalplatform.UpdateEvaluatorRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeV2Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		_, req.NameSet = raw["name"]
		_, req.DescriptionSet = raw["description"]
		_, req.ModelSet = raw["model"]
		_, req.PromptTemplateSet = raw["promptTemplate"]
		_, req.ChoiceScoresSet = raw["choiceScores"]
		_, req.QuestionSet = raw["question"]
		_, req.ScoringSet = raw["scoring"]
		_, req.ReadsSet = raw["reads"]
		_, req.MinConfidenceSet = raw["minConfidence"]
		_, req.EscalateToEvaluatorIDSet = raw["escalateToEvaluatorId"]
		resp, err := svc.Update(r.Context(), r.PathValue("id"), req, "tester")
		reply(w, http.StatusOK, resp, err)
	})
	return newV2PlatformClient(t, mux)
}

func evaluatorAsV2(t *testing.T, resp *evalplatform.EvaluatorResponse) wire.EvaluatorResponse {
	t.Helper()
	out := wire.EvaluatorResponse{
		ID: resp.ID, Type: "evaluator", Name: resp.Name, Description: resp.Description,
		EvaluatorType: resp.Type, HeuristicType: resp.HeuristicType, IsDefault: resp.IsDefault,
		Model: resp.Model, PromptTemplate: resp.PromptTemplate,
		Question: evaluatorQuestionAs[apiv2.EvaluatorQuestion](t, resp.Question),
		Reads:    resp.Reads, MinConfidence: resp.MinConfidence, EscalateToEvaluatorID: resp.EscalateToEvaluatorID,
		ExperimentCount: resp.ExperimentCount, CreatedBy: resp.CreatedBy, UpdatedBy: resp.UpdatedBy,
	}
	if resp.ChoiceScores != nil {
		out.ChoiceScores = make(map[string]float64, len(resp.ChoiceScores))
		for choice, score := range resp.ChoiceScores {
			out.ChoiceScores[choice], _ = score.(float64)
		}
	}
	if resp.Scoring != "" {
		out.Scoring = &resp.Scoring
	}
	return out
}

func evaluatorServiceScores(scores map[string]float64) map[string]any {
	if scores == nil {
		return nil
	}
	out := make(map[string]any, len(scores))
	for choice, score := range scores {
		out[choice] = score
	}
	return out
}

func evaluatorQuestionAs[To, From any](t *testing.T, from *From) *To {
	t.Helper()
	if from == nil {
		return nil
	}
	raw, err := json.Marshal(from)
	if err != nil {
		t.Fatal(err)
	}
	var to To
	if err := json.Unmarshal(raw, &to); err != nil {
		t.Fatal(err)
	}
	return &to
}

func applyAndReplan(t *testing.T, c *Client, desired []resolve.ResolvedResource) (applied, replanned *evaluatorKindPlan) {
	t.Helper()
	plan := func() *evaluatorKindPlan {
		actual, err := c.ListEvaluators(t.Context())
		if err != nil {
			t.Fatalf("list evaluators: %v", err)
		}
		p, err := planEvaluators(desired, actual)
		if err != nil {
			t.Fatalf("planEvaluators: %v", err)
		}
		return p
	}
	applied = plan()
	if out := applyEvaluators(t.Context(), c, applied, ApplyOptions{}); len(out.Failures) != 0 {
		t.Fatalf("apply failures: %v", out.Failures[0].Err)
	}
	return applied, plan()
}

func assertNoOp(t *testing.T, step string, p *evaluatorKindPlan, wantUnchanged int) {
	t.Helper()
	if len(p.Creates) != 0 || len(p.Updates) != 0 || len(p.Deletes) != 0 || len(p.Unchanged) != wantUnchanged {
		t.Fatalf("%s: re-plan after apply is not a no-op: creates %d, updates %+v, deletes %d, unchanged %d",
			step, len(p.Creates), p.Updates, len(p.Deletes), len(p.Unchanged))
	}
}

func TestEvaluatorRoundTrip_DecisionWithEscalationConverges(t *testing.T) {
	c := newEvaluatorPlatform(t)
	judge := resolvedEvaluator("quality-judge", evaluatorFixture(nil))

	applied, replanned := applyAndReplan(t, c, []resolve.ResolvedResource{
		resolvedEvaluator("answers", decisionManifestSpec(t, nil)), judge,
	})
	if len(applied.Creates) != 2 {
		t.Fatalf("first apply should create both evaluators; got %+v", applied)
	}
	assertNoOp(t, "create", replanned, 2)

	applied, replanned = applyAndReplan(t, c, []resolve.ResolvedResource{
		resolvedEvaluator("answers", decisionManifestSpec(t, func(m map[string]any) {
			m["minConfidence"] = 0.9
			m["reads"] = []any{"prompt", "response", "transcript"}
		})), judge,
	})
	if len(applied.Updates) != 1 {
		t.Fatalf("changed manifest should plan one update; got %+v", applied)
	}
	assertNoOp(t, "update", replanned, 2)

	applied, replanned = applyAndReplan(t, c, []resolve.ResolvedResource{
		resolvedEvaluator("answers", decisionManifestSpec(t, func(m map[string]any) {
			delete(m, "minConfidence")
			delete(m, "escalateToEvaluatorId")
		})), judge,
	})
	if len(applied.Updates) != 1 {
		t.Fatalf("dropping the escalation should plan one update; got %+v", applied)
	}
	assertNoOp(t, "clear escalation", replanned, 2)

	rows, err := c.ListEvaluators(t.Context())
	if err != nil {
		t.Fatalf("list evaluators: %v", err)
	}
	for _, r := range rows {
		if r.Name == "answers" && (r.EscalateToEvaluatorID != nil || r.MinConfidence != nil) {
			t.Fatalf("answers still escalates after the manifest dropped it: %+v", r)
		}
	}
}
