// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func feedbackPublishTestCore(t *testing.T) *GatewayCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := NewGatewayCore(&LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"feedback_publishing": map[string]any{
			"enabled": true, "include_task_info": "stim",
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return core
}

func seedPublishTestTask(t *testing.T, core *GatewayCore, taskID string) {
	t.Helper()
	agentName := "OrchestratorAgent"
	handlerType := store.TaskHandlerTypeAgent
	task := &store.Task{
		ID: taskID, UserID: "u1", StartTime: 1000, ExecutionMode: "foreground",
		AgentName: &agentName, HandlerName: &agentName, HandlerType: &handlerType,
	}
	if err := store.NewTaskRepo(core.Store()).Create(context.Background(), task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func TestPublishFeedbackEvent_NilEventRepoFallsBackToSummaryAtStimLevel(t *testing.T) {
	core := feedbackPublishTestCore(t)
	taskID := "10000000-0000-0000-0000-000000000001"
	seedPublishTestTask(t, core, taskID)

	brk := core.Services().Broker()
	q, err := brk.CreateQueue("test-nil-eventrepo", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, a2a.FeedbackTopic(core.Config().Namespace)); err != nil {
		t.Fatal(err)
	}

	feedback := &store.Feedback{
		TaskID: taskID, SessionID: "20000000-0000-0000-0000-000000000001",
		UserID: "u1", Rating: "up", ShareContent: true,
		SubjectID: "OrchestratorAgent", SubjectType: store.SubjectTypeAgent,
		EntrypointType: store.EntrypointTypeWebUI, Inserted: true,
	}
	PublishFeedbackEvent(context.Background(), FeedbackPublishParams{
		Core: core, Feedback: feedback, TaskRepo: store.NewTaskRepo(core.Store()),
		EventRepo: nil,
		Logger:    slog.Default(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("expected broker message, got error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal broker payload: %v", err)
	}

	if _, ok := payload["task_stim_data"]; ok {
		t.Errorf("task_stim_data present with a nil EventRepo: %v", payload["task_stim_data"])
	}
	summary, ok := payload["task_summary"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing task_summary fallback: %v", payload)
	}
	if summary["id"] != taskID {
		t.Errorf("task_summary.id = %v, want %q", summary["id"], taskID)
	}
	details, ok := payload["truncation_details"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing truncation_details explaining the stim fallback: %v", payload)
	}
	if details["reason"] != "stim_build_failed" {
		t.Errorf("truncation_details.reason = %v, want stim_build_failed", details["reason"])
	}
}

type blockUntilDoneBroker struct {
	broker.Broker
}

func (b *blockUntilDoneBroker) PublishGuaranteed(ctx context.Context, _ string, _ *broker.Message) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestPublishFeedbackEvent_CallerDeadlineClampsPublishTimeout(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := NewGatewayCore(&LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"feedback_publishing": map[string]any{"enabled": true},
	})
	core.SetServices(runtime.NewRuntime(&blockUntilDoneBroker{Broker: brk}, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	taskID := "30000000-0000-0000-0000-000000000001"
	seedPublishTestTask(t, core, taskID)
	feedback := &store.Feedback{
		TaskID: taskID, SessionID: "40000000-0000-0000-0000-000000000001",
		UserID: "u1", Rating: "up",
		SubjectID: "OrchestratorAgent", SubjectType: store.SubjectTypeAgent,
		EntrypointType: store.EntrypointTypeTeamsBot, Inserted: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	PublishFeedbackEvent(ctx, FeedbackPublishParams{
		Core: core, Feedback: feedback, TaskRepo: store.NewTaskRepo(core.Store()),
		Logger: slog.Default(),
	})
	elapsed := time.Since(start)

	if elapsed > 1*time.Second {
		t.Errorf("PublishFeedbackEvent took %v against a 200ms caller deadline; "+
			"want it bounded near the deadline, not PublishTimeout's 5s default", elapsed)
	}
}

func setPublishLevel(core *GatewayCore, level string, capBytes int) {
	raw := map[string]any{"enabled": true, "include_task_info": level}
	if capBytes > 0 {
		raw["max_payload_size_bytes"] = capBytes
	}
	core.Config().FeedbackPublishingRaw = raw
}

func subscribePublishedFeedback(t *testing.T, core *GatewayCore, queueName string) (broker.Broker, broker.Queue) {
	t.Helper()
	brk := core.Services().Broker()
	q, err := brk.CreateQueue(queueName, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, a2a.FeedbackTopic(core.Config().Namespace)); err != nil {
		t.Fatal(err)
	}
	return brk, q
}

func receivePublishedPayload(t *testing.T, brk broker.Broker, q broker.Queue) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("expected broker message, got error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal broker payload: %v", err)
	}
	return payload
}

func seedPublishTestEvent(t *testing.T, core *GatewayCore, taskID, topic, direction string, createdTime int64, payloadJSON string) {
	t.Helper()
	if err := store.NewTaskEventRepo(core.Store()).Create(context.Background(), &store.TaskEvent{
		ID:          idgen.New(),
		TaskID:      taskID,
		CreatedTime: createdTime,
		Topic:       topic,
		Direction:   direction,
		Payload:     payloadJSON,
	}); err != nil {
		t.Fatalf("seed task event: %v", err)
	}
}

func publishConsented(t *testing.T, core *GatewayCore, taskID string) {
	t.Helper()
	PublishFeedbackEvent(context.Background(), FeedbackPublishParams{
		Core: core,
		Feedback: &store.Feedback{
			TaskID: taskID, SessionID: "20000000-0000-0000-0000-000000000001",
			UserID: "u1", Rating: "up", ShareContent: true,
			SubjectID: "OrchestratorAgent", SubjectType: store.SubjectTypeAgent,
			EntrypointType: store.EntrypointTypeWebUI, Inserted: true,
		},
		TaskRepo:  store.NewTaskRepo(core.Store()),
		EventRepo: store.NewTaskEventRepo(core.Store()),
		Logger:    slog.Default(),
	})
}

func TestPublishFeedbackEvent_SummaryAttachesTaskSummary(t *testing.T) {
	core := feedbackPublishTestCore(t)
	setPublishLevel(core, "summary", 0)
	brk, q := subscribePublishedFeedback(t, core, "test-summary")

	taskID := "10000000-0000-0000-0000-000000000002"
	seedPublishTestTask(t, core, taskID)
	publishConsented(t, core, taskID)

	payload := receivePublishedPayload(t, brk, q)
	summary, ok := payload["task_summary"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing task_summary: %v", payload)
	}
	if summary["id"] != taskID {
		t.Errorf("task_summary.id = %v, want %q", summary["id"], taskID)
	}
	if summary["user_id"] != "u1" {
		t.Errorf("task_summary.user_id = %v, want u1", summary["user_id"])
	}
	if summary["execution_mode"] != "foreground" {
		t.Errorf("task_summary.execution_mode = %v, want foreground", summary["execution_mode"])
	}
	if _, ok := payload["task_stim_data"]; ok {
		t.Errorf("task_stim_data present at level=summary: %v", payload["task_stim_data"])
	}
	if _, ok := payload["truncation_details"]; ok {
		t.Errorf("truncation_details present without stim overflow: %v", payload["truncation_details"])
	}
}

func TestPublishFeedbackEvent_StimAttachesTaskStimData(t *testing.T) {
	core := feedbackPublishTestCore(t)
	setPublishLevel(core, "stim", 0)
	brk, q := subscribePublishedFeedback(t, core, "test-stim")

	taskID := "10000000-0000-0000-0000-000000000003"
	seedPublishTestTask(t, core, taskID)
	seedPublishTestEvent(t, core, taskID, "sam/agent/request/x", "request", 100, `{"params":{"message":{"parts":[{"kind":"text","text":"hi"}]}}}`)
	seedPublishTestEvent(t, core, taskID, "sam/gateway/response/x", "response", 200, `{"result":{"status":{"message":{"parts":[{"kind":"text","text":"hello"}]}}}}`)
	publishConsented(t, core, taskID)

	payload := receivePublishedPayload(t, brk, q)
	stimData, ok := payload["task_stim_data"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing task_stim_data: %v", payload)
	}
	details, ok := stimData["invocation_details"].(map[string]any)
	if !ok {
		t.Fatalf("invocation_details missing or wrong shape: %v", stimData)
	}
	if details["task_id"] != taskID {
		t.Errorf("invocation_details.task_id = %v, want %q", details["task_id"], taskID)
	}
	flow, ok := stimData["invocation_flow"].([]any)
	if !ok {
		t.Fatalf("invocation_flow missing or wrong shape: %v", stimData)
	}
	if len(flow) != 2 {
		t.Fatalf("flow length = %d, want 2", len(flow))
	}
	if _, ok := payload["truncation_details"]; ok {
		t.Errorf("truncation_details present when payload fit under cap: %v", payload["truncation_details"])
	}
}

func TestPublishFeedbackEvent_StimOversizedFallsBackToSummary(t *testing.T) {
	core := feedbackPublishTestCore(t)
	setPublishLevel(core, "stim", 2048)
	brk, q := subscribePublishedFeedback(t, core, "test-stim-oversize")

	taskID := "10000000-0000-0000-0000-000000000004"
	seedPublishTestTask(t, core, taskID)
	bigText := strings.Repeat("x", 4000)
	seedPublishTestEvent(t, core, taskID, "sam/agent/request/x", "request", 100,
		fmt.Sprintf(`{"params":{"message":{"parts":[{"kind":"text","text":%q}]}}}`, bigText))
	publishConsented(t, core, taskID)

	payload := receivePublishedPayload(t, brk, q)
	if _, ok := payload["task_stim_data"]; ok {
		t.Errorf("task_stim_data present after overflow: %v", payload["task_stim_data"])
	}
	if _, ok := payload["task_summary"].(map[string]any); !ok {
		t.Errorf("task_summary missing after fallback: %v", payload["task_summary"])
	}
	truncation, ok := payload["truncation_details"].(map[string]any)
	if !ok {
		t.Fatalf("truncation_details missing: %v", payload)
	}
	if truncation["strategy"] != "fallback_to_summary" {
		t.Errorf("truncation.strategy = %v, want fallback_to_summary", truncation["strategy"])
	}
	if truncation["reason"] != "payload_too_large" {
		t.Errorf("truncation.reason = %v, want payload_too_large", truncation["reason"])
	}
}
