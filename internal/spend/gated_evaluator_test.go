// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type countingEvaluator struct{ calls int }

func (e *countingEvaluator) Evaluate(context.Context, decision.Request) (decision.Result, error) {
	e.calls++
	return decision.Result{Model: "jev"}, nil
}

func TestGatedEvaluator_RefusesWithoutReachingTheProviderWhileBlocked(t *testing.T) {
	inner := &countingEvaluator{}
	evaluator := NewGatedEvaluator(inner, nil, budgettest.BlockedGate(t, gatedPeriodEnd))

	_, err := evaluator.Evaluate(t.Context(), decision.Request{})

	assertExhausted(t, err)
	if inner.calls != 0 {
		t.Errorf("provider called %d times while the budget was spent, want 0", inner.calls)
	}
}

func TestGatedEvaluator_PassesEvaluationsThroughWhileTheGateAdmits(t *testing.T) {
	inner := &countingEvaluator{}
	gate := budgettest.NewGate()
	gate.Apply(t.Context(), budgettest.MeshState(false, gatedPeriodEnd, time.Now()))

	res, err := NewGatedEvaluator(inner, nil, gate).Evaluate(t.Context(), decision.Request{})

	if err != nil || res.Model != "jev" || inner.calls != 1 {
		t.Errorf("evaluation = (%+v, %v) after %d provider calls, want the provider's result after 1", res, err, inner.calls)
	}
}

func TestNewGatedEvaluator_WithoutAGateReturnsTheInnerEvaluator(t *testing.T) {
	inner := &countingEvaluator{}

	if got := NewGatedEvaluator(inner, nil, nil); got != decision.Evaluator(inner) {
		t.Errorf("got %T, want the inner evaluator itself", got)
	}
}

func TestGatedEvaluator_RefusesAUserWhoseOwnBudgetIsSpent(t *testing.T) {
	alice := &llm.SpendAttribution{UserSub: "alice"}
	gate := budgettest.UserBlockedGate(t, gatedPeriodEnd, budget.UserKey("", "alice"))

	inner := &countingEvaluator{}
	_, err := NewGatedEvaluator(inner, alice, gate).Evaluate(t.Context(), decision.Request{})
	if exhausted, ok := errors.AsType[*budget.ExhaustedError](err); !ok || exhausted.Tier != budget.TierUser {
		t.Errorf("alice's evaluation = %v, want her own budget refusing it", err)
	}

	bob := &llm.SpendAttribution{UserSub: "bob"}
	if _, err := NewGatedEvaluator(inner, bob, gate).Evaluate(t.Context(), decision.Request{}); err != nil || inner.calls != 1 {
		t.Errorf("bob's evaluation = %v after %d provider calls, want it to reach the provider once", err, inner.calls)
	}
}
