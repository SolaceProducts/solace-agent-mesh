// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func installAudit(tb testing.TB, e *DAGExecutor, audit auth.AuditLogger, enforce bool) {
	tb.Helper()
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyWorkflowToolApprovalEnforcement, Default: enforce})
	var brk broker.Broker
	if e.instance.svc != nil {
		brk = e.instance.svc.Broker()
	}
	e.instance.svc = runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithFeatureRegistry(reg),
		runtime.WithAuditLogger(audit),
	).Services()
}

type approvalAudit struct {
	auth.NoopAuditLogger
	mu     sync.Mutex
	events []auth.ToolExecutionEvent
}

func (a *approvalAudit) LogToolExecution(_ context.Context, e auth.ToolExecutionEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
}

func (a *approvalAudit) all() []auth.ToolExecutionEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]auth.ToolExecutionEvent(nil), a.events...)
}

func (a *approvalAudit) only(tb testing.TB) auth.ToolExecutionEvent {
	tb.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.events) != 1 {
		tb.Fatalf("want exactly one tool-execution audit event, got %d", len(a.events))
	}
	return a.events[0]
}

func TestWorkflowAudit_RecordsSelfCertifiedDispatch(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, false)

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if !rec.executed() {
		t.Fatal("precondition: with enforcement off the tool should have dispatched")
	}
	ev := audit.only(t)
	if ev.ApprovalReason != "enforcement_disabled" {
		t.Errorf("approvalReason = %q, want enforcement_disabled — the audit log cannot say nobody was asked", ev.ApprovalReason)
	}
	if ev.ToolName != "gated_call" {
		t.Errorf("toolName = %q", ev.ToolName)
	}
}

func TestWorkflowAudit_OmitsReasonForAnUngatedTool(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, false)
	e.instance.hilToolConfigs = nil
	e.instance.tools.SetHILConfigs(nil)

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if got := audit.only(t).ApprovalReason; got != "" {
		t.Errorf("approvalReason = %q for an ungated tool, want empty", got)
	}
}

func TestEnforcementDisabled_WarnsOncePerTool(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	installAudit(t, e, &approvalAudit{}, false)
	var logged strings.Builder
	e.logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))

	for i := range 5 {
		e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
			NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1", MapNodeID: "each", ItemIndex: i})
		e.instance.wg.Wait()
	}

	got := logged.String()
	if n := strings.Count(got, "without asking anyone"); n != 1 {
		t.Errorf("warned %d times across a 5-item fan-out, want exactly 1", n)
	}
	if !strings.Contains(got, "gated_call") {
		t.Errorf("the warning must name the tool: %q", got)
	}
}

func TestWorkflowAudit_ApprovedDispatchRecordsHuman(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)

	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	if n := len(audit.all()); n != 0 {
		t.Fatalf("a parked call has not executed and must not be recorded as one: %d events", n)
	}

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	e.instance.wg.Wait()

	if !rec.executed() {
		t.Fatal("precondition: the approved call should have dispatched")
	}
	ev := audit.only(t)
	if ev.ApprovalReason != "human" {
		t.Errorf("approvalReason = %q, want human — the record cannot say a person approved this", ev.ApprovalReason)
	}
	if !ev.Success {
		t.Errorf("success = false for a call that ran and returned ok (error %q)", ev.Error)
	}
}

func TestWorkflowAudit_CarriesAttribution(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, false)

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	ev := audit.only(t)
	for _, f := range []struct{ name, got, want string }{
		{"userID", ev.UserID, "approver@test"},
		{"agentName", ev.AgentName, "wf"},
		{"toolName", ev.ToolName, "gated_call"},
		{"sessionID", ev.SessionID, "ctx-live"},
	} {
		if f.got != f.want {
			t.Errorf("%s = %q, want %q", f.name, f.got, f.want)
		}
	}
	if ev.DurationMs <= 0 {
		t.Errorf("durationMs = %v, want a measured duration", ev.DurationMs)
	}
}

