// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package modelinfo

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
)

func TestMaxInputTokens(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		want    int
		wantOK  bool
	}{
		{name: "openai gpt-4o exact", modelID: "gpt-4o", want: 128_000, wantOK: true},
		{name: "openai gpt-3.5-turbo exact", modelID: "gpt-3.5-turbo", want: 16_385, wantOK: true},
		{name: "anthropic claude opus 4-7 exact", modelID: "claude-opus-4-7", want: 1_000_000, wantOK: true},
		{name: "anthropic claude sonnet 4-6 exact", modelID: "claude-sonnet-4-6", want: 1_000_000, wantOK: true},
		{name: "google gemini 1.5 pro exact", modelID: "gemini-1.5-pro", want: 2_097_152, wantOK: true},
		{name: "google gemini 3.5 flash exact", modelID: "gemini-3.5-flash", want: 1_048_576, wantOK: true},
		{name: "google gemini 3.6 flash exact", modelID: "gemini-3.6-flash", want: 1_048_576, wantOK: true},
		{name: "meta llama 3.1 70b exact", modelID: "llama-3.1-70b", want: 128_000, wantOK: true},
		{name: "mistral large exact", modelID: "mistral-large", want: 128_000, wantOK: true},

		{name: "openai/ prefix stripped", modelID: "openai/gpt-4o", want: 128_000, wantOK: true},
		{name: "anthropic/ prefix stripped", modelID: "anthropic/claude-opus-4-7", want: 1_000_000, wantOK: true},
		{name: "vertex_ai/ prefix stripped", modelID: "vertex_ai/gemini-1.5-pro", want: 2_097_152, wantOK: true},
		{name: "vertex_ai/ prefix stripped gemini 3.5", modelID: "vertex_ai/gemini-3.5-flash", want: 1_048_576, wantOK: true},
		{name: "openai/ prefix stripped gemini 3.6", modelID: "openai/gemini-3.6-flash", want: 1_048_576, wantOK: true},
		{name: "openrouter vendor-qualified id", modelID: "openrouter/google/gemini-3.6-flash", want: 1_048_576, wantOK: true},
		{name: "openrouter vendor prefix without provider", modelID: "google/gemini-3.5-flash", want: 1_048_576, wantOK: true},
		{name: "gemini native models/ form", modelID: "models/gemini-3.5-flash", want: 1_048_576, wantOK: true},

		{name: "claude-3-5-sonnet dated variant", modelID: "claude-3-5-sonnet-20241022", want: 200_000, wantOK: true},
		{name: "claude-opus-4-7 with subversion", modelID: "claude-opus-4-7-20260101", want: 1_000_000, wantOK: true},
		{name: "gpt-4o-2024-08-06", modelID: "gpt-4o-2024-08-06", want: 128_000, wantOK: true},
		{name: "gemini 2.0 flash dated", modelID: "gemini-2.0-flash-001", want: 1_048_576, wantOK: true},
		{name: "gemini 3 flash preview suffix", modelID: "gemini-3-flash-preview", want: 1_048_576, wantOK: true},
		{name: "gemini 3.1 pro preview suffix", modelID: "gemini-3.1-pro-preview", want: 1_048_576, wantOK: true},
		{name: "gemini 3.5 flash-lite falls back to flash family", modelID: "gemini-3.5-flash-lite", want: 1_048_576, wantOK: true},
		{name: "gemini 3.1 flash-lite dated variant", modelID: "gemini-3.1-flash-lite-001", want: 1_048_576, wantOK: true},
		{name: "gemini 3.1 flash-lite preview suffix", modelID: "gemini-3.1-flash-lite-preview", want: 1_048_576, wantOK: true},

		{name: "gpt-4-turbo not gpt-4", modelID: "gpt-4-turbo-2024-04-09", want: 128_000, wantOK: true},
		{name: "gpt-3.5-turbo-16k not gpt-3.5-turbo", modelID: "gpt-3.5-turbo-16k-0613", want: 16_385, wantOK: true},

		{name: "gpt-4.1 not gpt-4", modelID: "gpt-4.1", want: 1_047_576, wantOK: true},
		{name: "gpt-4.1 dated not gpt-4", modelID: "gpt-4.1-2025-04-14", want: 1_047_576, wantOK: true},
		{name: "gpt-5.6 sol", modelID: "gpt-5.6-sol", want: 922_000, wantOK: true},
		{name: "gpt-5.6 terra", modelID: "gpt-5.6-terra", want: 922_000, wantOK: true},
		{name: "gpt-5.6 luna", modelID: "gpt-5.6-luna", want: 922_000, wantOK: true},
		{name: "gpt-5.5 not gpt-5", modelID: "gpt-5.5-2026-04-23", want: 922_000, wantOK: true},
		{name: "gpt-5.4 not gpt-5", modelID: "gpt-5.4", want: 922_000, wantOK: true},
		{name: "gpt-5 mini via gpt-5 family", modelID: "gpt-5-mini-2025-08-07", want: 272_000, wantOK: true},
		{name: "claude opus 4.7 not opus 4", modelID: "claude-opus-4-7", want: 1_000_000, wantOK: true},
		{name: "claude opus 4.8 not opus 4", modelID: "claude-opus-4-8", want: 1_000_000, wantOK: true},
		{name: "claude opus 4.6 not opus 4", modelID: "claude-opus-4-6", want: 1_000_000, wantOK: true},
		{name: "claude opus 4.0 dated stays 200k", modelID: "claude-opus-4-20250514", want: 200_000, wantOK: true},
		{name: "claude opus 5", modelID: "claude-opus-5", want: 1_000_000, wantOK: true},
		{name: "claude sonnet 5", modelID: "claude-sonnet-5", want: 1_000_000, wantOK: true},
		{name: "claude fable 5", modelID: "claude-fable-5", want: 1_000_000, wantOK: true},
		{name: "anthropic/ prefix stripped opus 5", modelID: "anthropic/claude-opus-5", want: 1_000_000, wantOK: true},
		{name: "claude mythos 5", modelID: "claude-mythos-5", want: 1_000_000, wantOK: true},

		{name: "gpt-5 chat-latest is not the gpt-5 family", modelID: "gpt-5-chat-latest", want: 128_000, wantOK: true},
		{name: "gpt-5.1 chat-latest is not the gpt-5.1 family", modelID: "gpt-5.1-chat-latest", want: 128_000, wantOK: true},
		{name: "gpt-5.2 chat-latest is not the gpt-5.2 family", modelID: "gpt-5.2-chat-latest", want: 128_000, wantOK: true},

		{name: "empty string", modelID: "", want: 0, wantOK: false},
		{name: "unknown model", modelID: "totally-fake-model-xyz", want: 0, wantOK: false},
		{name: "unknown provider prefix", modelID: "fakeprovider/whatever", want: 0, wantOK: false},
		{name: "near-miss with claude prefix", modelID: "claude-2", want: 0, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MaxInputTokens(tt.modelID)
			if ok != tt.wantOK {
				t.Fatalf("MaxInputTokens(%q) ok = %v, want %v", tt.modelID, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("MaxInputTokens(%q) = %d, want %d", tt.modelID, got, tt.want)
			}
		})
	}
}

