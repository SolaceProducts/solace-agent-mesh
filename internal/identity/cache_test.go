// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTLCache_HitReusesValueWithinTTL(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Hour, 0)
	var calls atomic.Int32
	loader := func(ctx context.Context) (int, error) {
		calls.Add(1)
		return 42, nil
	}
	for range 3 {
		v, err := c.getOrLoad(context.Background(), "k", loader)
		if err != nil {
			t.Fatalf("getOrLoad: %v", err)
		}
		if v != 42 {
			t.Fatalf("value = %d, want 42", v)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("loader calls = %d, want 1 (TTL caching broken)", got)
	}
}

func TestTTLCache_ExpiryReloads(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](10*time.Millisecond, 0)
	var calls atomic.Int32
	loader := func(ctx context.Context) (int, error) {
		calls.Add(1)
		return int(calls.Load()), nil
	}
	v1, _ := c.getOrLoad(context.Background(), "k", loader)
	time.Sleep(25 * time.Millisecond)
	v2, _ := c.getOrLoad(context.Background(), "k", loader)
	if v1 == v2 {
		t.Errorf("expected reload after TTL, got same value %d", v1)
	}
	if calls.Load() != 2 {
		t.Errorf("loader calls = %d, want 2", calls.Load())
	}
}

func TestTTLCache_ErrorIsNotCached(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Hour, 0)
	var calls atomic.Int32
	wantErr := errors.New("boom")
	loader := func(ctx context.Context) (int, error) {
		calls.Add(1)
		if calls.Load() == 1 {
			return 0, wantErr
		}
		return 7, nil
	}
	_, err := c.getOrLoad(context.Background(), "k", loader)
	if !errors.Is(err, wantErr) {
		t.Fatalf("first call err = %v, want %v", err, wantErr)
	}
	v, err := c.getOrLoad(context.Background(), "k", loader)
	if err != nil {
		t.Fatalf("second call err = %v", err)
	}
	if v != 7 {
		t.Errorf("value = %d, want 7", v)
	}
	if calls.Load() != 2 {
		t.Errorf("expected retry after error, got calls = %d", calls.Load())
	}
}

func TestTTLCache_SingleflightCollapsesConcurrentLoads(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Hour, 0)
	var calls atomic.Int32
	gate := make(chan struct{})
	loader := func(ctx context.Context) (int, error) {
		calls.Add(1)
		<-gate
		return 99, nil
	}

	const n = 20
	results := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			v, _ := c.getOrLoad(context.Background(), "k", loader)
			results[i] = v
		})
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()

	if calls.Load() != 1 {
		t.Errorf("loader calls = %d, want 1 (singleflight collapsing broken)", calls.Load())
	}
	for i, v := range results {
		if v != 99 {
			t.Errorf("results[%d] = %d, want 99", i, v)
		}
	}
}

func TestTTLCache_ColdWait_FirstRequestBlocks(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Hour, 50*time.Millisecond)
	loader := func(ctx context.Context) (int, error) {
		time.Sleep(10 * time.Millisecond)
		return 5, nil
	}
	start := time.Now()
	v, err := c.getOrLoadCold(context.Background(), "k", loader)
	if err != nil {
		t.Fatalf("getOrLoadCold: %v", err)
	}
	if v != 5 {
		t.Fatalf("v = %d, want 5", v)
	}
	if elapsed := time.Since(start); elapsed > 80*time.Millisecond {
		t.Errorf("first call took %v; expected ≤ ~50ms cold wait + loader (10ms)", elapsed)
	}
}

func TestTTLCache_ColdWait_TimesOutOnSlowLoader(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Hour, 30*time.Millisecond)
	loader := func(ctx context.Context) (int, error) {
		time.Sleep(200 * time.Millisecond)
		return 5, nil
	}
	start := time.Now()
	_, err := c.getOrLoadCold(context.Background(), "k", loader)
	elapsed := time.Since(start)
	if !errors.Is(err, errColdCacheTimeout) {
		t.Fatalf("err = %v, want errColdCacheTimeout", err)
	}
	if elapsed < 25*time.Millisecond || elapsed > 80*time.Millisecond {
		t.Errorf("elapsed = %v; expected ~30ms cold wait", elapsed)
	}
}

