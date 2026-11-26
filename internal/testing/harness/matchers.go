// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
)

var evalMathPattern = regexp.MustCompile(`eval_math:\[(.+?)\]`)

func resolveEvalMath(s string) string {
	return evalMathPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := evalMathPattern.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		expression := strings.TrimSpace(parts[1])

		env := testMathEnv()
		program, err := expr.Compile(expression, expr.Env(env))
		if err != nil {
			return match
		}
		result, err := expr.Run(program, env)
		if err != nil {
			return match
		}

		switch v := result.(type) {
		case float64:
			if math.IsInf(v, 1) {
				return "inf"
			}
			if math.IsInf(v, -1) {
				return "-inf"
			}
			if math.IsNaN(v) {
				return "nan"
			}
			if v == math.Trunc(v) {
				return fmt.Sprintf("%.1f", v)
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		default:
			return fmt.Sprintf("%v", v)
		}
	})
}

func testMathEnv() map[string]any {
	return map[string]any{
		"pi":      math.Pi,
		"e":       math.E,
		"inf":     math.Inf(1),
		"abs":     math.Abs,
		"round":   math.Round,
		"sin":     math.Sin,
		"cos":     math.Cos,
		"tan":     math.Tan,
		"sqrt":    math.Sqrt,
		"pow":     math.Pow,
		"exp":     math.Exp,
		"log":     math.Log,
		"log10":   math.Log10,
		"sinh":    math.Sinh,
		"cosh":    math.Cosh,
		"tanh":    math.Tanh,
		"radians": func(deg float64) float64 { return deg * math.Pi / 180 },
	}
}

func MatchTextContains(text string, substrings []string) error {
	for _, sub := range substrings {
		resolved := resolveEvalMath(sub)
		if !strings.Contains(text, resolved) {
			return fmt.Errorf("text does not contain %q\n  text: %s", truncate(resolved, 300), truncate(text, 500))
		}
	}
	return nil
}

func MatchTextExact(text, expected string) error {
	resolved := resolveEvalMath(expected)
	if text != resolved {
		return fmt.Errorf("text mismatch\n  expected: %s\n  got:      %s", truncate(resolved, 200), truncate(text, 200))
	}
	return nil
}

func MatchTextNotContains(text string, substrings []string) error {
	for _, sub := range substrings {
		resolved := resolveEvalMath(sub)
		if strings.Contains(text, resolved) {
			return fmt.Errorf("text should not contain %q but does\n  text: %s", resolved, truncate(text, 200))
		}
	}
	return nil
}

func MatchTextRegex(text, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid regex %q: %w", pattern, err)
	}
	if !re.MatchString(text) {
		return fmt.Errorf("text does not match regex %q\n  text: %s", pattern, truncate(text, 200))
	}
	return nil
}

func MatchWildcard(actual, pattern string) bool {
	if pattern == "*" {
		return true
	}
	return actual == pattern
}

func MatchPartText(actualText string, pm PartMatch) error {
	if pm.TextExact != "" {
		if err := MatchTextExact(actualText, pm.TextExact); err != nil {
			return err
		}
	}
	if len(pm.TextContains) > 0 {
		if err := MatchTextContains(actualText, pm.TextContains); err != nil {
			return err
		}
	}
	for _, pattern := range pm.TextRegex {
		if err := MatchTextRegex(actualText, pattern); err != nil {
			return err
		}
	}
	if len(pm.TextNotContains) > 0 {
		if err := MatchTextNotContains(actualText, pm.TextNotContains); err != nil {
			return err
		}
	}
	return nil
}

