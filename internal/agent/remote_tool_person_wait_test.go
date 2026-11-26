// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	personWaitRequestID = "req-remote-approval"
	personWaitTaskID    = "task-remote-hil"
	heldElsewhereID     = "req-held-elsewhere"
	victimTaskID        = "task-victim"
	victimOwnerID       = "owner-victim"
)

func strReplyWith(status string, data map[string]any) func(corrID string) []byte {
	return func(corrID string) []byte {
		payload, _ := json.Marshal(a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: map[string]any{
			"_schema": "ToolResult",
			"status":  status,
			"message": "waiting for approval",
			"data":    data,
		}}))
		return payload
	}
}

func askingForAPersonSTRReply(timeout time.Duration) func(corrID string) []byte {
	return strReplyWith("pending", map[string]any{
		"pending_kind":   "hil_approval",
		"hil_request_id": personWaitRequestID,
		"hil_timeout_ns": float64(timeout),
	})
}

func pausedOnARemoteCallReplying(t *testing.T, store session.Store, reply func(corrID string) []byte, configure ...func(*Agent)) (*Agent, *mockLLM, string) {
	t.Helper()
	agent, mock, _ := replicaOf(t, store, "owner", configure...)
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{
		singleToolCallResponse("call-r1", remoteReportTool),
		{Content: "Approved and done."},
	}
	ids, release := heldSTRReplying(t, strBrk, reply)

	if resp, err := agent.HandleTask(t.Context(), personWaitTaskID, concUserMessage("build the report", idgen.New()), concurrentUser); err != nil || resp != nil {
		t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
	}
	corrID := waitForCorrID(t, ids)
	release()
	return agent, mock, corrID
}

func pausedOnARemoteCallThatAsksAPerson(t *testing.T, store session.Store, timeout time.Duration, configure ...func(*Agent)) (*Agent, *mockLLM, string) {
	t.Helper()
	return pausedOnARemoteCallReplying(t, store, askingForAPersonSTRReply(timeout), configure...)
}

func personWaitCall(agent *Agent) *PendingToolCall {
	state := agent.pausedTasks.Load(personWaitTaskID)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if pc := state.PendingCalls["call-r1"]; pc != nil {
		snapshot := *pc
		return &snapshot
	}
	return nil
}

func waitingOnAPerson(agent *Agent) bool {
	pc := personWaitCall(agent)
	return pc != nil && pc.Kind == PendingCallHILApproval && !pc.notYetDurable
}

func personsAnswer() *a2a.UserInputResponseData {
	return &a2a.UserInputResponseData{
		Type: "user_input_response", RequestID: personWaitRequestID, Status: "answered",
		Data: map[string]any{"decision": "approved", "approver": "alice@example.com"},
	}
}

func toolTextsOf(req llm.ChatRequest) []string {
	var texts []string
	for _, m := range req.Messages {
		if m.Role == "tool" {
			texts = append(texts, m.Content)
		}
	}
	return texts
}

func finishedWithAnswer(agent *Agent, mock *mockLLM) bool {
	return len(mock.getCalls()) == 2 && agent.pausedTasks.Load(personWaitTaskID) == nil
}

