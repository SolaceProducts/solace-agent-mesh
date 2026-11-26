// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package platform

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestModelConfiguredReportsTheModelKind(t *testing.T) {
	for _, kind := range taxonomy.ModelKinds {
		props := modelConfiguredProperties("openai", "gpt-4o", "", kind, httputil.SurfaceWebUI)
		if props[productanalytics.PropModelKind] != kind {
			t.Errorf("modelKind = %v, want %q", props[productanalytics.PropModelKind], kind)
		}
	}
	props := modelConfiguredProperties("openai", "gpt-4o", "", "acme-embedding", httputil.SurfaceWebUI)
	if got, kept := props[productanalytics.PropModelKind]; kept {
		t.Errorf("modelKind = %v, want it absent", got)
	}
}

func assertProducesExactlyTheApprovedFields(t *testing.T, name productanalytics.EventName, produced map[string]any) {
	t.Helper()
	approved := productanalytics.ApprovedFields(productanalytics.SurfaceServer, name)
	for _, field := range approved {
		if _, ok := produced[field]; !ok {
			t.Errorf("%s approves %q but the emitter never produces it", name, field)
		}
	}
	for field := range produced {
		if !slices.Contains(approved, field) {
			t.Errorf("%s emitter produces %q, which the catalog does not approve", name, field)
		}
	}
}

func TestEvaluatorCreatedProducesEveryApprovedField(t *testing.T) {
	produced := evaluatorCreatedProperties(taxonomy.EvaluatorTypeDecision, "score", taxonomy.EscalationOnLowConfidence, httputil.SurfaceCLI)
	assertProducesExactlyTheApprovedFields(t, productanalytics.EventEvaluatorCreated, produced)
}

func TestEvaluatorCreatedCarriesDecisionFieldsOnlyForADecisionEvaluator(t *testing.T) {
	props := evaluatorCreatedProperties(taxonomy.EvaluatorTypeLLMJudge, "choice", taxonomy.EscalationOnError, httputil.SurfaceWebUI)
	if props[productanalytics.PropEvaluatorType] != taxonomy.EvaluatorTypeLLMJudge {
		t.Errorf("evaluatorType = %v, want llm_judge", props[productanalytics.PropEvaluatorType])
	}
	for _, field := range []string{productanalytics.PropQuestionType, productanalytics.PropEscalation} {
		if got, kept := props[field]; kept {
			t.Errorf("%s = %v on an llm_judge evaluator, want it absent", field, got)
		}
	}
}

func TestEvaluatorCreatedNeverBuildsAuthoredText(t *testing.T) {
	authored := []string{"acme-refund-judge", "Did the agent quote the Acme refund policy?", "escalate to acme-legal"}
	if props := evaluatorCreatedProperties(authored[0], authored[1], authored[2], "acme-provisioner"); len(props) != 0 {
		t.Errorf("built %v from authored text, want nothing", props)
	}
	props := evaluatorCreatedProperties(taxonomy.EvaluatorTypeDecision, authored[1], authored[2], httputil.SurfaceWebUI)
	rendered := fmt.Sprintf("%v", props)
	for _, text := range authored {
		if strings.Contains(rendered, text) {
			t.Errorf("%q reached the built properties %s", text, rendered)
		}
	}
}

func TestEvalRunProducesEveryApprovedField(t *testing.T) {
	shape := &EvalRunShape{
		EvaluatorTypes:    []string{taxonomy.EvaluatorTypeHeuristic},
		MultiTrial:        true,
		TrialFailureKinds: []string{"node_failed"},
	}
	produced := evalRunProperties(taxonomy.OutcomeError, taxonomy.ErrorCategoryUnknown, taxonomy.EvalTargetTypeWorkflow, shape, &EvalDecisionFacts{
		Evaluators: 1, Decided: 3, Escalated: 2, Errored: 1,
		EscalatedBy: map[string]int{taxonomy.EscalationReasonLowConfidence: 2},
	})
	assertProducesExactlyTheApprovedFields(t, productanalytics.EventEvalRun, produced)
}

func TestEvalCreatedProducesEveryApprovedField(t *testing.T) {
	produced := approvedOnly(productanalytics.PropTargetType, taxonomy.EvalTargetTypeWorkflow, productanalytics.EvalTargetTypes)
	assertProducesExactlyTheApprovedFields(t, productanalytics.EventEvalCreated, produced)
}

