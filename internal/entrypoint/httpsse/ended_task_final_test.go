// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func startWebUIInstance(t *testing.T, brk broker.Broker, flags *features.Registry, dbURL string) (*HTTPSSEAdapter, func()) {
	t.Helper()
	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"default_user_identity": "sam_dev_user",
		"event_workers":         1,
		"session_service":       map[string]any{"type": "sql", "database_url": dbURL},
	})
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.New(slog.DiscardHandler)),
		runtime.WithFeatureRegistry(flags),
	).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	db := core.Store()
	t.Cleanup(func() { _ = db.Close() })
	if err := core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() { once.Do(func() { _ = core.Stop(context.Background()) }) }
	t.Cleanup(stop)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})
	return adapter, stop
}

func TestFinalPublishedTwice_OneFinalRowInTheEventLog(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatal(err)
	}
	dbURL := testDBURL(t, "final-twice")
	ctx := t.Context()

	submit := func(adapter *HTTPSSEAdapter) (string, string) {
		taskID, sessionID := idgen.New(), idgen.New()
		adapter.sseManager.RegisterTask(taskID, sessionID, "user-1")
		msg := a2a.Message{Kind: "message", MessageID: idgen.New(), Role: a2a.RoleUser, ContextID: sessionID, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
		if err := adapter.core.SubmitTask(ctx, "TestAgent", taskID, msg, auth.ForUserIDOnly("user-1")); err != nil {
			t.Fatal(err)
		}
		return taskID, sessionID
	}
	answer := func(taskID, sessionID string) {
		payload, err := json.Marshal(a2a.NewResponse(taskID, a2a.Task{ID: taskID, ContextID: sessionID, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}))
		if err != nil {
			t.Fatal(err)
		}
		if err := brk.PublishGuaranteed(ctx, a2a.GatewayResponseTopic("test-ns", "test-gw", taskID), &broker.Message{Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	finalRows := func(adapter *HTTPSSEAdapter, taskID string) int {
		events, err := adapter.eventLog.Replay(ctx, taskID, 0)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range events {
			if e.EventType == "final_response" {
				n++
			}
		}
		return n
	}

	first, stopFirst := startWebUIInstance(t, brk, flags, dbURL)
	taskID, sessionID := submit(first)
	answer(taskID, sessionID)
	waitFor(t, "the first final was not stored", func() bool {
		return finalRows(first, taskID) == 1 && !first.core.IsTaskRunning(taskID)
	})
	stopFirst()

	restarted, _ := startWebUIInstance(t, brk, flags, dbURL)
	answer(taskID, sessionID)
	later, laterSession := submit(restarted)
	answer(later, laterSession)
	waitFor(t, "the later task's final was not stored", func() bool { return finalRows(restarted, later) == 1 })

	if n := finalRows(restarted, taskID); n != 1 {
		t.Fatalf("final_response rows = %d, want 1", n)
	}
}
