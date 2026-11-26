// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var topicRe = regexp.MustCompile(`\{\{(.*?)\}\}|\{\s*([A-Za-z_]\w*)\s*\}`)

func fillTopicTemplate(tmpl string, params map[string]any) (string, error) {
	var missing string
	out := topicRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		var expr string
		if strings.HasPrefix(m, "{{") {
			expr = strings.TrimSpace(m[2 : len(m)-2])
			if strings.Contains(expr, "://") {
				expr = strings.TrimSpace(expr[strings.Index(expr, "//")+2:])
			}
		} else {
			expr = strings.TrimSpace(m[1 : len(m)-1])
		}
		v, ok := params[expr]
		if !ok {
			if missing == "" {
				missing = expr
			}
			return m
		}
		return formatTemplateValue(v)
	})
	if missing != "" {
		return "", fmt.Errorf("missing required input property %q for topic address", missing)
	}
	return out, nil
}

func formatTemplateValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}
