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
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func TestDispatchToolNode_CancelPublishesSTRCancel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		withExit bool
	}{
		{name: "no_exit_handler", withExit: false},
		{name: "with_exit_handler", withExit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertToolNodeCancelPublishesSTRCancel(t, tc.withExit)
		})
	}
}

func assertToolNodeCancelPublishesSTRCancel(t *testing.T, withExit bool) {
	t.Helper()
	const (
		namespace = "ns"
		toolName  = "slow_remote"
	)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	invokeQ, err := brk.CreateQueue("obs-invoke", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create invoke queue: %v", err)
	}
	if err = brk.Subscribe(invokeQ, remotetool.InvokeTopic(namespace, toolName)); err != nil {
		t.Fatalf("subscribe invoke: %v", err)
	}
	cancelQ, err := brk.CreateQueue("obs-cancel", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create cancel queue: %v", err)
	}
	if err = brk.Subscribe(cancelQ, remotetool.CancelSubscription(namespace)); err != nil {
		t.Fatalf("subscribe cancel: %v", err)
	}

	exe := remote.NewExecutor(brk, namespace, "wf", remote.WithInvokeBuffer(2*time.Second))
	if err = exe.Start(t.Context()); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	t.Cleanup(exe.Stop)

	rt := remote.NewTool(&remote.ToolConfig{ToolName: toolName, TimeoutSeconds: 30}, exe)
	tools := tool.NewSet()
	if err = tools.Register(rt); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	cfg := &WorkflowConfig{
		Name:          "wf",
		Namespace:     namespace,
		Nodes:         []*NodeConfig{{ID: "t", Type: "tool", ToolName: toolName}},
		OutputMapping: map[string]any{"x": "{{t.output}}"},
	}
	if withExit {
		cfg.Nodes = append(cfg.Nodes, &NodeConfig{
			ID: "cleanup", Type: "agent", AgentName: "noop-agent", Timeout: 1 * time.Second,
		})
		cfg.OnExit = &ExitHandler{Always: "cleanup"}
	}

	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	for _, id := range exitHandlerNodeIDs(cfg.OnExit) {
		dag.InnerNodes[id] = true
	}

	w := newTestWorkflowInstance(cfg, &stubServices{brk: brk})
	w.dag = dag
	w.tools = tools

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
	t.Cleanup(execCtx.Cancel)

	if err = e.Advance(context.Background(), &EventStart{
		TaskID: "task-c", ContextID: "ctx-c", Input: map[string]any{},
	}); err != nil {
		t.Fatalf("advance start: %v", err)
	}

	recvInvoke, cancelInvoke := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelInvoke()
	if _, err = brk.Receive(recvInvoke, invokeQ); err != nil {
		t.Fatalf("tool never published invoke: %v", err)
	}

	if err = e.Advance(context.Background(), &EventCancel{Reason: "client-requested"}); err != nil {
		t.Fatalf("advance cancel: %v", err)
	}

	recvCancel, cancelCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelCancel()
	cancelMsg, err := brk.Receive(recvCancel, cancelQ)
	if err != nil {
		t.Fatalf("STR cancel not published for in-flight tool node: %v", err)
	}
	if corrID := remotetool.ExtractCancelCorrID(cancelMsg.Topic); corrID == "" {
		t.Fatalf("cancel published on unexpected topic %q", cancelMsg.Topic)
	}

	if !withExit {
		waitForFinalize(t, e, state)
		return
	}

	e.mu.Lock()
	finalized := state.Finalized
	_, cleanupDispatched := state.SubTaskForNode("cleanup")
	e.mu.Unlock()
	if finalized {
		t.Fatal("STR cancel only arrived after finalization — not prompt; it waited for the exit-handler chain")
	}
	if !cleanupDispatched {
		t.Fatal("exit handler 'cleanup' was not dispatched")
	}

	waitForFinalize(t, e, state)
}

func TestDispatchToolBranch_CancelPublishesSTRCancel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nodes []*NodeConfig
		seed  func(*WorkflowExecutionState)
	}{
		{
			name: "map_template",
			nodes: []*NodeConfig{
				{ID: "m", Type: "map", TemplateNode: "tmpl", ItemsExpression: "{{workflow.input.items}}"},
				{ID: "tmpl", Type: "tool", ToolName: "slow_remote", DependsOn: []string{"m"}},
			},
			seed: func(s *WorkflowExecutionState) {
				s.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{"items": []any{"only"}}}
			},
		},
		{
			name: "loop_body",
			nodes: []*NodeConfig{
				{ID: "l", Type: "loop", BodyNode: "body", Condition: "true", MaxIterations: 3},
				{ID: "body", Type: "tool", ToolName: "slow_remote", DependsOn: []string{"l"}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertToolBranchCancelPublishesSTRCancel(t, tc.nodes, tc.seed)
		})
	}
}

func assertToolBranchCancelPublishesSTRCancel(t *testing.T, nodes []*NodeConfig, seed func(*WorkflowExecutionState)) {
	t.Helper()
	const (
		namespace = "ns"
		toolName  = "slow_remote"
	)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	invokeQ, err := brk.CreateQueue("obs-invoke", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create invoke queue: %v", err)
	}
	if err = brk.Subscribe(invokeQ, remotetool.InvokeTopic(namespace, toolName)); err != nil {
		t.Fatalf("subscribe invoke: %v", err)
	}
	cancelQ, err := brk.CreateQueue("obs-cancel", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create cancel queue: %v", err)
	}
	if err = brk.Subscribe(cancelQ, remotetool.CancelSubscription(namespace)); err != nil {
		t.Fatalf("subscribe cancel: %v", err)
	}

	exe := remote.NewExecutor(brk, namespace, "wf", remote.WithInvokeBuffer(2*time.Second))
	if err = exe.Start(t.Context()); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	t.Cleanup(exe.Stop)

	rt := remote.NewTool(&remote.ToolConfig{ToolName: toolName, TimeoutSeconds: 30}, exe)
	tools := tool.NewSet()
	if err = tools.Register(rt); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	cfg := &WorkflowConfig{
		Name:          "wf",
		Namespace:     namespace,
		Nodes:         nodes,
		OutputMapping: map[string]any{"x": "done"},
	}

	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}

	w := newTestWorkflowInstance(cfg, &stubServices{brk: brk})
	w.dag = dag
	w.tools = tools

	state := NewExecutionState("wf", "exec")
	if seed != nil {
		seed(state)
	}
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
	t.Cleanup(execCtx.Cancel)

	if err = e.Advance(context.Background(), &EventStart{
		TaskID: "task-c", ContextID: "ctx-c", Input: map[string]any{},
	}); err != nil {
		t.Fatalf("advance start: %v", err)
	}

	recvInvoke, cancelInvoke := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelInvoke()
	if _, err = brk.Receive(recvInvoke, invokeQ); err != nil {
		t.Fatalf("tool body never published invoke: %v", err)
	}

	if err = e.Advance(context.Background(), &EventCancel{Reason: "client-requested"}); err != nil {
		t.Fatalf("advance cancel: %v", err)
	}

	recvCancel, cancelCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelCancel()
	cancelMsg, err := brk.Receive(recvCancel, cancelQ)
	if err != nil {
		t.Fatalf("STR cancel not published for in-flight tool body: %v", err)
	}
	if corrID := remotetool.ExtractCancelCorrID(cancelMsg.Topic); corrID == "" {
		t.Fatalf("cancel published on unexpected topic %q", cancelMsg.Topic)
	}

	waitForFinalize(t, e, state)
}
