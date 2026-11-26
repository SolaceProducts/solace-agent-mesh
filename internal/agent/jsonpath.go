// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"fmt"

	"github.com/ohler55/ojg/jp"
)

func ApplyJSONPath(data any, expression string) (any, error) {
	if _, ok := data.(string); ok {
		return nil, fmt.Errorf("jsonpath requires JSON data, got string")
	}

	expr, err := jp.ParseString(expression)
	if err != nil {
		return nil, fmt.Errorf("JSONPath evaluation failed: %w", err)
	}

	matches := expr.Get(data)
	if isMultiValueJSONPath(expr) {
		return matches, nil
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("JSONPath evaluation failed: no match for %q", expression)
	}
	return matches[0], nil
}

func isMultiValueJSONPath(expr jp.Expr) bool {
	for _, frag := range expr {
		switch frag.(type) {
		case jp.Wildcard, jp.Descent, jp.Slice, jp.Union, *jp.Filter:
			return true
		}
	}
	return false
}
