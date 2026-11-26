// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type InvokeFunc func(args []any) (any, error)

var invokeRegistry = map[string]InvokeFunc{
	"add":      invokeAdd,
	"subtract": invokeSubtract,
	"multiply": invokeMultiply,
	"divide":   invokeDivide,
	"modulus":  invokeModulus,
	"power":    invokePower,
	"negate":   invokeNegate,

	"equal":                 invokeEqual,
	"not_equal":             invokeNotEqual,
	"greater_than":          invokeGreaterThan,
	"less_than":             invokeLessThan,
	"greater_than_or_equal": invokeGreaterThanOrEqual,
	"less_than_or_equal":    invokeLessThanOrEqual,

	"and_op": invokeAnd,
	"or_op":  invokeOr,
	"not_op": invokeNot,

	"in_op":  invokeIn,
	"append": invokeAppend,

	"if_else": invokeIfElse,

	"empty_list":   func([]any) (any, error) { return []any{}, nil },
	"empty_dict":   func([]any) (any, error) { return map[string]any{}, nil },
	"empty_string": func([]any) (any, error) { return "", nil },
	"empty_int":    func([]any) (any, error) { return 0, nil },
	"empty_float":  func([]any) (any, error) { return 0.0, nil },

	"uuid": func([]any) (any, error) { return idgen.New(), nil },
}

func CallInvoke(name string, args []any) (any, error) {
	fn, ok := invokeRegistry[name]
	if !ok {
		return nil, fmt.Errorf("unknown invoke function: %q", name)
	}
	return fn(args)
}

func invokeAdd(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	a, b, err := toFloat64Pair(args[0], args[1])
	if err != nil {
		sa, sb := fmt.Sprintf("%v", args[0]), fmt.Sprintf("%v", args[1])
		return sa + sb, nil
	}
	return a + b, nil
}

func invokeSubtract(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	a, b, err := toFloat64Pair(args[0], args[1])
	if err != nil {
		return nil, fmt.Errorf("subtract: %w", err)
	}
	return a - b, nil
}

func invokeMultiply(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	a, b, err := toFloat64Pair(args[0], args[1])
	if err != nil {
		return nil, fmt.Errorf("multiply: %w", err)
	}
	return a * b, nil
}

func invokeDivide(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	a, b, err := toFloat64Pair(args[0], args[1])
	if err != nil {
		return nil, fmt.Errorf("divide: %w", err)
	}
	if b == 0 {
		return nil, fmt.Errorf("divide: division by zero")
	}
	return a / b, nil
}

func invokeModulus(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	a, b, err := toFloat64Pair(args[0], args[1])
	if err != nil {
		return nil, fmt.Errorf("modulus: %w", err)
	}
	if b == 0 {
		return nil, fmt.Errorf("modulus: division by zero")
	}
	return math.Mod(a, b), nil
}

func invokePower(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	a, b, err := toFloat64Pair(args[0], args[1])
	if err != nil {
		return nil, fmt.Errorf("power: %w", err)
	}
	return math.Pow(a, b), nil
}

func invokeNegate(args []any) (any, error) {
	if err := requireArgs(args, 1); err != nil {
		return nil, err
	}
	a, err := toFloat64(args[0])
	if err != nil {
		return nil, fmt.Errorf("negate: %w", err)
	}
	return -a, nil
}

