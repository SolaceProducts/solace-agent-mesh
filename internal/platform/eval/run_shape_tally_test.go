// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"slices"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestRunShapeTallyReportsTheRunsSetup(t *testing.T) {
	for _, tc := range []struct {
		name           string
		runsPerExample int
		evaluators     []*store.EvalEvaluator
		wantTypes      []string
		wantMulti      bool
	}{
		{
			name:           "one trial, one judge",
			runsPerExample: 1,
			evaluators:     []*store.EvalEvaluator{{Type: evaluatorTypeLLMJudge}},
			wantTypes:      []string{taxonomy.EvaluatorTypeLLMJudge},
		},
		{
			name:           "several trials",
			runsPerExample: 3,
			evaluators:     []*store.EvalEvaluator{{Type: evaluatorTypeLLMJudge}},
			wantTypes:      []string{taxonomy.EvaluatorTypeLLMJudge},
			wantMulti:      true,
		},
		{
			name:           "types are a deduplicated, sorted set",
			runsPerExample: 2,
			evaluators: []*store.EvalEvaluator{
				{Type: evaluatorTypeLLMJudge}, {Type: evaluatorTypeHeuristic}, {Type: evaluatorTypeLLMJudge},
				{Type: evaluatorTypeDecision}, nil,
			},
			wantTypes: []string{taxonomy.EvaluatorTypeDecision, taxonomy.EvaluatorTypeHeuristic, taxonomy.EvaluatorTypeLLMJudge},
			wantMulti: true,
		},
		{name: "no evaluators", runsPerExample: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shape := newRunShapeTally(&store.EvalExperiment{RunsPerExample: tc.runsPerExample}, tc.evaluators).snapshot()

			if !slices.Equal(shape.EvaluatorTypes, tc.wantTypes) {
				t.Errorf("evaluator types = %v, want %v", shape.EvaluatorTypes, tc.wantTypes)
			}
			if shape.MultiTrial != tc.wantMulti {
				t.Errorf("multi trial = %v, want %v for %d trials per example", shape.MultiTrial, tc.wantMulti, tc.runsPerExample)
			}
			if len(shape.TrialFailureKinds) != 0 {
				t.Errorf("failure kinds = %v before any trial ended", shape.TrialFailureKinds)
			}
		})
	}
}

func TestRunShapeTallyCountsOnlyEarlyEnds(t *testing.T) {
	tally := newRunShapeTally(&store.EvalExperiment{RunsPerExample: 1}, nil)
	for _, kind := range []string{
		FailureKindTimeout, FailureKindNodeFailed, FailureKindTimeout,
		FailureKindPartialFailure, "", "a node's own error text",
	} {
		tally.noteFailureKind(kind)
	}

	want := []string{FailureKindNodeFailed, FailureKindTimeout}
	if got := tally.snapshot().TrialFailureKinds; !slices.Equal(got, want) {
		t.Errorf("failure kinds = %v, want %v", got, want)
	}
}

func TestRunShapeTallySnapshotIsACopyUnderConcurrentTrials(t *testing.T) {
	tally := newRunShapeTally(&store.EvalExperiment{RunsPerExample: 2}, []*store.EvalEvaluator{{Type: evaluatorTypeLLMJudge}})
	var wg sync.WaitGroup
	for _, kind := range taxonomy.EvalWorkflowTrialFailureKinds {
		wg.Go(func() {
			tally.noteFailureKind(kind)
			_ = tally.snapshot()
		})
	}
	wg.Wait()

	got := tally.snapshot()
	if len(got.TrialFailureKinds) != len(taxonomy.EvalWorkflowTrialFailureKinds) {
		t.Errorf("failure kinds = %v, want every kind noted", got.TrialFailureKinds)
	}
	got.TrialFailureKinds[0] = "mutated"
	got.EvaluatorTypes[0] = "mutated"
	again := tally.snapshot()
	if slices.Contains(again.TrialFailureKinds, "mutated") || slices.Contains(again.EvaluatorTypes, "mutated") {
		t.Error("the snapshot aliases the tally's own state")
	}
}

