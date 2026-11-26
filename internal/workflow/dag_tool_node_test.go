// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestClassifyToolResult(t *testing.T) {
	tests := []struct {
		name      string
		result    *tool.Result
		err       error
		wantErr   string
		wantKind  ErrorKind
		wantData  map[string]any
		failNoErr bool
	}{
		{
			name:     "dispatch error",
			err:      errors.New("boom"),
			wantErr:  "boom",
			wantKind: ErrorKindError,
		},
		{
			name:     "nil result",
			wantErr:  "nil result",
			wantKind: ErrorKindError,
		},
		{
			name:     "failed",
			result:   tool.Failed("kaput", &tool.ToolFailedDetail{Message: "kaput"}),
			wantErr:  "kaput",
			wantKind: ErrorKindFailure,
		},
		{
			name:     "error",
			result:   tool.Error("nope"),
			wantErr:  "nope",
			wantKind: ErrorKindError,
		},
		{
			name:     "pending",
			result:   &tool.Result{Status: tool.StatusPending, Message: "auth"},
			wantErr:  "pending",
			wantKind: ErrorKindError,
		},
		{
			name:      "success with data",
			result:    &tool.Result{Status: tool.StatusSuccess, Message: "done", Data: map[string]any{"x": 1.0}},
			failNoErr: true,
			wantData:  map[string]any{"x": 1.0, "message": "done", "status": tool.StatusSuccess},
		},
		{
			name:      "success without data",
			result:    &tool.Result{Status: tool.StatusSuccess, Message: "ok"},
			failNoErr: true,
			wantData:  map[string]any{"message": "ok", "status": tool.StatusSuccess},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kind, out, err := classifyToolResult("t", tc.result, tc.err, nil)
			if tc.failNoErr {
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}
				for k, v := range tc.wantData {
					if out[k] != v {
						t.Fatalf("output[%q] = %v, want %v", k, out[k], v)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got success", tc.wantErr)
			}
			if !contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
			if kind != tc.wantKind {
				t.Fatalf("kind = %v, want %v", kind, tc.wantKind)
			}
		})
	}
}

func TestParseNodeConfigToolType(t *testing.T) {
	cfg, err := ParseWorkflowConfig("wf", "ns", map[string]any{
		"output_mapping": map[string]any{"x": "${nodes.t.output.message}"},
		"nodes": []any{
			map[string]any{"id": "t", "type": "tool", "tool_name": "echo"},
		},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(cfg.Nodes))
	}
	n := cfg.Nodes[0]
	if n.Type != "tool" {
		t.Fatalf("type = %q, want tool", n.Type)
	}
	if n.ToolName != "echo" {
		t.Fatalf("ToolName = %q, want echo", n.ToolName)
	}
}

func TestParseNodeConfigToolType_MissingToolName(t *testing.T) {
	_, err := ParseWorkflowConfig("wf", "ns", map[string]any{
		"output_mapping": map[string]any{"x": "${nodes.t.output.message}"},
		"nodes": []any{
			map[string]any{"id": "t", "type": "tool"},
		},
	})
	if err == nil {
		t.Fatalf("expected error for missing tool_name")
	}
	if !contains(err.Error(), "tool_name") {
		t.Fatalf("error %q should mention tool_name", err.Error())
	}
}

func TestShouldRetryNode_ToolNodeSkipsRetry(t *testing.T) {
	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns",
		RetryStrategy: &RetryStrategy{Limit: 5, RetryPolicy: "Always"},
		Nodes: []*NodeConfig{
			{ID: "t", Type: "tool", ToolName: "echo"},
		},
	}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	e := &DAGExecutor{
		cfg:   cfg,
		dag:   dag,
		state: NewExecutionState("wf", "exec"),
	}
	if e.shouldRetryNode("t", ErrorKindError) {
		t.Fatalf("tool node must never retry, even with Always policy")
	}
}

func TestBuildToolContext_ForwardsToolConfigAsACopy(t *testing.T) {
	nodes := []*NodeConfig{{ID: "n", Type: "tool", ToolName: "web_request"}}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()

	e.instance.toolConfigs = map[string]map[string]any{
		"web_request": {"marker": "x"},
	}

	withCfg := e.buildToolContext(&NodeConfig{ID: "n", ToolName: "web_request"}, NodeCompletion{NodeID: "n", SubTaskID: "sub-1"})
	if withCfg.ToolConfig["marker"] != "x" {
		t.Errorf("ToolConfig[marker] = %v, want x", withCfg.ToolConfig["marker"])
	}

	noCfg := e.buildToolContext(&NodeConfig{ID: "n2", ToolName: "other"}, NodeCompletion{NodeID: "n2", SubTaskID: "sub-2"})
	if noCfg.ToolConfig != nil {
		t.Errorf("ToolConfig = %v, want nil for a tool with no declared tool_config", noCfg.ToolConfig)
	}

	withCfg.ToolConfig["marker"] = "mutated"
	withCfg.ToolConfig["added"] = true
	if got := e.instance.toolConfigs["web_request"]["marker"]; got != "x" {
		t.Errorf("index was mutated through the handed-out map: marker = %v, want x", got)
	}
	if _, added := e.instance.toolConfigs["web_request"]["added"]; added {
		t.Error("a key added by one dispatch leaked back into the shared index")
	}
	fresh := e.buildToolContext(&NodeConfig{ID: "n3", ToolName: "web_request"}, NodeCompletion{NodeID: "n3", SubTaskID: "sub-3"})
	if fresh.ToolConfig["marker"] != "x" {
		t.Errorf("later dispatch saw a mutated config: marker = %v, want x", fresh.ToolConfig["marker"])
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

var _ = (*DAGExecutor).dispatchToolNode

var _ = context.Background

func TestParseNodeConfigToolType_ConnectorRefWithoutToolName(t *testing.T) {
	_, err := ParseWorkflowConfig("wf", "ns", map[string]any{
		"output_mapping": map[string]any{"x": "${nodes.t.output.message}"},
		"nodes": []any{
			map[string]any{"id": "t", "type": "tool", "connector": "mailer"},
		},
	})
	if err == nil {
		t.Fatal("expected error: connector refs are resolved at platform deploy time, not by the runtime")
	}
	if !contains(err.Error(), "deploy time") {
		t.Fatalf("error %q should explain that connector refs resolve at deploy time", err.Error())
	}
}
