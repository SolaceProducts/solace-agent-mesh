// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	recordPanickedLog = "recording a delivered tool result panicked; resuming the task with the result in hand"
	reportPanickedLog = "reporting a delivered remote tool call panicked; its audit event or metric may be missing"
)

const (
	deliverableTool    = "deliverable_call"
	deliverableTaskID  = "task-deliverable"
	auditSinkPanicText = "the audit sink exploded"
)

type panicOnFirstAuditLogger struct {
	auth.NoopAuditLogger
	attempts atomic.Int32
}

func (l *panicOnFirstAuditLogger) LogToolExecution(context.Context, auth.ToolExecutionEvent) {
	if l.attempts.Add(1) == 1 {
		panic(auditSinkPanicText)
	}
}

type orderLog struct {
	mu sync.Mutex

	entries []string
}

func (o *orderLog) add(entry string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries = append(o.entries, entry)
}

func (o *orderLog) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.entries...)
}

type recordCountingStore struct {
	session.Store
	order    *orderLog
	recorded atomic.Int32
}

func (s *recordCountingStore) RecordToolResult(ctx context.Context, taskID, corrID string, result *session.ToolCallResult) (int, error) {
	remaining, err := s.Store.RecordToolResult(ctx, taskID, corrID, result)
	s.recorded.Add(1)
	s.order.add("recorded")
	return remaining, err
}

func pausedOnDeliverableCall(t *testing.T, agent *Agent, mock *mockLLM, pendingKind string) func(*tool.Result) {
	t.Helper()
	deliverCh := make(chan func(*tool.Result), 1)
	data := map[string]any{"sub_task_id": "st"}
	if pendingKind != "" {
		data["pending_kind"] = pendingKind
	}
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        deliverableTool,
		description: "pauses until its result is delivered",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			r := tool.Pending("waiting for the delivery", tool.WithData(data))
			r.OnDelivery = func(deliver func(*tool.Result)) { deliverCh <- deliver }
			return r, nil
		},
	}); err != nil {
		t.Fatalf("register %s: %v", deliverableTool, err)
	}
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-d1", deliverableTool), {Content: "All done."}}
	if resp, err := agent.HandleTask(t.Context(), deliverableTaskID, messageWithParentSession("start the call"), holderUser()); err != nil || resp != nil {
		t.Fatalf("the task should pause on the call: resp=%v err=%v", resp, err)
	}
	select {
	case deliver := <-deliverCh:
		return deliver
	case <-time.After(5 * time.Second):
		t.Fatal("the pause never handed over its delivery callback")
		return nil
	}
}

func waitForTaskToFinishResuming(t *testing.T, agent *Agent, mock *mockLLM) {
	t.Helper()
	if !waitUntil(func() bool { return len(mock.getCalls()) == 2 && agent.pausedTasks.Load(deliverableTaskID) == nil }) {
		t.Fatalf("the task did not resume and finish: %d LLM calls", len(mock.getCalls()))
	}
}

