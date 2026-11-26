// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestEvaluateCondition_NothingHereCannotCertifyAStaleField(t *testing.T) {
	staleFieldOutputs := func() map[string]map[string]any {
		return map[string]map[string]any{
			"find_user":      {"output": map[string]any{"status": "success"}},
			"workflow_input": {"output": map[string]any{}},
		}
	}

	cases := map[string]string{
		"unsupplied workflow input": `'{{workflow.input.dry_run}}' != 'true' or '{{find_user.output.hits}}' == '0'`,
		"unknown node":              `'{{nosuchnode.output.flag}}' != 'true' or '{{find_user.output.hits}}' == '0'`,
		"two nothing-here arms":     `'{{nosuchnode.output.a}}' != 'x' and '{{find_user.output.hits}}' == '0'`,
	}
	for name, cond := range cases {
		t.Run(name, func(t *testing.T) {
			matched, _, err := evaluateCondition(cond, staleFieldOutputs(), nil)
			if !errors.Is(err, ErrUnresolvedReference) {
				t.Fatalf("err = %v, want ErrUnresolvedReference; a reference that resolved "+
					"to nothing carries no data and must not settle the answer", err)
			}
			if matched {
				t.Error("matched = true; the branch would dispatch on a field nobody produced")
			}
		})
	}
}

func TestEvaluateCondition_RealDataStillSettlesTheAnswer(t *testing.T) {
	outputs := map[string]map[string]any{
		"find_user":      {"output": map[string]any{"status": "success"}},
		"workflow_input": {"output": map[string]any{"dry_run": "true"}},
	}

	matched, stale, err := evaluateCondition(
		`'{{workflow.input.dry_run}}' == 'true' or '{{find_user.output.hits}}' == '0'`, outputs, nil)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !matched {
		t.Error("matched = false; the supplied input settles this either way")
	}
	if !errors.Is(stale, ErrUnresolvedReference) {
		t.Errorf("stale = %v, want ErrUnresolvedReference: the dead arm still deserves a WARN", stale)
	}
}

func TestEvaluateCondition_UnsuppliedInputAloneAbstains(t *testing.T) {
	outputs := map[string]map[string]any{
		"workflow_input": {"output": map[string]any{"segments": []any{"s1"}}},
	}

	cases := map[string]string{
		"issue 4403 either-input gate":        `'{{workflow.input.call_id}}' != '' or '{{workflow.input.text}}' != ''`,
		"single negated comparison":           `'{{workflow.input.call_id}}' != 'x'`,
		"single equality":                     `'{{workflow.input.call_id}}' == 'x'`,
		"unquoted numeric":                    `{{workflow.input.n}} > 5`,
		"unquoted boolean":                    `{{workflow.input.enabled}} == true`,
		"embedded in a double-quoted literal": `"id-{{workflow.input.call_id}}" == "id-x"`,
	}
	for name, cond := range cases {
		t.Run(name, func(t *testing.T) {
			matched, stale, err := evaluateCondition(cond, outputs, nil)
			if !errors.Is(err, ErrUnresolvedReference) {
				t.Fatalf("err = %v, want ErrUnresolvedReference; an unsupplied input "+
					"must not be compared as its literal template text", err)
			}
			if !errors.Is(err, errInputNotSupplied) {
				t.Errorf("err = %v, want errInputNotSupplied: only an input is missing, "+
					"so this is the caller's choice, not a stale reference", err)
			}
			if matched {
				t.Error("matched = true; the node would run on an input nobody supplied")
			}
			if stale != nil {
				t.Errorf("stale = %v, want nil", stale)
			}
		})
	}
}

func TestEvaluateCondition_InputSuppliedAsNullAbstains(t *testing.T) {
	outputs := map[string]map[string]any{
		"workflow_input": {"output": map[string]any{"call_id": nil}},
	}
	matched, _, err := evaluateCondition(`'{{workflow.input.call_id}}' != ''`, outputs, nil)
	if !errors.Is(err, errInputNotSupplied) {
		t.Fatalf("err = %v, want errInputNotSupplied", err)
	}
	if matched {
		t.Error("matched = true; null was compared as the literal template text")
	}
}

