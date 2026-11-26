// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tmpl

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

func Render(tmpl string, replacements map[string]string) string {
	keys := make([]string, 0, len(replacements))
	for k := range replacements {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Compare(len(b), len(a))
	})
	for _, k := range keys {
		tmpl = strings.ReplaceAll(tmpl, k, replacements[k])
	}
	return tmpl
}

func IndentText(text, indent string) string {
	lines := strings.Split(text, "\n")
	var result []string
	for i, line := range lines {
		if i == 0 {
			result = append(result, line)
		} else if strings.TrimSpace(line) == "" {
			result = append(result, "")
		} else {
			result = append(result, indent+line)
		}
	}
	return strings.Join(result, "\n")
}

func FormatYAMLList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = strconv.Quote(item)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
