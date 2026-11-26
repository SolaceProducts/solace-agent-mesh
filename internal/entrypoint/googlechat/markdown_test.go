// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderForChat(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"bold":                   {"a **b** c", "a *b* c"},
		"link":                   {"see [docs](https://example.com/x)", "see <https://example.com/x|docs>"},
		"strike":                 {"~~gone~~", "~gone~"},
		"blank run":              {"intro\n\n\n \n\n\t\nanswer", "intro\n\nanswer"},
		"code keeps blank lines": {"```\na\n\n\n\nb\n```", "```\na\n\n\n\nb\n```"},
		"heading":                {"## Title\nbody", "*Title*\nbody"},
		"rule":                   {"a\n---\nb", "a\n" + chatDivider + "\nb"},
		"code verbatim":          {"```go\nx := **y**\n```", "```\nx := **y**\n```"},
		"table": {
			"| a | b |\n|---|---|\n| 1 | 2 |",
			"```\n| a | b |\n| 1 | 2 |\n```",
		},
		"bullets untouched": {"* one\n- two", "* one\n- two"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := renderForChat(tc.in); got != tc.want {
				t.Errorf("renderForChat(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSplitForChat_RespectsLimitAndLosesNothing(t *testing.T) {
	paragraph := strings.Repeat("word ", 300) + "\n"
	text := strings.Repeat(paragraph, 10)

	chunks := splitForChat(text, maxMessageRunes)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want the text split", len(chunks))
	}
	for i, c := range chunks {
		if n := utf8.RuneCountInString(c); n > maxMessageRunes {
			t.Errorf("chunk %d has %d runes, over the %d limit", i, n, maxMessageRunes)
		}
	}
	if got, want := strings.Count(strings.Join(chunks, " "), "word"), strings.Count(text, "word"); got != want {
		t.Errorf("words after split = %d, want %d", got, want)
	}
}

func TestSplitForChat_UnbreakableTextIsCutAtLimit(t *testing.T) {
	chunks := splitForChat(strings.Repeat("x", maxMessageRunes+10), maxMessageRunes)
	if len(chunks) != 2 || utf8.RuneCountInString(chunks[0]) != maxMessageRunes {
		t.Fatalf("chunks = %d (first %d runes), want a cut exactly at the limit", len(chunks), utf8.RuneCountInString(chunks[0]))
	}
}
