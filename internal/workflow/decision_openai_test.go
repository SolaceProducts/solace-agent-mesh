// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const inlineTypeSafeBlock = "decision:\n  api_key: test-key\n  model: jev-1.13.0\n"

var openAIDecisionWorkflow = strings.Replace(routedDecisionWorkflow, inlineTypeSafeBlock,
	"decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n", 1)

var openAIEscalatingDecisionWorkflow = strings.Replace(escalatingDecisionWorkflow, inlineTypeSafeBlock,
	"decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n", 1)

func openAIDepartmentAnswer(choice string, confidence float64) func(map[string]any) string {
	return func(map[string]any) string {
		return `{"model":"gpt-6-luna","answers":[
			{"type":"choice","name":"department","choice":"` + choice + `","probabilities":[{"value":"billing","probability":0.5},{"value":"tech","probability":0.5}],"confidence":` + jsonNumber(confidence) + `},
			{"type":"predicate","name":"urgent","probability":0.9}],
			"usage":{"input_tokens":40,"output_tokens":0}}`
	}
}

func openAIRefusal(map[string]any) string {
	return `{"model":"gpt-6-luna","answers":[
		{"type":"refusal","name":"department","refusal":"REFUSAL-CANARY-4410"},
		{"type":"predicate","name":"urgent","probability":0.9}]}`
}

func registeredOpenAIDecisionModel(apiBase, apiKey string) map[string]any {
	raw := registeredDecisionModel(apiBase, apiKey)
	raw["model"] = "gpt-6-luna"
	raw[samapp.DecisionProviderKey] = decision.ProviderOpenAI
	return raw
}

func (f *fakeJev) sentOpenAIShape() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, hasInput := f.lastBody["input"].(string)
	_, questionsAreAList := f.lastBody["questions"].([]any)
	return hasInput && questionsAreAList
}

func TestOpenAIDecisionNodeRoutesOnTheChosenOption(t *testing.T) {
	jev := &fakeJev{respond: openAIDepartmentAnswer("tech", 0.9)}
	run := newDecisionRun(t, openAIDecisionWorkflow, jev, true)
	run.execute(t)

	run.assertBranches(t, "tech", "billing", "fallback")
	if !jev.sentOpenAIShape() {
		t.Errorf("request = %v, want the OpenAI wire shape", jev.lastBody)
	}
	if out := run.output(t, "triage"); out["model"] != "gpt-6-luna" {
		t.Errorf("triage output model = %v, want the served model", out["model"])
	}
}

func TestOpenAIDecisionNodeEscalatesBelowMinConfidence(t *testing.T) {
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.42)}
	run := newDecisionRun(t, openAIDecisionWorkflow, jev, true)
	run.execute(t)

	run.assertBranches(t, "fallback", "billing", "tech")
}

func TestOpenAIRefusal(t *testing.T) {
	t.Run("fails the node by default", func(t *testing.T) {
		run := newDecisionRun(t, openAIDecisionWorkflow, &fakeJev{respond: openAIRefusal}, true)
		run.execute(t)

		msg := run.errorMessage()
		if !strings.Contains(msg, string(decision.FailureRefused)+" failure") {
			t.Fatalf("workflow error = %q, want a refused failure", msg)
		}
		if strings.Contains(msg, "REFUSAL-CANARY") {
			t.Errorf("workflow error carries the refusal text: %q", msg)
		}
		run.escalationNeverRan(t)
	})
	t.Run("escalates when configured to", func(t *testing.T) {
		run := newDecisionRun(t, openAIEscalatingDecisionWorkflow, &fakeJev{respond: openAIRefusal}, true)
		run.execute(t)

		run.assertBranches(t, "fallback", "billing", "tech")
		out := run.output(t, "triage")
		if out["escalation_reason"] != escalationProviderError || out["provider_error"] != string(decision.FailureRefused) {
			t.Errorf("triage output = %v, want a provider_error escalation of kind refused", out)
		}
	})
}

func TestOpenAINoCreditsFailsTheNodeEvenWhenItEscalates(t *testing.T) {
	jev := &fakeJev{status: http.StatusTooManyRequests, errorBody: `{"error":{"type":"insufficient_quota","code":"credit_balance_exhausted"}}`}
	run := newDecisionRun(t, openAIEscalatingDecisionWorkflow, jev, true)
	run.execute(t)

	if msg := run.errorMessage(); !strings.Contains(msg, string(decision.FailureConfig)+" failure") {
		t.Fatalf("workflow error = %q, want a config failure", msg)
	}
	run.escalationNeverRan(t)
	if jev.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1: an account without credits is not retried", jev.calls.Load())
	}
}

