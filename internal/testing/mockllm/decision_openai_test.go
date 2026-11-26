// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/openai"
)

var openAIMockQuestions = []decision.Question{
	{Key: "urgent", Type: decision.TypeBoolean, Instructions: "x"},
	{Key: "department", Type: decision.TypeChoice, Instructions: "x", Criteria: []decision.Criterion{{Key: "technical"}, {Key: "billing", Description: "invoices"}}},
	{Key: "severity", Type: decision.TypeScore, Instructions: "x", Criteria: []decision.Criterion{{Key: "low"}, {Key: "medium"}, {Key: "high"}}},
}

func evaluateAgainstMock(t *testing.T, apiKey string, state any) (decision.Result, error) {
	t.Helper()
	srv := startServer(t, mustScenario(t, `{"id":"d","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)
	client, err := openai.New(openai.Config{BaseURL: "http://" + srv.Addr() + "/v1", APIKey: apiKey})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	return client.Evaluate(t.Context(), decision.Request{State: state, Questions: openAIMockQuestions})
}

func TestOpenAIDecisionAnswersEachQuestionTypeThroughTheRealClient(t *testing.T) {
	result, err := evaluateAgainstMock(t, "any-key", map[string]any{"ticket": "x"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if err := result.Validate(openAIMockQuestions); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := result.Answers["urgent"].Probability; got != mockBooleanProbability {
		t.Errorf("urgent = %v, want a confident true", got)
	}
	if got := result.Answers["department"]; got.Choice != "technical" || *got.Confidence != 1 {
		t.Errorf("department = %+v, want the first declared option at full confidence", got)
	}
	if got := result.Answers["severity"]; got.Score != 0 || got.Probabilities["low"] != 1 {
		t.Errorf("severity = %+v, want the lowest level", got)
	}
	if result.Model != mockOpenAIDecisionModel || result.Usage.InputTokens == 0 {
		t.Errorf("model/usage = %q %+v", result.Model, result.Usage)
	}
}

func TestOpenAIDecisionMarkersAndKeys(t *testing.T) {
	t.Run("low confidence", func(t *testing.T) {
		result, err := evaluateAgainstMock(t, "any-key", LowConfidenceMarker)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if got := *result.Answers["department"].Confidence; got != LowChoiceConfidence {
			t.Errorf("confidence = %v, want %v", got, LowChoiceConfidence)
		}
	})
	cases := map[string]struct {
		apiKey string
		state  string
		want   decision.FailureKind
	}{
		"refusal":      {apiKey: "any-key", state: RefusalMarker, want: decision.FailureRefused},
		"rejected key": {apiKey: RejectedDecisionAPIKey, state: "x", want: decision.FailureUnauthorized},
		"no credits":   {apiKey: NoCreditsDecisionAPIKey, state: "x", want: decision.FailureConfig},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := evaluateAgainstMock(t, tc.apiKey, tc.state)
			if kind := decision.Classify(err); kind != tc.want {
				t.Errorf("kind = %q (err %v), want %q", kind, err, tc.want)
			}
		})
	}
}