func TestEvalRunTargetTypeOnlyWhenKnown(t *testing.T) {
	if got, kept := evalRunProperties(taxonomy.OutcomeTimeout, "", "", nil, nil)[productanalytics.PropTargetType]; kept {
		t.Errorf("targetType = %v for a run reported without its experiment, want it absent", got)
	}
	if got := evalRunProperties(taxonomy.OutcomeSuccess, "", taxonomy.EvalTargetTypeAgent, nil, nil)[productanalytics.PropTargetType]; got != taxonomy.EvalTargetTypeAgent {
		t.Errorf("targetType = %v, want agent", got)
	}
	if got, kept := evalRunProperties(taxonomy.OutcomeSuccess, "", "robot", nil, nil)[productanalytics.PropTargetType]; kept {
		t.Errorf("targetType = %v for an unknown value, want it absent", got)
	}
}

func TestEvalRunReportsDecisionCountsOnlyWhenCounted(t *testing.T) {
	props := evalRunProperties(taxonomy.OutcomeTimeout, "", "", nil, nil)
	if got, kept := props[productanalytics.PropDecisionEvaluatorCount]; kept {
		t.Errorf("decisionEvaluatorCount = %v for an uncounted run, want it absent", got)
	}

	props = evalRunProperties(taxonomy.OutcomeSuccess, "", "", nil, &EvalDecisionFacts{})
	if props[productanalytics.PropDecisionEvaluatorCount] != 0 {
		t.Errorf("decisionEvaluatorCount = %v, want 0", props[productanalytics.PropDecisionEvaluatorCount])
	}
	for _, field := range []string{
		productanalytics.PropDecisionCasesDecided, productanalytics.PropDecisionCasesEscalated,
		productanalytics.PropDecisionCasesErrored, productanalytics.PropDecisionEscalatedLowConfidence,
	} {
		if got, kept := props[field]; kept {
			t.Errorf("%s = %v on a run without a decision-model evaluator, want it absent", field, got)
		}
	}

	props = evalRunProperties(taxonomy.OutcomeSuccess, "", "", nil, &EvalDecisionFacts{
		Evaluators: 2, Decided: 5, Escalated: 3, Errored: 1,
		EscalatedBy: map[string]int{
			taxonomy.EscalationReasonLowConfidence: 2,
			taxonomy.EscalationReasonProviderError: 1,
		},
	})
	want := map[string]int{
		productanalytics.PropDecisionEvaluatorCount:         2,
		productanalytics.PropDecisionCasesDecided:           5,
		productanalytics.PropDecisionCasesEscalated:         3,
		productanalytics.PropDecisionCasesErrored:           1,
		productanalytics.PropDecisionEscalatedLowConfidence: 2,
		productanalytics.PropDecisionEscalatedNoConfidence:  0,
		productanalytics.PropDecisionEscalatedProviderError: 1,
		productanalytics.PropDecisionEscalatedModelLookup:   0,
	}
	for field, n := range want {
		if props[field] != n {
			t.Errorf("%s = %v, want %d", field, props[field], n)
		}
	}
}

func TestEveryEscalationReasonHasAField(t *testing.T) {
	approved := productanalytics.ApprovedFields(productanalytics.SurfaceServer, productanalytics.EventEvalRun)
	for _, reason := range taxonomy.EscalationReasons {
		field, ok := productanalytics.DecisionEscalationReasonFields[reason]
		if !ok {
			t.Errorf("escalation reason %q has no eval:run field", reason)
			continue
		}
		if !slices.Contains(approved, field) {
			t.Errorf("escalation reason %q counts into %q, which eval:run does not approve", reason, field)
		}
	}
}

