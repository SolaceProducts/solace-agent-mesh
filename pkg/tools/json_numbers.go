// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import "math"

func NullifyNonFinite(v any) (any, int) {
	switch val := v.(type) {
	case float64:
		if isNonFinite(val) {
			return nil, 1
		}
	case map[string]any:
		replaced := 0
		for k, elem := range val {
			var n int
			val[k], n = NullifyNonFinite(elem)
			replaced += n
		}
		return val, replaced
	case []any:
		replaced := 0
		for i, elem := range val {
			var n int
			val[i], n = NullifyNonFinite(elem)
			replaced += n
		}
		return val, replaced
	}
	return v, 0
}

func NormalizeYAMLForJMESPath(v any) any {
	return exactIntsAsFloat64(normalizeYAML(v))
}

const maxExactFloatInt = 1 << 53

func exactIntsAsFloat64(v any) any {
	switch val := v.(type) {
	case int:
		if val >= -maxExactFloatInt && val <= maxExactFloatInt {
			return float64(val)
		}
	case int64:
		if val >= -maxExactFloatInt && val <= maxExactFloatInt {
			return float64(val)
		}
	case uint64:
		if val <= maxExactFloatInt {
			return float64(val)
		}
	case map[string]any:
		for k, elem := range val {
			val[k] = exactIntsAsFloat64(elem)
		}
	case []any:
		for i, elem := range val {
			val[i] = exactIntsAsFloat64(elem)
		}
	}
	return v
}

func isNonFinite(f float64) bool { return math.IsNaN(f) || math.IsInf(f, 0) }
