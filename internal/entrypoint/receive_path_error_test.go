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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestHandleStatusEvent_MalformedDropped(t *testing.T) {
	core, _, adapter := setupTestGateway(t)
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)
	ctx := context.Background()
	topic := a2a.GatewayStatusTopic("test-ns", "test-gw", "t")
	send := func(payload string) {
		core.handleStatusEvent(ctx, &broker.Message{Topic: topic, Payload: []byte(payload)})
	}

	send(`{bad json`)
	send(`{"jsonrpc":"2.0","id":"t"}`)
	send(`{"jsonrpc":"2.0","id":"t","result":[1,2,3]}`)
	send(`{"jsonrpc":"2.0","id":"t","result":{"kind":"bogus"}}`)
	send(`{"jsonrpc":"2.0","id":"t","result":{"kind":"status-update","status":123}}`)
	send(`{"jsonrpc":"2.0","id":"t","result":{"kind":"artifact-update","artifact":123}}`)

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.statusUpdates) != 0 || len(adapter.artifactUpdates) != 0 {
		t.Errorf("malformed status events must not dispatch: status=%d artifact=%d",
			len(adapter.statusUpdates), len(adapter.artifactUpdates))
	}
}

func TestHandleResponse_MalformedAndAbsentResult(t *testing.T) {
	core, _, adapter := setupTestGateway(t)
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)
	ctx := context.Background()
	send := func(taskID, payload string) {
		topic := a2a.GatewayResponseTopic("test-ns", "test-gw", taskID)
		core.handleResponse(ctx, &broker.Message{Topic: topic, Payload: []byte(payload)})
	}

	send("t1", `{bad json`)
	send("t2", `{"jsonrpc":"2.0","id":"t","result":[1,2,3]}`)
	send("t3", `{"jsonrpc":"2.0","id":"t","error":{"code":1,"message":"boom"}}`)
	send("t4", `{"jsonrpc":"2.0","id":"t"}`)

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.errors) != 1 {
		t.Errorf("expected 1 error dispatch, got %d", len(adapter.errors))
	}
	if len(adapter.finalResponses) != 1 {
		t.Errorf("expected 1 final response (absent-result zero Task), got %d", len(adapter.finalResponses))
	}
}

func TestTaskLogger_HandleMessages_EdgeCases(t *testing.T) {
	cfg := &TaskLoggerConfig{Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true, QueueSize: 256}
	s := NewTaskLoggerService(cfg, nil, nil, nil, "test-ns", "test-gw", slog.New(slog.DiscardHandler))

	taskID := tid("tl-task")
	ctxID := tid("tl-ctx")
	statusTopic := a2a.GatewayStatusTopic("test-ns", "test-gw", taskID)
	respTopic := a2a.GatewayResponseTopic("test-ns", "test-gw", taskID)
	up := a2a.UserProperties{}

	statusPayload, _ := json.Marshal(a2a.NewResponse(taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: ctxID, Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("hi")}}},
	}))
	respPayload, _ := json.Marshal(a2a.NewResponse(taskID, &a2a.Task{
		Kind: "task", ContextID: ctxID, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}))

	s.handleStatusMessage(&broker.Message{Topic: statusTopic, Payload: statusPayload}, statusTopic, up)
	s.handleStatusMessage(&broker.Message{Topic: statusTopic, Payload: []byte("{bad")}, statusTopic, up)
	s.handleStatusMessage(&broker.Message{Topic: statusTopic, Payload: []byte(`{"jsonrpc":"2.0","id":"t","result":[1]}`)}, statusTopic, up)
	s.handleStatusMessage(&broker.Message{Topic: statusTopic, Payload: []byte(`{"jsonrpc":"2.0","id":"t","error":{"code":1,"message":"x"}}`)}, statusTopic, up)

	s.handleResponseMessage(&broker.Message{Topic: respTopic, Payload: respPayload}, respTopic, up)
	s.handleResponseMessage(&broker.Message{Topic: respTopic, Payload: []byte(`{"jsonrpc":"2.0","id":"t","error":{"code":2,"message":"y"}}`)}, respTopic, up)
	s.handleResponseMessage(&broker.Message{Topic: respTopic, Payload: []byte(`{"jsonrpc":"2.0","id":"t","result":[1]}`)}, respTopic, up)

	got := drainTaskLogEvents(s.ch)
	dirs := map[string]int{}
	for _, e := range got {
		dirs[e.Direction]++
	}
	if dirs["status"] != 1 || dirs["response"] != 1 || dirs["error"] != 2 {
		t.Errorf("enqueued directions = %v, want status:1 response:1 error:2", dirs)
	}
	for _, e := range got {
		switch e.Direction {
		case "status":
			if e.EventKind != "status-update" {
				t.Errorf("status event kind = %q, want status-update", e.EventKind)
			}
		case "response":
			if e.FinalStatus != "completed" {
				t.Errorf("response FinalStatus = %q, want completed", e.FinalStatus)
			}
		}
	}
}

func drainTaskLogEvents(ch <-chan TaskLogEvent) []TaskLogEvent {
	var out []TaskLogEvent
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}
