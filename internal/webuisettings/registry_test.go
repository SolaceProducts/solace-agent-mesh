// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webuisettings

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistry_NilFetcherResolvesToNoOverrides(t *testing.T) {
	r := NewRegistry(nil, nil)
	row, err := r.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row != nil {
		t.Errorf("row = %+v, want nil (falls through to caller's YAML layer)", row)
	}
}

func TestRegistry_FetchesOnceThenCaches(t *testing.T) {
	var calls int32
	fetcher := func(context.Context) (*Settings, error) {
		atomic.AddInt32(&calls, 1)
		return &Settings{AppName: sp("Acme")}, nil
	}
	r := NewRegistry(fetcher, nil)

	for i := 0; i < 5; i++ {
		row, err := r.Get(context.Background())
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if row == nil || row.AppName == nil || *row.AppName != "Acme" {
			t.Fatalf("Get() = %+v", row)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("fetcher called %d times, want 1 (steady-state reads must not hit the fetcher)", got)
	}
}

func TestRegistry_NoOverridesCachesAsConfirmedEmpty(t *testing.T) {
	var calls int32
	fetcher := func(context.Context) (*Settings, error) {
		atomic.AddInt32(&calls, 1)
		return nil, nil
	}
	r := NewRegistry(fetcher, nil)

	for i := 0; i < 3; i++ {
		row, err := r.Get(context.Background())
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if row != nil {
			t.Errorf("row = %+v, want nil", row)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("fetcher called %d times, want 1 — a confirmed-empty result must not re-fetch on every read", got)
	}
}

func TestRegistry_FetchErrorLeavesPreviousValueInPlace(t *testing.T) {
	calls := 0
	fetcher := func(context.Context) (*Settings, error) {
		calls++
		if calls == 1 {
			return &Settings{AppName: sp("Acme")}, nil
		}
		return nil, errors.New("broker unreachable")
	}
	r := NewRegistry(fetcher, nil)

	row, err := r.Get(context.Background())
	if err != nil || row == nil || row.AppName == nil || *row.AppName != "Acme" {
		t.Fatalf("first Get() = (%+v, %v)", row, err)
	}

	r.Invalidate()
	if _, err := r.Get(context.Background()); err == nil {
		t.Fatal("expected the forced re-fetch to surface the fetcher error")
	}

}

func TestRegistry_InvalidateDuringInFlightFetchIsNotLost(t *testing.T) {
	var calls int32
	entered := make(chan struct{})
	release := make(chan struct{})
	fetcher := func(context.Context) (*Settings, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			close(entered)
			<-release
		}
		return &Settings{AppName: sp(fmt.Sprintf("v%d", n))}, nil
	}
	r := NewRegistry(fetcher, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.Get(context.Background())
	}()

	<-entered
	r.Invalidate()
	close(release)
	<-done

	row, err := r.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after the raced invalidation: %v", err)
	}
	if row == nil || row.AppName == nil {
		t.Fatalf("Get() = %+v, want a re-fetched value", row)
	}
	if *row.AppName == "v1" {
		t.Error("cache served v1: the invalidation that landed mid-fetch was lost, and with no TTL this value would persist until the next write or restart")
	}
}

func TestRegistry_FailedFetchIsNegativelyCached(t *testing.T) {
	var calls int32
	fetcher := func(context.Context) (*Settings, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("platform unreachable")
	}
	r := NewRegistry(fetcher, nil)
	r.failureWindow = time.Minute

	for i := 0; i < 5; i++ {
		if _, err := r.Get(context.Background()); err == nil {
			t.Fatal("expected an error while the platform is unreachable")
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("fetcher called %d times across 5 reads, want 1 (negative cache not suppressing retries)", got)
	}

	r.Invalidate()
	if _, err := r.Get(context.Background()); err == nil {
		t.Fatal("expected the post-invalidate read to attempt a real fetch")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("fetcher called %d times after Invalidate, want 2 (Invalidate must lift negative-cache suppression)", got)
	}
}

func TestRegistry_NegativeCacheExpiryAllowsRecovery(t *testing.T) {
	var calls int32
	fetcher := func(context.Context) (*Settings, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, errors.New("platform unreachable")
		}
		return &Settings{AppName: sp("Recovered")}, nil
	}
	r := NewRegistry(fetcher, nil)
	r.failureWindow = 0

	if _, err := r.Get(context.Background()); err == nil {
		t.Fatal("expected the first read to surface the fetch error")
	}
	row, err := r.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after the window lapsed: %v", err)
	}
	if row == nil || row.AppName == nil || *row.AppName != "Recovered" {
		t.Errorf("Get() = %+v, want the recovered value", row)
	}
}

