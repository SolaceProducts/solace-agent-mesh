// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tmpl

import (
	"regexp"
	"strings"
	"unicode"
)

type FormattedNames struct {
	KebabCase         string
	PascalCase        string
	SnakeCase         string
	SnakeUpperCase    string
	SpacedName        string
	SpacedCapitalized string
}

var (
	camelSplitRe   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	acronymSplitRe = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	separatorRe    = regexp.MustCompile(`[\s\-_]+`)
)

func FormatName(name string) FormattedNames {
	name = strings.TrimSpace(name)

	normalized := separatorRe.ReplaceAllString(name, "_")

	split := camelSplitRe.ReplaceAllString(normalized, "${1}_${2}")
	split = acronymSplitRe.ReplaceAllString(split, "${1}_${2}")

	rawParts := strings.Split(split, "_")
	var parts []string
	var rawNonEmpty []string
	for _, p := range rawParts {
		if p == "" {
			continue
		}
		rawNonEmpty = append(rawNonEmpty, p)
		parts = append(parts, strings.ToLower(p))
	}

	spacedCap := make([]string, len(rawNonEmpty))
	for i, p := range rawNonEmpty {
		if isAllUpper(p) {
			spacedCap[i] = p
		} else {
			spacedCap[i] = capitalize(strings.ToLower(p))
		}
	}

	pascal := make([]string, len(parts))
	for i, p := range parts {
		pascal[i] = capitalize(p)
	}

	return FormattedNames{
		KebabCase:         strings.Join(parts, "-"),
		PascalCase:        strings.Join(pascal, ""),
		SnakeCase:         strings.Join(parts, "_"),
		SnakeUpperCase:    strings.ToUpper(strings.Join(parts, "_")),
		SpacedName:        strings.Join(parts, " "),
		SpacedCapitalized: strings.Join(spacedCap, " "),
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func isAllUpper(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}
