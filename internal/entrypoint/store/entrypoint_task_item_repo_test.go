// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
)

func seedTaskRecord(t *testing.T, db *DB, taskID string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO task_records (task_id, gateway_id, agent_name, user_id, last_seen_at, created_at)
		 VALUES (?, 'slack', 'agent-a', 'u1', 1, 1)`, taskID); err != nil {
		t.Fatalf("seed task record: %v", err)
	}
}

const itemTaskID = "10000000-0000-0000-0000-0000000000c1"

func TestEntrypointTaskItemRepo_PutGetList(t *testing.T) {
	db := newTestDB(t)
	seedTaskRecord(t, db, itemTaskID)
	repo := NewEntrypointTaskItemRepo(db)
	ctx := t.Context()

	if err := repo.Put(ctx, itemTaskID, "destination", `{"c":"C1"}`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Put(ctx, itemTaskID, "message", "1.1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Put(ctx, itemTaskID, "message", "1.2"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := repo.Get(ctx, itemTaskID, "message"); err != nil || !ok || v != "1.2" {
		t.Fatalf("Get(message) = %q, %v, %v; want the replaced value", v, ok, err)
	}
	if _, ok, err := repo.Get(ctx, itemTaskID, "absent"); err != nil || ok {
		t.Fatalf("Get(absent) = %v, %v; want not found", ok, err)
	}
	items, err := repo.List(ctx, itemTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items["destination"] != `{"c":"C1"}` || items["message"] != "1.2" {
		t.Fatalf("List = %v", items)
	}
	if err := repo.Delete(ctx, itemTaskID, "message"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, itemTaskID, "message"); err != nil {
		t.Fatalf("deleting an absent item: %v", err)
	}
	if _, ok, _ := repo.Get(ctx, itemTaskID, "message"); ok {
		t.Fatal("item survived Delete")
	}
}

func TestEntrypointTaskItemRepo_ClaimHasOneWinner(t *testing.T) {
	db := newTestDB(t)
	seedTaskRecord(t, db, itemTaskID)
	repo := NewEntrypointTaskItemRepo(db)

	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			ok, err := repo.Claim(t.Context(), itemTaskID, "hil-answer:r1", "")
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			if ok {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("claim winners = %d, want 1", wins.Load())
	}
}

func TestEntrypointTaskItemRepo_ReplaceOnlyFromTheValueRead(t *testing.T) {
	db := newTestDB(t)
	seedTaskRecord(t, db, itemTaskID)
	repo := NewEntrypointTaskItemRepo(db)
	ctx := t.Context()
	if _, err := repo.Claim(ctx, itemTaskID, "hil-answered:r1", "100"); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.Replace(ctx, itemTaskID, "hil-answered:r1", "999", "200"); err != nil || ok {
		t.Fatalf("Replace from a stale value = %v, %v; want false", ok, err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if ok, err := repo.Replace(ctx, itemTaskID, "hil-answered:r1", "100", "200"); err == nil && ok {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("Replace winners = %d, want 1", wins.Load())
	}
	if v, _, _ := repo.Get(ctx, itemTaskID, "hil-answered:r1"); v != "200" {
		t.Fatalf("value = %q, want 200", v)
	}
}

func TestEntrypointTaskItemRepo_DeleteIf(t *testing.T) {
	db := newTestDB(t)
	seedTaskRecord(t, db, itemTaskID)
	repo := NewEntrypointTaskItemRepo(db)
	ctx := t.Context()
	if err := repo.Put(ctx, itemTaskID, "hil-answered:r1", "100"); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.DeleteIf(ctx, itemTaskID, "hil-answered:r1", "999"); err != nil || ok {
		t.Fatalf("DeleteIf with another value = %v, %v; want false", ok, err)
	}
	if ok, err := repo.DeleteIf(ctx, itemTaskID, "hil-answered:r1", "100"); err != nil || !ok {
		t.Fatalf("DeleteIf with the held value = %v, %v; want true", ok, err)
	}
	if _, found, _ := repo.Get(ctx, itemTaskID, "hil-answered:r1"); found {
		t.Fatal("item survived DeleteIf")
	}
}

func TestEntrypointTaskItemRepo_DeletedWithTaskRecord(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
	const reaped = "10000000-0000-0000-0000-0000000000c2"
	seedTaskRecord(t, db, itemTaskID)
	seedTaskRecord(t, db, reaped)
	repo := NewEntrypointTaskItemRepo(db)
	for _, id := range []string{itemTaskID, reaped} {
		if err := repo.Put(ctx, id, "destination", "x"); err != nil {
			t.Fatal(err)
		}
	}

	records := NewTaskRecordRepo(db, "slack")
	if err := records.Delete(ctx, itemTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := records.DeleteUnseenBefore(ctx, time.UnixMilli(2), 10); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{itemTaskID, reaped} {
		if items, err := repo.List(ctx, id); err != nil || len(items) != 0 {
			t.Fatalf("items of %s after its record was deleted: %v, %v", id, items, err)
		}
	}
	if err := repo.Put(ctx, itemTaskID, "message", "1.1"); err == nil {
		t.Fatal("an item was written for a task with no record")
	}
}

const entrypointTaskItemsMigration = "entrypoint_task_items"

func entrypointTaskItemsMigrationBody(t *testing.T, db *DB, version int64) {
	t.Helper()
	ctx := t.Context()
	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
	seedTaskRecord(t, db, itemTaskID)
	if err := NewEntrypointTaskItemRepo(db).Put(ctx, itemTaskID, "destination", "x"); err != nil {
		t.Fatalf("write after the migration: %v", err)
	}
	if err := db.MigrateDownTo(ctx, version-1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT 1 FROM entrypoint_task_items`); err == nil {
		t.Fatal("entrypoint_task_items survived the down migration")
	}
	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}

