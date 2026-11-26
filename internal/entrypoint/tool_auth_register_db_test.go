// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func toolAuthRegisterDBCore(t *testing.T) (*GatewayCore, *store.DB, *broker.DevBroker) {
	t.Helper()
	db := store.TestDB(t)
	core, brk, _ := setupTestGateway(t)
	core.logger = silentLogger()
	core.db = db
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core.StartToolAuthRegisterConsumer(t.Context(), silentLogger())
	return core, db, brk
}

func awaitToolAuthRow(t *testing.T, db *store.DB, state string) {
	t.Helper()
	waitFor(t, 2*time.Second, func() bool {
		var count int
		if err := db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM tool_auth_state WHERE state = ?`, state).Scan(&count); err != nil {
			t.Fatalf("count tool_auth_state: %v", err)
		}
		return count == 1
	})
}

func TestToolAuthRegister_TTLClampReachesDBRow(t *testing.T) {
	_, db, brk := toolAuthRegisterDBCore(t)
	repo := store.NewToolAuthStateRepo(db)
	ctx := context.Background()

	base := a2a.ToolAuthStateRegistration{
		TargetAgent:   "agent-x",
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
	}

	tests := []struct {
		name     string
		state    string
		ttlMs    int64
		takeAt   time.Duration
		wantLive bool
	}{
		{"below floor is raised to the 30s minimum", "STATE-FLOOR", 1, 20 * time.Second, true},
		{"above ceiling is live inside 30m", "STATE-CEIL-IN", 3_600_000, 29 * time.Minute, true},
		{"above ceiling is dead past 30m", "STATE-CEIL-OUT", 3_600_000, 31 * time.Minute, false},
	}
	for _, tt := range tests {
		reg := base
		reg.State = tt.state
		reg.TTLMs = tt.ttlMs
		publishRegistrationStruct(t, brk, reg, nil)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			awaitToolAuthRow(t, db, tt.state)
			_, ok, err := repo.Take(ctx, tt.state, time.Now().Add(tt.takeAt).UnixMilli())
			if err != nil {
				t.Fatalf("Take: %v", err)
			}
			if ok != tt.wantLive {
				t.Errorf("row live at +%v = %v, want %v", tt.takeAt, ok, tt.wantLive)
			}
		})
	}
}

func TestToolAuthRegister_SeedsCacheAndDB(t *testing.T) {
	core, db, brk := toolAuthRegisterDBCore(t)

	publishRegistrationStruct(t, brk, a2a.ToolAuthStateRegistration{
		State:         "STATE-BOTH",
		TargetAgent:   "agent-x",
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
	}, nil)
	awaitToolAuthRow(t, db, "STATE-BOTH")

	_, cached := core.Services().Cache().Get(toolAuthStateCacheKey("STATE-BOTH"))
	if wantCached := db.Dialect() == "sqlite"; cached != wantCached {
		t.Errorf("cache seeded = %v, want %v on %s", cached, wantCached, db.Dialect())
	}
	awaitToolAuthState(t, core, "STATE-BOTH")
}

func TestToolAuthRegister_SeedFailureIsNotSilent(t *testing.T) {
	const state = "STATE-SEED-FAIL"
	databaseURL := store.TestDBURL(t)
	consumerDB, err := store.Open(databaseURL, silentLogger())
	if err != nil {
		t.Fatalf("open consumer db: %v", err)
	}
	readerDB, err := store.Open(databaseURL, silentLogger())
	if err != nil {
		t.Fatalf("open reader db: %v", err)
	}
	t.Cleanup(func() { _ = readerDB.Close() })

	capture := newLogCapture()
	core, brk, _ := setupTestGateway(t)
	core.logger = capture.logger
	core.db = consumerDB
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core.StartToolAuthRegisterConsumer(t.Context(), capture.logger)

	if err := consumerDB.Close(); err != nil {
		t.Fatalf("close consumer db: %v", err)
	}

	publishRegistrationStruct(t, brk, a2a.ToolAuthStateRegistration{
		State:         state,
		TargetAgent:   "agent-x",
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
		CodeVerifier:  "VERIFIER-SECRET-DO-NOT-LOG",
	}, nil)

	awaitLogContains(t, capture, "failed to seed tool auth state")
	requireLogLevel(t, capture, "failed to seed tool auth state", "ERROR")

	var count int
	if err := readerDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM tool_auth_state WHERE state = ?`, state).Scan(&count); err != nil {
		t.Fatalf("count tool_auth_state: %v", err)
	}
	if count != 0 {
		t.Errorf("shared store holds %d rows for a registration that failed to seed, want 0", count)
	}

	logs := capture.text()
	if strings.Contains(logs, "seeded tool auth state from registration") {
		t.Errorf("a failed seed logged success:\n%s", logs)
	}
	if strings.Contains(logs, state) || strings.Contains(logs, "VERIFIER-SECRET-DO-NOT-LOG") {
		t.Errorf("the failure path logs the raw state or verifier:\n%s", logs)
	}
}
