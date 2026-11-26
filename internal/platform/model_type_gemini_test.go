// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestResolveModelTypeServesBothTypesOnGoogleAIStudio(t *testing.T) {
	cases := map[string]string{
		"":                      store.ModelTypeChat,
		store.ModelTypeChat:     store.ModelTypeChat,
		store.ModelTypeDecision: store.ModelTypeDecision,
	}
	for requested, want := range cases {
		got, err := ResolveModelType(requested, ProviderGoogleAIStudio)
		if err != nil || got != want {
			t.Errorf("ResolveModelType(%q, google_ai_studio) = %q, %v; want %q", requested, got, err, want)
		}
	}
}

func TestToRawLiteLLMConfigKeepsADecisionModelsBareName(t *testing.T) {
	decisionModel := &store.ModelConfiguration{Provider: ProviderGoogleAIStudio, ModelName: "gemini-decision-1",
		ModelType: store.ModelTypeDecision, ModelAuthConfig: `{"type":"apikey","api_key":"k"}`}
	if got := ToRawLiteLLMConfig(decisionModel)["model"]; got != "gemini-decision-1" {
		t.Errorf("decision model = %v, want gemini-decision-1", got)
	}
	chatModel := &store.ModelConfiguration{Provider: ProviderGoogleAIStudio, ModelName: "gemini-2.5-pro",
		ModelType: store.ModelTypeChat, ModelAuthConfig: `{"type":"apikey","api_key":"k"}`}
	if got := ToRawLiteLLMConfig(chatModel)["model"]; got != "gemini/gemini-2.5-pro" {
		t.Errorf("chat model = %v, want gemini/gemini-2.5-pro", got)
	}
}
