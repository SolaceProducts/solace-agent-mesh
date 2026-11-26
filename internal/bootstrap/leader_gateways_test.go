// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const twoGWENamespace = "two-gwe"

type gwe struct {
	rt      *runtime.Runtime
	elector *leader.Elector
}

func (g *gwe) leads() bool {
	_, leading := g.elector.Leading()
	return leading
}

func startGWE(t *testing.T, brokerAddr, id, dbURL string) *gwe {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	client := broker.NewNetworkDevBroker(brokerAddr, id, id, logger)
	if err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	rt := runtime.NewRuntime(client, nil, runtime.WithLogger(logger))
	exe := entrypoint.NewGatewayExecutor(rt.Services(), func(appConfig map[string]any, svc runtime.Services) (*entrypoint.GatewayCore, error) {
		core := entrypoint.NewGatewayCore(&fakeAdapter{}, appConfig)
		core.SetServices(svc)
		return core, nil
	})
	if err := exe.LoadConfig("webui", map[string]any{
		"namespace":       twoGWENamespace,
		"gateway_id":      "webui",
		"session_service": map[string]any{"type": "sql", "database_url": dbURL},
		"data_retention": map[string]any{
			"enabled":                     true,
			"cleanup_sse_events":          true,
			"sse_event_retention_seconds": 1,
			"cleanup_interval_seconds":    1,
		},
	}); err != nil {
		t.Fatal(err)
	}
	rt.Register("gateway", exe)
	elector := NewLeader(rt.Services(), twoGWENamespace, leader.RoleGateway, logger)
	if err := RegisterLeader(rt, elector); err != nil {
		t.Fatal(err)
	}
	if err := rt.Init(t.Context()); err != nil {
		t.Fatalf("init %s: %v", id, err)
	}
	if err := rt.Start(t.Context()); err != nil {
		t.Fatalf("start %s: %v", id, err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })
	return &gwe{rt: rt, elector: elector}
}

func agedSSEEvent(t *testing.T, db *store.DB, taskID string) {
	t.Helper()
	if err := store.NewSSEEventRepo(db).Append(t.Context(), &store.SSEEventBufferRow{
		TaskID: taskID, SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE sse_event_buffer SET created_at = ? WHERE task_id = ?`,
		time.Now().Add(-10*time.Second).UnixMilli(), taskID); err != nil {
		t.Fatal(err)
	}
}

func swept(t *testing.T, db *store.DB, taskID string) func() bool {
	return func() bool {
		rows, err := store.NewSSEEventRepo(db).GetByTask(t.Context(), taskID, 1)
		return err == nil && len(rows) == 0
	}
}

func within(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTwoGWEsSweepOnceAndTheStandbyTakesOverWhenTheLeaderStops(t *testing.T) {
	server := broker.NewDevBrokerServer(nil, slog.New(slog.DiscardHandler))
	addr, err := server.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	dbURL := store.TestDBURL(t)
	db, err := store.Open(dbURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	first := startGWE(t, addr, "gwe-a", dbURL)
	within(t, "the first GWE to lead", first.leads)
	second := startGWE(t, addr, "gwe-b", dbURL)
	if second.leads() {
		t.Fatal("both GWEs lead")
	}

	agedSSEEvent(t, db, "10000000-0000-0000-0000-000000000001")
	within(t, "the leader to sweep the aged event", swept(t, db, "10000000-0000-0000-0000-000000000001"))

	if err := first.rt.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	within(t, "the standby to lead once the leader stops", second.leads)
	agedSSEEvent(t, db, "10000000-0000-0000-0000-000000000002")
	within(t, "the new leader to sweep the aged event", swept(t, db, "10000000-0000-0000-0000-000000000002"))
}
