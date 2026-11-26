// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

type approvalArgTool struct {
	*schemaTool
	mu       sync.Mutex
	ran      bool
	runs     int
	lastArgs map[string]any
	block    chan struct{}
	running  chan struct{}
}

func (t *approvalArgTool) waitUntilRunning(tb testing.TB) {
	tb.Helper()
	select {
	case <-t.running:
	case <-time.After(5 * time.Second):
		tb.Fatal("the tool never started")
	}
}

func newApprovalArgTool() *approvalArgTool {
	return &approvalArgTool{running: make(chan struct{}), schemaTool: &schemaTool{
		name:     "gated_call",
		authored: true,
		props: map[string]any{
			"url":     map[string]any{"type": "string"},
			"retries": map[string]any{"type": "integer"},
		},
	}}
}

func (t *approvalArgTool) Execute(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
	t.mu.Lock()
	first := !t.ran
	t.ran = true
	t.runs++
	t.lastArgs = args
	block := t.block
	t.mu.Unlock()

	if first {
		close(t.running)
	}
	if block != nil {
		<-block
	}
	return tool.OK("ok"), nil
}

func (t *approvalArgTool) executed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ran
}

func (t *approvalArgTool) calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runs
}

func (t *approvalArgTool) args() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastArgs
}

func gatedCallNode() *NodeConfig {
	return &NodeConfig{ID: "call_gated", Type: "tool", ToolName: "gated_call"}
}

func gatedCallConfig() map[string]tool.HILToolConfig {
	return map[string]tool.HILToolConfig{"gated_call": {RequireApproval: true, ShowArgs: true}}
}

func newLivePauseExecutor(t *testing.T) (*DAGExecutor, *approvalArgTool) {
	t.Helper()
	w, _ := newHILInstance(t)

	node := gatedCallNode()
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	w.cfg = cfg
	w.dag = dag

	rec := newApprovalArgTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	w.tools = ts
	w.hilToolConfigs = gatedCallConfig()
	ts.SetHILConfigs(w.hilToolConfigs)

	state := NewExecutionState("wf", "exec-live")
	state.PendingNodes[node.ID] = true
	execCtx := NewExecutionContext(t.Context(), "task-live", "ctx-live", nil,
		a2a.UserProperties{UserID: "approver@test"}, false)
	t.Cleanup(execCtx.Cancel)

	e := &DAGExecutor{cfg: cfg, dag: dag, state: state, execCtx: execCtx, instance: w, logger: slog.Default()}
	exec := &WorkflowExecution{State: state, Context: execCtx, Executor: e}
	w.executions["task-live"] = exec
	w.executionsByID["exec-live"] = exec
	return e, rec
}

func pauseAndGetRequestID(t *testing.T, e *DAGExecutor, args map[string]any) string {
	t.Helper()
	e.invokeToolNode(gatedCallNode(), args, NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	rows, err := e.instance.pausedRepo.Expired(t.Context(), e.instance.cfg.Name, time.Now().Add(24*time.Hour).UnixMilli(), 10)
	if err != nil {
		t.Fatalf("list paused: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 paused row, got %d — the gate did not take the pause branch", len(rows))
	}
	return rows[0].RequestID
}

func TestApprovalSeam_PauseThenResumeDispatchesWithArgTypesIntact(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test", "retries": 30})

	if rec.executed() {
		t.Fatal("the tool ran before anyone approved it")
	}

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	e.instance.wg.Wait()

	if !rec.executed() {
		t.Fatal("an approved call did not dispatch after resuming from the durable row")
	}
	if got := rec.args()["url"]; got != "https://example.test" {
		t.Errorf("url reached the tool as %#v, want the approved value", got)
	}
	retries, ok := rec.args()["retries"].(float64)
	if !ok {
		t.Fatalf("retries reached the tool as %T, want float64 — the pause round trip retyped it", rec.args()["retries"])
	}
	if retries != 30 {
		t.Errorf("retries = %v, want 30", retries)
	}
}

func TestApprovalSeam_PredicateGatesAndDispatchesOnApproval(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	e.instance.hilToolConfigs = map[string]tool.HILToolConfig{
		"gated_call": {ApprovalWhen: []tool.ApprovalRule{{Arg: "retries", Op: tool.OpGt, Value: 3}}},
	}
	e.instance.tools.SetHILConfigs(e.instance.hilToolConfigs)

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test", "retries": 1},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-below"})
	e.instance.wg.Wait()
	if !rec.executed() {
		t.Fatal("a call the predicate does not match was blocked")
	}
	rows, err := e.instance.pausedRepo.Expired(t.Context(), e.instance.cfg.Name, time.Now().Add(24*time.Hour).UnixMilli(), 10)
	if err != nil {
		t.Fatalf("list paused: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a non-matching call parked an approval prompt: %d rows", len(rows))
	}

	e2, rec2 := newLivePauseExecutor(t)
	e2.instance.hilToolConfigs = e.instance.hilToolConfigs
	e2.instance.tools.SetHILConfigs(e2.instance.hilToolConfigs)

	requestID := pauseAndGetRequestID(t, e2, map[string]any{"url": "https://example.test", "retries": 9})
	if rec2.executed() {
		t.Fatal("a matching call ran before anyone approved it")
	}
	e2.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	e2.instance.wg.Wait()
	if !rec2.executed() {
		t.Error("an approved predicate-gated call did not dispatch; the backstop and the caller disagree")
	}
}

