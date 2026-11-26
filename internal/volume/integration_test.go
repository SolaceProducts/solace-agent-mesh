// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"sync"
	"testing"
	"time"
)

type storeFactory func(t *testing.T) Service

func memoryFactory(t *testing.T) Service {
	t.Helper()
	return NewMemoryStore()
}

func filesystemFactory(t *testing.T) Service {
	t.Helper()
	root := t.TempDir()
	store, err := NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func runForBothStores(t *testing.T, name string, fn func(t *testing.T, svc Service)) {
	t.Helper()
	for _, tc := range []struct {
		name    string
		factory storeFactory
	}{
		{"Memory", memoryFactory},
		{"Filesystem", filesystemFactory},
	} {
		t.Run(tc.name+"/"+name, func(t *testing.T) {
			fn(t, tc.factory(t))
		})
	}
}

func TestBothStores_SessionAffinityBasic(t *testing.T) {
	runForBothStores(t, "Basic", func(t *testing.T, svc Service) {
		ctx := context.Background()

		id1, _ := svc.Create(ctx, CreateOptions{Name: "v1", UserID: "user1", CreatorType: CallerHuman})
		id2, _ := svc.Create(ctx, CreateOptions{Name: "v2", UserID: "user1", CreatorType: CallerHuman})
		_, _ = svc.Create(ctx, CreateOptions{Name: "v3", UserID: "user1", CreatorType: CallerHuman})

		if err := svc.AssociateSession(ctx, id1, "sess-A"); err != nil {
			t.Fatalf("AssociateSession: %v", err)
		}
		if err := svc.AssociateSession(ctx, id2, "sess-A"); err != nil {
			t.Fatalf("AssociateSession: %v", err)
		}

		if err := svc.AssociateSession(ctx, id1, "sess-A"); err != nil {
			t.Fatalf("AssociateSession idempotent: %v", err)
		}

		ids, err := svc.ListSessionVolumes(ctx, "sess-A")
		if err != nil {
			t.Fatalf("ListSessionVolumes: %v", err)
		}
		if len(ids) != 2 {
			t.Errorf("got %d session volumes, want 2", len(ids))
		}

		sid := "sess-A"
		vols, err := svc.List(ctx, "user1", ListFilter{SessionID: &sid})
		if err != nil {
			t.Fatalf("List with SessionID: %v", err)
		}
		if len(vols) != 2 {
			t.Errorf("got %d filtered volumes, want 2", len(vols))
		}

		noSID := "sess-Z"
		vols, err = svc.List(ctx, "user1", ListFilter{SessionID: &noSID})
		if err != nil {
			t.Fatalf("List empty session: %v", err)
		}
		if len(vols) != 0 {
			t.Errorf("got %d volumes for empty session, want 0", len(vols))
		}
	})
}

func TestBothStores_DeleteCascadesSessionAssociations(t *testing.T) {
	runForBothStores(t, "DeleteCascade", func(t *testing.T, svc Service) {
		ctx := context.Background()

		id, _ := svc.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerAgent})
		_ = svc.AssociateSession(ctx, id, "sess-1")
		_ = svc.AssociateSession(ctx, id, "sess-2")

		if err := svc.Delete(ctx, id, CallerAgent); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		ids1, _ := svc.ListSessionVolumes(ctx, "sess-1")
		ids2, _ := svc.ListSessionVolumes(ctx, "sess-2")
		if len(ids1) != 0 || len(ids2) != 0 {
			t.Error("session associations not cleaned up after delete")
		}
	})
}

func TestBothStores_CleanupExpiredCascadesSessionAssociations(t *testing.T) {
	runForBothStores(t, "CleanupCascade", func(t *testing.T, svc Service) {
		ctx := context.Background()

		shortTTL := 1 * time.Nanosecond
		id, _ := svc.Create(ctx, CreateOptions{
			Name: "expiring", UserID: "u", CreatorType: CallerHuman, TTL: &shortTTL,
		})
		_ = svc.AssociateSession(ctx, id, "sess-1")

		time.Sleep(1 * time.Millisecond)

		removed, err := svc.CleanupExpired(ctx)
		if err != nil {
			t.Fatalf("CleanupExpired: %v", err)
		}
		if removed != 1 {
			t.Errorf("removed = %d, want 1", removed)
		}

		ids, _ := svc.ListSessionVolumes(ctx, "sess-1")
		if len(ids) != 0 {
			t.Error("session associations not cleaned up after expiry cleanup")
		}
	})
}

