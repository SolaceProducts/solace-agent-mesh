// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func authStatusEvent(taskID, state string) (*a2a.TaskStatusUpdateEvent, *broker.Message) {
	authData := map[string]any{
		"type":                          "authentication_required",
		"target_agent":                  "agent-x",
		"target_agent_task_id":          taskID,
		"user_id":                       "u1",
		"client_id":                     "test-gw",
		"auth_uri":                      "https://idp.example/authorize?state=" + state,
		"auth_request_function_call_id": "call-1",
		"credential_key":                "tool__oauth2",
		"gateway_task_id":               nil,
		"code_verifier":                 "v",
	}
	event := &a2a.TaskStatusUpdateEvent{
		Kind:   "status-update",
		TaskID: taskID,
		Status: a2a.TaskStatus{
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewDataPart(authData)},
			},
		},
	}
	msg := &broker.Message{}
	return event, msg
}

func TestCacheAuthStateFromStatus_ReportsAuthPending(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.logger = silentLogger()

	t.Run("auth_required signal returns true", func(t *testing.T) {
		event, msg := authStatusEvent("task-1", "STATE-AUTH")
		if !core.cacheAuthStateFromStatus(event, msg) {
			t.Fatal("expected authPending=true when authentication_required is present")
		}
		if _, ok := core.Services().Cache().Get("gateway_auth_state_STATE-AUTH"); !ok {
			t.Error("expected ToolAuthState cached under derived state key")
		}
	})

	t.Run("plain status update returns false", func(t *testing.T) {
		event := &a2a.TaskStatusUpdateEvent{
			Kind:   "status-update",
			TaskID: "task-2",
			Status: a2a.TaskStatus{
				Message: &a2a.Message{
					Role:  "agent",
					Parts: []a2a.Part{a2a.NewTextPart("just thinking out loud")},
				},
			},
		}
		if core.cacheAuthStateFromStatus(event, &broker.Message{}) {
			t.Error("expected authPending=false for a non-auth status update")
		}
	})

	t.Run("nil status message returns false", func(t *testing.T) {
		event := &a2a.TaskStatusUpdateEvent{Kind: "status-update", TaskID: "task-3"}
		if core.cacheAuthStateFromStatus(event, &broker.Message{}) {
			t.Error("expected authPending=false when Status.Message is nil")
		}
	})
}

func TestSubmitAuthResponse_OmitsReplyToAndStatusTopic(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.logger = silentLogger()

	queue, err := brk.CreateQueue("test-auth-resp-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	defer func() { _ = brk.DeleteQueue(queue.Name()) }()

	requestTopic := a2a.AgentRequestTopic("test-ns", "agent-x")
	if subErr := brk.Subscribe(queue, requestTopic); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	taskID := "task-auth-resp"
	msg := a2a.Message{
		Role:  "user",
		Parts: []a2a.Part{a2a.NewDataPart(map[string]any{"type": "auth_response"})},
	}
	if submitErr := core.SubmitAuthResponse(context.Background(), "agent-x", taskID, msg, "u1"); submitErr != nil {
		t.Fatalf("SubmitAuthResponse: %v", submitErr)
	}

	recvCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, recvErr := brk.Receive(recvCtx, queue)
	if recvErr != nil {
		t.Fatalf("receive auth_response: %v", recvErr)
	}

	props := a2a.DecodeUserProperties(got.UserProperties)
	if props.ReplyTo != "" {
		t.Errorf("ReplyTo = %q, want empty (legacyAuthSavedResponse must stay silent)", props.ReplyTo)
	}
	if props.A2AStatusTopic != "" {
		t.Errorf("A2AStatusTopic = %q, want empty", props.A2AStatusTopic)
	}

	var req a2a.Request
	if jsonErr := json.Unmarshal(got.Payload, &req); jsonErr != nil {
		t.Fatalf("decode envelope: %v", jsonErr)
	}
	if req.Method != "message/send" {
		t.Errorf("Method = %q, want message/send", req.Method)
	}
}

func TestSubmitAuthResponse_RearmsIdleTimer(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.logger = silentLogger()
	core.cfg.TaskTimeoutSeconds = 60

	taskID := "task-timer"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})

	if _, exists := core.taskTimers.timers[taskID]; exists {
		t.Fatalf("precondition: timer should be absent before SubmitAuthResponse")
	}

	msg := a2a.Message{
		Role:  "user",
		Parts: []a2a.Part{a2a.NewDataPart(map[string]any{"type": "auth_response"})},
	}
	if err := core.SubmitAuthResponse(context.Background(), "agent-x", taskID, msg, "u1"); err != nil {
		t.Fatalf("SubmitAuthResponse: %v", err)
	}

	core.taskTimers.mu.Lock()
	_, armed := core.taskTimers.timers[taskID]
	core.taskTimers.mu.Unlock()
	if !armed {
		t.Error("expected idle timer re-armed after SubmitAuthResponse")
	}

	core.cancelTaskTimeout(taskID)
}

