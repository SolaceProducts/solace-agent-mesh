// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

type stubEvaluator struct {
	res decision.Result
	err error
}

func (s stubEvaluator) Evaluate(context.Context, decision.Request) (decision.Result, error) {
	return s.res, s.err
}

func TestInstrumentedEvaluator_CountsTokensAndTimesTheCall(t *testing.T) {
	rec := NewRecordingMetrics()
	wrapped := NewInstrumentedEvaluator(stubEvaluator{res: decision.Result{Usage: decision.Usage{InputTokens: 42, OutputTokens: 7}}},
		"bound-model", "workflow-Triage", rec)

	if _, err := wrapped.Evaluate(t.Context(), decision.Request{Model: "jev-1"}); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	for tokenType, want := range map[string]int64{"input": 42, "output": 7, "cache_read": 0, "cache_write": 0} {
		got := rec.CounterSum("gen_ai.tokens.used", "gen_ai.token.type", tokenType,
			"component.name", "workflow-Triage", "gen_ai.request.model", "jev-1")
		if got != want {
			t.Errorf("%s tokens = %d, want %d", tokenType, got, want)
		}
	}
	if !rec.HasHistogram(metricGenAIOperationDuration, "gen_ai.request.model", "jev-1", "error.type", "") {
		t.Error("no successful duration recorded for jev-1")
	}
}

func TestInstrumentedEvaluator_FailedBilledCallStillCountsAndLabelsTheError(t *testing.T) {
	rec := NewRecordingMetrics()
	refused := &decision.Error{Kind: decision.FailureRefused, Err: errors.New("refused")}
	wrapped := NewInstrumentedEvaluator(stubEvaluator{res: decision.Result{Usage: decision.Usage{InputTokens: 5, OutputTokens: 1}}, err: refused},
		"bound-model", "workflow-Triage", rec)

	if _, err := wrapped.Evaluate(t.Context(), decision.Request{}); !errors.Is(err, refused) {
		t.Fatalf("Evaluate error = %v, want the provider's", err)
	}

	if got := rec.CounterSum("gen_ai.tokens.used", "gen_ai.request.model", "bound-model"); got != 6 {
		t.Errorf("tokens under the bound model = %d, want 6", got)
	}
	if got := rec.HistogramCount(metricGenAIOperationDuration, "gen_ai.request.model", "bound-model", "error.type", "error"); got != 1 {
		t.Errorf("failed call timed %d times under bound-model with error.type=error, want once: %+v",
			got, rec.Histograms(metricGenAIOperationDuration))
	}
}
