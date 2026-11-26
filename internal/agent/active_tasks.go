// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import "github.com/SolaceProducts/solace-agent-mesh/internal/a2a"

const metaKeyWorkflowName = "workflow_name"

func agentTaskKind(msg a2a.Message) string {
	if wn, _ := msg.Metadata[metaKeyWorkflowName].(string); wn != "" {
		return a2a.TaskKindWorkflowAgent
	}
	return a2a.TaskKindAgent
}

func (a *Agent) ActiveTaskCounts() map[a2a.ActiveTaskKey]int {
	a.activeTasksMu.Lock()
	defer a.activeTasksMu.Unlock()
	counts := make(map[a2a.ActiveTaskKey]int, len(a.activeTasks))
	for _, entry := range a.activeTasks {
		kind := entry.kind
		if kind == "" {
			kind = a2a.TaskKindUnknown
		}
		counts[a2a.ActiveTaskKey{
			Kind:    kind,
			Trigger: a2a.NormalizeTrigger(entry.userProps.Trigger),
		}]++
	}
	return counts
}
