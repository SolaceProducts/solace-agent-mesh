// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

func TestGatewayCore_StartDoesNotWaitForAsyncIndexes_Postgres(t *testing.T) {
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}
	core, _, _ := setupDBTestGateway(t)
	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	holder, err := core.db.RawDB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, dbutil.AsyncIndexLockID("gateway_async_indexes")); err != nil {
		t.Fatal(err)
	}

	started := make(chan error, 1)
	go func() { started <- core.Start(ctx) }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Start did not return while another process held the async index lock")
	}
	defer func() { _ = core.Stop(ctx) }()
	if ready, err := store.NewTaskEventRepo(core.db).Blobs().ReferenceIndexesReady(ctx); err != nil || ready {
		t.Errorf("ReferenceIndexesReady with the build lock held = %v, %v; want false, since Start returned without the builds", ready, err)
	}
}

func TestTheGatewayLeaderJobBuildsTheQueuedAsyncIndexes_Postgres(t *testing.T) {
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}
	exe := startedExecutor(t, map[string]map[string]any{"gw-pg": {
		"namespace":       "test",
		"session_service": map[string]any{"type": "sql", "database_url": store.TestDBURL(t)},
	}})
	core, ctx := exe.GetGateway("gw-pg"), t.Context()

	if err := leaderJob(t, exe, leader.JobGatewayAsyncIndexes).Run(ctx, leader.Term{Number: 1}); err != nil {
		t.Fatal(err)
	}
	var unbuilt int
	if err := core.db.RawDB().QueryRowContext(ctx,
		`SELECT count(*) FROM gateway_async_indexes WHERE completed_at IS NULL`).Scan(&unbuilt); err != nil {
		t.Fatal(err)
	}
	if unbuilt != 0 {
		t.Errorf("%d queued indexes left unbuilt after one run of the leader job", unbuilt)
	}
	if ready, err := store.NewTaskEventRepo(core.db).Blobs().ReferenceIndexesReady(ctx); err != nil || !ready {
		t.Errorf("ReferenceIndexesReady after the leader job = %v, %v; want true", ready, err)
	}
}
