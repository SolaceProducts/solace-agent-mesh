// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLastUsedThrottle_FirstWriteIsAlwaysDue(t *testing.T) {
	t.Parallel()
	throttle := newLastUsedThrottle(5*time.Minute, 24*time.Hour)
	if !throttle.due("key-1", 1_000) {
		t.Error("a key never stamped before is not due")
	}
}

func TestLastUsedThrottle_CoalescesWithinTheWindow(t *testing.T) {
	t.Parallel()
	const window = 5 * time.Minute
	throttle := newLastUsedThrottle(window, 24*time.Hour)
	start := int64(1_000_000)

	if !throttle.due("key-1", start) {
		t.Fatal("first call is not due")
	}
	throttle.record("key-1", start)

	for name, offset := range map[string]int64{
		"immediately":       0,
		"one second later":  1_000,
		"one ms before due": window.Milliseconds() - 1,
	} {
		if throttle.due("key-1", start+offset) {
			t.Errorf("%s: a write is due inside the window", name)
		}
	}
	if !throttle.due("key-1", start+window.Milliseconds()) {
		t.Error("a write is not due at the window boundary")
	}
}

func TestLastUsedThrottle_IsPerKey(t *testing.T) {
	t.Parallel()
	throttle := newLastUsedThrottle(5*time.Minute, 24*time.Hour)
	throttle.record("busy", 1_000)

	if throttle.due("busy", 2_000) {
		t.Error("the recorded key is due again immediately")
	}
	if !throttle.due("quiet", 2_000) {
		t.Error("a different key inherited the recorded key's clock")
	}
}

func TestLastUsedThrottle_FailedWriteIsRetriedNotSuppressed(t *testing.T) {
	t.Parallel()
	throttle := newLastUsedThrottle(5*time.Minute, 24*time.Hour)

	if !throttle.due("key-1", 1_000) {
		t.Fatal("first call is not due")
	}
	if !throttle.due("key-1", 1_001) {
		t.Error("a failed write suppressed the next attempt")
	}
}

func (t *lastUsedThrottle) held(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.lastWrite[key]
	return ok
}

func TestLastUsedThrottle_SweepDropsIdleKeysAndKeepsActiveOnes(t *testing.T) {
	t.Parallel()
	const stale = 24 * time.Hour
	throttle := newLastUsedThrottle(5*time.Minute, stale)
	start := 100 * time.Hour.Milliseconds()

	throttle.record("idle", start)
	throttle.record("active", start+stale.Milliseconds())

	throttle.due("anyone", start+stale.Milliseconds()+time.Hour.Milliseconds())

	if throttle.held("idle") {
		t.Error("an entry older than the stale age survived the sweep")
	}
	if !throttle.held("active") {
		t.Error("the sweep dropped an entry younger than the stale age, so an active key would write on every request")
	}
}

func TestLastUsedThrottle_SweepRunsAtMostOncePerInterval(t *testing.T) {
	t.Parallel()
	throttle := newLastUsedThrottle(5*time.Minute, 24*time.Hour)
	first := 100 * time.Hour.Milliseconds()
	throttle.due("anyone", first)

	throttle.mu.Lock()
	throttle.lastWrite["stale"] = first - 48*time.Hour.Milliseconds()
	throttle.mu.Unlock()

	throttle.due("anyone", first+30*time.Minute.Milliseconds())
	if !throttle.held("stale") {
		t.Fatal("a second sweep ran inside the hour")
	}
	throttle.due("anyone", first+lastUsedSweepInterval.Milliseconds()+1)
	if throttle.held("stale") {
		t.Error("no sweep ran once the hour had passed")
	}
}

func TestLastUsedThrottle_AClockThatStepsBackwardsIsDue(t *testing.T) {
	t.Parallel()
	throttle := newLastUsedThrottle(5*time.Minute, 24*time.Hour)
	now := 100 * time.Hour.Milliseconds()
	throttle.record("key-1", now)
	throttle.due("anyone", now)
	throttle.mu.Lock()
	throttle.lastWrite["stale"] = 0
	throttle.mu.Unlock()

	if !throttle.due("key-1", now-time.Hour.Milliseconds()) {
		t.Error("a key stamped in the future of the current clock is not due")
	}
	if throttle.held("stale") {
		t.Error("the sweep stayed suppressed after the clock stepped back")
	}
}

