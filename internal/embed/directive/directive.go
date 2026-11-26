// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package directive

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Open  = "«"
	Close = "»"
)

var InlineEmbedRegex = regexp.MustCompile(`«([^»]+)»`)

var TemplateBlockRegex = regexp.MustCompile(
	`«««template(?:_liquid)?:\s*([^\n]+)\n([\s\S]*?)»»»`,
)

var TemplateParamRegex = regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)

func ParseInlineEmbed(raw string) (typ, value string, hasValue, ok bool) {
	if len(raw) < len(Open)+len(Close) {
		return "", "", false, false
	}
	inner := strings.TrimSpace(raw[len(Open) : len(raw)-len(Close)])
	if inner == "" {
		return "", "", false, false
	}
	typ = inner
	if before, after, found := strings.Cut(inner, ":"); found {
		typ, value, hasValue = before, after, true
	}
	return typ, value, hasValue, true
}

func ParseArtifactSpec(spec string) (filename string, version *int) {
	parts := strings.SplitN(spec, ":", 2)
	filename = parts[0]
	if len(parts) > 1 {
		if v, err := strconv.Atoi(parts[1]); err == nil {
			version = &v
		}
	}
	return
}

var formatRegex = regexp.MustCompile(`^format:(.*)`)

type ModifierStep struct {
	Name  string
	Value string
}

func ParseModifierChain(expression string) (artifactSpec string, modifiers []ModifierStep, outputFormat string) {
	parts := strings.Split(expression, chainSeparator)

	var cleaned []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}

	if len(cleaned) == 0 {
		return "", nil, ""
	}

	artifactSpec = cleaned[0]

	if len(cleaned) == 1 {
		return artifactSpec, nil, ""
	}

	lastPart := cleaned[len(cleaned)-1]
	remaining := cleaned[1:]

	if m := formatRegex.FindStringSubmatch(lastPart); m != nil {
		outputFormat = strings.TrimSpace(m[1])
		remaining = cleaned[1 : len(cleaned)-1]
	}

	for _, part := range remaining {
		name, value, found := strings.Cut(part, ":")
		if !found {
			continue
		}
		modifiers = append(modifiers, ModifierStep{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
	}

	return artifactSpec, modifiers, outputFormat
}

const chainSeparator = ">>>"

type ArtifactRef struct {
	Spec     string
	Filename string
	Version  *int

	start, end int
}

var artifactRefEmbedTypes = map[string]bool{
	"artifact_content": true,
	"artifact_return":  true,
	"artifact_meta":    true,
}

func ScanArtifactRefs(text string) []ArtifactRef {
	var refs []ArtifactRef
	for _, m := range InlineEmbedRegex.FindAllStringSubmatchIndex(text, -1) {
		refs = append(refs, scanInlineEmbed(text, m[2], m[3])...)
	}
	for _, m := range TemplateBlockRegex.FindAllStringSubmatchIndex(text, -1) {
		paramsStart := m[2]
		params := text[paramsStart:m[3]]
		for _, pm := range TemplateParamRegex.FindAllStringSubmatchIndex(params, -1) {
			if params[pm[2]:pm[3]] != "data" {
				continue
			}
			refs = append(refs, newArtifactRef(text, paramsStart+pm[4], paramsStart+pm[5]))
		}
		contentStart, contentEnd := m[4], m[5]
		for _, im := range InlineEmbedRegex.FindAllStringSubmatchIndex(text[contentStart:contentEnd], -1) {
			refs = append(refs, scanInlineEmbed(text, contentStart+im[2], contentStart+im[3])...)
		}
	}
	slices.SortFunc(refs, func(a, b ArtifactRef) int { return cmp.Compare(a.start, b.start) })
	return dedupeRefs(refs)
}

func dedupeRefs(refs []ArtifactRef) []ArtifactRef {
	out := refs[:0]
	for _, r := range refs {
		if len(out) > 0 && out[len(out)-1].start == r.start && out[len(out)-1].end == r.end {
			continue
		}
		out = append(out, r)
	}
	return out
}

func RewriteArtifactRefs(text string, replace func(ArtifactRef) (string, bool)) string {
	refs := ScanArtifactRefs(text)
	out := text
	lastReplacedStart := len(text) + 1
	for i := len(refs) - 1; i >= 0; i-- {
		ref := refs[i]
		if ref.end > lastReplacedStart {
			continue
		}
		newSpec, ok := replace(ref)
		if !ok {
			continue
		}
		out = out[:ref.start] + newSpec + out[ref.end:]
		lastReplacedStart = ref.start
	}
	return out
}

func scanInlineEmbed(text string, innerStart, innerEnd int) []ArtifactRef {
	start, end := trimSpan(text, innerStart, innerEnd)
	colon := strings.Index(text[start:end], ":")
	if colon < 0 || !artifactRefEmbedTypes[text[start:start+colon]] {
		return nil
	}
	valueStart := start + colon + 1
	if text[start:start+colon] != "artifact_content" {
		s, e := trimSpan(text, valueStart, end)
		if s >= e {
			return nil
		}
		return []ArtifactRef{newArtifactRef(text, s, e)}
	}
	return scanModifierChain(text, valueStart, end)
}

func scanModifierChain(text string, start, end int) []ArtifactRef {
	var refs []ArtifactRef
	first := true
	for segStart := start; ; {
		segEnd := end
		rel := strings.Index(text[segStart:end], chainSeparator)
		if rel >= 0 {
			segEnd = segStart + rel
		}
		s, e := trimSpan(text, segStart, segEnd)
		switch {
		case s >= e:
		case first:
			refs = append(refs, newArtifactRef(text, s, e))
			first = false
		default:
			if c := strings.Index(text[s:e], ":"); c >= 0 && strings.TrimSpace(text[s:s+c]) == "apply_to_template" {
				vs, ve := trimSpan(text, s+c+1, e)
				if vs < ve {
					refs = append(refs, newArtifactRef(text, vs, ve))
				}
			}
		}
		if rel < 0 {
			return refs
		}
		segStart = segEnd + len(chainSeparator)
	}
}

func newArtifactRef(text string, start, end int) ArtifactRef {
	spec := text[start:end]
	filename, version := ParseArtifactSpec(spec)
	return ArtifactRef{Spec: spec, Filename: filename, Version: version, start: start, end: end}
}

func trimSpan(text string, start, end int) (int, int) {
	for start < end {
		r, size := utf8.DecodeRuneInString(text[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}
	for start < end {
		r, size := utf8.DecodeLastRuneInString(text[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	return start, end
}