func TestRemoteToolRequestForAPersonsAnswerKeepsTheTaskPausedAndResumesOnTheAnswer(t *testing.T) {
	store := session.NewMemoryStore()
	agent, mock, corrID := pausedOnARemoteCallThatAsksAPerson(t, store, time.Hour)

	if !waitUntil(func() bool { return waitingOnAPerson(agent) }) {
		t.Fatalf("the pending call = %+v, want a hil_approval wait", personWaitCall(agent))
	}
	pc := personWaitCall(agent)
	if pc.HILRequestID != personWaitRequestID || pc.Timeout != time.Hour {
		t.Errorf("pending call = %+v, want request %s with the STR's timeout", pc, personWaitRequestID)
	}
	if len(mock.getCalls()) != 1 {
		t.Fatalf("%d LLM calls; the task must stay paused while the person has not answered", len(mock.getCalls()))
	}

	ctx := context.Background()
	if row, err := store.LookupPendingCall(ctx, agent.cfg.Name, corrID); err != nil || row != nil {
		t.Errorf("the remote call's correlation row = %+v (err %v), want it replaced", row, err)
	}
	row, err := store.LookupPendingCall(ctx, agent.cfg.Name, personWaitRequestID)
	if err != nil || row == nil || row.Kind != session.PendingKindHILApproval || row.ToolCallID != "call-r1" {
		t.Fatalf("the person's correlation row = %+v (err %v), want a hil_approval row for call-r1", row, err)
	}
	cp, err := store.GetCheckpoint(ctx, personWaitTaskID)
	if err != nil || cp == nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if cp.PendingCount != 1 || len(cp.ToolResults) != 0 {
		t.Errorf("checkpoint pending count %d and %d banked results, want 1 and none: the pending reply must not stay banked", cp.PendingCount, len(cp.ToolResults))
	}
	persisted, err := unmarshalTaskState(cp.State)
	if err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	if got := persisted.PendingCalls["call-r1"]; got == nil || got.Kind != PendingCallHILApproval || got.HILRequestID != personWaitRequestID || got.Result != nil {
		t.Errorf("checkpointed pending call = %+v, want the unanswered hil_approval wait", got)
	}

	if got := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}
	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("the task did not resume on the answer: %d LLM calls", len(mock.getCalls()))
	}
	texts := toolTextsOf(mock.getCalls()[1])
	if len(texts) != 1 || !strings.Contains(texts[0], "approved") || strings.Contains(texts[0], "waiting for approval") {
		t.Errorf("tool results of the resumed turn = %q, want only the person's answer, never the remote tool's pending reply", texts)
	}
}

func TestRemoteToolRequestForAPersonsAnswerTimesOutIntoAToolResult(t *testing.T) {
	agent, mock, _ := pausedOnARemoteCallThatAsksAPerson(t, session.NewMemoryStore(), 200*time.Millisecond)

	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("the unanswered wait never timed out and resumed its task: %d LLM calls", len(mock.getCalls()))
	}
	texts := toolTextsOf(mock.getCalls()[1])
	if len(texts) != 1 || !strings.Contains(texts[0], "timed_out") {
		t.Errorf("tool results of the resumed turn = %q, want the wait's timeout", texts)
	}
}

type gatedStore struct {
	session.Store

	reached chan struct{}
	proceed chan struct{}
	gate    func(s *gatedStore, taskID, corrID string) bool

	mu      sync.Mutex
	once    sync.Once
	records []recordedCall
}

type recordedCall struct {
	corrID string
	err    error
}

func (s *gatedStore) UpdateCheckpoint(ctx context.Context, cp *session.TaskCheckpoint) error {
	if s.gate != nil && s.gate(s, cp.TaskID, "") {
		s.hold()
	}
	return s.Store.UpdateCheckpoint(ctx, cp)
}

func (s *gatedStore) RecordToolResult(ctx context.Context, taskID, corrID string, result *session.ToolCallResult) (int, error) {
	if s.gate != nil && s.gate(s, taskID, corrID) {
		s.hold()
	}
	remaining, err := s.Store.RecordToolResult(ctx, taskID, corrID, result)
	s.mu.Lock()
	s.records = append(s.records, recordedCall{corrID: corrID, err: err})
	s.mu.Unlock()
	return remaining, err
}

func (s *gatedStore) hold() {
	s.once.Do(func() {
		close(s.reached)
		<-s.proceed
	})
}

func (s *gatedStore) recordsFor(corrID string) []recordedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recordedCall
	for _, r := range s.records {
		if r.corrID == corrID {
			out = append(out, r)
		}
	}
	return out
}