func TestEntrypointTaskItemsMigration(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "entrypoint_task_items.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	version := migrationVersion(t, sqliteMigrations(), entrypointTaskItemsMigration)
	if err := db.MigrateUpTo(t.Context(), version-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	entrypointTaskItemsMigrationBody(t, db, version)
}

func TestEntrypointTaskItemsMigration_Postgres(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}
	version := migrationVersion(t, postgresMigrations(), entrypointTaskItemsMigration)
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_entrypoint_task_items", func(u string) error {
		db, openErr := Open(u, slog.Default())
		if openErr != nil {
			return openErr
		}
		defer func() { _ = db.Close() }()
		return db.MigrateUpTo(t.Context(), version-1)
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	t.Cleanup(tmpl.Close)
	db, err := Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	entrypointTaskItemsMigrationBody(t, db, version)
}

func TestEntrypointTaskItemRepo_ClosedDatabase(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	repo := NewEntrypointTaskItemRepo(db)
	ctx := t.Context()
	if err := repo.Put(ctx, itemTaskID, "a", "b"); err == nil {
		t.Error("Put on a closed database")
	}
	if _, err := repo.Claim(ctx, itemTaskID, "a", "b"); err == nil {
		t.Error("Claim on a closed database")
	}
	if _, _, err := repo.Get(ctx, itemTaskID, "a"); err == nil {
		t.Error("Get on a closed database")
	}
	if _, err := repo.List(ctx, itemTaskID); err == nil {
		t.Error("List on a closed database")
	}
	if err := repo.Delete(ctx, itemTaskID, "a"); err == nil {
		t.Error("Delete on a closed database")
	}
	if _, err := repo.Replace(ctx, itemTaskID, "a", "b", "c"); err == nil {
		t.Error("Replace on a closed database")
	}
	if _, err := repo.DeleteIf(ctx, itemTaskID, "a", "b"); err == nil {
		t.Error("DeleteIf on a closed database")
	}
}