func TestEvaluateCondition_ManyOptionalInputsPastTheAtomCap(t *testing.T) {
	var arms []string
	for i := range maxStaleAtoms + 2 {
		arms = append(arms, fmt.Sprintf("'{{workflow.input.f%d}}' != ''", i))
	}
	gate := strings.Join(arms, " or ")

	t.Run("one supplied settles it", func(t *testing.T) {
		outputs := map[string]map[string]any{"workflow_input": {"output": map[string]any{"f3": "x"}}}
		matched, stale, err := evaluateCondition(gate, outputs, nil)
		if err != nil || stale != nil || !matched {
			t.Errorf("matched, stale, err = %v, %v, %v; want true, nil, nil", matched, stale, err)
		}
	})
	t.Run("a resolved arm settles it even beside a failing operand", func(t *testing.T) {
		outputs := map[string]map[string]any{
			"a":              {"output": map[string]any{"ok": "yes", "v": "abc"}},
			"workflow_input": {"output": map[string]any{}},
		}
		cond := `('{{a.output.ok}}' == 'yes' or int('{{a.output.v}}') > 0) or (` + gate + `)`
		matched, stale, err := evaluateCondition(cond, outputs, nil)
		if err != nil || stale != nil || !matched {
			t.Errorf("matched, stale, err = %v, %v, %v; want true, nil, nil", matched, stale, err)
		}
	})
	t.Run("a settled left operand keeps a failing right one from running", func(t *testing.T) {
		outputs := map[string]map[string]any{
			"a":              {"output": map[string]any{"ok": "yes", "v": "abc"}},
			"workflow_input": {"output": map[string]any{}},
		}
		cond := `'{{a.output.ok}}' == 'yes' or (int('{{a.output.v}}') > 0 and (` + gate + `))`
		matched, stale, err := evaluateCondition(cond, outputs, nil)
		if err != nil || stale != nil || !matched {
			t.Errorf("matched, stale, err = %v, %v, %v; want true, nil, nil", matched, stale, err)
		}
	})
	t.Run("none supplied does not match", func(t *testing.T) {
		outputs := map[string]map[string]any{"workflow_input": {"output": map[string]any{}}}
		matched, _, err := evaluateCondition(gate, outputs, nil)
		if !errors.Is(err, errInputNotSupplied) || matched {
			t.Errorf("matched, err = %v, %v; want false, errInputNotSupplied", matched, err)
		}
	})
}

func TestEvaluateCondition_BrokenPredicateOverUnsuppliedInputStillFails(t *testing.T) {
	outputs := map[string]map[string]any{"workflow_input": {"output": map[string]any{}}}

	_, _, err := evaluateCondition(`'{{workflow.input.call_id}}' != '' and (`, outputs, nil)
	if err == nil {
		t.Fatal("err = nil; a predicate that does not parse must fail")
	}
	if errors.Is(err, ErrUnresolvedReference) {
		t.Errorf("err = %v; a malformed predicate was downgraded to an abstention", err)
	}
}

func causeOf(err error, cond string) string {
	_, after, _ := strings.Cut(err.Error(), fmt.Sprintf("%q", cond))
	return after
}

func TestEvaluateCondition_MissingInputBesideAStaleField(t *testing.T) {
	const cond = `'{{find_user.output.hits}}' == '0' or '{{workflow.input.call_id}}' != ''`
	outputs := map[string]map[string]any{
		"find_user":      {"output": map[string]any{"status": "success"}},
		"workflow_input": {"output": map[string]any{}},
	}
	_, _, err := evaluateCondition(cond, outputs, nil)
	if !errors.Is(err, ErrUnresolvedReference) {
		t.Fatalf("err = %v, want ErrUnresolvedReference", err)
	}
	if isOrdinaryNotMatched(err) {
		t.Errorf("err = %v; reported as an ordinary absence, hiding the field the author got wrong", err)
	}
	if cause := causeOf(err, cond); !strings.HasPrefix(cause, ": {{find_user.output.hits}}: ") {
		t.Errorf("cause = %q, want it to name the stale field", cause)
	}
}

