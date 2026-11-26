// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

var geminiDecisionWorkflow = strings.Replace(routedDecisionWorkflow, inlineTypeSafeBlock,
	"decision:\n  provider: google_ai_studio\n  api_key: test-key\n  model: gemini-decision-1\n", 1)

func geminiDepartmentAnswer(choice string, confidence float64) func(map[string]any) string {
	return func(map[string]any) string {
		return `{"answers":{
			"department":{"type":"choice","choice":"` + choice + `","probabilities":{"billing":0.5,"tech":0.5},"confidence":` + jsonNumber(confidence) + `},
			"urgent":{"type":"noul","noul":0.9,"confidence":0.9}},
			"usage":{"total_input_tokens":40,"total_output_tokens":0}}`
	}
}

func registeredGeminiDecisionModel(apiBase, apiKey string) map[string]any {
	raw := registeredDecisionModel(apiBase, apiKey)
	raw["model"] = "gemini-decision-1-preview"
	raw[samapp.DecisionProviderKey] = decision.ProviderGoogleAIStudio
	return raw
}

func (f *fakeJev) sentGeminiShape() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, hasInput := f.lastBody["input"].(string)
	_, questionsAreAMap := f.lastBody["questions"].(map[string]any)
	return hasInput && questionsAreAMap
}

func (f *fakeJev) lastModel() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	model, _ := f.lastBody["model"].(string)
	return model
}

func TestAliasedGeminiDecisionModel(t *testing.T) {
	jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, aliasedDecisionWorkflow, jev, true,
		withRegistry(t, nil, "jev", func(url string) map[string]any { return registeredGeminiDecisionModel(url, "registry-key") }))
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	if !jev.sentGeminiShape() {
		t.Errorf("request = %v, want the Gemini wire shape", jev.lastBody)
	}
	if got := jev.lastModel(); got != "gemini-decision-1-preview" {
		t.Errorf("model = %q, want the registered name gemini-decision-1-preview", got)
	}
}

func TestInlineGeminiDecisionModel(t *testing.T) {
	jev := &fakeJev{respond: geminiDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, geminiDecisionWorkflow, jev, true)
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	if !jev.sentGeminiShape() || jev.lastModel() != "gemini-decision-1" {
		t.Errorf("request = %v, want the Gemini wire shape for gemini-decision-1", jev.lastBody)
	}
}

func TestParseDecisionProviderAcceptsGoogleAIStudio(t *testing.T) {
	cfg, err := ParseDecisionProvider(map[string]any{"provider": "google_ai_studio", "api_key": "k"})
	if err != nil {
		t.Fatalf("ParseDecisionProvider: %v", err)
	}
	if cfg.Provider != DecisionProviderGoogleAIStudio {
		t.Errorf("provider = %q, want google_ai_studio", cfg.Provider)
	}
}