func TestEvalEventsCarryTheTargetTypeToTheSink(t *testing.T) {
	svc, sink := newCapturingAnalytics()
	r := NewResourceReporter(svc, stubDeployHistory{}, slog.New(slog.DiscardHandler))

	r.EvalCreated(t.Context(), taxonomy.EvalTargetTypeWorkflow)
	r.EvalRun(t.Context(), taxonomy.OutcomeError, taxonomy.ErrorCategoryBudgetExhausted, taxonomy.EvalTargetTypeWorkflow, nil, nil)

	r.AwaitReports(t.Context())
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	byName := map[string]map[string]any{}
	for _, e := range sink.all() {
		byName[e.Name] = e.Properties
	}
	created, run := byName[productanalytics.EventEvalCreated], byName[productanalytics.EventEvalRun]
	if created[productanalytics.PropTargetType] != taxonomy.EvalTargetTypeWorkflow {
		t.Errorf("eval:created properties = %v, want targetType workflow", created)
	}
	if run[productanalytics.PropTargetType] != taxonomy.EvalTargetTypeWorkflow || run[productanalytics.PropErrorCategory] != taxonomy.ErrorCategoryBudgetExhausted {
		t.Errorf("eval:run properties = %v, want targetType workflow and errorCategory budget_exhausted", run)
	}
}

func TestEvalRunShapesReachTheSink(t *testing.T) {
	svc, sink := newCapturingAnalytics()
	r := NewResourceReporter(svc, stubDeployHistory{}, slog.New(slog.DiscardHandler))
	allTypes := []string{taxonomy.EvaluatorTypeDecision, taxonomy.EvaluatorTypeHeuristic, taxonomy.EvaluatorTypeLLMJudge}

	r.EvalRun(t.Context(), taxonomy.OutcomeSuccess, "", taxonomy.EvalTargetTypeWorkflow,
		&EvalRunShape{EvaluatorTypes: allTypes, MultiTrial: true, TrialFailureKinds: []string{"timeout"}}, nil)
	r.EvalRun(t.Context(), taxonomy.OutcomeSuccess, "", taxonomy.EvalTargetTypeAgent,
		&EvalRunShape{EvaluatorTypes: []string{taxonomy.EvaluatorTypeHeuristic}, TrialFailureKinds: []string{"auth_required"}}, nil)
	r.EvalRun(t.Context(), taxonomy.OutcomeError, "", taxonomy.EvalTargetTypeWorkflow,
		&EvalRunShape{EvaluatorTypes: []string{taxonomy.EvaluatorTypeLLMJudge}, TrialFailureKinds: []string{"timeout"}, TrialFailureKindsUnknown: true}, nil)

	r.AwaitReports(t.Context())
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	runs := map[string]map[string]any{}
	for _, e := range sink.all() {
		if e.Name == productanalytics.EventEvalRun {
			runs[fmt.Sprint(e.Properties[productanalytics.PropOutcome], "/", e.Properties[productanalytics.PropTargetType])] = e.Properties
		}
	}
	for key, want := range map[string]struct{ types, trials, kinds string }{
		"success/workflow": {fmt.Sprint(allTypes), taxonomy.EvalTrialsMultiple, fmt.Sprint([]string{"timeout"})},
		"success/agent":    {fmt.Sprint([]string{taxonomy.EvaluatorTypeHeuristic}), taxonomy.EvalTrialsSingle, ""},
		"error/workflow":   {fmt.Sprint([]string{taxonomy.EvaluatorTypeLLMJudge}), taxonomy.EvalTrialsSingle, ""},
	} {
		props, ok := runs[key]
		if !ok {
			t.Errorf("no eval:run delivered for %s; delivered %v", key, runs)
			continue
		}
		if got := fmt.Sprint(props[productanalytics.PropEvaluatorTypes]); got != want.types {
			t.Errorf("%s evaluatorTypes = %s, want %s", key, got, want.types)
		}
		if got := props[productanalytics.PropTrialsPerExample]; got != want.trials {
			t.Errorf("%s trialsPerExample = %v, want %s", key, got, want.trials)
		}
		kinds, kept := props[productanalytics.PropWorkflowTrialFailureKinds]
		switch {
		case kept && fmt.Sprint(kinds) != want.kinds:
			t.Errorf("%s workflowTrialFailureKinds = %v, want %q", key, kinds, want.kinds)
		case !kept && want.kinds != "":
			t.Errorf("%s carried no workflowTrialFailureKinds, want %s", key, want.kinds)
		}
	}
}