func TestImageOutputVariantsAreUnknown(t *testing.T) {
	families := []string{
		"gemini-2.0-flash", "gemini-2.5-flash", "gemini-2.5-pro",
		"gemini-3-flash", "gemini-3-pro", "gemini-3.1-flash",
		"gemini-3.1-flash-lite", "gemini-3.1-pro", "gemini-3.5-flash",
		"gemini-3.5-flash-lite", "gemini-3.6-flash",
	}
	suffixes := []string{"-image", "-image-preview", "-preview-image-generation"}

	for _, family := range families {
		for _, suffix := range suffixes {
			id := family + suffix
			t.Run(id, func(t *testing.T) {
				if got, ok := MaxInputTokens(id); ok {
					t.Errorf("MaxInputTokens(%q) = %d, want unknown: image-output "+
						"variants have 32K-131K windows, so reporting the chat "+
						"sibling's window overstates it by up to 32x", id, got)
				}
			})
			qualified := "openrouter/google/" + id
			t.Run(qualified, func(t *testing.T) {
				if got, ok := MaxInputTokens(qualified); ok {
					t.Errorf("MaxInputTokens(%q) = %d, want unknown", qualified, got)
				}
			})
		}
	}
}

func TestOpenAIInputCapsAreReserved(t *testing.T) {
	cases := []struct {
		id            string
		contextWindow int
		wantInput     int
	}{
		{"gpt-5", 400_000, 272_000},
		{"gpt-5-mini", 400_000, 272_000},
		{"gpt-5-nano", 400_000, 272_000},
		{"gpt-5.1", 400_000, 272_000},
		{"gpt-5.2", 400_000, 272_000},
		{"gpt-5.4", 1_050_000, 922_000},
		{"gpt-5.5", 1_050_000, 922_000},
		{"gpt-5.6-sol", 1_050_000, 922_000},
		{"gpt-5.6-terra", 1_050_000, 922_000},
		{"gpt-5.6-luna", 1_050_000, 922_000},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got, ok := MaxInputTokens(tc.id)
			if !ok {
				t.Fatalf("MaxInputTokens(%q) = unknown, want %d", tc.id, tc.wantInput)
			}
			if got == tc.contextWindow {
				t.Fatalf("MaxInputTokens(%q) = %d, the full context window; OpenAI caps "+
					"input at %d and reserves the rest for output", tc.id, got, tc.wantInput)
			}
			if got != tc.wantInput {
				t.Fatalf("MaxInputTokens(%q) = %d, want %d", tc.id, got, tc.wantInput)
			}
		})
	}
}

