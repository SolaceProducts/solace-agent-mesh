// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

type scriptedEvaluator struct {
	res decision.Result
	err error
}

func (e scriptedEvaluator) Evaluate(context.Context, decision.Request) (decision.Result, error) {
	return e.res, e.err
}

func TestRecordingEvaluator_AnAnsweredEvaluationRecordsTheServedModelAndItsConfiguration(t *testing.T) {
	bus := newSpendBus(t)
	p := bus.publisher(t)
	attr := attribution()
	const boundConfig, resolvedConfig = "0190c0de-0000-7000-8000-0000000000b1", "0190c0de-0000-7000-8000-0000000000b2"
	attr.ModelConfigID = boundConfig
	inner := scriptedEvaluator{res: decision.Result{Provider: decision.ProviderTypeSafe, Model: "jev-1.13.0", ModelConfigID: resolvedConfig,
		Usage: decision.Usage{InputTokens: 42, OutputTokens: 7}}}

	if _, err := NewRecordingEvaluator(inner, p, attr).Evaluate(t.Context(), decision.Request{Model: "jev-latest"}); err != nil {
		t.Fatal(err)
	}

	events := bus.published(t, p)
	if len(events) != 1 {
		t.Fatalf("%d events, want 1", len(events))
	}
	e := events[0]
	if e.ModelName != "typesafe/jev-1.13.0" || e.ModelConfigID != resolvedConfig || e.Tokens != (Tokens{UncachedInput: 42, Output: 7}) {
		t.Errorf("event model %q config %q tokens %+v, want the served jev-1.13.0, the resolved configuration and 42/7",
			e.ModelName, e.ModelConfigID, e.Tokens)
	}
	if attr.ModelConfigID != boundConfig {
		t.Errorf("the caller's attribution was changed to config %q", attr.ModelConfigID)
	}
}

func TestRecordingEvaluator_AFailureTheProviderBilledIsRecordedWithItsError(t *testing.T) {
	bus := newSpendBus(t)
	p := bus.publisher(t)
	inner := scriptedEvaluator{err: errors.New("refused"),
		res: decision.Result{Model: "jev-1.13.0", Usage: decision.Usage{InputTokens: 9, OutputTokens: 2}}}

	_, err := NewRecordingEvaluator(inner, p, attribution()).Evaluate(t.Context(), decision.Request{})

	if !errors.Is(err, inner.err) {
		t.Errorf("error %v, want the inner %v", err, inner.err)
	}
	if events := bus.published(t, p); len(events) != 1 || events[0].Tokens != (Tokens{UncachedInput: 9, Output: 2}) {
		t.Errorf("events = %+v, want the billed 9/2 recorded", events)
	}
}

func TestRecordingEvaluator_AnEvaluationWithoutUsageRecordsNothingAndWarnsOnlyForAnAnswer(t *testing.T) {
	cases := map[string]struct {
		inner    scriptedEvaluator
		wantWarn bool
	}{
		"an answer that reports no usage": {scriptedEvaluator{res: decision.Result{Model: "jev-quiet"}}, true},
		"a failure nothing answered":      {scriptedEvaluator{err: errors.New("unreachable")}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			bus := newSpendBus(t)
			p := bus.publisher(t)
			logs := &syncBuffer{}
			e := NewRecordingEvaluator(tc.inner, p, attribution()).(*recordingEvaluator)
			e.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			e.missingUsage = newUsageGapLog(time.Minute, time.Now)

			_, err := e.Evaluate(t.Context(), decision.Request{Model: "jev-latest"})

			if !errors.Is(err, tc.inner.err) {
				t.Errorf("error %v, want the inner %v", err, tc.inner.err)
			}
			if events := bus.published(t, p); len(events) != 0 {
				t.Errorf("%d events, want none", len(events))
			}
			if warned := len(linesWith(logs.String(), "level=WARN")) == 1; warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v; logs:\n%s", warned, tc.wantWarn, logs.String())
			}
		})
	}
}

func TestRecordingEvaluator_ANilPublisherOrAttributionLeavesTheEvaluatorUntouched(t *testing.T) {
	inner := &scriptedEvaluator{}
	if got := NewRecordingEvaluator(inner, nil, attribution()); got != decision.Evaluator(inner) {
		t.Errorf("wrapped without a publisher: %T", got)
	}
	if got := NewRecordingEvaluator(inner, newSpendBus(t).publisher(t), nil); got != decision.Evaluator(inner) {
		t.Errorf("wrapped without an attribution: %T", got)
	}
}