func TestEvaluateCondition_MissingInputBesideANodeThatDidNotRun(t *testing.T) {
	outputs := map[string]map[string]any{
		"gate":           {"output": map[string]any{"mode": "b"}},
		"workflow_input": {"output": map[string]any{}},
	}

	t.Run("they decide it", func(t *testing.T) {
		const cond = `'{{branch_a.output.x}}' != '' or '{{workflow.input.y}}' != ''`
		matched, stale, err := evaluateCondition(cond, outputs, nil)
		if !errors.Is(err, errNotProduced) {
			t.Fatalf("err = %v, want errNotProduced", err)
		}
		if errors.Is(err, errInputNotSupplied) {
			t.Errorf("err = %v; the node is the one to name", err)
		}
		if cause := causeOf(err, cond); !strings.HasPrefix(cause, ": {{branch_a.output.x}}: ") {
			t.Errorf("cause = %q, want it to name the node", cause)
		}
		if matched || stale != nil {
			t.Errorf("matched, stale = %v, %v; want false, nil", matched, stale)
		}
	})
	t.Run("the rest settles it", func(t *testing.T) {
		const cond = `'{{gate.output.mode}}' == 'a' and ('{{branch_a.output.ok}}' == 'true' or '{{workflow.input.force}}' == 'true')`
		matched, stale, err := evaluateCondition(cond, outputs, nil)
		if err != nil || stale != nil || matched {
			t.Errorf("matched, stale, err = %v, %v, %v; want false, nil, nil", matched, stale, err)
		}
	})
}

func TestEvaluateCondition_ArmFailingOnlyPastAMissingInputAbstains(t *testing.T) {
	outputs := map[string]map[string]any{
		"fetch":          {"output": map[string]any{"v": "abc"}},
		"workflow_input": {"output": map[string]any{}},
	}
	for name, cond := range map[string]string{
		"and form":                     `'{{workflow.input.check}}' == 'true' and int('{{fetch.output.v}}') > 0`,
		"or form":                      `'{{workflow.input.check}}' == 'true' or int('{{fetch.output.v}}') > 0`,
		"non-boolean arm":              `'{{workflow.input.check}}' == 'true' and '{{fetch.output.v}}'`,
		"failure after a disagreement": `'{{workflow.input.a}}' == 'x' or ('{{workflow.input.b}}' == 'y' and int('{{fetch.output.v}}') > 0)`,
		"past the atom cap":            `(` + manyInputArms(maxStaleAtoms+1) + `) and int('{{fetch.output.v}}') > 0`,
		"past the atom cap, or form":   `(` + manyInputArms(maxStaleAtoms+1) + `) or int('{{fetch.output.v}}') > 0`,
	} {
		t.Run(name, func(t *testing.T) {
			matched, _, err := evaluateCondition(cond, outputs, nil)
			if !errors.Is(err, errInputNotSupplied) {
				t.Fatalf("err = %v, want errInputNotSupplied", err)
			}
			if matched {
				t.Error("matched = true")
			}
		})
	}
}

func TestEvaluateCondition_StaleFieldBesideAFailingArmAbstains(t *testing.T) {
	outputs := map[string]map[string]any{"fetch": {"output": map[string]any{"v": "abc"}}}
	matched, stale, err := evaluateCondition(`'{{fetch.output.nope}}' == 'x' and int('{{fetch.output.v}}') > 0`, outputs, nil)
	if !errors.Is(err, ErrUnresolvedReference) || isOrdinaryNotMatched(err) {
		t.Fatalf("err = %v, want a stale-reference abstention", err)
	}
	if matched || stale != nil {
		t.Errorf("matched, stale = %v, %v; want false, nil", matched, stale)
	}
}

func manyInputArms(n int) string {
	arms := make([]string, n)
	for i := range arms {
		arms[i] = fmt.Sprintf("'{{workflow.input.f%d}}' != ''", i)
	}
	return strings.Join(arms, " or ")
}

