// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestTitleModelAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *entrypoint.GatewayConfig
		want string
	}{
		{"nil config", nil, ""},
		{"nil ModelRaw", &entrypoint.GatewayConfig{}, ""},
		{"non-map ModelRaw", &entrypoint.GatewayConfig{ModelRaw: "a-string"}, ""},
		{
			"map without title model",
			&entrypoint.GatewayConfig{ModelRaw: map[string]any{"llm_service_model_name": "gpt-5"}},
			"",
		},
		{
			"map with title model",
			&entrypoint.GatewayConfig{ModelRaw: map[string]any{"llm_service_title_model_name": "fast-model"}},
			"fast-model",
		},
		{
			"title model wrong type is ignored",
			&entrypoint.GatewayConfig{ModelRaw: map[string]any{"llm_service_title_model_name": 42}},
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := titleModelAlias(tc.cfg); got != tc.want {
				t.Errorf("titleModelAlias = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStripTitleMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"plain text", "plain text"},
		{"**bold**", "bold"},
		{"***bolder***", "bolder"},
		{"_italic_", "italic"},
		{"~~strike~~", "strike"},
		{"`code`", "code"},
		{"# Heading", "Heading"},
		{"### Deep heading", "Deep heading"},
		{"#nohash", "#nohash"},
		{"  **trim me**  ", "trim me"},
		{"mix **of** _markers_ and `code`", "mix of markers and code"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := stripTitleMarkdown(tc.in); got != tc.want {
				t.Errorf("stripTitleMarkdown(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"strips surrounding double quotes", `"hello"`, "hello"},
		{"strips surrounding single quotes", `'hello'`, "hello"},
		{"strips whitespace and markdown", "  **Bold Title**  ", "Bold Title"},
		{"strips heading marker", "# My Title", "My Title"},
		{"empty string stays empty", "", ""},
		{"collapsed quotes", `"he said "hi""`, `he said "hi`},

		{"flattens a leading preamble", "Sure! Here is a title:\n\nOttawa Weather", "Sure! Here is a title: Ottawa Weather"},
		{"flattens trailing commentary", "Ottawa Weather\n\nHope that helps!", "Ottawa Weather Hope that helps!"},
		{"collapses interior whitespace", "Ottawa   Weather\tReport", "Ottawa Weather Report"},
		{"flattens a newline into a single line", "Here:\n**Ottawa Weather**", "Here: Ottawa Weather"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cleanTitle(tc.in); got != tc.want {
				t.Errorf("cleanTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	t.Run("enforces max length", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("x", titleMaxLen+50)
		got := cleanTitle(long)
		if len(got) != titleMaxLen {
			t.Errorf("cleanTitle length = %d, want %d", len(got), titleMaxLen)
		}
	})

	t.Run("never splits a multi-byte rune at the cut", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("x", titleMaxLen-1) + "économie"
		got := cleanTitle(long)
		if !utf8.ValidString(got) {
			t.Errorf("cleanTitle produced invalid UTF-8: %q", got)
		}
	})
}

func TestFallbackTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"empty falls back to placeholder", "", placeholderSessionTitle},
		{"whitespace falls back to placeholder", "  \t ", placeholderSessionTitle},
		{"short message kept verbatim", "weather in London", "weather in London"},
		{"long message truncated with ellipsis", strings.Repeat("x", titleCharLimit+10), strings.Repeat("x", titleCharLimit) + "..."},
		{"never splits a multi-byte rune at the cut", strings.Repeat("x", titleCharLimit-1) + "économie", strings.Repeat("x", titleCharLimit-1) + "..."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := fallbackTitle(tc.in); got != tc.want {
				t.Errorf("fallbackTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