func TestWorkflowAudit_RecordsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name        string
		nodeStatus  string
		data        map[string]any
		wantRefusal string
	}{
		{"a person said no", "answered", map[string]any{"approved": false}, "denied"},
		{"nobody answered in time", "timed_out", nil, "timed_out"},
		{"the prompt was cancelled", "cancelled", nil, "cancelled"},
		{"the answer could not be read", "answered", map[string]any{"approved": "yes please"}, "unreadable"},
		{"the status is not one we know", "acknowledged", map[string]any{"approved": true}, "unanswerable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, rec := newLivePauseExecutor(t)
			audit := &approvalAudit{}
			installAudit(t, e, audit, true)

			requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
			e.instance.answer(t.Context(), requestID, "resumed", tc.nodeStatus, tc.data)
			e.instance.wg.Wait()

			if rec.executed() {
				t.Fatal("a refused call was dispatched")
			}
			ev := audit.only(t)
			if ev.ApprovalRefusal != tc.wantRefusal {
				t.Errorf("approvalRefusal = %q, want %q", ev.ApprovalRefusal, tc.wantRefusal)
			}
			if ev.Error != "tool_error" {
				t.Errorf("error = %q, want tool_error", ev.Error)
			}
			if ev.Success {
				t.Error("a refused call must not be recorded as a success")
			}
			if ev.ApprovalReason != "" {
				t.Errorf("approvalReason = %q on a call that never ran, want empty", ev.ApprovalReason)
			}
			if ev.ToolName != "gated_call" || ev.UserID != "approver@test" {
				t.Errorf("a refusal record must be as attributable as a dispatch: %+v", ev)
			}
		})
	}
}

func TestWorkflowAudit_RecordsApprovalUnreachable(t *testing.T) {
	e, rec := newApprovalExecutor(t, requireApproval())
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)

	e.invokeToolNode(gatedToolNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if rec.executed.Load() {
		t.Fatal("precondition: the call should have failed closed")
	}
	ev := audit.only(t)
	if ev.ApprovalRefusal != "no_approval_route" {
		t.Errorf("approvalRefusal = %q, want no_approval_route", ev.ApprovalRefusal)
	}
	if ev.Error != "tool_error" {
		t.Errorf("error = %q, want tool_error", ev.Error)
	}
}

func TestWorkflowAudit_ClassifiesFailuresLikeTheAgentLoop(t *testing.T) {
	t.Run("a dispatch error is classified, not labelled", func(t *testing.T) {
		e, _ := newLivePauseExecutor(t)
		audit := &approvalAudit{}
		installAudit(t, e, audit, false)
		ts := tool.NewSet()
		if err := ts.Register(&failingTool{err: fmt.Errorf("calling backend: %w", context.DeadlineExceeded)}); err != nil {
			t.Fatalf("register: %v", err)
		}
		e.instance.tools = ts

		e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
			NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
		e.instance.wg.Wait()

		ev := audit.only(t)
		if ev.Error != "timeout" {
			t.Errorf("error = %q, want the shared taxonomy's timeout", ev.Error)
		}
		if ev.Success {
			t.Error("a failed dispatch recorded as a success")
		}
	})

	t.Run("a tool-reported error is tool_error", func(t *testing.T) {
		e, _ := newLivePauseExecutor(t)
		audit := &approvalAudit{}
		installAudit(t, e, audit, false)
		ts := tool.NewSet()
		if err := ts.Register(&failingTool{result: tool.Error("the backend said no")}); err != nil {
			t.Fatalf("register: %v", err)
		}
		e.instance.tools = ts

		e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
			NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
		e.instance.wg.Wait()

		if got := audit.only(t).Error; got != "tool_error" {
			t.Errorf("error = %q, want tool_error", got)
		}
	})
}

