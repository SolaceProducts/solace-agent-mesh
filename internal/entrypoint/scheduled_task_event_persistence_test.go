// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func setupTaskLoggingCore(t *testing.T) (*GatewayCore, broker.Broker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{"enabled": true},
		"task_logging": map[string]any{
			"enabled":            true,
			"log_status_updates": true,
		},
	}
	core := NewGatewayCore(&recordingAdapter{}, appConfig)
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "test-agent"})
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return core, brk
}

type stubObservation struct {
	taskID      string
	statusTopic string
	replyTo     string
	err         error
}

func startStubAgent(t *testing.T, brk broker.Broker) <-chan stubObservation {
	t.Helper()
	queue, err := brk.CreateQueue("stub-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test-ns", "test-agent")); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}

	observed := make(chan stubObservation, 2)
	recvCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	go func() {
		fail := func(err error) {
			select {
			case observed <- stubObservation{err: err}:
			default:
			}
		}
		msg, err := brk.Receive(recvCtx, queue)
		if err != nil {
			fail(fmt.Errorf("receive submission: %w", err))
			return
		}
		if msg == nil {
			fail(errors.New("receive submission: no message delivered"))
			return
		}
		var req a2a.Request
		if unmarshalErr := json.Unmarshal(msg.Payload, &req); unmarshalErr != nil {
			fail(fmt.Errorf("unmarshal submission: %w", unmarshalErr))
			return
		}
		taskID := req.ID
		contextID := ""
		if params, ok := req.Params.(map[string]any); ok {
			if m, ok := params["message"].(map[string]any); ok {
				contextID, _ = m["contextId"].(string)
			}
		}

		props := a2a.DecodeUserProperties(msg.UserProperties)
		observed <- stubObservation{taskID: taskID, statusTopic: props.A2AStatusTopic, replyTo: props.ReplyTo}
		if props.A2AStatusTopic == "" || props.ReplyTo == "" {
			return
		}

		publishStatus := func(data any) {
			ev := a2a.NewStatusUpdateEvent(taskID, contextID, []a2a.Part{a2a.NewDataPart(data)}, false, nil)
			payload, marshalErr := json.Marshal(a2a.NewResponse(taskID, ev))
			if marshalErr != nil {
				fail(fmt.Errorf("marshal status event: %w", marshalErr))
				return
			}
			if pubErr := brk.Publish(context.Background(), props.A2AStatusTopic, &broker.Message{
				Topic:   props.A2AStatusTopic,
				Payload: payload,
			}); pubErr != nil {
				fail(fmt.Errorf("publish status event: %w", pubErr))
			}
		}

		publishStatus(a2a.ToolInvocationStartData{
			Type:           string(a2a.SignalToolInvocationStart),
			ToolName:       "scheduled_probe_tool",
			ToolArgs:       map[string]any{"query": "nightly"},
			FunctionCallID: "fc-1",
		})
		publishStatus(a2a.ToolResultData{
			Type:           string(a2a.SignalToolResult),
			ToolName:       "scheduled_probe_tool",
			ResultData:     "probe ok",
			FunctionCallID: "fc-1",
		})

		final := a2a.Task{
			ID:        taskID,
			ContextID: contextID,
			Kind:      "task",
			Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		}
		payload, err := json.Marshal(a2a.NewResponse(taskID, final))
		if err != nil {
			fail(fmt.Errorf("marshal terminal response: %w", err))
			return
		}
		if pubErr := brk.Publish(context.Background(), props.ReplyTo, &broker.Message{
			Topic:   props.ReplyTo,
			Payload: payload,
		}); pubErr != nil {
			fail(fmt.Errorf("publish terminal response: %w", pubErr))
		}
	}()
	return observed
}

func assertStubHealthy(t *testing.T, observed <-chan stubObservation) {
	t.Helper()
	select {
	case obs := <-observed:
		if obs.err != nil {
			t.Fatalf("stub agent: %v", obs.err)
		}
	default:
	}
}

func awaitStubAgent(t *testing.T, observed <-chan stubObservation) stubObservation {
	t.Helper()
	select {
	case obs := <-observed:
		if obs.err != nil {
			t.Fatalf("stub agent: %v", obs.err)
		}
		if obs.statusTopic == "" || obs.replyTo == "" {
			t.Fatalf("submission carried no reply addresses (statusTopic=%q replyTo=%q); "+
				"the agent would have nowhere to publish status to", obs.statusTopic, obs.replyTo)
		}
		return obs
	case <-time.After(5 * time.Second):
		t.Fatal("stub agent never observed the submission")
		return stubObservation{}
	}
}

func awaitTaskEvents(t *testing.T, repo *store.TaskEventRepo, taskID string, want int) []*store.TaskEvent {
	t.Helper()
	var events []*store.TaskEvent
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := repo.GetByTaskID(t.Context(), taskID)
		if err != nil {
			t.Fatalf("read task_events: %v", err)
		}
		events = got
		if len(events) >= want {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	return events
}

func assertRunIsRecoverable(t *testing.T, events []*store.TaskEvent) {
	t.Helper()
	directions := map[string]int{}
	toolCallRecovered := false
	for _, e := range events {
		directions[e.Direction]++
		if e.Direction == "status" &&
			strings.Contains(e.Payload, string(a2a.SignalToolInvocationStart)) &&
			strings.Contains(e.Payload, "scheduled_probe_tool") {
			toolCallRecovered = true
		}
	}
	t.Logf("task_events rows=%d directions=%v toolCallRecovered=%v", len(events), directions, toolCallRecovered)

	for _, direction := range []string{"request", "status", "response"} {
		if directions[direction] == 0 {
			t.Errorf("task_events has no %q rows (rows=%d directions=%v)", direction, len(events), directions)
		}
	}
	if !toolCallRecovered {
		t.Errorf("no status row carries the run's tool invocation; an unattended run leaves no "+
			"recoverable trace of what it did (rows=%d directions=%v)", len(events), directions)
	}
}

func TestScheduledRun_EventPersistence(t *testing.T) {
	core, brk := setupTaskLoggingCore(t)
	svc := core.ScheduledTasks()
	if svc == nil {
		t.Fatal("ScheduledTasks() returned nil")
	}

	observed := startStubAgent(t, brk)

	task := makeIdentityTestTask(idgen.New(), "alice@example.com")
	due := time.Now().Add(-time.Second).UnixMilli()
	task.NextRunAt = &due
	insertTask(t, svc, task)

	if err := svc.RunPasses(t.Context()); err != nil {
		t.Fatalf("RunPasses: %v", err)
	}

	obs := awaitStubAgent(t, observed)
	events := awaitTaskEvents(t, store.NewTaskEventRepo(core.Store()), obs.taskID, 4)
	assertStubHealthy(t, observed)
	assertRunIsRecoverable(t, events)
}

func TestSessionlessSubmission_EventPersistence(t *testing.T) {
	core, brk := setupTaskLoggingCore(t)

	observed := startStubAgent(t, brk)

	a2aTaskID := idgen.New()
	msg := a2a.Message{Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("run the nightly report")}}
	if err := core.SubmitTask(t.Context(), "test-agent", a2aTaskID, msg, auth.ForUserIDOnly("system:webhook")); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	obs := awaitStubAgent(t, observed)
	if obs.taskID != a2aTaskID {
		t.Fatalf("stub agent observed task %q, want %q", obs.taskID, a2aTaskID)
	}

	events := awaitTaskEvents(t, store.NewTaskEventRepo(core.Store()), a2aTaskID, 4)
	assertStubHealthy(t, observed)
	assertRunIsRecoverable(t, events)
}
