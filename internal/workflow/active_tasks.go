// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import "github.com/SolaceProducts/solace-agent-mesh/internal/a2a"

func (w *WorkflowInstance) ActiveTaskCounts() map[a2a.ActiveTaskKey]int {
	w.executionsMu.Lock()
	defer w.executionsMu.Unlock()
	counts := make(map[a2a.ActiveTaskKey]int, len(w.executions))
	for _, exec := range w.executions {
		if exec == nil || exec.Context == nil {
			continue
		}
		counts[a2a.ActiveTaskKey{
			Kind:    a2a.TaskKindWorkflow,
			Trigger: a2a.NormalizeTrigger(exec.Context.UserProps.Trigger),
		}]++
	}
	return counts
}
