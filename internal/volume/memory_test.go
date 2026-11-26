// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStore_CreateAndGet(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ttl := 24 * time.Hour
	id, err := store.Create(ctx, CreateOptions{
		Name:        "test-vol",
		Description: "a test volume",
		UserID:      "user1",
		CreatorType: CallerHuman,
		CreatorName: "alice",
		TTL:         &ttl,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	v, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Name != "test-vol" {
		t.Errorf("Name = %q, want %q", v.Name, "test-vol")
	}
	if v.Description != "a test volume" {
		t.Errorf("Description = %q, want %q", v.Description, "a test volume")
	}
	if v.UserID != "user1" {
		t.Errorf("UserID = %q, want %q", v.UserID, "user1")
	}
	if v.CreatorType != CallerHuman {
		t.Errorf("CreatorType = %q, want %q", v.CreatorType, CallerHuman)
	}
	if v.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}
	if v.WriteLock != nil {
		t.Error("expected WriteLock to be nil")
	}
}

func TestMemoryStore_CreateValidation(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if _, err := store.Create(ctx, CreateOptions{UserID: "u"}); err != ErrInvalidOptions {
		t.Errorf("expected ErrInvalidOptions for missing name, got %v", err)
	}

	if _, err := store.Create(ctx, CreateOptions{Name: "n"}); err != ErrInvalidOptions {
		t.Errorf("expected ErrInvalidOptions for missing user, got %v", err)
	}
}

