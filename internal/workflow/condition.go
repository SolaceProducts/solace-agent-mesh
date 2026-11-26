// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"errors"
	"fmt"

	"github.com/expr-lang/expr"
)

var ErrUnresolvedReference = errors.New("condition references a value that did not resolve")

var errInputNotSupplied = errors.New("workflow input not supplied")

var errNotProduced = errors.New("referenced node produced nothing")

func isOrdinaryNotMatched(err error) bool {
	return errors.Is(err, errInputNotSupplied) || errors.Is(err, errNotProduced)
}

func EvaluateCondition(condExpr string, nodeOutputs map[string]map[string]any) (bool, error) {
	matched, _, err := evaluateCondition(condExpr, nodeOutputs, nil)
	return matched, err
}

func evaluateCondition(condExpr string, nodeOutputs map[string]map[string]any, named schemaDerived) (matched bool, stale error, err error) {
	if condExpr == "" {
		return true, nil, nil
	}

	resolved, res := resolveStringTemplatesReporting(condExpr, nodeOutputs, named)

	if res.failed == 0 && res.inputs > 0 {
		return decideOverMissingInputs(condExpr, res)
	}

	if res.failed > 0 {
		matched, ok, why := decideDespiteStale(resolved, res.unresolvedText)
		if ok {
			return matched, fmt.Errorf("%w: %q: %w", ErrUnresolvedReference, condExpr, res.firstErr), nil
		}
		return false, nil, fmt.Errorf("%w: %q: %w (%v)", ErrUnresolvedReference, condExpr, res.firstErr, why)
	}

	result, err := expr.Eval(resolved, nil)
	if err != nil {
		return false, nil, fmt.Errorf("condition evaluation failed for %q (resolved: %q): %w", condExpr, resolved, err)
	}

	b, ok := result.(bool)
	if !ok {
		return false, nil, fmt.Errorf("condition %q did not evaluate to bool (got %T: %v)", condExpr, result, result)
	}
	return b, nil, nil
}

func decideOverMissingInputs(condExpr string, res templateResolution) (matched bool, stale error, err error) {
	matched, ok, why := decideDespiteMissingInputs(res.decideText, res.unresolvedText)
	if ok {
		return matched, nil, nil
	}
	var invalid *invalidExpressionError
	if errors.As(why, &invalid) {
		return false, nil, fmt.Errorf("condition evaluation failed for %q (resolved: %q): %w", condExpr, res.decideText, why)
	}
	if res.nothing > 0 {
		return false, nil, fmt.Errorf("%w: %q: %s: %w (%v)", ErrUnresolvedReference, condExpr, res.firstNothing, errNotProduced, why)
	}
	return false, nil, fmt.Errorf("%w: %q: %s: %w (%v)", ErrUnresolvedReference, condExpr, res.firstInput, errInputNotSupplied, why)
}
