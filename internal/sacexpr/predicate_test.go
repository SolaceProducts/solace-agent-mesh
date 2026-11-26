// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func mustDecodePredicate(t *testing.T, src string) any {
	t.Helper()
	var raw any
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return raw
}

func mustCompilePredicate(t *testing.T, src string) *Predicate {
	t.Helper()
	p, err := CompilePredicate("event_filter", mustDecodePredicate(t, src))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return p
}

const orderCreatedOverThreshold = `
function: and_op
params:
  positional:
    - invoke:
        function: equal
        params:
          positional: ["evaluate_expression(input.payload:type)", "order.created"]
    - invoke:
        function: greater_than
        params:
          positional: ["evaluate_expression(input.payload:order.total)", 1000]
`

func TestPredicate_MatchesNestedPayloadFields(t *testing.T) {
	p := mustCompilePredicate(t, orderCreatedOverThreshold)

	cases := []struct {
		name    string
		payload any
		want    bool
	}{
		{"both hold", map[string]any{"type": "order.created", "order": map[string]any{"total": 1500.0}}, true},
		{"total too low", map[string]any{"type": "order.created", "order": map[string]any{"total": 10.0}}, false},
		{"other type", map[string]any{"type": "order.cancelled", "order": map[string]any{"total": 1500.0}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.Match(NewContext(tc.payload, "orders/eu", nil))
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			if got != tc.want {
				t.Errorf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPredicate_AndShortCircuitsSoAGuardPreventsTheError(t *testing.T) {
	p := mustCompilePredicate(t, orderCreatedOverThreshold)

	got, err := p.Match(NewContext(map[string]any{"type": "customer.updated"}, "", nil))
	if err != nil {
		t.Fatalf("a false first operand must not evaluate the comparison on the missing field: %v", err)
	}
	if got {
		t.Error("Match = true, want false")
	}
}

func TestPredicate_EvaluationErrorOnMissingFieldOrdering(t *testing.T) {
	p := mustCompilePredicate(t, `
function: greater_than
params:
  positional: ["evaluate_expression(input.payload:amount)", 1000]
`)
	_, err := p.Match(NewContext(map[string]any{"other": 1.0}, "", nil))
	if err == nil {
		t.Fatal("ordering a missing field must be an evaluation error")
	}
	if !strings.Contains(err.Error(), "greater_than") {
		t.Errorf("error %q should name the function", err)
	}
}

func TestPredicate_UndecidableEventIsAnErrorWhateverTheFunction(t *testing.T) {
	filters := map[string]string{
		"equal":               `{function: equal, params: {positional: ["evaluate_expression(input.payload:type)", "order"]}}`,
		"not_equal":           `{function: not_equal, params: {positional: ["evaluate_expression(input.payload:type)", "order"]}}`,
		"greater_than":        `{function: greater_than, params: {positional: ["evaluate_expression(input.payload:total)", 1000]}}`,
		"in_op":               `{function: in_op, params: {positional: ["evaluate_expression(input.payload:type)", ["order", "refund"]]}}`,
		"not_op of equal":     `{function: not_op, params: {positional: [{invoke: {function: equal, params: {positional: ["evaluate_expression(input.payload:type)", "order"]}}}]}}`,
		"or_op first":         `{function: or_op, params: {positional: [{invoke: {function: equal, params: {positional: ["evaluate_expression(input.payload:type)", "order"]}}}, true]}}`,
		"or_op second":        flowCall("or_op", "false", nested(flowCall("greater_than", readsInput("input.payload:total"), "1000"))),
		"and_op second":       flowCall("and_op", "true", nested(flowCall("greater_than", readsInput("input.payload:total"), "1000"))),
		"if_else condition":   flowCall("if_else", nested(flowCall("equal", readsInput("input.payload:type"), `"order"`)), "true", "false"),
		"if_else chosen then": flowCall("if_else", "true", nested(flowCall("greater_than", readsInput("input.payload:total"), "1000")), "false"),
		"if_else chosen else": flowCall("if_else", "false", "true", nested(flowCall("greater_than", readsInput("input.payload:total"), "1000"))),
		"arithmetic":          flowCall("greater_than", nested(flowCall("subtract", readsInput("input.payload:total"), "1")), "0"),
		"negate":              flowCall("less_than", nested(flowCall("negate", readsInput("input.payload:total"))), "0"),
	}
	payloads := map[string]any{
		"missing field":        map[string]any{"other": "x"},
		"null field":           map[string]any{"type": nil, "total": nil},
		"empty payload":        nil,
		"non-JSON payload":     "not json at all",
		"JSON array payload":   []any{1.0, 2.0},
		"field of wrong shape": map[string]any{"type": map[string]any{"nested": true}, "total": "lots"},
	}
	for fname, src := range filters {
		p := mustCompilePredicate(t, src)
		for pname, payload := range payloads {
			t.Run(fname+"/"+pname, func(t *testing.T) {
				matched, err := p.Match(NewContext(payload, "orders/eu", nil))
				if err == nil {
					t.Fatalf("Match = %v with no error; an event the filter cannot decide must be an evaluation error", matched)
				}
			})
		}
	}
}

func flowCall(function string, params ...string) string {
	return "{function: " + function + ", params: {positional: [" + strings.Join(params, ", ") + "]}}"
}

func readsInput(expression string) string {
	return `"evaluate_expression(` + expression + `)"`
}

func nested(call string) string {
	return "{invoke: " + call + "}"
}

func TestPredicate_EqualOnMismatchedTypesIsUndecidable(t *testing.T) {
	for _, function := range []string{"equal", "not_equal"} {
		p := mustCompilePredicate(t, `{function: `+function+`, params: {positional: ["evaluate_expression(input.payload:code)", "500"]}}`)
		if _, err := p.Match(NewContext(map[string]any{"code": 500.0}, "", nil)); err == nil {
			t.Errorf("%s of a number against a string literal must be an evaluation error", function)
		}
	}
}

func TestPredicate_DecidableEventsStillDecideAfterTheUndecidableRule(t *testing.T) {
	p := mustCompilePredicate(t, `{function: equal, params: {positional: ["evaluate_expression(input.payload:type)", "order"]}}`)
	for payload, want := range map[string]bool{"order": true, "refund": false, "": false} {
		got, err := p.Match(NewContext(map[string]any{"type": payload}, "", nil))
		if err != nil || got != want {
			t.Errorf("type %q: Match = %v, %v; want %v, nil", payload, got, err, want)
		}
	}
}

func TestPredicate_ErrorTextCarriesNoMessageValues(t *testing.T) {
	p := mustCompilePredicate(t, `
function: greater_than
params:
  positional: ["evaluate_expression(input.payload:amount)", 1000]
`)
	const secret = "CANARY-value-in-payload"
	_, err := p.Match(NewContext(map[string]any{"amount": secret}, "", nil))
	if err == nil {
		t.Fatal("comparing a string to a number must fail")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error %q carries the message value", err)
	}
}

func TestPredicate_TopicLevelsAndUserProperties(t *testing.T) {
	p := mustCompilePredicate(t, `
function: or_op
params:
  positional:
    - invoke:
        function: equal
        params:
          positional: ["evaluate_expression(input.topic_levels:1)", "priority"]
    - invoke:
        function: in_op
        params:
          positional: ["evaluate_expression(input.user_properties:region)", ["eu", "uk"]]
`)
	cases := []struct {
		name  string
		topic string
		props map[string]string
		want  bool
	}{
		{"topic level matches", "orders/priority/new", nil, true},
		{"user property in list", "orders/normal/new", map[string]string{"region": "uk"}, true},
		{"neither", "orders/normal/new", map[string]string{"region": "us"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.Match(NewContext(nil, tc.topic, tc.props))
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			if got != tc.want {
				t.Errorf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPredicate_TextPayloadSubstring(t *testing.T) {
	p := mustCompilePredicate(t, `
function: in_op
params:
  positional: ["URGENT", "evaluate_expression(input.payload:)"]
`)
	matched, err := p.Match(NewContext("URGENT: disk full", "", nil))
	if err != nil || !matched {
		t.Errorf("Match = %v, %v; want true, nil", matched, err)
	}
	matched, err = p.Match(NewContext("routine heartbeat", "", nil))
	if err != nil || matched {
		t.Errorf("Match = %v, %v; want false, nil", matched, err)
	}
}

func TestPredicate_IfElseEvaluatesOnlyTheChosenBranch(t *testing.T) {
	p := mustCompilePredicate(t, `
function: if_else
params:
  positional:
    - invoke:
        function: in_op
        params:
          positional: ["amount", "evaluate_expression(input.payload:)"]
    - invoke:
        function: greater_than
        params:
          positional: ["evaluate_expression(input.payload:amount)", 10]
    - false
`)
	got, err := p.Match(NewContext(map[string]any{"note": "no amount"}, "", nil))
	if err != nil || got {
		t.Errorf("Match = %v, %v; want false, nil", got, err)
	}
	got, err = p.Match(NewContext(map[string]any{"amount": 11.0}, "", nil))
	if err != nil || !got {
		t.Errorf("Match = %v, %v; want true, nil", got, err)
	}
}

func TestCompilePredicate_Refusals(t *testing.T) {
	deep := "function: not_op\nparams:\n  positional: [true]\n"
	for range MaxPredicateDepth {
		deep = "function: not_op\nparams:\n  positional:\n    - invoke:\n" + indent(deep, 8)
	}
	wide := make([]string, 0, MaxPredicateListLiteral+1)
	for range MaxPredicateListLiteral + 1 {
		wide = append(wide, "x")
	}

	cases := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"not an object", `"evaluate_expression(input.payload:x)"`, "must be an object"},
		{"unknown function", "function: shell\nparams: {positional: []}", `"shell" is not a filter function`},
		{"uuid is not deterministic", "function: uuid\nparams: {positional: []}", `"uuid" is not a filter function`},
		{"wrong arity", "function: equal\nparams: {positional: [1]}", "equal takes 2 params, got 1"},
		{"keyword params", "function: not_op\nparams: {keyword: {a: true}}", "positional params only"},
		{"missing params", "function: not_op", "params: must be an object"},
		{"unknown key", "function: not_op\nparams: {positional: [true]}\nfuction: x", `unknown key "fuction"`},
		{"user_data source", `{function: not_op, params: {positional: ["evaluate_expression(user_data.x:y)"]}}`, "a filter reads only"},
		{"keyword_args source", `{function: not_op, params: {positional: ["evaluate_expression(keyword_args:index)"]}}`, "a filter reads only"},
		{"template source", `{function: not_op, params: {positional: ["evaluate_expression(template:{{text://input.topic}})"]}}`, "a filter reads only"},
		{"non-integer topic level", `{function: not_op, params: {positional: ["evaluate_expression(input.topic_levels:x)"]}}`, "must be an integer"},
		{"object literal", `{function: not_op, params: {positional: [{a: 1}]}}`, "must be {invoke: {...}}"},
		{"nested list", `{function: in_op, params: {positional: [1, [[1]]]}}`, "a list param holds only"},
		{"too deep", deep, "nested deeper than"},
		{"list too long", `{function: in_op, params: {positional: ["x", [` + strings.Join(wide, ",") + `]]}}`, "exceeds"},
		{"evaluate_expression missing its closing paren", `{function: equal, params: {positional: ["evaluate_expression(input.payload:type", "order"]}}`, "evaluate_expression(<expression>)"},
		{"evaluate_expression with a space", `{function: equal, params: {positional: ["evaluate_expression (input.payload:type)", "order"]}}`, "evaluate_expression(<expression>)"},
		{"evaluate_expression in the wrong case", `{function: equal, params: {positional: ["Evaluate_Expression(input.payload:type)", "order"]}}`, "evaluate_expression(<expression>)"},
		{"evaluate_expression with leading text", `{function: equal, params: {positional: [" evaluate_expression(input.payload:type)", "order"]}}`, "evaluate_expression(<expression>)"},
		{"bare expression without the wrapper", `{function: equal, params: {positional: ["input.payload:type", "order"]}}`, "write evaluate_expression(input.payload:type)"},
		{"bare static expression", `{function: equal, params: {positional: ["static:order", "order"]}}`, "write evaluate_expression(static:order)"},
		{"equal of two literals", `{function: equal, params: {positional: ["order", "order"]}}`, "compares only literals"},
		{"greater_than of two literals", `{function: greater_than, params: {positional: [5, 1000]}}`, "compares only literals"},
		{"in_op of two literals", `{function: in_op, params: {positional: ["a", ["a", "b"]]}}`, "compares only literals"},
		{"equal against a null literal", `{function: equal, params: {positional: ["evaluate_expression(input.payload:a)", null]}}`, "a null param is not allowed"},
		{"not_equal against a null literal", `{function: not_equal, params: {positional: [null, "evaluate_expression(input.payload:a)"]}}`, "a null param is not allowed"},
		{"in_op over a null literal", `{function: in_op, params: {positional: ["evaluate_expression(input.payload:a)", null]}}`, "a null param is not allowed"},
		{"not_op of a null literal", `{function: not_op, params: {positional: [null]}}`, "a null param is not allowed"},
		{"null inside a list literal", `{function: in_op, params: {positional: ["evaluate_expression(input.payload:a)", ["x", null]]}}`, "event_filter.params.positional[1][1]: a null param is not allowed"},
		{"nested null literal", `{function: and_op, params: {positional: [true, {invoke: {function: equal, params: {positional: ["evaluate_expression(input.payload:a)", null]}}}]}}`, "event_filter.params.positional[1].invoke.params.positional[1]: a null param is not allowed"},
		{"null literal error names the presence test", `{function: equal, params: {positional: ["evaluate_expression(input.payload:a)", null]}}`, `in_op with the key and evaluate_expression(input.payload:)`},
		{"null literal error says the presence test needs an object payload", `{function: equal, params: {positional: ["evaluate_expression(input.payload:a)", null]}}`, "which needs a JSON object payload"},
		{"nan literal", `{function: greater_than, params: {positional: ["evaluate_expression(input.payload:x)", .nan]}}`, "must be a finite number"},
		{"inf literal", `{function: greater_than, params: {positional: ["evaluate_expression(input.payload:x)", .inf]}}`, "must be a finite number"},
		{"negative inf literal", `{function: less_than, params: {positional: ["evaluate_expression(input.payload:x)", -.inf]}}`, "must be a finite number"},
		{"nan inside a list literal", `{function: in_op, params: {positional: ["evaluate_expression(input.payload:x)", [1, .nan]]}}`, "event_filter.params.positional[1][1]: a list param must hold finite numbers"},
		{"inf inside a list literal", `{function: in_op, params: {positional: ["evaluate_expression(input.payload:x)", [.inf]]}}`, "event_filter.params.positional[1][0]: a list param must hold finite numbers"},
		{"nested unknown function", `{function: not_op, params: {positional: [{invoke: {function: nope, params: {positional: []}}}]}}`, "event_filter.params.positional[0].invoke.function"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompilePredicate("event_filter", mustDecodePredicate(t, tc.src))
			if err == nil {
				t.Fatalf("compile accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCompilePredicate_RefusesANonFiniteJSONNumberLiteral(t *testing.T) {
	const (
		scalarProblem = "event_filter.params.positional[1]: a number param must be a finite number, not NaN or infinity"
		listProblem   = "event_filter.params.positional[1][1]: a list param must hold finite numbers, not NaN or infinity"
	)
	read := "evaluate_expression(input.payload:x)"
	compare := func(literal any) map[string]any {
		return map[string]any{"function": "greater_than", "params": map[string]any{"positional": []any{read, literal}}}
	}
	membership := func(literal any) map[string]any {
		return map[string]any{"function": "in_op", "params": map[string]any{"positional": []any{read, []any{1, literal}}}}
	}
	cases := []struct {
		name    string
		raw     map[string]any
		wantErr string
	}{
		{"NaN", compare(json.Number("NaN")), scalarProblem},
		{"Inf", compare(json.Number("Inf")), scalarProblem},
		{"+Inf", compare(json.Number("+Inf")), scalarProblem},
		{"-Inf", compare(json.Number("-Inf")), scalarProblem},
		{"infinity", compare(json.Number("infinity")), scalarProblem},
		{"a number too large for a float", compare(json.Number("1e999")), scalarProblem},
		{"a negative number too large for a float", compare(json.Number("-1e999")), scalarProblem},
		{"NaN inside a list", membership(json.Number("NaN")), listProblem},
		{"Inf inside a list", membership(json.Number("Inf")), listProblem},
		{"a number too large for a float inside a list", membership(json.Number("1e999")), listProblem},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompilePredicate("event_filter", tc.raw)

			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("CompilePredicate error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPredicate_ANonFiniteNumberIsUndecidable(t *testing.T) {
	comparisons := []string{"greater_than", "greater_than_or_equal", "less_than", "less_than_or_equal", "equal", "not_equal"}
	sources := map[string]string{
		"power of a negative base":      flowCall("power", readsInput("input.payload:negative"), "0.5"),
		"power overflowing":             flowCall("power", readsInput("input.payload:big"), "2"),
		"subtract overflowing":          flowCall("subtract", readsInput("input.payload:negative_big"), readsInput("input.payload:big")),
		"multiply overflowing":          flowCall("multiply", readsInput("input.payload:big"), "10"),
		"divide overflowing":            flowCall("divide", readsInput("input.payload:big"), readsInput("input.payload:tiny")),
		"infinity minus infinity":       flowCall("subtract", nested(flowCall("multiply", readsInput("input.payload:big"), "10")), nested(flowCall("multiply", readsInput("input.payload:big"), "10"))),
		"nested inside another formula": flowCall("subtract", "1", nested(flowCall("power", readsInput("input.payload:negative"), "0.5"))),
	}
	payload := map[string]any{"negative": -4.0, "big": 1e308, "negative_big": -1e308, "tiny": 1e-308}
	for sname, source := range sources {
		for _, comparison := range comparisons {
			t.Run(sname+"/"+comparison, func(t *testing.T) {
				p := mustCompilePredicate(t, flowCall(comparison, nested(source), "0"))
				matched, err := p.Match(NewContext(payload, "", nil))
				if err == nil {
					t.Fatalf("Match = %v with no error; a result that is not a finite number must be an evaluation error", matched)
				}
				if !strings.Contains(err.Error(), "not a finite number") {
					t.Errorf("error %q does not say the result is not a finite number", err)
				}
			})
		}
	}
}

func TestPredicate_FiniteArithmeticStillDecides(t *testing.T) {
	p := mustCompilePredicate(t, flowCall("greater_than_or_equal", nested(flowCall("power", readsInput("input.payload:x"), "0.5")), "0"))
	matched, err := p.Match(NewContext(map[string]any{"x": 4.0}, "", nil))
	if err != nil || !matched {
		t.Errorf("Match = %v, %v; want true, nil", matched, err)
	}
}

func TestPredicate_ANaNInTheMessageIsUndecidableUnderEveryComparison(t *testing.T) {
	for _, comparison := range []string{"greater_than", "greater_than_or_equal", "less_than", "less_than_or_equal"} {
		p := mustCompilePredicate(t, flowCall(comparison, readsInput("input.payload:x"), "0"))
		matched, err := p.Match(NewContext(map[string]any{"x": math.NaN()}, "", nil))
		if err == nil {
			t.Errorf("%s of NaN: Match = %v with no error; want an evaluation error", comparison, matched)
		}
	}
}

func TestCompilePredicate_AcceptsALiteralBesideAnExpression(t *testing.T) {
	for _, src := range []string{
		`{function: equal, params: {positional: ["evaluate_expression(input.payload:type)", "order"]}}`,
		`{function: equal, params: {positional: ["order", "evaluate_expression(input.payload:type)"]}}`,
		`{function: not_op, params: {positional: [true]}}`,
		`{function: equal, params: {positional: ["evaluate_expression(static:order)", "order"]}}`,
	} {
		if _, err := CompilePredicate("event_filter", mustDecodePredicate(t, src)); err != nil {
			t.Errorf("compile refused %s: %v", src, err)
		}
	}
}

func TestCompilePredicate_RefusesTooManyCalls(t *testing.T) {
	leaf := map[string]any{"function": "not_op", "params": map[string]any{"positional": []any{true}}}
	level := func(a, b any) map[string]any {
		return map[string]any{"function": "and_op", "params": map[string]any{"positional": []any{
			map[string]any{"invoke": a}, map[string]any{"invoke": b},
		}}}
	}
	var tree any = leaf
	for range 6 {
		tree = level(tree, tree)
	}
	if _, err := CompilePredicate("event_filter", tree); err == nil || !strings.Contains(err.Error(), "calls in one filter") {
		t.Fatalf("a 127-call filter must be refused, got %v", err)
	}
}

func TestPredicate_JSONNumberLiteralsCompareAsNumbers(t *testing.T) {
	var raw any
	dec := json.NewDecoder(strings.NewReader(`{"function": "in_op", "params": {"positional": ["evaluate_expression(input.payload:code)", [404, 500]]}}`))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	p, err := CompilePredicate("event_filter", raw)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	matched, err := p.Match(NewContext(map[string]any{"code": 500.0}, "", nil))
	if err != nil || !matched {
		t.Errorf("Match = %v, %v; want true, nil", matched, err)
	}
}

func TestPredicateArity_IsASubsetOfTheInvokeRegistry(t *testing.T) {
	for name := range predicateArity {
		if _, ok := invokeRegistry[name]; !ok {
			t.Errorf("predicate function %q is not an invoke function", name)
		}
	}
}

func TestPredicate_SubstringSearchOnHostileSizesIsLinear(t *testing.T) {
	p := mustCompilePredicate(t, `
function: in_op
params:
  positional: ["evaluate_expression(input.payload:needle)", "evaluate_expression(input.payload:haystack)"]
`)
	payload := map[string]any{
		"needle":   strings.Repeat("a", 1<<19) + "b",
		"haystack": strings.Repeat("a", 1<<20),
	}
	start := time.Now()
	matched, err := p.Match(NewContext(payload, "", nil))
	if err != nil || matched {
		t.Fatalf("Match = %v, %v; want false, nil", matched, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("substring search over 1 MiB took %v; it must not be quadratic", elapsed)
	}
}

func TestPredicate_InOpOnUserPropertiesIsAPresenceTest(t *testing.T) {
	presence := flowCall("in_op", `"region"`, readsInput("input.user_properties:"))
	absence := flowCall("not_op", nested(presence))
	cases := []struct {
		name   string
		filter string
		props  map[string]string
		want   bool
	}{
		{"present", presence, map[string]string{"region": "eu"}, true},
		{"absent among others", presence, map[string]string{"tier": "gold"}, false},
		{"no properties at all", presence, nil, false},
		{"absence test, present", absence, map[string]string{"region": "eu"}, false},
		{"absence test, absent", absence, map[string]string{"tier": "gold"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mustCompilePredicate(t, tc.filter).Match(NewContext(nil, "orders/eu", tc.props))
			if err != nil || got != tc.want {
				t.Errorf("Match = %v, %v; want %v, nil", got, err, tc.want)
			}
		})
	}
}

func TestPredicate_AGuardedUserPropertyComparison(t *testing.T) {
	filter := flowCall("and_op",
		nested(flowCall("in_op", `"region"`, readsInput("input.user_properties:"))),
		nested(flowCall("equal", readsInput("input.user_properties:region"), `"eu"`)))
	for props, want := range map[string]bool{"eu": true, "us": false, "": false} {
		m := map[string]string{}
		if props != "" {
			m["region"] = props
		}
		got, err := mustCompilePredicate(t, filter).Match(NewContext(nil, "", m))
		if err != nil || got != want {
			t.Errorf("region %q: Match = %v, %v; want %v, nil", props, got, err, want)
		}
	}
}

func TestPredicate_AWholeValueTheFilterCannotReadIsAnErrorInUserTerms(t *testing.T) {
	filters := map[string]string{
		"in_op over bare topic levels":      flowCall("in_op", `"priority"`, readsInput("input.topic_levels:")),
		"equal of bare topic levels":        flowCall("equal", readsInput("input.topic_levels:"), `"priority"`),
		"in_op needle of bare topic levels": flowCall("in_op", readsInput("input.topic_levels:"), `["a"]`),
	}
	for name, src := range filters {
		t.Run(name, func(t *testing.T) {
			_, err := mustCompilePredicate(t, src).Match(NewContext(nil, "orders/priority/new", nil))
			if err == nil {
				t.Fatal("a bare list of topic levels must be an evaluation error")
			}
			for _, leak := range []string{"[]string", "[]interface", "map[", "float64", "%!"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q leaks the Go type %q", err, leak)
				}
			}
			const want = "input.topic_levels: is a list a filter cannot use as a whole; read one level with input.topic_levels:<index>"
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should contain %q", err, want)
			}
		})
	}
}

func TestPredicate_KindMismatchIsUndecidable(t *testing.T) {
	payload := map[string]any{
		"n": 5.0, "s": "abc", "b": true, "o": map[string]any{"a": 1.0}, "l": []any{"a", "b"},
		"nums": []any{1.0, 2.0}, "mixed": []any{"a", 1.0, nil}, "objs": []any{map[string]any{"a": 1.0}},
		"lists": []any{[]any{"a"}},
	}
	in := func(needle, haystack string) string {
		return flowCall("in_op", needle, haystack)
	}
	cases := []struct {
		name    string
		filter  string
		wantErr string
	}{
		{"number in a list of text", in(readsInput("input.payload:n"), `["a", "b"]`), "cannot look for a number in a list of text"},
		{"text in a list of numbers", in(readsInput("input.payload:s"), `[1, 2]`), "cannot look for a text in a list of numbers"},
		{"boolean in a list of text", in(readsInput("input.payload:b"), `["a"]`), "cannot look for a boolean in a list of text"},
		{"number in a list of text from the payload", in(readsInput("input.payload:n"), readsInput("input.payload:l")), "cannot look for a number in a list of text"},
		{"text in a list of numbers from the payload", in(readsInput("input.payload:s"), readsInput("input.payload:nums")), "cannot look for a text in a list of numbers"},
		{"text in a list of objects", in(readsInput("input.payload:s"), readsInput("input.payload:objs")), "cannot look for a text in a list of objects"},
		{"text in a list of lists", in(readsInput("input.payload:s"), readsInput("input.payload:lists")), "cannot look for a text in a list of lists"},
		{"list in a list of lists", in(readsInput("input.payload:l"), readsInput("input.payload:lists")), "cannot look for a list in a list"},
		{"object in a list of objects", in(readsInput("input.payload:o"), readsInput("input.payload:objs")), "cannot look for an object in a list"},
		{"boolean in a mixed list", in(readsInput("input.payload:b"), readsInput("input.payload:mixed")), "cannot look for a boolean in a list of numbers and text"},
		{"number in a text", in(readsInput("input.payload:n"), readsInput("input.payload:s")), "cannot look for a number in a text"},
		{"boolean in an object", in(readsInput("input.payload:b"), readsInput("input.payload:o")), "cannot look for a boolean in an object"},
		{"object in an object", in(readsInput("input.payload:o"), readsInput("input.payload:o")), "cannot look for an object in an object"},
		{"list in a list", in(readsInput("input.payload:l"), readsInput("input.payload:l")), "cannot look for a list in a list"},
		{"inside a number", in(`"a"`, readsInput("input.payload:n")), "cannot look inside a number"},
		{"inside a boolean", in(`"a"`, readsInput("input.payload:b")), "cannot look inside a boolean"},
		{"and_op first operand text", flowCall("and_op", readsInput("input.payload:s"), "true"), "and_op: needs true or false, got a text"},
		{"and_op second operand number", flowCall("and_op", "true", readsInput("input.payload:n")), "and_op: needs true or false, got a number"},
		{"or_op first operand list", flowCall("or_op", readsInput("input.payload:l"), "true"), "or_op: needs true or false, got a list"},
		{"or_op second operand object", flowCall("or_op", "false", readsInput("input.payload:o")), "or_op: needs true or false, got an object"},
		{"not_op number", flowCall("not_op", readsInput("input.payload:n")), "not_op: needs true or false, got a number"},
		{"not_op text", flowCall("not_op", readsInput("input.payload:s")), "not_op: needs true or false, got a text"},
		{"if_else condition text", flowCall("if_else", readsInput("input.payload:s"), "true", "false"), "if_else: needs true or false, got a text"},
		{"if_else condition from a nested number", flowCall("if_else", nested(flowCall("subtract", readsInput("input.payload:n"), "1")), "true", "false"), "if_else: needs true or false, got a number"},
		{"root text", flowCall("if_else", "true", readsInput("input.payload:s"), "false"), "filter result: needs true or false, got a text"},
		{"root number", flowCall("subtract", readsInput("input.payload:n"), "1"), "filter result: needs true or false, got a number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mustCompilePredicate(t, tc.filter).Match(NewContext(payload, "", nil))
			if err == nil {
				t.Fatalf("Match = %v with no error; want an evaluation error containing %q", got, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestPredicate_KindsThatMatchStillDecide(t *testing.T) {
	payload := map[string]any{
		"n": 5.0, "s": "abc", "b": true, "off": false, "nums": []any{1.0, 5.0}, "mixed": []any{"a", 1.0, nil}, "empty": []any{}, "nulls": []any{nil, nil},
		"o": map[string]any{"a": 1.0},
	}
	in := func(needle, haystack string) string { return flowCall("in_op", needle, haystack) }
	cases := []struct {
		name   string
		filter string
		want   bool
	}{
		{"number in numbers", in(readsInput("input.payload:n"), readsInput("input.payload:nums")), true},
		{"number not in numbers", in("7", readsInput("input.payload:nums")), false},
		{"text in a mixed list", in(`"a"`, readsInput("input.payload:mixed")), true},
		{"number in a mixed list", in("1", readsInput("input.payload:mixed")), true},
		{"text not in a mixed list", in(`"z"`, readsInput("input.payload:mixed")), false},
		{"text in a list of only nulls", in(`"a"`, readsInput("input.payload:nulls")), false},
		{"anything in an empty list", in(`"a"`, readsInput("input.payload:empty")), false},
		{"key in an object", in(`"a"`, readsInput("input.payload:o")), true},
		{"key not in an object", in(`"z"`, readsInput("input.payload:o")), false},
		{"substring of a text", in(`"bc"`, readsInput("input.payload:s")), true},
		{"and_op of booleans", flowCall("and_op", readsInput("input.payload:b"), "true"), true},
		{"and_op with a false boolean", flowCall("and_op", readsInput("input.payload:off"), "true"), false},
		{"or_op of booleans", flowCall("or_op", readsInput("input.payload:off"), readsInput("input.payload:b")), true},
		{"not_op of a boolean", flowCall("not_op", readsInput("input.payload:off")), true},
		{"if_else on a boolean", flowCall("if_else", readsInput("input.payload:b"), "true", "false"), true},
		{"root boolean field read through if_else", flowCall("if_else", "true", readsInput("input.payload:off"), "true"), false},
		{"and_op short-circuits past a non-boolean", flowCall("and_op", "false", readsInput("input.payload:s")), false},
		{"or_op short-circuits past a non-boolean", flowCall("or_op", "true", readsInput("input.payload:s")), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mustCompilePredicate(t, tc.filter).Match(NewContext(payload, "", nil))
			if err != nil || got != tc.want {
				t.Errorf("Match = %v, %v; want %v, nil", got, err, tc.want)
			}
		})
	}
}

func TestPredicate_NumbersCompareWithNumbers(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		literal string
		equal   bool
	}{
		{"int payload, int literal, equal", 500, "500", true},
		{"int payload, int literal, unequal", 404, "500", false},
		{"int64 payload, int literal, equal", int64(500), "500", true},
		{"float payload, int literal, equal", 500.0, "500", true},
		{"float payload, int literal, unequal", 404.0, "500", false},
		{"int payload, float literal, equal", 500, "500.0", true},
		{"int payload, float literal, unequal", 404, "500.0", false},
		{"float payload, float literal, equal", 1.5, "1.5", true},
		{"float payload, float literal, unequal", 2.5, "1.5", false},
	}
	for _, tc := range cases {
		for function, want := range map[string]bool{"equal": tc.equal, "not_equal": !tc.equal} {
			t.Run(function+"/"+tc.name, func(t *testing.T) {
				p := mustCompilePredicate(t, flowCall(function, readsInput("input.payload:code"), tc.literal))
				got, err := p.Match(NewContext(map[string]any{"code": tc.payload}, "", nil))
				if err != nil || got != want {
					t.Errorf("Match = %v, %v; want %v, nil", got, err, want)
				}
			})
		}
	}
}

func TestPredicate_ListsCompareItemByItemWithNumbers(t *testing.T) {
	cases := []struct {
		name    string
		payload []any
		literal string
		equal   bool
	}{
		{"JSON numbers, int literal, equal", []any{1.0, 2.0}, "[1, 2]", true},
		{"JSON numbers, int literal, one differs", []any{1.0, 3.0}, "[1, 2]", false},
		{"JSON numbers, int literal, shorter", []any{1.0}, "[1, 2]", false},
		{"JSON numbers, int literal, longer", []any{1.0, 2.0, 3.0}, "[1, 2]", false},
		{"JSON numbers, float literal, equal", []any{1.0, 2.0}, "[1.0, 2.0]", true},
		{"JSON texts, text literal, equal", []any{"a", "b"}, `["a", "b"]`, true},
		{"JSON texts, text literal, one differs", []any{"a", "c"}, `["a", "b"]`, false},
		{"JSON booleans, boolean literal, equal", []any{true}, "[true]", true},
		{"empty list, empty literal", []any{}, "[]", true},
	}
	for _, tc := range cases {
		for function, want := range map[string]bool{"equal": tc.equal, "not_equal": !tc.equal} {
			t.Run(function+"/"+tc.name, func(t *testing.T) {
				p := mustCompilePredicate(t, flowCall(function, readsInput("input.payload:l"), tc.literal))
				got, err := p.Match(NewContext(map[string]any{"l": tc.payload}, "", nil))
				if err != nil || got != want {
					t.Errorf("Match = %v, %v; want %v, nil", got, err, want)
				}
			})
		}
	}
}

func TestPredicate_ListsWithItemsOfAnotherKindAreUndecidable(t *testing.T) {
	cases := []struct {
		name    string
		payload []any
		literal string
		wantErr string
	}{
		{"texts against numbers", []any{"1", "2"}, "[1, 2]", "cannot compare a text item with a number item"},
		{"number against text in the second item", []any{1.0, 2.0}, `[1, "2"]`, "cannot compare a number item with a text item"},
		{"null item against a number", []any{nil, 2.0}, "[1, 2]", "cannot compare a null item with a number item"},
		{"nested list against a number", []any{[]any{1.0}}, "[1]", "cannot compare a list item with a number item"},
	}
	for _, tc := range cases {
		for _, function := range []string{"equal", "not_equal"} {
			t.Run(function+"/"+tc.name, func(t *testing.T) {
				p := mustCompilePredicate(t, flowCall(function, readsInput("input.payload:l"), tc.literal))
				got, err := p.Match(NewContext(map[string]any{"l": tc.payload}, "", nil))
				if err == nil {
					t.Fatalf("Match = %v with no error; want an evaluation error containing %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q does not contain %q", err, tc.wantErr)
				}
			})
		}
	}
}

func TestPredicate_InOpOverTheWholePayloadReadsThePayloadAsItIs(t *testing.T) {
	presence := mustCompilePredicate(t, flowCall("in_op", `"k"`, readsInput("input.payload:")))
	absence := mustCompilePredicate(t, flowCall("not_op", nested(flowCall("in_op", `"k"`, readsInput("input.payload:")))))
	cases := []struct {
		name     string
		payload  any
		present  bool
		wantsErr bool
	}{
		{"JSON object with the key", map[string]any{"k": 1.0}, true, false},
		{"JSON object without the key", map[string]any{"other": 1.0}, false, false},
		{"text that contains the key as a substring", "kafka text", true, false},
		{"text without the key", "hello", false, false},
		{"JSON list holding the key", []any{"k"}, true, false},
		{"JSON list without the key", []any{"other"}, false, false},
		{"empty payload", nil, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := presence.Match(NewContext(tc.payload, "", nil))
			if tc.wantsErr {
				if err == nil {
					t.Fatalf("in_op Match = %v with no error; want an evaluation error", got)
				}
				if _, err = absence.Match(NewContext(tc.payload, "", nil)); err == nil {
					t.Fatal("not_op of in_op Match gave no error; want an evaluation error")
				}
				return
			}
			if err != nil || got != tc.present {
				t.Errorf("in_op Match = %v, %v; want %v, nil", got, err, tc.present)
			}
			gotAbsent, err := absence.Match(NewContext(tc.payload, "", nil))
			if err != nil || gotAbsent == tc.present {
				t.Errorf("not_op of in_op Match = %v, %v; want %v, nil", gotAbsent, err, !tc.present)
			}
		})
	}
}

func TestPredicate_EqualOnBooleansDecides(t *testing.T) {
	p := mustCompilePredicate(t, flowCall("equal", readsInput("input.payload:active"), "true"))
	for active, want := range map[bool]bool{true: true, false: false} {
		got, err := p.Match(NewContext(map[string]any{"active": active}, "", nil))
		if err != nil || got != want {
			t.Errorf("active %v: Match = %v, %v; want %v, nil", active, got, err, want)
		}
	}
}

func TestPredicate_ComparisonLimitErrorNamesTheFunctionOnce(t *testing.T) {
	selfObject := map[string]any{}
	selfObject["k"] = selfObject
	payload := map[string]any{"a": selfObject, "b": selfObject}
	for _, function := range []string{"equal", "not_equal"} {
		t.Run(function, func(t *testing.T) {
			p := mustCompilePredicate(t, flowCall(function, readsInput("input.payload:a"), readsInput("input.payload:b")))
			got, err := p.Match(NewContext(payload, "", nil))
			if err == nil {
				t.Fatalf("Match = %v with no error; want an evaluation error", got)
			}
			if want := function + ": values too large to compare"; !strings.HasPrefix(err.Error(), want) {
				t.Errorf("error %q does not start with %q", err, want)
			}
		})
	}
}

func TestPredicate_KindErrorsCarryNoMessageValues(t *testing.T) {
	const secret = "CANARY-value-in-payload"
	payload := map[string]any{"s": secret, "o": map[string]any{secret: 1.0}, "l": []any{secret}}
	filters := map[string]string{
		"equal":           flowCall("equal", readsInput("input.payload:s"), "5"),
		"in_op list":      flowCall("in_op", `5`, readsInput("input.payload:l")),
		"in_op into text": flowCall("in_op", `5`, readsInput("input.payload:s")),
		"in_op object":    flowCall("in_op", `5`, readsInput("input.payload:o")),
		"and_op":          flowCall("and_op", readsInput("input.payload:s"), "true"),
		"not_op":          flowCall("not_op", readsInput("input.payload:s")),
		"root":            flowCall("if_else", "true", readsInput("input.payload:s"), "false"),
	}
	for name, src := range filters {
		t.Run(name, func(t *testing.T) {
			_, err := mustCompilePredicate(t, src).Match(NewContext(payload, "", nil))
			if err == nil {
				t.Fatal("the kind mismatch must be an evaluation error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q carries the message value", err)
			}
		})
	}
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n") + "\n"
}
