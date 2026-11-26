// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

type sharedNameWriterTool struct{}

func (sharedNameWriterTool) Name() string                                      { return "shared_name_writer" }
func (sharedNameWriterTool) Description() string                               { return "writes same.txt" }
func (sharedNameWriterTool) Instructions() string                              { return "" }
func (sharedNameWriterTool) IsLongRunning() bool                               { return false }
func (sharedNameWriterTool) RequiredScopes() []string                          { return nil }
func (sharedNameWriterTool) RawStringArgs() []string                           { return nil }
func (sharedNameWriterTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (sharedNameWriterTool) ContextParam() string                              { return "" }
func (sharedNameWriterTool) Parameters() *tool.JSONSchema                      { return &tool.JSONSchema{Type: "object"} }

func (sharedNameWriterTool) Execute(ctx context.Context, args map[string]any, tc *tool.Context) (*tool.Result, error) {
	item := fmt.Sprint(args["value"])
	if _, err := tc.Facade().SaveArtifactWithMetadata(ctx, "same.txt", []byte(item), "text/plain", artifact.MetadataOpts{}); err != nil {
		return tool.Error(err.Error()), nil
	}
	return tool.OK("wrote", tool.WithData(map[string]any{"item": item})), nil
}

func TestMapIterationsReadTheirOwnCreatedArtifact(t *testing.T) {
	items := []any{"A", "B", "C", "D", "E", "F"}
	art := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	svc := &instTmplServices{stubServices: &stubServices{brk: brk}, art: art}

	cfg := &WorkflowConfig{
		Name:      "wf",
		Namespace: "ns",
		FailFast:  true,
		OutputMapping: map[string]any{
			"written": "{{ fan.output.results }}",
			"read":    "{{ readback.output.results }}",
		},
		Tools: []any{
			map[string]any{"tool_type": "builtin", "tool_name": "shared_name_writer"},
			map[string]any{"tool_type": "builtin", "tool_name": "load_artifact"},
		},
		Nodes: []*NodeConfig{
			{ID: "fan", Type: "map", TemplateNode: "write", ItemsExpression: "{{ workflow.input.items }}", ConcurrencyLimit: len(items)},
			{ID: "write", Type: "tool", ToolName: "shared_name_writer", DependsOn: []string{"fan"},
				Input: map[string]any{"value": "{{ _map_item }}"}},
			{ID: "readback", Type: "map", TemplateNode: "read", ItemsExpression: "{{ fan.output.results }}",
				ConcurrencyLimit: len(items), DependsOn: []string{"fan"}},
			{ID: "read", Type: "tool", ToolName: "load_artifact", DependsOn: []string{"readback"},
				Input: map[string]any{"filename": "{{ _map_item.created_artifact }}"}},
		},
	}

	w := newTestWorkflowInstance(cfg, svc)
	w.SetToolInitOptions(toolinit.Options{
		AdditionalBuiltin: func(name string, _ map[string]any) tool.BuiltinTool {
			if name == "shared_name_writer" {
				return sharedNameWriterTool{}
			}
			return nil
		},
	})
	if err := w.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := w.startSubComponents(context.Background()); err != nil {
		t.Fatalf("start sub-components: %v", err)
	}
	t.Cleanup(w.stopSubComponents)

	input := map[string]any{"items": items}
	state := NewExecutionState(cfg.Name, "exec")
	state.NodeOutputs["workflow_input"] = map[string]any{"output": input}
	execCtx := NewExecutionContext(context.Background(), "task-t", "ctx-t", nil, a2a.UserProperties{UserID: "u1"}, false)
	t.Cleanup(execCtx.Cancel)
	e := &DAGExecutor{cfg: cfg, dag: w.dag, state: state, execCtx: execCtx, instance: w, logger: slog.Default()}
	w.timerManager = NewInProcessTimerManager(slog.Default(), func(id string, ev Event) {
		if id == state.ExecutionID {
			_ = e.Advance(context.Background(), ev)
		}
	})
	go w.timerManager.Start(execCtx.Ctx)

	if err := e.Advance(context.Background(), &EventStart{TaskID: "task-t", ContextID: "ctx-t", Input: input}); err != nil {
		t.Fatalf("advance start: %v", err)
	}
	waitForFinalize(t, e, state)
	if state.ErrorState != nil {
		t.Fatalf("workflow failed: %+v", state.ErrorState)
	}

	written := mapResults(t, state, "fan")
	read := mapResults(t, state, "readback")
	if len(written) != len(items) || len(read) != len(items) {
		t.Fatalf("got %d writes and %d reads, want %d each", len(written), len(read), len(items))
	}
	for i := range items {
		if read[i]["content"] != written[i]["item"] {
			t.Errorf("iteration %d wrote %q but read %q back through created_artifact %q",
				i, written[i]["item"], read[i]["content"], written[i]["created_artifact"])
		}
	}
}

func mapResults(t *testing.T, state *WorkflowExecutionState, nodeID string) []map[string]any {
	t.Helper()
	output, _ := state.NodeOutputs[nodeID]["output"].(map[string]any)
	raw, _ := output["results"].([]any)
	results := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("%s result %#v is not an object", nodeID, r)
		}
		results = append(results, m)
	}
	return results
}
