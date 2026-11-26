// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func newHILInstance(t *testing.T) (*WorkflowInstance, broker.Broker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	db, err := wfstore.Open("sqlite:///"+filepath.Join(t.TempDir(), "wf.db"), slog.Default())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	w := &WorkflowInstance{
		cfg:            &WorkflowConfig{Name: "wf", Namespace: "test-ns"},
		svc:            rt.Services(),
		logger:         slog.Default(),
		ctx:            t.Context(),
		executions:     map[string]*WorkflowExecution{},
		executionsByID: map[string]*WorkflowExecution{},
		subTaskIndex:   map[string]string{},
		pausedDB:       db,
		pausedRepo:     wfstore.NewPausedRepo(db),
	}
	return w, brk
}

func TestPauseEventForResult(t *testing.T) {
	routing := NodeCompletion{NodeID: "n", SubTaskID: "st", MapNodeID: "m", ItemIndex: 3}

	if ev := pauseEventForResult(routing, &tool.Result{Status: tool.StatusSuccess}); ev != nil {
		t.Errorf("a success result must not pause, got %+v", ev)
	}
	if ev := pauseEventForResult(routing, &tool.Result{Status: tool.StatusPending, Data: map[string]any{}}); ev != nil {
		t.Errorf("pending without hil_request_id is not routable; must not pause")
	}
	ev := pauseEventForResult(routing, &tool.Result{Status: tool.StatusPending, Data: map[string]any{
		"hil_request_id": "req-1",
		"surface":        map[string]any{"surfaceId": "s"},
	}})
	if ev == nil {
		t.Fatal("pending + hil_request_id must produce a pause event")
	}
	if ev.RequestID != "req-1" || ev.NodeID != "n" || ev.SubTaskID != "st" || ev.MapNodeID != "m" || ev.ItemIndex != 3 {
		t.Errorf("pause event routing mismatch: %+v", ev)
	}
	if len(ev.Surface) == 0 || ev.ExpiresAt == "" {
		t.Errorf("surface + expiry must be set: surfaceLen=%d expires=%q", len(ev.Surface), ev.ExpiresAt)
	}
}

func TestPauseWorkflowNode_PersistsAndRoutes(t *testing.T) {
	w, brk := newHILInstance(t)
	ctx := t.Context()

	q, err := brk.CreateQueue("notify-req", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.NotifyRequestSubscription("test-ns")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	state := NewExecutionState("wf", "exec-1")
	state.AddPending("approve")
	execCtx := NewExecutionContext(w.ctx, "task-1", "ctx-1", nil,
		a2a.UserProperties{UserID: "trent@example.org", TraceID: "tr-1"}, false)
	e := &DAGExecutor{cfg: w.cfg, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}

	evt := &EventNodePaused{
		NodeID: "approve", SubTaskID: "st-1", RequestID: "req-1",
		Surface:   json.RawMessage(`{"surfaceId":"s"}`),
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}
	if err = w.pauseWorkflowNode(ctx, e, evt); err != nil {
		t.Fatalf("pauseWorkflowNode: %v", err)
	}

	row, err := w.pausedRepo.Get(ctx, "req-1")
	if err != nil || row == nil {
		t.Fatalf("paused row not found: row=%v err=%v", row, err)
	}
	wantAddr := a2a.NotifyResponseTopic("test-ns", "wf", "req-1")
	if row.ExecutionID != "exec-1" || row.NodeID != "approve" || row.RecipientUserID != "trent@example.org" || row.RequesterAddr != wantAddr {
		t.Errorf("paused row mismatch: %+v", row)
	}
	parked := parkAndRebuild(t, w, e, nil)
	if !parked.State.PendingNodes["approve"] {
		t.Errorf("the parked state must keep the paused node pending, got %+v", parked.State.PendingNodes)
	}

	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive NotifyRequest: %v", err)
	}
	var nr a2a.NotifyRequest
	if err := json.Unmarshal(msg.Payload, &nr); err != nil {
		t.Fatalf("payload not a NotifyRequest: %v", err)
	}
	if nr.Mode != a2a.NotifyModeInput || nr.RequesterAddr != wantAddr || nr.RecipientUserID != "trent@example.org" || len(nr.Surface) == 0 {
		t.Errorf("input request mismatch: %+v", nr)
	}
	if nr.Metadata["session_id"] != "ctx-1" || nr.Metadata["trace_id"] != "tr-1" {
		t.Errorf("metadata session_id=%v trace_id=%v, want ctx-1 and tr-1", nr.Metadata["session_id"], nr.Metadata["trace_id"])
	}
}

