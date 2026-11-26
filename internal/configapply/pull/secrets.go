// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"fmt"
	"strings"
	"unicode"
)

const redactedSentinel = "<REDACTED>"

func substituteSecrets(kind, resourceName string, in map[string]any) (map[string]any, []SecretWarning) {
	var warnings []SecretWarning
	out, _ := walkSecrets(kind, resourceName, secretPath{}, in, &warnings).(map[string]any)
	return out, warnings
}

type secretPath struct {
	full    string
	lastKey string
	index   string
}

func (p secretPath) leaf() string {
	if strings.Contains(p.lastKey, ".") {
		return p.lastKey + p.index
	}
	return leafSegment(p.full)
}

func walkSecrets(kind, name string, path secretPath, v any, warnings *[]SecretWarning) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			child := secretPath{full: joinPath(path.full, k), lastKey: k}
			out[k] = walkSecrets(kind, name, child, val, warnings)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			suffix := fmt.Sprintf("[%d]", i)
			child := secretPath{full: path.full + suffix, lastKey: path.lastKey, index: path.index + suffix}
			out[i] = walkSecrets(kind, name, child, item, warnings)
		}
		return out
	case string:
		if x == redactedSentinel {
			placeholder := placeholderFor(kind, name, path.leaf())
			*warnings = append(*warnings, SecretWarning{
				Kind:         kind,
				ResourceName: name,
				FieldPath:    path.full,
				Placeholder:  placeholder,
			})
			return placeholder
		}
		return x
	}
	return v
}

func placeholderFor(kind, name, leaf string) string {
	parts := []string{kind, name, leaf}
	return "${" + sanitizeVarName(strings.Join(parts, "_")) + "}"
}

func leafSegment(path string) string {
	if path == "" {
		return path
	}
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	if before, _, ok := strings.Cut(path, "["); ok {
		return before
	}
	return path
}

func sanitizeVarName(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
			prevUnderscore = false
		default:
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	out := b.String()
	out = strings.TrimRight(out, "_")
	if out == "" {
		out = "VAR"
	}
	return out
}

func joinPath(prefix, segment string) string {
	if prefix == "" {
		return segment
	}
	return prefix + "." + segment
}