func MatchJSONContains(jsonData []byte, expected map[string]any) error {
	var actual map[string]any
	if err := json.Unmarshal(jsonData, &actual); err != nil {
		return fmt.Errorf("unmarshal JSON: %w", err)
	}

	for key, expectedVal := range expected {
		if strings.HasSuffix(key, "_matches_regex") {
			baseKey := strings.TrimSuffix(key, "_matches_regex")
			actualVal, ok := actual[baseKey]
			if !ok {
				return fmt.Errorf("key %q not found in JSON (from regex assertion %q)", baseKey, key)
			}
			av, ok := actualVal.(string)
			if !ok {
				return fmt.Errorf("key %q: regex expects string, got %T", baseKey, actualVal)
			}
			pattern, ok := expectedVal.(string)
			if !ok {
				return fmt.Errorf("key %q: regex pattern must be string", key)
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("key %q: invalid regex %q: %w", key, pattern, err)
			}
			if !re.MatchString(av) {
				return fmt.Errorf("key %q: value %q does not match regex %q", baseKey, av, pattern)
			}
			continue
		}

		if strings.HasSuffix(key, "_contains") {
			baseKey := strings.TrimSuffix(key, "_contains")
			actualVal, ok := actual[baseKey]
			if !ok {
				return fmt.Errorf("key %q not found in JSON (from contains assertion %q)", baseKey, key)
			}
			av, ok := actualVal.(string)
			if !ok {
				return fmt.Errorf("key %q: _contains expects string, got %T", baseKey, actualVal)
			}
			expectedList, ok := expectedVal.([]any)
			if !ok {
				return fmt.Errorf("key %q: _contains expects array of strings", key)
			}
			for _, item := range expectedList {
				substr, ok := item.(string)
				if !ok {
					continue
				}
				if !strings.Contains(av, substr) {
					return fmt.Errorf("key %q: value does not contain %q", baseKey, substr)
				}
			}
			continue
		}

		actualVal, ok := actual[key]
		if !ok {
			return fmt.Errorf("key %q not found in JSON", key)
		}

		switch ev := expectedVal.(type) {
		case string:
			av, ok := actualVal.(string)
			if !ok {
				return fmt.Errorf("key %q: expected string, got %T", key, actualVal)
			}
			if av != ev {
				return fmt.Errorf("key %q: expected %q, got %q", key, ev, av)
			}
		case float64:
			switch av := actualVal.(type) {
			case float64:
				if av != ev {
					return fmt.Errorf("key %q: expected %v, got %v", key, ev, av)
				}
			default:
				return fmt.Errorf("key %q: expected number %v, got %T(%v)", key, ev, actualVal, actualVal)
			}
		case int:
			switch av := actualVal.(type) {
			case float64:
				if av != float64(ev) {
					return fmt.Errorf("key %q: expected %v, got %v", key, ev, av)
				}
			default:
				return fmt.Errorf("key %q: expected number %v, got %T(%v)", key, ev, actualVal, actualVal)
			}
		case bool:
			av, ok := actualVal.(bool)
			if !ok || av != ev {
				return fmt.Errorf("key %q: expected %v, got %v", key, ev, actualVal)
			}
		case map[string]any:
			if regexPattern, ok := ev["_regex"].(string); ok {
				av, ok := actualVal.(string)
				if !ok {
					return fmt.Errorf("key %q: _regex expects string, got %T", key, actualVal)
				}
				re, err := regexp.Compile(regexPattern)
				if err != nil {
					return fmt.Errorf("key %q: invalid _regex %q: %w", key, regexPattern, err)
				}
				if !re.MatchString(av) {
					return fmt.Errorf("key %q: value %q does not match regex %q", key, av, regexPattern)
				}
			} else {
				nestedJSON, err := json.Marshal(actualVal)
				if err != nil {
					return fmt.Errorf("key %q: marshal nested: %w", key, err)
				}
				if err := MatchJSONContains(nestedJSON, ev); err != nil {
					return fmt.Errorf("key %q: %w", key, err)
				}
			}
		case []any:
			actualArr, ok := actualVal.([]any)
			if !ok {
				return fmt.Errorf("key %q: expected array, got %T", key, actualVal)
			}
			if len(ev) > len(actualArr) {
				return fmt.Errorf("key %q: expected at least %d elements, got %d", key, len(ev), len(actualArr))
			}
			for i, expectedElem := range ev {
				expectedMap, ok := expectedElem.(map[string]any)
				if !ok {
					expectedStr := fmt.Sprintf("%v", expectedElem)
					actualStr := fmt.Sprintf("%v", actualArr[i])
					if expectedStr != actualStr {
						return fmt.Errorf("key %q[%d]: expected %v, got %v", key, i, expectedElem, actualArr[i])
					}
					continue
				}
				elemJSON, err := json.Marshal(actualArr[i])
				if err != nil {
					return fmt.Errorf("key %q[%d]: marshal: %w", key, i, err)
				}
				if err := MatchJSONContains(elemJSON, expectedMap); err != nil {
					return fmt.Errorf("key %q[%d]: %w", key, i, err)
				}
			}
		}
	}
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
