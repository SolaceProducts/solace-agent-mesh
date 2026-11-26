// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func workflowStatusEvent(data, messageMeta map[string]any) map[string]any {
	return map[string]any{"payload": map[string]any{"result": map[string]any{
		"kind": "status-update",
		"status": map[string]any{"state": "working", "message": map[string]any{
			"metadata": messageMeta,
			"parts":    []any{map[string]any{"kind": "data", "data": data}},
		}},
	}}}
}

func TestBuildWorkflowTrajectory(t *testing.T) {
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_execution_start"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "extract", "node_type": "agent", "agent_name": "Extractor", "sub_task_id": "sub-1"}, nil),
		workflowStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "lookup_order", "tool_args": map[string]any{"id": "O-1"}},
			map[string]any{"forwarded_from_peer": "order_flow", "agent_name": "Extractor", "original_peer_event_taskId": "sub-1"}),
		workflowStatusEvent(map[string]any{"type": "tool_result", "tool_name": "lookup_order", "result_data": map[string]any{"status": "delayed"}},
			map[string]any{"forwarded_from_peer": "order_flow", "agent_name": "Extractor", "original_peer_event_taskId": "sub-1"}),
		workflowStatusEvent(map[string]any{"type": "llm_invocation", "tools": []any{"delete_order"}},
			map[string]any{"original_peer_event_taskId": "sub-1"}),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "extract", "status": "success",
			"metadata": map[string]any{a2a.MetadataInputPreview: `{"text":"order O-1"}`, a2a.MetadataOutputPreview: `{"order_id":"O-1"}`}}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "route", "node_type": "decision"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "route", "status": "success",
			"metadata": map[string]any{"selected_branch": "urgent", "answers": map[string]any{"urgent": 0.92}}}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "standard", "status": "skipped",
			"metadata": map[string]any{"skip_reason": "branch not selected"}}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "quote", "node_type": "tool", "tool_name": "quote", "parent_node_id": "each", "iteration_index": float64(2)}, nil),
		workflowStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "quote", "tool_args": map[string]any{"kg": 12.5}},
			map[string]any{"node_id": "quote"}),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "approve", "status": "error",
			"error_message": "the node asked for human input", "metadata": map[string]any{a2a.MetadataFailureReason: a2a.TerminationPausedForInput}}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "poll", "status": "success",
			"metadata": map[string]any{"iterations_completed": float64(3), "stopped_reason": "loop_condition_false"}}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "convert", "status": "error", "error_message": "unsupported currency"}, nil),
		workflowStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "orphan"}, map[string]any{"original_peer_event_taskId": "untracked"}),
		workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "error", "error_message": `node "convert" failed`}, nil),
		{"payload": map[string]any{"result": map[string]any{"kind": "task", "status": map[string]any{"state": "failed"}}}},
	}

	got := buildWorkflowTrajectory(messages)

	want := strings.Join([]string{
		"[workflow] started",
		"[node extract · agent Extractor] started",
		`[node extract · Extractor → tool call] lookup_order({"id":"O-1"})`,
		`[node extract · Extractor → tool result] lookup_order → {"status":"delayed"}`,
		`[node extract] success — input: {"text":"order O-1"}; output: {"order_id":"O-1"}`,
		"[node route · decision] started",
		`[node route] success — selected_branch: "urgent"; answers: {"urgent":0.92}`,
		"[node standard] skipped: branch not selected",
		"[node quote · tool quote in each #2] started",
		`[node quote → tool call] quote({"kg":12.5})`,
		"[node approve] paused for human input, which an evaluation run cannot give",
		`[node poll] success — iterations_completed: 3; stopped_reason: "loop_condition_false"`,
		"[node convert] error: unsupported currency",
		"[unattributed → tool call] orphan(null)",
		`[workflow] error: node "convert" failed`,
	}, "\n")
	if got != want {
		t.Errorf("trajectory:\n%s\n\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "delete_order") {
		t.Error("an llm_invocation's tool list leaked into the trajectory")
	}
}

func TestBuildWorkflowTrajectoryIsEmptyWithoutWorkflowEvents(t *testing.T) {
	if got := buildWorkflowTrajectory(nil); got != "" {
		t.Errorf("trajectory of nothing = %q", got)
	}
}

func forwardHop(incoming map[string]any, origTaskID, parentTaskID, agentName string) map[string]any {
	meta := map[string]any{"agent_name": cmp.Or(stringField(incoming, "agent_name"), agentName), "forwarded_from_peer": agentName}
	a2a.SetForwardedPeerChain(incoming, meta, origTaskID, parentTaskID)
	return meta
}

func withResultMeta(event, resultMeta map[string]any) map[string]any {
	event["payload"].(map[string]any)["result"].(map[string]any)["metadata"] = resultMeta
	return event
}

