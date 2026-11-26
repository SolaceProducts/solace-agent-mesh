// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package strutil

import (
	"regexp"
	"strings"
)

const (
	GrepAlternationRetriedNote = `No line matched \| as a literal "|", so these matches read \| as "or". ` +
		`This tool uses RE2 syntax, where "or" is a bare |.`
	GrepLiteralPipeNote = `In this tool's RE2 syntax \| matches a literal "|" and \( \) \{ \} \+ \? match literal characters; ` +
		`write "or" as a bare | and group with bare ( ).`
)

const grepOperators = "(){}+?"

func GrepAlternation(pattern string, caseInsensitive bool) (re *regexp.Regexp, holds bool) {
	alternation, holds, grouped := rewriteGrepAlternation(pattern)
	if !holds || grouped {
		return nil, holds
	}
	if caseInsensitive {
		alternation = "(?i)" + alternation
	}
	re, err := regexp.Compile(alternation)
	if err != nil || re.MatchString("") {
		return nil, true
	}
	return re, true
}

func rewriteGrepAlternation(pattern string) (string, bool, bool) {
	holds, grouped := false, false
	if !strings.Contains(pattern, `\|`) {
		return pattern, false, false
	}
	var b strings.Builder
	b.Grow(len(pattern))
	escaped := false
	for _, r := range pattern {
		switch {
		case !escaped && r == '\\':
			escaped = true
		case escaped && r == '|':
			b.WriteRune('|')
			holds, escaped = true, false
		case escaped:
			grouped = grouped || strings.ContainsRune(grepOperators, r)
			b.WriteRune('\\')
			b.WriteRune(r)
			escaped = false
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	return b.String(), holds, grouped
}
