// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const triggerTestQueue = "test-ns/q/gdk/task_log/gw-test"

func startTriggerTestLogger(t *testing.T) (*TaskLoggerService, *mockBroker, *store.TaskRepo, context.CancelFunc) {
	t.Helper()
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	brk := newMockBroker()
	cfg := &TaskLoggerConfig{Enabled: true, LogStatusUpdates: true, QueueSize: 100}
	svc := NewTaskLoggerService(cfg, taskRepo, store.NewTaskEventRepo(db), brk, "test-ns", "gw-test", slog.Default())
	ctx, cancel := context.WithCancel(t.Context())
	if err := svc.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	return svc, brk, taskRepo, cancel
}

func triggerRequestMessage(taskID, trigger string) *broker.Message {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": taskID, "method": "message/send",
		"params": map[string]any{"message": map[string]any{
			"kind": "message", "role": "user", "messageId": "m-" + taskID,
			"parts": []any{map[string]any{"kind": "text", "text": "hello"}},
		}},
	})
	props := map[string]string{a2a.PropUserID: "system:eval"}
	a2a.AddTrigger(props, trigger)
	return &broker.Message{Topic: "test-ns/a2a/v1/agent/request/my-agent", Payload: payload, UserProperties: props}
}

func triggerStatusMessage(taskID string) *broker.Message {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": taskID,
		"result": map[string]any{"kind": "status-update", "taskId": taskID, "status": map[string]any{"state": "working"}},
	})
	props := map[string]string{a2a.PropUserID: "system:eval"}
	return &broker.Message{Topic: "test-ns/a2a/v1/gateway/status/gw-test/" + taskID, Payload: payload, UserProperties: props}
}

func waitForTaskTrigger(t *testing.T, repo *store.TaskRepo, taskID string) *string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if task, _ := repo.GetByID(context.Background(), taskID); task != nil && task.TriggerKind != nil {
			return task.TriggerKind
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, err := repo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatalf("task %s was never created", taskID)
	}
	return task.TriggerKind
}

func TestTaskLoggerService_RecordsTheRequestTrigger(t *testing.T) {
	svc, brk, repo, cancel := startTriggerTestLogger(t)
	defer cancel()
	taskID := tid("trigger-eval-task")
	brk.SendToQueue(triggerTestQueue, triggerRequestMessage(taskID, a2a.TriggerEval))

	got := waitForTaskTrigger(t, repo, taskID)
	cancel()
	svc.Stop()
	if got == nil || *got != a2a.TriggerEval {
		t.Errorf("trigger_kind = %v, want %q", got, a2a.TriggerEval)
	}
}

