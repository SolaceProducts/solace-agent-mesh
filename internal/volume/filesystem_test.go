// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestFilesystemStore(t *testing.T) *FilesystemStore {
	t.Helper()
	root := t.TempDir()
	store, err := NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestFilesystemStore_CreateAndGet(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	ttl := 24 * time.Hour
	id, err := store.Create(ctx, CreateOptions{
		Name:        "test-vol",
		Description: "test description",
		UserID:      "user1",
		CreatorType: CallerHuman,
		CreatorName: "alice",
		TTL:         &ttl,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	dataDir := filepath.Join(store.root, id, "data")
	if _, statErr := os.Stat(dataDir); statErr != nil {
		t.Fatalf("data directory not created: %v", statErr)
	}

	v, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Name != "test-vol" {
		t.Errorf("Name = %q, want %q", v.Name, "test-vol")
	}
	if v.Description != "test description" {
		t.Errorf("Description = %q, want %q", v.Description, "test description")
	}
	if v.UserID != "user1" {
		t.Errorf("UserID = %q, want %q", v.UserID, "user1")
	}
	if v.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}
}

func TestFilesystemStore_CreateValidation(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, CreateOptions{UserID: "u"}); err != ErrInvalidOptions {
		t.Errorf("expected ErrInvalidOptions for missing name, got %v", err)
	}
	if _, err := store.Create(ctx, CreateOptions{Name: "n"}); err != ErrInvalidOptions {
		t.Errorf("expected ErrInvalidOptions for missing user, got %v", err)
	}
}

