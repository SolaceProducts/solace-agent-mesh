// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"sync"
	"testing"
	"time"
)

func TestCacheBasic(t *testing.T) {
	c := NewMapCache()

	if _, ok := c.Get("missing"); ok {
		t.Error("should not find missing key")
	}

	c.Set("key1", "value1")
	v, ok := c.Get("key1")
	if !ok || v != "value1" {
		t.Errorf("get = %v, %v", v, ok)
	}

	c.Set("key1", 42)
	v, ok = c.Get("key1")
	if !ok || v != 42 {
		t.Errorf("get = %v, %v", v, ok)
	}

	c.Delete("key1")
	if _, ok := c.Get("key1"); ok {
		t.Error("should be deleted")
	}
}

func TestCacheTTL(t *testing.T) {
	c := NewMapCache()

	c.Set("expires", "soon", WithTTL(100*time.Millisecond))
	v, ok := c.Get("expires")
	if !ok || v != "soon" {
		t.Errorf("get = %v, %v", v, ok)
	}

	time.Sleep(150 * time.Millisecond)

	if _, ok := c.Get("expires"); ok {
		t.Error("should have expired")
	}
}

func TestCacheOnExpiry(t *testing.T) {
	c := NewMapCache()

	expiredCh := make(chan string, 1)
	c.Set("watched", "data", WithTTL(50*time.Millisecond), WithOnExpiry(func(key string, _ any) {
		expiredCh <- key
	}))

	select {
	case key := <-expiredCh:
		if key != "watched" {
			t.Errorf("expired key = %q", key)
		}
	case <-time.After(time.Second):
		t.Error("expiry callback not fired")
	}
}

func TestCacheOverwriteResetsTimer(t *testing.T) {
	c := NewMapCache()

	c.Set("key", "v1", WithTTL(50*time.Millisecond))
	time.Sleep(30 * time.Millisecond)

	c.Set("key", "v2", WithTTL(100*time.Millisecond))
	time.Sleep(60 * time.Millisecond)

	v, ok := c.Get("key")
	if !ok || v != "v2" {
		t.Errorf("get = %v, %v (should still be present)", v, ok)
	}
}

func TestCacheConcurrent(t *testing.T) {
	c := NewMapCache()
	const n = 100
	var wg sync.WaitGroup
	wg.Add(n * 3)

	for range n {
		go func() { defer wg.Done(); c.Set("key", "val", WithTTL(10*time.Millisecond)) }()
		go func() { defer wg.Done(); c.Get("key") }()
		go func() { defer wg.Done(); c.Delete("key") }()
	}
	wg.Wait()
}

func TestMapCache_GetAndDelete_Atomic(t *testing.T) {
	c := NewMapCache()
	c.Set("k", "v")

	type result struct {
		val any
		ok  bool
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, ok := c.GetAndDelete("k")
			results <- result{v, ok}
		}()
	}
	wg.Wait()
	close(results)

	hits := 0
	misses := 0
	for r := range results {
		if r.ok {
			hits++
			if r.val != "v" {
				t.Errorf("hit returned wrong value: %v", r.val)
			}
		} else {
			misses++
		}
	}
	if hits != 1 || misses != 1 {
		t.Errorf("concurrent GetAndDelete: got hits=%d misses=%d, want hits=1 misses=1", hits, misses)
	}
	if _, ok := c.Get("k"); ok {
		t.Error("key still present after GetAndDelete")
	}
}

func TestMapCache_GetAndDelete_Missing(t *testing.T) {
	c := NewMapCache()
	v, ok := c.GetAndDelete("absent")
	if ok || v != nil {
		t.Errorf("GetAndDelete on absent key = (%v, %v), want (nil, false)", v, ok)
	}
}

func TestMapCache_GetAndDelete_Expired(t *testing.T) {
	c := NewMapCache()
	c.Set("k", "v", WithTTL(1*time.Millisecond))
	time.Sleep(5 * time.Millisecond)
	v, ok := c.GetAndDelete("k")
	if ok || v != nil {
		t.Errorf("GetAndDelete on expired key = (%v, %v), want (nil, false)", v, ok)
	}
}

func TestMapCache_SetIfAbsent_Basic(t *testing.T) {
	c := NewMapCache()

	if !c.SetIfAbsent("k", "first") {
		t.Error("first SetIfAbsent should return true")
	}
	v, ok := c.Get("k")
	if !ok || v != "first" {
		t.Errorf("Get after SetIfAbsent = (%v, %v), want (first, true)", v, ok)
	}

	if c.SetIfAbsent("k", "second") {
		t.Error("second SetIfAbsent should return false (key exists)")
	}
	v, _ = c.Get("k")
	if v != "first" {
		t.Errorf("value should still be first, got %v", v)
	}
}

func TestMapCache_SetIfAbsent_Atomic(t *testing.T) {
	c := NewMapCache()

	results := make(chan bool, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- c.SetIfAbsent("k", struct{}{}, WithTTL(time.Minute))
		}()
	}
	wg.Wait()
	close(results)

	wins := 0
	for stored := range results {
		if stored {
			wins++
		}
	}
	if wins != 1 {
		t.Errorf("concurrent SetIfAbsent: got %d wins, want 1", wins)
	}
}

