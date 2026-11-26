// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
)

func mkForwardEvent(t *testing.T, sigType, sourceAgent, subTaskID string) []byte {
	t.Helper()
	env := map[string]any{
		"id":      subTaskID,
		"jsonrpc": "2.0",
		"result": map[string]any{
			"taskId": subTaskID,
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId": subTaskID,
					"metadata": map[string]any{
						"agent_name": sourceAgent,
					},
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{"type": sigType},
						},
					},
				},
			},
		},
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mkForwardArtifactEvent(t *testing.T, sourceAgent, subTaskID, filename string) []byte {
	t.Helper()
	env := map[string]any{
		"id":      subTaskID,
		"jsonrpc": "2.0",
		"result": map[string]any{
			"taskId": subTaskID,
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId": subTaskID,
					"metadata": map[string]any{
						"agent_name": sourceAgent,
					},
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{
								"type":     "artifact_creation_progress",
								"filename": filename,
							},
						},
					},
				},
			},
		},
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mkForwardArtifactProgressEvent(t *testing.T, sourceAgent, subTaskID, filename, status string, version any) []byte {
	t.Helper()
	env := map[string]any{
		"id":      subTaskID,
		"jsonrpc": "2.0",
		"result": map[string]any{
			"taskId": subTaskID,
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId": subTaskID,
					"metadata": map[string]any{
						"agent_name": sourceAgent,
					},
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{
								"type":     "artifact_creation_progress",
								"filename": filename,
								"status":   status,
								"version":  version,
							},
						},
					},
				},
			},
		},
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func metaOf(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	return got["result"].(map[string]any)["status"].(map[string]any)["message"].(map[string]any)["metadata"].(map[string]any)
}

func TestRewriteForwardedStatus_NonWorkflowSignalSetsForwardedFromPeer(t *testing.T) {
	payload := mkForwardEvent(t, "llm_invocation", "DataDoubler", "sub-1")
	out, _ := rewriteForwardedStatus(payload, "parent-task", "SimpleSequentialWorkflow")
	if out == nil {
		t.Fatal("expected payload, got nil")
	}
	meta := metaOf(t, out)
	if meta["forwarded_from_peer"] != "SimpleSequentialWorkflow" {
		t.Errorf("forwarded_from_peer = %v, want %q (UI dedup marker)", meta["forwarded_from_peer"], "SimpleSequentialWorkflow")
	}
	if meta["agent_name"] != "DataDoubler" {
		t.Errorf("agent_name = %v, want %q (preserved source agent for fallback)", meta["agent_name"], "DataDoubler")
	}
}

func TestRewriteForwardedStatus_WorkflowSignalsForwarded(t *testing.T) {
	cases := []string{
		"workflow_execution_start",
		"workflow_execution_result",
		"workflow_node_execution_start",
		"workflow_node_execution_result",
		"workflow_map_progress",
	}
	for _, sig := range cases {
		t.Run(sig, func(t *testing.T) {
			payload := mkForwardEvent(t, sig, "SimpleSequentialWorkflow", "sub-1")
			out, gotSig := rewriteForwardedStatus(payload, "parent-task", "SimpleSequentialWorkflow")
			if out == nil {
				t.Fatalf("workflow signal %q must be forwarded, got nil (sig=%q)", sig, gotSig)
			}
			meta := metaOf(t, out)
			if meta["forwarded_from_peer"] != "SimpleSequentialWorkflow" {
				t.Errorf("forwarded_from_peer = %v, want %q", meta["forwarded_from_peer"], "SimpleSequentialWorkflow")
			}
			if meta["original_peer_event_taskId"] != "sub-1" {
				t.Errorf("original_peer_event_taskId = %v, want %q", meta["original_peer_event_taskId"], "sub-1")
			}
			chain, ok := meta["peer_task_chain"].([]any)
			if !ok || len(chain) != 1 || chain[0] != "sub-1" {
				t.Errorf("peer_task_chain = %v, want [sub-1]", meta["peer_task_chain"])
			}
		})
	}
}