func TestWorkflowAudit_ToolCannotRewriteItsOwnAuthorisation(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, false)
	ts := tool.NewSet()
	if err := ts.Register(&reasonClearingTool{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	ts.SetHILConfigs(e.instance.hilToolConfigs)

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if got := audit.only(t).ApprovalReason; got != "enforcement_disabled" {
		t.Errorf("approvalReason = %q — the tool erased the record of how it came to run", got)
	}
}

type reasonClearingTool struct{ *schemaTool }

func (t *reasonClearingTool) Name() string { return "gated_call" }

func (t *reasonClearingTool) Description() string { return "clears its approval reason" }

func (t *reasonClearingTool) Parameters() *tool.JSONSchema { return nil }

func (t *reasonClearingTool) Execute(_ context.Context, _ map[string]any, tc *tool.Context) (*tool.Result, error) {
	tc.ApprovalReason = tool.ApprovalNone
	return tool.OK("ok"), nil
}

type failingTool struct {
	*schemaTool
	result *tool.Result
	err    error
}

func (t *failingTool) Name() string { return "gated_call" }

func (t *failingTool) Description() string { return "fails on demand" }

func (t *failingTool) Parameters() *tool.JSONSchema { return nil }

func (t *failingTool) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	return t.result, t.err
}

func TestWorkflowAudit_RecordsApprovalsAbandonedByAFinishedRun(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)

	pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	if n := len(audit.all()); n != 0 {
		t.Fatalf("a parked call has not executed and must not be recorded as one: %d events", n)
	}
	e.instance.abandonPausedNodes("exec-live")

	if rec.executed() {
		t.Fatal("retiring a paused row ran the tool")
	}
	ev := audit.only(t)
	if ev.ApprovalRefusal != "run_ended_unanswered" {
		t.Errorf("approvalRefusal = %q, want run_ended_unanswered — nobody answered, the run ended first", ev.ApprovalRefusal)
	}
	if ev.ToolName != "gated_call" {
		t.Errorf("toolName = %q, want gated_call — the record cannot say what was being approved", ev.ToolName)
	}
	if ev.UserID != "approver@test" {
		t.Errorf("userID = %q, want the caller whose run this was", ev.UserID)
	}
	if ev.SessionID != "ctx-live" {
		t.Errorf("sessionID = %q, want the task's context so the record can be correlated", ev.SessionID)
	}
	if ev.ApprovalRecipient != "approver@test" {
		t.Errorf("approvalRecipient = %q, want the person who was asked", ev.ApprovalRecipient)
	}
	if ev.Success || ev.ApprovalReason != "" {
		t.Errorf("a call that never ran must record neither success nor a reason: %+v", ev)
	}
}

func TestWorkflowAudit_DeferredResultPauseIsNotRecordedAsARefusal(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)

	_ = e.Advance(t.Context(), &EventNodePaused{
		NodeID:    "call_gated",
		SubTaskID: "sub-1",
		RequestID: "hil-deferred-1",
		Surface:   []byte(`{"surfaceId":"s"}`),
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	e.instance.abandonPausedNodes("exec-live")

	if n := len(audit.all()); n != 0 {
		t.Errorf("a deferred-result pause was recorded as a refused approval: %+v", audit.all())
	}
}

func TestWorkflowAudit_ApprovalAnsweredDuringRetirementIsNotRecordedAsAbandoned(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	store := e.instance.pausedStore()

	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	stale, err := store.PausedForExecution(t.Context(), "exec-live")
	if err != nil || len(stale) != 1 {
		t.Fatalf("precondition: want the one paused row, got %d (%v)", len(stale), err)
	}

	if _, claimed, cerr := store.Claim(t.Context(), requestID, "resumed"); cerr != nil || !claimed {
		t.Fatalf("precondition: the other replica's claim failed (claimed=%v): %v", claimed, cerr)
	}

	e.instance.settleAbandonedRows(t.Context(), store, stale)

	if events := audit.all(); len(events) != 0 {
		t.Errorf("an approval another replica answered was recorded as abandoned, asserting a call never happened when it did: %+v", events)
	}
}

func TestWorkflowAudit_ConfirmedAbandonmentIsStillRecorded(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	store := e.instance.pausedStore()

	pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	stale, err := store.PausedForExecution(t.Context(), "exec-live")
	if err != nil || len(stale) != 1 {
		t.Fatalf("precondition: want the one paused row, got %d (%v)", len(stale), err)
	}
	if _, cerr := store.ClaimAllForExecution(t.Context(), "exec-live", "cancelled"); cerr != nil {
		t.Fatalf("claim: %v", cerr)
	}

	e.instance.settleAbandonedRows(t.Context(), store, stale)

	if got := audit.only(t).ApprovalRefusal; got != "run_ended_unanswered" {
		t.Errorf("approvalRefusal = %q, want run_ended_unanswered", got)
	}
}

func TestWorkflowAudit_BackstopRefusalReachesTheRecord(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)

	e.instance.hilToolConfigs = nil

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if rec.executed() {
		t.Fatal("the backstop let a gated call through")
	}
	ev := audit.only(t)
	if ev.ApprovalRefusal != "backstop" {
		t.Errorf("approvalRefusal = %q, want backstop — the record cannot say a gate refused this, only that something failed", ev.ApprovalRefusal)
	}
}