func TestHandleStatusEvent_AuthPendingCancelsTimer(t *testing.T) {
	encodeStatusEvent := func(t *testing.T, taskID string, parts []a2a.Part, final bool, state a2a.TaskState) *broker.Message {
		t.Helper()
		event := a2a.TaskStatusUpdateEvent{
			Kind:   "status-update",
			TaskID: taskID,
			Final:  final,
			Status: a2a.TaskStatus{
				State:   state,
				Message: &a2a.Message{Role: "agent", Parts: parts},
			},
		}
		resp := a2a.Response{Result: &event}
		payload, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal status response: %v", err)
		}
		return &broker.Message{
			Topic:   "test-ns/a2a/v1/gateway/status/test-gw/" + taskID,
			Payload: payload,
		}
	}

	t.Run("auth_required signal cancels timer", func(t *testing.T) {
		core, _, _ := setupTestGateway(t)
		if err := core.Init(context.Background(), nil); err != nil {
			t.Fatalf("Init: %v", err)
		}
		core.logger = silentLogger()
		core.cfg.TaskTimeoutSeconds = 60

		taskID := "task-auth-cancel"
		_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
		core.startTaskTimeout(taskID)

		core.taskTimers.mu.Lock()
		_, armed := core.taskTimers.timers[taskID]
		core.taskTimers.mu.Unlock()
		if !armed {
			t.Fatal("precondition: timer should be armed before handleStatusEvent")
		}

		authData := map[string]any{
			"type":                          "authentication_required",
			"target_agent":                  "agent-x",
			"target_agent_task_id":          taskID,
			"user_id":                       "u1",
			"client_id":                     "test-gw",
			"auth_uri":                      "https://idp.example/authorize?state=STATE-X",
			"auth_request_function_call_id": "call-1",
			"credential_key":                "tool__oauth2",
			"code_verifier":                 "v",
		}
		msg := encodeStatusEvent(t, taskID, []a2a.Part{a2a.NewDataPart(authData)}, false, a2a.TaskStateInputNeeded)

		core.handleStatusEvent(context.Background(), msg)

		core.taskTimers.mu.Lock()
		_, stillArmed := core.taskTimers.timers[taskID]
		core.taskTimers.mu.Unlock()
		if stillArmed {
			t.Error("expected timer canceled when auth_required signal lands; user's browser flow would race the 5-min idle otherwise")
		}
		if taskWaiting(core, taskID) {
			t.Error("auth pause must cancel without recording a wait: a recorded wait makes SubmitAuthResponse's re-arm a no-op, leaving the task with no idle/hard timeout after auth")
		}
	})

	t.Run("plain status update keeps timer armed", func(t *testing.T) {
		core, _, _ := setupTestGateway(t)
		if err := core.Init(context.Background(), nil); err != nil {
			t.Fatalf("Init: %v", err)
		}
		core.logger = silentLogger()
		core.cfg.TaskTimeoutSeconds = 60

		taskID := "task-plain"
		_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})

		msg := encodeStatusEvent(t, taskID, []a2a.Part{a2a.NewTextPart("thinking")}, false, "")
		core.handleStatusEvent(context.Background(), msg)

		core.taskTimers.mu.Lock()
		_, armed := core.taskTimers.timers[taskID]
		core.taskTimers.mu.Unlock()
		if !armed {
			t.Error("expected timer armed after a non-auth status update")
		}
		core.cancelTaskTimeout(taskID)
	})
}