func TestApprovalSeam_DenialNeverDispatches(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": false})
	e.instance.wg.Wait()

	if rec.executed() {
		t.Fatal("a denied call was dispatched")
	}
	if run := runRowOf(t, e.instance, "exec-live"); run.Status != "ended" {
		t.Errorf("run status = %q after a denial, want ended: a denial must fail the node, not leave it pending", run.Status)
	}
}

func TestApprovalSeam_LateApprovalOnEndedRunDoesNotDispatch(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	rewriteParkedState(t, e.instance, "exec-live", func(s *WorkflowExecutionState) {
		s.WorkflowDeadline = time.Now().Add(-time.Minute)
	})

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	e.instance.wg.Wait()

	if rec.executed() {
		t.Fatal("a late approval ran the tool for a run that had already ended")
	}
	if got := audit.only(t).ApprovalRefusal; got != "run_ended" {
		t.Errorf("approvalRefusal = %q, want run_ended", got)
	}
}

func TestApprovalGate_UnaskablePromptFailsClosed(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": make(chan int)},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if rec.executed() {
		t.Fatal("a gated tool ran after its approval prompt failed to build")
	}
	if got := audit.only(t).ApprovalRefusal; got != "prompt_failed" {
		t.Errorf("approvalRefusal = %q, want prompt_failed", got)
	}
}

func TestFinalization_RetiresPausedRows(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	cancelTask(t, e.instance, "task-live")

	if run := runRowOf(t, e.instance, "exec-live"); run.Status != "ended" {
		t.Fatalf("run status = %q, want ended: the run did not finalize, so this proves nothing about retirement", run.Status)
	}

	e.instance.drainRetirements()

	row, err := e.instance.pausedRepo.Get(t.Context(), requestID)
	if err != nil || row == nil {
		t.Fatalf("row gone entirely: %v", err)
	}
	if row.Status == "paused" {
		t.Error("a finished run left its approval row answerable")
	}
}

func TestDrainRetirements_JoinsAnAdmittedRetirement(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	e.instance.retirePausedNodesAsync("exec-live")
	e.instance.drainRetirements()

	row, err := e.instance.pausedRepo.Get(t.Context(), requestID)
	if err != nil || row == nil {
		t.Fatalf("row gone entirely: %v", err)
	}
	if row.Status == "paused" {
		t.Error("drainRetirements returned before the retirement had run")
	}
}

func TestRetirement_RunsInlineAfterAdmissionCloses(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	e.instance.drainRetirements()
	e.instance.retirePausedNodesAsync("exec-live")

	row, err := e.instance.pausedRepo.Get(t.Context(), requestID)
	if err != nil || row == nil {
		t.Fatalf("row gone entirely: %v", err)
	}
	if row.Status == "paused" {
		t.Error("a retirement arriving during shutdown was dropped; the row stays answerable")
	}
}

func TestStop_ClosesRetirementAdmission(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	w := e.instance

	if err := w.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	w.retireMu.Lock()
	closed := w.retireClosed
	w.retireMu.Unlock()
	if !closed {
		t.Error("Stop returned without closing retirement admission; a retirement landing now is lost with the handle")
	}
}

