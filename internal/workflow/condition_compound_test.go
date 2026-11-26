// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"errors"
	"strings"
	"testing"
)

func TestEvaluateCondition_CompoundAnswersWhenResolvedArmsSettleIt(t *testing.T) {
	cases := map[string]struct {
		cond string
		want bool
	}{
		"stale arm first, live arm true": {
			`'{{find_user.output.hits}}' == '' or '{{find_user.output.status}}' == 'success'`, true,
		},
		"live arm first, stale arm second": {
			`'{{find_user.output.status}}' == 'success' or '{{find_user.output.hits}}' == ''`, true,
		},
		"live arm decides false": {
			`'{{find_user.output.hits}}' == '' and '{{find_user.output.status}}' == 'nope'`, false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			matched, stale, err := evaluateCondition(tc.cond, presentNodeOutputs(), nil)
			if err != nil {
				t.Fatalf("err = %v, want nil; a partially-resolved predicate must still answer", err)
			}
			if matched != tc.want {
				t.Errorf("matched = %v, want %v", matched, tc.want)
			}
			if !errors.Is(stale, ErrUnresolvedReference) {
				t.Fatalf("stale = %v, want ErrUnresolvedReference", stale)
			}
			if !strings.Contains(stale.Error(), "hits") {
				t.Errorf("stale %q does not name the reference that failed", stale)
			}
		})
	}
}

func TestEvaluateCondition_StaleArmThatWouldDecideAbstains(t *testing.T) {
	cases := map[string]string{
		"and with negated stale arm":      `'{{find_user.output.status}}' == 'success' and '{{find_user.output.hits}}' != '0'`,
		"or with negated stale arm":       `'{{find_user.output.status}}' == 'nope' or '{{find_user.output.hits}}' != '0'`,
		"and where the stale arm decides": `'{{find_user.output.status}}' == 'success' and '{{find_user.output.hits}}' == '0'`,
		"negation wrapping a stale arm":   `not ('{{find_user.output.hits}}' == '0') and '{{find_user.output.status}}' == 'success'`,
	}
	for name, cond := range cases {
		t.Run(name, func(t *testing.T) {
			matched, stale, err := evaluateCondition(cond, presentNodeOutputs(), nil)
			if !errors.Is(err, ErrUnresolvedReference) {
				t.Fatalf("err = %v, want ErrUnresolvedReference; the missing field decides this predicate", err)
			}
			if matched {
				t.Error("matched = true; a branch would dispatch on data nobody produced")
			}
			if stale != nil {
				t.Errorf("stale = %v, want nil; this is the fail-closed path", stale)
			}
		})
	}
}

func TestEvaluateCondition_EveryReferenceStaleStillFailsClosed(t *testing.T) {
	matched, stale, err := evaluateCondition(
		`'{{find_user.output.a}}' == '' or '{{find_user.output.b}}' == ''`, presentNodeOutputs(), nil)
	if !errors.Is(err, ErrUnresolvedReference) {
		t.Fatalf("err = %v, want ErrUnresolvedReference", err)
	}
	if matched {
		t.Error("matched = true; a wholly literal predicate must not select anything")
	}
	if stale != nil {
		t.Errorf("stale = %v, want nil; the failure is reported as err, not as stale", stale)
	}
}

func TestEvaluateCondition_MissingNodeCannotRescueAStaleField(t *testing.T) {
	matched, _, err := evaluateCondition(
		`'{{find_user.output.hits}}' != '{{nosuchnode.output.x}}'`, presentNodeOutputs(), nil)
	if !errors.Is(err, ErrUnresolvedReference) {
		t.Fatalf("err = %v, want ErrUnresolvedReference", err)
	}
	if matched {
		t.Error("matched = true; comparing two literals is the hazard this guard exists for")
	}
}

func TestEvaluateCondition_FullyResolvedCompoundReportsNoStale(t *testing.T) {
	matched, stale, err := evaluateCondition(
		`'{{find_user.output.status}}' == 'success' and '{{find_user.output.message}}' != ''`,
		presentNodeOutputs(), nil)
	if err != nil || stale != nil {
		t.Fatalf("err, stale = %v, %v; want nil, nil", err, stale)
	}
	if !matched {
		t.Error("matched = false, want true")
	}
}
