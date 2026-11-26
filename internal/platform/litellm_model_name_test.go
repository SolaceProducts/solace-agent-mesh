// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestResolveLiteLLMModelName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{"vertex_ai adds prefix to bare Claude model", "vertex_ai", "claude-3-haiku@20240307", "vertex_ai/claude-3-haiku@20240307"},
		{"vertex_ai adds prefix to bare Gemini model", "vertex_ai", "gemini-1.5-pro", "vertex_ai/gemini-1.5-pro"},
		{"vertex_ai leaves prefixed name alone", "vertex_ai", "vertex_ai/gemini-1.5-pro", "vertex_ai/gemini-1.5-pro"},
		{"vertex_ai leaves any other slash-prefixed name alone", "vertex_ai", "anthropic/claude-3-haiku@20240307", "anthropic/claude-3-haiku@20240307"},
		{"bedrock adds prefix", "bedrock", "anthropic.claude-3-5-sonnet-20241022-v2:0", "bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0"},
		{"ollama adds prefix", "ollama", "llama2", "ollama/llama2"},
		{"azure_openai adds prefix", "azure_openai", "gpt-4o", "azure/gpt-4o"},
		{"google_ai_studio adds gemini prefix", "google_ai_studio", "gemini-1.5-pro", "gemini/gemini-1.5-pro"},
		{"openai passes through (no prefix needed)", "openai", "gpt-4o", "gpt-4o"},
		{"anthropic passes through (name-inference route)", "anthropic", "claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"custom passes through (FE handled at save)", "custom", "openai/my-model", "openai/my-model"},
		{"empty provider returns model unchanged", "", "some-model", "some-model"},
		{"empty model returns empty", "vertex_ai", "", ""},
		{"unknown provider passes through", "made_up", "some-model", "some-model"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveLiteLLMModelName(tc.provider, "", tc.model); got != tc.want {
				t.Errorf("ResolveLiteLLMModelName(%q, \"\", %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
			}
		})
	}
}

func TestResolveLiteLLMModelNameLeavesDecisionModelsBare(t *testing.T) {
	t.Parallel()
	if got := ResolveLiteLLMModelName("google_ai_studio", store.ModelTypeDecision, "gemini-decision-1"); got != "gemini-decision-1" {
		t.Errorf("decision model = %q, want gemini-decision-1", got)
	}
	if got := ResolveLiteLLMModelName("google_ai_studio", store.ModelTypeChat, "gemini-2.5-pro"); got != "gemini/gemini-2.5-pro" {
		t.Errorf("chat model = %q, want gemini/gemini-2.5-pro", got)
	}
}

func TestAnUnprefixedCustomModelKeepsTheAPIItsNameSelects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ model, wantProvider string }{
		{"claude-haiku-4-5-20251001", "anthropic"},
		{"bedrock-claude-4-5-haiku", "anthropic"},
		{"gemini-2.5-pro", "gemini"},
		{"gpt-4o", "openai"},
		{"meta-llama/Llama-3.3-70B-Instruct", "openai"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			stored := &store.ModelConfiguration{ID: "model-1", Provider: "custom", ModelType: store.ModelTypeChat, ModelName: tc.model, ModelAuthConfig: "{}", ModelParams: "{}"}
			called, err := samapp.ParseModelConfig(ToRawLiteLLMConfig(stored), nil)
			if err != nil {
				t.Fatal(err)
			}
			if called.Provider != tc.wantProvider || called.Model != tc.model {
				t.Errorf("custom model %q is called as %s/%s, want %s/%s", tc.model, called.Provider, called.Model, tc.wantProvider, tc.model)
			}
		})
	}
}