func TestReconstructExecution_RestoresState(t *testing.T) {
	w, _ := newHILInstance(t)
	w.cfg = &WorkflowConfig{Name: "wf", Namespace: "test-ns"}
	w.dag = &DependencyGraph{}

	state := NewExecutionState("wf", "exec-9")
	state.NodeOutputs["earlier"] = map[string]any{"output": map[string]any{"k": "v"}}
	state.AddPending("approve")
	stateJSON, _ := json.Marshal(state)
	ecJSON, _ := json.Marshal(persistedExecContext{
		UserProps: a2a.UserProperties{UserID: "trent", TraceID: "tr"}, TaskID: "task-9", ContextID: "ctx-9",
	})
	row := &wfstore.PausedWorkflow{
		RequestID: "req-9", ExecutionID: "exec-9", WorkflowName: "wf", NodeID: "approve",
		StateSnapshot: string(stateJSON), ExecContext: string(ecJSON),
		Routing: `{"node_id":"approve","sub_task_id":"st-9"}`,
	}

	exec, err := w.reconstructExecution(row)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if exec.State.ExecutionID != "exec-9" || !exec.State.PendingNodes["approve"] {
		t.Errorf("state not restored: %+v", exec.State)
	}
	if exec.State.NodeOutputs["earlier"]["output"] == nil {
		t.Errorf("prior node outputs must survive reconstruction")
	}
	if exec.Context.TaskID != "task-9" || exec.Context.ContextID != "ctx-9" || exec.Context.UserProps.UserID != "trent" {
		t.Errorf("exec context not restored: %+v", exec.Context)
	}
	if exec.Context.Ctx == nil || exec.Executor == nil || exec.Executor.instance != w {
		t.Errorf("executor/context not wired to the instance")
	}
}

func TestResumePausedNode_FirstWins(t *testing.T) {
	w, brk := newHILInstance(t)
	ctx := t.Context()

	node := &NodeConfig{ID: "approve", Type: "tool", ToolName: "ask"}
	dag, err := BuildDependencyGraph([]*NodeConfig{node})
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	w.cfg = &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: []*NodeConfig{node}}
	w.dag = dag

	const statusTopic = "test-ns/status/wf-resume"
	sq, err := brk.CreateQueue("status-sink", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create status queue: %v", err)
	}
	if err := brk.Subscribe(sq, statusTopic); err != nil {
		t.Fatalf("subscribe status: %v", err)
	}

	state := NewExecutionState("wf", "exec-1")
	state.AddPending("approve")
	state.TrackSubTask("st-1", "approve")
	stateJSON, _ := json.Marshal(state)
	ecJSON, _ := json.Marshal(persistedExecContext{
		UserProps: a2a.UserProperties{UserID: "trent", A2AStatusTopic: statusTopic}, TaskID: "task-1", ContextID: "ctx-1",
	})
	if err := w.pausedRepo.Insert(ctx, wfstore.PausedWorkflow{
		RequestID: "req-1", ExecutionID: "exec-1", WorkflowName: "wf", NodeID: "approve",
		RequesterAddr: a2a.NotifyResponseTopic("test-ns", "wf", "req-1"),
		StateSnapshot: string(stateJSON), ExecContext: string(ecJSON),
		Routing: `{"node_id":"approve","sub_task_id":"st-1"}`,
	}); err != nil {
		t.Fatalf("insert paused: %v", err)
	}

	w.answer(ctx, "req-1", "resumed", "answered", map[string]any{"choice": "approve"})

	row, _ := w.pausedRepo.Get(ctx, "req-1")
	if row == nil || row.Status != "resumed" {
		t.Fatalf("row should be claimed resumed, got %+v", row)
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := brk.Receive(rctx, sq); err != nil {
		t.Fatalf("resume must advance the workflow to publish its final result: %v", err)
	}
	if w.executionsByID["exec-1"] != nil {
		t.Errorf("a finalized single-node run should be cleaned up, still registered")
	}

	w.answer(ctx, "req-1", "resumed", "answered", nil)
	if again, _ := w.pausedRepo.Get(ctx, "req-1"); again == nil || again.Status != "resumed" {
		t.Errorf("duplicate resume must leave the row resolved, got %+v", again)
	}
}

