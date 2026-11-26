// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestAgentTaskKind(t *testing.T) {
	if got := agentTaskKind(a2a.Message{}); got != a2a.TaskKindAgent {
		t.Errorf("plain message kind = %q, want %q", got, a2a.TaskKindAgent)
	}
	wf := a2a.Message{Metadata: map[string]any{"workflow_name": "pipeline"}}
	if got := agentTaskKind(wf); got != a2a.TaskKindWorkflowAgent {
		t.Errorf("workflow-node message kind = %q, want %q", got, a2a.TaskKindWorkflowAgent)
	}
	empty := a2a.Message{Metadata: map[string]any{"workflow_name": ""}}
	if got := agentTaskKind(empty); got != a2a.TaskKindAgent {
		t.Errorf("empty workflow_name kind = %q, want %q", got, a2a.TaskKindAgent)
	}
}

func TestAgentActiveTaskCounts(t *testing.T) {
	i := a2a.TriggerInteractive
	a := &Agent{activeTasks: map[string]*activeTaskEntry{
		"t1": {kind: a2a.TaskKindAgent, userProps: a2a.UserProperties{Trigger: i}},
		"t2": {kind: a2a.TaskKindAgent, userProps: a2a.UserProperties{Trigger: i}},
		"t3": {kind: a2a.TaskKindAgent, userProps: a2a.UserProperties{Trigger: a2a.TriggerScheduled}},
		"t4": {kind: a2a.TaskKindWorkflowAgent, userProps: a2a.UserProperties{Trigger: a2a.TriggerScheduled}},
		"t5": {kind: a2a.TaskKindAgent, userProps: a2a.UserProperties{Trigger: "garbage"}},
		"t6": {kind: "", userProps: a2a.UserProperties{}},
	}}
	counts := a.ActiveTaskCounts()

	want := map[a2a.ActiveTaskKey]int{
		{Kind: a2a.TaskKindAgent, Trigger: a2a.TriggerInteractive}:       2,
		{Kind: a2a.TaskKindAgent, Trigger: a2a.TriggerScheduled}:         1,
		{Kind: a2a.TaskKindWorkflowAgent, Trigger: a2a.TriggerScheduled}: 1,
		{Kind: a2a.TaskKindAgent, Trigger: a2a.TriggerUnknown}:           1,
		{Kind: a2a.TaskKindUnknown, Trigger: a2a.TriggerUnknown}:         1,
	}
	if len(counts) != len(want) {
		t.Fatalf("got %d series, want %d: %v", len(counts), len(want), counts)
	}
	for key, n := range want {
		if counts[key] != n {
			t.Errorf("counts[%v] = %d, want %d", key, counts[key], n)
		}
	}
}