func TestMapCache_SetIfAbsent_Expired(t *testing.T) {
	c := NewMapCache()
	c.Set("k", "old", WithTTL(1*time.Millisecond))
	time.Sleep(5 * time.Millisecond)

	if !c.SetIfAbsent("k", "new") {
		t.Error("SetIfAbsent should succeed on expired key")
	}
	v, ok := c.Get("k")
	if !ok || v != "new" {
		t.Errorf("Get after SetIfAbsent on expired = (%v, %v), want (new, true)", v, ok)
	}
}

func TestMapCache_DeleteIf(t *testing.T) {
	c := NewMapCache()
	c.Set("k", "owner-A")

	if c.DeleteIf("k", func(v any) bool { return v == "owner-B" }) {
		t.Fatal("pred=false must not delete")
	}
	if _, ok := c.Get("k"); !ok {
		t.Fatal("entry must survive a rejected DeleteIf")
	}

	if !c.DeleteIf("k", func(v any) bool { return v == "owner-A" }) {
		t.Fatal("pred=true must delete")
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("entry must be gone after DeleteIf")
	}

	if c.DeleteIf("missing", func(any) bool { return true }) {
		t.Fatal("missing key must report false")
	}
}

func TestMapCache_DeleteIf_ExpiredEntry(t *testing.T) {
	c := NewMapCache()
	c.Set("k", "v", WithTTL(time.Nanosecond))
	time.Sleep(5 * time.Millisecond)
	if c.DeleteIf("k", func(any) bool { return true }) {
		t.Fatal("an expired entry must not count as a live delete")
	}
}

func TestCacheOnExpiry_NotFiredForDeadEntry(t *testing.T) {
	for _, tt := range []struct {
		name string
		kill func(c *MapCache)
		want any
	}{
		{
			name: "superseded by a replacement",
			kill: func(c *MapCache) { c.entries["k"] = &cacheEntry{value: "second"} },
			want: "second",
		},
		{
			name: "removed by the caller",
			kill: func(c *MapCache) { delete(c.entries, "k") },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := NewMapCache()
			t.Cleanup(c.Clear)

			fired := make(chan any, 4)
			c.Set("k", "first", WithTTL(20*time.Millisecond), WithOnExpiry(func(_ string, v any) {
				fired <- v
			}))

			c.mu.Lock()
			time.Sleep(80 * time.Millisecond)
			tt.kill(c)
			c.mu.Unlock()

			select {
			case v := <-fired:
				t.Errorf("expiry fired for %v after the entry was no longer live", v)
			case <-time.After(150 * time.Millisecond):
			}
			got, ok := c.Get("k")
			if tt.want == nil {
				if ok {
					t.Errorf("entry = %v, want the key gone", got)
				}
				return
			}
			if !ok || got != tt.want {
				t.Errorf("live entry = %v (present %v), want %v — the stale timer must not evict it", got, ok, tt.want)
			}
		})
	}
}

func TestMapCache_Swap_NoDisplacedValueGoesUnseen(t *testing.T) {
	c := NewMapCache()
	t.Cleanup(c.Clear)

	const writers = 64
	var mu sync.Mutex
	displaced := map[int]int{}

	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(writers)
	for i := range writers {
		go func() {
			defer done.Done()
			start.Wait()
			if prev, had := c.Swap("k", i); had {
				mu.Lock()
				displaced[prev.(int)]++
				mu.Unlock()
			}
		}()
	}
	start.Done()
	done.Wait()

	final, ok := c.Get("k")
	if !ok {
		t.Fatal("a value must remain after the writers finish")
	}
	for v, n := range displaced {
		if n != 1 {
			t.Errorf("value %d reported displaced %d times, want 1", v, n)
		}
		if v == final.(int) {
			t.Errorf("the surviving value %d must not also be reported displaced", v)
		}
	}
	if got := len(displaced); got != writers-1 {
		t.Errorf("distinct displaced values = %d, want %d (every writer but the survivor)", got, writers-1)
	}
}

func TestMapCache_Swap_ReportsPredecessorPastItsDeadline(t *testing.T) {
	c := NewMapCache()
	t.Cleanup(c.Clear)

	expired := make(chan any, 4)
	c.Set("k", "old", WithTTL(time.Hour), WithOnExpiry(func(_ string, v any) {
		expired <- v
	}))

	c.mu.Lock()
	live := c.entries["k"]
	live.timer.Stop()
	live.expiresAt = time.Now().Add(-time.Second)
	c.mu.Unlock()

	prev, had := c.Swap("k", "new")

	if !had || prev != "old" {
		t.Errorf("Swap returned (%v, %v), want (\"old\", true) — a predecessor whose timer Swap just stopped must not be dropped", prev, had)
	}
	select {
	case v := <-expired:
		t.Errorf("onExpiry also fired for %v — it must be wound down once, by its displacer", v)
	case <-time.After(120 * time.Millisecond):
	}
	if v, ok := c.Get("k"); !ok || v != "new" {
		t.Errorf("live entry = %v (present %v), want \"new\"", v, ok)
	}
}

func TestMapCache_Swap_AbsentKeyDisplacesNothing(t *testing.T) {
	c := NewMapCache()
	t.Cleanup(c.Clear)

	if prev, had := c.Swap("k", "first"); had {
		t.Errorf("Swap over an absent key returned %v, want nothing displaced", prev)
	}
	c.Set("k2", "old", WithTTL(20*time.Millisecond))
	time.Sleep(120 * time.Millisecond)
	if prev, had := c.Swap("k2", "new"); had {
		t.Errorf("Swap after the timer reaped the entry returned %v, want nothing displaced", prev)
	}
}