func seedOneNodePaused(t *testing.T, w *WorkflowInstance, requestID string, expiresAt *int64) {
	t.Helper()
	node := &NodeConfig{ID: "approve", Type: "tool", ToolName: "ask"}
	dag, err := BuildDependencyGraph([]*NodeConfig{node})
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	w.cfg = &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: []*NodeConfig{node}}
	w.dag = dag

	state := NewExecutionState("wf", "exec-"+requestID)
	state.AddPending("approve")
	state.TrackSubTask("st-"+requestID, "approve")
	stateJSON, _ := json.Marshal(state)
	ecJSON, _ := json.Marshal(persistedExecContext{
		UserProps: a2a.UserProperties{UserID: "trent", A2AStatusTopic: "test-ns/status/seed"}, TaskID: "task-" + requestID, ContextID: "ctx",
	})
	if err := w.pausedRepo.Insert(t.Context(), wfstore.PausedWorkflow{
		RequestID: requestID, ExecutionID: "exec-" + requestID, WorkflowName: "wf", NodeID: "approve",
		RequesterAddr: a2a.NotifyResponseTopic("test-ns", "wf", requestID),
		StateSnapshot: string(stateJSON), ExecContext: string(ecJSON),
		Routing:   `{"node_id":"approve","sub_task_id":"st-` + requestID + `"}`,
		ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("insert paused: %v", err)
	}
}

