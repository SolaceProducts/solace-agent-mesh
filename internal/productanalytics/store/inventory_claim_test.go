// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"sync"
	"testing"
)

const otherMarker = "22222222-2222-2222-2222-222222222222"

func TestClaimInventoryDayIsGrantedOnce(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	first, err := repo.ClaimInventoryDay(ctx, grantedMarker, "2026-08-21")
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("the first caller of the day was refused the claim")
	}
	second, err := repo.ClaimInventoryDay(ctx, grantedMarker, "2026-08-21")
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Error("a second caller took the same day's claim, which is the duplicate this exists to prevent")
	}
}

func TestClaimInventoryDayIsGrantedAgainTheNextDay(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	for _, day := range []string{"2026-08-21", "2026-08-22", "2026-08-23"} {
		claimed, err := repo.ClaimInventoryDay(ctx, grantedMarker, day)
		if err != nil {
			t.Fatal(err)
		}
		if !claimed {
			t.Errorf("day %s was refused, want one claim per day", day)
		}
	}
}

func TestClaimInventoryDayRefusesAnEarlierDay(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	if _, err := repo.ClaimInventoryDay(ctx, grantedMarker, "2026-08-22"); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimInventoryDay(ctx, grantedMarker, "2026-08-21")
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Error("a day earlier than the one already claimed was granted")
	}
}

func TestClaimInventoryDayFollowsTheMarker(t *testing.T) {
	db := newTestDB(t)
	repo := NewProductAnalyticsRepo(db)
	ctx := context.Background()

	if _, err := repo.ClaimInventoryDay(ctx, grantedMarker, "2026-08-21"); err != nil {
		t.Fatal(err)
	}
	rotated, err := repo.ClaimInventoryDay(ctx, otherMarker, "2026-08-21")
	if err != nil {
		t.Fatal(err)
	}
	if !rotated {
		t.Fatal("a rotated marker was refused the day the previous marker claimed")
	}

	rows, err := countClaims(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d claim rows survive, want the replaced marker's row pruned", rows)
	}
}

func TestClaimInventoryDayIsSafeUnderConcurrency(t *testing.T) {
	repo := NewProductAnalyticsRepo(newTestDB(t))
	ctx := context.Background()

	const racers = 8
	var mu sync.Mutex
	won := 0
	var wg sync.WaitGroup
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := repo.ClaimInventoryDay(ctx, grantedMarker, "2026-08-21")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if claimed {
				won++
			}
		}()
	}
	wg.Wait()

	if won != 1 {
		t.Errorf("%d of %d concurrent callers took the claim, want exactly 1", won, racers)
	}
}

func countClaims(ctx context.Context, db *DB) (int, error) {
	var n int
	row := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM product_analytics_inventory_claim`)
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
