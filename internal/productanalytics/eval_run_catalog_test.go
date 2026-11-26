// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"slices"
	"testing"
)

func scrubEvalRun(t *testing.T, props map[string]any) map[string]any {
	t.Helper()
	out, ok := scrub(SurfaceServer, RawEvent{Name: EventEvalRun, Properties: props})
	if !ok {
		t.Fatal("eval:run should be an approved server event")
	}
	return out
}

func TestEvalRunSetupFieldsAreClosedVocabularies(t *testing.T) {
	out := scrubEvalRun(t, map[string]any{
		PropEvaluatorTypes:            []string{"llm_judge", "acme-payroll-judge", "decision", "heuristic"},
		PropTrialsPerExample:          "multiple",
		PropWorkflowTrialFailureKinds: []string{"timeout", `node "reconcile-acme-ledger" failed`, "partial_failure", "node_failed"},
	})
	if got := setField(t, out, PropEvaluatorTypes); !slices.Equal(got, []string{"decision", "heuristic", "llm_judge"}) {
		t.Errorf("evaluatorTypes = %v, want the three types and no evaluator name", got)
	}
	if got := out[PropTrialsPerExample]; got != "multiple" {
		t.Errorf("trialsPerExample = %v, want multiple", got)
	}
	if got := setField(t, out, PropWorkflowTrialFailureKinds); !slices.Equal(got, []string{"node_failed", "timeout"}) {
		t.Errorf("workflowTrialFailureKinds = %v, want only the early-end kinds", got)
	}

	out = scrubEvalRun(t, map[string]any{PropTrialsPerExample: "five"})
	if got, kept := out[PropTrialsPerExample]; kept {
		t.Errorf("trialsPerExample = %v for a value outside the vocabulary, want it dropped", got)
	}
}

func TestEvalRunReportsEveryEarlyEndKind(t *testing.T) {
	out := scrubEvalRun(t, map[string]any{PropWorkflowTrialFailureKinds: EvalWorkflowTrialFailureKinds})
	if got := setField(t, out, PropWorkflowTrialFailureKinds); len(got) != len(EvalWorkflowTrialFailureKinds) {
		t.Errorf("kept %v of %v", got, EvalWorkflowTrialFailureKinds)
	}
}
