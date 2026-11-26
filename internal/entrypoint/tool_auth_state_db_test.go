// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestTakeToolAuthState_DBBacked(t *testing.T) {
	db := store.TestDB(t)
	core, _, _ := setupTestGateway(t)
	core.logger = silentLogger()
	core.db = db
	ctx := context.Background()

	st := &ToolAuthState{
		ToolType:       ToolAuthTypeMCP,
		TargetAgent:    "agent-x",
		TaskID:         "task-1",
		UserID:         "u1",
		ClientID:       "gw",
		FunctionCallID: "call-1",
		CredentialKey:  "tool__oauth2",
		CodeVerifier:   "verifier-1",
	}
	core.putToolAuthState("STATE-DB", st)

	_, cached := core.Services().Cache().Get(toolAuthStateCacheKey("STATE-DB"))
	if wantCached := db.Dialect() == "sqlite"; cached != wantCached {
		t.Errorf("cache seeded = %v, want %v on %s", cached, wantCached, db.Dialect())
	}

	got, ok := core.TakeToolAuthState(ctx, "STATE-DB")
	if !ok {
		t.Fatal("expected to find DB-backed state")
	}
	if got.TargetAgent != "agent-x" || got.TaskID != "task-1" || got.CredentialKey != "tool__oauth2" || got.CodeVerifier != "verifier-1" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	if _, ok := core.Services().Cache().Get(toolAuthStateCacheKey("STATE-DB")); ok {
		t.Error("successful DB Take must drop the in-process cache copy")
	}

	if _, ok := core.TakeToolAuthState(ctx, "STATE-DB"); ok {
		t.Error("second Take should miss (state consumed)")
	}
}

func TestTakeToolAuthState_SingleProcessHandoff(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	svc := runtime.NewRuntime(brk, nil).Services()

	openDB := func() *store.DB {
		db, err := store.Open(store.TestSQLiteDBURL(t), silentLogger())
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		if err := db.Migrate(t.Context()); err != nil {
			t.Fatalf("migrate db: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	initiating := NewGatewayCore(&recordingAdapter{}, map[string]any{"namespace": "ns", "gateway_id": "slack"})
	initiating.SetServices(svc)
	initiating.logger = silentLogger()
	initiating.db = openDB()

	callback := NewGatewayCore(&recordingAdapter{}, map[string]any{"namespace": "ns", "gateway_id": "webui"})
	callback.SetServices(svc)
	callback.logger = silentLogger()
	callback.db = openDB()

	initiating.putToolAuthState("STATE-SP", &ToolAuthState{
		TargetAgent:   "agent-z",
		TaskID:        "t42",
		UserID:        "u3",
		CredentialKey: "k",
	})

	got, ok := callback.TakeToolAuthState(t.Context(), "STATE-SP")
	if !ok {
		t.Fatal("callback core could not resolve state via the shared cache (single-process handoff broken)")
	}
	if got.TargetAgent != "agent-z" || got.TaskID != "t42" {
		t.Errorf("single-process handoff mismatch: %+v", got)
	}

	if _, ok := callback.TakeToolAuthState(t.Context(), "STATE-SP"); ok {
		t.Error("second Take should miss (state consumed from shared cache)")
	}
}

func TestTakeToolAuthState_NoDBCacheRoundTrip(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	core.logger = silentLogger()
	if core.db != nil {
		t.Fatal("precondition: setupTestGateway must leave db nil for this path")
	}
	ctx := t.Context()

	core.putToolAuthState("STATE-NODB", &ToolAuthState{TargetAgent: "agent-n", TaskID: "t7"})

	got, ok := core.TakeToolAuthState(ctx, "STATE-NODB")
	if !ok {
		t.Fatal("expected to resolve state from the in-process cache")
	}
	if got.TargetAgent != "agent-n" || got.TaskID != "t7" {
		t.Errorf("no-DB round-trip mismatch: %+v", got)
	}

	if _, ok := core.TakeToolAuthState(ctx, "STATE-NODB"); ok {
		t.Error("second Take should miss (state consumed from cache)")
	}
}

func TestTakeToolAuthState_CrossPodHandoff(t *testing.T) {
	db := store.TestDB(t)

	initiating, _, _ := setupTestGateway(t)
	initiating.logger = silentLogger()
	initiating.db = db

	callback, _, _ := setupTestGateway(t)
	callback.logger = silentLogger()
	callback.db = db

	initiating.putToolAuthState("STATE-X", &ToolAuthState{
		TargetAgent:   "agent-y",
		TaskID:        "t9",
		UserID:        "u2",
		CredentialKey: "k",
	})

	got, ok := callback.TakeToolAuthState(context.Background(), "STATE-X")
	if !ok {
		t.Fatal("callback core could not resolve state written by the initiating core (cross-pod handoff broken)")
	}
	if got.TargetAgent != "agent-y" || got.TaskID != "t9" {
		t.Errorf("cross-pod handoff mismatch: %+v", got)
	}
}

func TestToolAuthState_DBErrorDegrades(t *testing.T) {
	db := store.TestDB(t)
	core, _, _ := setupTestGateway(t)
	core.logger = silentLogger()
	core.db = db
	ctx := context.Background()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	core.putToolAuthState("s", &ToolAuthState{TargetAgent: "a"})

	if got, ok := core.TakeToolAuthState(ctx, "s"); ok || got != nil {
		t.Errorf("TakeToolAuthState on closed DB = (%+v, %v), want (nil, false)", got, ok)
	}
}

func TestTakeToolAuthState_MalformedDataMisses(t *testing.T) {
	db := store.TestDB(t)
	core, _, _ := setupTestGateway(t)
	core.logger = silentLogger()
	core.db = db
	ctx := context.Background()

	now := time.Now().UnixMilli()
	if err := store.NewToolAuthStateRepo(db).Put(ctx, "BAD", []byte("{not-json"), now+60_000, now); err != nil {
		t.Fatalf("seed malformed row: %v", err)
	}

	got, ok := core.TakeToolAuthState(ctx, "BAD")
	if ok || got != nil {
		t.Errorf("TakeToolAuthState(malformed) = (%+v, %v), want (nil, false)", got, ok)
	}
}

func TestTakeToolAuthState_PostgresKeepsNoCacheCopy(t *testing.T) {
	db := store.TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("needs SAM_TEST_GATEWAY_DB_URL (Postgres)")
	}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	svc := runtime.NewRuntime(brk, nil).Services()
	initiating := NewGatewayCore(&recordingAdapter{}, map[string]any{"namespace": "ns", "gateway_id": "a"})
	initiating.SetServices(svc)
	initiating.logger = silentLogger()
	initiating.db = db
	other := NewGatewayCore(&recordingAdapter{}, map[string]any{"namespace": "ns", "gateway_id": "b"})
	other.SetServices(svc)
	other.logger = silentLogger()
	other.db = db

	initiating.putToolAuthState("STATE-PG", &ToolAuthState{TargetAgent: "a"})
	if _, ok := svc.Cache().Get(toolAuthStateCacheKey("STATE-PG")); ok {
		t.Fatal("Postgres put wrote a process cache copy")
	}
	if _, ok := other.TakeToolAuthState(t.Context(), "STATE-PG"); !ok {
		t.Fatal("take from the shared record failed")
	}
	svc.Cache().Set(toolAuthStateCacheKey("STATE-PG"), &ToolAuthState{TargetAgent: "a"})
	if _, ok := initiating.TakeToolAuthState(t.Context(), "STATE-PG"); ok {
		t.Error("Postgres take fell back to the process cache")
	}
}
