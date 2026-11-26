// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

const evaluationsDecisionModel = "eval-judge"

func evaluationsDecisionModels(api *entrypointAPI) {
	api.t.Helper()
	expectCreated[Model](api.t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": evaluationsDecisionModel, "provider": "typesafe", "modelName": "jev-1.13.0",
		"modelType": "decision", "authConfig": apiKeyAuth(),
	}), "models")
	createModel(api, "gpt", apiKeyAuth())
}

type evaluationsDecisionFixture struct {
	experimentFixture
	judgeID      string
	experimentID string
}

func newEvaluationsDecisionFixture(t *testing.T, decisionModel string) evaluationsDecisionFixture {
	t.Helper()
	api := newEvaluatorAPI(t)
	evaluationsDecisionModels(api)
	judgeBody := llmJudgeBody("judge")
	judgeBody["model"] = "gpt"
	judge := newEvaluator(api, judgeBody)
	answers := decisionBody("answers")
	answers["model"], answers["escalateToEvaluatorId"] = decisionModel, judge.ID
	decision := newEvaluator(api, answers)
	dataset := newDataset(api, "decision-dataset")
	newExample(api, dataset.ID, map[string]any{"prompt": "ping"})
	f := experimentFixture{api: api, datasetID: dataset.ID, evaluators: []string{decision.ID}}
	return evaluationsDecisionFixture{experimentFixture: f, judgeID: judge.ID, experimentID: f.create("decision-experiment", "Orchestrator").ID}
}

func (f evaluationsDecisionFixture) expectTriggerRefused(wants ...string) {
	f.api.t.Helper()
	rec := f.api.call(http.MethodPut, experimentsPath+"/"+f.experimentID+"/trigger", map[string]any{})
	expectStatus(f.api.t, rec, http.StatusUnprocessableEntity)
	details := decodeError(f.api.t, rec).ValidationDetails["evaluatorIds"]
	if len(details) != 1 {
		f.api.t.Fatalf("validationDetails.evaluatorIds = %v, want one message: %s", details, rec.Body.String())
	}
	for _, want := range wants {
		if !strings.Contains(details[0], want) {
			f.api.t.Errorf("validationDetails.evaluatorIds = %q, want it to contain %q", details[0], want)
		}
	}
}

func TestEvaluators_DecisionModelMustBeADecisionModel(t *testing.T) {
	api := newEvaluatorAPI(t)
	evaluationsDecisionModels(api)

	onChat := decisionBody("answers-chat")
	onChat["model"] = "gpt"
	expectDetail(t, api.call(http.MethodPost, evaluatorsPath, onChat), http.StatusUnprocessableEntity, "model")

	onDecision := decisionBody("answers")
	onDecision["model"] = evaluationsDecisionModel
	expectStatus(t, api.call(http.MethodPost, evaluatorsPath, onDecision), http.StatusCreated)
}

func TestRuns_TriggerAcceptsARegisteredDecisionModel(t *testing.T) {
	f := newEvaluationsDecisionFixture(t, evaluationsDecisionModel)
	runs := f.groupRuns(f.trigger(f.experimentID, map[string]any{}).ID)
	if len(runs) != 1 || runs[0].ExperimentID != f.experimentID {
		t.Fatalf("runs = %+v, want one run of experiment %s", runs, f.experimentID)
	}
}

func TestRuns_TriggerRefusesAnUnregisteredDecisionModel(t *testing.T) {
	f := newEvaluationsDecisionFixture(t, "never-registered")
	f.expectTriggerRefused(`"answers"`, `"never-registered"`, "is not configured")
}

func TestRuns_TriggerRefusedOnceDecisionEvaluatorsAreTurnedOff(t *testing.T) {
	f := newEvaluationsDecisionFixture(t, evaluationsDecisionModel)
	t.Setenv(features.EnvVarName(features.KeyEvalDecisionEvaluator), "false")
	f.expectTriggerRefused(`"answers"`, "SAM_FEATURE_EVAL_DECISION_EVALUATOR")
}

func TestEvaluators_DeletingAnEscalationTargetIs422(t *testing.T) {
	f := newEvaluationsDecisionFixture(t, evaluationsDecisionModel)
	rec := f.api.call(http.MethodDelete, evaluatorsPath+"/"+f.judgeID, nil)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	decodeError(t, rec)
	expectStatus(t, f.api.call(http.MethodGet, evaluatorsPath+"/"+f.judgeID, nil), http.StatusOK)
}