func TestStripProviderPrefix(t *testing.T) {
	cases := map[string]string{
		"openai/gpt-4o":                      "gpt-4o",
		"anthropic/claude-opus-4-7":          "claude-opus-4-7",
		"vertex_ai/gemini-1.5-pro":           "gemini-1.5-pro",
		"gpt-4o":                             "gpt-4o",
		"":                                   "",
		"openrouter/google/gemini-3.6-flash": "gemini-3.6-flash",
		"google/gemini-3.5-flash":            "gemini-3.5-flash",
		"models/gemini-3.5-flash":            "gemini-3.5-flash",
		"openai/gpt-4o/":                     "openai/gpt-4o/",
	}
	for in, want := range cases {
		if got := stripProviderPrefix(in); got != want {
			t.Errorf("stripProviderPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaxOutputTokens(t *testing.T) {
	tests := []struct {
		modelID string
		want    int
		wantOK  bool
	}{
		{modelID: "claude-sonnet-5-5", want: 128_000, wantOK: true},
		{modelID: "anthropic/claude-sonnet-5-5", want: 128_000, wantOK: true},
		{modelID: "claude-sonnet-5", want: 128_000, wantOK: true},
		{modelID: "claude-opus-5-5", want: 128_000, wantOK: true},
		{modelID: "claude-haiku-5-5", want: 128_000, wantOK: true},
		{modelID: "claude-fable-5-1", want: 128_000, wantOK: true},
		{modelID: "claude-mythos-5-1", want: 128_000, wantOK: true},
		{modelID: "us.anthropic.claude-sonnet-5-5-v1:0", want: 128_000, wantOK: true},
		{modelID: "global.anthropic.claude-opus-5-5", want: 128_000, wantOK: true},
		{modelID: "claude-sonnet-5-5@20260901", want: 128_000, wantOK: true},

		{modelID: "claude-sonnet-5-5-thinking", want: 128_000, wantOK: true},
		{modelID: "claude-sonnet-5.5", want: 128_000, wantOK: true},
		{modelID: "claude-sonnet-5@20260901", want: 128_000, wantOK: true},
		{modelID: "anthropic.claude-sonnet-5:0", want: 128_000, wantOK: true},
		{modelID: "Claude-Sonnet-5-5", wantOK: false},

		{modelID: "claude-sonnet-50", wantOK: false},
		{modelID: "claude-sonnet-5x", wantOK: false},
		{modelID: "claude-sonnet-4-6", wantOK: false},
		{modelID: "claude-sonnet-4-5", wantOK: false},
		{modelID: "claude-3-5-sonnet-20241022", wantOK: false},
		{modelID: "gpt-5.5", wantOK: false},
		{modelID: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got, ok := MaxOutputTokens(tt.modelID)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("MaxOutputTokens(%q) = (%d, %v), want (%d, %v)", tt.modelID, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestModelFamiliesCoverTheCatalog(t *testing.T) {
	for _, table := range []map[string]int{models, prefixModels} {
		for id := range table {
			if got := modelfamily.Of(id); got == modelfamily.Other {
				t.Errorf("model %q resolves to %q; add a family for it in internal/modelfamily",
					id, modelfamily.Other)
			}
		}
	}
}
