// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestNewExecutionState(t *testing.T) {
	s := NewExecutionState("test-wf", "exec-123")

	if s.WorkflowName != "test-wf" {
		t.Errorf("WorkflowName = %q, want %q", s.WorkflowName, "test-wf")
	}
	if s.ExecutionID != "exec-123" {
		t.Errorf("ExecutionID = %q, want %q", s.ExecutionID, "exec-123")
	}
	if s.StartTime.IsZero() {
		t.Error("StartTime should not be zero")
	}

	if s.CompletedNodes == nil {
		t.Error("CompletedNodes should be initialized")
	}
	if s.PendingNodes == nil {
		t.Error("PendingNodes should be initialized")
	}
	if s.SkippedNodes == nil {
		t.Error("SkippedNodes should be initialized")
	}
	if s.NodeOutputs == nil {
		t.Error("NodeOutputs should be initialized")
	}
	if s.ParallelBranchAssignments == nil {
		t.Error("ParallelBranchAssignments should be initialized")
	}
	if s.ActiveBranches == nil {
		t.Error("ActiveBranches should be initialized")
	}
	if s.LoopIterations == nil {
		t.Error("LoopIterations should be initialized")
	}
	if s.RetryCounts == nil {
		t.Error("RetryCounts should be initialized")
	}
	if s.Metadata == nil {
		t.Error("Metadata should be initialized")
	}
}

func TestIsNodeCompleted(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	if s.IsNodeCompleted("a") {
		t.Error("node 'a' should not be completed initially")
	}

	s.CompletedNodes["a"] = "artifact-a"
	if !s.IsNodeCompleted("a") {
		t.Error("node 'a' should be completed after marking")
	}
	if s.IsNodeCompleted("b") {
		t.Error("node 'b' should not be completed")
	}
}

func TestIsNodeSkipped(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	if s.IsNodeSkipped("a") {
		t.Error("node 'a' should not be skipped initially")
	}

	s.SkippedNodes["a"] = "condition not met"
	if !s.IsNodeSkipped("a") {
		t.Error("node 'a' should be skipped after marking")
	}
	if s.IsNodeSkipped("b") {
		t.Error("node 'b' should not be skipped")
	}
}

func TestIsNodePending(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	if s.IsNodePending("a") {
		t.Error("node 'a' should not be pending initially")
	}

	s.PendingNodes["a"] = true
	if !s.IsNodePending("a") {
		t.Error("node 'a' should be pending after setting")
	}
}

func TestAddPending(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	s.AddPending("node1")
	if !s.PendingNodes["node1"] {
		t.Error("AddPending should mark node as pending")
	}

	s.AddPending("node1")
	if !s.PendingNodes["node1"] {
		t.Error("AddPending should remain pending after second call")
	}
}

func TestRemovePending(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	s.PendingNodes["node1"] = true
	s.RemovePending("node1")
	if s.IsNodePending("node1") {
		t.Error("RemovePending should remove node from pending")
	}

	s.RemovePending("nonexistent")
}

func TestNodeOutputs(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	if _, ok := s.NodeOutputs["a"]; ok {
		t.Error("node 'a' should have no output initially")
	}

	s.NodeOutputs["a"] = map[string]any{"output": map[string]any{"result": "hello"}}
	out, ok := s.NodeOutputs["a"]
	if !ok {
		t.Fatal("expected output for node 'a'")
	}
	if out["output"].(map[string]any)["result"] != "hello" {
		t.Errorf("unexpected output: %v", out)
	}
}

func TestRetryCounts(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	if s.RetryCounts["a"] != 0 {
		t.Errorf("initial retry count should be 0, got %d", s.RetryCounts["a"])
	}

	s.RetryCounts["a"] = 1
	s.RetryCounts["b"] = 3
	if s.RetryCounts["a"] != 1 {
		t.Errorf("retry count for 'a' = %d, want 1", s.RetryCounts["a"])
	}
	if s.RetryCounts["b"] != 3 {
		t.Errorf("retry count for 'b' = %d, want 3", s.RetryCounts["b"])
	}
}

func TestWorkflowInputViaNodeOutputs(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	input := map[string]any{"question": "What is SAM?"}
	s.NodeOutputs["workflow_input"] = map[string]any{"output": input}

	out, ok := s.NodeOutputs["workflow_input"]
	if !ok {
		t.Fatal("expected workflow_input in NodeOutputs")
	}
	data, ok := out["output"].(map[string]any)
	if !ok {
		t.Fatal("expected map output")
	}
	if data["question"] != "What is SAM?" {
		t.Errorf("unexpected workflow input: %v", data)
	}
}