func TestLastUsedThrottle_AFailedWriteBacksOffThenRetries(t *testing.T) {
	t.Parallel()
	throttle := newLastUsedThrottle(5*time.Minute, 24*time.Hour)
	now := 100 * time.Hour.Milliseconds()

	if !throttle.due("key-1", now) {
		t.Fatal("first call is not due")
	}
	throttle.fail("key-1", now)

	if throttle.due("key-1", now+lastUsedRetryAfter.Milliseconds()-1) {
		t.Error("a failed key was retried inside the backoff")
	}
	if !throttle.due("key-1", now+lastUsedRetryAfter.Milliseconds()) {
		t.Error("a failed key was not retried once the backoff passed")
	}
	if throttle.due("other", now) != true {
		t.Error("one key's failure held back another key")
	}

	throttle.fail("key-1", now)
	throttle.record("key-1", now+1)
	if throttle.due("key-1", now+2) {
		t.Error("a success did not advance the window")
	}
	throttle.mu.Lock()
	_, stillHeld := throttle.retryAt["key-1"]
	throttle.mu.Unlock()
	if stillHeld {
		t.Error("a success left the retry hold in the map")
	}
}

func TestLastUsedThrottle_EvictionOnlyCostsOneExtraWrite(t *testing.T) {
	t.Parallel()
	throttle := newLastUsedThrottle(5*time.Minute, time.Hour)
	throttle.record("key-1", 1_000)

	farFuture := int64(1_000) + lastUsedSweepInterval.Milliseconds() + time.Hour.Milliseconds()
	if !throttle.due("key-1", farFuture) {
		t.Error("a swept key is not due for a stamp")
	}
}

func TestLastUsedThrottle_ConcurrentAccessIsCheckedByRaceDetector(t *testing.T) {
	t.Parallel()
	throttle := newLastUsedThrottle(time.Millisecond, time.Hour)

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := []string{"a", "b", "c"}[n%3]
			now := int64(n) * 10
			if throttle.due(key, now) {
				throttle.record(key, now)
			}
		}(i)
	}
	wg.Wait()
}

type stampCountingKeysRepo struct {
	calls atomic.Int64
	err   error
}

func (s *stampCountingKeysRepo) TouchLastUsed(_ context.Context, _ string, _ int64) error {
	s.calls.Add(1)
	return s.err
}

func TestAPIKeyAuthority_FailedStampDoesNotFailValidationAndBacksOff(t *testing.T) {
	ctx := t.Context()
	f := newAuthorityFixture(t, []string{"agent:*:invoke"})
	owner := f.seedSystemOwner(ctx, t, "system:ci-flaky-store")
	key := f.seedKey(ctx, t, owner.ID, "flaky-hash", []string{"agent:*:invoke"}, nil)
	stamps := &stampCountingKeysRepo{err: errors.New("database is locked")}
	f.authority.stamps = stamps
	var logs bytes.Buffer
	f.authority.logger = slog.New(slog.NewJSONHandler(&logs, nil))

	validateAt := func(at time.Time) {
		t.Helper()
		f.authority.now = func() time.Time { return at }
		if _, err := f.authority.Validate(ctx, "flaky-hash"); err != nil {
			t.Fatalf("Validate at %v: %v; a failed stamp must not refuse an authenticated key", at, err)
		}
	}

	base := time.Now()
	for i := range 5 {
		validateAt(base.Add(time.Duration(i) * time.Second))
	}
	if got := stamps.calls.Load(); got != 1 {
		t.Errorf("TouchLastUsed calls = %d, want 1: requests inside the backoff must not retry", got)
	}

	validateAt(base.Add(lastUsedRetryAfter + time.Second))
	if got := stamps.calls.Load(); got != 2 {
		t.Errorf("TouchLastUsed calls = %d, want 2: the stamp must be retried once the backoff passes", got)
	}

	var warns int
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil || rec["msg"] != "api key last-used stamp failed; retrying after a delay" {
			continue
		}
		warns++
		if rec["level"] != "WARN" || rec["credentialID"] != key.ID {
			t.Errorf("log record = %v, want a WARN naming the key as credentialID", rec)
		}
	}
	if warns != 2 {
		t.Errorf("logged %d failures for 6 requests over 2 windows, want 2 (one per window)", warns)
	}
}