func TestNilRunShapeTallyReportsNothing(t *testing.T) {
	var tally *runShapeTally
	tally.noteFailureKind(FailureKindTimeout)
	if shape := tally.snapshot(); shape != nil {
		t.Errorf("shape = %+v from a nil tally, want nil", shape)
	}
}

func TestAnAbandonedTrialWithNoKindLeavesTheEndsUnknown(t *testing.T) {
	agent := newRunShapeTally(&store.EvalExperiment{RunsPerExample: 1}, nil)
	agent.noteAbandoned("")
	if shape := agent.snapshot(); shape.TrialFailureKindsUnknown {
		t.Errorf("shape = %+v, want an agent run's ends known: its trials record no kind", *shape)
	}
	tally := newRunShapeTally(&store.EvalExperiment{RunsPerExample: 1, TargetType: store.EvalTargetTypeWorkflow}, nil)
	tally.noteAbandoned(FailureKindTimeout)
	if shape := tally.snapshot(); !slices.Equal(shape.TrialFailureKinds, []string{FailureKindTimeout}) || shape.TrialFailureKindsUnknown {
		t.Errorf("shape = %+v, want timeout and every end known", *shape)
	}
	tally.noteAbandoned("")
	if shape := tally.snapshot(); !shape.TrialFailureKindsUnknown {
		t.Errorf("shape = %+v, want the ends unknown after a trial abandoned with no kind", *shape)
	}
}

func TestAnUnreportableKindLeavesTheEndsUnknown(t *testing.T) {
	workflow := newRunShapeTally(&store.EvalExperiment{TargetType: store.EvalTargetTypeWorkflow}, nil)
	workflow.noteFailureKind(FailureKindPartialFailure)
	workflow.noteFailureKind("")
	if shape := workflow.snapshot(); len(shape.TrialFailureKinds) != 0 || shape.TrialFailureKindsUnknown {
		t.Errorf("shape = %+v, want no early end after a graded and an unclassified trial", *shape)
	}
	workflow.noteFailureKind("quota_exceeded")
	if shape := workflow.snapshot(); !shape.TrialFailureKindsUnknown {
		t.Errorf("shape = %+v, want the ends unknown after a kind outside the vocabulary", *shape)
	}
	agent := newRunShapeTally(&store.EvalExperiment{}, nil)
	agent.noteFailureKind("quota_exceeded")
	if shape := agent.snapshot(); shape.TrialFailureKindsUnknown {
		t.Errorf("shape = %+v, want an agent run's ends known", *shape)
	}
}

func TestFailureKindSpellingsAreStable(t *testing.T) {
	got := []string{
		FailureKindNodeFailed, FailureKindPausedForInput, FailureKindTimeout, FailureKindCancelled,
		FailureKindAuthRequired, FailureKindInvalidInput, FailureKindRejected, FailureKindPartialFailure,
	}
	want := []string{
		"node_failed", "paused_for_input", "timeout", "cancelled",
		"auth_required", "invalid_input", "rejected", "partial_failure",
	}
	if !slices.Equal(got, want) {
		t.Errorf("failure kinds = %v, want %v", got, want)
	}
}

func TestWorkflowTrialFailureKindsMatchTheCatalog(t *testing.T) {
	earlyEnds := []string{
		FailureKindNodeFailed, FailureKindPausedForInput, FailureKindTimeout, FailureKindCancelled,
		FailureKindAuthRequired, FailureKindInvalidInput, FailureKindRejected,
	}
	got := slices.Clone(taxonomy.EvalWorkflowTrialFailureKinds)
	slices.Sort(got)
	slices.Sort(earlyEnds)
	if !slices.Equal(got, earlyEnds) {
		t.Errorf("catalog failure kinds = %v, want the early-end kinds %v", got, earlyEnds)
	}
	if slices.Contains(got, FailureKindPartialFailure) {
		t.Error("partial_failure is graded, so it is no early end")
	}
}