func TestErrorState(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	if s.ErrorState != nil {
		t.Error("ErrorState should be nil initially")
	}

	s.ErrorState = &ErrorInfo{
		FailedNodeID:  "node_x",
		FailureReason: "error",
		ErrorMessage:  "something went wrong",
		Timestamp:     "2024-01-01T00:00:00Z",
	}

	if s.ErrorState.FailedNodeID != "node_x" {
		t.Errorf("FailedNodeID = %q, want %q", s.ErrorState.FailedNodeID, "node_x")
	}
	if s.ErrorState.ErrorMessage != "something went wrong" {
		t.Errorf("ErrorMessage = %q, want %q", s.ErrorState.ErrorMessage, "something went wrong")
	}
}

func TestLoopIterations(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	if s.LoopIterations["loop1"] != 0 {
		t.Errorf("initial loop iteration should be 0, got %d", s.LoopIterations["loop1"])
	}

	s.LoopIterations["loop1"] = 5
	if s.LoopIterations["loop1"] != 5 {
		t.Errorf("loop iteration = %d, want 5", s.LoopIterations["loop1"])
	}
}

func TestActiveBranches(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	s.ActiveBranches["map1"] = append(s.ActiveBranches["map1"], BranchInfo{
		Index:     0,
		SubTaskID: "st-1",
	})
	s.ActiveBranches["map1"] = append(s.ActiveBranches["map1"], BranchInfo{
		Index:     1,
		SubTaskID: "st-2",
	})

	branches := s.ActiveBranches["map1"]
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(branches))
	}
	if branches[0].SubTaskID != "st-1" || branches[1].SubTaskID != "st-2" {
		t.Errorf("unexpected branch info: %+v", branches)
	}
}

func TestParallelBranchAssignments(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	s.ParallelBranchAssignments["group1"] = map[string]int{
		"a": 0,
		"b": 1,
	}

	if s.ParallelBranchAssignments["group1"]["a"] != 0 {
		t.Error("expected branch 0 for node 'a'")
	}
	if s.ParallelBranchAssignments["group1"]["b"] != 1 {
		t.Error("expected branch 1 for node 'b'")
	}
}

func TestNewExecutionContext(t *testing.T) {
	ctx := context.Background()
	ec := NewExecutionContext(ctx, "task-1", "ctx-1", nil, a2a.UserProperties{}, false)
	defer ec.Cancel()

	if ec.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", ec.TaskID, "task-1")
	}
	if ec.ContextID != "ctx-1" {
		t.Errorf("ContextID = %q, want %q", ec.ContextID, "ctx-1")
	}
	if ec.IsStructuredInvocation {
		t.Error("IsStructuredInvocation should be false")
	}
}

func TestNewExecutionContext_Structured(t *testing.T) {
	ctx := context.Background()
	ec := NewExecutionContext(ctx, "task-2", "ctx-2", nil, a2a.UserProperties{}, true)
	defer ec.Cancel()

	if !ec.IsStructuredInvocation {
		t.Error("IsStructuredInvocation should be true")
	}
}

func TestExecutionContext_Cancel(t *testing.T) {
	ctx := context.Background()
	ec := NewExecutionContext(ctx, "task-1", "ctx-1", nil, a2a.UserProperties{}, false)

	select {
	case <-ec.Ctx.Done():
		t.Fatal("context should not be cancelled yet")
	default:
	}

	ec.Cancel()

	select {
	case <-ec.Ctx.Done():
	default:
		t.Fatal("context should be cancelled after Cancel()")
	}
}

func TestTrackSubTask(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	s.TrackSubTask("sub-1", "node-a")

	nodeID, ok := s.NodeForSubTask("sub-1")
	if !ok || nodeID != "node-a" {
		t.Errorf("NodeForSubTask(sub-1) = %q, %v; want node-a, true", nodeID, ok)
	}
}

func TestNodeForSubTask(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	if _, ok := s.NodeForSubTask("sub-1"); ok {
		t.Error("expected NodeForSubTask to return false for unknown sub-task")
	}

	s.TrackSubTask("sub-1", "node-a")

	nodeID, ok := s.NodeForSubTask("sub-1")
	if !ok || nodeID != "node-a" {
		t.Errorf("NodeForSubTask(sub-1) = %q, %v; want node-a, true", nodeID, ok)
	}
}

func TestRemoveSubTaskByNode(t *testing.T) {
	s := NewExecutionState("wf", "e1")
	s.TrackSubTask("sub-1", "node-a")
	s.SetSubTaskTarget("sub-1", "agent_broker_a")

	s.RemoveSubTaskByNode("node-a")

	if _, ok := s.NodeForSubTask("sub-1"); ok {
		t.Error("sub-1 should be removed after RemoveSubTaskByNode")
	}
	if _, ok := s.TargetForSubTask("sub-1"); ok {
		t.Error("sub-1 target should be removed after RemoveSubTaskByNode")
	}

	s.RemoveSubTaskByNode("nonexistent")
}

