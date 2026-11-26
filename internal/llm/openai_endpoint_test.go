// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import "testing"

func TestChatCompletionsEndpoint(t *testing.T) {
	for base, want := range map[string]string{
		"https://h/api/v3/chat/completions":       "https://h/api/v3/chat/completions",
		"https://h/api/v3/chat/completions/":      "https://h/api/v3/chat/completions",
		"\thttps://h/api/v3/chat/completions\n":   "https://h/api/v3/chat/completions",
		"https://h/api/v3/chat/completions?k=v":   "https://h/api/v3/chat/completions?k=v",
		"https://h/chat/completions":              "https://h/chat/completions",
		"https://h":                               "",
		"https://h/v1":                            "",
		"https://h/api/v3":                        "",
		"https://h/chat/completions/proxy":        "",
		"https://h/api/v3?next=/chat/completions": "",
		"/api/v3/chat/completions":                "",
		"":                                        "",
	} {
		got, ok := ChatCompletionsEndpoint(base)
		if got != want || ok != (want != "") {
			t.Errorf("ChatCompletionsEndpoint(%q) = %q, %v, want %q", base, got, ok, want)
		}
	}
}

func TestChatCompletionsRoot(t *testing.T) {
	for base, want := range map[string]string{
		"https://h/api/v3/chat/completions":     "https://h/api/v3",
		"https://h/api/v3/chat/completions/":    "https://h/api/v3",
		"https://h/api/v3/chat/completions?k=v": "https://h/api/v3?k=v",
		"https://h/api/v3":                      "",
	} {
		got, ok := ChatCompletionsRoot(base)
		if got != want || ok != (want != "") {
			t.Errorf("ChatCompletionsRoot(%q) = %q, %v, want %q", base, got, ok, want)
		}
	}
}

func TestOpenAIModelsURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://h/api/v3/chat/completions":     "https://h/api/v3/models",
		"https://h/api/v3/chat/completions/":    "https://h/api/v3/models",
		"https://h/api/v3/chat/completions?k=v": "https://h/api/v3/models?k=v",
		"https://h":                             "https://h/v1/models",
		"https://h/":                            "https://h/v1/models",
		"https://h/v1":                          "https://h/v1/models",
		"https://h/api/v3":                      "https://h/api/v3/v1/models",
	} {
		if got := OpenAIModelsURL(base); got != want {
			t.Errorf("OpenAIModelsURL(%q) = %q, want %q", base, got, want)
		}
	}
}
