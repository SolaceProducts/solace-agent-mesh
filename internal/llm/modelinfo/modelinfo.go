// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package modelinfo

import (
	"regexp"
	"strings"
)

func MaxInputTokens(modelID string) (int, bool) {
	if modelID == "" {
		return 0, false
	}

	if v, ok := models[modelID]; ok {
		return v, true
	}

	bare := stripProviderPrefix(modelID)
	if bare != modelID {
		if v, ok := models[bare]; ok {
			return v, true
		}
	}

	if isImageOutputVariant(bare) {
		return 0, false
	}

	if v, ok := longestPrefixMatch(bare); ok {
		return v, true
	}
	return 0, false
}

func isImageOutputVariant(id string) bool {
	return strings.Contains(id, "-image")
}

func stripProviderPrefix(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

func longestPrefixMatch(id string) (int, bool) {
	bestLen := 0
	bestVal := 0
	found := false
	for prefix, val := range prefixModels {
		if len(prefix) <= bestLen {
			continue
		}
		if strings.HasPrefix(id, prefix) {
			bestLen = len(prefix)
			bestVal = val
			found = true
		}
	}
	return bestVal, found
}

var models = map[string]int{
	"gpt-3.5-turbo":       16_385,
	"gpt-3.5-turbo-16k":   16_385,
	"gpt-4":               8_192,
	"gpt-4-32k":           32_768,
	"gpt-4-turbo":         128_000,
	"gpt-4-turbo-preview": 128_000,
	"gpt-4.1":             1_047_576,
	"gpt-4o":              128_000,
	"gpt-4o-mini":         128_000,
	"gpt-5":               272_000,
	"gpt-5.1":             272_000,
	"gpt-5.2":             272_000,
	"gpt-5.4":             922_000,
	"gpt-5.5":             922_000,
	"gpt-5.6":             922_000,
	"gpt-5-chat-latest":   128_000,
	"gpt-5.1-chat-latest": 128_000,
	"gpt-5.2-chat-latest": 128_000,

	"claude-3-5-haiku":  200_000,
	"claude-3-5-sonnet": 200_000,
	"claude-3-7-sonnet": 200_000,
	"claude-opus-4":     200_000,
	"claude-opus-4-5":   200_000,
	"claude-opus-4-6":   1_000_000,
	"claude-opus-4-7":   1_000_000,
	"claude-opus-4-8":   1_000_000,
	"claude-opus-5":     1_000_000,
	"claude-sonnet-4":   200_000,
	"claude-sonnet-4-5": 200_000,
	"claude-sonnet-4-6": 1_000_000,
	"claude-sonnet-5":   1_000_000,
	"claude-haiku-4-5":  200_000,
	"claude-fable-5":    1_000_000,
	"claude-mythos-5":   1_000_000,

	"gemini-1.5-flash":      1_048_576,
	"gemini-1.5-pro":        2_097_152,
	"gemini-2.0-flash":      1_048_576,
	"gemini-2.5-flash":      1_048_576,
	"gemini-2.5-pro":        1_048_576,
	"gemini-3-flash":        1_048_576,
	"gemini-3-pro":          1_048_576,
	"gemini-3.1-flash-lite": 1_048_576,
	"gemini-3.1-pro":        1_048_576,
	"gemini-3.5-flash":      1_048_576,
	"gemini-3.6-flash":      1_048_576,

	"llama-3.1-8b":   128_000,
	"llama-3.1-70b":  128_000,
	"llama-3.1-405b": 128_000,
	"llama-3.3-70b":  128_000,

	"mistral-large": 128_000,
	"mistral-small": 32_768,
}

var prefixModels = map[string]int{
	"gpt-3.5-turbo-16k":   16_385,
	"gpt-3.5-turbo":       16_385,
	"gpt-4-turbo":         128_000,
	"gpt-4-32k":           32_768,
	"gpt-4.1":             1_047_576,
	"gpt-4o-mini":         128_000,
	"gpt-4o":              128_000,
	"gpt-4":               8_192,
	"gpt-5.1-chat-latest": 128_000,
	"gpt-5.2-chat-latest": 128_000,
	"gpt-5-chat-latest":   128_000,
	"gpt-5.1":             272_000,
	"gpt-5.2":             272_000,
	"gpt-5.4":             922_000,
	"gpt-5.5":             922_000,
	"gpt-5.6":             922_000,
	"gpt-5":               272_000,

	"claude-3-5-haiku":  200_000,
	"claude-3-5-sonnet": 200_000,
	"claude-3-7-sonnet": 200_000,
	"claude-opus-4-5":   200_000,
	"claude-opus-4-6":   1_000_000,
	"claude-opus-4-7":   1_000_000,
	"claude-opus-4-8":   1_000_000,
	"claude-opus-4":     200_000,
	"claude-opus-5":     1_000_000,
	"claude-sonnet-4-5": 200_000,
	"claude-sonnet-4-6": 1_000_000,
	"claude-sonnet-4":   200_000,
	"claude-sonnet-5":   1_000_000,
	"claude-haiku-4-5":  200_000,
	"claude-fable-5":    1_000_000,
	"claude-mythos-5":   1_000_000,

	"gemini-1.5-flash":      1_048_576,
	"gemini-1.5-pro":        2_097_152,
	"gemini-2.0-flash":      1_048_576,
	"gemini-2.5-flash":      1_048_576,
	"gemini-2.5-pro":        1_048_576,
	"gemini-3-flash":        1_048_576,
	"gemini-3-pro":          1_048_576,
	"gemini-3.1-flash-lite": 1_048_576,
	"gemini-3.1-pro":        1_048_576,
	"gemini-3.5-flash":      1_048_576,
	"gemini-3.6-flash":      1_048_576,

	"llama-3.1-405b": 128_000,
	"llama-3.1-70b":  128_000,
	"llama-3.1-8b":   128_000,
	"llama-3.3-70b":  128_000,

	"mistral-large": 128_000,
	"mistral-small": 32_768,
}

func MaxOutputTokens(modelID string) (int, bool) {
	i := strings.Index(modelID, claudeModelMarker)
	if i < 0 {
		return 0, false
	}
	if !claudeGeneration5Family.MatchString(modelID[i:]) {
		return 0, false
	}
	return claudeGeneration5MaxOutputTokens, true
}

const (
	claudeModelMarker                = "claude-"
	claudeGeneration5MaxOutputTokens = 128_000
)

var claudeGeneration5Family = regexp.MustCompile(`^claude-(?:sonnet|opus|haiku|fable|mythos)-5(?:[-@:.]|$)`)