func TestEvaluateCondition_ArmFailingInEveryRowStillFails(t *testing.T) {
	for name, tc := range map[string]struct {
		cond  string
		input map[string]any
	}{
		"omitted":                    {`int('{{fetch.output.v}}') > 0 and '{{workflow.input.a}}' == 'x'`, map[string]any{}},
		"supplied":                   {`int('{{fetch.output.v}}') > 0 and '{{workflow.input.a}}' == 'x'`, map[string]any{"a": "x"}},
		"omitted, past the atom cap": {`int('{{fetch.output.v}}') > 0 and (` + manyInputArms(maxStaleAtoms+1) + `)`, map[string]any{}},
		"omitted, more atoms than a 64-bit mask holds": {`int('{{fetch.output.v}}') > 0 and (` + manyInputArms(70) + `)`, map[string]any{}},
	} {
		t.Run(name, func(t *testing.T) {
			cond, input := tc.cond, tc.input
			outputs := map[string]map[string]any{
				"fetch":          {"output": map[string]any{"v": "abc"}},
				"workflow_input": {"output": input},
			}
			_, _, err := evaluateCondition(cond, outputs, nil)
			if err == nil {
				t.Fatal("err = nil; a predicate that cannot be evaluated must fail")
			}
			if errors.Is(err, ErrUnresolvedReference) {
				t.Errorf("err = %v; downgraded to an abstention", err)
			}
		})
	}
}

func TestEvaluateCondition_UnplacedMissingInputFailsClosed(t *testing.T) {
	outputs := map[string]map[string]any{
		"a":              {"output": map[string]any{"b": "x"}},
		"workflow_input": {"output": map[string]any{}},
	}
	for name, cond := range map[string]string{
		"bytes literal":                       `b'{{workflow.input.x}}' != b''`,
		"bytes literal in a call":             `len(b'{{workflow.input.x}}') > 0`,
		"escaped path":                        `'{{workflow.input.\x78}}' != ''`,
		"only inside a comment":               "'{{a.output.b}}' == 'x' // was: {{workflow.input.y}}",
		"escaped path beside a placed input":  `'{{workflow.input.y}}' == 'q' or '{{workflow.input.\x78}}' != ''`,
		"escaped path, double-quoted":         `"{{workflow.input.y}}" == "q" or "{{workflow.input.\x78}}" != ""`,
		"escaped path, bare":                  `'{{workflow.input.y}}' == 'q' or {{workflow.input.\x78}} != 5`,
		"escaped path beside a node not run":  `'{{skipped.output.f}}' == 'z' or '{{workflow.input.\x78}}' != ''`,
		"escaped path past the atom cap":      `(` + manyInputArms(maxStaleAtoms+1) + `) or '{{workflow.input.\x78}}' != ''`,
		"bytes literal beside a placed input": `'{{workflow.input.y}}' == 'q' or b'{{workflow.input.x}}' != b''`,
		"bytes literal past the atom cap":     `(` + manyInputArms(maxStaleAtoms+1) + `) or b'{{workflow.input.x}}' != b''`,
	} {
		t.Run(name, func(t *testing.T) {
			matched, _, err := evaluateCondition(cond, outputs, nil)
			if matched {
				t.Errorf("matched = true; err = %v", err)
			}
			if !isOrdinaryNotMatched(err) {
				t.Errorf("err = %v, want an ordinary not-matched", err)
			}
		})
	}
}

func TestEvaluateCondition_MixedRowDecides(t *testing.T) {
	const inputs = `'{{workflow.input.a}}' == 'x' or not ('{{workflow.input.b}}' == 'y')`
	const fields = `'{{fetch.output.a}}' == 'x' or not ('{{fetch.output.b}}' == 'y')`
	outputs := map[string]map[string]any{
		"fetch":          {"output": map[string]any{"v": "1"}},
		"workflow_input": {"output": map[string]any{}},
	}

	t.Run("omitted inputs", func(t *testing.T) {
		matched, _, err := evaluateCondition(inputs, outputs, nil)
		if matched || !errors.Is(err, errInputNotSupplied) {
			t.Errorf("matched, err = %v, %v; want false, errInputNotSupplied", matched, err)
		}
	})
	t.Run("stale fields", func(t *testing.T) {
		matched, _, err := evaluateCondition(fields, outputs, nil)
		if matched || !errors.Is(err, ErrUnresolvedReference) || isOrdinaryNotMatched(err) {
			t.Errorf("matched, err = %v, %v; want false and a stale-reference abstention", matched, err)
		}
	})
}