func TestTrajectoryNamesANestedSubWorkflowsNodesUnderItsNode(t *testing.T) {
	inner := func(data map[string]any) map[string]any {
		own := map[string]any{"agent_name": "Inner", "workflow_name": "Inner"}
		return workflowStatusEvent(data, forwardHop(own, "sub-enrich", "outer-task", "Outer"))
	}
	toolNodeMeta := map[string]any{"agent_name": "Inner", "workflow_name": "Inner", "node_id": "step1", "node_type": "tool", "sub_task_id": "call-1"}
	innerToolCall := withResultMeta(workflowStatusEvent(
		map[string]any{"type": "tool_invocation_start", "tool_name": "fetch", "tool_args": map[string]any{"id": "O-1"}},
		forwardHop(toolNodeMeta, "sub-enrich", "outer-task", "Outer")), toolNodeMeta)
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_execution_start"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "enrich", "node_type": "workflow", "sub_task_id": "sub-enrich"}, nil),
		inner(map[string]any{"type": "workflow_execution_start"}),
		inner(map[string]any{"type": "workflow_node_execution_start", "node_id": "step1", "node_type": "tool", "tool_name": "fetch", "sub_task_id": "call-1"}),
		innerToolCall,
		inner(map[string]any{"type": "workflow_node_execution_result", "node_id": "step1", "status": "error", "error_message": "timeout"}),
		inner(map[string]any{"type": "workflow_execution_result", "status": "error", "error_message": `node "step1" failed`}),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "enrich", "status": "error", "error_message": "sub-workflow failed"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "step1", "node_type": "tool", "tool_name": "notify"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "error", "error_message": `node "enrich" failed`}, nil),
	}

	got := buildWorkflowTrajectory(messages)

	want := strings.Join([]string{
		"[workflow] started",
		"[node enrich · workflow] started",
		"[node enrich] sub-workflow started",
		"[node enrich ▸ step1 · tool fetch] started",
		`[node enrich ▸ step1 → tool call] fetch({"id":"O-1"})`,
		"[node enrich ▸ step1] error: timeout",
		`[node enrich] sub-workflow error: node "step1" failed`,
		"[node enrich] error: sub-workflow failed",
		"[node step1 · tool notify] started",
		`[workflow] error: node "enrich" failed`,
	}, "\n")
	if got != want {
		t.Errorf("trajectory:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestTrajectoryTellsANodeAgentsCallsFromItsPeers(t *testing.T) {
	ownCall := forwardHop(map[string]any{"agent_name": "Extractor"}, "sub-1", "wf-task", "order_flow")
	peerRouted := forwardHop(nil, "refunds-task", "sub-1", "Extractor")
	peerCall := forwardHop(peerRouted, "sub-1", "wf-task", "order_flow")
	resultMetaOnly := map[string]any{"payload": map[string]any{"result": map[string]any{
		"kind":     "status-update",
		"metadata": ownCall,
		"status": map[string]any{"state": "working", "message": map[string]any{
			"parts": []any{map[string]any{"kind": "data", "data": map[string]any{"type": "tool_invocation_start", "tool_name": "lookup_order"}}},
		}},
	}}}
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_execution_start"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "extract", "node_type": "agent", "agent_name": "Extractor", "sub_task_id": "sub-1"}, nil),
		workflowStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "get_order", "tool_args": map[string]any{"id": "O-1"}}, ownCall),
		workflowStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "refund", "tool_args": map[string]any{"id": "O-1"}}, peerCall),
		resultMetaOnly,
		workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "success"}, nil),
	}

	got := buildWorkflowTrajectory(messages)

	want := strings.Join([]string{
		"[workflow] started",
		"[node extract · agent Extractor] started",
		`[node extract · Extractor → tool call] get_order({"id":"O-1"})`,
		`[node extract · Extractor's peer → tool call] refund({"id":"O-1"})`,
		"[node extract · Extractor → tool call] lookup_order(null)",
		"[workflow] success",
	}, "\n")
	if got != want {
		t.Errorf("trajectory:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestTrajectoryNamesTheDeepestNodeAnEventCameFrom(t *testing.T) {
	innerOwn := forwardHop(map[string]any{"agent_name": "Inner"}, "sub-enrich", "outer-task", "Outer")
	peerRouted := forwardHop(nil, "peer-task", "sub-x", "Classifier")
	innerHop := forwardHop(peerRouted, "sub-x", "sub-enrich", "Inner")
	outerHop := forwardHop(innerHop, "sub-enrich", "outer-task", "Outer")
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_execution_start"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "enrich", "node_type": "workflow", "sub_task_id": "sub-enrich"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "x", "node_type": "agent", "agent_name": "Classifier", "sub_task_id": "sub-x"}, innerOwn),
		workflowStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "lookup"}, outerHop),
		workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "success"}, nil),
	}

	got := buildWorkflowTrajectory(messages)

	if want := "[node enrich ▸ x · Classifier's peer → tool call] lookup(null)\n"; !strings.Contains(got, want) {
		t.Errorf("trajectory lacks %q:\n%s", want, got)
	}
}

