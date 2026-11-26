// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package modelfamily

import (
	"strings"
	"testing"
)

func TestModelFamilyIsAlwaysDrawnFromTheClosedSet(t *testing.T) {
	families := make(map[string]bool, len(Families))
	for _, f := range Families {
		families[f] = true
	}
	for _, id := range []string{
		"", "gpt-4o", "GPT-4O", "openai/gpt-4o-mini", "gpt-4.1-2025-04-14", "gpt-3.5-turbo-16k",
		"gpt-5.6-sol", "anthropic.claude-3-5-sonnet-20241022-v2:0", "claude-sonnet-4-6",
		"claude-opus-5", "claude-haiku-4-5", "claude-fable-5", "claude-mythos-5",
		"gemini-1.5-pro", "openrouter/google/gemini-3.6-flash", "llama-3.3-70b-versatile",
		"mistral-small-latest", "acme legal triage v3", "sk-live-0123456789",
		"john.doe@acme.com", strings.Repeat("x", 4096),
	} {
		if got := Of(id); !families[got] {
			t.Errorf("Of(%q) = %q, which is not in Families", id, got)
		}
	}
}

func TestModelFamilyResolvesOverlappingMarkers(t *testing.T) {
	cases := map[string]string{
		"gpt-4o-mini":               GPT4o,
		"gpt-4.1":                   GPT41,
		"gpt-4-turbo":               GPT4,
		"gpt-4":                     GPT4,
		"gpt-3.5-turbo":             GPT35,
		"gpt-5.6":                   GPT5,
		"claude-3-5-sonnet-2024102": ClaudeSonnet,
		"claude-sonnet-4-5":         ClaudeSonnet,
		"claude-3-5-haiku":          ClaudeHaiku,
		"gemini-1.5-pro":            GeminiPro,
		"gemini-3.1-flash-lite":     GeminiFlash,
	}
	for id, want := range cases {
		if got := Of(id); got != want {
			t.Errorf("Of(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestModelFamilyClassifiesAnOperatorAuthoredIdentifier(t *testing.T) {
	cases := map[string]string{
		"ft:gpt-4o:acme-corp:custom:9xYz": GPT4o,
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/anthropic.claude-sonnet-4-5": ClaudeSonnet,
		"acme-prod-westeurope-deployment": Other,
	}
	for id, want := range cases {
		if got := Of(id); got != want {
			t.Errorf("Of(%q) = %q, want %q", id, got, want)
		}
	}
}
