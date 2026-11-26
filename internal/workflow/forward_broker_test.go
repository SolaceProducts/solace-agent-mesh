// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newForwardTestInstance(t *testing.T, up a2a.UserProperties) (*WorkflowInstance, *capturingBroker, string, *WorkflowExecution) {
	t.Helper()
	const (
		parentTaskID = "parent-task"
		subTaskID    = "sub-1"
		nodeID       = "writer"
	)
	brk := &capturingBroker{}
	w := &WorkflowInstance{
		cfg:             &WorkflowConfig{Name: "GreetWorkflow"},
		svc:             &capturingServices{brk: brk},
		logger:          slog.Default(),
		executions:      make(map[string]*WorkflowExecution),
		executionsByID:  make(map[string]*WorkflowExecution),
		subTaskIndex:    make(map[string]string),
		finalizedRoutes: runtime.NewMapCache(),
	}
	state := NewExecutionState(w.cfg.Name, "exec-1")
	state.TrackSubTask(subTaskID, nodeID)
	exec := &WorkflowExecution{
		State:    state,
		Context:  &WorkflowExecutionContext{TaskID: parentTaskID, UserProps: up},
		Executor: &DAGExecutor{state: state},
	}
	w.executions[parentTaskID] = exec
	w.subTaskIndex[subTaskID] = parentTaskID
	return w, brk, subTaskID, exec
}

func TestForwardSubTaskStatus_StampsUserAttribution(t *testing.T) {
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
		ClientID:       "web-ui",
		A2AUserConfig:  `{"model":"general"}`,
		TraceID:        "trace-xyz",
	}
	w, brk, subTaskID, _ := newForwardTestInstance(t, up)

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: mkForwardEvent(t, "workflow_execution_start", "GreetWorkflow", subTaskID),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 forwarded publish, got %d", len(msgs))
	}
	got := msgs[0]
	if got.Topic != up.A2AStatusTopic {
		t.Errorf("forwarded topic = %q, want parent status topic %q", got.Topic, up.A2AStatusTopic)
	}
	if got.UserProperties["userId"] != "alice@example.com" {
		t.Errorf("userId = %q, want alice@example.com (owner attribution for the cross-task feed)", got.UserProperties["userId"])
	}
	if got.UserProperties["clientId"] != "web-ui" {
		t.Errorf("clientId = %q, want web-ui", got.UserProperties["clientId"])
	}
	if got.UserProperties["a2aUserConfig"] != `{"model":"general"}` {
		t.Errorf("a2aUserConfig = %q, want the per-user config", got.UserProperties["a2aUserConfig"])
	}
	if got.UserProperties["traceId"] != "trace-xyz" {
		t.Errorf("traceId = %q, want trace-xyz (log-correlation carried on the forward)", got.UserProperties["traceId"])
	}
}

func TestForwardSubTaskStatus_OmitsEmptyAttribution(t *testing.T) {
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}
	w, brk, subTaskID, _ := newForwardTestInstance(t, up)

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: mkForwardEvent(t, "workflow_execution_start", "GreetWorkflow", subTaskID),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 forwarded publish, got %d", len(msgs))
	}
	if _, ok := msgs[0].UserProperties["clientId"]; ok {
		t.Error("clientId must be omitted when UserProps.ClientID is empty")
	}
	if _, ok := msgs[0].UserProperties["a2aUserConfig"]; ok {
		t.Error("a2aUserConfig must be omitted when UserProps.A2AUserConfig is empty")
	}
}

func TestForwardSubTaskStatus_UnknownSubTaskDropsWithoutPublishButLogsDebug(t *testing.T) {
	var logBuf bytes.Buffer
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}
	w, brk, _, _ := newForwardTestInstance(t, up)
	w.logger = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/never-registered",
		Payload: mkForwardEvent(t, "workflow_execution_start", "GreetWorkflow", "never-registered"),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("unknown sub-task must not publish, got %d", n)
	}
	if !strings.Contains(logBuf.String(), "dropping status for unknown sub-task") {
		t.Errorf("expected a Debug log for the drop, got: %s", logBuf.String())
	}
}

func TestForwardSubTaskStatus_RetriedAwaySubTaskDropsSilently(t *testing.T) {
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}
	w, brk, subTaskID, exec := newForwardTestInstance(t, up)
	nodeID, ok := exec.State.NodeForSubTask(subTaskID)
	if !ok {
		t.Fatal("test fixture must track the sub-task before simulating a retry")
	}
	exec.State.RemoveSubTaskByNode(nodeID)

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: mkForwardEvent(t, "workflow_execution_start", "GreetWorkflow", subTaskID),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("retried-away sub-task must not publish, got %d", n)
	}
}

func TestForwardSubTaskStatus_EmptyStatusTopicDropsSilently(t *testing.T) {
	var logBuf bytes.Buffer
	up := a2a.UserProperties{UserID: "alice@example.com"}
	w, brk, subTaskID, _ := newForwardTestInstance(t, up)
	w.logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: mkForwardEvent(t, "workflow_execution_start", "GreetWorkflow", subTaskID),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("empty parent status topic must not publish, got %d", n)
	}
	if logBuf.Len() != 0 {
		t.Errorf("expected no log for the expected no-SSE-topic case, got: %s", logBuf.String())
	}
}