func TestRewriteForwardedStatus_PlainTextNotForwarded(t *testing.T) {
	env := map[string]any{
		"id":      "sub-1",
		"jsonrpc": "2.0",
		"result": map[string]any{
			"taskId": "sub-1",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"parts": []any{
						map[string]any{"kind": "text", "text": "streaming..."},
					},
				},
			},
		},
	}
	payload, _ := json.Marshal(env)
	out, sig := rewriteForwardedStatus(payload, "parent-task", "wf")
	if out != nil {
		t.Errorf("plain text should be dropped, got payload (sig=%q)", sig)
	}
}

func TestRewriteForwardedStatus_InternalHandoffArtifactProgressFiltered(t *testing.T) {
	payload := mkForwardArtifactEvent(t, "DataDoubler", "sub-1", "wf_write_draft_output_3b8526bf.json")
	out, _ := rewriteForwardedStatus(payload, "parent-task", "wf")
	if out != nil {
		t.Error("artifact_creation_progress for a wf_-prefixed handoff artifact should be dropped")
	}
}

func TestRewriteForwardedStatus_ContentArtifactProgressForwarded(t *testing.T) {
	payload := mkForwardArtifactEvent(t, "DataDoubler", "sub-1", "draft_cheese-tasters.md")
	out, sig := rewriteForwardedStatus(payload, "parent-task", "wf")
	if out == nil {
		t.Fatalf("artifact_creation_progress for a real content artifact must be forwarded, got nil (sig=%q)", sig)
	}
	if sig != "artifact_creation_progress" {
		t.Errorf("sig = %q, want artifact_creation_progress", sig)
	}
}

func TestRewriteForwardedStatus_CompletedWithNoVersionFiltered(t *testing.T) {
	payload := mkForwardArtifactProgressEvent(t, "DataDoubler", "sub-1", "draft_cheese-tasters.md", "completed", nil)
	out, sig := rewriteForwardedStatus(payload, "parent-task", "wf")
	if out != nil {
		t.Error("a completed event with no version (bytes never persisted) should be filtered")
	}
	if sig != "artifact_creation_progress" {
		t.Errorf("sig = %q, want artifact_creation_progress", sig)
	}
}

func TestRewriteForwardedStatus_CompletedWithVersionForwarded(t *testing.T) {
	payload := mkForwardArtifactProgressEvent(t, "DataDoubler", "sub-1", "draft_cheese-tasters.md", "completed", 2)
	out, sig := rewriteForwardedStatus(payload, "parent-task", "wf")
	if out == nil {
		t.Fatalf("a completed event with a real version must be forwarded, got nil (sig=%q)", sig)
	}
	if sig != "artifact_creation_progress" {
		t.Errorf("sig = %q, want artifact_creation_progress", sig)
	}
}

func TestRewriteForwardedStatus_InProgressWithNoVersionStillForwarded(t *testing.T) {
	payload := mkForwardArtifactProgressEvent(t, "DataDoubler", "sub-1", "draft_cheese-tasters.md", "in-progress", nil)
	out, sig := rewriteForwardedStatus(payload, "parent-task", "wf")
	if out == nil {
		t.Fatalf("an in-progress event must still be forwarded despite no version, got nil (sig=%q)", sig)
	}
	if sig != "artifact_creation_progress" {
		t.Errorf("sig = %q, want artifact_creation_progress", sig)
	}
}

