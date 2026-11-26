// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func hilTimerGateway(t *testing.T) *GatewayCore {
	t.Helper()
	core, _, _ := setupTestGateway(t)
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.logger = silentLogger()
	core.cfg.TaskTimeoutSeconds = 60
	return core
}

func idleTimerArmed(core *GatewayCore, taskID string) bool {
	core.taskTimers.mu.Lock()
	defer core.taskTimers.mu.Unlock()
	_, ok := core.taskTimers.timers[taskID]
	return ok
}

func armedTimerTiers(core *GatewayCore, taskID string) (next time.Time, tiers int, armed bool) {
	core.taskTimers.mu.Lock()
	defer core.taskTimers.mu.Unlock()
	entry, ok := core.taskTimers.timers[taskID]
	if !ok {
		return time.Time{}, 0, false
	}
	for _, deadline := range []time.Time{entry.softDeadline, entry.hardDeadline} {
		if deadline.IsZero() {
			continue
		}
		tiers++
		if next.IsZero() || deadline.Before(next) {
			next = deadline
		}
	}
	return next, tiers, true
}

func onlyHardTimeoutArmed(core *GatewayCore, taskID string) bool {
	core.taskTimers.mu.Lock()
	defer core.taskTimers.mu.Unlock()
	entry, ok := core.taskTimers.timers[taskID]
	return ok && entry.softDeadline.IsZero() && !entry.hardDeadline.IsZero()
}

func taskWaiting(core *GatewayCore, taskID string) bool {
	info := core.tasks.Get(taskID)
	return info != nil && info.WaitingRequestID != ""
}

func emitInputRequired(t *testing.T, core *GatewayCore, taskID, requestID string) {
	t.Helper()
	parts := []a2a.Part{a2a.NewTextPart("")}
	if requestID != "" {
		parts = append(parts, a2a.NewDataPart(map[string]any{"type": "user_input_request", "request_id": requestID}))
	}
	resp := a2a.Response{Result: &a2a.TaskStatusUpdateEvent{
		Kind:   "status-update",
		TaskID: taskID,
		Status: a2a.TaskStatus{State: a2a.TaskStateInputNeeded, Message: &a2a.Message{Role: "agent", Parts: parts}},
	}}
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal input-required: %v", err)
	}
	core.handleStatusEvent(t.Context(), &broker.Message{Topic: "test-ns/a2a/v1/gateway/status/test-gw/" + taskID, Payload: payload})
}

func emitArtifactUpdate(t *testing.T, core *GatewayCore, taskID string) {
	t.Helper()
	payload, err := json.Marshal(a2a.Response{Result: &a2a.TaskArtifactUpdateEvent{Kind: "artifact-update", TaskID: taskID}})
	if err != nil {
		t.Fatalf("marshal artifact-update: %v", err)
	}
	core.handleStatusEvent(t.Context(), &broker.Message{Topic: "test-ns/a2a/v1/gateway/status/test-gw/" + taskID, Payload: payload})
}

func TestStatusAwaitsUserInput(t *testing.T) {
	cases := map[a2a.TaskState]bool{
		a2a.TaskStateInputNeeded: true,
		a2a.TaskStateWorking:     false,
	}
	for state, want := range cases {
		event := &a2a.TaskStatusUpdateEvent{Kind: "status-update", TaskID: "t", Status: a2a.TaskStatus{State: state}}
		if got := statusAwaitsUserInput(event); got != want {
			t.Errorf("statusAwaitsUserInput(state=%q) = %v, want %v", state, got, want)
		}
	}
}

func TestUserInputRequestID(t *testing.T) {
	withRequest := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
		a2a.NewTextPart("please answer"),
		a2a.NewDataPart(map[string]any{"type": "user_input_request", "request_id": "req-7"}),
	}}}}
	if got := userInputRequestID(withRequest); got != "req-7" {
		t.Errorf("request id = %q, want req-7", got)
	}
	otherData := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
		a2a.NewDataPart(map[string]any{"type": "authentication_required"}),
	}}}}
	if got := userInputRequestID(otherData); got != "" {
		t.Errorf("request id for another data part = %q, want empty", got)
	}
	if got := userInputRequestID(&a2a.TaskStatusUpdateEvent{}); got != "" {
		t.Errorf("request id with no message = %q, want empty", got)
	}
}

func TestHandleStatusEvent_UserInputPendingKeepsOnlyTheHardTimeout(t *testing.T) {
	core := hilTimerGateway(t)
	taskID := "task-hil-cancel"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)
	if !idleTimerArmed(core, taskID) {
		t.Fatal("precondition: timer should be armed before handleStatusEvent")
	}

	emitInputRequired(t, core, taskID, "req-hil-1")

	if !onlyHardTimeoutArmed(core, taskID) {
		t.Error("in the input-required state the task must keep the hard timeout alone")
	}
	if info := core.tasks.Get(taskID); info == nil || info.WaitingRequestID != "req-hil-1" {
		t.Errorf("the task must be recorded as waiting on req-hil-1, got %+v", info)
	}
}