func TestRestart_ReopensTheStoreAndReadmitsRetirements(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance
	w.cfg.HILDatabaseURL = "sqlite:///" + filepath.Join(t.TempDir(), "restart.db")
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "workflow_hil_pause_resume", Default: true})
	w.svc = runtime.NewRuntime(w.svc.Broker(), nil,
		runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services()

	if err := w.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if w.workflowHILEnabled() {
		t.Error("a stopped instance reports it can park a node for approval, over a closed handle")
	}

	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if !w.workflowHILEnabled() {
		t.Error("the restarted instance cannot park a node for approval; every gate it has fails closed")
	}
	w.retireMu.Lock()
	closed := w.retireClosed
	w.retireMu.Unlock()
	if closed {
		t.Error("retirement admission stayed latched across the restart; every finalization now writes inline under the executor mutex")
	}
}

func TestReopenRetirements_DoesNotReuseAGroupWithABlockedWaiter(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance

	w.retireMu.Lock()
	before := w.retireGroupLocked()
	w.retireMu.Unlock()

	before.Add(1)
	stuck := make(chan struct{})
	go func() {
		before.Wait()
		close(stuck)
	}()

	w.reopenRetirements()

	w.retireMu.Lock()
	after := w.retireGroupLocked()
	w.retireMu.Unlock()
	if after == before {
		t.Fatal("the reopened generation reuses the group a waiter is still blocked on; the next Add panics the process")
	}

	w.retirePausedNodesAsync("exec-live")
	w.drainRetirements()

	before.Done()
	select {
	case <-stuck:
	case <-time.After(5 * time.Second):
		t.Error("the abandoned waiter never returned")
	}
}

func TestRetirement_PanicDoesNotEscapeEitherPath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drive func(*WorkflowInstance)
	}{
		{"the goroutine path", func(w *WorkflowInstance) {
			w.retirePausedNodesAsync("exec-live")
			w.drainRetirements()
		}},
		{"the inline path", func(w *WorkflowInstance) {
			w.drainRetirements()
			w.retirePausedNodesAsync("exec-live")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newLivePauseExecutor(t)
			pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
			w := e.instance

			var logged strings.Builder
			w.logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelError}))
			w.ctx = nil //nolint:staticcheck // the panic under test comes from exactly this

			tc.drive(w)

			if got := logged.String(); !strings.Contains(got, "panic while retiring paused rows") {
				t.Errorf("the panic was swallowed without a trace; logs were:\n%s", got)
			}
		})
	}
}

func TestRetirement_ReportsAFailedWrite(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	w := e.instance

	var logged strings.Builder
	w.logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := w.pausedDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	w.abandonPausedNodes("exec-live")

	got := logged.String()
	if !strings.Contains(got, "could not retire paused rows") {
		t.Errorf("a retirement that could not write said nothing; logs were:\n%s", got)
	}
	if !strings.Contains(got, "exposedToolsKnown=false") {
		t.Errorf("the report presented an unknown tool list as an empty one; logs were:\n%s", got)
	}
}

func TestPauseBookkeeping_MarksOnlyRunsThatStoredARow(t *testing.T) {
	e, rec := newLivePauseExecutor(t)

	e.instance.hilToolConfigs = nil
	e.instance.tools.SetHILConfigs(nil)
	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if !rec.executed() {
		t.Fatal("precondition: the ungated tool should have run")
	}
	if e.state.HasPausedRows {
		t.Error("a run that never paused was marked as having rows to retire")
	}

	e2, _ := newLivePauseExecutor(t)
	if err := e2.instance.pausedDB.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	err := e2.instance.pauseWorkflowNode(t.Context(), e2, &EventNodePaused{
		NodeID: "call_gated", SubTaskID: "sub-1", RequestID: "hil-approval-x",
		Kind: PauseKindApproval, ToolName: "gated_call",
	})
	if err == nil {
		t.Fatal("precondition: the insert should have failed against a closed store")
	}
	if e2.state.HasPausedRows {
		t.Error("a pause whose insert failed still marked the execution as having rows")
	}
}

func TestDispatchBackstop_RefusesUnapprovedGatedCall(t *testing.T) {
	rec := newApprovalArgTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	ts.SetHILConfigs(gatedCallConfig())
	call := tool.Call{ID: "c1", Name: "gated_call", Arguments: `{"url":"https://example.test"}`}

	res, err := ts.Dispatch(t.Context(), call, &tool.Context{})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if rec.executed() {
		t.Fatal("Dispatch ran a gated tool for a caller that never obtained approval")
	}
	if res == nil || !strings.Contains(res.Message, "requires human approval") {
		t.Errorf("want a soft refusal naming the reason, got %+v", res)
	}

	if _, err = ts.Dispatch(t.Context(), call, &tool.Context{ApprovalReason: tool.ApprovalHuman}); err != nil {
		t.Fatalf("dispatch approved: %v", err)
	}
	if !rec.executed() {
		t.Error("an approved call was refused by the backstop")
	}
}

