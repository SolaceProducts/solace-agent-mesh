// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

func TestDecideDespiteStale_FailsClosed(t *testing.T) {
	const ref = "{{n.output.missing}}"

	cases := map[string]struct {
		resolved string
		stale    []string
		wantWhy  string
	}{
		"no stale references given": {
			`'a' == 'a'`, nil, "no unresolved references",
		},
		"unparseable expression": {
			ref + ` > 5`, []string{ref}, "does not parse",
		},
		"stale text appears nowhere in the expression": {
			`'a' == 'a'`, []string{ref}, "does not sit anywhere",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, ok, why := decideDespiteStale(tc.resolved, tc.stale)
			if ok {
				t.Fatalf("ok = true for %q; every path that cannot decide must fail closed", tc.resolved)
			}
			if why == nil || !strings.Contains(why.Error(), tc.wantWhy) {
				t.Errorf("why = %v, want it to mention %q", why, tc.wantWhy)
			}
		})
	}
}

func TestDecideDespiteStale_EvaluationFailuresFailClosed(t *testing.T) {
	const ref = "{{n.output.missing}}"
	for name, cond := range map[string]string{
		"vm.Run failure":     `'` + ref + `' == 'x' and null`,
		"non-boolean result": `'` + ref + `' == 'x' and 'str'`,
	} {
		t.Run(name, func(t *testing.T) {
			_, ok, why := decideDespiteStale(cond, []string{ref})
			if ok {
				t.Fatalf("ok = true for %q; an expression that cannot be evaluated must fail closed", cond)
			}
			if why == nil || !strings.Contains(why.Error(), "does not evaluate") &&
				!strings.Contains(why.Error(), "not a boolean") {
				t.Errorf("why = %v, want an evaluation cause", why)
			}
		})
	}
}

func TestDecideDespiteStale_RefusesPastTheAtomCap(t *testing.T) {
	agreeingArms := func(n int) (string, []string) {
		var arms, stale []string
		for i := range n {
			ref := fmt.Sprintf("{{n.output.f%d}}", i)
			arms = append(arms, "('"+ref+"' == 'x' or true)")
			stale = append(stale, ref)
		}
		return strings.Join(arms, " and "), stale
	}

	over, overStale := agreeingArms(maxStaleAtoms + 1)
	_, ok, why := decideDespiteStale(over, overStale)
	if ok {
		t.Errorf("ok = true with %d agreeing atoms; the cap must refuse before enumerating "+
			"2^%d, which a loop would then pay per iteration", len(overStale), len(overStale))
	}
	if why == nil || !strings.Contains(why.Error(), "exceeds") {
		t.Errorf("why = %v, want it to name the cap; its message is the only signal of "+
			"the behavioural cliff at %d atoms", why, maxStaleAtoms)
	}

	at, atStale := agreeingArms(maxStaleAtoms)
	matched, ok, _ := decideDespiteStale(at, atStale)
	if !ok || !matched {
		t.Errorf("matched, ok = %v, %v at exactly the cap; agreeing atoms must answer", matched, ok)
	}
}

func TestDecideDespiteStale_AnswersOnlyWhenTheStaleAtomCannotMatter(t *testing.T) {
	const ref = "{{n.output.missing}}"

	matched, ok, _ := decideDespiteStale(`'`+ref+`' == 'x' or 'live' == 'live'`, []string{ref})
	if !ok || !matched {
		t.Errorf("matched, ok = %v, %v; the live arm settles this either way", matched, ok)
	}

	_, ok, why := decideDespiteStale(`'`+ref+`' != 'x' and 'live' == 'live'`, []string{ref})
	if ok {
		t.Error("ok = true; the stale atom decides this one")
	}
	if why == nil || !strings.Contains(why.Error(), "decides the answer") {
		t.Errorf("why = %v, want the disagreement cause — it must stay distinguishable "+
			"from a parse or evaluation failure", why)
	}
}

