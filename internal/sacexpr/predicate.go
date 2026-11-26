// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

const (
	MaxPredicateDepth       = 8
	MaxPredicateCalls       = 64
	MaxPredicateListLiteral = 1024
)

var predicateArity = map[string]int{
	"equal":                 2,
	"not_equal":             2,
	"greater_than":          2,
	"less_than":             2,
	"greater_than_or_equal": 2,
	"less_than_or_equal":    2,
	"and_op":                2,
	"or_op":                 2,
	"not_op":                1,
	"in_op":                 2,
	"if_else":               3,
	"subtract":              2,
	"multiply":              2,
	"divide":                2,
	"modulus":               2,
	"negate":                1,
	"power":                 2,
}

const (
	kindNull    = "null"
	kindText    = "text"
	kindNumber  = "number"
	kindBoolean = "boolean"
	kindList    = "list"
	kindObject  = "object"
	kindValue   = "value"

	filterResultLabel = "filter result"
)

const (
	evaluateExpressionName   = "evaluate_expression"
	evaluateExpressionPrefix = evaluateExpressionName + "("
	nestedInvokeKey          = "invoke"
)

var literalComparisons = map[string]bool{
	"equal":                 true,
	"not_equal":             true,
	"greater_than":          true,
	"less_than":             true,
	"greater_than_or_equal": true,
	"less_than_or_equal":    true,
	"in_op":                 true,
}

type Predicate struct {
	root *predicateCall
}

type predicateCall struct {
	function string
	args     []predicateArg
}

type predicateArg struct {
	literal    any
	expression string
	call       *predicateCall
}

func CompilePredicate(field string, raw any) (*Predicate, error) {
	c := predicateCompiler{}
	root, err := c.call(field, raw, 1)
	if err != nil {
		return nil, err
	}
	return &Predicate{root: root}, nil
}

type predicateCompiler struct {
	calls int
}

func (c *predicateCompiler) call(path string, raw any, depth int) (*predicateCall, error) {
	if depth > MaxPredicateDepth {
		return nil, fmt.Errorf("%s: nested deeper than %d calls", path, MaxPredicateDepth)
	}
	c.calls++
	if c.calls > MaxPredicateCalls {
		return nil, fmt.Errorf("%s: more than %d calls in one filter", path, MaxPredicateCalls)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: must be an object with function and params, got %s", path, describeType(raw))
	}
	for key := range m {
		if key != "function" && key != "params" && key != "module" {
			return nil, fmt.Errorf("%s: unknown key %q (allowed: function, params, module)", path, key)
		}
	}
	function, _ := m["function"].(string)
	arity, known := predicateArity[function]
	if !known {
		return nil, fmt.Errorf("%s.function: %q is not a filter function (allowed: %s)", path, function, strings.Join(slices.Sorted(maps.Keys(predicateArity)), ", "))
	}
	positional, err := positionalParams(path, m["params"])
	if err != nil {
		return nil, err
	}
	if len(positional) != arity {
		return nil, fmt.Errorf("%s: %s takes %d params, got %d", path, function, arity, len(positional))
	}
	call := &predicateCall{function: function, args: make([]predicateArg, len(positional))}
	for i, p := range positional {
		argPath := fmt.Sprintf("%s.params.positional[%d]", path, i)
		if call.args[i], err = c.arg(argPath, p, depth); err != nil {
			return nil, err
		}
	}
	if literalComparisons[function] && !slices.ContainsFunc(call.args, predicateArg.readsMessage) {
		return nil, fmt.Errorf("%s: %s compares only literals, so its answer never depends on the message", path, function)
	}
	return call, nil
}

func (a predicateArg) readsMessage() bool {
	return a.call != nil || a.expression != ""
}

func positionalParams(path string, raw any) ([]any, error) {
	params, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.params: must be an object with a positional list", path)
	}
	for key := range params {
		if key != "positional" {
			return nil, fmt.Errorf("%s.params: unknown key %q (a filter takes positional params only)", path, key)
		}
	}
	positional, ok := params["positional"].([]any)
	if !ok {
		return nil, fmt.Errorf("%s.params.positional: must be a list", path)
	}
	return positional, nil
}