func TestApprovalGate_ExitHandlerFailsClosed(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	e.cfg.OnExit = &ExitHandler{Always: "call_gated"}
	e.state.InExitHandler = true

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if rec.executed() {
		t.Fatal("a gated tool ran unapproved in an exit handler")
	}
	rows, err := e.instance.pausedRepo.Expired(t.Context(), e.instance.cfg.Name, time.Now().Add(24*time.Hour).UnixMilli(), 10)
	if err != nil {
		t.Fatalf("list paused: %v", err)
	}
	if len(rows) != 0 {
		t.Error("an exit handler parked the run on an approval prompt instead of failing")
	}
	if got := audit.only(t).ApprovalRefusal; got != "exit_handler" {
		t.Errorf("approvalRefusal = %q, want exit_handler", got)
	}
	if e.state.PendingNodes["call_gated"] {
		t.Error("the refused exit-handler node was left pending, which hangs finalization")
	}
}

func TestDrainRetirements_GiveUpNamesWhatItAbandoned(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance

	w.retireDrainTimeout = 50 * time.Millisecond

	var logged strings.Builder
	var mu sync.Mutex
	w.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	w.retireMu.Lock()
	wg := w.retireGroupLocked()
	wg.Add(1)
	w.retireInFlight["exec-stuck"]++
	w.retireMu.Unlock()
	release := make(chan struct{})
	go func() {
		<-release
		w.retireMu.Lock()
		delete(w.retireInFlight, "exec-stuck")
		w.retireMu.Unlock()
		wg.Done()
	}()

	w.drainRetirements()

	mu.Lock()
	got := logged.String()
	mu.Unlock()
	close(release)

	if !strings.Contains(got, "gave up waiting") {
		t.Fatalf("the drain gave up silently; logs were:\n%s", got)
	}
	if !strings.Contains(got, "exec-stuck") {
		t.Errorf("the give-up did not name the run whose rows stay answerable; logs were:\n%s", got)
	}
	waited, ok := waitedFromLog(got)
	if !ok {
		t.Fatalf("the give-up reported no elapsed time at all; logs were:\n%s", got)
	}
	if waited <= 50*time.Millisecond || waited > 5*time.Second {
		t.Errorf("waited = %v, want a measured elapsed time just past the 50ms bound this test set", waited)
	}
}

func TestDrainRetirements_TimeoutWithNothingStrandedIsSilent(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance

	w.retireDrainTimeout = time.Nanosecond

	var logged strings.Builder
	var mu sync.Mutex
	w.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	w.drainRetirements()

	mu.Lock()
	got := logged.String()
	mu.Unlock()
	if strings.Contains(got, "gave up waiting") {
		t.Errorf("a drain with nothing outstanding reported an abandonment; logs were:\n%s", got)
	}
}

type failingStartable struct{}

func (failingStartable) Start(context.Context) error {
	return errors.New("sub-component refused to start")
}
func (failingStartable) Stop() {}

