// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package modelfamily

import (
	"slices"
	"strings"
)

const (
	ClaudeFable  = "claude-fable"
	ClaudeHaiku  = "claude-haiku"
	ClaudeMythos = "claude-mythos"
	ClaudeOpus   = "claude-opus"
	ClaudeSonnet = "claude-sonnet"
	GeminiFlash  = "gemini-flash"
	GeminiPro    = "gemini-pro"
	GPT35        = "gpt-3.5"
	GPT4         = "gpt-4"
	GPT41        = "gpt-4.1"
	GPT4o        = "gpt-4o"
	GPT5         = "gpt-5"
	Llama        = "llama"
	Mistral      = "mistral"
	Other        = "other"
)

var Families = []string{
	ClaudeFable,
	ClaudeHaiku,
	ClaudeMythos,
	ClaudeOpus,
	ClaudeSonnet,
	GeminiFlash,
	GeminiPro,
	GPT35,
	GPT4,
	GPT41,
	GPT4o,
	GPT5,
	Llama,
	Mistral,
	Other,
}

var ClaudeTiers = []string{"sonnet", "opus", "haiku", "fable", "mythos"}

type rule struct {
	family  string
	markers []string
}

var rules = slices.Concat(
	gptRulesMostSpecificFirst(),
	claudeRules(),
	[]rule{
		{GeminiPro, []string{"gemini", "pro"}},
		{GeminiFlash, []string{"gemini", "flash"}},
		{Llama, []string{"llama"}},
		{Mistral, []string{"mistral"}},
	},
)

func gptRulesMostSpecificFirst() []rule {
	return []rule{
		{GPT4o, []string{"gpt-4o"}},
		{GPT41, []string{"gpt-4.1"}},
		{GPT35, []string{"gpt-3.5"}},
		{GPT5, []string{"gpt-5"}},
		{GPT4, []string{"gpt-4"}},
	}
}

func claudeRules() []rule {
	out := make([]rule, 0, len(ClaudeTiers))
	for _, tier := range ClaudeTiers {
		out = append(out, rule{"claude-" + tier, []string{"claude", tier}})
	}
	return out
}

func Of(modelID string) string {
	id := strings.ToLower(modelID)
	for _, rule := range rules {
		if matchesAllMarkers(id, rule.markers) {
			return rule.family
		}
	}
	return Other
}

func matchesAllMarkers(id string, markers []string) bool {
	for _, marker := range markers {
		if !strings.Contains(id, marker) {
			return false
		}
	}
	return true
}