func TestHandlePause_ViaAdvance(t *testing.T) {
	w, brk := newHILInstance(t)
	ctx := t.Context()
	q, err := brk.CreateQueue("nreq2", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.NotifyRequestSubscription("test-ns")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	node := &NodeConfig{ID: "approve", Type: "tool", ToolName: "ask"}
	dag, _ := BuildDependencyGraph([]*NodeConfig{node})
	w.cfg = &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: []*NodeConfig{node}}
	w.dag = dag

	state := NewExecutionState("wf", "exec-2")
	state.AddPending("approve")
	state.TrackSubTask("st-2", "approve")
	state.Timers = append(state.Timers, Timer{Kind: TimerKindNodeTimeout, SubTaskID: "st-2", NodeID: "approve"})
	execCtx := NewExecutionContext(w.ctx, "task-2", "ctx-2", nil, a2a.UserProperties{UserID: "trent"}, false)
	e := &DAGExecutor{cfg: w.cfg, dag: w.dag, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}

	if err := e.Advance(ctx, &EventNodePaused{
		NodeID: "approve", SubTaskID: "st-2", RequestID: "req-2",
		Surface: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("Advance(EventNodePaused): %v", err)
	}

	if row, _ := w.pausedRepo.Get(ctx, "req-2"); row == nil {
		t.Fatal("Advance(EventNodePaused) must persist a paused row")
	}
	if !state.PendingNodes["approve"] {
		t.Error("paused node must remain pending (no completion)")
	}
	for _, tm := range state.Timers {
		if tm.SubTaskID == "st-2" {
			t.Error("the per-node timeout must be cancelled on pause")
		}
	}
	if _, err := brk.Receive(ctx, q); err != nil {
		t.Errorf("pause must route an input request: %v", err)
	}
}

func TestHandleWorkflowHILResponse_ResumesAndGuards(t *testing.T) {
	w, _ := newHILInstance(t)
	ctx := t.Context()
	seedOneNodePaused(t, w, "req-H", nil)

	payload, _ := json.Marshal(a2a.UserInputResponseData{RequestID: "req-H", Status: "answered", Data: map[string]any{"x": 1}})
	w.handleWorkflowHILResponse(ctx, &broker.Message{Payload: payload})
	if row, _ := w.pausedRepo.Get(ctx, "req-H"); row == nil || row.Status != "resumed" {
		t.Fatalf("response must claim+resume the row, got %+v", row)
	}

	w.handleWorkflowHILResponse(ctx, &broker.Message{Payload: []byte(`{bad`)})
	noID, _ := json.Marshal(a2a.UserInputResponseData{Status: "answered"})
	w.handleWorkflowHILResponse(ctx, &broker.Message{Payload: noID})
}

func TestSweepHILTimeouts_PublishesATimeoutPastTheMargin(t *testing.T) {
	w, brk := newHILInstance(t)
	ctx := t.Context()
	q, err := brk.CreateQueue("answers", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.NotifyResponseSubscription("test-ns", "wf")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	withinMargin := time.Now().Add(-time.Minute).UnixMilli()
	pastMargin := time.Now().Add(-3 * time.Minute).UnixMilli()
	seedOneNodePaused(t, w, "req-recent", &withinMargin)
	seedOneNodePaused(t, w, "req-T", &pastMargin)

	w.sweepHILTimeouts(ctx)

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive the backstop timeout: %v", err)
	}
	var resp a2a.UserInputResponseData
	if err = json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RequestID != "req-T" || resp.Status != "timed_out" || msg.Topic != a2a.NotifyResponseTopic("test-ns", "wf", "req-T") {
		t.Errorf("published %+v on %s, want a timed_out answer for req-T on its answer topic", resp, msg.Topic)
	}
	shortCtx, cancelShort := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancelShort()
	if extra, err := brk.Receive(shortCtx, q); err == nil && extra != nil {
		t.Errorf("published a timeout for a pause still within the margin: %s", extra.Topic)
	}
}

func TestWorkflowHILEnabled(t *testing.T) {
	w := &WorkflowInstance{}
	if w.workflowHILEnabled() {
		t.Error("disabled without a paused repo")
	}
	w2, _ := newHILInstance(t)
	if !w2.workflowHILEnabled() {
		t.Error("enabled when the paused repo is wired")
	}
}

func TestWorkflowInstance_HILLifecycle(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "workflow_hil_pause_resume", Default: true})
	svc := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services()

	node := &NodeConfig{ID: "approve", Type: "tool", ToolName: "ask"}
	w := NewWorkflowInstance(&WorkflowConfig{
		Name: "wf-life", Namespace: "test-ns", Nodes: []*NodeConfig{node},
		HILDatabaseURL: "sqlite:///" + filepath.Join(t.TempDir(), "life.db"),
	})
	if err := w.Init(ctx, nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !w.workflowHILEnabled() {
		t.Fatal("Init must open the HIL store when the flag is on and a DB is configured")
	}
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	regOff := features.NewRegistry()
	regOff.Register(features.Definition{Key: "workflow_hil_pause_resume", Default: false})
	svcOff := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(regOff)).Services()
	wOff := NewWorkflowInstance(&WorkflowConfig{
		Name: "wf-off", Namespace: "test-ns", Nodes: []*NodeConfig{node},
		HILDatabaseURL: "sqlite:///" + filepath.Join(t.TempDir(), "off.db"),
	})
	if err := wOff.Init(ctx, nil, svcOff); err != nil {
		t.Fatalf("Init (flag off): %v", err)
	}
	if wOff.workflowHILEnabled() {
		t.Error("pause must stay disabled when the flag is off")
	}
}