func TestAliasedOpenAIDecisionModel(t *testing.T) {
	t.Run("calls the provider its binding names", func(t *testing.T) {
		jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
		run := newDecisionRun(t, aliasedDecisionWorkflow, jev, true,
			withRegistry(t, nil, "jev", func(url string) map[string]any { return registeredOpenAIDecisionModel(url, "registry-key") }))
		run.execute(t)

		run.assertBranches(t, "billing", "tech", "fallback")
		if !jev.sentOpenAIShape() {
			t.Errorf("request = %v, want the OpenAI wire shape", jev.lastBody)
		}
	})
}

func TestAliasedDecisionEvaluatorRebuildsWhenTheProviderChanges(t *testing.T) {
	jev := &fakeJev{}
	jev.respond = func(map[string]any) string {
		if jev.sentOpenAIShape() {
			return openAIDepartmentAnswer("billing", 0.9)(nil)
		}
		return departmentAnswer("billing", 0.9)(nil)
	}
	var reg *bindings.BindingsRegistry
	jevURL := ""
	run := newDecisionRun(t, aliasedDecisionWorkflow, jev, true,
		withRegistry(t, &reg, "jev", func(url string) map[string]any {
			jevURL = url
			return registeredDecisionModel(url, "key")
		}))

	if _, err := run.e.instance.decisions.Evaluate(t.Context(), decisionRequestFor(run)); err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	if jev.sentOpenAIShape() {
		t.Fatal("a binding without decision_provider must call TypeSafe")
	}
	if err := reg.UpsertModel(t.Context(), "jev", registeredOpenAIDecisionModel(jevURL, "key")); err != nil {
		t.Fatalf("switch provider: %v", err)
	}
	if _, err := run.e.instance.decisions.Evaluate(t.Context(), decisionRequestFor(run)); err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if !jev.sentOpenAIShape() {
		t.Error("after the binding moved to openai the evaluator still spoke TypeSafe")
	}
}

func TestParseDecisionProviderAcceptsOpenAI(t *testing.T) {
	cfg, err := ParseDecisionProvider(map[string]any{"provider": "openai", "api_key": "k"})
	if err != nil {
		t.Fatalf("ParseDecisionProvider: %v", err)
	}
	if cfg.Provider != DecisionProviderOpenAI {
		t.Errorf("provider = %q, want openai", cfg.Provider)
	}
	_, err = ParseDecisionProvider(map[string]any{"provider": "acme"})
	if err == nil || !strings.Contains(err.Error(), "supported: typesafe, google_ai_studio, openai") {
		t.Errorf("err = %v, want the supported providers listed", err)
	}
}

func TestDecisionNodeFlagOffSendsNothingForAnyProvider(t *testing.T) {
	typesafeAlias := withRegistry(t, nil, "jev", func(url string) map[string]any { return registeredDecisionModel(url, "k") })
	openAIAlias := withRegistry(t, nil, "jev", func(url string) map[string]any { return registeredOpenAIDecisionModel(url, "k") })
	geminiAlias := withRegistry(t, nil, "jev", func(url string) map[string]any { return registeredGeminiDecisionModel(url, "k") })
	cases := map[string]struct {
		doc  string
		wire []func(string, *stubServices)
	}{
		"inline typesafe":              {doc: routedDecisionWorkflow},
		"inline openai":                {doc: openAIDecisionWorkflow},
		"inline openai that escalates": {doc: openAIEscalatingDecisionWorkflow},
		"inline gemini":                {doc: geminiDecisionWorkflow},
		"aliased typesafe":             {doc: aliasedDecisionWorkflow, wire: []func(string, *stubServices){typesafeAlias}},
		"aliased openai":               {doc: aliasedEscalatingDecisionWorkflow, wire: []func(string, *stubServices){openAIAlias}},
		"aliased gemini":               {doc: aliasedEscalatingDecisionWorkflow, wire: []func(string, *stubServices){geminiAlias}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
			run := newDecisionRun(t, tc.doc, jev, false, tc.wire...)
			if run.e.instance.decisions != nil {
				t.Fatal("a provider client was built while decision nodes are off")
			}
			run.execute(t)

			msg := run.errorMessage()
			if !strings.Contains(msg, features.KeyWorkflowDecisionNode) || !strings.Contains(msg, features.EnvVarName(features.KeyWorkflowDecisionNode)) {
				t.Fatalf("workflow error = %q, want it to name the flag and its variable", msg)
			}
			run.escalationNeverRan(t)
			if jev.calls.Load() != 0 {
				t.Errorf("the provider was called %d times with decision nodes off", jev.calls.Load())
			}
		})
	}
}