func TestWorkflowAudit_UnconfirmableAbandonmentIsNotRecorded(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	store := e.instance.pausedStore()

	pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	stale, err := store.PausedForExecution(t.Context(), "exec-live")
	if err != nil || len(stale) != 1 {
		t.Fatalf("precondition: want the one paused row, got %d (%v)", len(stale), err)
	}

	var logged strings.Builder
	var mu sync.Mutex
	e.instance.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	e.instance.closePausedStore()

	e.instance.settleAbandonedRows(t.Context(), store, stale)

	if events := audit.all(); len(events) != 0 {
		t.Errorf("an abandonment that could not be confirmed was recorded anyway: %+v", events)
	}
	mu.Lock()
	got := logged.String()
	mu.Unlock()
	if !strings.Contains(got, "could not confirm") {
		t.Errorf("the unrecorded abandonment was not reported; logs were:\n%s", got)
	}
	if !strings.Contains(got, "exec-live") || !strings.Contains(got, "rowCount=1") {
		t.Errorf("the warning does not say which run lost how many records; logs were:\n%s", got)
	}
}

func TestWorkflowAudit_EqualCountsDoNotConfirmAnAnsweredApproval(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	store := e.instance.pausedStore()

	requestA := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	listed, err := store.PausedForExecution(t.Context(), "exec-live")
	if err != nil || len(listed) != 1 {
		t.Fatalf("precondition: want the one paused row, got %d (%v)", len(listed), err)
	}

	if _, claimed, cerr := store.Claim(t.Context(), requestA, "resumed"); cerr != nil || !claimed {
		t.Fatalf("precondition: the other replica's claim failed (claimed=%v): %v", claimed, cerr)
	}
	rowC := listed[0]
	rowC.RequestID = "hil-approval-second-node"
	rowC.NodeID = "call_gated_2"
	rowC.Status = "paused"
	if ierr := store.Insert(t.Context(), rowC); ierr != nil {
		t.Fatalf("precondition: insert the second paused row: %v", ierr)
	}

	n, cerr := store.ClaimAllForExecution(t.Context(), "exec-live", "cancelled")
	if cerr != nil {
		t.Fatalf("claim: %v", cerr)
	}
	if n != int64(len(listed)) {
		t.Fatalf("precondition: want equal counts to reproduce the shortcut, got claimed=%d listed=%d", n, len(listed))
	}

	e.instance.settleAbandonedRows(t.Context(), store, listed)

	if events := audit.all(); len(events) != 0 {
		t.Errorf("an approval that was answered and dispatched elsewhere was recorded as never having happened: %+v", events)
	}
}

