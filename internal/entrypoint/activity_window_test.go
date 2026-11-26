// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"
	"time"
)

func TestResolveActivityWindow_CatalogWinsOverAbsolute(t *testing.T) {
	now := time.Date(2026, 7, 15, 17, 6, 59, 0, time.UTC)
	nowMs := now.UnixMilli()
	staleStart := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC).UnixMilli()
	staleEnd := time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC).UnixMilli()

	start, end, err := resolveActivityWindow("24h", staleStart, staleEnd, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if end != nowMs {
		t.Errorf("end = %d, want %d (now); window catalog must ignore stale endDate", end, nowMs)
	}
	if want := nowMs - 24*time.Hour.Milliseconds(); start != want {
		t.Errorf("start = %d, want %d (now - 24h)", start, want)
	}
}

func TestResolveActivityWindow_ClampsFutureEnd(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	future := now.Add(48 * time.Hour).UnixMilli()
	start := now.Add(-24 * time.Hour).UnixMilli()

	_, end, err := resolveActivityWindow("", start, future, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if end != now.UnixMilli() {
		t.Errorf("end = %d, want %d (clamped to now)", end, now.UnixMilli())
	}
}

func TestResolveActivityWindow_AbsoluteDatesPreserved(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC).UnixMilli()

	gotStart, gotEnd, err := resolveActivityWindow("", start, end, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotStart != start || gotEnd != end {
		t.Errorf("absolute range mangled: got start=%d end=%d, want start=%d end=%d", gotStart, gotEnd, start, end)
	}
}

func TestResolveActivityWindow_ArbitraryDuration(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	nowMs := now.UnixMilli()

	cases := []struct {
		in      string
		wantDur time.Duration
	}{
		{"3h", 3 * time.Hour},
		{"45m", 45 * time.Minute},
		{"2h30m", 2*time.Hour + 30*time.Minute},
		{"7d", 7 * 24 * time.Hour},
		{"90d", 90 * 24 * time.Hour},
	}
	for _, c := range cases {
		start, end, err := resolveActivityWindow(c.in, 0, 0, now)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if end != nowMs {
			t.Errorf("%q: end = %d, want %d", c.in, end, nowMs)
		}
		if want := nowMs - c.wantDur.Milliseconds(); start != want {
			t.Errorf("%q: start = %d, want %d (now - %s)", c.in, start, want, c.wantDur)
		}
	}
}

func TestResolveActivityWindow_InvalidDurationIsError(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	bad := []string{"2weeks", "abc", "-1h", "0", "91d"}
	for _, s := range bad {
		if _, _, err := resolveActivityWindow(s, 0, 0, now); err == nil {
			t.Errorf("%q: expected error, got none", s)
		}
	}
}

func TestResolveActivityWindow_StartMissingDefaultsFromEnd(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	end := now.Add(-6 * time.Hour).UnixMilli()

	gotStart, gotEnd, err := resolveActivityWindow("", 0, end, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotEnd != end {
		t.Errorf("gotEnd = %d, want %d (unmodified)", gotEnd, end)
	}
	wantStart := end - 24*time.Hour.Milliseconds()
	if gotStart != wantStart {
		t.Errorf("gotStart = %d, want %d (end - 24h default)", gotStart, wantStart)
	}
}

func TestResolveActivityWindow_AbsoluteBeyondMaxWindowRejected(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	start := now.Add(-91 * 24 * time.Hour).UnixMilli()
	end := now.UnixMilli()

	_, _, err := resolveActivityWindow("", start, end, now)
	if err == nil {
		t.Fatal("91-day absolute range must error, got nil")
	}
}

func TestResolveActivityWindow_InvertedRangeRejected(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	start := now.Add(-1 * time.Hour).UnixMilli()
	end := now.Add(-24 * time.Hour).UnixMilli()
	if _, _, err := resolveActivityWindow("", start, end, now); err == nil {
		t.Fatal("inverted range (start after end) must error, got nil")
	}
}

func TestResolveActivityWindow_DefaultWhenAllZero(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	nowMs := now.UnixMilli()

	start, end, err := resolveActivityWindow("", 0, 0, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if end != nowMs {
		t.Errorf("end = %d, want %d (now)", end, nowMs)
	}
	if want := nowMs - 24*time.Hour.Milliseconds(); start != want {
		t.Errorf("start = %d, want %d (now - 24h default)", start, want)
	}
}