func newGatedStore(gate func(s *gatedStore, taskID, corrID string) bool) *gatedStore {
	return &gatedStore{Store: session.NewMemoryStore(), reached: make(chan struct{}), proceed: make(chan struct{}), gate: gate}
}

func TestAnswerArrivingWhileTheRepauseIsBeingWrittenIsBankedAndResumesTheTaskOnce(t *testing.T) {
	store := newGatedStore(func(_ *gatedStore, _, corrID string) bool { return corrID == "" })
	agent, mock, _ := pausedOnARemoteCallThatAsksAPerson(t, store, time.Hour)
	waitOrFail(t, store.reached, "the re-pause to reach its checkpoint write")

	if got := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("delivery during the write = %v, want the answer held for the task", got)
	}
	if len(mock.getCalls()) != 1 {
		t.Fatalf("%d LLM calls; the answer must not resume a task whose wait is not written yet", len(mock.getCalls()))
	}
	close(store.proceed)

	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("the task did not resume on the answer: %d LLM calls", len(mock.getCalls()))
	}
	if brieflyBecomes(func() bool { return len(mock.getCalls()) > 2 }) {
		t.Errorf("%d LLM calls, want the task resumed exactly once", len(mock.getCalls()))
	}
	got := store.recordsFor(personWaitRequestID)
	if len(got) != 1 || got[0].err != nil {
		t.Errorf("records of the person's answer = %+v, want one that found its correlation row", got)
	}
	texts := toolTextsOf(mock.getCalls()[1])
	if len(texts) != 1 || !strings.Contains(texts[0], "approved") {
		t.Errorf("tool results of the resumed turn = %q, want the person's answer", texts)
	}
}

func TestAnswerArrivingBeforeTheTaskRepausesIsBankedAndResumesTheTaskOnce(t *testing.T) {
	store := newGatedStore(func(_ *gatedStore, _, corrID string) bool { return corrID != "" && corrID != personWaitRequestID })
	agent, mock, _ := pausedOnARemoteCallThatAsksAPerson(t, store, time.Hour)
	waitOrFail(t, store.reached, "the delivery of the pending reply to be recorded")

	if got := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("delivery before the re-pause = %v, want the answer held for the task", got)
	}
	close(store.proceed)

	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("the task did not resume on the answer: %d LLM calls", len(mock.getCalls()))
	}
	if brieflyBecomes(func() bool { return len(mock.getCalls()) > 2 }) {
		t.Errorf("%d LLM calls, want the task resumed exactly once", len(mock.getCalls()))
	}
	got := store.recordsFor(personWaitRequestID)
	if len(got) != 1 || got[0].err != nil {
		t.Errorf("records of the person's answer = %+v, want one that found its correlation row", got)
	}
	texts := toolTextsOf(mock.getCalls()[1])
	if len(texts) != 1 || !strings.Contains(texts[0], "approved") {
		t.Errorf("tool results of the resumed turn = %q, want the person's answer", texts)
	}
}

func TestAnswerReachesAnotherReplicaAfterTheHolderDiesFollowingTheRepause(t *testing.T) {
	store := session.NewMemoryStore()
	holder, holderLLM, _ := replicaOf(t, store, "holder")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	strBrk := strBroker(t)
	registerRemoteTool(t, holder, strBrk)
	holderLLM.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool)}
	survivorLLM.responses = []*llm.ChatResponse{{Content: "Approved and done."}}
	ids, release := heldSTRReplying(t, strBrk, askingForAPersonSTRReply(time.Hour))
	user := concurrentUser
	user.ReplyTo = resumedReplyTopic

	if resp, err := holder.HandleTask(t.Context(), personWaitTaskID, concUserMessage("build the report", idgen.New()), user); err != nil || resp != nil {
		t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
	}
	waitForCorrID(t, ids)
	release()
	if !waitUntil(func() bool { return waitingOnAPerson(holder) }) {
		t.Fatal("the holder never re-paused the task on the person")
	}
	holder.pausedTasks.Delete(personWaitTaskID)
	ownerDies(t, store, personWaitTaskID)

	if got := survivor.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("delivery to the replica that does not hold the task = %v, want delivered", got)
	}

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, personWaitTaskID, "approved")
}