func TestBothStores_ListFilterAllCombinations(t *testing.T) {
	runForBothStores(t, "FilterCombinations", func(t *testing.T, svc Service) {
		ctx := context.Background()

		id1, _ := svc.Create(ctx, CreateOptions{Name: "human-sess", UserID: "u", CreatorType: CallerHuman})
		id2, _ := svc.Create(ctx, CreateOptions{Name: "agent-sess", UserID: "u", CreatorType: CallerAgent})
		_, _ = svc.Create(ctx, CreateOptions{Name: "human-nosess", UserID: "u", CreatorType: CallerHuman})
		_, _ = svc.Create(ctx, CreateOptions{Name: "agent-nosess", UserID: "u", CreatorType: CallerAgent})

		_ = svc.AssociateSession(ctx, id1, "sess-X")
		_ = svc.AssociateSession(ctx, id2, "sess-X")

		vols, _ := svc.List(ctx, "u", ListFilter{})
		if len(vols) != 4 {
			t.Errorf("no filter: got %d, want 4", len(vols))
		}

		ct := CallerHuman
		vols, _ = svc.List(ctx, "u", ListFilter{CreatorType: &ct})
		if len(vols) != 2 {
			t.Errorf("human filter: got %d, want 2", len(vols))
		}

		ct = CallerAgent
		vols, _ = svc.List(ctx, "u", ListFilter{CreatorType: &ct})
		if len(vols) != 2 {
			t.Errorf("agent filter: got %d, want 2", len(vols))
		}

		sid := "sess-X"
		vols, _ = svc.List(ctx, "u", ListFilter{SessionID: &sid})
		if len(vols) != 2 {
			t.Errorf("session filter: got %d, want 2", len(vols))
		}

		ct = CallerHuman
		vols, _ = svc.List(ctx, "u", ListFilter{CreatorType: &ct, SessionID: &sid})
		if len(vols) != 1 {
			t.Errorf("human+session filter: got %d, want 1", len(vols))
		}
		if vols[0].Name != "human-sess" {
			t.Errorf("expected human-sess, got %q", vols[0].Name)
		}

		ct = CallerAgent
		vols, _ = svc.List(ctx, "u", ListFilter{CreatorType: &ct, SessionID: &sid})
		if len(vols) != 1 {
			t.Errorf("agent+session filter: got %d, want 1", len(vols))
		}
		if vols[0].Name != "agent-sess" {
			t.Errorf("expected agent-sess, got %q", vols[0].Name)
		}
	})
}

func TestBothStores_ConcurrentSessionAssociation(t *testing.T) {
	runForBothStores(t, "ConcurrentAssociate", func(t *testing.T, svc Service) {
		ctx := context.Background()

		id, _ := svc.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})

		var wg sync.WaitGroup
		for i := range 10 {
			wg.Go(func() {
				sessID := "sess-" + string(rune('A'+i))
				if err := svc.AssociateSession(ctx, id, sessID); err != nil {
					t.Errorf("goroutine %d: AssociateSession: %v", i, err)
				}
			})
		}
		wg.Wait()

		for i := range 10 {
			sessID := "sess-" + string(rune('A'+i))
			ids, err := svc.ListSessionVolumes(ctx, sessID)
			if err != nil {
				t.Errorf("ListSessionVolumes(%q): %v", sessID, err)
			}
			if len(ids) != 1 || ids[0] != id {
				t.Errorf("session %q: got %v, want [%s]", sessID, ids, id)
			}
		}
	})
}

func TestBothStores_ConcurrentCreateAndList(t *testing.T) {
	runForBothStores(t, "ConcurrentCreateList", func(t *testing.T, svc Service) {
		ctx := context.Background()

		var wg sync.WaitGroup
		for i := range 10 {
			wg.Go(func() {
				_, _ = svc.Create(ctx, CreateOptions{
					Name: "conc-" + string(rune('A'+i)), UserID: "u", CreatorType: CallerHuman,
				})
			})
		}

		wg.Go(func() {
			for range 5 {
				_, _ = svc.List(ctx, "u", ListFilter{})
			}
		})

		wg.Wait()

		vols, _ := svc.List(ctx, "u", ListFilter{})
		if len(vols) != 10 {
			t.Errorf("got %d volumes, want 10", len(vols))
		}
	})
}

func TestFilesystemStore_SessionAffinityPersistence(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()

	store1, err := NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}

	id, _ := store1.Create(ctx, CreateOptions{Name: "v", UserID: "u", CreatorType: CallerHuman})
	_ = store1.AssociateSession(ctx, id, "sess-persist")

	ids1, _ := store1.ListSessionVolumes(ctx, "sess-persist")
	if len(ids1) != 1 {
		t.Fatalf("before close: got %d session volumes, want 1", len(ids1))
	}

	_ = store1.Close()

	store2, err := NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore (reopen): %v", err)
	}
	defer func() { _ = store2.Close() }()

	ids2, err := store2.ListSessionVolumes(ctx, "sess-persist")
	if err != nil {
		t.Fatalf("ListSessionVolumes after reopen: %v", err)
	}
	if len(ids2) != 1 || ids2[0] != id {
		t.Errorf("after reopen: got %v, want [%s]", ids2, id)
	}

	v, err := store2.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if v.Name != "v" {
		t.Errorf("Name = %q after reopen", v.Name)
	}
}