func TestRemoveSubTaskByNode_ThenRetrack(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	s.TrackSubTask("sub-old", "node-a")
	s.RemoveSubTaskByNode("node-a")
	s.TrackSubTask("sub-new", "node-a")

	if _, ok := s.NodeForSubTask("sub-old"); ok {
		t.Error("old sub-task should be removed")
	}

	nodeID, ok := s.NodeForSubTask("sub-new")
	if !ok || nodeID != "node-a" {
		t.Errorf("new sub-task mapping incorrect: %q, %v", nodeID, ok)
	}
}

func TestMultipleSubTasks(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	s.TrackSubTask("sub-1", "node-a")
	s.TrackSubTask("sub-2", "node-b")
	s.TrackSubTask("sub-3", "node-c")

	tests := []struct {
		subTaskID string
		wantNode  string
	}{
		{"sub-1", "node-a"},
		{"sub-2", "node-b"},
		{"sub-3", "node-c"},
	}

	for _, tt := range tests {
		nodeID, ok := s.NodeForSubTask(tt.subTaskID)
		if !ok || nodeID != tt.wantNode {
			t.Errorf("NodeForSubTask(%q) = %q, %v; want %q, true", tt.subTaskID, nodeID, ok, tt.wantNode)
		}
	}
}

func TestUserProperties(t *testing.T) {
	ctx := context.Background()
	props := a2a.UserProperties{
		ClientID:  "client-1",
		UserID:    "user-1",
		CallDepth: 3,
	}
	ec := NewExecutionContext(ctx, "task-1", "ctx-1", nil, props, false)
	defer ec.Cancel()

	if ec.UserProps.ClientID != "client-1" {
		t.Errorf("ClientID = %q, want %q", ec.UserProps.ClientID, "client-1")
	}
	if ec.UserProps.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", ec.UserProps.UserID, "user-1")
	}
	if ec.UserProps.CallDepth != 3 {
		t.Errorf("CallDepth = %d, want 3", ec.UserProps.CallDepth)
	}
}

func TestNodeCompletion_Fields(t *testing.T) {
	tests := []struct {
		name string
		nc   NodeCompletion
	}{
		{
			name: "basic completion",
			nc: NodeCompletion{
				NodeID:    "a",
				SubTaskID: "st-1",
				Output:    map[string]any{"data": "result"},
			},
		},
		{
			name: "error completion",
			nc: NodeCompletion{
				NodeID: "b",
				Error:  context.DeadlineExceeded,
			},
		},
		{
			name: "map branch completion",
			nc: NodeCompletion{
				NodeID:    "c",
				MapNodeID: "map1",
				ItemIndex: 2,
				Output:    map[string]any{"item": "processed"},
			},
		},
		{
			name: "loop iteration completion",
			nc: NodeCompletion{
				NodeID:     "d",
				LoopNodeID: "loop1",
				Output:     map[string]any{"iter": 3},
			},
		},
		{
			name: "retry signal",
			nc: NodeCompletion{
				NodeID: "e",
				Retry:  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.nc.NodeID == "" {
				t.Error("NodeID should not be empty")
			}
		})
	}
}

func TestBranchInfo_Fields(t *testing.T) {
	bi := BranchInfo{
		Index:     3,
		Iteration: 5,
		Result: &BranchResult{
			ArtifactName:    "result-artifact",
			ArtifactVersion: 2,
		},
	}

	if bi.Index != 3 {
		t.Errorf("Index = %d, want 3", bi.Index)
	}
	if bi.Iteration != 5 {
		t.Errorf("Iteration = %d, want 5", bi.Iteration)
	}
	if bi.Result.ArtifactName != "result-artifact" {
		t.Errorf("ArtifactName = %q, want %q", bi.Result.ArtifactName, "result-artifact")
	}
}

func TestStateNodeLifecycle(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	nodeID := "process_data"

	if s.IsNodeCompleted(nodeID) || s.IsNodePending(nodeID) || s.IsNodeSkipped(nodeID) {
		t.Error("node should have no state initially")
	}

	s.AddPending(nodeID)
	if !s.IsNodePending(nodeID) {
		t.Error("node should be pending")
	}
	if s.IsNodeCompleted(nodeID) {
		t.Error("node should not be completed while pending")
	}

	s.RemovePending(nodeID)
	s.CompletedNodes[nodeID] = "artifact-123"
	s.NodeOutputs[nodeID] = map[string]any{"output": map[string]any{"result": "success"}}

	if s.IsNodePending(nodeID) {
		t.Error("node should not be pending after completion")
	}
	if !s.IsNodeCompleted(nodeID) {
		t.Error("node should be completed")
	}
	if s.IsNodeSkipped(nodeID) {
		t.Error("completed node should not be skipped")
	}
}

