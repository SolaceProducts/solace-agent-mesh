// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type gatedEvaluator struct {
	inner decision.Evaluator
	gate  *budget.Gate
	user  string
}

func NewGatedEvaluator(inner decision.Evaluator, attribution *llm.SpendAttribution, gate *budget.Gate) decision.Evaluator {
	if gate == nil {
		return inner
	}
	return &gatedEvaluator{inner: inner, gate: gate, user: attributedUser(attribution)}
}

func (e *gatedEvaluator) Evaluate(ctx context.Context, req decision.Request) (decision.Result, error) {
	if err := e.gate.CheckKnownBlock(e.user); err != nil {
		return decision.Result{}, err
	}
	return e.inner.Evaluate(ctx, req)
}
