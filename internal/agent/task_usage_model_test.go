// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func runReportingModel(t *testing.T, modelID string) map[string]any {
	t.Helper()
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "done", FinishReason: "stop"})
	agent.cfg.Model = modelID
	agent.cfg.OriginalModel = modelID

	resp, err := agent.HandleTask(context.Background(), "task-model",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	return finalTaskOf(t, resp).Metadata
}

func TestFinishedTaskReportsTheModelFamilyAndProvider(t *testing.T) {
	metadata := runReportingModel(t, "anthropic/claude-sonnet-4-5")

	if got := metadata[a2a.MetadataModelFamily]; got != modelfamily.ClaudeSonnet {
		t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, got, modelfamily.ClaudeSonnet)
	}
	if got := metadata[a2a.MetadataModelProvider]; got != "anthropic" {
		t.Errorf("%s = %v, want %q", a2a.MetadataModelProvider, got, "anthropic")
	}
}

func TestTerminalMetadataCarriesNoModelIdentifier(t *testing.T) {
	cases := map[string]string{
		"ft:gpt-4o:acme-corp:legal-triage:AbCd1234":                                            modelfamily.GPT4o,
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/anthropic.claude-sonnet-4-5": modelfamily.ClaudeSonnet,
		"acme-prod-westeurope-deployment":                                                      modelfamily.Other,
	}
	for modelID, wantFamily := range cases {
		t.Run(modelID, func(t *testing.T) {
			metadata := runReportingModel(t, modelID)

			rendered, err := json.Marshal(metadata)
			if err != nil {
				t.Fatalf("marshal metadata: %v", err)
			}
			for _, secret := range []string{
				modelID, "acme-corp", "legal-triage", "AbCd1234", "123456789012",
				"us-east-1", "westeurope",
			} {
				if strings.Contains(string(rendered), secret) {
					t.Errorf("%q reached the terminal metadata %s", secret, rendered)
				}
			}
			if got := metadata[a2a.MetadataModelFamily]; got != wantFamily {
				t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, got, wantFamily)
			}
		})
	}
}

func TestErrorResponseCarriesTheModel(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.usageFacts.recordModel("gpt-4o-mini", "openai", "")

	resp := errorResponseWithUsage(state, -32603, "The AI model is currently rate limited.")
	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("Error.Data = %#v, want a map", resp.Error.Data)
	}
	if got := data[a2a.MetadataModelFamily]; got != modelfamily.GPT4o {
		t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, got, modelfamily.GPT4o)
	}
	if got := data[a2a.MetadataModelProvider]; got != "openai" {
		t.Errorf("%s = %v, want %q", a2a.MetadataModelProvider, got, "openai")
	}
}

func TestModelOverrideIsWhatTheRunReports(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.ModelOverride = &llm.BifrostConfig{Model: "gemini-2.5-flash", Provider: "vertex"}

	modelID, provider, apiBase := callModel(state, "anthropic/claude-sonnet-4-5", "")
	if modelID != "gemini-2.5-flash" || provider != "vertex" {
		t.Fatalf("callModel = (%q, %q), want the override", modelID, provider)
	}

	state.usageFacts.recordModel(modelID, provider, apiBase)
	metadata := map[string]any{}
	state.usageFacts.AddTo(metadata)
	if got := metadata[a2a.MetadataModelFamily]; got != modelfamily.GeminiFlash {
		t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, got, modelfamily.GeminiFlash)
	}
}

func TestAnUnresolvedModelReportsNoProvider(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)

	modelID, provider, apiBase := callModel(state, "", "")
	if modelID != "" || provider != "" {
		t.Fatalf("callModel = (%q, %q), want neither", modelID, provider)
	}

	state.usageFacts.recordModel(modelID, provider, apiBase)
	metadata := map[string]any{}
	state.usageFacts.AddTo(metadata)
	if len(metadata) != 0 {
		t.Errorf("metadata = %#v, want nothing reported", metadata)
	}
}

func TestUsageFactModelVocabulariesMatchTheCatalog(t *testing.T) {
	for _, provider := range llm.ProviderLabels() {
		if !slices.Contains(taxonomy.ProviderLabels, provider) {
			t.Errorf("recordModel can write provider %q, which the catalog does not approve", provider)
		}
	}
	for _, modelID := range []string{
		"gpt-4o", "openai/gpt-4.1-2025-04-14", "anthropic.claude-3-5-sonnet-20241022-v2:0",
		"ft:gpt-4o:acme-corp:legal-triage:AbCd1234", "acme-internal-vllm-cluster",
		"gemini-1.5-pro", "llama-3.3-70b-versatile", "mistral-small-latest",
	} {
		for _, provider := range []string{"openai", "acme-internal", ""} {
			var facts taskUsageFacts
			facts.recordModel(modelID, provider, "")
			metadata := map[string]any{}
			facts.AddTo(metadata)
			family, _ := metadata[a2a.MetadataModelFamily].(string)
			if !slices.Contains(modelfamily.Families, family) {
				t.Errorf("recordModel(%q) wrote family %q, which the catalog does not approve", modelID, family)
			}
			label, wrote := metadata[a2a.MetadataModelProvider].(string)
			if wrote && !slices.Contains(taxonomy.ProviderLabels, label) {
				t.Errorf("recordModel(_, %q) wrote provider %q, which the catalog does not approve", provider, label)
			}
		}
	}
}
