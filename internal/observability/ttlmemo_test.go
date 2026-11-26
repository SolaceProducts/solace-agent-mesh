// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTTLMemo_CachesWithinTTL(t *testing.T) {
	var calls int
	m := NewTTLMemo(time.Hour, func() (int, error) {
		calls++
		return calls, nil
	})

	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }

	v1, _ := m.Value()
	now = now.Add(30 * time.Minute)
	v2, _ := m.Value()

	if calls != 1 {
		t.Fatalf("compute called %d times within ttl, want 1", calls)
	}
	if v1 != 1 || v2 != 1 {
		t.Fatalf("values = %d,%d, want cached 1,1", v1, v2)
	}
}

func TestTTLMemo_RecomputesAfterTTL(t *testing.T) {
	var calls int
	m := NewTTLMemo(time.Hour, func() (int, error) {
		calls++
		return calls, nil
	})

	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }

	v1, _ := m.Value()
	now = now.Add(2 * time.Hour)
	v2, _ := m.Value()

	if calls != 2 {
		t.Fatalf("compute called %d times across ttl boundary, want 2", calls)
	}
	if v1 != 1 || v2 != 2 {
		t.Fatalf("values = %d,%d, want 1 then recomputed 2", v1, v2)
	}
}

func TestTTLMemo_ZeroTTLAlwaysRecomputes(t *testing.T) {
	var calls int
	m := NewTTLMemo(0, func() (int, error) {
		calls++
		return calls, nil
	})

	_, _ = m.Value()
	_, _ = m.Value()
	if calls != 2 {
		t.Fatalf("compute called %d times with zero ttl, want 2 (no caching)", calls)
	}
}

func TestTTLMemo_PropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	m := NewTTLMemo(time.Hour, func() (int, error) {
		return 0, sentinel
	})
	if _, err := m.Value(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestTTLMemo_DoesNotCacheFailure(t *testing.T) {
	var calls int
	fail := true
	m := NewTTLMemo(time.Hour, func() (int, error) {
		calls++
		if fail {
			return 0, errors.New("boom")
		}
		return 42, nil
	})
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }

	if _, err := m.Value(); err == nil {
		t.Fatal("first: want error")
	}
	if _, err := m.Value(); err == nil {
		t.Fatal("second: want error")
	}
	if calls != 2 {
		t.Fatalf("compute called %d times, want 2 (failures must not be cached)", calls)
	}

	fail = false
	if v, err := m.Value(); err != nil || v != 42 {
		t.Fatalf("success: got %d,%v want 42,nil", v, err)
	}
	if v, _ := m.Value(); v != 42 || calls != 3 {
		t.Fatalf("within ttl should serve cached: v=%d calls=%d want 42,3", v, calls)
	}

	fail = true
	now = now.Add(2 * time.Hour)
	v, err := m.Value()
	if err == nil {
		t.Fatal("refresh failure: want error")
	}
	if v != 42 {
		t.Errorf("refresh failure should serve stale 42, got %d", v)
	}
}

func TestTTLMemo_ConcurrentValue(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	m := NewTTLMemo(time.Hour, func() (int, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return calls, nil
	})

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { _, _ = m.Value() })
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls < 1 {
		t.Fatalf("compute never ran")
	}
}