const (
	unusableIDResult = `{"message":"The tool asked a person for an answer but gave the request an id that cannot be used, so the answer could not be collected.","status":"error"}`
	idInUseResult    = `{"message":"The tool asked a person for an answer with a request id that another task already uses, so the answer could not be collected.","status":"error"}`
)

func TestRemoteToolReplyIsEitherAPersonsWaitOrADeliveredResult(t *testing.T) {
	type observed struct {
		WaitingOnPerson bool
		LLMCalls        int
		ToolResults     []string
		VictimRowTask   string
		EarlySlots      int
	}
	tests := []struct {
		name   string
		status string
		data   map[string]any
		want   observed
	}{
		{
			name:   "a pending hil_approval reply with a usable request id waits on the person",
			status: "pending",
			data:   map[string]any{"pending_kind": "hil_approval", "hil_request_id": personWaitRequestID},
			want:   observed{WaitingOnPerson: true, LLMCalls: 1, VictimRowTask: victimTaskID},
		},
		{
			name:   "a pending hil_approval reply without a request id is refused with an error result",
			status: "pending",
			data:   map[string]any{"pending_kind": "hil_approval"},
			want:   observed{LLMCalls: 2, ToolResults: []string{unusableIDResult}, VictimRowTask: victimTaskID},
		},
		{
			name:   "a pending hil_approval reply with a 257 byte request id is refused with an error result",
			status: "pending",
			data:   map[string]any{"pending_kind": "hil_approval", "hil_request_id": strings.Repeat("r", 257)},
			want:   observed{LLMCalls: 2, ToolResults: []string{unusableIDResult}, VictimRowTask: victimTaskID},
		},
		{
			name:   "a pending hil_approval reply whose request id another task holds is refused and leaves that task's row alone",
			status: "pending",
			data:   map[string]any{"pending_kind": "hil_approval", "hil_request_id": heldElsewhereID},
			want:   observed{LLMCalls: 2, ToolResults: []string{idInUseResult}, VictimRowTask: victimTaskID},
		},
		{
			name:   "a successful reply that carries hil_approval data is delivered as the call's result",
			status: "success",
			data:   map[string]any{"pending_kind": "hil_approval", "hil_request_id": personWaitRequestID},
			want: observed{LLMCalls: 2, VictimRowTask: victimTaskID, ToolResults: []string{
				`{"hil_request_id":"req-remote-approval","message":"waiting for approval","pending_kind":"hil_approval","status":"success"}`,
			}},
		},
		{
			name:   "a pending reply of another kind is delivered as the call's result",
			status: "pending",
			data:   map[string]any{"pending_kind": "workflow", "hil_request_id": personWaitRequestID},
			want: observed{LLMCalls: 2, VictimRowTask: victimTaskID, ToolResults: []string{
				`{"hil_request_id":"req-remote-approval","message":"waiting for approval","pending_kind":"workflow","status":"pending"}`,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			seedTaskHoldingARequestID(t, store, heldElsewhereID)
			agent, mock, _ := pausedOnARemoteCallReplying(t, store, strReplyWith(tt.status, tt.data))
			if !waitUntil(func() bool { return waitingOnAPerson(agent) || len(mock.getCalls()) == 2 }) {
				t.Fatalf("the task neither waited on a person nor resumed: %d LLM calls", len(mock.getCalls()))
			}
			if !tt.want.WaitingOnPerson && !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
				t.Fatalf("the task did not finish: %d LLM calls", len(mock.getCalls()))
			}

			got := observed{WaitingOnPerson: waitingOnAPerson(agent), LLMCalls: len(mock.getCalls()), EarlySlots: earlySlotCount(agent)}
			if calls := mock.getCalls(); len(calls) == 2 {
				got.ToolResults = toolTextsOf(calls[1])
			}
			if row, err := store.LookupPendingCall(t.Context(), agent.cfg.Name, heldElsewhereID); err == nil && row != nil {
				got.VictimRowTask = row.TaskID
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("outcome of the reply (-want +got):\n%s", diff)
			}
		})
	}
}

