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
	"sync"
	"testing"
)

func makeSizedSource(t *testing.T, name string, size int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFilesystemStore_UserUsage_EmptyManagedAndLinkedAreZero(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, CreateOptions{
		Name: "empty", UserID: "u", CreatorType: CallerHuman,
	}); err != nil {
		t.Fatalf("create empty: %v", err)
	}
	hostDir := makeSizedSource(t, "host", 4096)
	if _, err := store.Create(ctx, CreateOptions{
		Name: "linked", UserID: "u", CreatorType: CallerHuman,
		ExternalPath: hostDir,
	}); err != nil {
		t.Fatalf("create linked: %v", err)
	}

	usage, err := store.UserUsage(ctx, "u")
	if err != nil {
		t.Fatalf("UserUsage: %v", err)
	}
	if usage != 0 {
		t.Fatalf("UserUsage = %d, want 0 (empty managed + linked contribute zero)", usage)
	}
}

func TestFilesystemStore_UserUsage_CountsCopiedVolumes(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	src1 := makeSizedSource(t, "a", 1000)
	src2 := makeSizedSource(t, "b", 500)
	if _, err := store.Create(ctx, CreateOptions{
		Name: "v1", UserID: "u", CreatorType: CallerHuman, CopyFromPath: src1,
	}); err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if _, err := store.Create(ctx, CreateOptions{
		Name: "v2", UserID: "u", CreatorType: CallerHuman, CopyFromPath: src2,
	}); err != nil {
		t.Fatalf("create v2: %v", err)
	}

	usage, err := store.UserUsage(ctx, "u")
	if err != nil {
		t.Fatalf("UserUsage: %v", err)
	}
	if usage != 1500 {
		t.Fatalf("UserUsage = %d, want 1500", usage)
	}

	if u, _ := store.UserUsage(ctx, "other"); u != 0 {
		t.Fatalf("cross-user leak: other user sees %d bytes", u)
	}
}

