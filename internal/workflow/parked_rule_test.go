// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import "testing"

func parkedRuleExecutor(state *WorkflowExecutionState, parked map[string]parkedStep) *DAGExecutor {
	return &DAGExecutor{
		dag: &DependencyGraph{Nodes: map[string]*NodeConfig{
			"m":    {ID: "m", Type: "map"},
			"l":    {ID: "l", Type: "loop"},
			"gate": {ID: "gate", Type: "tool"},
		}},
		state:        state,
		parkedPauses: parked,
	}
}

func TestParkedLocked_EachWorkflowShape(t *testing.T) {
	mapBranches := func() map[string][]BranchInfo {
		return map[string][]BranchInfo{"m": {{Index: 0, SubTaskID: "b0"}, {Index: 1, SubTaskID: "b1"}}}
	}
	cases := []struct {
		name   string
		state  *WorkflowExecutionState
		parked map[string]parkedStep
		want   bool
	}{
		{
			name:   "map with one branch parked and one running",
			state:  &WorkflowExecutionState{PendingNodes: map[string]bool{"m": true}, ActiveBranches: mapBranches()},
			parked: map[string]parkedStep{"r0": {nodeID: "inner", subTaskID: "b0", iterOfNode: true}},
			want:   false,
		},
		{
			name:  "map with every open branch parked",
			state: &WorkflowExecutionState{PendingNodes: map[string]bool{"m": true}, ActiveBranches: mapBranches()},
			parked: map[string]parkedStep{
				"r0": {nodeID: "inner", subTaskID: "b0", iterOfNode: true},
				"r1": {nodeID: "inner", subTaskID: "b1", iterOfNode: true},
			},
			want: true,
		},
		{
			name: "map with one branch done and the other parked",
			state: &WorkflowExecutionState{PendingNodes: map[string]bool{"m": true}, ActiveBranches: mapBranches(),
				Metadata: map[string]any{"m_done": []bool{true, false}}},
			parked: map[string]parkedStep{"r1": {nodeID: "inner", subTaskID: "b1", iterOfNode: true}},
			want:   true,
		},
		{
			name: "loop whose current iteration is parked",
			state: &WorkflowExecutionState{PendingNodes: map[string]bool{"l": true},
				ActiveBranches: map[string][]BranchInfo{"l": {{Iteration: 0, SubTaskID: "i0"}, {Iteration: 1, SubTaskID: "i1"}}}},
			parked: map[string]parkedStep{"r": {nodeID: "inner", subTaskID: "i1", iterOfNode: true}},
			want:   true,
		},
		{
			name: "loop whose parked pause is from an earlier iteration",
			state: &WorkflowExecutionState{PendingNodes: map[string]bool{"l": true},
				ActiveBranches: map[string][]BranchInfo{"l": {{Iteration: 0, SubTaskID: "i0"}, {Iteration: 1, SubTaskID: "i1"}}}},
			parked: map[string]parkedStep{"r": {nodeID: "inner", subTaskID: "i0", iterOfNode: true}},
			want:   false,
		},
		{
			name: "parked step with a retry timer pending",
			state: &WorkflowExecutionState{PendingNodes: map[string]bool{"gate": true},
				Timers: []Timer{{Kind: TimerKindRetry, NodeID: "other"}}},
			parked: map[string]parkedStep{"r": {nodeID: "gate"}},
			want:   false,
		},
		{
			name:   "parked step while an outcome is being reached",
			state:  &WorkflowExecutionState{PendingNodes: map[string]bool{"gate": true}, PendingOutcome: "failed"},
			parked: map[string]parkedStep{"r": {nodeID: "gate"}},
			want:   false,
		},
		{
			name:   "parked step inside the exit handler",
			state:  &WorkflowExecutionState{PendingNodes: map[string]bool{"gate": true}, InExitHandler: true},
			parked: map[string]parkedStep{"r": {nodeID: "gate"}},
			want:   false,
		},
		{
			name:   "plain parked step",
			state:  &WorkflowExecutionState{PendingNodes: map[string]bool{"gate": true}},
			parked: map[string]parkedStep{"r": {nodeID: "gate"}},
			want:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parkedRuleExecutor(tc.state, tc.parked).parkedLocked(); got != tc.want {
				t.Errorf("parkedLocked() = %v, want %v", got, tc.want)
			}
		})
	}
}
