// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const b01TraceID = "01890000-0000-7000-8000-0000000000bb"

type b01Case struct {
	name  string
	topic func(taskID string) string
	body  func(taskID string) map[string]any
}

func b01Cases() []b01Case {
	gatewayTopic := func(kind string) func(string) string {
		return func(taskID string) string { return "test-ns/a2a/v1/gateway/" + kind + "/gw-test/" + taskID }
	}
	rpcError := func(taskID string) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": taskID, "error": map[string]any{"code": -32000, "message": "boom"}}
	}
	return []b01Case{
		{
			name:  "request",
			topic: func(string) string { return "test-ns/a2a/v1/agent/request/my-agent" },
			body: func(taskID string) map[string]any {
				return map[string]any{"jsonrpc": "2.0", "id": taskID, "method": "message/send",
					"params": map[string]any{"message": map[string]any{"parts": []any{}}}}
			},
		},
		{
			name:  "response",
			topic: gatewayTopic("response"),
			body: func(taskID string) map[string]any {
				return map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "task", "id": taskID, "status": map[string]any{"state": "completed"}}}
			},
		},
		{name: "response_error", topic: gatewayTopic("response"), body: rpcError},
		{
			name:  "status",
			topic: gatewayTopic("status"),
			body: func(taskID string) map[string]any {
				return map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "status-update", "taskId": taskID}}
			},
		},
		{name: "status_error", topic: gatewayTopic("status"), body: rpcError},
	}
}

func TestB01_TaskLoggerStampsTraceIDOnEveryBranch(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true, QueueSize: 100}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	type sent struct {
		taskID string
		want   *string
	}
	var expectations []sent
	for _, tc := range b01Cases() {
		for _, withTrace := range []bool{true, false} {
			taskID := tid("b01-" + tc.name)
			props := a2a.UserProperties{UserID: "u1"}
			var want *string
			if withTrace {
				props.TraceID = b01TraceID
				want = new(b01TraceID)
			} else {
				taskID = tid("b01-untraced-" + tc.name)
			}
			payload, err := json.Marshal(tc.body(taskID))
			if err != nil {
				t.Fatal(err)
			}
			svc.handleBrokerMessage(&broker.Message{Topic: tc.topic(taskID), Payload: payload, UserProperties: a2a.EncodeUserProperties(&props)})
			expectations = append(expectations, sent{taskID: taskID, want: want})
		}
	}
	svc.Stop()

	for i, exp := range expectations {
		tc := b01Cases()[i/2]
		name := "B01_" + tc.name + "_with_trace"
		if exp.want == nil {
			name = "B01_" + tc.name + "_without_trace_is_null"
		}
		t.Run(name, func(t *testing.T) {
			events, err := eventRepo.GetByTaskID(t.Context(), exp.taskID)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 {
				t.Fatalf("got %d events for task, want 1", len(events))
			}
			got := events[0]
			if got.TaskID != exp.taskID {
				t.Fatalf("task_id = %s, want %s", got.TaskID, exp.taskID)
			}
			switch {
			case exp.want == nil && got.TraceID != nil:
				t.Fatalf("trace_id = %q, want NULL", *got.TraceID)
			case exp.want != nil && (got.TraceID == nil || *got.TraceID != *exp.want):
				t.Fatalf("trace_id = %v, want %q", got.TraceID, *exp.want)
			}
		})
	}
}

func TestB01_TaskLoggerParentAndDelegatedChildShareTraceID(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{Enabled: true, LogStatusUpdates: true, QueueSize: 100}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	parent, child := tid("b01-parent"), tid("b01-child")
	props := a2a.EncodeUserProperties(&a2a.UserProperties{UserID: "u1", TraceID: b01TraceID})
	for _, req := range []struct {
		taskID   string
		metadata map[string]any
	}{
		{parent, map[string]any{}},
		{child, map[string]any{"parentTaskId": parent}},
	} {
		payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.taskID, "method": "message/send",
			"params": map[string]any{"message": map[string]any{"parts": []any{}, "metadata": req.metadata}}})
		if err != nil {
			t.Fatal(err)
		}
		svc.handleBrokerMessage(&broker.Message{Topic: "test-ns/a2a/v1/agent/request/peer", Payload: payload, UserProperties: props})
	}
	svc.Stop()

	events, err := eventRepo.GetByTaskIDs(t.Context(), []string{parent, child})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		if e.TraceID == nil || *e.TraceID != b01TraceID {
			t.Fatalf("event for task %s trace_id = %v, want %q", e.TaskID, e.TraceID, b01TraceID)
		}
		seen[e.TaskID] = true
	}
	if !seen[parent] || !seen[child] || len(seen) != 2 {
		t.Fatalf("task ids = %v, want distinct parent %s and child %s", seen, parent, child)
	}
	childTask, err := taskRepo.GetByID(t.Context(), child)
	if err != nil || childTask == nil {
		t.Fatalf("child task row: %v, %v", childTask, err)
	}
	if childTask.ParentTaskID == nil || *childTask.ParentTaskID != parent {
		t.Fatalf("child parent_task_id = %v, want %s", childTask.ParentTaskID, parent)
	}
}

func TestB01_BuildTaskEventMapsEmptyTraceIDToNull(t *testing.T) {
	if got := buildTaskEvent(TaskLogEvent{TaskID: tid("b01-build"), Direction: "status"}, 1).TraceID; got != nil {
		t.Fatalf("empty trace id built as %q, want nil", *got)
	}
	got := buildTaskEvent(TaskLogEvent{TaskID: tid("b01-build"), Direction: "status", TraceID: b01TraceID}, 1).TraceID
	if got == nil || *got != b01TraceID {
		t.Fatalf("trace id built as %v, want %q", got, b01TraceID)
	}
}