func TestRewriteForwardedStatus_RewritesTaskIDs(t *testing.T) {
	payload := mkForwardEvent(t, "llm_invocation", "DataDoubler", "sub-task-orig")
	out, _ := rewriteForwardedStatus(payload, "parent-task", "wf")
	if out == nil {
		t.Fatal("expected payload")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "parent-task" {
		t.Errorf("envelope id = %v, want parent-task", got["id"])
	}
	result := got["result"].(map[string]any)
	if result["taskId"] != "parent-task" {
		t.Errorf("result.taskId = %v, want parent-task", result["taskId"])
	}
	meta := metaOf(t, out)
	if meta["original_peer_event_taskId"] != "sub-task-orig" {
		t.Errorf("original_peer_event_taskId = %v, want sub-task-orig", meta["original_peer_event_taskId"])
	}
}

func TestRewriteForwardedStatus_StripsContextIDSuffix(t *testing.T) {
	env := map[string]any{
		"id":      "sub-1",
		"jsonrpc": "2.0",
		"result": map[string]any{
			"taskId":    "sub-1",
			"contextId": "ctx-parent:sub-1:run",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{"type": "llm_invocation"},
						},
					},
				},
			},
		},
	}
	payload, _ := json.Marshal(env)
	out, _ := rewriteForwardedStatus(payload, "parent-task", "wf")
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	cid := got["result"].(map[string]any)["contextId"]
	if !strings.HasPrefix(cid.(string), "ctx-parent") || strings.Contains(cid.(string), ":") {
		t.Errorf("contextId = %v, want ctx-parent (suffix stripped)", cid)
	}
}

func TestRewriteForwardedStatus_ExtendsIncomingPeerChain(t *testing.T) {
	env := map[string]any{
		"id":      "sub-task",
		"jsonrpc": "2.0",
		"result": map[string]any{
			"taskId": "sub-task",
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"taskId": "sub-task",
					"metadata": map[string]any{
						"agent_name":                 "LeafAgent",
						"original_peer_event_taskId": "leaf-task",
						"peer_task_chain":            []any{"leaf-task"},
					},
					"parts": []any{
						map[string]any{"kind": "data", "data": map[string]any{"type": "llm_invocation"}},
					},
				},
			},
		},
	}
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := rewriteForwardedStatus(payload, "parent-task", "wf")
	if out == nil {
		t.Fatal("expected payload, got nil")
	}
	meta := metaOf(t, out)
	if got := meta["original_peer_event_taskId"]; got != "leaf-task" {
		t.Errorf("original_peer_event_taskId = %v, want %q (leaf preserved across the workflow hop)", got, "leaf-task")
	}
	chain, ok := meta["peer_task_chain"].([]any)
	if !ok {
		t.Fatalf("peer_task_chain = %v (%T), want []any", meta["peer_task_chain"], meta["peer_task_chain"])
	}
	want := []any{"leaf-task", "sub-task"}
	if len(chain) != len(want) || chain[0] != want[0] || chain[1] != want[1] {
		t.Errorf("peer_task_chain = %v, want %v (incoming chain extended with this hop)", chain, want)
	}
}

func TestRewriteForwardedStatusKeepsTheNodesModelAnnouncement(t *testing.T) {
	var envelope map[string]any
	if err := json.Unmarshal(mkForwardEvent(t, "llm_invocation", "DataDoubler", "sub-1"), &envelope); err != nil {
		t.Fatal(err)
	}
	result := envelope["result"].(map[string]any)
	result["metadata"] = map[string]any{
		a2a.MetadataModelFamily:   modelfamily.ClaudeSonnet,
		a2a.MetadataModelProvider: "anthropic",
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}

	out, _ := rewriteForwardedStatus(payload, "parent-task", "PayrollWorkflow")
	if out == nil {
		t.Fatal("expected payload, got nil")
	}
	var forwarded map[string]any
	if err := json.Unmarshal(out, &forwarded); err != nil {
		t.Fatal(err)
	}
	announced, ok := forwarded["result"].(map[string]any)["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("the forwarded event carries no top-level metadata, so a node's model announcement is gone: %s", out)
	}
	if got := announced[a2a.MetadataModelFamily]; got != modelfamily.ClaudeSonnet {
		t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, got, modelfamily.ClaudeSonnet)
	}
	if got := announced[a2a.MetadataModelProvider]; got != "anthropic" {
		t.Errorf("%s = %v, want anthropic", a2a.MetadataModelProvider, got)
	}
}