func (c *predicateCompiler) arg(path string, raw any, depth int) (predicateArg, error) {
	switch v := raw.(type) {
	case map[string]any:
		inner, ok := v[nestedInvokeKey]
		if !ok || len(v) != 1 {
			return predicateArg{}, fmt.Errorf("%s: an object param must be {invoke: {...}}", path)
		}
		call, err := c.call(path+"."+nestedInvokeKey, inner, depth+1)
		return predicateArg{call: call}, err
	case string:
		return compileStringParam(path, v)
	case []any:
		if len(v) > MaxPredicateListLiteral {
			return predicateArg{}, fmt.Errorf("%s: list of %d items exceeds %d", path, len(v), MaxPredicateListLiteral)
		}
		items := make([]any, len(v))
		for i, item := range v {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			if item == nil {
				return predicateArg{}, nullParamError(itemPath)
			}
			if isNonFinite(item) {
				return predicateArg{}, fmt.Errorf("%s: a list param must hold finite numbers, not NaN or infinity", itemPath)
			}
			scalar, ok := scalarLiteral(item)
			if !ok {
				return predicateArg{}, fmt.Errorf("%s: a list param holds only strings, numbers or booleans, got %s", itemPath, describeType(item))
			}
			items[i] = scalar
		}
		return predicateArg{literal: items}, nil
	case nil:
		return predicateArg{}, nullParamError(path)
	default:
		if isNonFinite(v) {
			return predicateArg{}, fmt.Errorf("%s: a number param must be a finite number, not NaN or infinity", path)
		}
		scalar, ok := scalarLiteral(v)
		if !ok {
			return predicateArg{}, fmt.Errorf("%s: unsupported param type %s", path, describeType(v))
		}
		return predicateArg{literal: scalar}, nil
	}
}

func prefixError(label string, err error) error {
	return fmt.Errorf("%s: %w", label, err)
}

func nullParamError(path string) error {
	return fmt.Errorf("%s: a null param is not allowed; to test whether a key is present, use in_op with the key and evaluate_expression(input.payload:), which needs a JSON object payload", path)
}

func compileStringParam(path, s string) (predicateArg, error) {
	if strings.HasPrefix(s, evaluateExpressionPrefix) && strings.HasSuffix(s, ")") {
		expression := s[len(evaluateExpressionPrefix) : len(s)-1]
		if err := validateFilterExpression(expression); err != nil {
			return predicateArg{}, prefixError(path, err)
		}
		return predicateArg{expression: expression}, nil
	}
	if strings.Contains(strings.ToLower(s), evaluateExpressionName) {
		return predicateArg{}, fmt.Errorf("%s: a param that mentions %s must be written exactly as evaluate_expression(<expression>)", path, evaluateExpressionName)
	}
	if parseExpression(s).source != sourceUnknown {
		return predicateArg{}, fmt.Errorf("%s: %q would be read as a plain string; write evaluate_expression(%s) to read the message, or change the text", path, s, s)
	}
	return predicateArg{literal: s}, nil
}

func validateFilterExpression(expression string) error {
	parsed := parseExpression(expression)
	switch parsed.source {
	case sourceInputPayload, sourceInputTopic, sourceInputUserProps, sourceStatic:
		return nil
	case sourceInputTopicLvls:
		if parsed.path == "" {
			return nil
		}
		if _, err := strconv.Atoi(parsed.path); err != nil {
			return fmt.Errorf("expression %q: topic_levels index must be an integer", expression)
		}
		return nil
	default:
		return fmt.Errorf("expression %q: a filter reads only input.payload, input.topic, input.topic_levels, input.user_properties or static", expression)
	}
}

func isNonFinite(v any) bool {
	if n, ok := v.(json.Number); ok {
		f, _ := strconv.ParseFloat(string(n), 64)
		return math.IsNaN(f) || math.IsInf(f, 0)
	}
	f, ok := v.(float64)
	return ok && (math.IsNaN(f) || math.IsInf(f, 0))
}

func scalarLiteral(v any) (any, bool) {
	switch n := v.(type) {
	case nil, string, bool, int, int64:
		return v, true
	case float64:
		return v, !isNonFinite(v)
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return nil, false
	}
}

func describeType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "list"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func (p *Predicate) Match(ctx *Context) (matched bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			matched, err = false, errors.New("filter evaluation panicked")
		}
	}()
	v, err := p.root.eval(ctx)
	if err != nil {
		return false, err
	}
	return requireBoolean(filterResultLabel, v)
}

func requireBoolean(operator string, v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s: needs true or false, got %s", operator, withArticle(kindName(v)))
	}
	return b, nil
}

func withArticle(kind string) string {
	if kind == kindObject {
		return "an " + kind
	}
	return "a " + kind
}