func TestFilesystemStore_Quota_RejectsBeforeCopy(t *testing.T) {
	store := newTestFilesystemStore(t)
	store.SetPerUserQuotaBytes(800)
	ctx := context.Background()

	src := makeSizedSource(t, "fits", 500)
	if _, err := store.Create(ctx, CreateOptions{
		Name: "v1", UserID: "u", CreatorType: CallerHuman, CopyFromPath: src,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	overSrc := makeSizedSource(t, "over", 400)
	_, err := store.Create(ctx, CreateOptions{
		Name: "v2", UserID: "u", CreatorType: CallerHuman, CopyFromPath: overSrc,
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	var qe *QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *QuotaError in chain, got %T", err)
	}
	if qe.UsedBytes != 500 || qe.WouldAdd != 400 || qe.LimitBytes != 800 {
		t.Fatalf("quota error fields mismatch: %+v", qe)
	}

	vols, _ := store.List(ctx, "u", ListFilter{})
	if len(vols) != 1 {
		t.Fatalf("expected 1 volume after rejected second create, got %d", len(vols))
	}
}

func TestFilesystemStore_Quota_LinkedDoesNotCount(t *testing.T) {
	store := newTestFilesystemStore(t)
	store.SetPerUserQuotaBytes(100)
	ctx := context.Background()

	hostDir := makeSizedSource(t, "big", 1<<20)
	if _, err := store.Create(ctx, CreateOptions{
		Name: "linked", UserID: "u", CreatorType: CallerHuman,
		ExternalPath: hostDir,
	}); err != nil {
		t.Fatalf("linked create rejected: %v", err)
	}
	if u, _ := store.UserUsage(ctx, "u"); u != 0 {
		t.Fatalf("linked volume contributed %d bytes to usage", u)
	}
}

func TestFilesystemStore_Quota_DefaultDisabled(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()
	src := makeSizedSource(t, "any", 10_000)
	if _, err := store.Create(ctx, CreateOptions{
		Name: "v1", UserID: "u", CreatorType: CallerHuman, CopyFromPath: src,
	}); err != nil {
		t.Fatalf("create with no quota: %v", err)
	}
}

func TestFilesystemStore_RefreshSize_TracksOnDiskBytes(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, CreateOptions{
		Name: "empty", UserID: "u", CreatorType: CallerHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := store.Get(ctx, id)
	if v.SizeBytes != 0 {
		t.Fatalf("freshly created empty volume should have size 0, got %d", v.SizeBytes)
	}

	dataDir := filepath.Join(store.root, id, "data")
	if writeErr := os.WriteFile(filepath.Join(dataDir, "file"), make([]byte, 2048), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	sz, err := store.RefreshSize(ctx, id)
	if err != nil {
		t.Fatalf("RefreshSize: %v", err)
	}
	if sz != 2048 {
		t.Fatalf("RefreshSize = %d, want 2048", sz)
	}
	v, _ = store.Get(ctx, id)
	if v.SizeBytes != 2048 {
		t.Fatalf("Get after refresh shows size %d, want 2048", v.SizeBytes)
	}
}

func TestFilesystemStore_RefreshSize_LinkedReportsZero(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()
	hostDir := makeSizedSource(t, "big", 4096)
	id, err := store.Create(ctx, CreateOptions{
		Name: "linked", UserID: "u", CreatorType: CallerHuman,
		ExternalPath: hostDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	sz, err := store.RefreshSize(ctx, id)
	if err != nil {
		t.Fatalf("RefreshSize: %v", err)
	}
	if sz != 0 {
		t.Fatalf("RefreshSize for linked = %d, want 0", sz)
	}
}

func TestMemoryStore_UserUsage_TracksStoredSize(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	id, err := m.Create(ctx, CreateOptions{Name: "v", UserID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.volumes[id].SizeBytes = 1234
	m.mu.Unlock()

	got, err := m.UserUsage(ctx, "u")
	if err != nil {
		t.Fatal(err)
	}
	if got != 1234 {
		t.Fatalf("UserUsage = %d, want 1234", got)
	}
}

func TestFilesystemStore_QuotaSerializesPerUser(t *testing.T) {
	store := newTestFilesystemStore(t)
	store.SetPerUserQuotaBytes(1500)

	srcA := makeSizedSource(t, "a", 1000)
	srcB := makeSizedSource(t, "b", 1000)

	ctx := context.Background()
	var (
		errA, errB error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errA = store.Create(ctx, CreateOptions{Name: "v1", UserID: "u", CreatorType: CallerHuman, CopyFromPath: srcA})
	}()
	go func() {
		defer wg.Done()
		_, errB = store.Create(ctx, CreateOptions{Name: "v2", UserID: "u", CreatorType: CallerHuman, CopyFromPath: srcB})
	}()
	wg.Wait()

	successes := 0
	rejects := 0
	for _, err := range []error{errA, errB} {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrQuotaExceeded):
			rejects++
		default:
			t.Fatalf("unexpected error from concurrent create: %v", err)
		}
	}
	if successes != 1 || rejects != 1 {
		t.Fatalf("concurrent same-user create did not serialize: successes=%d rejects=%d", successes, rejects)
	}
}

func TestFilesystemStore_RejectsCallerSuppliedTraversalID(t *testing.T) {
	store := newTestFilesystemStore(t)
	for _, badID := range []string{
		"../../etc",
		"vol-no-trailing-slash/../..",
		"absolute/path",
		"vol-bad/slash",
		"vol-bad.dots",
		"\x00null-byte",
		"",
		"x",
	} {
		if badID == "" {
			continue
		}
		t.Run(badID, func(t *testing.T) {
			_, err := store.Create(context.Background(), CreateOptions{
				ID:          badID,
				Name:        "v",
				UserID:      "u",
				CreatorType: CallerHuman,
			})
			if !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("expected ErrInvalidOptions for caller-supplied ID %q, got %v", badID, err)
			}
		})
	}
}

func TestFilesystemStore_AcceptsCompliantCallerSuppliedID(t *testing.T) {
	store := newTestFilesystemStore(t)
	id, err := store.Create(context.Background(), CreateOptions{
		ID:          "vol-0123456789abcdef",
		Name:        "v",
		UserID:      "u",
		CreatorType: CallerHuman,
	})
	if err != nil {
		t.Fatalf("compliant caller-supplied ID rejected: %v", err)
	}
	if id != "vol-0123456789abcdef" {
		t.Fatalf("ID = %q, want vol-0123456789abcdef", id)
	}
}

func TestFilesystemStore_RefreshAllSizes(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	idMan, err := store.Create(ctx, CreateOptions{Name: "managed", UserID: "u", CreatorType: CallerHuman})
	if err != nil {
		t.Fatal(err)
	}
	hostDir := makeSizedSource(t, "host", 100)
	if _, linkErr := store.Create(ctx, CreateOptions{Name: "linked", UserID: "u", CreatorType: CallerHuman, ExternalPath: hostDir}); linkErr != nil {
		t.Fatal(linkErr)
	}

	dataDir := filepath.Join(store.root, idMan, "data")
	if writeErr := os.WriteFile(filepath.Join(dataDir, "x"), make([]byte, 4096), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	v, _ := store.Get(ctx, idMan)
	if v.SizeBytes != 0 {
		t.Fatalf("pre-refresh size = %d, want 0", v.SizeBytes)
	}

	n, err := store.RefreshAllSizes(ctx)
	if err != nil {
		t.Fatalf("RefreshAllSizes: %v", err)
	}
	if n != 1 {
		t.Fatalf("refreshed = %d, want 1 (only the managed volume)", n)
	}
	v, _ = store.Get(ctx, idMan)
	if v.SizeBytes != 4096 {
		t.Fatalf("post-refresh size = %d, want 4096", v.SizeBytes)
	}
}
