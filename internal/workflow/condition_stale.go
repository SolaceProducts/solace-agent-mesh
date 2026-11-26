// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"errors"
	"fmt"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/compiler"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
)

const maxStaleAtoms = 6

const _ = uint(62 - maxStaleAtoms)

var errDecidedByUnresolved = errors.New("the unresolved reference decides the answer")

type invalidExpressionError struct{ err error }

func (e *invalidExpressionError) Error() string { return e.err.Error() }
func (e *invalidExpressionError) Unwrap() error { return e.err }

func invalidExpression(format string, args ...any) error {
	return &invalidExpressionError{err: fmt.Errorf(format, args...)}
}

func decideDespiteStale(resolved string, staleText []string) (matched bool, ok bool, why error) {
	return decideUnresolved(resolved, staleText, false)
}

func decideDespiteMissingInputs(resolved string, missing []string) (matched bool, ok bool, why error) {
	return decideUnresolved(resolved, missing, true)
}

func decideUnresolved(resolved string, staleText []string, threeValuedPastCap bool) (matched bool, ok bool, why error) {
	if len(staleText) == 0 {
		return false, false, errors.New("no unresolved references to weigh")
	}
	tree, err := parser.Parse(resolved)
	if err != nil {
		return false, false, invalidExpression("the resolved expression does not parse: %w", err)
	}

	var atoms []*ast.Node
	collectStaleAtoms(&tree.Node, staleText, &atoms)
	for _, text := range staleText {
		if strings.ContainsRune(text, '\\') {
			return false, false, fmt.Errorf("the unresolved reference %s contains an escape, so the text expr reads may differ from what was written", text)
		}
		if !holdsStaleText(tree.Node, []string{text}) {
			return false, false, fmt.Errorf("the unresolved reference %s does not sit anywhere the truth table can substitute it", text)
		}
	}
	if len(atoms) == 0 {
		return false, false, errors.New("no unresolved reference sits in a comparison the truth table can substitute")
	}
	original := make([]ast.Node, len(atoms))
	for i, a := range atoms {
		original[i] = *a
	}
	restore := func() {
		for i, a := range atoms {
			*a = original[i]
		}
	}
	defer restore()

	if len(atoms) > maxStaleAtoms {
		if threeValuedPastCap {
			matched, ok, operandErr := decideThreeValued(&tree.Node, atoms, staleText)
			if ok {
				return matched, true, nil
			}
			if operandErr != nil {
				_, errAllFalse := evaluateRow(tree, atoms, func(int) bool { return false })
				_, errAllTrue := evaluateRow(tree, atoms, func(int) bool { return true })
				if errAllFalse != nil && errAllTrue != nil {
					return false, false, &invalidExpressionError{err: errAllFalse}
				}
				return false, false, fmt.Errorf("%w for some values of the unresolved reference", operandErr)
			}
		}
		return false, false, fmt.Errorf("%d unresolved comparisons exceeds the %d the truth table will enumerate", len(atoms), maxStaleAtoms)
	}

	rows := 1 << len(atoms)
	var first, disagree bool
	answered, failed := 0, 0
	var rowErr error
	for mask := range rows {
		b, err := evaluateRow(tree, atoms, func(i int) bool { return mask&(1<<i) != 0 })
		var invalid *invalidExpressionError
		switch {
		case errors.As(err, &invalid):
			return false, false, err
		case err != nil:
			failed++
			if rowErr == nil {
				rowErr = err
			}
			continue
		}
		if answered == 0 {
			first = b
		} else if b != first {
			disagree = true
		}
		answered++
	}
	switch {
	case failed == rows:
		return false, false, &invalidExpressionError{err: rowErr}
	case failed > 0:
		return false, false, fmt.Errorf("%w for some values of the unresolved reference", rowErr)
	case disagree:
		return false, false, errDecidedByUnresolved
	}
	return first, true, nil
}

