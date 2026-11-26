// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestWorkflowInstanceActiveTaskCounts(t *testing.T) {
	w := &WorkflowInstance{executions: map[string]*WorkflowExecution{
		"e1": {Context: &WorkflowExecutionContext{UserProps: a2a.UserProperties{Trigger: a2a.TriggerInteractive}}},
		"e2": {Context: &WorkflowExecutionContext{UserProps: a2a.UserProperties{Trigger: a2a.TriggerScheduled}}},
		"e3": {Context: nil},
	}}
	counts := w.ActiveTaskCounts()

	if got := counts[a2a.ActiveTaskKey{Kind: a2a.TaskKindWorkflow, Trigger: a2a.TriggerInteractive}]; got != 1 {
		t.Errorf("interactive workflows = %d, want 1", got)
	}
	if got := counts[a2a.ActiveTaskKey{Kind: a2a.TaskKindWorkflow, Trigger: a2a.TriggerScheduled}]; got != 1 {
		t.Errorf("scheduled workflows = %d, want 1", got)
	}
	if len(counts) != 2 {
		t.Fatalf("got %d series, want 2 (nil-context execution skipped): %v", len(counts), counts)
	}
}