func TestMemoryStore_GetNotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.Get(context.Background(), "nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_List(t *testing.T) {
	store := NewMemoryStore()
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

	ct := CallerHuman
	vols, err = store.List(ctx, "user1", ListFilter{CreatorType: &ct})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(vols) != 1 {
		t.Errorf("got %d volumes, want 1", len(vols))
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	if err := store.Delete(ctx, id, CallerAgent); err != ErrPermission {
		t.Errorf("expected ErrPermission, got %v", err)
	}

	if err := store.Delete(ctx, id, CallerHuman); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := store.Get(ctx, id); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMemoryStore_DeleteNotFound(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Delete(context.Background(), "nope", CallerHuman); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_WriteLock(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	if err := store.AcquireWriteLock(ctx, id, "agent-a", 5*time.Minute); err != nil {
		t.Fatalf("AcquireWriteLock: %v", err)
	}

	err := store.AcquireWriteLock(ctx, id, "agent-b", 5*time.Minute)
	if err == nil {
		t.Fatal("expected error on second acquire")
	}
	lockErr, ok := errors.AsType[*LockError](err)
	if !ok {
		t.Fatalf("expected *LockError, got %T", err)
	}
	if lockErr.Holder != "agent-a" {
		t.Errorf("Holder = %q, want %q", lockErr.Holder, "agent-a")
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

	if err := store.AcquireWriteLock(ctx, id, "agent-b", 5*time.Minute); err != nil {
		t.Fatalf("AcquireWriteLock after release: %v", err)
	}
}

func TestMemoryStore_ExpiredLockAutoRelease(t *testing.T) {
	store := NewMemoryStore()
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

func TestMemoryStore_Resolve(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	path, err := store.Resolve(ctx, id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
}

func TestMemoryStore_ResolveExpired(t *testing.T) {
	store := NewMemoryStore()
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

func TestMemoryStore_CleanupExpired(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	shortTTL := 1 * time.Nanosecond
	_, _ = store.Create(ctx, CreateOptions{Name: "short", UserID: "u", CreatorType: CallerHuman, TTL: &shortTTL})
	_, _ = store.Create(ctx, CreateOptions{Name: "permanent", UserID: "u", CreatorType: CallerHuman})

	time.Sleep(1 * time.Millisecond)

	removed, err := store.CleanupExpired(ctx)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	vols, _ := store.List(ctx, "u", ListFilter{})
	if len(vols) != 1 {
		t.Errorf("remaining = %d, want 1", len(vols))
	}
	if vols[0].Name != "permanent" {
		t.Errorf("remaining volume name = %q, want %q", vols[0].Name, "permanent")
	}
}

func TestMemoryStore_RecordAccess(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	v1, _ := store.Get(ctx, id)
	time.Sleep(1 * time.Millisecond)

	if err := store.RecordAccess(ctx, id); err != nil {
		t.Fatalf("RecordAccess: %v", err)
	}

	v2, _ := store.Get(ctx, id)
	if !v2.LastAccess.After(v1.LastAccess) {
		t.Error("LastAccess not updated")
	}
}

func TestMemoryStore_SessionAffinity(t *testing.T) {
	store := NewMemoryStore()
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

func TestMemoryStore_DeleteCascadesSessionAssociations(t *testing.T) {
	store := NewMemoryStore()
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

func TestMemoryStore_CleanupExpiredCascadesSessionAssociations(t *testing.T) {
	store := NewMemoryStore()
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

func TestMemoryStore_Create_RejectsMalformedID(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()

	bad := []string{"../etc", "vol-" + string([]byte{0}), "no-prefix", "vol-bad/slash"}
	for _, id := range bad {
		_, err := m.Create(ctx, CreateOptions{
			Name: "x", UserID: "u", CreatorType: CallerHuman, ID: id,
		})
		if !errors.Is(err, ErrInvalidOptions) {
			t.Errorf("id=%q: expected ErrInvalidOptions, got %v", id, err)
		}
	}
}

func TestMemoryStore_Create_LinkedExternalPath(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()

	id, err := m.Create(ctx, CreateOptions{
		Name: "linked", UserID: "u", CreatorType: CallerHuman,
		ExternalPath: "/abs/host/path",
	})
	if err != nil {
		t.Fatalf("Create linked: %v", err)
	}

	v, err := m.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !v.Linked {
		t.Error("expected Linked=true")
	}
	if v.Path != "/abs/host/path" {
		t.Errorf("Path = %q, want /abs/host/path", v.Path)
	}

	got, err := m.Resolve(ctx, id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "/abs/host/path" {
		t.Errorf("Resolve = %q, want host path", got)
	}

	m.volumes[id].SizeBytes = 9999
	used, err := m.UserUsage(ctx, "u")
	if err != nil {
		t.Fatalf("UserUsage: %v", err)
	}
	if used != 0 {
		t.Errorf("UserUsage = %d, want 0 (linked excluded)", used)
	}
}

func TestMemoryStore_Create_LinkedRequiresAbsolutePath(t *testing.T) {
	m := NewMemoryStore()
	_, err := m.Create(context.Background(), CreateOptions{
		Name: "x", UserID: "u", CreatorType: CallerHuman,
		ExternalPath: "relative/host/dir",
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("expected ErrInvalidOptions, got %v", err)
	}
}

func TestMemoryStore_Resolve_NotFound(t *testing.T) {
	m := NewMemoryStore()
	_, err := m.Resolve(context.Background(), "vol-missing")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_RecordAccess_NotFound(t *testing.T) {
	m := NewMemoryStore()
	err := m.RecordAccess(context.Background(), "vol-missing")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_ReleaseWriteLock_UnlockedIsNoop(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	id, _ := m.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	if err := m.ReleaseWriteLock(ctx, id, "anybody"); err != nil {
		t.Fatalf("unlocked release should be no-op, got %v", err)
	}
}

func TestMemoryStore_ReleaseWriteLock_NotFound(t *testing.T) {
	m := NewMemoryStore()
	err := m.ReleaseWriteLock(context.Background(), "vol-missing", "h")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_AcquireWriteLock_NotFound(t *testing.T) {
	m := NewMemoryStore()
	err := m.AcquireWriteLock(context.Background(), "vol-missing", "h", time.Minute)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_RefreshSize_ReportsStoredBytes(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()

	idMan, _ := m.Create(ctx, CreateOptions{Name: "m", UserID: "u", CreatorType: CallerHuman})
	idLink, _ := m.Create(ctx, CreateOptions{
		Name: "l", UserID: "u", CreatorType: CallerHuman,
		ExternalPath: "/abs/host",
	})

	m.volumes[idMan].SizeBytes = 123

	got, err := m.RefreshSize(ctx, idMan)
	if err != nil {
		t.Fatalf("RefreshSize managed: %v", err)
	}
	if got != 123 {
		t.Errorf("managed RefreshSize = %d, want 123", got)
	}

	got, err = m.RefreshSize(ctx, idLink)
	if err != nil {
		t.Fatalf("RefreshSize linked: %v", err)
	}
	if got != 0 {
		t.Errorf("linked RefreshSize = %d, want 0", got)
	}

	if _, missingErr := m.RefreshSize(ctx, "vol-missing"); !errors.Is(missingErr, ErrNotFound) {
		t.Fatalf("missing RefreshSize: got %v", missingErr)
	}

	count, err := m.RefreshAllSizes(ctx)
	if err != nil {
		t.Fatalf("RefreshAllSizes: %v", err)
	}
	if count != 1 {
		t.Errorf("RefreshAllSizes count = %d, want 1 (only managed)", count)
	}
}

func TestMemoryStore_ListSessionVolumes_UnknownSession(t *testing.T) {
	m := NewMemoryStore()
	ids, err := m.ListSessionVolumes(context.Background(), "no-such-session")
	if err != nil {
		t.Fatalf("ListSessionVolumes: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}
}

func TestMemoryStore_GetReturnsCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	id, _ := store.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

	v1, _ := store.Get(ctx, id)
	v1.Name = "mutated"

	v2, _ := store.Get(ctx, id)
	if v2.Name == "mutated" {
		t.Error("Get returned a reference instead of a copy")
	}
}
