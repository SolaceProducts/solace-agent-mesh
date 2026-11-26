// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ctxProbeTool struct {
	name   string
	ran    chan struct{}
	ctxErr error
}

func (t *ctxProbeTool) Name() string                 { return t.name }
func (t *ctxProbeTool) Description() string          { return "records dispatch ctx liveness" }
func (t *ctxProbeTool) Instructions() string         { return "" }
func (t *ctxProbeTool) IsLongRunning() bool          { return false }
func (t *ctxProbeTool) RequiredScopes() []string     { return nil }
func (t *ctxProbeTool) RawStringArgs() []string      { return nil }
func (t *ctxProbeTool) ContextParam() string         { return "" }
func (t *ctxProbeTool) Parameters() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }

func (t *ctxProbeTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }

func (t *ctxProbeTool) Execute(ctx context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	t.ctxErr = ctx.Err()
	close(t.ran)
	return tool.OK("cleaned up"), nil
}

func TestExitHandlerToolNode_RunsOnLiveContextAfterCancel(t *testing.T) {
	blocker := &blockingTool{
		name:      "blocker",
		ctxObs:    make(chan struct{}),
		enterChan: make(chan struct{}),
	}
	probe := &ctxProbeTool{name: "probe", ran: make(chan struct{})}

	cfg := &WorkflowConfig{
		Name:      "wf",
		Namespace: "ns",
		Nodes: []*NodeConfig{
			{ID: "t", Type: "tool", ToolName: "blocker"},
			{ID: "cleanup", Type: "tool", ToolName: "probe", Timeout: 5 * time.Second},
		},
		OutputMapping: map[string]any{"x": "{{t.output}}"},
		OnExit:        &ExitHandler{Always: "cleanup"},
	}

	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	for _, id := range exitHandlerNodeIDs(cfg.OnExit) {
		dag.InnerNodes[id] = true
	}

	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	w.dag = dag
	w.tools = tool.NewSet()
	if err = w.tools.Register(blocker); err != nil {
		t.Fatalf("register blocker: %v", err)
	}
	if err = w.tools.Register(probe); err != nil {
		t.Fatalf("register probe: %v", err)
	}

	state := NewExecutionState("wf", "exec")
	execCtx := NewExecutionContext(context.Background(), "task-c", "ctx-c", nil, a2a.UserProperties{}, false)
	e := &DAGExecutor{
		cfg:      cfg,
		dag:      dag,
		state:    state,
		execCtx:  execCtx,
		instance: w,
		logger:   slog.Default(),
	}
	w.timerManager = NewInProcessTimerManager(slog.Default(), func(execID string, event Event) {
		if execID == state.ExecutionID {
			_ = e.Advance(context.Background(), event)
		}
	})
	go w.timerManager.Start(execCtx.Ctx)
	defer execCtx.Cancel()

	if err = e.Advance(context.Background(), &EventStart{
		TaskID: "task-c", ContextID: "ctx-c", Input: map[string]any{},
	}); err != nil {
		t.Fatalf("advance start: %v", err)
	}

	select {
	case <-blocker.enterChan:
	case <-time.After(2 * time.Second):
		t.Fatal("main tool never entered Execute")
	}

	if err = e.Advance(context.Background(), &EventCancel{Reason: "test"}); err != nil {
		t.Fatalf("advance cancel: %v", err)
	}

	select {
	case <-probe.ran:
	case <-time.After(5 * time.Second):
		t.Fatal("exit-handler tool never ran")
	}

	if probe.ctxErr != nil {
		t.Fatalf("exit-handler tool dispatched on a cancelled context: %v", probe.ctxErr)
	}
}