func TestFilesystemStore_GetNotFound(t *testing.T) {
	store := newTestFilesystemStore(t)
	_, err := store.Get(context.Background(), "nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFilesystemStore_List(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	_, _ = store.Create(ctx, CreateOptions{Name: "v1", UserID: "user1", CreatorType: CallerHuman})
	_, _ = store.Create(ctx, CreateOptions{Name: "v2", UserID: "user1", CreatorType: CallerAgent})
	_, _ = store.Create(ctx, CreateOptions{Name: "v3", UserID: "user2", CreatorType: CallerHuman})

	vols, err := store.List(ctx, "user1", ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(vols) != 2 {
		t.Errorf("got %d volumes, want 2", len(vols))
	}

	ct := CallerAgent
	vols, err = store.List(ctx, "user1", ListFilter{CreatorType: &ct})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(vols) != 1 {
		t.Errorf("got %d volumes, want 1", len(vols))
	}
}

func TestFilesystemStore_Delete(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	if err := store.Delete(ctx, id, CallerAgent); err != ErrPermission {
		t.Errorf("expected ErrPermission, got %v", err)
	}

	if err := store.Delete(ctx, id, CallerHuman); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	volDir := filepath.Join(store.root, id)
	if _, err := os.Stat(volDir); !os.IsNotExist(err) {
		t.Errorf("volume directory still exists after delete")
	}

	if _, err := store.Get(ctx, id); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestFilesystemStore_WriteLock(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	if err := store.AcquireWriteLock(ctx, id, "agent-a", 5*time.Minute); err != nil {
		t.Fatalf("AcquireWriteLock: %v", err)
	}

	v, _ := store.Get(ctx, id)
	if v.WriteLock == nil || v.WriteLock.Holder != "agent-a" {
		t.Fatal("lock not persisted")
	}

	err := store.AcquireWriteLock(ctx, id, "agent-b", 5*time.Minute)
	if _, ok := errors.AsType[*LockError](err); !ok {
		t.Fatalf("expected *LockError, got %T: %v", err, err)
	}

	if err := store.Delete(ctx, id, CallerHuman); err == nil {
		t.Error("expected error deleting locked volume")
	}

	if err := store.ReleaseWriteLock(ctx, id, "agent-b"); err != ErrNotLockHolder {
		t.Errorf("expected ErrNotLockHolder, got %v", err)
	}

	if err := store.ReleaseWriteLock(ctx, id, "agent-a"); err != nil {
		t.Fatalf("ReleaseWriteLock: %v", err)
	}

	v, _ = store.Get(ctx, id)
	if v.WriteLock != nil {
		t.Error("lock not cleared after release")
	}
}

func TestFilesystemStore_ExpiredLockAutoRelease(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	if err := store.AcquireWriteLock(ctx, id, "agent-a", 1*time.Nanosecond); err != nil {
		t.Fatalf("AcquireWriteLock: %v", err)
	}
	time.Sleep(1 * time.Millisecond)

	if err := store.AcquireWriteLock(ctx, id, "agent-b", 5*time.Minute); err != nil {
		t.Fatalf("AcquireWriteLock after expiry: %v", err)
	}
}

func TestFilesystemStore_Resolve(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	path, err := store.Resolve(ctx, id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	expected := filepath.Join(store.root, id, "data")
	if path != expected {
		t.Errorf("Resolve = %q, want %q", path, expected)
	}
}

func TestFilesystemStore_ResolveExpired(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	ttl := 1 * time.Nanosecond
	id, _ := store.Create(ctx, CreateOptions{
		Name: "v", UserID: "u", CreatorType: CallerHuman, TTL: &ttl,
	})
	time.Sleep(1 * time.Millisecond)

	if _, err := store.Resolve(ctx, id); err != ErrExpired {
		t.Errorf("expected ErrExpired, got %v", err)
	}
}

func TestFilesystemStore_CleanupExpired(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	shortTTL := 1 * time.Nanosecond
	id1, _ := store.Create(ctx, CreateOptions{Name: "short", UserID: "u", CreatorType: CallerHuman, TTL: &shortTTL})
	_, _ = store.Create(ctx, CreateOptions{Name: "permanent", UserID: "u", CreatorType: CallerHuman})

	time.Sleep(1 * time.Millisecond)

	removed, err := store.CleanupExpired(ctx)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	if _, err := os.Stat(filepath.Join(store.root, id1)); !os.IsNotExist(err) {
		t.Error("expired volume directory not cleaned up")
	}

	vols, _ := store.List(ctx, "u", ListFilter{})
	if len(vols) != 1 || vols[0].Name != "permanent" {
		t.Errorf("unexpected volumes after cleanup: %v", vols)
	}
}

func TestFilesystemStore_RecordAccess(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	v1, _ := store.Get(ctx, id)
	if err := store.RecordAccess(ctx, id); err != nil {
		t.Fatalf("RecordAccess: %v", err)
	}

	v2, _ := store.Get(ctx, id)
	if v2.LastAccess.Before(v1.LastAccess) {
		t.Error("LastAccess went backwards")
	}
}

func TestFilesystemStore_RecordAccessNotFound(t *testing.T) {
	store := newTestFilesystemStore(t)
	if err := store.RecordAccess(context.Background(), "nope"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFilesystemStore_SessionAffinity(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	id1, _ := store.Create(ctx, CreateOptions{Name: "v1", UserID: "user1", CreatorType: CallerHuman})
	id2, _ := store.Create(ctx, CreateOptions{Name: "v2", UserID: "user1", CreatorType: CallerHuman})
	_, _ = store.Create(ctx, CreateOptions{Name: "v3", UserID: "user1", CreatorType: CallerHuman})

	if err := store.AssociateSession(ctx, id1, "session-A"); err != nil {
		t.Fatalf("AssociateSession: %v", err)
	}
	if err := store.AssociateSession(ctx, id2, "session-A"); err != nil {
		t.Fatalf("AssociateSession: %v", err)
	}

	if err := store.AssociateSession(ctx, id1, "session-A"); err != nil {
		t.Fatalf("AssociateSession idempotent: %v", err)
	}

	ids, err := store.ListSessionVolumes(ctx, "session-A")
	if err != nil {
		t.Fatalf("ListSessionVolumes: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("got %d session volumes, want 2", len(ids))
	}

	sid := "session-A"
	vols, err := store.List(ctx, "user1", ListFilter{SessionID: &sid})
	if err != nil {
		t.Fatalf("List with SessionID: %v", err)
	}
	if len(vols) != 2 {
		t.Errorf("got %d filtered volumes, want 2", len(vols))
	}

	noSID := "session-Z"
	vols, err = store.List(ctx, "user1", ListFilter{SessionID: &noSID})
	if err != nil {
		t.Fatalf("List empty session: %v", err)
	}
	if len(vols) != 0 {
		t.Errorf("got %d volumes for empty session, want 0", len(vols))
	}
}

func TestFilesystemStore_DeleteCascadesSessionAssociations(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerAgent})

	_ = store.AssociateSession(ctx, id, "sess-1")
	_ = store.AssociateSession(ctx, id, "sess-2")

	if err := store.Delete(ctx, id, CallerAgent); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	ids1, _ := store.ListSessionVolumes(ctx, "sess-1")
	ids2, _ := store.ListSessionVolumes(ctx, "sess-2")
	if len(ids1) != 0 || len(ids2) != 0 {
		t.Error("session associations not cleaned up after delete")
	}
}

func TestFilesystemStore_CleanupExpiredCascadesSessionAssociations(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	shortTTL := 1 * time.Nanosecond
	id, _ := store.Create(ctx, CreateOptions{
		Name: "expiring", UserID: "u", CreatorType: CallerHuman, TTL: &shortTTL,
	})
	_ = store.AssociateSession(ctx, id, "sess-1")

	time.Sleep(1 * time.Millisecond)

	removed, err := store.CleanupExpired(ctx)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	ids, _ := store.ListSessionVolumes(ctx, "sess-1")
	if len(ids) != 0 {
		t.Error("session associations not cleaned up after expiry cleanup")
	}
}

func TestFilesystemStore_SQLitePragmas(t *testing.T) {
	store := newTestFilesystemStore(t)

	tests := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
		{"synchronous", "1"},
		{"cache_size", "-64000"},
		{"wal_autocheckpoint", "1000"},
	}

	for _, tt := range tests {
		t.Run(tt.pragma, func(t *testing.T) {
			var val string
			if err := store.db.QueryRow("PRAGMA " + tt.pragma).Scan(&val); err != nil {
				t.Fatalf("query PRAGMA %s: %v", tt.pragma, err)
			}
			if val != tt.want {
				t.Errorf("PRAGMA %s = %q, want %q", tt.pragma, val, tt.want)
			}
		})
	}
}

func TestFilesystemStore_DataDirectoryUsable(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	path, _ := store.Resolve(ctx, id)

	testFile := filepath.Join(path, "hello.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write to volume: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read from volume: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("data = %q, want %q", data, "hello")
	}
}

func TestFilesystemStore_AcquireWriteLock_NoTOCTOU(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()
	id, err := store.Create(ctx, CreateOptions{
		Name: "v", UserID: "u", CreatorType: CallerHuman,
	})
	if err != nil {
		t.Fatal(err)
	}

	const N = 8
	var wg sync.WaitGroup
	wg.Add(N)
	successes := atomic.Int32{}
	rejects := atomic.Int32{}
	for i := range N {
		go func() {
			defer wg.Done()
			holder := fmt.Sprintf("h%d", i)
			err := store.AcquireWriteLock(ctx, id, holder, time.Minute)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, &LockError{}):
				rejects.Add(1)
			default:
				if _, ok := err.(*LockError); ok {
					rejects.Add(1)
					return
				}
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("expected exactly one successful Acquire, got %d (rejects=%d)", successes.Load(), rejects.Load())
	}
	if rejects.Load() != N-1 {
		t.Fatalf("expected %d rejects, got %d", N-1, rejects.Load())
	}
}
