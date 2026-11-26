// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const callRunsFor = 20 * time.Millisecond

const auditedFailureErrorType = "tool_error"

const (
	auditHolderSession  = "parent-session-holder"
	auditAdoptedSession = "parent-session-adopted"
	auditHolderTrace    = "trace-holder"
	auditAdoptedTrace   = "trace-1"
	auditAdoptedUser    = "user-1"
	auditAdoptedKey     = "key-1"
)

type lockedToolMetrics struct {
	mu         sync.Mutex
	histograms []toolMetricCall
}

func (m *lockedToolMetrics) Counter(string, int64, ...string) {}
func (m *lockedToolMetrics) Gauge(string, float64, ...string) {}
func (m *lockedToolMetrics) Histogram(name string, duration time.Duration, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.histograms = append(m.histograms, toolMetricCall{name: name, duration: duration, labels: labels})
}

func (m *lockedToolMetrics) durationsFor(tool string) []time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	var durations []time.Duration
	for _, h := range m.histograms {
		if h.name == "operation.duration" && labelOf(h.labels, "component.name") == tool {
			durations = append(durations, h.duration)
		}
	}
	return durations
}

func (m *lockedToolMetrics) errorTypesFor(tool string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errorTypes []string
	for _, h := range m.histograms {
		if h.name != "operation.duration" || labelOf(h.labels, "component.name") != tool {
			continue
		}
		errorTypes = append(errorTypes, labelOf(h.labels, "error.type"))
	}
	return errorTypes
}

func labelOf(labels []string, key string) string {
	for i := 0; i+1 < len(labels); i += 2 {
		if labels[i] == key {
			return labels[i+1]
		}
	}
	return ""
}

func (r *toolAuditRecorder) eventsFor(toolName string) []auth.ToolExecutionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var matching []auth.ToolExecutionEvent
	for _, e := range r.events {
		if e.ToolName == toolName {
			matching = append(matching, e)
		}
	}
	return matching
}