func TestEvaluateCondition_EscapedReferenceCopiesFailClosed(t *testing.T) {
	outputs := map[string]map[string]any{
		"a":              {"output": map[string]any{"v": `{{workflow.input.\\x78}}`}},
		"workflow_input": {"output": map[string]any{}},
	}
	for name, cond := range map[string]string{
		"backtick copy":       "`{{workflow.input.\\x78}}` == 'q' or '{{workflow.input.\\x78}}' != ''",
		"backtick copy, bare": "`{{workflow.input.\\x78}}` == 'q' or {{workflow.input.\\x78}} != 5",
		"copy echoed by data": `'{{a.output.v}}' == 'q' or '{{workflow.input.\x78}}' != ''`,
	} {
		t.Run(name, func(t *testing.T) {
			matched, _, err := evaluateCondition(cond, outputs, nil)
			if matched {
				t.Errorf("matched = true; err = %v", err)
			}
			if !isOrdinaryNotMatched(err) {
				t.Errorf("err = %v, want an ordinary not-matched", err)
			}
		})
	}
}

func TestEvaluateCondition_StalePathUnplacedReferenceAbstains(t *testing.T) {
	outputs := map[string]map[string]any{
		"fetch":          {"output": map[string]any{"v": "1"}},
		"workflow_input": {"output": map[string]any{}},
	}
	for name, cond := range map[string]string{
		"escaped field":        `'{{fetch.output.nope}}' == 'x' or '{{fetch.output.\x6e}}' != ''`,
		"escaped input beside": `'{{fetch.output.nope}}' == 'x' or '{{workflow.input.\x78}}' != ''`,
	} {
		t.Run(name, func(t *testing.T) {
			matched, stale, err := evaluateCondition(cond, outputs, nil)
			if !errors.Is(err, ErrUnresolvedReference) || isOrdinaryNotMatched(err) {
				t.Fatalf("err = %v, want a stale-reference abstention", err)
			}
			if matched || stale != nil {
				t.Errorf("matched, stale = %v, %v; want false, nil", matched, stale)
			}
		})
	}
}

func TestEvaluateCondition_QuotingSurvivesTrickyLiterals(t *testing.T) {
	for name, tc := range map[string]struct {
		cond  string
		fetch map[string]any
	}{
		"escaped quote before the reference": {`"a\"b{{workflow.input.a}}" == "q"`, nil},
		"raw string with a quote inside":     {"`a'b` == 'x' or {{workflow.input.n}} > 5", nil},
		"escaped backslash ends the literal": {`'a\\' == 'b' or {{workflow.input.n}} > 5`, nil},
		"backslash ends a raw string":        {"`a\\` == 'x' or {{workflow.input.n}} > 5", nil},
		"quote inside an earlier reference":  {"{{fetch.output.o'k}} == 5 and {{workflow.input.n}} > 5", map[string]any{"o'k": 5}},
		"quote inside a line comment":        {"'a' == 'b' // it's\n or {{workflow.input.n}} > 5", nil},
		"quote inside a block comment":       {"'a' == 'b' /* it's */ or {{workflow.input.n}} > 5", nil},
	} {
		t.Run(name, func(t *testing.T) {
			outputs := map[string]map[string]any{"workflow_input": {"output": map[string]any{}}}
			if tc.fetch != nil {
				outputs["fetch"] = map[string]any{"output": tc.fetch}
			}
			_, _, err := evaluateCondition(tc.cond, outputs, nil)
			if !errors.Is(err, errInputNotSupplied) {
				t.Errorf("err = %v, want errInputNotSupplied", err)
			}
		})
	}
}

func TestEvaluateCondition_SuppliedInputStillDecides(t *testing.T) {
	const gate = `'{{workflow.input.call_id}}' != '' or '{{workflow.input.text}}' != ''`

	cases := []struct {
		name  string
		input map[string]any
		want  bool
	}{
		{"one supplied, other unsupplied", map[string]any{"call_id": "abc"}, true},
		{"both supplied", map[string]any{"call_id": "abc", "text": "hi"}, true},
		{"both supplied empty", map[string]any{"call_id": "", "text": ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outputs := map[string]map[string]any{"workflow_input": {"output": tc.input}}
			matched, stale, err := evaluateCondition(gate, outputs, nil)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if matched != tc.want {
				t.Errorf("matched = %v, want %v", matched, tc.want)
			}
			if stale != nil {
				t.Errorf("stale = %v, want nil: an optional input the caller left out is "+
					"not an authoring error and must not warn on every run", stale)
			}
		})
	}
}