func TestEvalRunReportsHowTheRunWasSetUp(t *testing.T) {
	for _, tc := range []struct {
		name       string
		multiTrial bool
		want       string
	}{
		{name: "one trial per example", want: taxonomy.EvalTrialsSingle},
		{name: "several trials per example", multiTrial: true, want: taxonomy.EvalTrialsMultiple},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shape := &EvalRunShape{
				EvaluatorTypes: []string{taxonomy.EvaluatorTypeDecision, taxonomy.EvaluatorTypeLLMJudge},
				MultiTrial:     tc.multiTrial,
			}
			props := evalRunProperties(taxonomy.OutcomeSuccess, "", taxonomy.EvalTargetTypeAgent, shape, nil)
			if got := props[productanalytics.PropTrialsPerExample]; got != tc.want {
				t.Errorf("trialsPerExample = %v, want %q", got, tc.want)
			}
			wantTypes := []string{taxonomy.EvaluatorTypeDecision, taxonomy.EvaluatorTypeLLMJudge}
			if got := props[productanalytics.PropEvaluatorTypes]; !slices.Equal(got.([]string), wantTypes) {
				t.Errorf("evaluatorTypes = %v, want %v", got, wantTypes)
			}
		})
	}
}

func TestEvalRunWithoutAShapeReportsNoSetup(t *testing.T) {
	props := evalRunProperties(taxonomy.OutcomeTimeout, "", taxonomy.EvalTargetTypeWorkflow, nil, nil)
	for _, field := range []string{
		productanalytics.PropEvaluatorTypes, productanalytics.PropTrialsPerExample, productanalytics.PropWorkflowTrialFailureKinds,
	} {
		if got, kept := props[field]; kept {
			t.Errorf("%s = %v for a run reported without its shape, want it absent", field, got)
		}
	}
}

func TestEvalRunReportsWhyWorkflowTrialsEndedEarly(t *testing.T) {
	kinds := []string{"timeout", "node_failed"}
	props := evalRunProperties(taxonomy.OutcomeSuccess, "", taxonomy.EvalTargetTypeWorkflow, &EvalRunShape{TrialFailureKinds: kinds}, nil)
	if got := props[productanalytics.PropWorkflowTrialFailureKinds]; !slices.Equal(got.([]string), []string{"node_failed", "timeout"}) {
		t.Errorf("workflowTrialFailureKinds = %v, want node_failed and timeout", got)
	}

	props = evalRunProperties(taxonomy.OutcomeSuccess, "", taxonomy.EvalTargetTypeWorkflow, &EvalRunShape{}, nil)
	got, kept := props[productanalytics.PropWorkflowTrialFailureKinds]
	if !kept || len(got.([]string)) != 0 {
		t.Errorf("workflowTrialFailureKinds = %v (kept %v), want an empty set for a workflow run that ended nothing early", got, kept)
	}

	unknown := &EvalRunShape{TrialFailureKinds: []string{"timeout"}, TrialFailureKindsUnknown: true}
	props = evalRunProperties(taxonomy.OutcomeError, "", taxonomy.EvalTargetTypeWorkflow, unknown, nil)
	if got, kept := props[productanalytics.PropWorkflowTrialFailureKinds]; kept {
		t.Errorf("workflowTrialFailureKinds = %v for a run with a trial abandoned for no recorded reason, want it absent", got)
	}

	props = evalRunProperties(taxonomy.OutcomeSuccess, "", taxonomy.EvalTargetTypeAgent, &EvalRunShape{TrialFailureKinds: []string{"auth_required"}}, nil)
	if got, kept := props[productanalytics.PropWorkflowTrialFailureKinds]; kept {
		t.Errorf("workflowTrialFailureKinds = %v on an agent run, want it absent", got)
	}
}

func TestEvalRunShapeNeverBuildsAuthoredText(t *testing.T) {
	authored := []string{"acme-payroll-judge", `node "reconcile-acme-ledger" failed`, "partial_failure"}
	shape := &EvalRunShape{EvaluatorTypes: authored[:1], TrialFailureKinds: authored[1:]}
	props := evalRunProperties(taxonomy.OutcomeError, "", taxonomy.EvalTargetTypeWorkflow, shape, nil)
	rendered := fmt.Sprintf("%v", props)
	for _, text := range authored {
		if strings.Contains(rendered, text) {
			t.Errorf("%q reached the built properties %s", text, rendered)
		}
	}
}