func failingSTRReply(corrID string) []byte {
	return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%q,"error":{"code":"EXECUTION_ERROR","message":"the report builder crashed"}}`, corrID)
}

func publishSTRReplyPayload(t *testing.T, brk *broker.DevBroker, replyTo string, payload []byte) {
	t.Helper()
	if err := brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload}); err != nil {
		t.Errorf("publish STR reply: %v", err)
	}
}

func wantRemoteFailureAudit(user, credential, sessionID, traceID string) auth.ToolExecutionEvent {
	return auth.ToolExecutionEvent{
		UserID:       user,
		CredentialID: credential,
		AgentName:    "test-agent",
		ToolName:     remoteReportTool,
		SessionID:    sessionID,
		TraceID:      traceID,
		Success:      false,
		Error:        auditedFailureErrorType,
	}
}

func assertAuditedOnce(t *testing.T, rec *toolAuditRecorder, want auth.ToolExecutionEvent) {
	t.Helper()
	events := rec.eventsFor(remoteReportTool)
	if len(events) != 1 {
		t.Fatalf("audit events for %s = %+v, want exactly one", remoteReportTool, events)
	}
	got := events[0]
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(auth.ToolExecutionEvent{}, "DurationMs")); diff != "" {
		t.Errorf("audit event mismatch (-want +got):\n%s", diff)
	}
	if got.DurationMs < float64(callRunsFor.Milliseconds()) {
		t.Errorf("audit duration = %vms, want at least the %v the call ran", got.DurationMs, callRunsFor)
	}
}

func assertStillAuditedOnce(t *testing.T, rec *toolAuditRecorder, settle time.Duration) {
	t.Helper()
	time.Sleep(settle)
	if events := rec.eventsFor(remoteReportTool); len(events) != 1 {
		t.Errorf("audit events for %s after settling = %+v, want still exactly one", remoteReportTool, events)
	}
}

func holderUser() a2a.UserProperties {
	user := concurrentUser
	user.TraceID = auditHolderTrace
	return user
}

func messageWithParentSession(text string) a2a.Message {
	msg := concUserMessage(text, idgen.New())
	msg.Metadata = map[string]any{"parentSessionID": auditHolderSession}
	return msg
}

type checkpointWriteHook struct {
	session.Store
	onWrite func() error
}

func (s *checkpointWriteHook) SaveCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	if err := s.onWrite(); err != nil {
		return err
	}
	return s.Store.SaveCheckpoint(ctx, cp)
}

func (s *checkpointWriteHook) UpdateCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	if err := s.onWrite(); err != nil {
		return err
	}
	return s.Store.UpdateCheckpoint(ctx, cp)
}

func TestPausedRemoteCallThatLaterFailsIsAuditedOnceWithItsRealOutcome(t *testing.T) {
	agent, mock, _ := replicaOf(t, session.NewMemoryStore(), "owner")
	rec := &toolAuditRecorder{}
	metrics := &lockedToolMetrics{}
	svc := agent.svc.(*mockServices)
	svc.auditLog, svc.metrics = rec, metrics
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool), {Content: "The report builder crashed."}}
	ids, release := heldSTRReplying(t, strBrk, failingSTRReply)

	if resp, err := agent.HandleTask(t.Context(), "task-remote-audit", messageWithParentSession("build the report"), holderUser()); err != nil || resp != nil {
		t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
	}
	waitForCorrID(t, ids)
	if events := rec.eventsFor(remoteReportTool); len(events) != 0 {
		t.Errorf("audit events at publish time = %+v, want none: a paused call has not executed yet", events)
	}
	if errorTypes := metrics.errorTypesFor(remoteReportTool); len(errorTypes) != 0 {
		t.Errorf("metric samples at publish time = %q, want none", errorTypes)
	}

	time.Sleep(callRunsFor)
	release()
	if !waitUntil(func() bool { return len(mock.getCalls()) == 2 && agent.pausedTasks.Load("task-remote-audit") == nil }) {
		t.Fatalf("the task did not resume and finish: %d LLM calls", len(mock.getCalls()))
	}

	assertAuditedOnce(t, rec, wantRemoteFailureAudit(concurrentUser.UserID, "", auditHolderSession, auditHolderTrace))
	if errorTypes := metrics.errorTypesFor(remoteReportTool); len(errorTypes) != 1 || errorTypes[0] != auditedFailureErrorType {
		t.Errorf("metric error types = %q, want one sample typed %q", errorTypes, auditedFailureErrorType)
	}
}

func TestRemoteCallResolvedElsewhereBeforeTheHolderDeliversIsNotAuditedByTheHolder(t *testing.T) {
	store := session.NewMemoryStore()
	agent, mock, _ := replicaOf(t, store, "owner")
	rec := &toolAuditRecorder{}
	agent.svc.(*mockServices).auditLog = rec
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool), {Content: "done"}}
	ids, release := heldSTRReplying(t, strBrk, failingSTRReply)

	if resp, err := agent.HandleTask(t.Context(), "task-remote-lost-race", messageWithParentSession("build the report"), holderUser()); err != nil || resp != nil {
		t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
	}
	corrID := waitForCorrID(t, ids)
	if _, err := store.RecordToolResult(t.Context(), "task-remote-lost-race", corrID, &session.ToolCallResult{ToolCallID: "call-r1", ToolName: remoteReportTool, Content: "adopted elsewhere"}); err != nil {
		t.Fatalf("resolve the call as another replica would: %v", err)
	}

	release()
	if !waitUntil(func() bool { return len(mock.getCalls()) >= 2 }) {
		t.Fatalf("the task did not resume after the holder's reply: %d LLM calls", len(mock.getCalls()))
	}

	if events := rec.eventsFor(remoteReportTool); len(events) != 0 {
		t.Errorf("holder audit events = %+v, want none: the call was resolved, and so audited, elsewhere", events)
	}
}

func TestRemoteCallAdoptedOnAnotherReplicaIsAuditedOnceWithItsRealOutcome(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	rec := &toolAuditRecorder{}
	survivor.svc.(*mockServices).auditLog = rec
	strBrk := strBroker(t)
	registerRemoteTool(t, survivor, strBrk)
	survivor.installRemoteToolOrphanHandlers()
	corrID := idgen.New()
	pausedOnRemoteCallWithOriginalSession(t, store, owner, "task-remote-adopt-audit", corrID, auditAdoptedSession)

	time.Sleep(callRunsFor)
	publishSTRReplyPayload(t, strBrk, remotetool.ResponseTopic("test-ns", "test-agent", corrID), failingSTRReply(corrID))

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, "task-remote-adopt-audit", "the report builder crashed")
	assertAuditedOnce(t, rec, wantRemoteFailureAudit(auditAdoptedUser, auditAdoptedKey, auditAdoptedSession, auditAdoptedTrace))
}

func TestRemoteCallExpiredByTheReaperIsAuditedOnceAsAFailure(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, _, _ := replicaOf(t, store, "survivor")
	rec := &toolAuditRecorder{}
	survivor.svc.(*mockServices).auditLog = rec
	pausedOnRemoteCallWithOriginalSession(t, store, owner, "task-remote-expire-audit", idgen.New(), auditAdoptedSession)

	time.Sleep(callRunsFor)
	survivor.reapExpiredPendingCalls(context.Background(), time.Now().Add(2*time.Hour))

	assertAuditedOnce(t, rec, wantRemoteFailureAudit(auditAdoptedUser, auditAdoptedKey, auditAdoptedSession, auditAdoptedTrace))
}

func TestCancellingATaskPausedOnARemoteToolAuditsTheCallOnceAsAFailure(t *testing.T) {
	agent, mock, _ := replicaOf(t, session.NewMemoryStore(), "owner")
	rec := &toolAuditRecorder{}
	metrics := &lockedToolMetrics{}
	svc := agent.svc.(*mockServices)
	svc.auditLog, svc.metrics = rec, metrics
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool)}
	ids, release := heldSTRReplying(t, strBrk, failingSTRReply)

	if resp, err := agent.HandleTask(t.Context(), "task-remote-cancel-audit", messageWithParentSession("build the report"), holderUser()); err != nil || resp != nil {
		t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
	}
	waitForCorrID(t, ids)
	state := agent.pausedTasks.Load("task-remote-cancel-audit")
	if state == nil {
		t.Fatal("the task is not paused")
	}
	time.Sleep(callRunsFor)
	agent.cancelPausedTask(state, cancelFromElsewhere)

	assertAuditedOnce(t, rec, wantRemoteFailureAudit(concurrentUser.UserID, "", auditHolderSession, auditHolderTrace))
	release()
	assertStillAuditedOnce(t, rec, 200*time.Millisecond)
	if errorTypes := metrics.errorTypesFor(remoteReportTool); len(errorTypes) != 1 || errorTypes[0] != auditedFailureErrorType {
		t.Errorf("metric error types = %q, want one sample typed %q", errorTypes, auditedFailureErrorType)
	}
}

func TestACancelledContextAtTheRemoteToolPauseAuditsTheCallOnceAsAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := &checkpointWriteHook{Store: session.NewMemoryStore(), onWrite: func() error { time.Sleep(callRunsFor); cancel(); return nil }}
	agent, mock, _ := replicaOf(t, store, "owner")
	rec := &toolAuditRecorder{}
	agent.svc.(*mockServices).auditLog = rec
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool)}
	ids, release := heldSTRReplying(t, strBrk, failingSTRReply)

	if _, err := agent.HandleTask(ctx, "task-remote-ctx-audit", messageWithParentSession("build the report"), holderUser()); err != nil {
		t.Fatalf("task: %v", err)
	}
	waitForCorrID(t, ids)

	if !waitUntil(func() bool { return len(rec.eventsFor(remoteReportTool)) > 0 }) {
		t.Fatal("the call paused under a cancelled context was never audited")
	}
	release()
	assertAuditedOnce(t, rec, wantRemoteFailureAudit(concurrentUser.UserID, "", auditHolderSession, auditHolderTrace))
	assertStillAuditedOnce(t, rec, 200*time.Millisecond)
}

func TestAHolderThatStandsDownAtTheRemoteToolPauseAuditsItsCallOnceAsAFailure(t *testing.T) {
	store := &checkpointWriteHook{Store: session.NewMemoryStore(), onWrite: func() error { time.Sleep(callRunsFor); return session.ErrNotOwner }}
	agent, mock, _ := replicaOf(t, store, "owner")
	rec := &toolAuditRecorder{}
	agent.svc.(*mockServices).auditLog = rec
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool)}
	ids, release := heldSTRReplying(t, strBrk, failingSTRReply)

	if _, err := agent.HandleTask(t.Context(), "task-remote-lost-audit", messageWithParentSession("build the report"), holderUser()); err != nil {
		t.Fatalf("task: %v", err)
	}
	waitForCorrID(t, ids)

	if !waitUntil(func() bool { return len(rec.eventsFor(remoteReportTool)) > 0 }) {
		t.Fatal("the call dispatched by a holder that stood down was never audited")
	}
	release()
	assertAuditedOnce(t, rec, wantRemoteFailureAudit(concurrentUser.UserID, "", auditHolderSession, auditHolderTrace))
	assertStillAuditedOnce(t, rec, 200*time.Millisecond)
}