func TestStateNodeSkipLifecycle(t *testing.T) {
	s := NewExecutionState("wf", "e1")

	nodeID := "optional_step"

	s.SkippedNodes[nodeID] = "condition not met"

	if !s.IsNodeSkipped(nodeID) {
		t.Error("node should be skipped")
	}
	if s.IsNodeCompleted(nodeID) {
		t.Error("skipped node should not be completed")
	}
	if s.IsNodePending(nodeID) {
		t.Error("skipped node should not be pending")
	}
}

func TestExecutionState_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	orig := &WorkflowExecutionState{
		WorkflowName: "wf",
		ExecutionID:  "exec-1",
		StartTime:    now,
		CompletedNodes: map[string]string{
			"a": "wf_a_output_exec-1",
		},
		PendingNodes: map[string]bool{
			"b": true,
		},
		SkippedNodes: map[string]string{
			"skipped_node": "condition not met",
		},
		NodeOutputs: map[string]map[string]any{
			"a": {"output": map[string]any{"result": "ok", "count": float64(3)}},
		},
		ParallelBranchAssignments: map[string]map[string]int{
			"group-1": {"a": 0, "b": 1},
		},
		ActiveBranches: map[string][]BranchInfo{
			"map-1": {{
				Index:     0,
				SubTaskID: "st-map-0",
				Type:      "map",
				Result: &BranchResult{
					ArtifactName:    "wf_map_0_output_exec-1",
					ArtifactVersion: 1,
				},
			}},
		},
		LoopIterations: map[string]int{"loop-1": 2},
		RetryCounts:    map[string]int{"a": 1},
		ErrorState: &ErrorInfo{
			FailedNodeID:  "a",
			FailureReason: "error",
			ErrorMessage:  "boom",
			Timestamp:     "2026-04-24T12:00:00Z",
		},
		Metadata: map[string]any{
			"map-1_total":     float64(2),
			"map-1_completed": float64(1),
		},

		WorkflowDeadline: now.Add(time.Minute),
		Cancelled:        true,
		InExitHandler:    true,
		ExitHandlersRan:  true,
		Finalized:        true,
		SubTasks: map[string]string{
			"st-1": "a",
			"st-2": "b",
		},
		Timers: []Timer{
			{Kind: TimerKindRetry, NodeID: "b", FiresAt: now.Add(5 * time.Second)},
			{Kind: TimerKindWorkflowTimeout, FiresAt: now.Add(time.Minute)},
		},
	}

	orig.SearchTurns.Seed(tool.WebSearchTurnKey, 2)

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"search_turns":{"web_search":2}`) {
		t.Errorf("snapshot does not carry the search turns: %s", data)
	}

	var decoded WorkflowExecutionState
	if uerr := json.Unmarshal(data, &decoded); uerr != nil {
		t.Fatalf("Unmarshal: %v", uerr)
	}

	roundTrip, err := json.Marshal(&decoded)
	if err != nil {
		t.Fatalf("re-Marshal: %v", err)
	}
	if string(data) != string(roundTrip) {
		t.Errorf("JSON round-trip differs:\noriginal: %s\nroundtrip: %s", data, roundTrip)
	}

	if !decoded.WorkflowDeadline.Equal(orig.WorkflowDeadline) {
		t.Errorf("WorkflowDeadline = %v, want %v", decoded.WorkflowDeadline, orig.WorkflowDeadline)
	}
	if !decoded.ExitHandlersRan || !decoded.Cancelled || !decoded.InExitHandler || !decoded.Finalized {
		t.Errorf("phase-A booleans did not round-trip as true: cancelled=%v in_exit=%v exit_ran=%v finalized=%v",
			decoded.Cancelled, decoded.InExitHandler, decoded.ExitHandlersRan, decoded.Finalized)
	}
	if !reflect.DeepEqual(decoded.SubTasks, orig.SubTasks) {
		t.Errorf("SubTasks differ: got %v, want %v", decoded.SubTasks, orig.SubTasks)
	}
	if len(decoded.Timers) != len(orig.Timers) {
		t.Fatalf("Timers length = %d, want %d", len(decoded.Timers), len(orig.Timers))
	}
	for i, want := range orig.Timers {
		got := decoded.Timers[i]
		if got.Kind != want.Kind || got.NodeID != want.NodeID || !got.FiresAt.Equal(want.FiresAt) {
			t.Errorf("Timers[%d] = %+v, want %+v", i, got, want)
		}
	}
}

func TestExecutionState_NoUnserializableFields(t *testing.T) {
	s := NewExecutionState("wf", "e1")
	s.TrackSubTask("st-1", "node-a")
	s.AddTimer(Timer{Kind: TimerKindRetry, NodeID: "node-a", FiresAt: time.Now()})

	if _, err := json.Marshal(s); err != nil {
		t.Fatalf("WorkflowExecutionState must be JSON-serializable end-to-end: %v", err)
	}
}
