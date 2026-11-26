// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package strutil

import "testing"

func TestGrepAlternation(t *testing.T) {
	cases := map[string]struct {
		pattern         string
		caseInsensitive bool
		holds           bool
		orMatches       []string
		orMisses        []string
	}{
		"grep_style_or":                       {pattern: `alpha\|beta`, holds: true, orMatches: []string{"alpha", "a beta"}, orMisses: []string{"gamma", "alph|bet"}},
		"several_alternatives":                {pattern: `a.*b\|c\|d`, holds: true, orMatches: []string{"a b", "c", "d"}, orMisses: []string{"x"}},
		"case_insensitive":                    {pattern: `ALPHA\|beta`, caseInsensitive: true, holds: true, orMatches: []string{"alpha", "BETA"}},
		"case_sensitive":                      {pattern: `ALPHA\|beta`, holds: true, orMatches: []string{"ALPHA"}, orMisses: []string{"alpha"}},
		"other_escapes_are_kept":              {pattern: `\d+\.\s\|x`, holds: true, orMatches: []string{"12. a", "x"}, orMisses: []string{"12a"}},
		"re2_or_is_not_grep_style":            {pattern: `alpha|beta`},
		"escaped_backslash_then_or":           {pattern: `a\\|b`},
		"no_backslash":                        {pattern: `plain`},
		"trailing_backslash_without_a_pipe":   {pattern: `a\`},
		"trailing_backslash_after_a_pipe":     {pattern: `a\|b\`, holds: true},
		"grep_open_group":                     {pattern: `\(a\|b`, holds: true},
		"grep_close_group":                    {pattern: `a\|b\)`, holds: true},
		"grep_open_interval":                  {pattern: `a\{\|b`, holds: true},
		"grep_close_interval":                 {pattern: `a\|b\}`, holds: true},
		"grep_one_or_more":                    {pattern: `a\+\|b`, holds: true},
		"grep_optional":                       {pattern: `a\?\|b`, holds: true},
		"an_operator_before_a_later_escape":   {pattern: `\(a\|b\)\.go`, holds: true},
		"an_operator_before_a_class_escape":   {pattern: `\(a\d\|b`, holds: true},
		"odd_backslash_run_before_a_pipe":     {pattern: `a\\\|b`, holds: true, orMatches: []string{`a\`, "b"}, orMisses: []string{"a"}},
		"sole_pipe_would_match_every_line":    {pattern: `\|`, holds: true},
		"leading_pipe_would_match_every_line": {pattern: `^\|`, holds: true},
		"trailing_empty_alternative":          {pattern: `foo\|`, holds: true},
		"doubled_empty_alternative":           {pattern: `a\|\|b`, holds: true},
		"empty_group_alternative":             {pattern: `(\|)`, holds: true},
		"grep_grouping_is_not_half_read":      {pattern: `\(foo\|bar\)`, holds: true},
		"grep_interval_is_not_half_read":      {pattern: `a\{2\}\|b`, holds: true},
		"reading_that_does_not_compile":       {pattern: `foo\|*`, holds: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			re, holds := GrepAlternation(tc.pattern, tc.caseInsensitive)
			if holds != tc.holds {
				t.Fatalf("holds = %v, want %v", holds, tc.holds)
			}
			if (re != nil) != (tc.orMatches != nil) {
				t.Fatalf("reading offered = %v, want %v", re != nil, tc.orMatches != nil)
			}
			for _, line := range tc.orMatches {
				if !re.MatchString(line) {
					t.Errorf("%s does not match %q", re, line)
				}
			}
			for _, line := range tc.orMisses {
				if re.MatchString(line) {
					t.Errorf("%s matches %q", re, line)
				}
			}
		})
	}
}
