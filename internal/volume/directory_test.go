// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestDirStore(t *testing.T) (*DirectoryStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := NewDirectoryStore(root, nil)
	if err != nil {
		t.Fatalf("NewDirectoryStore: %v", err)
	}
	id := "vol-1"
	if err := os.MkdirAll(filepath.Join(root, id), 0o755); err != nil {
		t.Fatalf("mkdir vol dir: %v", err)
	}
	return store, id
}

func TestDirectoryStore_AcquireWriteLock_NoTOCTOU(t *testing.T) {
	store, id := newTestDirStore(t)
	ctx := context.Background()

	const goroutines = 100
	var success atomic.Int32
	var locked atomic.Int32
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)

	for i := range goroutines {
		done.Add(1)
		go func(holder int) {
			defer done.Done()
			start.Wait()
			err := store.AcquireWriteLock(ctx, id, "h-"+strconv.Itoa(holder), time.Hour)
			if err == nil {
				success.Add(1)
				return
			}
			if _, ok := errors.AsType[*LockError](err); ok {
				locked.Add(1)
				return
			}
			t.Errorf("unexpected error: %v", err)
		}(i)
	}
	start.Done()
	done.Wait()

	if success.Load() != 1 {
		t.Errorf("exactly 1 goroutine should acquire the lock, got %d", success.Load())
	}
	if locked.Load() != goroutines-1 {
		t.Errorf("expected %d LockError, got %d", goroutines-1, locked.Load())
	}
}

func TestDirectoryStore_AcquireRelease_Cycle(t *testing.T) {
	store, id := newTestDirStore(t)
	ctx := context.Background()

	if err := store.AcquireWriteLock(ctx, id, "h1", time.Hour); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := store.AcquireWriteLock(ctx, id, "h2", time.Hour); err == nil {
		t.Fatal("second acquire should have failed while first holds the lock")
	}
	if err := store.ReleaseWriteLock(ctx, id, "h1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := store.AcquireWriteLock(ctx, id, "h2", time.Hour); err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
}

func TestDirectoryStore_DifferentVolumesDontBlock(t *testing.T) {
	root := t.TempDir()
	store, err := NewDirectoryStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"vol-a", "vol-b"} {
		if err := os.MkdirAll(filepath.Join(root, id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := store.AcquireWriteLock(ctx, "vol-a", "h", time.Hour); err != nil {
		t.Fatalf("acquire vol-a: %v", err)
	}
	if err := store.AcquireWriteLock(ctx, "vol-b", "h", time.Hour); err != nil {
		t.Fatalf("acquire vol-b should not be blocked by vol-a lock: %v", err)
	}
}

func TestDirectoryStore_RejectsTraversalIDs(t *testing.T) {
	store, _ := newTestDirStore(t)
	ctx := context.Background()

	for _, badID := range []string{
		"../../etc",
		"vol-bad/slash",
		"vol-bad.dots",
		"\x00null-byte",
		"x",
	} {
		t.Run(badID, func(t *testing.T) {
			if _, err := store.Resolve(ctx, badID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Resolve(%q) = %v, want ErrNotFound", badID, err)
			}
			if err := store.AcquireWriteLock(ctx, badID, "holder", time.Minute); !errors.Is(err, ErrNotFound) {
				t.Fatalf("AcquireWriteLock(%q) = %v, want ErrNotFound", badID, err)
			}
			if err := store.ReleaseWriteLock(ctx, badID, "holder"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("ReleaseWriteLock(%q) = %v, want ErrNotFound", badID, err)
			}
		})
	}
}

func TestDirectoryStore_NoOpServiceMethods(t *testing.T) {
	store, _ := newTestDirStore(t)
	ctx := context.Background()

	if n, err := store.CleanupExpired(ctx); n != 0 || err != nil {
		t.Errorf("CleanupExpired = (%d, %v), want (0, nil)", n, err)
	}
	if err := store.AssociateSession(ctx, "vol-1", "session-1"); err != nil {
		t.Errorf("AssociateSession: %v", err)
	}
	if ids, err := store.ListSessionVolumes(ctx, "session-1"); ids != nil || err != nil {
		t.Errorf("ListSessionVolumes = (%v, %v), want (nil, nil)", ids, err)
	}
	if n, err := store.UserUsage(ctx, "u"); n != 0 || err != nil {
		t.Errorf("UserUsage = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := store.RefreshSize(ctx, "vol-1"); n != 0 || err != nil {
		t.Errorf("RefreshSize = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := store.RefreshAllSizes(ctx); n != 0 || err != nil {
		t.Errorf("RefreshAllSizes = (%d, %v), want (0, nil)", n, err)
	}
	if q := store.PerUserQuotaBytes(); q != 0 {
		t.Errorf("PerUserQuotaBytes = %d, want 0 (no quota on STR-side store)", q)
	}

	if _, err := store.Create(ctx, CreateOptions{}); err == nil {
		t.Error("Create on DirectoryStore must error — host-side FilesystemStore owns Creates")
	}
	if err := store.Delete(ctx, "vol-1", CallerHuman); err == nil {
		t.Error("Delete on DirectoryStore must error — host-side FilesystemStore owns Deletes")
	}

	if v, err := store.Get(ctx, "vol-1"); v != nil || err == nil {
		t.Errorf("Get on DirectoryStore must error, got (%v, %v)", v, err)
	}
	if vs, err := store.List(ctx, "u", ListFilter{}); vs != nil || err == nil {
		t.Errorf("List on DirectoryStore must error, got (%v, %v)", vs, err)
	}
}

func TestDirectoryStore_Resolve_MissingManagedVolumeReturnsNotFound(t *testing.T) {
	root := t.TempDir()
	store, err := NewDirectoryStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(context.Background(), "vol-deadbeefdeadbeef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve = %v, want ErrNotFound", err)
	}
}