func startRefreshLoop(t *testing.T, r *Registry) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.RunRefreshLoop(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func peek(r *Registry) (*Settings, bool) {
	cached := r.cached.Load()
	if cached == sentinelEmpty {
		return nil, true
	}
	return cached, cached != nil
}

func TestRegistry_RefreshKeepsCachedValueWhenTheFetchFails(t *testing.T) {
	var calls atomic.Int32
	r := NewRegistry(func(context.Context) (*Settings, error) {
		if calls.Add(1) == 1 {
			return &Settings{AppName: sp("Acme")}, nil
		}
		return nil, errors.New("platform read fault")
	}, nil)
	r.retryInitial, r.retryMax = time.Hour, time.Hour

	if row, err := r.Get(t.Context()); err != nil || row == nil || *row.AppName != "Acme" {
		t.Fatalf("priming Get() = (%+v, %v)", row, err)
	}
	startRefreshLoop(t, r)

	r.Refresh()
	waitFor(t, func() bool { return calls.Load() >= 2 })

	row, ok := peek(r)
	if !ok || row == nil || row.AppName == nil || *row.AppName != "Acme" {
		t.Errorf("Cached() = (%+v, %v); want the previous row retained", row, ok)
	}
}

func TestRegistry_RefreshInstallsTheNewRowOnSuccess(t *testing.T) {
	var calls atomic.Int32
	r := NewRegistry(func(context.Context) (*Settings, error) {
		return &Settings{AppName: sp(fmt.Sprintf("v%d", calls.Add(1)))}, nil
	}, nil)

	if _, err := r.Get(t.Context()); err != nil {
		t.Fatalf("priming Get: %v", err)
	}
	startRefreshLoop(t, r)
	r.Refresh()
	waitFor(t, func() bool {
		row, ok := peek(r)
		return ok && row != nil && row.AppName != nil && *row.AppName == "v2"
	}, "the refreshed row")
}

func TestRegistry_ResyncPullsWithoutANotice(t *testing.T) {
	var calls atomic.Int32
	r := NewRegistry(func(context.Context) (*Settings, error) {
		return &Settings{AppName: sp(fmt.Sprintf("v%d", calls.Add(1)))}, nil
	}, nil)
	r.resync = time.Millisecond

	if _, err := r.Get(t.Context()); err != nil {
		t.Fatalf("priming Get: %v", err)
	}
	startRefreshLoop(t, r)
	waitFor(t, func() bool {
		row, ok := peek(r)
		return ok && row != nil && row.AppName != nil && *row.AppName != "v1"
	}, "a row pulled by the resync")
}

func TestRegistry_RefreshOfAClearedRowReadsBackAsEmpty(t *testing.T) {
	var cleared atomic.Bool
	var calls atomic.Int32
	r := NewRegistry(func(context.Context) (*Settings, error) {
		calls.Add(1)
		if cleared.Load() {
			return &Settings{}, nil
		}
		return &Settings{AppName: sp("Acme")}, nil
	}, nil)

	if _, err := r.Get(t.Context()); err != nil {
		t.Fatalf("priming Get: %v", err)
	}
	startRefreshLoop(t, r)
	cleared.Store(true)
	r.Refresh()
	waitFor(t, func() bool {
		row, ok := peek(r)
		return ok && row == nil
	}, "the cleared row cached as no overrides")

	before := calls.Load()
	for range 3 {
		if row, err := r.Get(t.Context()); err != nil || row != nil {
			t.Fatalf("Get() = (%+v, %v), want (nil, nil)", row, err)
		}
	}
	if got := calls.Load() - before; got != 0 {
		t.Errorf("fetcher called %d times after the refresh, want 0", got)
	}
}

func TestRegistry_FailedRefreshIsRetriedUntilItSucceeds(t *testing.T) {
	var calls atomic.Int32
	r := NewRegistry(func(context.Context) (*Settings, error) {
		if calls.Add(1) <= 3 {
			return nil, errors.New("platform read fault")
		}
		return &Settings{AppName: sp("recovered")}, nil
	}, nil)
	r.retryInitial, r.retryMax = time.Millisecond, 5*time.Millisecond
	startRefreshLoop(t, r)

	r.Refresh()
	waitFor(t, func() bool {
		row, ok := peek(r)
		return ok && row != nil && row.AppName != nil && *row.AppName == "recovered"
	}, "the row after three failed pulls")
	if got := calls.Load(); got != 4 {
		t.Errorf("fetcher called %d times, want 4 (three failures, one success)", got)
	}
}

func TestRegistry_NoticesDuringARefreshCauseExactlyOneMore(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	r := NewRegistry(func(context.Context) (*Settings, error) {
		n := calls.Add(1)
		entered <- struct{}{}
		if n == 2 {
			<-release
		}
		return &Settings{AppName: sp(fmt.Sprintf("v%d", n))}, nil
	}, nil)
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("priming Get: %v", err)
	}
	<-entered
	startRefreshLoop(t, r)

	r.Refresh()
	<-entered
	for range 5 {
		r.Refresh()
	}
	close(release)

	waitFor(t, func() bool {
		row, ok := peek(r)
		return ok && row != nil && row.AppName != nil && *row.AppName == "v3"
	}, "one more pull after the first refresh")
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 3 {
		t.Errorf("fetcher called %d times, want 3 (priming Get, the refresh, one more); the notices during the refresh must collapse into one", got)
	}
}

