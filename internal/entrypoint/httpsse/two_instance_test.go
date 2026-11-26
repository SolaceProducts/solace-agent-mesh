// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newWebUIInstances(t *testing.T, n int) []*HTTPSSEAdapter {
	t.Helper()
	dbURL := testDBURL(t, "two-instances")
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatal(err)
	}

	out := make([]*HTTPSSEAdapter, n)
	for i := range out {
		a := NewHTTPSSEAdapter()
		core := entrypoint.NewGatewayCore(a, map[string]any{
			"namespace":             "test-ns",
			"session_secret_key":    "test-secret",
			"gateway_id":            "test-gw",
			"default_user_identity": "sam_dev_user",
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
		t.Cleanup(func() { _ = core.Stop(context.Background()) })
		core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})
		out[i] = a
	}
	return out
}

func TestTwoInstances_StreamFollowsTheInstanceThatWritesTheRows(t *testing.T) {
	inst := newWebUIInstances(t, 2)
	writer, holder := inst[0], inst[1]
	ctx := t.Context()
	taskID, sessionID := idgen.New(), idgen.New()

	holder.sseManager.RegisterTask(taskID, sessionID, "user-1")
	msg := a2a.Message{Kind: "message", MessageID: idgen.New(), Role: a2a.RoleUser, ContextID: sessionID, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
	if err := holder.core.SubmitTask(ctx, "TestAgent", taskID, msg, auth.ForUserIDOnly("user-1")); err != nil {
		t.Fatal(err)
	}
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: holder.sseManager, eventLog: holder.eventLog})
	frames, ended := liveFeed(ctx, t, ts.URL+"/api/v1/sse/subscribe/"+taskID)

	for i := 1; i <= 3; i++ {
		start := time.Now()
		writer.OnStatusUpdate(ctx, taskID, nil, statusPayload(i))
		fr := nextFrame(t, frames, 3*time.Second)
		if fr.id != strconv.Itoa(i) || fr.eventType != "status_update" {
			t.Fatalf("frame = %+v, want status_update %d", fr, i)
		}
		if took := time.Since(start); took > 500*time.Millisecond {
			t.Fatalf("row %d reached the other instance after %v, past what the stream topic gives", i, took)
		}
	}

	writer.OnActivityEvent(ctx, taskID, "test-ns/a2a/v1/agent/status/TestAgent/"+taskID, statusUpdateFixture(taskID, "TestAgent"))
	if fr := nextFrame(t, frames, 3*time.Second); fr.eventType != "activity_event" || fr.id != "" {
		t.Fatalf("frame = %+v, want the writer's activity frame", fr)
	}

	writer.OnFinalResponse(ctx, taskID, nil, finalPayload())
	if fr := nextFrame(t, frames, 3*time.Second); fr.id != "4" || fr.eventType != "final_response" {
		t.Fatalf("frame = %+v, want final_response 4", fr)
	}
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream did not end on the final row")
	}
	waitFor(t, "the entry on the instance that took the question was not freed", func() bool { return !holder.sseManager.HasTask(taskID) })
}
