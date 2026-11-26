// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolAuthStateRepo_PutAndTake(t *testing.T) {
	db := newTestDB(t)
	repo := NewToolAuthStateRepo(db)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	if err := repo.Put(ctx, "state-1", []byte(`{"credential_key":"k1"}`), now+60_000, now); err != nil {
		t.Fatalf("Put: %v", err)
	}

	data, ok, err := repo.Take(ctx, "state-1", now)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if !ok {
		t.Fatal("Take: expected found")
	}
	if string(data) != `{"credential_key":"k1"}` {
		t.Errorf("Take data = %q", string(data))
	}
}

func TestToolAuthStateRepo_TakeIsOneShot(t *testing.T) {
	db := newTestDB(t)
	repo := NewToolAuthStateRepo(db)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	_ = repo.Put(ctx, "state-1", []byte("payload"), now+60_000, now)

	if _, ok, _ := repo.Take(ctx, "state-1", now); !ok {
		t.Fatal("first Take should succeed")
	}
	if _, ok, _ := repo.Take(ctx, "state-1", now); ok {
		t.Error("second Take should miss (state consumed)")
	}
}

func TestToolAuthStateRepo_TakeMissing(t *testing.T) {
	db := newTestDB(t)
	repo := NewToolAuthStateRepo(db)
	now := time.Now().UnixMilli()

	if _, ok, err := repo.Take(context.Background(), "absent", now); err != nil || ok {
		t.Errorf("Take(absent) = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

func TestToolAuthStateRepo_ExpiredNotReturned(t *testing.T) {
	db := newTestDB(t)
	repo := NewToolAuthStateRepo(db)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	_ = repo.Put(ctx, "state-old", []byte("payload"), now-1_000, now)

	if _, ok, err := repo.Take(ctx, "state-old", now); err != nil || ok {
		t.Errorf("Take(expired) = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

func TestToolAuthStateRepo_PutSweepsExpired(t *testing.T) {
	db := newTestDB(t)
	repo := NewToolAuthStateRepo(db)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	_ = repo.Put(ctx, "stale", []byte("x"), now-10_000, now-20_000)
	_ = repo.Put(ctx, "fresh", []byte("y"), now+60_000, now)

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tool_auth_state`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1 (expired row swept on Put)", count)
	}
}

func TestToolAuthStateRepo_TakeConcurrentOnlyOneWins(t *testing.T) {
	db := newTestDB(t)
	repo := NewToolAuthStateRepo(db)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	_ = repo.Put(ctx, "state-1", []byte("payload"), now+60_000, now)

	const racers = 8
	var wins int64
	var wg sync.WaitGroup
	for range racers {
		wg.Go(func() {
			if _, ok, err := repo.Take(ctx, "state-1", now); err == nil && ok {
				atomic.AddInt64(&wins, 1)
			}
		})
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("concurrent Take winners = %d, want exactly 1", wins)
	}
}

func TestToolAuthStateRepo_ErrorsOnClosedDB(t *testing.T) {
	db := newTestDB(t)
	repo := NewToolAuthStateRepo(db)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	if err := repo.Put(ctx, "s", []byte("x"), now+1000, now); err == nil {
		t.Error("Put on a closed DB should return an error")
	}
	if _, ok, err := repo.Take(ctx, "s", now); err == nil || ok {
		t.Errorf("Take on a closed DB = (ok=%v, err=%v), want (false, non-nil error)", ok, err)
	}
}

func TestToolAuthStateRepo_PutUpserts(t *testing.T) {
	db := newTestDB(t)
	repo := NewToolAuthStateRepo(db)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	_ = repo.Put(ctx, "state-1", []byte("first"), now+60_000, now)
	_ = repo.Put(ctx, "state-1", []byte("second"), now+60_000, now)

	data, ok, err := repo.Take(ctx, "state-1", now)
	if err != nil || !ok {
		t.Fatalf("Take after upsert: ok=%v err=%v", ok, err)
	}
	if string(data) != "second" {
		t.Errorf("data = %q, want %q (upsert overwrote)", string(data), "second")
	}
}