func (c *predicateCall) eval(ctx *Context) (any, error) {
	switch c.function {
	case "and_op":
		return c.shortCircuit(ctx, false)
	case "or_op":
		return c.shortCircuit(ctx, true)
	case "if_else":
		cond, err := c.evalBoolean(0, ctx)
		if err != nil {
			return nil, err
		}
		if cond {
			return c.evalArg(1, ctx)
		}
		return c.evalArg(2, ctx)
	}
	args := make([]any, len(c.args))
	for i := range c.args {
		v, err := c.evalArg(i, ctx)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	if err := checkOperandKinds(c.function, args); err != nil {
		return nil, err
	}
	if c.function == "not_op" {
		b, err := requireBoolean(c.function, args[0])
		return !b, err
	}
	v, err := CallInvoke(c.function, args)
	if err != nil {
		return nil, prefixError(c.function, err)
	}
	if isNonFinite(v) {
		return nil, fmt.Errorf("%s: result is not a finite number", c.function)
	}
	return v, nil
}

func (c *predicateCall) evalArg(i int, ctx *Context) (any, error) {
	v, err := c.args[i].eval(ctx)
	if err != nil && c.args[i].call == nil {
		return nil, prefixError(c.function, err)
	}
	return v, err
}

func (c *predicateCall) evalBoolean(i int, ctx *Context) (bool, error) {
	v, err := c.evalArg(i, ctx)
	if err != nil {
		return false, err
	}
	return requireBoolean(c.function, v)
}

func checkOperandKinds(function string, args []any) error {
	switch function {
	case "equal", "not_equal":
		if left, right := kindName(args[0]), kindName(args[1]); left != right {
			return fmt.Errorf("%s: cannot compare %s with %s", function, withArticle(left), withArticle(right))
		}
		return checkListItemKinds(function, args[0], args[1])
	case "in_op":
		return checkMembershipKinds(args[0], args[1])
	}
	return nil
}

func checkListItemKinds(function string, left, right any) error {
	leftItems, leftIsList := left.([]any)
	rightItems, rightIsList := right.([]any)
	if !leftIsList || !rightIsList {
		return nil
	}
	for i := range min(len(leftItems), len(rightItems)) {
		if l, r := kindName(leftItems[i]), kindName(rightItems[i]); l != r {
			return fmt.Errorf("%s: cannot compare %s item with %s item", function, withArticle(l), withArticle(r))
		}
	}
	return nil
}

func checkMembershipKinds(needle, haystack any) error {
	needleKind := kindName(needle)
	switch haystack := haystack.(type) {
	case []any:
		return checkListMembership(needleKind, haystack)
	case string, map[string]any, map[string]string:
		kind := kindName(haystack)
		if needleKind != kindText {
			return fmt.Errorf("in_op: cannot look for %s in %s", withArticle(needleKind), withArticle(kind))
		}
		return nil
	default:
		return fmt.Errorf("in_op: cannot look inside %s", withArticle(kindName(haystack)))
	}
}

func checkListMembership(needleKind string, items []any) error {
	if needleKind == kindList || needleKind == kindObject {
		return fmt.Errorf("in_op: cannot look for %s in a list", withArticle(needleKind))
	}
	kinds := map[string]bool{}
	for _, item := range items {
		if item != nil {
			kinds[kindName(item)] = true
		}
	}
	if len(kinds) == 0 || kinds[needleKind] {
		return nil
	}
	plural := make([]string, 0, len(kinds))
	for kind := range kinds {
		plural = append(plural, pluralKind(kind))
	}
	slices.Sort(plural)
	return fmt.Errorf("in_op: cannot look for %s in a list of %s", withArticle(needleKind), strings.Join(plural, " and "))
}

func pluralKind(kind string) string {
	if kind == kindText {
		return kind
	}
	return kind + "s"
}

func kindName(v any) string {
	if v == nil {
		return kindNull
	}
	switch reflect.TypeOf(v).Kind() {
	case reflect.String:
		return kindText
	case reflect.Bool:
		return kindBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return kindNumber
	case reflect.Slice, reflect.Array:
		return kindList
	case reflect.Map, reflect.Struct:
		return kindObject
	default:
		return kindValue
	}
}

func isReadableValue(v any) bool {
	switch v.(type) {
	case nil, string, bool, int, int64, float64, []any, map[string]any, map[string]string:
		return true
	default:
		return false
	}
}

func (c *predicateCall) shortCircuit(ctx *Context, decidedBy bool) (any, error) {
	first, err := c.evalBoolean(0, ctx)
	if err != nil {
		return nil, err
	}
	if first == decidedBy {
		return decidedBy, nil
	}
	return c.evalBoolean(1, ctx)
}

func (a *predicateArg) eval(ctx *Context) (any, error) {
	switch {
	case a.call != nil:
		return a.call.eval(ctx)
	case a.expression != "":
		v, err := ctx.GetData(a.expression)
		if err != nil {
			return nil, err
		}
		if v == nil {
			return nil, fmt.Errorf("%s has no value in this message", a.expression)
		}
		if !isReadableValue(v) {
			return nil, unreadableValueError(a.expression, v)
		}
		return v, nil
	default:
		return a.literal, nil
	}
}

func unreadableValueError(expression string, v any) error {
	err := fmt.Errorf("%s is %s a filter cannot use as a whole", expression, withArticle(kindName(v)))
	if parseExpression(expression).source == sourceInputTopicLvls {
		return fmt.Errorf("%w; read one level with input.topic_levels:<index>", err)
	}
	return err
}