func TestForwardSubTaskStatus_EmptyParentTaskIDLogsWarn(t *testing.T) {
	var logBuf bytes.Buffer
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}
	w, brk, subTaskID, exec := newForwardTestInstance(t, up)
	exec.Context.TaskID = ""
	w.logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: mkForwardEvent(t, "workflow_execution_start", "GreetWorkflow", subTaskID),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("empty parent task ID must not publish, got %d", n)
	}
	if !strings.Contains(logBuf.String(), "dropping status with malformed route") {
		t.Errorf("expected a Warn log for the malformed route, got: %s", logBuf.String())
	}
}

func TestForwardSubTaskStatus_FilteredEventNotPublished(t *testing.T) {
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}
	w, brk, subTaskID, _ := newForwardTestInstance(t, up)

	env := map[string]any{
		"id":      subTaskID,
		"jsonrpc": "2.0",
		"result": map[string]any{
			"taskId": subTaskID,
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"parts": []any{map[string]any{"kind": "text", "text": "streaming..."}},
				},
			},
		},
	}
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: payload,
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("filtered plain-text event must not publish, got %d", n)
	}
}

func TestForwardSubTaskStatus_UnparseablePayloadLogsWarn(t *testing.T) {
	var logBuf bytes.Buffer
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}
	w, brk, subTaskID, _ := newForwardTestInstance(t, up)
	w.logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: []byte("{not valid json"),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("unparseable payload must not publish, got %d", n)
	}
	if !strings.Contains(logBuf.String(), "dropping status with unparseable payload") {
		t.Errorf("expected a Warn log for the unparseable payload, got: %s", logBuf.String())
	}
}

func TestForwardSubTaskStatus_WorkflowSignalLandsOnParentTopic(t *testing.T) {
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}
	w, brk, subTaskID, _ := newForwardTestInstance(t, up)

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: mkForwardEvent(t, "workflow_node_execution_start", "GreetWorkflow", subTaskID),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 forwarded publish, got %d", len(msgs))
	}
	var env map[string]any
	if err := json.Unmarshal(msgs[0].Payload, &env); err != nil {
		t.Fatal(err)
	}
	if env["result"].(map[string]any)["taskId"] != "parent-task" {
		t.Errorf("result.taskId = %v, want parent-task (rewritten)", env["result"].(map[string]any)["taskId"])
	}
}

func TestForwardSubTaskStatus_SurvivesCleanupGracePeriod(t *testing.T) {
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}
	w, brk, subTaskID, exec := newForwardTestInstance(t, up)

	exec.State.Finalized = true
	w.cleanupIfFinalized(exec)

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: mkForwardArtifactEvent(t, "GreetWorkflow", subTaskID, "draft_cheese-tasters.md"),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	msgs := brk.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 forwarded publish after cleanup, got %d — the terminal node's artifact would be silently dropped", len(msgs))
	}
	if msgs[0].Topic != up.A2AStatusTopic {
		t.Errorf("forwarded topic = %q, want parent status topic %q", msgs[0].Topic, up.A2AStatusTopic)
	}
}

func TestForwardSubTaskStatus_GracePeriodExpires(t *testing.T) {
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}
	w, brk, subTaskID, exec := newForwardTestInstance(t, up)

	w.executionsMu.Lock()
	delete(w.executions, exec.Context.TaskID)
	delete(w.subTaskIndex, subTaskID)
	w.executionsMu.Unlock()
	w.finalizedRoutes.Set(subTaskID, subTaskRoute{
		parentTopic:  up.A2AStatusTopic,
		parentTaskID: exec.Context.TaskID,
	}, runtime.WithTTL(1*time.Millisecond))
	time.Sleep(20 * time.Millisecond)

	msg := &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: mkForwardArtifactEvent(t, "GreetWorkflow", subTaskID, "draft_cheese-tasters.md"),
	}
	w.forwardSubTaskStatus(context.Background(), msg)

	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("expired grace-window entry must not publish, got %d", n)
	}
}

func TestForwardSubTaskStatus_RaceWithCleanup(t *testing.T) {
	up := a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/GreetWorkflow/parent-task",
		UserID:         "alice@example.com",
	}

	for i := 0; i < 500; i++ {
		w, brk, subTaskID, exec := newForwardTestInstance(t, up)
		exec.State.Finalized = true

		msg := &broker.Message{
			Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
			Payload: mkForwardArtifactEvent(t, "GreetWorkflow", subTaskID, "draft_cheese-tasters.md"),
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			w.cleanupIfFinalized(exec)
		}()
		go func() {
			defer wg.Done()
			w.forwardSubTaskStatus(context.Background(), msg)
		}()
		wg.Wait()

		if n := len(brk.snapshot()); n != 1 {
			t.Fatalf("iter %d: expected the forward to land regardless of scheduling order, got %d publishes", i, n)
		}
	}
}