func TestStart_CleansUpWhenItGivesUp(t *testing.T) {
	t.Run("the context is cancelled when the store cannot be armed", func(t *testing.T) {
		e, _ := newLivePauseExecutor(t)
		w := e.instance
		w.closePausedStore()
		w.cfg.HILDatabaseURL = "wat://not-a-supported-driver"
		w.hilToolConfigs = gatedCallConfig()
		reg := features.NewRegistry()
		reg.Register(features.Definition{Key: "workflow_hil_pause_resume", Default: true})
		reg.Register(features.Definition{Key: features.KeyWorkflowToolApprovalEnforcement, Default: true})
		w.svc = runtime.NewRuntime(w.svc.Broker(), nil,
			runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services()

		if err := w.Start(t.Context()); err == nil {
			t.Fatal("precondition: Start should have failed to arm the store")
		}
		if w.ctx == nil || w.ctx.Err() == nil {
			t.Error("Start returned without cancelling the context it created; every retried start leaks one")
		}
	})

	t.Run("the store handle is released when a later step fails", func(t *testing.T) {
		e, _ := newLivePauseExecutor(t)
		w := e.instance
		w.subComponents = append(w.subComponents, failingStartable{})

		if err := w.Start(t.Context()); err == nil {
			t.Fatal("precondition: Start should have failed on the sub-component")
		}
		if w.pausedStore() != nil {
			t.Error("Start returned without releasing the store handle; every retried start leaks one")
		}
		if w.ctx == nil || w.ctx.Err() == nil {
			t.Error("Start returned without cancelling the context it created")
		}
	})
}

func TestGatedToolNames(t *testing.T) {
	t.Parallel()
	row := func(kind, toolName string) wfstore.PausedWorkflow {
		routing, err := json.Marshal(persistedRouting{Kind: kind, ToolName: toolName})
		if err != nil {
			t.Fatalf("marshal routing: %v", err)
		}
		return wfstore.PausedWorkflow{Routing: string(routing)}
	}

	got := gatedToolNames([]wfstore.PausedWorkflow{
		row(PauseKindApproval, "send_payment"),
		row(PauseKindApproval, "read_file"),
		row(PauseKindApproval, "send_payment"),
		row("", "deferred_tool"),
		{Routing: "{not json"},
	})

	want := []string{"read_file", "send_payment"}
	if !slices.Equal(got, want) {
		t.Errorf("gatedToolNames = %v, want %v (sorted, deduplicated, approvals only)", got, want)
	}
}

func TestDecodeRouting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		routing     string
		wantDecoded bool
		wantKind    string
	}{
		{"an approval pause", `{"kind":"approval","tool_name":"send_payment"}`, true, PauseKindApproval},
		{"a deferred-result pause", `{"node_id":"n"}`, true, ""},
		{"routing that will not decode", "{not json", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			routing, decoded := decodeRouting(wfstore.PausedWorkflow{Routing: tc.routing})
			if decoded != tc.wantDecoded {
				t.Errorf("decoded = %v, want %v", decoded, tc.wantDecoded)
			}
			if routing.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", routing.Kind, tc.wantKind)
			}
		})
	}
}

func TestRetirement_FailedWriteNamesToolsWhenItCanListThem(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance
	pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	var logged strings.Builder
	var mu sync.Mutex
	w.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	rows, err := w.pausedStore().PausedForExecution(t.Context(), "exec-live")
	if err != nil || len(rows) != 1 {
		t.Fatalf("precondition: want the one paused row, got %d (%v)", len(rows), err)
	}
	w.reportRetirementFailure("exec-live", rows, true, errors.New("write refused"))

	mu.Lock()
	got := logged.String()
	mu.Unlock()
	if !strings.Contains(got, "gated_call") {
		t.Errorf("the report does not name the tools left answerable; logs were:\n%s", got)
	}
	if !strings.Contains(got, "exposedToolsKnown=true") {
		t.Errorf("a complete list was reported as untrustworthy; logs were:\n%s", got)
	}
}

func waitedFromLog(logged string) (time.Duration, bool) {
	const key = "waited="
	i := strings.Index(logged, key)
	if i < 0 {
		return 0, false
	}
	rest := logged[i+len(key):]
	if end := strings.IndexAny(rest, " \n"); end >= 0 {
		rest = rest[:end]
	}
	d, err := time.ParseDuration(rest)
	if err != nil {
		return 0, false
	}
	return d, true
}

func TestRetirement_SuccessLineSaysWhetherAnythingWasRecorded(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance
	pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	var logged strings.Builder
	var mu sync.Mutex
	w.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelInfo}))

	w.abandonPausedNodes("exec-live")

	mu.Lock()
	got := logged.String()
	mu.Unlock()
	if !strings.Contains(got, "retired paused nodes") {
		t.Fatalf("precondition: no retirement was reported; logs were:\n%s", got)
	}
	if !strings.Contains(got, "listingComplete=true") {
		t.Errorf("the success line does not say whether anything was recorded; logs were:\n%s", got)
	}
}

func TestRetirement_ReportsACountMismatchBetweenListedAndRetired(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance
	store := w.pausedStore()
	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	var logged strings.Builder
	var mu sync.Mutex
	w.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	original := w.retireListHook
	w.retireListHook = func() {
		if _, _, err := store.Claim(context.Background(), requestID, "resumed"); err != nil {
			t.Errorf("claim by the other replica: %v", err)
		}
	}
	t.Cleanup(func() { w.retireListHook = original })

	w.abandonPausedNodes("exec-live")

	mu.Lock()
	got := logged.String()
	mu.Unlock()
	if !strings.Contains(got, "different NUMBER") {
		t.Errorf("a retirement that converted a different number of rows than it listed said nothing; logs were:\n%s", got)
	}
	if !strings.Contains(got, "listed=1") || !strings.Contains(got, "retired=0") {
		t.Errorf("the report does not carry both counts; logs were:\n%s", got)
	}
}