func TestHandleStatusEvent_UserInputPendingRecordsWaitAndBlocksRearm(t *testing.T) {
	core := hilTimerGateway(t)
	taskID := "task-hil-wait"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	emitInputRequired(t, core, taskID, "req-hil-2")
	if !onlyHardTimeoutArmed(core, taskID) {
		t.Fatal("only the hard timeout should be left after input-required")
	}
	if !taskWaiting(core, taskID) {
		t.Fatal("task should be recorded as waiting after input-required")
	}

	emitArtifactUpdate(t, core, taskID)

	if !onlyHardTimeoutArmed(core, taskID) {
		t.Error("artifact-update re-armed the idle notice during a HIL wait")
	}
	if !taskWaiting(core, taskID) {
		t.Error("task should remain waiting through the HIL wait")
	}
	core.cancelTaskTimeout(taskID)
}

func TestSubmitUserInputResponse_RearmsIdleTimer(t *testing.T) {
	core := hilTimerGateway(t)
	taskID := "task-hil-rearm"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
	emitInputRequired(t, core, taskID, "req-hil-3")
	if !onlyHardTimeoutArmed(core, taskID) {
		t.Fatal("precondition: only the hard timeout while the task waits")
	}

	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-hil-3", Status: "answered"}
	if err := core.SubmitUserInputResponse(t.Context(), "agent-x", taskID, resp, auth.NewTestSubject(auth.SubjectFields{UserID: "u1"})); err != nil {
		t.Fatalf("SubmitUserInputResponse: %v", err)
	}

	if taskWaiting(core, taskID) {
		t.Error("the answer must end the wait")
	}
	if !idleTimerArmed(core, taskID) || onlyHardTimeoutArmed(core, taskID) {
		t.Error("expected the idle notice re-armed after SubmitUserInputResponse")
	}
	core.cancelTaskTimeout(taskID)
}

func TestSubmitUserInputResponse_AnswerToAnotherRequestLeavesTheWait(t *testing.T) {
	core := hilTimerGateway(t)
	taskID := "task-hil-other"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
	emitInputRequired(t, core, taskID, "req-hil-new")

	stale := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-hil-old", Status: "answered"}
	if err := core.SubmitUserInputResponse(t.Context(), "agent-x", taskID, stale, auth.NewTestSubject(auth.SubjectFields{UserID: "u1"})); err != nil {
		t.Fatalf("SubmitUserInputResponse: %v", err)
	}

	if !taskWaiting(core, taskID) {
		t.Error("an answer to another request must not end the wait")
	}
	if !onlyHardTimeoutArmed(core, taskID) {
		t.Error("an answer to another request must not arm the idle notice")
	}
	core.cancelTaskTimeout(taskID)
}

func TestStandbyInstanceArmsNoTimer(t *testing.T) {
	core := hilTimerGateway(t)
	core.eventBindActive.Store(false)
	taskID := "task-standby"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})

	core.startTaskTimeout(taskID)
	if idleTimerArmed(core, taskID) {
		t.Error("a standby instance must not arm a timer at submit")
	}
	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-1", Status: "answered"}
	if err := core.SubmitUserInputResponse(t.Context(), "agent-x", taskID, resp, auth.NewTestSubject(auth.SubjectFields{UserID: "u1"})); err != nil {
		t.Fatalf("SubmitUserInputResponse: %v", err)
	}
	if idleTimerArmed(core, taskID) {
		t.Error("a standby instance must not arm a timer when it forwards an answer")
	}
}

func TestEventBindState_StandbyKeepsTimersWithoutTaskRecords(t *testing.T) {
	core := hilTimerGateway(t)
	taskID := "task-memory-standby"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	core.onEventBindState(broker.BindStandby)

	if !core.eventBindActive.Load() {
		t.Error("a process without task records stays active through a standby report")
	}
	if !idleTimerArmed(core, taskID) {
		t.Error("a process without task records keeps its timers")
	}
	core.cancelTaskTimeout(taskID)
}

type answeringAdapter struct {
	recordingAdapter
	core *GatewayCore
}

func (a *answeringAdapter) OnStatusUpdate(ctx context.Context, taskID string, event *a2a.TaskStatusUpdateEvent, raw []byte) {
	if id := userInputRequestID(event); id != "" {
		a.core.tasks.ClearWaiting(taskID, id)
	}
	a.recordingAdapter.OnStatusUpdate(ctx, taskID, event, raw)
}

func TestHandleStatusEvent_AnswerAsSoonAsShownEndsTheWait(t *testing.T) {
	core := hilTimerGateway(t)
	core.adapter = &answeringAdapter{core: core}
	taskID := "task-hil-quick-answer"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})

	emitInputRequired(t, core, taskID, "req-1")

	if taskWaiting(core, taskID) {
		t.Error("the task must not wait on a question that was already answered")
	}
}