func TestRouteMessage_DispatchesHILResponse(t *testing.T) {
	w, _ := newHILInstance(t)
	ctx := t.Context()
	seedOneNodePaused(t, w, "req-R", nil)

	payload, _ := json.Marshal(a2a.UserInputResponseData{RequestID: "req-R", Status: "answered"})
	w.routeMessage(ctx, &broker.Message{
		Topic:   a2a.NotifyResponseTopic("test-ns", "wf", "req-R"),
		Payload: payload,
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if row, _ := w.pausedRepo.Get(ctx, "req-R"); row != nil && row.Status == "resumed" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("routeMessage must dispatch a notify response topic to the HIL resume")
}

func TestHandlePause_PersistFailureFailsNode(t *testing.T) {
	w, _ := newHILInstance(t)
	ctx := t.Context()
	node := &NodeConfig{ID: "approve", Type: "tool", ToolName: "ask"}
	dag, _ := BuildDependencyGraph([]*NodeConfig{node})
	w.cfg = &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: []*NodeConfig{node}}
	w.dag = dag

	_ = w.pausedDB.Close()

	state := NewExecutionState("wf", "exec-err")
	state.AddPending("approve")
	state.TrackSubTask("st-err", "approve")
	execCtx := NewExecutionContext(w.ctx, "task-err", "ctx-err", nil, a2a.UserProperties{UserID: "trent"}, false)
	e := &DAGExecutor{cfg: w.cfg, dag: w.dag, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}

	if err := e.Advance(ctx, &EventNodePaused{NodeID: "approve", SubTaskID: "st-err", RequestID: "req-err"}); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if state.PendingNodes["approve"] {
		t.Error("a failed pause must fail the node, not leave it pending")
	}
}

func TestEventNodePaused_IsEvent(t *testing.T) {
	var ev Event = &EventNodePaused{}
	ev.isWorkflowEvent()
}

func TestParseExpiresMs(t *testing.T) {
	if parseExpiresMs("") != nil {
		t.Error("empty → nil (no deadline)")
	}
	if parseExpiresMs("not-a-time") != nil {
		t.Error("unparseable → nil (no deadline)")
	}
	got := parseExpiresMs("2026-06-08T12:00:00Z")
	if got == nil || *got != time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("valid ISO must parse, got %v", got)
	}
}

func TestPauseWorkflowNode_EmptyRecipientFails(t *testing.T) {
	w, _ := newHILInstance(t)
	state := NewExecutionState("wf", "exec-er")
	state.AddPending("approve")
	execCtx := NewExecutionContext(w.ctx, "t", "c", nil, a2a.UserProperties{}, false)
	e := &DAGExecutor{cfg: w.cfg, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}
	if err := w.pauseWorkflowNode(t.Context(), e, &EventNodePaused{NodeID: "approve", SubTaskID: "st", RequestID: "req-er"}); err == nil {
		t.Error("an empty recipient must fail the pause (unanswerable), not persist a row")
	}
	if row, _ := w.pausedRepo.Get(t.Context(), "req-er"); row != nil {
		t.Error("no row should be persisted for an empty-recipient pause")
	}
}

