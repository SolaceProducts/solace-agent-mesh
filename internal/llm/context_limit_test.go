// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"strings"
	"testing"
)

func TestEveryContextLimitPatternIsMentionedInAnyCase(t *testing.T) {
	patterns := ContextLimitPatterns()
	if len(patterns) == 0 {
		t.Fatal("no context limit patterns")
	}
	for _, p := range patterns {
		for _, text := range []string{"provider said: " + p + " (request 7)", strings.ToUpper(p)} {
			if !MentionsContextLimit(text) {
				t.Errorf("MentionsContextLimit(%q) = false, want true", text)
			}
		}
	}
	for _, text := range []string{"", "upstream connect error", "rate limit exceeded", "invalid tool schema"} {
		if MentionsContextLimit(text) {
			t.Errorf("MentionsContextLimit(%q) = true, want false", text)
		}
	}
}

func TestContextLimitPatternsIsACopy(t *testing.T) {
	patterns := ContextLimitPatterns()
	original := patterns[0]
	patterns[0] = "edited by a caller"
	if got := ContextLimitPatterns()[0]; got != original {
		t.Errorf("a caller's edit reached the patterns: first is %q, want %q", got, original)
	}
}

func TestAProviderErrorIsRenderedWithThePrefixOfItsKind(t *testing.T) {
	for _, tc := range []struct {
		err    *Error
		prefix string
	}{
		{&Error{StatusCode: 400, Message: "tools.0.input_schema: JSON schema is invalid"}, BadRequestMessagePrefix},
		{&Error{StatusCode: 500, Message: "upstream connect error"}, OtherErrorMessagePrefix},
		{&Error{StatusCode: 500, Message: "prompt is too long: 250000 tokens"}, OtherErrorMessagePrefix},
	} {
		if got := tc.err.UserFacingMessage(); !strings.HasPrefix(got, tc.prefix) {
			t.Errorf("%d %q rendered %q, want it to start with %q", tc.err.StatusCode, tc.err.Message, got, tc.prefix)
		}
	}
	if !(&Error{StatusCode: 400, Message: "prompt is too long: 250000 tokens"}).IsContextLimitError() {
		t.Error("a 400 naming the context limit is not a context limit error")
	}
}