func evaluateRow(tree *parser.Tree, atoms []*ast.Node, value func(i int) bool) (bool, error) {
	for i, a := range atoms {
		*a = &ast.BoolNode{Value: value(i)}
	}
	program, err := compiler.Compile(tree, nil)
	if err != nil {
		return false, invalidExpression("the resolved expression does not compile: %w", err)
	}
	out, err := vm.Run(program, nil)
	if err != nil {
		return false, fmt.Errorf("the resolved expression does not evaluate: %w", err)
	}
	b, isBool := out.(bool)
	if !isBool {
		return false, fmt.Errorf("the resolved expression is not a boolean (got %T)", out)
	}
	return b, nil
}

func decideThreeValued(root *ast.Node, atoms []*ast.Node, staleText []string) (matched, ok bool, operandErr error) {
	unknown := make(map[*ast.Node]bool, len(atoms))
	for _, a := range atoms {
		unknown[a] = true
	}
	const (
		triFalse = iota
		triTrue
		triUnknown
	)
	evalResolved := func(n *ast.Node) (int, error) {
		program, err := compiler.Compile(&parser.Tree{Node: *n}, nil)
		if err != nil {
			return 0, fmt.Errorf("an operand does not compile: %w", err)
		}
		out, err := vm.Run(program, nil)
		if err != nil {
			return 0, fmt.Errorf("an operand does not evaluate: %w", err)
		}
		b, isBool := out.(bool)
		if !isBool {
			return 0, fmt.Errorf("an operand is not a boolean (got %T)", out)
		}
		if b {
			return triTrue, nil
		}
		return triFalse, nil
	}
	var eval func(n *ast.Node) (int, error)
	eval = func(n *ast.Node) (int, error) {
		if unknown[n] {
			return triUnknown, nil
		}
		if !holdsStaleText(*n, staleText) {
			return evalResolved(n)
		}
		switch node := (*n).(type) {
		case *ast.UnaryNode:
			if !isBooleanConnective(node) {
				break
			}
			v, err := eval(&node.Node)
			if err != nil || v == triUnknown {
				return v, err
			}
			return 1 - v, nil
		case *ast.BinaryNode:
			if !isBooleanConnective(node) {
				break
			}
			short, other := triFalse, triTrue
			if node.Operator == "or" || node.Operator == "||" {
				short, other = triTrue, triFalse
			}
			l, err := eval(&node.Left)
			if err != nil {
				return 0, err
			}
			if l == short {
				return short, nil
			}
			r, err := eval(&node.Right)
			if err != nil {
				return 0, err
			}
			switch {
			case r == short:
				return short, nil
			case l == other && r == other:
				return other, nil
			}
			return triUnknown, nil
		}
		return evalResolved(n)
	}
	v, err := eval(root)
	if err != nil {
		return false, false, err
	}
	if v == triUnknown {
		return false, false, nil
	}
	return v == triTrue, true, nil
}

func collectStaleAtoms(node *ast.Node, staleText []string, out *[]*ast.Node) {
	if !holdsStaleText(*node, staleText) {
		return
	}
	if !isBooleanConnective(*node) {
		*out = append(*out, node)
		return
	}
	switch n := (*node).(type) {
	case *ast.BinaryNode:
		collectStaleAtoms(&n.Left, staleText, out)
		collectStaleAtoms(&n.Right, staleText, out)
	case *ast.UnaryNode:
		collectStaleAtoms(&n.Node, staleText, out)
	}
}

func isBooleanConnective(node ast.Node) bool {
	switch n := node.(type) {
	case *ast.BinaryNode:
		return n.Operator == "and" || n.Operator == "&&" || n.Operator == "or" || n.Operator == "||"
	case *ast.UnaryNode:
		return n.Operator == "not" || n.Operator == "!"
	}
	return false
}

func holdsStaleText(node ast.Node, staleText []string) bool {
	found := false
	ast.Walk(&node, staleLiteralVisitor{staleText: staleText, found: &found})
	return found
}

type staleLiteralVisitor struct {
	staleText []string
	found     *bool
}

func (v staleLiteralVisitor) Visit(node *ast.Node) {
	var value string
	switch n := (*node).(type) {
	case *ast.StringNode:
		value = n.Value
	case *ast.BytesNode:
		value = string(n.Value)
	default:
		return
	}
	for _, stale := range v.staleText {
		if strings.Contains(value, stale) {
			*v.found = true
			return
		}
	}
}
