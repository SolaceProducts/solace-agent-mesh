// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"fmt"
	"regexp"
	"strings"
)

const maxParamNameLen = 60

var (
	reNonAlnum        = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	reLowerUpper      = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	reUpperUpperLower = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	reMultiUnderscore = regexp.MustCompile(`_+`)
)

var pythonReservedWords = map[string]struct{}{
	"False": {}, "None": {}, "True": {},
	"and": {}, "as": {}, "assert": {}, "async": {}, "await": {},
	"break": {}, "class": {}, "continue": {}, "def": {}, "del": {},
	"elif": {}, "else": {}, "except": {}, "finally": {}, "for": {},
	"from": {}, "global": {}, "if": {}, "import": {}, "in": {},
	"is": {}, "lambda": {}, "nonlocal": {}, "not": {}, "or": {},
	"pass": {}, "raise": {}, "return": {}, "try": {}, "while": {},
	"with": {}, "yield": {}, "match": {}, "case": {},
}

func toSnakeCase(text string) string {
	if text == "" {
		return ""
	}
	text = reNonAlnum.ReplaceAllString(text, "_")
	text = reLowerUpper.ReplaceAllString(text, "${1}_${2}")
	text = reUpperUpperLower.ReplaceAllString(text, "${1}_${2}")
	text = strings.ToLower(text)
	text = reMultiUnderscore.ReplaceAllString(text, "_")
	text = strings.Trim(text, "_")
	return text
}

func renamePythonKeywords(s string) string {
	if _, ok := pythonReservedWords[s]; ok {
		return "param_" + s
	}
	return s
}

func truncateTo60(s string) string {
	return truncateRunes(s, maxParamNameLen)
}

func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

func claimUniqueName(name string, taken map[string]struct{}) string {
	if _, seen := taken[name]; !seen {
		taken[name] = struct{}{}
		return name
	}
	for i := 0; ; i++ {
		suffix := fmt.Sprintf("_%d", i)
		candidate := truncateRunes(name, maxParamNameLen-len(suffix)) + suffix
		if _, seen := taken[candidate]; !seen {
			taken[candidate] = struct{}{}
			return candidate
		}
	}
}

func shortPropertyNames(names []string) map[string]string {
	short := make(map[string]string, len(names))
	taken := make(map[string]struct{}, len(names))
	for _, name := range names {
		if truncateTo60(name) == name {
			short[name] = name
			taken[name] = struct{}{}
		}
	}
	for _, name := range names {
		if _, done := short[name]; !done {
			short[name] = claimUniqueName(truncateTo60(name), taken)
		}
	}
	return short
}

func pyNameFor(originalName string) string {
	return truncateTo60(renamePythonKeywords(toSnakeCase(originalName)))
}

func OperationToolName(operationID string) string {
	return truncateTo60(toSnakeCase(operationID))
}