func threeValued(t *testing.T, cond string) (matched, ok bool) {
	t.Helper()
	tree, err := parser.Parse(cond)
	if err != nil {
		t.Fatalf("parse %q: %v", cond, err)
	}
	var atoms []*ast.Node
	collectStaleAtoms(&tree.Node, []string{"{{s}}"}, &atoms)
	if len(atoms) == 0 {
		t.Fatalf("no atoms in %q", cond)
	}
	m, definite, err := decideThreeValued(&tree.Node, atoms, []string{"{{s}}"})
	if err != nil {
		t.Fatalf("%q: operand failed: %v", cond, err)
	}
	return m, definite
}

func TestDecideThreeValued_Connectives(t *testing.T) {
	const u = `'{{s}}' == 'x'`
	cases := []struct {
		cond     string
		definite bool
		want     bool
	}{
		{u, false, false},
		{"not (" + u + ")", false, false},
		{"false and " + u, true, false},
		{u + " and false", true, false},
		{"true and " + u, false, false},
		{"true or " + u, true, true},
		{u + " || true", true, true},
		{"false or " + u, false, false},
		{"not (false and " + u + ")", true, true},
		{"not (true or " + u + ")", true, false},
		{"! (" + u + " && false)", true, true},
		{"(" + u + " or true) and (" + u + " or false)", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.cond, func(t *testing.T) {
			matched, ok := threeValued(t, tc.cond)
			if ok != tc.definite {
				t.Fatalf("definite = %v, want %v", ok, tc.definite)
			}
			if ok && matched != tc.want {
				t.Errorf("matched = %v, want %v", matched, tc.want)
			}
		})
	}
}

func TestDecideThreeValued_NeverContradictsTheTruthTable(t *testing.T) {
	const u = `'{{s}}' == 'x'`
	const v = `'{{s}}' != 'y'`
	conds := []string{
		"true or " + u,
		"false and " + u,
		"not (" + u + " and false)",
		"(" + u + " or 1 > 0) and not (" + v + " and 2 < 1)",
		"(" + u + " and true) or (" + v + " and false)",
		"not (not (" + u + " or true))",
		"(" + u + " or " + v + ") and false",
		u + " or not (" + u + ")",
		"(" + u + " and " + v + ") or (" + u + " and 'a' == 'a')",
	}
	for _, cond := range conds {
		t.Run(cond, func(t *testing.T) {
			three, definite := threeValued(t, cond)
			table, answered, _ := decideDespiteStale(cond, []string{"{{s}}"})
			if definite && (!answered || three != table) {
				t.Errorf("three-valued = %v (definite), truth table = %v (answered %v)", three, table, answered)
			}
		})
	}
}

func genCondition(r *rand.Rand, depth int, atoms *int) string {
	if depth == 0 || r.Intn(3) == 0 {
		leaves := []string{"true", "false", "1 > 0", "2 < 1", `'{{s}}' == 'x'`, `'{{s}}' != 'y'`}
		leaf := leaves[r.Intn(len(leaves))]
		if strings.Contains(leaf, "{{s}}") {
			if *atoms >= maxStaleAtoms {
				return "true"
			}
			*atoms++
		}
		return leaf
	}
	switch r.Intn(3) {
	case 0:
		return "not (" + genCondition(r, depth-1, atoms) + ")"
	case 1:
		return "(" + genCondition(r, depth-1, atoms) + " and " + genCondition(r, depth-1, atoms) + ")"
	default:
		return "(" + genCondition(r, depth-1, atoms) + " or " + genCondition(r, depth-1, atoms) + ")"
	}
}

func TestDecideThreeValued_GeneratedNeverContradictsTheTruthTable(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	definite := 0
	for range 5000 {
		atoms := 0
		cond := genCondition(r, 5, &atoms)
		if atoms == 0 {
			continue
		}
		three, ok := threeValued(t, cond)
		if !ok {
			continue
		}
		definite++
		table, answered, _ := decideDespiteStale(cond, []string{"{{s}}"})
		if !answered || three != table {
			t.Fatalf("%s: three-valued = %v (definite), truth table = %v (answered %v)", cond, three, table, answered)
		}
	}
	if definite == 0 {
		t.Fatal("no definite answers generated; the check proved nothing")
	}
}