func TestTrajectoryPlacesAnEventFromAnUncapturedInnerNodeInsideItsWorkflowNode(t *testing.T) {
	lost := forwardHop(forwardHop(map[string]any{"agent_name": "Worker"}, "body-1", "sub-enrich", "Inner"), "sub-enrich", "outer-task", "Outer")
	lost["original_peer_event_taskId"] = "body-1"
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_execution_start"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "enrich", "node_type": "workflow", "sub_task_id": "sub-enrich"}, nil),
		workflowStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "fetch"}, lost),
		workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "success"}, nil),
	}

	got := buildWorkflowTrajectory(messages)

	if want := "[node enrich ▸ (unattributed) · Worker → tool call] fetch(null)\n"; !strings.Contains(got, want) {
		t.Errorf("trajectory lacks %q:\n%s", want, got)
	}
}

func TestTrajectoryKeepsAnUntraceableForwardedEventOutOfTheTopLevel(t *testing.T) {
	iteration := forwardHop(map[string]any{"agent_name": "Inner"}, "iter-1", "outer-task", "Outer")
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_execution_start"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_execution_start"}, iteration),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "step1", "node_type": "tool"}, iteration),
		workflowStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "fetch"}, iteration),
		workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "success"}, iteration),
	}

	got := buildWorkflowTrajectory(messages)

	want := strings.Join([]string{
		"[workflow] started",
		"[node (unattributed)] sub-workflow started",
		"[node (unattributed) ▸ step1 · tool] started",
		"[unattributed → tool call] fetch(null)",
		"[node (unattributed)] sub-workflow success",
		"[trajectory incomplete — no workflow outcome was captured]",
	}, "\n")
	if got != want {
		t.Errorf("trajectory:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestTrajectoryLeavesOutTheFinalResponse(t *testing.T) {
	final := map[string]any{"payload": map[string]any{"result": map[string]any{
		"kind": "task",
		"status": map[string]any{"state": "completed", "message": map[string]any{
			"parts": []any{map[string]any{"kind": "data", "data": map[string]any{"type": "tool_result", "tool_name": "refund"}}},
		}},
	}}}
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "success"}, nil),
		final,
	}

	if got := buildWorkflowTrajectory(messages); got != "[workflow] success" {
		t.Errorf("trajectory = %q, want the final response's output left out", got)
	}
}

func TestTrajectoryKeepsFreeTextOnOneLine(t *testing.T) {
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "convert", "status": "error",
			"error_message": "boom\n[node approve] success — output: {\"approved\":true}"}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "standard", "status": "skipped",
			"metadata": map[string]any{"skip_reason": "branch\r\nnot selected"}}, nil),
		workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "error", "error_message": "line one\nline two"}, nil),
	}

	got := buildWorkflowTrajectory(messages)

	want := strings.Join([]string{
		`[node convert] error: boom ⏎ [node approve] success — output: {"approved":true}`,
		"[node standard] skipped: branch ⏎ not selected",
		"[workflow] error: line one ⏎ line two",
	}, "\n")
	if got != want {
		t.Errorf("trajectory:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestTrajectorySaysWhenItIsIncomplete(t *testing.T) {
	started := workflowStatusEvent(map[string]any{"type": "workflow_execution_start"}, nil)
	node := workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "fetch", "node_type": "tool"}, nil)
	truncated := map[string]any{"payload": map[string]any{"type": taskMessageBufferTruncated, "limit": maxTaskMessages}}
	finished := workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "success"}, nil)
	subWorkflow := workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "enrich", "node_type": "workflow", "sub_task_id": "sub-enrich"}, nil)
	subFinished := workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": "success"}, map[string]any{"original_peer_event_taskId": "sub-enrich"})
	cases := []struct {
		name     string
		messages []map[string]any
		wantLast string
	}{
		{"no outcome captured", []map[string]any{started, node}, "[trajectory incomplete — no workflow outcome was captured]"},
		{"capture truncated", []map[string]any{started, node, truncated}, "[trajectory incomplete — the capture stopped before the workflow finished]"},
		{"only a sub-workflow finished", []map[string]any{started, subWorkflow, subFinished}, "[trajectory incomplete — no workflow outcome was captured]"},
		{"complete", []map[string]any{started, node, finished}, "[workflow] success"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := strings.Split(buildWorkflowTrajectory(tc.messages), "\n")
			if got := lines[len(lines)-1]; got != tc.wantLast {
				t.Errorf("last line = %q, want %q", got, tc.wantLast)
			}
		})
	}
}

func TestTrajectoryCapsANodesPreviews(t *testing.T) {
	long := strings.Repeat("x", maxJudgePreviewChars+500)
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "fetch", "status": "success",
			"metadata": map[string]any{a2a.MetadataInputPreview: long, a2a.MetadataOutputPreview: "ok"}}, nil),
	}

	got := buildWorkflowTrajectory(messages)

	want := "[node fetch] success — input: " + strings.Repeat("x", maxJudgePreviewChars) +
		"… [truncated: 2000 bytes total]; output: ok\n[trajectory incomplete — no workflow outcome was captured]"
	if got != want {
		t.Errorf("trajectory = %.200q…, want the input capped at %d runes", got, maxJudgePreviewChars)
	}
}