func TestAPIKeyAuthority_StampsLastUsedOnlyOnSuccess(t *testing.T) {
	ctx := t.Context()

	t.Run("a successful validation stamps", func(t *testing.T) {
		f := newAuthorityFixture(t, []string{"agent:*:invoke"})
		owner := f.seedSystemOwner(ctx, t, "system:ci-stamped")
		key := f.seedKey(ctx, t, owner.ID, "stamp-hash", []string{"agent:*:invoke"}, nil)

		if stored, _ := f.keys.GetByID(ctx, key.ID); stored.LastUsedTime != nil {
			t.Fatalf("a freshly created key already carries last_used_time = %v", *stored.LastUsedTime)
		}
		if _, err := f.authority.Validate(ctx, "stamp-hash"); err != nil {
			t.Fatalf("Validate: %v", err)
		}

		stored, err := f.keys.GetByID(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if stored.LastUsedTime == nil {
			t.Fatal("last_used_time was not stamped after a successful validation")
		}
	})

	t.Run("a refused credential does not stamp", func(t *testing.T) {
		f := newAuthorityFixture(t, nil)
		owner := f.seedOwner(ctx, t, "person@example.com", "https://idp.example.com", "oidc")
		key := f.seedKey(ctx, t, owner.ID, "refused-hash", []string{"agent:*:invoke"}, nil)

		if _, err := f.authority.Validate(ctx, "refused-hash"); err == nil {
			t.Fatal("an ineligible owner's key validated")
		}

		stored, err := f.keys.GetByID(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if stored.LastUsedTime != nil {
			t.Errorf("last_used_time = %v after a refusal; the column would report a probe as use",
				*stored.LastUsedTime)
		}
	})

	t.Run("a key with no surviving scope does not stamp", func(t *testing.T) {
		f := newAuthorityFixture(t, []string{"agent:finance:invoke"})
		owner := f.seedSystemOwner(ctx, t, "system:ci-uncovered")
		key := f.seedKey(ctx, t, owner.ID, "uncovered-hash", []string{"agent_builder:_:create"}, nil)

		if _, err := f.authority.Validate(ctx, "uncovered-hash"); err == nil {
			t.Fatal("a key whose scopes the owner no longer covers validated")
		}
		stored, err := f.keys.GetByID(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if stored.LastUsedTime != nil {
			t.Errorf("last_used_time = %v after a coverage refusal", *stored.LastUsedTime)
		}
	})

	t.Run("an expired key does not stamp", func(t *testing.T) {
		f := newAuthorityFixture(t, []string{"agent:*:invoke"})
		owner := f.seedSystemOwner(ctx, t, "system:ci-expired")
		past := time.Now().Add(-time.Hour).UnixMilli()
		key := f.seedKey(ctx, t, owner.ID, "expired-hash", []string{"agent:*:invoke"}, &past)

		if _, err := f.authority.Validate(ctx, "expired-hash"); err == nil {
			t.Fatal("an expired key validated")
		}
		stored, err := f.keys.GetByID(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if stored.LastUsedTime != nil {
			t.Errorf("last_used_time = %v on an expired key", *stored.LastUsedTime)
		}
	})
}

func TestAPIKeyAuthority_RepeatedValidationsDoNotRewriteLastUsed(t *testing.T) {
	ctx := t.Context()
	f := newAuthorityFixture(t, []string{"agent:*:invoke"})
	owner := f.seedSystemOwner(ctx, t, "system:ci-polling")
	key := f.seedKey(ctx, t, owner.ID, "poll-hash", []string{"agent:*:invoke"}, nil)

	base := time.Now()
	f.authority.now = func() time.Time { return base }
	if _, err := f.authority.Validate(ctx, "poll-hash"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	first, firstErr := f.keys.GetByID(ctx, key.ID)
	if firstErr != nil {
		t.Fatalf("GetByID: %v", firstErr)
	}
	if first.LastUsedTime == nil {
		t.Fatal("last_used_time was never stamped")
	}
	stampedAt := *first.LastUsedTime
	if stampedAt != base.UnixMilli() {
		t.Fatalf("last_used_time = %d, want the authority's clock %d", stampedAt, base.UnixMilli())
	}

	for i := 1; i <= 5; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		f.authority.now = func() time.Time { return at }
		if _, err := f.authority.Validate(ctx, "poll-hash"); err != nil {
			t.Fatalf("Validate %d: %v", i, err)
		}
		stored, err := f.keys.GetByID(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetByID %d: %v", i, err)
		}
		if *stored.LastUsedTime != stampedAt {
			t.Fatalf("request %d rewrote last_used_time to %d, want it held at %d",
				i, *stored.LastUsedTime, stampedAt)
		}
	}

	past := base.Add(lastUsedWriteThrottle + time.Minute)
	f.authority.now = func() time.Time { return past }
	if _, err := f.authority.Validate(ctx, "poll-hash"); err != nil {
		t.Fatalf("Validate after the window: %v", err)
	}
	after, afterErr := f.keys.GetByID(ctx, key.ID)
	if afterErr != nil {
		t.Fatalf("GetByID: %v", afterErr)
	}
	if *after.LastUsedTime != past.UnixMilli() {
		t.Errorf("last_used_time = %d, want %d once the window elapsed",
			*after.LastUsedTime, past.UnixMilli())
	}
}
