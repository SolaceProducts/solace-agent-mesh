// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	DelimiterOpen  = "\u00ab"
	DelimiterClose = "\u00bb"
	ChainDelimiter = ">>>"
	TypeSeparator  = ":"
)

var embedRegex = regexp.MustCompile(
	`\x{00ab}([a-zA-Z0-9_]+)` +
		`(.*?)` +
		`(?:\s*\|\s*(.*?))?` +
		`\x{00bb}`,
)

type ParsedEmbed struct {
	Raw        string
	Type       string
	Expression string
	Format     string
	Start      int
	End        int
}

func FindAll(text string) []ParsedEmbed {
	matches := embedRegex.FindAllStringSubmatchIndex(text, -1)
	result := make([]ParsedEmbed, 0, len(matches))

	for _, m := range matches {
		embedType := text[m[2]:m[3]]
		expression := text[m[4]:m[5]]
		expression = strings.TrimPrefix(expression, ":")

		var format string
		if m[6] >= 0 && m[7] >= 0 {
			format = text[m[6]:m[7]]
		}

		result = append(result, ParsedEmbed{
			Raw:        text[m[0]:m[1]],
			Type:       embedType,
			Expression: expression,
			Format:     format,
			Start:      m[0],
			End:        m[1],
		})
	}
	return result
}

type ModifierChain struct {
	ArtifactSpec string
	Modifiers    []ModifierStep
	OutputFormat string
}

type ModifierStep struct {
	Name  string
	Param string
}

func ParseModifierChain(expression string) (ModifierChain, error) {
	parts := strings.Split(expression, ChainDelimiter)
	chain := ModifierChain{
		ArtifactSpec: strings.TrimSpace(parts[0]),
		OutputFormat: "text",
	}

	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		colonIdx := strings.Index(part, ":")
		if colonIdx < 0 {
			return chain, fmt.Errorf("Malformed modifier step '%s'. Expected 'modifier_name:modifier_value'", part) //nolint:staticcheck // ST1005: user-facing message must match Python wire format
		}
		name := part[:colonIdx]
		param := part[colonIdx+1:]

		if name == "format" {
			chain.OutputFormat = param
		} else {
			chain.Modifiers = append(chain.Modifiers, ModifierStep{Name: name, Param: param})
		}
	}

	return chain, nil
}

func ParseArtifactSpec(spec string) (filename string, version *int) {
	lastColon := strings.LastIndex(spec, ":")
	if lastColon < 0 {
		return spec, nil
	}
	possibleVersion := spec[lastColon+1:]
	var v int
	for _, c := range possibleVersion {
		if c < '0' || c > '9' {
			return spec, nil
		}
		v = v*10 + int(c-'0')
	}
	if possibleVersion == "" {
		return spec, nil
	}
	return spec[:lastColon], &v
}