func TestTTLCache_ColdWait_SurfacesLoaderErrorAndRetriesWithoutNegativeCache(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Hour, 100*time.Millisecond)
	want := errors.New("BambooHR 401 unauthorized")
	loader := func(ctx context.Context) (int, error) {
		return 0, want
	}
	_, err := c.getOrLoadCold(context.Background(), "k", loader)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want loader error %v (not errColdCacheTimeout)", err, want)
	}
	called := 0
	loader2 := func(ctx context.Context) (int, error) {
		called++
		return 0, errors.New("second failure")
	}
	_, err = c.getOrLoadCold(context.Background(), "k", loader2)
	if err == nil || called != 1 {
		t.Errorf("retry did not re-run loader: err=%v called=%d", err, called)
	}
}

func TestTTLCache_ColdWait_ConcurrentWaitersAllSeeLoaderError(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Hour, 200*time.Millisecond)
	want := errors.New("upstream rejected")
	released := make(chan struct{})
	loader := func(ctx context.Context) (int, error) {
		<-released
		return 0, want
	}

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, errs[i] = c.getOrLoadCold(context.Background(), "k", loader)
		})
	}
	time.Sleep(20 * time.Millisecond)
	close(released)
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, want) {
			t.Errorf("waiter %d err = %v, want %v (concurrent waiters must all see the real loader error)", i, err, want)
		}
	}
}

func TestTTLCache_ColdWait_ConcurrentRequestsShareBarrier(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Hour, 100*time.Millisecond)
	var calls atomic.Int32
	gate := make(chan struct{})
	loader := func(ctx context.Context) (int, error) {
		calls.Add(1)
		<-gate
		return 1, nil
	}

	const n = 5
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, errs[i] = c.getOrLoadCold(context.Background(), "k", loader)
		})
	}
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, errColdCacheTimeout) {
			t.Errorf("call %d err = %v, want errColdCacheTimeout (concurrent callers must share the barrier)", i, err)
		}
	}
	close(gate)
	if got := calls.Load(); got != 1 {
		t.Errorf("loader calls = %d, want 1 (concurrent callers must collapse onto a single load)", got)
	}
}

func TestTTLCache_ColdWait_ExpiresAndReloads(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](20*time.Millisecond, 100*time.Millisecond)
	var calls atomic.Int32
	loader := func(ctx context.Context) (int, error) {
		return int(calls.Add(1)), nil
	}
	v1, err := c.getOrLoadCold(context.Background(), "k", loader)
	if err != nil || v1 != 1 {
		t.Fatalf("first call (v=%d, err=%v); want v=1", v1, err)
	}
	time.Sleep(35 * time.Millisecond)
	v2, err := c.getOrLoadCold(context.Background(), "k", loader)
	if err != nil {
		t.Fatalf("second call err = %v", err)
	}
	if v2 == v1 {
		t.Errorf("expected reload after TTL on cold-cache path, got same value %d", v1)
	}
	if calls.Load() != 2 {
		t.Errorf("loader calls = %d, want 2 (TTL must apply to cold-loaded entries)", calls.Load())
	}
}

func TestTTLCache_ColdWait_CachedSecondHit(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Hour, 100*time.Millisecond)
	var calls atomic.Int32
	loader := func(ctx context.Context) (int, error) {
		calls.Add(1)
		return 9, nil
	}
	v1, err := c.getOrLoadCold(context.Background(), "k", loader)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	v2, err := c.getOrLoadCold(context.Background(), "k", loader)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if v1 != 9 || v2 != 9 {
		t.Fatalf("values = (%d,%d), want 9,9", v1, v2)
	}
	if calls.Load() != 1 {
		t.Errorf("loader calls = %d, want 1 (second call must hit the cache)", calls.Load())
	}
}