func invokeEqual(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	equal, err := deepEqual(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return equal, nil
}

func invokeNotEqual(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	equal, err := deepEqual(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return !equal, nil
}

func invokeGreaterThan(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	cmp, err := compareValues(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return cmp > 0, nil
}

func invokeLessThan(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	cmp, err := compareValues(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return cmp < 0, nil
}

func invokeGreaterThanOrEqual(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	cmp, err := compareValues(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return cmp >= 0, nil
}

func invokeLessThanOrEqual(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	cmp, err := compareValues(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return cmp <= 0, nil
}

func invokeAnd(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	return isTruthy(args[0]) && isTruthy(args[1]), nil
}

func invokeOr(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	return isTruthy(args[0]) || isTruthy(args[1]), nil
}

func invokeNot(args []any) (any, error) {
	if err := requireArgs(args, 1); err != nil {
		return nil, err
	}
	return !isTruthy(args[0]), nil
}

func invokeIn(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	needle := args[0]
	haystack := args[1]

	switch h := haystack.(type) {
	case []any:
		for _, item := range h {
			equal, err := deepEqual(needle, item)
			if err != nil {
				return nil, err
			}
			if equal {
				return true, nil
			}
		}
		return false, nil
	case string:
		s, ok := needle.(string)
		if !ok {
			return false, nil
		}
		return len(s) > 0 && len(h) > 0 && strings.Contains(h, s), nil
	case map[string]any:
		key, ok := needle.(string)
		if !ok {
			return false, nil
		}
		_, found := h[key]
		return found, nil
	case map[string]string:
		key, ok := needle.(string)
		if !ok {
			return false, nil
		}
		_, found := h[key]
		return found, nil
	default:
		return false, nil
	}
}

func invokeAppend(args []any) (any, error) {
	if err := requireArgs(args, 2); err != nil {
		return nil, err
	}
	list, ok := args[0].([]any)
	if !ok {
		return nil, fmt.Errorf("append: first argument must be a list, got %T", args[0])
	}
	return append(list, args[1]), nil
}

func invokeIfElse(args []any) (any, error) {
	if err := requireArgs(args, 3); err != nil {
		return nil, err
	}
	if isTruthy(args[0]) {
		return args[1], nil
	}
	return args[2], nil
}

func requireArgs(args []any, n int) error {
	if len(args) < n {
		return fmt.Errorf("expected %d arguments, got %d", n, len(args))
	}
	return nil
}

func toFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case float32:
		return float64(n), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to number", v)
	}
}

func toFloat64Pair(a, b any) (float64, float64, error) {
	fa, err := toFloat64(a)
	if err != nil {
		return 0, 0, err
	}
	fb, err := toFloat64(b)
	if err != nil {
		return 0, 0, err
	}
	return fa, fb, nil
}

const (
	maxCompareDepth = 10000
	maxCompareSteps = 1000000
)

var errCompareTooLarge = fmt.Errorf("values too large to compare: more than %d levels deep or more than %d values", maxCompareDepth, maxCompareSteps)

type comparison struct {
	steps int
}

func deepEqual(a, b any) (bool, error) {
	var c comparison
	return c.equal(a, b, 0)
}

func (c *comparison) equal(a, b any, depth int) (bool, error) {
	c.steps++
	if depth > maxCompareDepth || c.steps > maxCompareSteps {
		return false, errCompareTooLarge
	}
	fa, errA := toFloat64(a)
	fb, errB := toFloat64(b)
	if errA == nil && errB == nil {
		return fa == fb, nil
	}
	switch av := a.(type) {
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false, nil
		}
		for i := range av {
			equal, err := c.equal(av[i], bv[i], depth+1)
			if err != nil || !equal {
				return false, err
			}
		}
		return true, nil
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false, nil
		}
		for key, item := range av {
			other, present := bv[key]
			if !present {
				return false, nil
			}
			equal, err := c.equal(item, other, depth+1)
			if err != nil || !equal {
				return false, err
			}
		}
		return true, nil
	}
	return reflect.DeepEqual(a, b), nil
}

func compareValues(a, b any) (int, error) {
	fa, errA := toFloat64(a)
	fb, errB := toFloat64(b)
	if errA == nil && errB == nil {
		if math.IsNaN(fa) || math.IsNaN(fb) {
			return 0, fmt.Errorf("cannot compare NaN")
		}
		if fa < fb {
			return -1, nil
		}
		if fa > fb {
			return 1, nil
		}
		return 0, nil
	}

	sa, okA := a.(string)
	sb, okB := b.(string)
	if okA && okB {
		if sa < sb {
			return -1, nil
		}
		if sa > sb {
			return 1, nil
		}
		return 0, nil
	}

	return 0, fmt.Errorf("cannot compare %T and %T", a, b)
}

func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val != ""
	case int:
		return val != 0
	case float64:
		return val != 0
	case []any:
		return len(val) > 0
	case map[string]any:
		return len(val) > 0
	default:
		return true
	}
}
