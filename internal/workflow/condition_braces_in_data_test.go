// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"errors"
	"testing"
)

func TestEvaluateCondition_BracesInDataCannotHideAStaleReference(t *testing.T) {
	const cond = `'{{a.output.frag}}' == 'zzz' or '{{a.output.m1}}' != 'x' or '{{a.output.m2}}' != 'y'`

	outputs := func(frag string) map[string]map[string]any {
		return map[string]map[string]any{"a": {"output": map[string]any{"frag": frag}}}
	}

	for name, frag := range map[string]string{
		"value carries an unbalanced brace": "open{{",
		"control: same shape, no braces":    "openXX",
	} {
		t.Run(name, func(t *testing.T) {
			matched, _, err := evaluateCondition(cond, outputs(frag), nil)
			if !errors.Is(err, ErrUnresolvedReference) {
				t.Fatalf("err = %v, want ErrUnresolvedReference; the first arm is false on "+
					"real data, so m1 and m2 decide this and neither was produced", err)
			}
			if matched {
				t.Error("matched = true; a data value must not be able to hide a stale reference")
			}
		})
	}
}

func TestEvaluateCondition_WellFormedTemplateInDataIsNotAReference(t *testing.T) {
	outputs := map[string]map[string]any{
		"a": {"output": map[string]any{
			"tpl":    "Hello {{name}}",
			"status": "ready",
		}},
	}
	matched, stale, err := evaluateCondition(
		`'{{a.output.tpl}}' == 'Hello {{name}}' and '{{a.output.status}}' == 'ready'`, outputs, nil)
	if err != nil {
		t.Fatalf("err = %v, want nil; every reference here resolved", err)
	}
	if stale != nil {
		t.Errorf("stale = %v, want nil; the braces are data, not an unresolved reference", stale)
	}
	if !matched {
		t.Error("matched = false, want true")
	}
}
