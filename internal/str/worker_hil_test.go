// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestMaybePublishHILRequest_PendingWithHIL(t *testing.T) {
	t.Parallel()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	statusTopic := "test/status/topic"
	q, err := brk.CreateQueue("hil-sub", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, statusTopic); err != nil {
		t.Fatal(err)
	}

	result := &remotetool.InvokeResult{
		ToolResult: map[string]any{
			"status":  "pending",
			"message": "Waiting for approval...",
			"data": map[string]any{
				"hil_request_id": "hil-123",
				"hil_source":     "test_hil_tool",
				"hil_surface":    `{"type":"slack"}`,
				"hil_expires_at": "2026-01-01T00:00:00Z",
			},
		},
	}
	params := &remotetool.InvokeParams{ParentTaskID: "task-abc"}
	up := &remotetool.UserProperties{
		A2AStatusTopic: statusTopic,
		ContextID:      "ctx-1",
		ClientID:       "agent-1",
		UserID:         "user-1",
	}

	w.maybePublishHILRequest(t.Context(), result, params, up, slog.Default())

	msg, err := brk.Receive(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected message on status topic")
	}

	var resp a2a.Response
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatal(err)
	}

	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	if resultMap["kind"] != "status-update" {
		t.Errorf("kind = %v, want status-update", resultMap["kind"])
	}

	statusMap, _ := resultMap["status"].(map[string]any)
	if statusMap["state"] != "input-required" {
		t.Errorf("state = %v, want input-required", statusMap["state"])
	}
}

func TestMaybePublishHILRequest_NonPending(t *testing.T) {
	t.Parallel()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	result := &remotetool.InvokeResult{
		ToolResult: map[string]any{
			"status":  "success",
			"message": "Done",
		},
	}
	params := &remotetool.InvokeParams{}
	up := &remotetool.UserProperties{A2AStatusTopic: "some/topic"}

	w.maybePublishHILRequest(t.Context(), result, params, up, slog.Default())
}

func TestMaybePublishHILRequest_MissingRequestID(t *testing.T) {
	t.Parallel()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	result := &remotetool.InvokeResult{
		ToolResult: map[string]any{
			"status": "pending",
			"data":   map[string]any{"hil_source": "test_hil_tool"},
		},
	}
	params := &remotetool.InvokeParams{}
	up := &remotetool.UserProperties{A2AStatusTopic: "some/topic"}

	w.maybePublishHILRequest(t.Context(), result, params, up, slog.Default())
}

func TestMaybePublishHILRequest_MissingStatusTopic(t *testing.T) {
	t.Parallel()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	result := &remotetool.InvokeResult{
		ToolResult: map[string]any{
			"status": "pending",
			"data":   map[string]any{"hil_request_id": "hil-123"},
		},
	}
	params := &remotetool.InvokeParams{}
	up := &remotetool.UserProperties{}

	w.maybePublishHILRequest(t.Context(), result, params, up, slog.Default())
}

func TestMaybePublishHILRequest_MissingRequiredFields(t *testing.T) {
	t.Parallel()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	result := &remotetool.InvokeResult{
		ToolResult: map[string]any{
			"status": "pending",
			"data":   map[string]any{"hil_request_id": "hil-123"},
		},
	}

	params := &remotetool.InvokeParams{}
	up := &remotetool.UserProperties{A2AStatusTopic: "some/topic"}

	w.maybePublishHILRequest(t.Context(), result, params, up, slog.Default())
}

func TestMaybePublishHILRequest_InvalidSurfaceJSON(t *testing.T) {
	t.Parallel()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	w := &Worker{
		cfg:    WorkerConfig{Namespace: "test-ns"},
		svc:    svc,
		logger: slog.Default(),
	}

	statusTopic := "test/status/invalid-surface"
	q, err := brk.CreateQueue("hil-invalid-surface", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, statusTopic); err != nil {
		t.Fatal(err)
	}

	result := &remotetool.InvokeResult{
		ToolResult: map[string]any{
			"status":  "pending",
			"message": "Waiting...",
			"data": map[string]any{
				"hil_request_id": "hil-bad-surface",
				"hil_source":     "test_hil_tool",
				"hil_surface":    "not{json",
				"hil_expires_at": "2026-01-01T00:00:00Z",
			},
		},
	}
	params := &remotetool.InvokeParams{ParentTaskID: "task-abc"}
	up := &remotetool.UserProperties{
		A2AStatusTopic: statusTopic,
		ContextID:      "ctx-1",
		ClientID:       "agent-1",
	}

	w.maybePublishHILRequest(t.Context(), result, params, up, slog.Default())

	msg, err := brk.Receive(t.Context(), q)
	if err != nil {
		t.Fatalf("expected message despite invalid surface: %v", err)
	}

	var resp a2a.Response
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatal(err)
	}

	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	if resultMap["kind"] != "status-update" {
		t.Errorf("kind = %v, want status-update", resultMap["kind"])
	}

	statusMap, _ := resultMap["status"].(map[string]any)
	msgMap, _ := statusMap["message"].(map[string]any)
	parts, _ := msgMap["parts"].([]any)
	if len(parts) == 0 {
		t.Fatal("expected at least one part")
	}
	part, _ := parts[0].(map[string]any)
	data, _ := part["data"].(map[string]any)
	if data["surface"] != nil {
		t.Errorf("surface should be nil for invalid JSON, got %v", data["surface"])
	}
}