func seedTaskHoldingARequestID(t *testing.T, store session.Store, requestID string) {
	t.Helper()
	ctx := context.Background()
	lease := time.Now().Add(time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: victimTaskID, AppName: "test-agent", AgentName: "test-agent", UserID: "u2", SessionID: "sess-victim",
		PendingCount: 1, OwnerID: victimOwnerID, LeaseExpiresAt: &lease,
	}); err != nil {
		t.Fatalf("seed the other task's checkpoint: %v", err)
	}
	if err := store.RegisterPendingCalls(ctx, victimTaskID, victimOwnerID, []session.PendingCall{{
		CorrID: requestID, TaskID: victimTaskID, AgentName: "test-agent", ToolCallID: "call-victim", Kind: session.PendingKindHILApproval,
	}}); err != nil {
		t.Fatalf("seed the other task's pending call: %v", err)
	}
}

func TestADeliveryFromInsideTheWiringStillResumesThePausedTask(t *testing.T) {
	agent, mock, _ := replicaOf(t, session.NewMemoryStore(), "owner")
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        deliverableTool,
		description: "delivers its result before the pause finishes wiring it",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			r := tool.Pending("waiting for the delivery", tool.WithData(map[string]any{"sub_task_id": "st"}))
			r.OnDelivery = func(deliver func(*tool.Result)) { deliver(tool.OK("the call finished")) }
			return r, nil
		},
	}); err != nil {
		t.Fatalf("register %s: %v", deliverableTool, err)
	}
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-d1", deliverableTool), {Content: "All done."}}

	if resp, err := agent.HandleTask(t.Context(), deliverableTaskID, messageWithParentSession("start the call"), holderUser()); err != nil || resp != nil {
		t.Fatalf("the task should pause on the call: resp=%v err=%v", resp, err)
	}

	if !waitUntil(func() bool { return len(mock.getCalls()) == 2 && agent.pausedTasks.Load(deliverableTaskID) == nil }) {
		t.Errorf("LLM calls = %d and task still paused = %v, want the delivered task resumed to completion with a second call",
			len(mock.getCalls()), agent.pausedTasks.Load(deliverableTaskID) != nil)
	}
}

func TestAnAdoptedRemoteToolReplyAskingForAPersonIsRecordedForTheTaskToWaitOnThePerson(t *testing.T) {
	agent, _, _ := replicaOf(t, session.NewMemoryStore(), "survivor")
	registerRemoteTool(t, agent, strBroker(t))
	reply := askingForAPersonSTRReply(time.Hour)("corr-1")

	got := agent.remoteToolReplyResult(&session.PendingCall{CorrID: "corr-1", TaskID: "task-1", ToolName: remoteReportTool}, reply)

	if got == nil || got.Status != tool.StatusPending || !isPersonRequest(got) {
		t.Errorf("recorded result = %+v, want the pending request for a person, which the resumed task turns into a wait", got)
	}
}

type failingUpdateStore struct {
	session.Store
	err error
}

func (s failingUpdateStore) UpdateCheckpoint(context.Context, *session.TaskCheckpoint) error {
	return s.err
}

