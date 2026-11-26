// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"os"
	"regexp"
	"slices"
)

var expandVarsPattern = regexp.MustCompile(`\$\{([^}:\s]+)(?:(\s*,\s*|:-|:\+)((?:[^$}]|\$[^{}]|\$\{[^}]*\}|\$)*))?\}`)

func ExpandVars(text string, lookup func(string) (string, bool)) string {
	out, _ := ExpandVarsCollect(text, lookup)
	return out
}

func ExpandVarsEnv(text string) string {
	return ExpandVars(text, os.LookupEnv)
}

func ExpandVarsCollect(text string, lookup func(string) (string, bool)) (string, []string) {
	var seen map[string]struct{}
	expanded := expandVarsPattern.ReplaceAllStringFunc(text, func(match string) string {
		groups := expandVarsPattern.FindStringSubmatch(match)
		varName := groups[1]
		op := groups[2]
		rhs := groups[3]

		val, ok := lookup(varName)
		switch op {
		case ":-":
			if ok && val != "" {
				return val
			}
			return ExpandVars(rhs, lookup)
		case ":+":
			if ok && val != "" {
				return ExpandVars(rhs, lookup)
			}
			return ""
		case "":
			if ok {
				return val
			}
			if seen == nil {
				seen = map[string]struct{}{}
			}
			seen[varName] = struct{}{}
			return ""
		default:
			if ok {
				return val
			}
			return ExpandVars(rhs, lookup)
		}
	})
	if len(seen) == 0 {
		return expanded, nil
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return expanded, names
}
