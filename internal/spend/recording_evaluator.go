// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"cmp"
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type recordingEvaluator struct {
	inner        decision.Evaluator
	publisher    *Publisher
	attribution  *llm.SpendAttribution
	logger       *slog.Logger
	missingUsage *usageGapLog
}

func NewRecordingEvaluator(inner decision.Evaluator, publisher *Publisher, attribution *llm.SpendAttribution) decision.Evaluator {
	if publisher == nil || attribution == nil {
		return inner
	}
	return &recordingEvaluator{inner: inner, publisher: publisher, attribution: attribution,
		logger: publisher.logger, missingUsage: unreportedUsage}
}

func (e *recordingEvaluator) Evaluate(ctx context.Context, req decision.Request) (decision.Result, error) {
	res, err := e.inner.Evaluate(ctx, req)
	tokens := Tokens{
		UncachedInput: int64(max(res.Usage.InputTokens, 0)),
		Output:        int64(max(res.Usage.OutputTokens, 0)),
	}
	if tokens == (Tokens{}) {
		e.missingUsage.note(e.logger, cmp.Or(res.Model, req.Model), err == nil)
		return res, err
	}
	attribution := *e.attribution
	attribution.ModelConfigID = cmp.Or(res.ModelConfigID, attribution.ModelConfigID)
	e.publisher.Record(NewEvent(&attribution, llm.QualifiedModel(res.Provider, cmp.Or(res.Model, req.Model)), tokens, 0))
	return res, err
}
