// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package apiv2

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const evaluationsAuthoredQuestion = "Did the agent quote the Acme refund policy verbatim?"

type evaluationsAnalytics struct {
	api       *entrypointAPI
	sink      *analyticsSink
	analytics *productanalytics.Service
}

func newEvaluationsAnalytics(t *testing.T, decisionEvaluators bool) evaluationsAnalytics {
	t.Helper()
	sink := &analyticsSink{}
	analytics := productanalytics.NewService(productanalytics.Options{
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{Marker: "install-under-test", BindingKey: []byte("binding-key")}
		}),
		Logger: slog.New(slog.DiscardHandler),
	})
	testUsageReporter = analytics
	t.Cleanup(func() { testUsageReporter = nil })
	t.Setenv(features.EnvVarName(features.KeyEvalDecisionEvaluator), fmt.Sprint(decisionEvaluators))
	return evaluationsAnalytics{api: newEntrypointAPI(t, nil), sink: sink, analytics: analytics}
}

func (a evaluationsAnalytics) callFrom(surface, method, path string, body any) *httptest.ResponseRecorder {
	a.api.t.Helper()
	req := newJSONRequest(a.api.t, method, path, body)
	req.Header.Set(httputil.HeaderClientSurface, surface)
	rec := httptest.NewRecorder()
	a.api.handler.ServeHTTP(rec, req)
	return rec
}

func (a evaluationsAnalytics) delivered() []productanalytics.Event {
	a.api.t.Helper()
	a.api.core.ResourceAnalytics().AwaitReports(a.api.t.Context())
	if err := a.analytics.Stop(a.api.t.Context()); err != nil {
		a.api.t.Fatalf("stop analytics: %v", err)
	}
	return a.sink.all()
}

func (a evaluationsAnalytics) judgeAndDecisionModel() string {
	a.api.t.Helper()
	evaluationsDecisionModels(a.api)
	return newEvaluator(a.api, llmJudgeBody("acme-refund-judge")).ID
}

func evaluationsEventsNamed(events []productanalytics.Event, name string) []productanalytics.Event {
	var found []productanalytics.Event
	for _, e := range events {
		if e.Name == name {
			found = append(found, e)
		}
	}
	return found
}

func TestEvaluators_CreatingADecisionEvaluatorReportsItsShapeNotItsContent(t *testing.T) {
	a := newEvaluationsAnalytics(t, true)
	judgeID := a.judgeAndDecisionModel()

	expectStatus(t, a.callFrom(httputil.SurfaceCLI, http.MethodPost, evaluatorsPath, map[string]any{
		"name": "acme-refund-decider", "description": "decides the refund answers", "evaluatorType": "decision", "model": evaluationsDecisionModel,
		"question": map[string]any{"type": "choice", "instructions": evaluationsAuthoredQuestion,
			"criteria": []map[string]any{{"key": "quoted"}, {"key": "paraphrased"}}},
		"choiceScores":          map[string]float64{"quoted": 1, "paraphrased": 0},
		"minConfidence":         0.8,
		"escalateToEvaluatorId": judgeID,
	}), http.StatusCreated)

	events := a.delivered()
	created := evaluationsEventsNamed(events, productanalytics.EventEvaluatorCreated)
	if len(created) != 2 {
		t.Fatalf("got %d evaluator:created events, want one per evaluator", len(created))
	}
	byType := map[any]map[string]any{}
	for _, e := range created {
		byType[e.Properties[productanalytics.PropEvaluatorType]] = e.Properties
	}
	judge := byType[taxonomy.EvaluatorTypeLLMJudge]
	if judge[productanalytics.PropSubmittedFrom] != httputil.SurfaceAPI {
		t.Errorf("llm_judge submittedFrom = %v, want api", judge[productanalytics.PropSubmittedFrom])
	}
	if _, kept := judge[productanalytics.PropQuestionType]; kept {
		t.Errorf("an llm_judge evaluator reported questionType %v", judge[productanalytics.PropQuestionType])
	}
	decider := byType[taxonomy.EvaluatorTypeDecision]
	for field, want := range map[string]string{
		productanalytics.PropQuestionType:  "choice",
		productanalytics.PropEscalation:    taxonomy.EscalationOnLowConfidence,
		productanalytics.PropSubmittedFrom: httputil.SurfaceCLI,
	} {
		if decider[field] != want {
			t.Errorf("decision evaluator %s = %v, want %q", field, decider[field], want)
		}
	}
	rendered := fmt.Sprintf("%v", created)
	for _, authored := range []string{"acme-refund", evaluationsAuthoredQuestion, "quoted", "paraphrased", judgeID} {
		if strings.Contains(rendered, authored) {
			t.Errorf("%q reached a delivered evaluator:created event: %s", authored, rendered)
		}
	}

	var decisionModels int
	for _, e := range evaluationsEventsNamed(events, productanalytics.EventModelConfigured) {
		if e.Properties[productanalytics.PropModelKind] == taxonomy.ModelKindDecision {
			decisionModels++
			if e.Properties[productanalytics.PropProvider] != "typesafe" {
				t.Errorf("decision model provider = %v, want typesafe", e.Properties[productanalytics.PropProvider])
			}
		}
	}
	if decisionModels != 1 {
		t.Errorf("model:configured events of kind decision = %d, want 1", decisionModels)
	}
}

func TestEvaluators_DecisionEscalationIsReportedAsConfigured(t *testing.T) {
	a := newEvaluationsAnalytics(t, true)
	judgeID := a.judgeAndDecisionModel()
	boolean := map[string]any{"type": "boolean", "instructions": "Answered?"}

	expectStatus(t, a.api.call(http.MethodPost, evaluatorsPath, map[string]any{
		"name": "no-escalation", "description": "d", "evaluatorType": "decision", "model": evaluationsDecisionModel, "question": boolean,
	}), http.StatusCreated)
	expectStatus(t, a.api.call(http.MethodPost, evaluatorsPath, map[string]any{
		"name": "on-error", "description": "d", "evaluatorType": "decision", "model": evaluationsDecisionModel, "question": boolean,
		"escalateToEvaluatorId": judgeID,
	}), http.StatusCreated)

	got := map[any]int{}
	for _, e := range evaluationsEventsNamed(a.delivered(), productanalytics.EventEvaluatorCreated) {
		if e.Properties[productanalytics.PropEvaluatorType] == taxonomy.EvaluatorTypeDecision {
			got[e.Properties[productanalytics.PropEscalation]]++
		}
	}
	if got[taxonomy.EscalationNone] != 1 || got[taxonomy.EscalationOnError] != 1 {
		t.Errorf("escalation counts = %v, want one none and one on_error", got)
	}
}

func TestEvaluators_ARefusedDecisionEvaluatorReportsNothing(t *testing.T) {
	a := newEvaluationsAnalytics(t, false)

	expectStatus(t, a.api.call(http.MethodPost, evaluatorsPath, decisionBody("decider")), http.StatusUnprocessableEntity)
	newEvaluator(a.api, llmJudgeBody("accepted-judge"))

	created := evaluationsEventsNamed(a.delivered(), productanalytics.EventEvaluatorCreated)
	if len(created) != 1 || created[0].Properties[productanalytics.PropEvaluatorType] != taxonomy.EvaluatorTypeLLMJudge {
		t.Errorf("evaluator:created events = %v, want only the accepted llm_judge", created)
	}
}
