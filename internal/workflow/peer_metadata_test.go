// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestPeerRequestMetadata_AgentNode(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "validate", Type: "agent", AgentName: "DataDoubler"},
	}
	cfg := &WorkflowConfig{
		Name:      "ParallelExecutionWorkflow",
		Namespace: "test-ns",
		Nodes:     nodes,
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("ParallelExecutionWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	const subTaskID = "wf_parent-task-abc_validate_deadbeef"
	e.dispatchAgentNode(context.Background(), nodes[0], subTaskID)

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, "DataDoubler")
	var payload []byte
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			payload = m.Payload
			break
		}
	}
	if payload == nil {
		t.Fatalf("no message published to %s", wantTopic)
	}

	md := extractRequestMetadata(t, payload)
	wantParent := e.execCtx.TaskID
	checkMetadata(t, md, "ParallelExecutionWorkflow", "validate", subTaskID, wantParent)
}

func TestPeerRequestMetadata_MapBranch(t *testing.T) {
	template := &NodeConfig{ID: "double_each", Type: "agent", AgentName: "NumberDoubler"}
	mapNode := &NodeConfig{
		ID:           "fan_out",
		Type:         "map",
		TemplateNode: "double_each",
	}
	cfg := &WorkflowConfig{
		Name:      "MapWorkflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{mapNode, template},
	}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("MapWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	e.launchMapIteration(context.Background(), mapNode, map[string]any{"value": 1}, 0)

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, "NumberDoubler")
	var payload []byte
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			payload = m.Payload
			break
		}
	}
	if payload == nil {
		t.Fatalf("no message published to %s", wantTopic)
	}

	md := extractRequestMetadata(t, payload)
	if md["workflow_name"] != "MapWorkflow" {
		t.Errorf("workflow_name = %v, want MapWorkflow", md["workflow_name"])
	}
	if md["node_id"] != "double_each" {
		t.Errorf("node_id = %v, want double_each", md["node_id"])
	}
	if md["sub_task_id"] == nil || md["sub_task_id"] == "" {
		t.Errorf("sub_task_id missing or empty: %v", md["sub_task_id"])
	}
	if md["parentTaskId"] != e.execCtx.TaskID {
		t.Errorf("parentTaskId = %v, want %q", md["parentTaskId"], e.execCtx.TaskID)
	}
}

func TestPeerRequestMetadata_LoopIteration(t *testing.T) {
	body := &NodeConfig{ID: "worker", Type: "agent", AgentName: "RetryWorker"}
	loopNode := &NodeConfig{
		ID:            "retry_loop",
		Type:          "loop",
		BodyNode:      "worker",
		MaxIterations: 3,
	}
	cfg := &WorkflowConfig{
		Name:      "LoopWorkflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{loopNode, body},
	}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	state := NewExecutionState("LoopWorkflow", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)

	e.dispatchLoopIteration(context.Background(), loopNode, 0)

	wantTopic := a2a.AgentRequestTopic(cfg.Namespace, "RetryWorker")
	var payload []byte
	for _, m := range brk.snapshot() {
		if m.Topic == wantTopic {
			payload = m.Payload
			break
		}
	}
	if payload == nil {
		t.Fatalf("no message published to %s", wantTopic)
	}

	md := extractRequestMetadata(t, payload)
	if md["workflow_name"] != "LoopWorkflow" {
		t.Errorf("workflow_name = %v, want LoopWorkflow", md["workflow_name"])
	}
	if md["node_id"] != "worker" {
		t.Errorf("node_id = %v, want worker", md["node_id"])
	}
	if md["sub_task_id"] == nil || md["sub_task_id"] == "" {
		t.Errorf("sub_task_id missing or empty: %v", md["sub_task_id"])
	}
	if md["parentTaskId"] != e.execCtx.TaskID {
		t.Errorf("parentTaskId = %v, want %q", md["parentTaskId"], e.execCtx.TaskID)
	}
}

func extractRequestMetadata(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var env struct {
		Method string `json:"method"`
		Params struct {
			Message struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("unmarshal request: %v\npayload=%s", err, string(payload))
	}
	if env.Method != "message/send" {
		t.Fatalf("expected method=message/send, got %q", env.Method)
	}
	if env.Params.Message.Metadata == nil {
		t.Fatalf("params.message.metadata is nil; payload=%s", string(payload))
	}
	return env.Params.Message.Metadata
}

func checkMetadata(t *testing.T, md map[string]any, wfName, nodeID, subTaskID, parentTaskID string) {
	t.Helper()
	if md["workflow_name"] != wfName {
		t.Errorf("workflow_name = %v, want %q", md["workflow_name"], wfName)
	}
	if md["node_id"] != nodeID {
		t.Errorf("node_id = %v, want %q", md["node_id"], nodeID)
	}
	if md["sub_task_id"] != subTaskID {
		t.Errorf("sub_task_id = %v, want %q", md["sub_task_id"], subTaskID)
	}
	if md["parentTaskId"] != parentTaskID {
		t.Errorf("parentTaskId = %v, want %q", md["parentTaskId"], parentTaskID)
	}
}