func TestAuthPauseThenResume_ReArmsTimer(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.logger = silentLogger()
	core.cfg.TaskTimeoutSeconds = 60

	taskID := "task-auth-resume"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
	core.startTaskTimeout(taskID)

	authData := map[string]any{
		"type":                          "authentication_required",
		"target_agent":                  "agent-x",
		"target_agent_task_id":          taskID,
		"user_id":                       "u1",
		"client_id":                     "test-gw",
		"auth_uri":                      "https://idp.example/authorize?state=STATE-RESUME",
		"auth_request_function_call_id": "call-1",
		"credential_key":                "tool__oauth2",
		"code_verifier":                 "v",
	}
	event := a2a.TaskStatusUpdateEvent{
		Kind:   "status-update",
		TaskID: taskID,
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateInputNeeded,
			Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewDataPart(authData)}},
		},
	}
	payload, err := json.Marshal(a2a.Response{Result: &event})
	if err != nil {
		t.Fatalf("marshal auth status: %v", err)
	}
	core.handleStatusEvent(context.Background(), &broker.Message{
		Topic:   "test-ns/a2a/v1/gateway/status/test-gw/" + taskID,
		Payload: payload,
	})

	respMsg := a2a.Message{
		Role:  "user",
		Parts: []a2a.Part{a2a.NewDataPart(map[string]any{"type": "auth_response"})},
	}
	if err := core.SubmitAuthResponse(context.Background(), "agent-x", taskID, respMsg, "u1"); err != nil {
		t.Fatalf("SubmitAuthResponse: %v", err)
	}

	core.taskTimers.mu.Lock()
	_, armed := core.taskTimers.timers[taskID]
	core.taskTimers.mu.Unlock()
	if taskWaiting(core, taskID) {
		t.Error("task recorded as waiting after SubmitAuthResponse; the auth pause must not record a HIL wait")
	}
	if !armed {
		t.Error("expected idle timer re-armed after auth resume; a recorded wait leaves the task with no timeout backstop")
	}
	core.cancelTaskTimeout(taskID)
}

type deadlineBroker struct{ *broker.DevBroker }

func (b deadlineBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.DevBroker.PublishGuaranteed(ctx, topic, msg)
}

func TestHardTimeoutCancelsAtTheAgentAfterTheCallbackDeadline(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	core.SetServices(runtime.NewRuntime(deadlineBroker{brk}, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.logger = silentLogger()
	core.cfg.TaskTimeoutSeconds = 60
	core.eventBindActive.Store(true)

	queue, err := brk.CreateQueue("test-timeout-cancel-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	defer func() { _ = brk.DeleteQueue(queue.Name()) }()
	if subErr := brk.Subscribe(queue, a2a.AgentRequestTopic("test-ns", "agent-x")); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	taskID := "task-timeout-cancel"
	_ = core.tasks.Register(taskID, "agent-x", "u1", TaskIdentity{})
	expired, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	core.onTaskHardTimeout(expired, taskID, 60)

	recvCtx, cancelRecv := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRecv()
	got, recvErr := brk.Receive(recvCtx, queue)
	if recvErr != nil {
		t.Fatalf("the cancel must reach the agent: %v", recvErr)
	}
	var req a2a.Request
	if jsonErr := json.Unmarshal(got.Payload, &req); jsonErr != nil {
		t.Fatalf("decode envelope: %v", jsonErr)
	}
	if req.Method != "tasks/cancel" {
		t.Errorf("Method = %q, want tasks/cancel", req.Method)
	}
}