func TestWorkflowAudit_UnattributableAbandonmentSaysSo(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	store := e.instance.pausedStore()

	pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	stale, err := store.PausedForExecution(t.Context(), "exec-live")
	if err != nil || len(stale) != 1 {
		t.Fatalf("precondition: want the one paused row, got %d (%v)", len(stale), err)
	}
	if _, cerr := store.ClaimAllForExecution(t.Context(), "exec-live", "cancelled"); cerr != nil {
		t.Fatalf("claim: %v", cerr)
	}
	stale[0].ExecContext = "{not json"

	var logged strings.Builder
	var mu sync.Mutex
	e.instance.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	e.instance.settleAbandonedRows(t.Context(), store, stale)

	ev := audit.only(t)
	if ev.UserID != "" {
		t.Errorf("userID = %q from a context that does not decode", ev.UserID)
	}
	if ev.ApprovalRecipient != "" {
		t.Errorf("approvalRecipient = %q beside an empty caller; the record asserts a delegation nothing here established", ev.ApprovalRecipient)
	}
	mu.Lock()
	got := logged.String()
	mu.Unlock()
	if !strings.Contains(got, "could not read the caller") {
		t.Errorf("an unattributed record went out silently; logs were:\n%s", got)
	}
	if strings.Contains(got, "{not json") {
		t.Errorf("the warning logged the payload: %s", got)
	}
}

func TestWorkflowAudit_AbandonmentAttributesCallerAndRecipientSeparately(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	store := e.instance.pausedStore()

	pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	rows, err := store.PausedForExecution(t.Context(), "exec-live")
	if err != nil || len(rows) != 1 {
		t.Fatalf("precondition: want the one paused row, got %d (%v)", len(rows), err)
	}
	if rows[0].RecipientUserID != "approver@test" {
		t.Fatalf("precondition: recipient = %q", rows[0].RecipientUserID)
	}

	if _, cerr := store.ClaimAllForExecution(t.Context(), "exec-live", "cancelled"); cerr != nil {
		t.Fatalf("claim: %v", cerr)
	}

	var ec persistedExecContext
	if uerr := json.Unmarshal([]byte(rows[0].ExecContext), &ec); uerr != nil {
		t.Fatalf("decode exec context: %v", uerr)
	}
	ec.UserProps.UserID = "caller@test"
	encoded, merr := json.Marshal(ec)
	if merr != nil {
		t.Fatalf("encode exec context: %v", merr)
	}
	rows[0].ExecContext = string(encoded)

	e.instance.settleAbandonedRows(t.Context(), store, rows)

	ev := audit.only(t)
	if ev.UserID != "caller@test" {
		t.Errorf("userID = %q, want the caller whose run this was — not the person who was asked", ev.UserID)
	}
	if ev.ApprovalRecipient != "approver@test" {
		t.Errorf("approvalRecipient = %q, want the person who was asked", ev.ApprovalRecipient)
	}
}

func TestAbandonmentConfirmed(t *testing.T) {
	w := &WorkflowInstance{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	row := wfstore.PausedWorkflow{RequestID: "req-1", ExecutionID: "exec-1"}
	routing := persistedRouting{ToolName: "gated_call"}

	for _, tc := range []struct {
		name     string
		statuses map[string]string
		want     bool
	}{
		{"cancelled by this retirement", map[string]string{"req-1": "cancelled"}, true},
		{"answered elsewhere", map[string]string{"req-1": "resumed"}, false},
		{"timed out elsewhere", map[string]string{"req-1": "timed_out"}, false},
		{"failed elsewhere", map[string]string{"req-1": "failed"}, false},
		{"a vanished row is out-of-band tampering, not a race", map[string]string{}, false},
		{"a status nothing recognises", map[string]string{"req-1": "weird"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.abandonmentConfirmed(row, routing, tc.statuses); got != tc.want {
				t.Errorf("abandonmentConfirmed = %v, want %v", got, tc.want)
			}
		})
	}
}
