// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"strings"
	"testing"
)

func TestMigrateModel_StructuredEntry(t *testing.T) {
	legacy := map[string]any{
		"model":            "claude-opus-4-7",
		"api_base":         "${ANTHROPIC_API_BASE}",
		"api_key":          "${ANTHROPIC_API_KEY}",
		"max_tokens":       8192,
		"max_input_tokens": 200000,
		"temperature":      0.2,
	}
	clean, warnings, err := MigrateModel(legacy, "planning")
	if err != nil {
		t.Fatalf("MigrateModel: %v", err)
	}
	if clean.Name != "planning" || clean.Kind != "model" {
		t.Errorf("identity = %s/%s", clean.Kind, clean.Name)
	}
	if clean.Spec["modelName"] != "claude-opus-4-7" {
		t.Errorf("modelName = %v", clean.Spec["modelName"])
	}
	if clean.Spec["provider"] != "anthropic" {
		t.Errorf("provider = %v; want anthropic", clean.Spec["provider"])
	}
	if clean.Spec["apiBase"] != "${ANTHROPIC_API_BASE}" {
		t.Errorf("apiBase = %v", clean.Spec["apiBase"])
	}
	auth, _ := clean.Spec["authConfig"].(map[string]any)
	if auth["apiKey"] != "${ANTHROPIC_API_KEY}" {
		t.Errorf("authConfig.apiKey = %v", auth)
	}
	params, _ := clean.Spec["modelParams"].(map[string]any)
	if params["max_tokens"] != 8192 {
		t.Errorf("modelParams.max_tokens = %v", params["max_tokens"])
	}
	if params["temperature"] != 0.2 {
		t.Errorf("modelParams.temperature = %v", params["temperature"])
	}
	if clean.Spec["maxInputTokens"] != int64(200000) {
		t.Errorf("maxInputTokens = %v (%T)", clean.Spec["maxInputTokens"], clean.Spec["maxInputTokens"])
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings; got %v", warnings)
	}
}

func TestMigrateModel_BareStringWarns(t *testing.T) {
	clean, warnings, err := MigrateModel("gemini-2.5-flash", "multimodal")
	if err != nil {
		t.Fatalf("MigrateModel: %v", err)
	}
	if clean.Spec["modelName"] != "gemini-2.5-flash" {
		t.Errorf("modelName = %v", clean.Spec["modelName"])
	}
	if clean.Spec["provider"] != "google" {
		t.Errorf("provider = %v; want google", clean.Spec["provider"])
	}
	if len(warnings) == 0 {
		t.Errorf("expected a warning for bare-string model")
	}
}

func TestMigrateModel_UnknownProviderWarns(t *testing.T) {
	legacy := map[string]any{
		"model":   "${SOMETHING_OBSCURE}",
		"api_key": "x",
	}
	_, warnings, err := MigrateModel(legacy, "obscure")
	if err != nil {
		t.Fatalf("MigrateModel: %v", err)
	}
	hasUnknown := false
	for _, w := range warnings {
		if strings.Contains(w, "could not infer provider") {
			hasUnknown = true
		}
	}
	if !hasUnknown {
		t.Errorf("expected unknown-provider warning; got %v", warnings)
	}
}

func TestExtractModelsFromSharedConfig(t *testing.T) {
	doc := parseDoc(t, `
shared_config:
  - broker_connection:
      broker_url: ws://x
  - models:
      planning:
        model: claude-opus-4-7
        api_base: ${A}
        api_key: ${K}
      bare:
        # Unsupported shape — list (not map).
        - foo
      general:
        model: gpt-4o
        api_key: ${OPENAI}
  - services:
      session_service: { type: memory }
`)
	docs, _, skipped := ExtractModelsFromSharedConfig(doc)
	if len(docs) != 2 {
		t.Fatalf("got %d migrated; want 2", len(docs))
	}
	if len(skipped) != 1 || skipped["bare"] == "" {
		t.Errorf("expected 1 skipped (bare); got %v", skipped)
	}
	gotProviders := map[string]string{}
	for _, d := range docs {
		gotProviders[d.Name], _ = d.Spec["provider"].(string)
	}
	if gotProviders["planning"] != "anthropic" || gotProviders["general"] != "openai" {
		t.Errorf("providers = %v", gotProviders)
	}
}