func TestRegistry_RefreshBeatsAnInFlightGet(t *testing.T) {
	getEntered := make(chan struct{})
	releaseGet := make(chan struct{})
	var calls atomic.Int32
	r := NewRegistry(func(context.Context) (*Settings, error) {
		if calls.Add(1) == 1 {
			close(getEntered)
			<-releaseGet
			return &Settings{AppName: sp("before-the-write")}, nil
		}
		return &Settings{AppName: sp("after-the-write")}, nil
	}, nil)
	startRefreshLoop(t, r)

	getDone := make(chan struct{})
	go func() {
		defer close(getDone)
		_, _ = r.Get(t.Context())
	}()
	<-getEntered

	r.Refresh()
	waitFor(t, func() bool {
		row, ok := peek(r)
		return ok && row != nil && row.AppName != nil && *row.AppName == "after-the-write"
	}, "the refreshed row")
	close(releaseGet)
	<-getDone

	row, _ := peek(r)
	if row == nil || row.AppName == nil || *row.AppName != "after-the-write" {
		t.Errorf("Cached() = %+v; the older in-flight Get overwrote the refreshed row", row)
	}
}

func TestRegistry_RefreshIsNotSwallowedByAnInFlightWarm(t *testing.T) {
	var fetches atomic.Int32
	release := make(chan struct{})
	r := NewRegistry(func(context.Context) (*Settings, error) {
		fetches.Add(1)
		<-release
		return &Settings{AppName: sp("pulled")}, nil
	}, nil)
	startRefreshLoop(t, r)
	t.Cleanup(func() { close(release) })

	r.Warm()
	waitFor(t, func() bool { return fetches.Load() == 1 })

	r.Refresh()

	waitFor(t, func() bool { return fetches.Load() == 2 },
		"a second pull: the refresh was swallowed by the in-flight warm")
}

func TestRegistry_WarmPullsAgainOnceTheFirstFinishes(t *testing.T) {
	var fetches atomic.Int32
	r := NewRegistry(func(context.Context) (*Settings, error) {
		n := fetches.Add(1)
		return &Settings{AppName: sp(fmt.Sprintf("v%d", n))}, nil
	}, nil)

	r.Warm()
	waitFor(t, func() bool { return fetches.Load() == 1 && !r.warming.Load() },
		"the first warm pulling and releasing its flag")

	r.Invalidate()
	r.Warm()
	waitFor(t, func() bool { return fetches.Load() == 2 },
		"a second pull after the first finished — a warm flag that is never released leaves every later miss unfilled")
}

func TestRegistry_ConcurrentWarmsCoalesce(t *testing.T) {
	var fetches atomic.Int32
	release := make(chan struct{})
	r := NewRegistry(func(context.Context) (*Settings, error) {
		fetches.Add(1)
		<-release
		return &Settings{AppName: sp("pulled")}, nil
	}, nil)

	r.Warm()
	waitFor(t, func() bool { return fetches.Load() == 1 })

	parked := runtime.NumGoroutine()
	for range 20 {
		r.Warm()
	}
	time.Sleep(50 * time.Millisecond)
	extra := runtime.NumGoroutine() - parked
	close(release)

	if extra > 5 {
		t.Errorf("a burst of 20 warms left %d extra goroutines parked in the fetcher, want ~0 — each miss on a hot path would pile one up", extra)
	}
}

func waitFor(t *testing.T, cond func() bool, what ...string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(what) > 0 {
		t.Fatalf("never observed: %s", what[0])
	}
	t.Fatal("condition not met within the deadline")
}

func TestRegistry_InvalidateCausesExactlyOneRefetchUnderConcurrentReaders(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	fetcher := func(context.Context) (*Settings, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return &Settings{AppName: sp("Acme")}, nil
	}
	r := NewRegistry(fetcher, nil)

	closedRelease := make(chan struct{})
	close(closedRelease)
	r.fetcher = func(context.Context) (*Settings, error) {
		return &Settings{AppName: sp("prime")}, nil
	}
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("priming Get: %v", err)
	}
	r.fetcher = fetcher
	r.Invalidate()

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Get(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Get failed: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("fetcher called %d times under a concurrent burst, want 1 (singleflight coalescing broken)", got)
	}
}

func TestRegistry_InvalidateThenGetRefetches(t *testing.T) {
	var calls int32
	fetcher := func(context.Context) (*Settings, error) {
		n := atomic.AddInt32(&calls, 1)
		return &Settings{AppName: sp(fmt.Sprintf("v%d", n))}, nil
	}
	r := NewRegistry(fetcher, nil)

	first, _ := r.Get(context.Background())
	r.Invalidate()
	second, _ := r.Get(context.Background())

	if *first.AppName == *second.AppName {
		t.Errorf("expected a re-fetch after Invalidate, got the same cached value %q twice", *first.AppName)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("fetcher called %d times across two reads separated by Invalidate, want 2", got)
	}
}