func TestOnlyARemoteToolDeliveryIsAuditedAndMeasuredByTheHolder(t *testing.T) {
	type observed struct{ Audits, Metrics int }
	tests := []struct {
		name        string
		pendingKind string
		want        observed
	}{
		{name: "a remote tool call is audited and measured once", pendingKind: "remote_tool", want: observed{Audits: 1, Metrics: 1}},
		{name: "a workflow call is neither audited nor measured as a remote tool", pendingKind: "workflow"},
		{name: "a peer call is neither audited nor measured as a remote tool", pendingKind: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, mock, _ := replicaOf(t, session.NewMemoryStore(), "owner")
			rec := &toolAuditRecorder{}
			metrics := &lockedToolMetrics{}
			svc := agent.svc.(*mockServices)
			svc.auditLog, svc.metrics = rec, metrics
			deliver := pausedOnDeliverableCall(t, agent, mock, tt.pendingKind)
			atPause := observed{Audits: len(rec.eventsFor(deliverableTool)), Metrics: len(metrics.errorTypesFor(deliverableTool))}

			deliver(tool.OK("the call finished"))

			waitForTaskToFinishResuming(t, agent, mock)
			got := observed{
				Audits:  len(rec.eventsFor(deliverableTool)) - atPause.Audits,
				Metrics: len(metrics.errorTypesFor(deliverableTool)) - atPause.Metrics,
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("audit events and metric samples added by the delivery (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRedeliveryAfterAPanickedDeliveryRecordsAndResumesOnce(t *testing.T) {
	logs := &syncLogBuffer{}
	sink := &panicOnFirstAuditLogger{}
	store := &recordCountingStore{Store: session.NewMemoryStore(), order: &orderLog{}}
	agent, mock, _ := replicaOf(t, store, "owner", capturingLogs(logs))
	agent.svc.(*mockServices).auditLog = sink
	deliver := pausedOnDeliverableCall(t, agent, mock, "remote_tool")

	deliver(tool.OK("the call finished"))
	deliver(tool.OK("the call finished again"))

	waitForTaskToFinishResuming(t, agent, mock)
	got := map[string]int{
		"results recorded": int(store.recorded.Load()),
		"llm calls":        len(mock.getCalls()),
		"audit attempts":   int(sink.attempts.Load()),
	}
	if diff := cmp.Diff(map[string]int{"results recorded": 1, "llm calls": 2, "audit attempts": 1}, got); diff != "" {
		t.Errorf("work done for two deliveries of one call, the first of which panicked (-want +got):\n%s", diff)
	}
}

func TestPanickedDeliveryIsLoggedAtErrorWithItsPanicAndStack(t *testing.T) {
	logs := &syncLogBuffer{}
	agent, mock, _ := replicaOf(t, session.NewMemoryStore(), "owner", capturingLogs(logs))
	agent.svc.(*mockServices).auditLog = &panicOnFirstAuditLogger{}
	deliver := pausedOnDeliverableCall(t, agent, mock, "remote_tool")

	deliver(tool.OK("the call finished"))

	records := logs.records(reportPanickedLog)
	if diff := cmp.Diff(1, len(records)); diff != "" {
		t.Fatalf("records %q count mismatch (-want +got):\n%s", reportPanickedLog, diff)
	}
	rec := records[0]
	if diff := cmp.Diff(map[string]any{"level": "ERROR", "taskID": deliverableTaskID, "toolName": deliverableTool, "toolCallID": "call-d1", "panic": auditSinkPanicText},
		map[string]any{"level": rec["level"], "taskID": rec["taskID"], "toolName": rec["toolName"], "toolCallID": rec["toolCallID"], "panic": rec["panic"]}); diff != "" {
		t.Errorf("panic record fields (-want +got):\n%s", diff)
	}
	if stack, _ := rec["stack"].(string); !strings.Contains(stack, "reportDeliveredCall") {
		t.Errorf("panic record stack = %q, want it to name the panicking reportDeliveredCall frame", stack)
	}
}

func TestDeliveredResultIsRecordedBeforeTheTaskResumes(t *testing.T) {
	order := &orderLog{}
	store := &recordCountingStore{Store: session.NewMemoryStore(), order: order}
	agent, mock, _ := replicaOf(t, store, "owner")
	mock.preCallHook = func(callIndex int) {
		if callIndex == 1 {
			order.add("resumed")
		}
	}
	deliver := pausedOnDeliverableCall(t, agent, mock, "remote_tool")

	deliver(tool.OK("the call finished"))

	waitForTaskToFinishResuming(t, agent, mock)
	if diff := cmp.Diff([]string{"recorded", "resumed"}, order.snapshot()); diff != "" {
		t.Errorf("order of the durable record and the resumed turn (-want +got):\n%s", diff)
	}
}

type panickingAuditLogger struct {
	auth.NoopAuditLogger
}

func (panickingAuditLogger) LogToolExecution(context.Context, auth.ToolExecutionEvent) {
	panic("the audit sink exploded")
}

type recordPanickingStore struct {
	session.Store
}

func (recordPanickingStore) RecordToolResult(context.Context, string, string, *session.ToolCallResult) (int, error) {
	panic("the session store exploded")
}

func TestPanicInsideTheDeliveryCallbackStillResumesTheTaskWithTheRealResult(t *testing.T) {
	tests := []struct {
		name        string
		auditPanics bool
		wantLog     string
	}{
		{name: "the audit sink panics", auditPanics: true, wantLog: reportPanickedLog},
		{name: "the session store panics while recording the result", wantLog: recordPanickedLog},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var store session.Store = session.NewMemoryStore()
			if !tt.auditPanics {
				store = recordPanickingStore{Store: store}
			}
			logs := &syncLogBuffer{}
			agent, mock, _ := replicaOf(t, store, "owner", capturingLogs(logs))
			if tt.auditPanics {
				agent.svc.(*mockServices).auditLog = panickingAuditLogger{}
			}
			strBrk := strBroker(t)
			registerRemoteTool(t, agent, strBrk)
			mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool), {Content: "The report is ready."}}
			ids, release := heldSTR(t, strBrk, "report ready")
			const taskID = "task-delivery-panic"
			if resp, err := agent.HandleTask(t.Context(), taskID, messageWithParentSession("build the report"), holderUser()); err != nil || resp != nil {
				t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
			}
			waitForCorrID(t, ids)
			state := agent.pausedTasks.Load(taskID)
			if state == nil {
				t.Fatal("the task is not paused")
			}

			release()

			if !waitUntil(func() bool { return len(mock.getCalls()) == 2 && agent.pausedTasks.Load(taskID) == nil }) {
				t.Fatalf("the task did not resume after its call's delivery panicked: %d LLM calls", len(mock.getCalls()))
			}
			if !requestContains(mock.getCalls()[1], "report ready") {
				t.Error("the resumed turn did not see the tool's real reply")
			}
			if diff := cmp.Diff(1, len(logs.records(tt.wantLog))); diff != "" {
				t.Errorf("records %q count mismatch (-want +got):\n%s", tt.wantLog, diff)
			}
			writesDone := make(chan struct{})
			go func() {
				state.durableWrites.Wait()
				close(writesDone)
			}()
			select {
			case <-writesDone:
			case <-time.After(5 * time.Second):
				t.Error("durableWrites never balanced: a panic skipped its Done, so a later resume would block forever")
			}
		})
	}
}
