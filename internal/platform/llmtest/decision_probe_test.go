// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llmtest

import (
	"log/slog"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/typesafe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func startMockLLM(t *testing.T) string {
	t.Helper()
	sc, err := mockllm.ParseScenario([]byte(`{"id":"d","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`))
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	srv := mockllm.NewMockLLMServer("127.0.0.1:0", 0, 0, slog.Default())
	srv.LoadScenarios(sc)
	if err := srv.Start(); err != nil {
		t.Fatalf("start mock llm: %v", err)
	}
	t.Cleanup(srv.Stop)
	return "http://" + srv.Addr()
}

func TestProbeDecisionAgainstTheE2EMockAnswersInTheShapeTheClientAccepts(t *testing.T) {
	apiBase := startMockLLM(t)

	t.Run("a valid key passes", func(t *testing.T) {
		ok, msg := ProbeDecision(t.Context(), decision.ProviderTypeSafe, map[string]any{"api_base": apiBase, "api_key": "e2e-key", "model": "jev-1.13.0"}, http.DefaultClient, slog.Default(), nil)
		if !ok || msg != probeSuccessMessage {
			t.Fatalf("probe = (%v, %q), want success", ok, msg)
		}
	})
	t.Run("the rejected key fails as an authentication failure", func(t *testing.T) {
		ok, msg := ProbeDecision(t.Context(), decision.ProviderTypeSafe, map[string]any{"api_base": apiBase, "api_key": mockllm.RejectedDecisionAPIKey, "model": "jev-1.13.0"}, http.DefaultClient, slog.Default(), nil)
		if ok || !strings.Contains(msg, "API key rejected (HTTP 401)") {
			t.Fatalf("probe = (%v, %q), want the 401 rejection", ok, msg)
		}
	})
}

func TestProbeOpenAIDecisionAgainstTheE2EMock(t *testing.T) {
	apiBase := startMockLLM(t) + "/v1"

	cases := map[string]struct {
		apiKey  string
		wantOK  bool
		wantMsg string
	}{
		"a valid key passes":               {apiKey: "e2e-key", wantOK: true, wantMsg: probeSuccessMessage},
		"the rejected key fails":           {apiKey: mockllm.RejectedDecisionAPIKey, wantMsg: "API key rejected (HTTP 401)"},
		"an account without credits fails": {apiKey: mockllm.NoCreditsDecisionAPIKey, wantMsg: "no credits or quota left"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ok, msg := ProbeDecision(t.Context(), decision.ProviderOpenAI, map[string]any{"api_base": apiBase, "api_key": tc.apiKey, "model": "gpt-6-luna"}, http.DefaultClient, slog.Default(), nil)
			if ok != tc.wantOK || !strings.Contains(msg, tc.wantMsg) {
				t.Fatalf("probe = (%v, %q), want (%v, containing %q)", ok, msg, tc.wantOK, tc.wantMsg)
			}
		})
	}
}

func TestTheE2EMockAnswersEveryQuestionTypeTheClientDecodes(t *testing.T) {
	client, err := typesafe.New(typesafe.Config{BaseURL: startMockLLM(t), APIKey: "e2e-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	questions := []decision.Question{
		{Key: "department", Type: decision.TypeChoice, Instructions: "Which desk owns the ticket?", Criteria: []decision.Criterion{
			{Key: "billing", Description: "Invoices"}, {Key: "technical", Description: "Outages"}, {Key: "general", Description: "Anything else"},
		}},
		{Key: "severity", Type: decision.TypeScore, Instructions: "How severe is it?", Criteria: []decision.Criterion{
			{Key: "low", Description: "Cosmetic"}, {Key: "medium", Description: "Degraded"}, {Key: "high", Description: "Down"},
		}},
		{Key: "urgent", Type: decision.TypeBoolean, Instructions: "Is it urgent?"},
	}
	cases := map[string]struct {
		state          string
		wantConfidence float64
	}{
		"a plain ticket gets confident answers":   {state: "my invoice is wrong", wantConfidence: 1},
		"the marker lowers the choice confidence": {state: "help " + mockllm.LowConfidenceMarker, wantConfidence: mockllm.LowChoiceConfidence},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := client.Evaluate(t.Context(), decision.Request{Model: "jev-1.13.0", State: tc.state, Questions: questions})
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if err := result.Validate(questions); err != nil {
				t.Fatalf("validate: %v", err)
			}
			department := result.Answers["department"]
			if department.Choice != "billing" || department.Confidence == nil || math.Abs(*department.Confidence-tc.wantConfidence) > 1e-9 {
				t.Errorf("department = %+v, want billing at confidence %v", department, tc.wantConfidence)
			}
			if severity := result.Answers["severity"]; severity.Type != decision.TypeScore || severity.Score != 0 {
				t.Errorf("severity = %+v, want the lowest level", severity)
			}
			if urgent := result.Answers["urgent"]; urgent.Type != decision.TypeBoolean || urgent.Probability < 0.5 {
				t.Errorf("urgent = %+v, want a confident true", urgent)
			}
		})
	}
}
