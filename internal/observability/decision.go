// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"cmp"
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type InstrumentedEvaluator struct {
	inner         decision.Evaluator
	model         string
	componentName string
	metrics       runtime.Metrics
}

func NewInstrumentedEvaluator(inner decision.Evaluator, model, componentName string, metrics ...runtime.Metrics) *InstrumentedEvaluator {
	e := &InstrumentedEvaluator{inner: inner, model: model, componentName: componentName}
	if len(metrics) > 0 {
		e.metrics = metrics[0]
	}
	return e
}

func (e *InstrumentedEvaluator) mp() runtime.Metrics {
	if e.metrics != nil {
		return e.metrics
	}
	return runtime.MetricProvider()
}

func (e *InstrumentedEvaluator) Evaluate(ctx context.Context, req decision.Request) (res decision.Result, err error) {
	model := cmp.Or(req.Model, e.model)
	defer NewGenAIMonitor(e.mp(), model).Stop(&err)

	res, err = e.inner.Evaluate(ctx, req)
	e.recordTokenUsage(model, res.Usage)
	return res, err
}

func (e *InstrumentedEvaluator) recordTokenUsage(model string, usage decision.Usage) {
	recordTokenBuckets(e.mp(), model, e.componentName, "",
		[]tokenBucket{{tokenTypeInput, usage.InputTokens}, {tokenTypeOutput, usage.OutputTokens}})
}