func TestRepauseThatCannotWriteItsCheckpoint(t *testing.T) {
	type observed struct {
		WaitingOnPerson bool
		TaskHeldHere    bool
		LLMCalls        int
		EarlySlots      int
	}
	tests := []struct {
		name string
		err  error
		want observed
	}{
		{
			name: "a task another process now holds is dropped here and never resumed",
			err:  session.ErrNotOwner,
			want: observed{LLMCalls: 1},
		},
		{
			name: "any other store failure still lets the task wait on the person in this process",
			err:  errors.New("the session database is unreachable"),
			want: observed{WaitingOnPerson: true, TaskHeldHere: true, LLMCalls: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, mock, _ := pausedOnARemoteCallThatAsksAPerson(t, failingUpdateStore{Store: session.NewMemoryStore(), err: tt.err}, time.Hour)
			if !waitUntil(func() bool { return waitingOnAPerson(agent) || agent.pausedTasks.Load(personWaitTaskID) == nil }) {
				t.Fatal("the re-pause neither stood the task down nor made it wait")
			}

			got := observed{WaitingOnPerson: waitingOnAPerson(agent), TaskHeldHere: agent.pausedTasks.Load(personWaitTaskID) != nil, LLMCalls: len(mock.getCalls()), EarlySlots: earlySlotCount(agent)}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("state after a re-pause whose checkpoint write failed (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCancelDuringTheRepauseTearsTheTaskDownWithoutResumingIt(t *testing.T) {
	store := newGatedStore(func(_ *gatedStore, _, corrID string) bool { return corrID == "" })
	agent, mock, _ := pausedOnARemoteCallThatAsksAPerson(t, store, time.Hour)
	waitOrFail(t, store.reached, "the re-pause to reach its checkpoint write")
	state := agent.pausedTasks.Load(personWaitTaskID)
	if state == nil {
		t.Fatal("the task is not held while it re-pauses")
	}

	agent.cancelPausedTask(state, cancelFromElsewhere)
	close(store.proceed)

	if !waitUntil(func() bool { return agent.pausedTasks.Load(personWaitTaskID) == nil }) {
		t.Fatal("the cancelled task was never torn down")
	}
	if cp, err := store.GetCheckpoint(t.Context(), personWaitTaskID); err != nil || cp != nil {
		t.Errorf("checkpoint of the cancelled task = %+v (err %v), want it removed", cp, err)
	}
	if got := len(mock.getCalls()); got != 1 {
		t.Errorf("%d LLM calls, want the cancelled task never resumed", got)
	}
	if got := earlySlotCount(agent); got != 0 {
		t.Errorf("%d early answer slots left, want the cancelled task's dropped", got)
	}
}

func TestARemoteReplyAskingForAPersonIsAuditedOnceAsPausedAndNotAgainAtTheAnswer(t *testing.T) {
	type observed struct {
		AuditSuccess []bool
		MetricTypes  []string
	}
	rec := &toolAuditRecorder{}
	metrics := &lockedToolMetrics{}
	agent, mock, _ := pausedOnARemoteCallThatAsksAPerson(t, session.NewMemoryStore(), time.Hour, func(a *Agent) {
		svc := a.svc.(*mockServices)
		svc.auditLog, svc.metrics = rec, metrics
	})
	snapshot := func() observed {
		var got observed
		for _, e := range rec.eventsFor(remoteReportTool) {
			got.AuditSuccess = append(got.AuditSuccess, e.Success)
		}
		got.MetricTypes = metrics.errorTypesFor(remoteReportTool)
		return got
	}
	if !waitUntil(func() bool { return waitingOnAPerson(agent) }) {
		t.Fatal("the task never waited on the person")
	}
	want := observed{AuditSuccess: []bool{true}, MetricTypes: []string{"paused"}}
	if diff := cmp.Diff(want, snapshot()); diff != "" {
		t.Errorf("audit events and metrics once the reply arrived (-want +got):\n%s", diff)
	}

	if got := agent.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}
	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatal("the task did not finish after the answer")
	}
	if diff := cmp.Diff(want, snapshot()); diff != "" {
		t.Errorf("audit events and metrics after the person answered (-want +got):\n%s", diff)
	}
}