func TestTaskLoggerService_PlaceholderTakesTheRequestTrigger(t *testing.T) {
	svc, brk, repo, cancel := startTriggerTestLogger(t)
	defer cancel()
	taskID := tid("trigger-placeholder-task")
	brk.SendToQueue(triggerTestQueue, triggerStatusMessage(taskID))
	deadline := time.Now().Add(2 * time.Second)
	var placeholder *store.Task
	for time.Now().Before(deadline) {
		if placeholder, _ = repo.GetByID(context.Background(), taskID); placeholder != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if placeholder == nil || placeholder.HandlerName != nil {
		t.Fatalf("placeholder = %+v, want an unclassified placeholder before the request", placeholder)
	}
	if placeholder.TriggerKind != nil {
		t.Fatalf("placeholder trigger = %q, want none until the request is logged", *placeholder.TriggerKind)
	}
	brk.SendToQueue(triggerTestQueue, triggerRequestMessage(taskID, a2a.TriggerEval))

	got := waitForTaskTrigger(t, repo, taskID)
	cancel()
	svc.Stop()
	if got == nil || *got != a2a.TriggerEval {
		t.Errorf("trigger_kind = %v, want %q backfilled from the request", got, a2a.TriggerEval)
	}
}

const webhookEntrypointUUID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42"

func webhookRequestMessage(taskID string) *broker.Message {
	msg := triggerRequestMessage(taskID, a2a.TriggerEvent)
	msg.UserProperties[a2a.PropClientID] = "github-hook"
	a2a.AddOrigin(msg.UserProperties, a2a.Origin{Channel: "webhook", EntrypointUUID: webhookEntrypointUUID})
	msg.UserProperties[a2a.PropTriggerRoute] = "pull-requests"
	msg.UserProperties[a2a.PropTriggerEvent] = "check_suite.completed"
	msg.UserProperties[a2a.PropTriggerDeliveryID] = "72d3162e-cc78-11e3-81ab-4c9367dc0958"
	return msg
}

func assertWebhookOrigin(t *testing.T, task *store.Task) {
	t.Helper()
	columns := []struct {
		name string
		got  *string
		want string
	}{
		{"channel", task.Channel, "webhook"},
		{"entrypoint_id", task.EntrypointID, webhookEntrypointUUID},
		{"entrypoint_name", task.EntrypointName, "github-hook"},
	}
	for _, c := range columns {
		if c.got == nil || *c.got != c.want {
			t.Errorf("%s = %v, want %q", c.name, c.got, c.want)
		}
	}
	want := store.TaskTriggerSource{Route: "pull-requests", Event: "check_suite.completed", DeliveryID: "72d3162e-cc78-11e3-81ab-4c9367dc0958"}
	if got := task.DecodedTriggerSource(); got != want {
		t.Errorf("trigger_source = %+v, want %+v", got, want)
	}
}

func TestTaskLoggerService_IgnoresATriggerSourceOnADelegatedHop(t *testing.T) {
	svc, brk, repo, cancel := startTriggerTestLogger(t)
	defer cancel()
	taskID := tid("trigger-origin-peer-task")
	msg := webhookRequestMessage(taskID)
	msg.UserProperties[a2a.PropDelegatingAgentName] = "orchestrator"
	brk.SendToQueue(triggerTestQueue, msg)

	waitForTaskTrigger(t, repo, taskID)
	cancel()
	svc.Stop()
	task, err := repo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.TriggerSource != nil {
		t.Errorf("trigger_source = %q, want NULL on a delegated hop", *task.TriggerSource)
	}
	if task.Channel == nil || *task.Channel != "webhook" {
		t.Errorf("channel = %v, want the forwarded origin kept", task.Channel)
	}
}

func TestTaskLoggerService_RecordsTheRequestOrigin(t *testing.T) {
	svc, brk, repo, cancel := startTriggerTestLogger(t)
	defer cancel()
	taskID := tid("trigger-origin-task")
	brk.SendToQueue(triggerTestQueue, webhookRequestMessage(taskID))

	waitForTaskTrigger(t, repo, taskID)
	cancel()
	svc.Stop()
	task, err := repo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	assertWebhookOrigin(t, task)
}

func TestTaskLoggerService_PlaceholderTakesTheRequestOrigin(t *testing.T) {
	svc, brk, repo, cancel := startTriggerTestLogger(t)
	defer cancel()
	taskID := tid("trigger-origin-placeholder-task")
	brk.SendToQueue(triggerTestQueue, triggerStatusMessage(taskID))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if placeholder, _ := repo.GetByID(context.Background(), taskID); placeholder != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	brk.SendToQueue(triggerTestQueue, webhookRequestMessage(taskID))

	waitForTaskTrigger(t, repo, taskID)
	cancel()
	svc.Stop()
	task, err := repo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	assertWebhookOrigin(t, task)
}

func TestTaskLoggerService_LeavesTheTriggerUnsetWhenNoneIsStamped(t *testing.T) {
	svc, brk, repo, cancel := startTriggerTestLogger(t)
	defer cancel()
	taskID := tid("trigger-none-task")
	brk.SendToQueue(triggerTestQueue, triggerRequestMessage(taskID, ""))
	deadline := time.Now().Add(2 * time.Second)
	var task *store.Task
	for time.Now().Before(deadline) {
		if task, _ = repo.GetByID(context.Background(), taskID); task != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	svc.Stop()
	if task == nil {
		t.Fatal("task was never created")
	}
	if task.TriggerKind != nil {
		t.Errorf("trigger_kind = %q, want NULL when the request stamps none", *task.TriggerKind)
	}
}