func TestPauseWorkflowNode_PublishFailureCleansUpRow(t *testing.T) {
	w, brk := newHILInstance(t)
	state := NewExecutionState("wf", "exec-pf")
	state.AddPending("approve")
	execCtx := NewExecutionContext(w.ctx, "t", "c", nil, a2a.UserProperties{UserID: "trent"}, false)
	e := &DAGExecutor{cfg: w.cfg, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}

	_ = brk.Disconnect(context.Background())

	err := w.pauseWorkflowNode(t.Context(), e, &EventNodePaused{
		NodeID: "approve", SubTaskID: "st", RequestID: "req-pf",
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	if err == nil {
		t.Fatal("a publish failure must surface an error so the node is failed")
	}
	if row, _ := w.pausedRepo.Get(t.Context(), "req-pf"); row == nil || row.Status != "failed" {
		t.Errorf("publish-failure row must be marked failed, got %+v", row)
	}
}

func TestResumePausedNode_ReconstructFailLeavesRowPaused(t *testing.T) {
	w, _ := newHILInstance(t)
	ctx := t.Context()
	if err := w.pausedRepo.Insert(ctx, wfstore.PausedWorkflow{
		RequestID: "req-bad", ExecutionID: "x", WorkflowName: "wf", NodeID: "approve", RequesterAddr: "a",
		StateSnapshot: `{bad json`, ExecContext: `{}`, Routing: `{"node_id":"approve","sub_task_id":"st"}`,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	w.answer(ctx, "req-bad", "resumed", "answered", nil)
	if row, _ := w.pausedRepo.Get(ctx, "req-bad"); row == nil || row.Status != "paused" {
		t.Errorf("a reconstruct failure must leave the row paused, got %+v", row)
	}
}

func TestResumePausedNode_MalformedRoutingLeavesRowPaused(t *testing.T) {
	w, _ := newHILInstance(t)
	ctx := t.Context()
	if err := w.pausedRepo.Insert(ctx, wfstore.PausedWorkflow{
		RequestID: "req-rt", ExecutionID: "x", WorkflowName: "wf", NodeID: "approve", RequesterAddr: "a",
		StateSnapshot: `{}`, ExecContext: `{}`, Routing: `{bad`,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	w.answer(ctx, "req-rt", "resumed", "answered", nil)
	if row, _ := w.pausedRepo.Get(ctx, "req-rt"); row == nil || row.Status != "paused" {
		t.Errorf("malformed routing must leave the row paused, got %+v", row)
	}
}

func TestInit_BadHILDatabaseURLFails(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "workflow_hil_pause_resume", Default: true})
	svc := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services()

	node := &NodeConfig{ID: "approve", Type: "tool", ToolName: "ask"}
	w := NewWorkflowInstance(&WorkflowConfig{
		Name: "wf-bad", Namespace: "test-ns", Nodes: []*NodeConfig{node},
		HILDatabaseURL: "not-a-valid-db-url",
	})
	if err := w.Init(ctx, nil, svc); err == nil {
		t.Error("Init must fail loudly on a bad HIL database url, not silently disable pause")
	}
}

func TestPausedExecutionResumesWithItsSpendOrigin(t *testing.T) {
	w, _ := newHILInstance(t)
	w.dag = &DependencyGraph{}
	state := NewExecutionState("wf", "exec-1")
	state.AddPending("approve")
	execCtx := NewExecutionContext(w.ctx, "task-1", "ctx-1", nil, a2a.UserProperties{UserID: "trent@example.org"}, false)
	execCtx.SpendOrigin = agent.SpendOrigin{WorkflowName: "parent-wf", WorkflowNodeID: "child", ParentTaskID: "parent-task"}
	e := &DAGExecutor{cfg: w.cfg, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}
	if err := w.pauseWorkflowNode(t.Context(), e, &EventNodePaused{
		NodeID: "approve", SubTaskID: "st-1", RequestID: "req-1",
		Surface: json.RawMessage(`{"surfaceId":"s"}`), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("pauseWorkflowNode: %v", err)
	}
	exec := parkAndRebuild(t, w, e, nil)

	if exec.Context.SpendOrigin != execCtx.SpendOrigin {
		t.Errorf("resumed spend origin = %+v, want %+v", exec.Context.SpendOrigin, execCtx.SpendOrigin)
	}
}

func TestAWorkflowPausedBeforeItsSpendOriginWasKeptResumesWithItUnrecorded(t *testing.T) {
	w, _ := newHILInstance(t)
	w.dag = &DependencyGraph{}
	state := NewExecutionState("wf", "exec-1")
	state.AddPending("approve")
	props := a2a.UserProperties{UserID: "trent@example.org", DelegatingAgentName: "parent-wf"}
	execCtx := NewExecutionContext(w.ctx, "task-1", "ctx-1", nil, props, false)
	e := &DAGExecutor{cfg: w.cfg, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}
	if err := w.pauseWorkflowNode(t.Context(), e, &EventNodePaused{
		NodeID: "approve", SubTaskID: "st-1", RequestID: "req-1",
		Surface: json.RawMessage(`{"surfaceId":"s"}`), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("pauseWorkflowNode: %v", err)
	}
	exec := parkAndRebuild(t, w, e, func(run *wfstore.WorkflowRun) {
		var stored map[string]any
		if err := json.Unmarshal([]byte(run.ExecContext), &stored); err != nil {
			t.Fatalf("decode exec context: %v", err)
		}
		delete(stored, "spend_origin")
		written, _ := json.Marshal(stored)
		run.ExecContext = string(written)
	})

	if !exec.Context.SpendOrigin.Unrecorded {
		t.Errorf("resumed spend origin = %+v, want it unrecorded", exec.Context.SpendOrigin)
	}
	if kind, id := agent.SpendTrigger(props, exec.Context.SpendOrigin); kind != "" || id != "" {
		t.Errorf("trigger = %s/%s, want none rather than a guess at the delegator", kind, id)
	}
}
