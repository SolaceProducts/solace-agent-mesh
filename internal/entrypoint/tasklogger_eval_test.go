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
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func TestTaskLogger_RecordsEvalTaskRepliesPublishedOnGatewayTopics(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{Enabled: true, LogStatusUpdates: true, QueueSize: 100}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default())
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	agentQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(agentQueue, a2a.AgentRequestTopic("test-ns", "my-agent")); err != nil {
		t.Fatal(err)
	}

	evalClient := eval.NewBrokerClient(brk, "test-ns", nil, nil)
	if err = evalClient.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer evalClient.Stop()
	runReplies := evalClient.RegisterRun("run-1")

	taskID, err := evalClient.PublishTask(ctx, eval.PublishTaskParams{RunID: "run-1", AgentName: "my-agent", Prompt: "What is 2+2?"})
	if err != nil {
		t.Fatal(err)
	}

	receiveCtx, cancelReceive := context.WithTimeout(ctx, 2*time.Second)
	defer cancelReceive()
	request, err := brk.Receive(receiveCtx, agentQueue)
	if err != nil || request == nil {
		t.Fatalf("agent never received the eval request: msg=%v err=%v", request, err)
	}
	requestProps := a2a.DecodeUserProperties(request.UserProperties)

	statusPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": taskID,
		"result": map[string]any{"kind": "status-update", "taskId": taskID, "contextId": taskID, "status": map[string]any{"state": "working"}},
	})
	if err = brk.Publish(ctx, requestProps.A2AStatusTopic, &broker.Message{Topic: requestProps.A2AStatusTopic, Payload: statusPayload, UserProperties: request.UserProperties}); err != nil {
		t.Fatal(err)
	}
	finalPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": taskID,
		"result": map[string]any{"kind": "task", "id": taskID, "contextId": taskID, "status": map[string]any{"state": "completed"}},
	})
	if err = brk.Publish(ctx, request.ReplyTo, &broker.Message{Topic: request.ReplyTo, Payload: finalPayload, UserProperties: request.UserProperties}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{requestProps.A2AStatusTopic, request.ReplyTo} {
		select {
		case reply := <-runReplies:
			if reply.Topic != want {
				t.Errorf("eval run received reply on %q, want %q", reply.Topic, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("eval run never received the reply on %q", want)
		}
	}

	var task *store.Task
	var events []*store.TaskEvent
	waitForCondition(t, 5*time.Second, func() bool {
		task, _ = taskRepo.GetByID(ctx, taskID)
		events, _ = eventRepo.GetByTaskID(ctx, taskID)
		return task != nil && task.Status != nil && task.EndTime != nil && len(events) >= 3
	}, "task row and events never recorded the eval task's final response")
	if *task.Status != "completed" {
		t.Errorf("task status = %q, want completed", *task.Status)
	}
	directions := make([]string, 0, len(events))
	for _, event := range events {
		directions = append(directions, event.Direction)
	}
	if len(events) != 3 || directions[0] != "request" || directions[1] != "status" || directions[2] != "response" {
		t.Fatalf("task events = %v, want request, status, response", directions)
	}
}
